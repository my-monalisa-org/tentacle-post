// Package inbox implements an in-memory, deterministic event delivery simulator.
package inbox

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"sync"
)

const (
	MaxEvents   = 1000
	MaxBodySize = 64 << 10
	MaxAttempts = 3
)

type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Target  string          `json:"target"`
	Payload json.RawMessage `json:"payload"`
}

type Delivery struct {
	Event    Event  `json:"event"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	LastCode int    `json:"last_code"`
}

type Inbox struct {
	mu         sync.Mutex
	deliveries []Delivery
	ids        map[string]bool
}

func New() *Inbox {
	return &Inbox{deliveries: make([]Delivery, 0), ids: make(map[string]bool)}
}

var eventID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func valid(e Event) bool {
	if !eventID.MatchString(e.ID) {
		return false
	}
	switch e.Type {
	case "push", "issues.opened", "pull_request.merged", "release.published":
	default:
		return false
	}
	switch e.Target {
	case "octocat-inbox", "sleepy-mona", "lost-in-space":
	default:
		return false
	}
	var payload map[string]json.RawMessage
	return json.Unmarshal(e.Payload, &payload) == nil && payload != nil
}

func (s *Inbox) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "purring"})
	})
	mux.HandleFunc("POST /events", s.enqueue)
	mux.HandleFunc("GET /events", s.list)
	mux.HandleFunc("GET /events/{id}", s.get)
	mux.HandleFunc("POST /deliveries/tick", s.tick)
	return mux
}

func (s *Inbox) enqueue(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var e Event
	err = decoder.Decode(&e)
	if err == nil {
		var extra any
		err = decoder.Decode(&extra)
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("multiple JSON values")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "event exceeds 64 KiB")
		} else {
			fail(w, http.StatusBadRequest, "expected exactly one JSON event with known fields")
		}
		return
	}
	if !valid(e) {
		fail(w, http.StatusUnprocessableEntity, "invalid id, event type, target, or object payload")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[e.ID] {
		fail(w, http.StatusConflict, "event id already exists")
		return
	}
	if len(s.deliveries) >= MaxEvents {
		fail(w, http.StatusServiceUnavailable, "inbox full; restart to clear demo data")
		return
	}
	d := Delivery{Event: e, State: "queued"}
	s.deliveries = append(s.deliveries, d)
	s.ids[e.ID] = true
	w.Header().Set("Location", "/events/"+e.ID)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Inbox) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.deliveries)
}

func (s *Inbox) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.deliveries {
		if d.Event.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	fail(w, http.StatusNotFound, "event not found")
}

func (s *Inbox) tick(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]Delivery, 0)
	for i := range s.deliveries {
		d := &s.deliveries[i]
		if d.State == "delivered" || d.State == "dead_letter" {
			continue
		}
		d.Attempts++
		d.LastCode = http.StatusOK
		if d.Event.Target == "lost-in-space" || (d.Event.Target == "sleepy-mona" && d.Attempts < MaxAttempts) {
			d.LastCode = http.StatusServiceUnavailable
		}
		switch {
		case d.LastCode == http.StatusOK:
			d.State = "delivered"
		case d.Attempts >= MaxAttempts:
			d.State = "dead_letter"
		default:
			d.State = "retrying"
		}
		changed = append(changed, *d)
	}
	writeJSON(w, http.StatusOK, changed)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}
