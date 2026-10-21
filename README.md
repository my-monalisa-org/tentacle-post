# Tentacle Post

**Eight arms. One inbox. Zero real webhooks.**

Tentacle Post is a tiny Go HTTP event inbox and deterministic delivery simulator
for a fictional octoverse. Octocat gets mail immediately, sleepy Mona needs two
retries, and Astrocat's mailbox is lost in space. No external packages, accounts,
database, or outgoing HTTP requests are needed.

## Sort some mail

Requires Go 1.25 or newer and curl. Start the loopback-only server:

```sh
go run ./cmd/tentacle-post
```

In another terminal:

```sh
curl -sS http://127.0.0.1:8080/healthz
curl -sS -H 'Content-Type: application/json' --data-binary @examples/octocat-push.json http://127.0.0.1:8080/events
curl -sS -H 'Content-Type: application/json' --data-binary @examples/mona-release.json http://127.0.0.1:8080/events
curl -sS -H 'Content-Type: application/json' --data-binary @examples/space-issue.json http://127.0.0.1:8080/events
curl -sS -X POST http://127.0.0.1:8080/deliveries/tick
curl -sS -X POST http://127.0.0.1:8080/deliveries/tick
curl -sS -X POST http://127.0.0.1:8080/deliveries/tick
curl -sS http://127.0.0.1:8080/events
```

After three ticks, the final states are:

| Fixture / target | State | Attempts | Last simulated HTTP code |
|---|---|---|---|
| `octocat-push.json` / `octocat-inbox` | `delivered` | 1 | 200 |
| `mona-release.json` / `sleepy-mona` | `delivered` | 3 | 200 |
| `space-issue.json` / `lost-in-space` | `dead_letter` | 3 | 503 |

Every tick processes each nonterminal event once, in insertion order. There are no
background workers, real network deliveries, clocks, sleeps, or automatic retries.
Another tick returns `[]`; terminal deliveries never change. All fixtures are
explicitly fictional, not real GitHub webhook envelopes or production traffic.
`last_code` is a simulated destination response, **not** the tick endpoint's status.

Try the deliberately invalid address fixture:

```sh
curl -i -H 'Content-Type: application/json' --data-binary @examples/invalid-target.json http://127.0.0.1:8080/events
```

It returns 422 and stores nothing. Posting an existing ID returns 409, without
changing the original event. Restart the process to clear all in-memory data.
Use `-port 8081` if 8080 is occupied; the bind address remains `127.0.0.1`.

## Pocket API contract

| Method / path | Result |
|---|---|
| `GET /healthz` | 200, `{"status":"purring"}` |
| `POST /events` | 201, queued delivery and `Location: /events/{id}` |
| `GET /events` | 200, all deliveries in insertion order (empty array initially) |
| `GET /events/{id}` | 200, delivery; 404 for unknown ID |
| `POST /deliveries/tick` | 200, array of deliveries attempted by this tick |

An event has exactly these top-level fields: `id`, `type`, `target`, `payload`.
IDs match `[a-zA-Z0-9_-]{1,80}`. Types are `push`, `issues.opened`,
`pull_request.merged`, and `release.published`. Targets are the three mailbox
names above, **never URLs**. Payload must be a JSON object; nested fictional
event data is otherwise opaque.

Requests to enqueue must use `application/json` (charset parameters are allowed).
Malformed JSON, unknown top-level fields, and trailing JSON values return 400;
invalid field values return 422; oversized events return 413 (64 KiB maximum);
unsupported content type returns 415. At 1,000 events, new IDs return 503 until
restart; duplicates still return 409. Application errors have `{"error":"..."}`.
Unknown routes/methods use Go's standard 404/405 responses.

This is an unauthenticated, local teaching service, **not a production webhook
receiver**. It has no signatures, persistence, backoff, deletion, redelivery, or
exactly-once delivery guarantee. Do not expose it through a public tunnel or use
real secrets/personal data in payloads. Memory is bounded by 1,000 events and
64 KiB per enqueue request; mutex-protected transitions preserve consistency.

