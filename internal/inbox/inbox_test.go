package inbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, status, w.Body)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("not JSON: %v", w.Header())
	}
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../examples/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFictionalMailRoute(t *testing.T) {
	h := New().Handler()
	decode[map[string]string](t, request(h, "GET", "/healthz", ""), 200)
	empty := decode[[]Delivery](t, request(h, "GET", "/events", ""), 200)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty inbox = %#v, want []", empty)
	}
	for _, name := range []string{"octocat-push", "mona-release", "space-issue"} {
		w := request(h, "POST", "/events", fixture(t, name))
		d := decode[Delivery](t, w, 201)
		if d.State != "queued" || d.Attempts != 0 || d.LastCode != 0 {
			t.Fatalf("new delivery = %+v", d)
		}
		if w.Header().Get("Location") != "/events/"+d.Event.ID {
			t.Fatal("missing event Location")
		}
	}
	decode[map[string]string](t, request(h, "POST", "/events", fixture(t, "invalid-target")), 422)
	decode[map[string]string](t, request(h, "POST", "/events", fixture(t, "octocat-push")), 409)

	for tick := 1; tick <= 3; tick++ {
		changed := decode[[]Delivery](t, request(h, "POST", "/deliveries/tick", ""), 200)
		wantCount := 2
		if tick == 1 {
			wantCount = 3
		}
		if len(changed) != wantCount {
			t.Fatalf("tick %d changed %d events, want %d", tick, len(changed), wantCount)
		}
		all := decode[[]Delivery](t, request(h, "GET", "/events", ""), 200)
		if len(all) != 3 {
			t.Fatalf("inbox contains %d events, want 3", len(all))
		}
		for i, d := range all {
			wantState, wantAttempts, wantCode := "retrying", tick, 503
			if i == 0 {
				wantState, wantAttempts, wantCode = "delivered", 1, 200
			} else if tick == 3 && i == 1 {
				wantState, wantCode = "delivered", 200
			} else if tick == 3 {
				wantState = "dead_letter"
			}
			if d.State != wantState || d.Attempts != wantAttempts || d.LastCode != wantCode {
				t.Fatalf("tick %d event %d = %+v, want %s/%d/%d", tick, i, d, wantState, wantAttempts, wantCode)
			}
		}
	}
	if changed := decode[[]Delivery](t, request(h, "POST", "/deliveries/tick", ""), 200); changed == nil || len(changed) != 0 {
		t.Fatalf("terminal deliveries changed: %+v", changed)
	}
	d := decode[Delivery](t, request(h, "GET", "/events/demo-release-002", ""), 200)
	if d.Event.ID != "demo-release-002" || d.State != "delivered" {
		t.Fatalf("get event = %+v", d)
	}
	decode[map[string]string](t, request(h, "GET", "/events/missing", ""), 404)
}

func TestInvalidEvents(t *testing.T) {
	const good = `{"id":"kitten","type":"push","target":"octocat-inbox","payload":{}}`
	tests := []struct {
		name, body string
		status     int
	}{
		{"empty", "", 400},
		{"syntax", "{", 400},
		{"array envelope", "[]", 400},
		{"null envelope", "null", 422},
		{"missing fields", "{}", 422},
		{"unknown field", strings.Replace(good, `"id"`, `"url":"https://example.invalid","id"`, 1), 400},
		{"two objects", good + good, 400},
		{"trailing null", good + " null", 400},
		{"trailing junk", good + " xyz", 400},
		{"bad id", strings.Replace(good, "kitten", "../kitten", 1), 422},
		{"long id", strings.Replace(good, "kitten", strings.Repeat("k", 81), 1), 422},
		{"unknown type", strings.Replace(good, "push", "deployment.deleted", 1), 422},
		{"URL target", strings.Replace(good, "octocat-inbox", "http://127.0.0.1:1", 1), 422},
		{"null payload", strings.Replace(good, `"payload":{}`, `"payload":null`, 1), 422},
		{"array payload", strings.Replace(good, `"payload":{}`, `"payload":[]`, 1), 422},
		{"scalar payload", strings.Replace(good, `"payload":{}`, `"payload":1`, 1), 422},
		{"oversized", strings.Replace(good, `"payload":{}`, `"payload":{"note":"`+strings.Repeat("x", MaxBodySize)+`"}`, 1), 413},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := New().Handler()
			result := decode[map[string]string](t, request(h, "POST", "/events", tc.body), tc.status)
			if result["error"] == "" {
				t.Fatal("missing explicit error")
			}
			all := decode[[]Delivery](t, request(h, "GET", "/events", ""), 200)
			if len(all) != 0 {
				t.Fatal("invalid event was stored")
			}
		})
	}
	h := New().Handler()
	r := httptest.NewRequest("POST", "/events", strings.NewReader(good))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	decode[map[string]string](t, w, 415)
	for _, eventType := range []string{"push", "issues.opened", "pull_request.merged", "release.published"} {
		h := New().Handler()
		decode[Delivery](t, request(h, "POST", "/events", strings.Replace(good, "push", eventType, 1)), 201)
	}
	if w := request(h, "DELETE", "/events", ""); w.Code != 405 {
		t.Fatalf("DELETE status = %d, want 405", w.Code)
	}
}

func TestCapacityAndDuplicateAtCapacity(t *testing.T) {
	s := New()
	h := s.Handler()
	for i := 0; i < MaxEvents; i++ {
		body := fmt.Sprintf(`{"id":"mail-%d","type":"push","target":"octocat-inbox","payload":{}}`, i)
		decode[Delivery](t, request(h, "POST", "/events", body), 201)
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"mail-0", 409}, {"overflow", 503}} {
		body := fmt.Sprintf(`{"id":%q,"type":"push","target":"octocat-inbox","payload":{}}`, tc.id)
		decode[map[string]string](t, request(h, "POST", "/events", body), tc.status)
	}
}

func TestDuplicatePreservesOriginalAndJSONCharset(t *testing.T) {
	h := New().Handler()
	body := fixture(t, "octocat-push")
	r := httptest.NewRequest("POST", "/events", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	original := decode[Delivery](t, w, 201)
	changed := strings.Replace(body, "octocat-inbox", "lost-in-space", 1)
	decode[map[string]string](t, request(h, "POST", "/events", changed), 409)
	stored := decode[Delivery](t, request(h, "GET", "/events/"+original.Event.ID, ""), 200)
	if stored.Event.Target != original.Event.Target || string(stored.Event.Payload) != string(original.Event.Payload) {
		t.Fatalf("duplicate mutated original event: %+v", stored)
	}
}

func TestConcurrentDuplicateAndTicks(t *testing.T) {
	h := New().Handler()
	body := fixture(t, "mona-release")
	var wg sync.WaitGroup
	statuses := make(chan int, 30)
	for i := 0; i < cap(statuses); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- request(h, "POST", "/events", body).Code
		}()
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == 201 {
			created++
		} else if status != 409 {
			t.Fatalf("unexpected concurrent status: %d", status)
		}
	}
	if created != 1 {
		t.Fatalf("created %d deliveries, want 1", created)
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request(h, "POST", "/deliveries/tick", "")
			request(h, "GET", "/events", "")
		}()
	}
	wg.Wait()
	d := decode[Delivery](t, request(h, "GET", "/events/demo-release-002", ""), 200)
	if d.Attempts != 3 || d.State != "delivered" {
		t.Fatalf("concurrent final delivery = %+v", d)
	}
}