## Workflows with a day job

Ordinary **CI** runs formatting checks, `go vet`, race-enabled tests, and a build
on pull requests and pushes to `main`. The tests load the actual JSON fixtures,
assert the complete three-tick lifecycle and rejected events, and exercise
capacity and concurrent duplicate/tick handling:

```sh
go vet ./...
go test -race -cover ./...
go build ./cmd/tentacle-post
```

**[Reliability Lookout](.github/workflows/reliability-lookout.md)** is a real
[GitHub Agentic Workflow](https://github.github.com/gh-aw/), not a scripted fake
AI check. It runs **weekly on Mondays at a repository-scattered UTC time**, with optional `workflow_dispatch`
for a maintainer-authorized run. GitHub schedules may be delayed. Its compiled
`.lock.yml` is committed alongside the Markdown source; the schedule starts
through normal merge to the default branch, with no separate activation step.

The workflow prepares real test/vet evidence, then asks Copilot to compare the
documented API with code, tests, and fixtures. It can create **at most one**
evidence-backed issue, and does nothing if there is no actionable gap. Findings
receive the dedicated `reliability-finding` label; an open issue with that label
skips agent execution until maintainers close/resolve it. Framework billing and
detector-error notifications do not carry this label and must not suppress reviews.
Prior labeled findings and title deduplication reduce repeats. Intentional
simulated 503s are not incidents. Keep `reliability-finding` reserved for actual
Lookout findings rather than setup or framework notifications.

### Authentication, permissions, and cost

The source and lock are compiled with **`gh aw v0.92.6`**. Use that version when
editing this demo; do not hand-edit generated locks:

```sh
gh aw version
gh aw compile reliability-lookout --strict --validate
gh aw list
```

Copilot is explicit; the main model defaults to **`auto`** (no custom model
override). The generated runtime resolves `GH_AW_MODEL_AGENT_COPILOT`, then
`GH_AW_DEFAULT_MODEL_COPILOT`, then `auto`. Its separate output threat-detection
pass defaults to the `detection` alias; `GH_AW_MODEL_DETECTION_COPILOT` or the
shared default variable can override that model. Before running, an organization administrator must
permit GitHub Actions, the pinned gh-aw actions, and Copilot workflow-token
inference for this repository, with a supported model and sufficient billing
budget/quota. The workflow requests `copilot-requests: write` so the runtime can
authenticate with the short-lived `GITHUB_TOKEN`; no PAT or
`COPILOT_GITHUB_TOKEN` secret should be created for this configuration.
Availability and pricing depend on GitHub's current rollout and org policy;
compilation does not prove entitlement or available quota.

The agent has repository `contents: read` and `issues: read`; the inference
permission does not grant repository writes. A separate compiler-generated safe
output job has the issue-write permission. Agent editing is disabled, shell
commands are narrowly limited to inspection, and its external network allowlist
is empty. Trusted preparation steps run tests outside the agent firewall.
There is a 10-minute agent timeout, 12-turn cap, 100 AI Credit main-agent budget,
one-issue cap, and weekly cadence. The generated threat-detection pass has its
own 10-minute timeout and a default 400 AI Credit budget, configurable via
`GH_AW_DEFAULT_DETECTION_MAX_AI_CREDITS`. These guardrails are **not a guaranteed
monetary spending cap** and do not include runner/setup overhead. Actions runner
time and Copilot inference may be billable. Administrators should configure
platform budgets and review actual run usage before authorizing manual runs.

### Honest demo evidence

Local simulator traffic is not GitHub Actions history. After publication,
ordinary CI and authorized agentic executions generate actual run logs and
artifacts that future monitoring can consume. No workflow runs, timestamps,
costs, or telemetry are seeded or invented. This bootstrap does not manually
dispatch AI workflows, install CAO or Repo Assist, or activate a campaign;
those integrations belong to the later quickstart.
