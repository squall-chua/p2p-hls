# Slice 6a — Browser Control Plane (Go) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a loopback HTTP/JSON control plane + SSE event stream to the bridge, host self-playback, and `cmd/node` wiring — everything the browser UI needs, fully Go-testable, serving a placeholder page.

**Architecture:** The existing loopback `bridge` gains `/api/*` JSON command handlers (thin adapters over `Node`), a `GET /api/events` SSE stream fed by a new in-process event hub, and static serving of an embedded SPA with token injection. Presence/request/audience changes reach the hub via small nil-defaulted callbacks added to the signaling client, catalog service, and party coordinator. `Node.Playlist`/`Segment` short-circuit `host == self` to the local media engine (bypassing the remote-access Policy). `cmd/node` constructs and starts the bridge.

**Tech Stack:** Go 1.25, `net/http` (stdlib SSE via `http.Flusher`), `gorilla/websocket` (existing), `go:embed`. Companion plan **Slice 6b** builds the Nuxt SPA on top.

**Reference:** spec `docs/superpowers/specs/2026-06-08-slice-6-web-ui-design.md`; ADR `docs/adr/0006-browser-ui-control-plane.md`. Commit style: short point-form, **no** `Co-Authored-By` footer. Work on branch `slice-6-web-ui` (already checked out).

---

## Shared types (defined in Task 2, referenced throughout)

These bridge-local DTOs are the JSON contract. `Node` maps its internal types to them.

```go
// internal/bridge/api.go
type SelfView struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"`
}
type PeerView struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"`
	Online      bool   `json:"online"`
}
type TitleView struct {
	ContentID    string `json:"contentId"`
	DisplayTitle string `json:"displayTitle"`
	DurationMs   int64  `json:"durationMs"`
	PartyLive    bool   `json:"partyLive"`
	PartyViewers int    `json:"partyViewers"`
}

// Control is the set of Node operations the API exposes. Bridge depends on this
// interface, not on *app.Node, so handlers unit-test against a fake.
type Control interface {
	Self() SelfView
	Presence() []PeerView
	Library() ([]TitleView, error)
	Catalog(ctx context.Context, peer string) ([]TitleView, error) // ErrDenied -> 403
	RequestAccess(ctx context.Context, peer, message string) error
	PendingRequests() []string
	Approve(peer string) error
	StartParty(contentID string) string
	JoinParty(ctx context.Context, host, contentID string) error
	LeaveParty()
	EndParty(reason string)
}
```

---

## Task 1: Event hub

**Files:**
- Create: `internal/app/events.go`
- Test: `internal/app/events_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/app/events_test.go
package app

import (
	"testing"
	"time"
)

func TestHubFanOutToSubscribers(t *testing.T) {
	h := newHub()
	a := h.subscribe()
	b := h.subscribe()
	h.publish(Event{Type: "presence", Data: map[string]any{"n": 1}})

	for _, ch := range []<-chan Event{a.C, b.C} {
		select {
		case e := <-ch:
			if e.Type != "presence" {
				t.Fatalf("got %q", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestHubDropsSlowSubscriberWithoutBlocking(t *testing.T) {
	h := newHub()
	s := h.subscribe() // never drained; buffer is small
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.publish(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	h.unsubscribe(s)
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := newHub()
	s := h.subscribe()
	h.unsubscribe(s)
	h.publish(Event{Type: "x"})
	select {
	case _, ok := <-s.C:
		if ok {
			t.Fatal("received after unsubscribe")
		}
	case <-time.After(100 * time.Millisecond):
		// acceptable: closed channel or no delivery
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestHub -v`
Expected: FAIL — `newHub` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/app/events.go
package app

import "sync"

// Event is a control-plane notification fanned out to SSE subscribers.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// subscription is one SSE client's delivery channel.
type subscription struct {
	C  chan Event
	ch chan Event // same channel, writable side
}

// hub is an in-process fan-out. Publishers never block on a slow subscriber:
// a full subscriber buffer drops the event for that subscriber only.
type hub struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

func newHub() *hub { return &hub{subs: map[*subscription]struct{}{}} }

func (h *hub) subscribe() *subscription {
	ch := make(chan Event, 32)
	s := &subscription{C: ch, ch: ch}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *hub) unsubscribe(s *subscription) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
	h.mu.Unlock()
}

func (h *hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.ch <- e:
		default: // slow subscriber: drop this event for them
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestHub -race -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/app/events.go internal/app/events_test.go
git commit -m "app: in-process event hub for SSE fan-out"
```

---

## Task 2: Control interface + `/api/self` handler

**Files:**
- Create: `internal/bridge/api.go`
- Modify: `internal/bridge/bridge.go` (add `control` field, `SetControl`, register `/api/` in `Start`)
- Test: `internal/bridge/api_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bridge/api_test.go
package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

// fakeControl implements bridge.Control for handler tests.
type fakeControl struct {
	self     bridge.SelfView
	presence []bridge.PeerView
	library  []bridge.TitleView
	catalog  []bridge.TitleView
	catErr   error
	pending  []string
	approved []string
	reqMsg   string
	started  string
	joined   [2]string
	left     bool
	ended    string
}

func (f *fakeControl) Self() bridge.SelfView        { return f.self }
func (f *fakeControl) Presence() []bridge.PeerView  { return f.presence }
func (f *fakeControl) Library() ([]bridge.TitleView, error) { return f.library, nil }
func (f *fakeControl) Catalog(_ context.Context, _ string) ([]bridge.TitleView, error) {
	return f.catalog, f.catErr
}
func (f *fakeControl) RequestAccess(_ context.Context, _ , msg string) error { f.reqMsg = msg; return nil }
func (f *fakeControl) PendingRequests() []string { return f.pending }
func (f *fakeControl) Approve(p string) error    { f.approved = append(f.approved, p); return nil }
func (f *fakeControl) StartParty(cid string) string { f.started = cid; return "pid:" + cid }
func (f *fakeControl) JoinParty(_ context.Context, host, cid string) error { f.joined = [2]string{host, cid}; return nil }
func (f *fakeControl) LeaveParty()             { f.left = true }
func (f *fakeControl) EndParty(reason string)  { f.ended = reason }

func newTestBridge(t *testing.T, c bridge.Control) (*bridge.Bridge, string) {
	t.Helper()
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetControl(c)
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, b.BaseURL()
}

// apiGET issues an authenticated GET against the control API.
func apiGET(t *testing.T, base, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPISelf(t *testing.T) {
	c := &fakeControl{self: bridge.SelfView{NodeID: "n1", DisplayName: "Alice"}}
	_, base := newTestBridge(t, c)

	resp := apiGET(t, base, "/api/self")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got bridge.SelfView
	json.NewDecoder(resp.Body).Decode(&got)
	if got.DisplayName != "Alice" {
		t.Fatalf("got %+v", got)
	}
}

func TestAPIRejectsMissingToken(t *testing.T) {
	_, base := newTestBridge(t, &fakeControl{})
	resp, _ := http.Get(base + "/api/self") // no Authorization header
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}
```

Note: `fakeStreamer` already exists in `internal/bridge/bridge_test.go` — reuse it.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestAPI -v`
Expected: FAIL — `SetControl`/`Control`/`SelfView` undefined.

- [ ] **Step 3: Write the implementation**

Add to `internal/bridge/bridge.go` the `control` field and registration. In the `Bridge` struct add:

```go
	control Control
```

In `Start`, after `mux.HandleFunc("/party/", b.handleParty)`, add:

```go
	mux.HandleFunc("/api/", b.handleAPI)
```

Create `internal/bridge/api.go`:

```go
// internal/bridge/api.go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/squall-chua/p2p-hls/internal/peer"
)

// (paste the SelfView / PeerView / TitleView / Control block from "Shared types" here)

// SetControl installs the Node adapter that backs the /api/* command handlers.
func (b *Bridge) SetControl(c Control) {
	b.mu.Lock()
	b.control = c
	b.mu.Unlock()
}

// apiAuthOK checks the bearer token (Authorization header) and origin.
func (b *Bridge) apiAuthOK(r *http.Request) bool {
	if !b.originOK(r) {
		return false
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok == b.token
}

// writeJSON encodes v as JSON with status 200.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// statusForErr maps domain errors to HTTP status codes.
func statusForErr(err error) int {
	switch {
	case errors.Is(err, peer.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, peer.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

func (b *Bridge) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !b.apiAuthOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	c := b.control
	b.mu.Unlock()
	if c == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "self" && r.Method == http.MethodGet:
		writeJSON(w, c.Self())
	default:
		http.NotFound(w, r)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run TestAPI -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/api.go internal/bridge/bridge.go internal/bridge/api_test.go
git commit -m "bridge: control API skeleton + /api/self (bearer-token gated)"
```

---

## Task 3: Read endpoints — presence, library, requests

**Files:**
- Modify: `internal/bridge/api.go` (extend `handleAPI` switch)
- Test: `internal/bridge/api_test.go` (add cases)

- [ ] **Step 1: Write the failing test**

```go
// add to internal/bridge/api_test.go
func TestAPIPresenceLibraryRequests(t *testing.T) {
	c := &fakeControl{
		presence: []bridge.PeerView{{NodeID: "n2", DisplayName: "Bob", Online: true}},
		library:  []bridge.TitleView{{ContentID: "cid1", DisplayTitle: "Movie"}},
		pending:  []string{"n3"},
	}
	_, base := newTestBridge(t, c)

	var peers []bridge.PeerView
	resp := apiGET(t, base, "/api/presence")
	json.NewDecoder(resp.Body).Decode(&peers)
	if len(peers) != 1 || peers[0].DisplayName != "Bob" {
		t.Fatalf("presence %+v", peers)
	}

	var lib []bridge.TitleView
	json.NewDecoder(apiGET(t, base, "/api/library").Body).Decode(&lib)
	if len(lib) != 1 || lib[0].ContentID != "cid1" {
		t.Fatalf("library %+v", lib)
	}

	var reqs []string
	json.NewDecoder(apiGET(t, base, "/api/requests").Body).Decode(&reqs)
	if len(reqs) != 1 || reqs[0] != "n3" {
		t.Fatalf("requests %+v", reqs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestAPIPresenceLibraryRequests -v`
Expected: FAIL — 404 (routes not handled).

- [ ] **Step 3: Add the routes**

In `handleAPI`'s switch, before `default:`:

```go
	case path == "presence" && r.Method == http.MethodGet:
		writeJSON(w, c.Presence())
	case path == "library" && r.Method == http.MethodGet:
		lib, err := c.Library()
		if err != nil {
			http.Error(w, err.Error(), statusForErr(err))
			return
		}
		writeJSON(w, lib)
	case path == "requests" && r.Method == http.MethodGet:
		writeJSON(w, c.PendingRequests())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run TestAPIPresenceLibraryRequests -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/api.go internal/bridge/api_test.go
git commit -m "bridge: /api/presence, /api/library, /api/requests"
```

---

## Task 4: Peer catalog (403 on deny) + request-access + approve

**Files:**
- Modify: `internal/bridge/api.go`
- Test: `internal/bridge/api_test.go`

- [ ] **Step 1: Write the failing test**

```go
// add to internal/bridge/api_test.go
import "github.com/squall-chua/p2p-hls/internal/peer" // add to imports

func apiPOST(t *testing.T, base, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPICatalogDeniedIs403(t *testing.T) {
	c := &fakeControl{catErr: peer.ErrDenied}
	_, base := newTestBridge(t, c)
	resp := apiGET(t, base, "/api/peers/n9/catalog")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}

func TestAPIRequestAccessAndApprove(t *testing.T) {
	c := &fakeControl{}
	_, base := newTestBridge(t, c)

	if r := apiPOST(t, base, "/api/peers/n9/request-access", `{"message":"please"}`); r.StatusCode != http.StatusAccepted {
		t.Fatalf("request-access status %d", r.StatusCode)
	}
	if c.reqMsg != "please" {
		t.Fatalf("message not passed: %q", c.reqMsg)
	}
	if r := apiPOST(t, base, "/api/requests/n3/approve", ""); r.StatusCode != 200 {
		t.Fatalf("approve status %d", r.StatusCode)
	}
	if len(c.approved) != 1 || c.approved[0] != "n3" {
		t.Fatalf("approved %+v", c.approved)
	}
}
```

Add `"strings"` to the test imports if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run 'TestAPICatalog|TestAPIRequestAccess' -v`
Expected: FAIL — routes 404.

- [ ] **Step 3: Add the routes**

These paths have a `{id}` segment. Add a helper and cases. In `api.go`:

```go
// segAfter returns the path segment after prefix, and the trailing action (or "").
// e.g. peers/n9/catalog -> ("n9", "catalog").
func splitPeerPath(path, prefix string) (id, action string, ok bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}
```

In the `handleAPI` switch, before `default:`:

```go
	case strings.HasPrefix(path, "peers/"):
		id, action, _ := splitPeerPath(path, "peers/")
		switch {
		case action == "catalog" && r.Method == http.MethodGet:
			cat, err := c.Catalog(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			writeJSON(w, cat)
		case action == "request-access" && r.Method == http.MethodPost:
			var body struct{ Message string `json:"message"` }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if err := c.RequestAccess(r.Context(), id, body.Message); err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	case strings.HasPrefix(path, "requests/") && r.Method == http.MethodPost:
		id, action, _ := splitPeerPath(path, "requests/")
		if action != "approve" {
			http.NotFound(w, r)
			return
		}
		if err := c.Approve(id); err != nil {
			http.Error(w, err.Error(), statusForErr(err))
			return
		}
		w.WriteHeader(http.StatusOK)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run 'TestAPICatalog|TestAPIRequestAccess' -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/api.go internal/bridge/api_test.go
git commit -m "bridge: peer catalog (403 on deny), request-access, approve"
```

---

## Task 5: Party endpoints — start / join / leave / end

**Files:**
- Modify: `internal/bridge/api.go`
- Test: `internal/bridge/api_test.go`

- [ ] **Step 1: Write the failing test**

```go
// add to internal/bridge/api_test.go
func TestAPIPartyEndpoints(t *testing.T) {
	c := &fakeControl{}
	_, base := newTestBridge(t, c)

	resp := apiPOST(t, base, "/api/party/start", `{"contentId":"cidX"}`)
	var sp struct{ PartyID string `json:"partyId"` }
	json.NewDecoder(resp.Body).Decode(&sp)
	if sp.PartyID != "pid:cidX" || c.started != "cidX" {
		t.Fatalf("start %+v / %q", sp, c.started)
	}
	if r := apiPOST(t, base, "/api/party/join", `{"hostNodeId":"h1","contentId":"cidY"}`); r.StatusCode != 200 {
		t.Fatalf("join %d", r.StatusCode)
	}
	if c.joined != [2]string{"h1", "cidY"} {
		t.Fatalf("joined %+v", c.joined)
	}
	if r := apiPOST(t, base, "/api/party/leave", ""); r.StatusCode != 200 || !c.left {
		t.Fatalf("leave %d %v", r.StatusCode, c.left)
	}
	if r := apiPOST(t, base, "/api/party/end", ""); r.StatusCode != 200 || c.ended == "" {
		t.Fatalf("end %d %q", r.StatusCode, c.ended)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestAPIPartyEndpoints -v`
Expected: FAIL — routes 404.

- [ ] **Step 3: Add the routes**

In the `handleAPI` switch, before `default:`:

```go
	case strings.HasPrefix(path, "party/") && r.Method == http.MethodPost:
		switch strings.TrimPrefix(path, "party/") {
		case "start":
			var body struct{ ContentID string `json:"contentId"` }
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeJSON(w, map[string]string{"partyId": c.StartParty(body.ContentID)})
		case "join":
			var body struct {
				HostNodeID string `json:"hostNodeId"`
				ContentID  string `json:"contentId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if err := c.JoinParty(r.Context(), body.HostNodeID, body.ContentID); err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			w.WriteHeader(http.StatusOK)
		case "leave":
			c.LeaveParty()
			w.WriteHeader(http.StatusOK)
		case "end":
			c.EndParty("host ended the party")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run TestAPIPartyEndpoints -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/api.go internal/bridge/api_test.go
git commit -m "bridge: party start/join/leave/end endpoints"
```

---

## Task 6: SSE stream `GET /api/events`

**Files:**
- Create: `internal/bridge/sse.go`
- Modify: `internal/bridge/bridge.go` (add `events Subscriber` field + `SetEvents`; register `/api/events` BEFORE the generic `/api/` in `Start`)
- Test: `internal/bridge/sse_test.go`

The hub from Task 1 lives in `internal/app`. The bridge consumes it through a tiny interface so it stays decoupled and testable.

- [ ] **Step 1: Write the failing test**

```go
// internal/bridge/sse_test.go
package bridge_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

// fakeEvents is an in-test Subscriber: Subscribe returns a channel the test pushes to.
type fakeEvents struct{ ch chan string }

func (f *fakeEvents) Subscribe() (<-chan string, func()) { return f.ch, func() {} }

func TestSSEStreamsEvents(t *testing.T) {
	fe := &fakeEvents{ch: make(chan string, 1)}
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetEvents(fe)
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	req, _ := http.NewRequest(http.MethodGet, b.BaseURL()+"/api/events?token=secret-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}

	fe.ch <- `{"type":"presence"}`
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				line <- sc.Text()
				return
			}
		}
	}()
	select {
	case got := <-line:
		if !strings.Contains(got, "presence") {
			t.Fatalf("data line %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE data received")
	}
}

func TestSSERejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetEvents(&fakeEvents{ch: make(chan string)})
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })
	resp, _ := http.Get(b.BaseURL() + "/api/events?token=wrong")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestSSE -v`
Expected: FAIL — `SetEvents`/`Subscriber` undefined.

- [ ] **Step 3: Write the implementation**

In `internal/bridge/bridge.go`, add field `events Subscriber` to the struct, and in `Start` register the specific route **before** `/api/`:

```go
	mux.HandleFunc("/api/events", b.handleSSE)
	mux.HandleFunc("/api/", b.handleAPI)
```

Create `internal/bridge/sse.go`:

```go
// internal/bridge/sse.go
package bridge

import (
	"fmt"
	"net/http"
)

// Subscriber yields a stream of already-encoded JSON event strings and a cancel func.
type Subscriber interface {
	Subscribe() (<-chan string, func())
}

// SetEvents installs the event source for GET /api/events.
func (b *Bridge) SetEvents(s Subscriber) {
	b.mu.Lock()
	b.events = s
	b.mu.Unlock()
}

// handleSSE streams hub events as Server-Sent Events. EventSource cannot set
// headers, so the token rides the ?token= query.
func (b *Bridge) handleSSE(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) || r.URL.Query().Get("token") != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	src := b.events
	b.mu.Unlock()
	flusher, ok := w.(http.Flusher)
	if src == nil || !ok {
		http.Error(w, "unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := src.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run TestSSE -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/sse.go internal/bridge/bridge.go internal/bridge/sse_test.go
git commit -m "bridge: SSE /api/events (query-token gated)"
```

---

## Task 7: hub → Subscriber adapter (encode events to JSON strings)

**Files:**
- Modify: `internal/app/events.go` (add `Subscribe() (<-chan string, func())` adapter on hub)
- Test: `internal/app/events_test.go`

The bridge's `Subscriber` wants `<-chan string` (encoded JSON). The hub emits `Event`. Add an adapter so `*hub` satisfies `bridge.Subscriber`.

- [ ] **Step 1: Write the failing test**

```go
// add to internal/app/events_test.go
import "encoding/json" // add to imports
import "strings"       // add to imports

func TestHubSubscribeEncodesJSON(t *testing.T) {
	h := newHub()
	ch, cancel := h.Subscribe()
	defer cancel()
	h.publish(Event{Type: "audience", Data: map[string]any{"count": 2}})
	select {
	case s := <-ch:
		if !strings.Contains(s, `"type":"audience"`) {
			t.Fatalf("encoded %q", s)
		}
		var e Event
		if json.Unmarshal([]byte(s), &e); e.Type != "audience" {
			t.Fatalf("roundtrip %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("no encoded event")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestHubSubscribeEncodesJSON -v`
Expected: FAIL — `Subscribe` (string variant) undefined.

- [ ] **Step 3: Write the implementation**

Add to `internal/app/events.go`:

```go
import "encoding/json" // add to the import block

// Subscribe adapts the hub to bridge.Subscriber: it returns a channel of
// JSON-encoded events and a cancel func.
func (h *hub) Subscribe() (<-chan string, func()) {
	sub := h.subscribe()
	out := make(chan string, cap(sub.C))
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case <-done:
				return
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				b, err := json.Marshal(e)
				if err != nil {
					continue
				}
				select {
				case out <- string(b):
				case <-done:
					return
				}
			}
		}
	}()
	return out, func() { close(done); h.unsubscribe(sub) }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestHub -race -v`
Expected: PASS (all hub tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/events.go internal/app/events_test.go
git commit -m "app: hub Subscribe adapter (encoded JSON for SSE)"
```

---

## Task 8: Notification hooks (signaling, catalog, party) → hub

**Files:**
- Modify: `internal/signaling/client.go` (add `OnPresenceChange func()` field + setter; call it on join/leave)
- Modify: `internal/catalog/requests.go` (add `OnAdd func(node identity.NodeID)` + call in `Add`)
- Modify: `internal/app/party.go` (call an `onAudience func()` in `broadcastAudience` and `OnPartyAudience`)
- Modify: `internal/app/node.go` (own a `*hub`; wire the three callbacks to publish)
- Test: `internal/signaling/client_test.go`, `internal/catalog/requests_test.go`, `internal/app/party_test.go` (add focused cases)

These callbacks default nil (no-op) so existing tests are unaffected.

- [ ] **Step 1: Write the failing tests**

```go
// internal/catalog/requests_test.go (new or append)
package catalog

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

func TestRequestsOnAddFires(t *testing.T) {
	r := NewRequests()
	var got identity.NodeID
	r.OnAdd = func(n identity.NodeID) { got = n }
	r.Add("n7", "hi")
	if got != "n7" {
		t.Fatalf("OnAdd not fired: %q", got)
	}
}
```

```go
// internal/signaling/client_test.go (append a focused unit if a suitable harness exists;
// otherwise assert the setter+field compile and the callback type is invoked from applyPresence).
// Minimal compile-level guard:
func TestClientOnPresenceChangeSettable(t *testing.T) {
	var c Client
	c.OnPresenceChange = func() {}
	if c.OnPresenceChange == nil {
		t.Fatal("field not wired")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/catalog/ ./internal/signaling/ -run 'OnAdd|OnPresenceChange' -v`
Expected: FAIL — fields undefined.

- [ ] **Step 3: Write the implementation**

`internal/catalog/requests.go` — add the field and fire it (outside the lock):

```go
type Requests struct {
	mu      sync.Mutex
	pending map[identity.NodeID]string
	OnAdd   func(node identity.NodeID) // optional; fired after a new/updated request
}
```

In `Add`, after unlocking:

```go
func (r *Requests) Add(node identity.NodeID, message string) {
	r.mu.Lock()
	r.pending[node] = message
	cb := r.OnAdd
	r.mu.Unlock()
	if cb != nil {
		cb(node)
	}
}
```

`internal/signaling/client.go` — add `OnPresenceChange func()` to the `Client` struct, and call it (non-blocking, outside the lock) wherever `c.peers` is mutated on join/leave (the two `c.peers[...] = p` / `delete(...)` sites). Example at the snapshot/join site:

```go
	c.mu.Lock()
	c.peers[p.NodeID] = p
	cb := c.OnPresenceChange
	c.mu.Unlock()
	if cb != nil {
		cb()
	}
```

Apply the same pattern at the leave (`delete`) site.

`internal/app/party.go` — add `onAudience func()` to `partyCoordinator` and call it at the end of `broadcastAudience` and `OnPartyAudience`:

```go
	pc.mu.Lock()
	cb := pc.onAudience
	pc.mu.Unlock()
	if cb != nil {
		cb()
	}
```

`internal/app/node.go` — give `Node` a `hub *hub`, construct it in `NewNode`, and wire the callbacks. In `NewNode` after `n.party = newPartyCoordinator(...)`:

```go
	n.hub = newHub()
	n.party.onAudience = func() { n.hub.publish(Event{Type: "audience"}) }
	client.OnPresenceChange = func() { n.hub.publish(Event{Type: "presence"}) }
```

Wire the catalog hook in `SetCatalog` (after `n.catalog = svc`):

```go
	svc.Requests().OnAdd = func(identity.NodeID) { n.hub.publish(Event{Type: "request"}) }
```

(Events carry only a `type`; the SPA refetches the relevant snapshot on receipt. Keeps payloads trivial and avoids leaking internal shapes.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ ./internal/signaling/ ./internal/app/ -race`
Expected: PASS; existing tests still green (nil callbacks unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/signaling/client.go internal/catalog/requests.go internal/app/party.go internal/app/node.go internal/catalog/requests_test.go internal/signaling/client_test.go
git commit -m "wire presence/request/audience change hooks into the event hub"
```

---

## Task 9: Host self-playback (local serve, policy bypass)

**Files:**
- Modify: `internal/media/service.go` (add `LocalPlaylist`, `LocalSegment` — no policy)
- Modify: `internal/app/node.go` (`Playlist`/`Segment`: `host == self` short-circuit)
- Test: `internal/media/service_test.go`, `internal/app/node_test.go` (or a focused new test file)

- [ ] **Step 1: Write the failing test**

```go
// internal/media/service_test.go (append)
func TestLocalServeBypassesPolicy(t *testing.T) {
	// engine + a Policy that denies everyone
	eng := newTestEngine(t)          // reuse existing engine test helper
	svc := NewService(eng, catalog.NewPolicy(nil)) // denies all remotes
	// LocalPlaylist must still serve (owner sees own Library)
	_, _, _, err := svc.LocalPlaylist("cid-under-test", "index.m3u8")
	if err == peer.ErrDenied {
		t.Fatal("local serve must not be policy-gated")
	}
}
```

Adjust `newTestEngine`/`NewPolicy` to the existing helpers in `internal/media` and `internal/catalog` (inspect those packages' test files for the exact constructor used; the engine helper already exists for `TestEngineServesMasterAndSegment`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/media/ -run TestLocalServeBypassesPolicy -v`
Expected: FAIL — `LocalPlaylist` undefined.

- [ ] **Step 3: Write the implementation**

`internal/media/service.go`:

```go
// LocalPlaylist serves a playlist for the owner's own playback — no access check.
func (s *Service) LocalPlaylist(contentID, name string) ([]byte, string, bool, error) {
	data, complete, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, "", false, err
	}
	s.touch(contentID)
	return data, contentType(name), complete, nil
}

// LocalSegment serves a segment for the owner's own playback — no access check.
func (s *Service) LocalSegment(contentID, name string) ([]byte, error) {
	data, _, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, err
	}
	s.touch(contentID)
	return data, nil
}
```

`internal/app/node.go` — define a local interface and short-circuit. Near the top (after imports):

```go
// localMedia is the owner-playback subset of the media service (no access policy).
type localMedia interface {
	LocalPlaylist(contentID, name string) ([]byte, string, bool, error)
	LocalSegment(contentID, name string) ([]byte, error)
}
```

In `Node.Playlist`, before dialing the host session:

```go
	if host == n.self.NodeID() {
		if lm, ok := n.media.(localMedia); ok {
			return lm.LocalPlaylist(contentID, name)
		}
	}
```

In `Node.Segment`, before the swarm/session paths:

```go
	if host == n.self.NodeID() {
		if lm, ok := n.media.(localMedia); ok {
			data, err := lm.LocalSegment(contentID, name)
			if err != nil {
				return nil, "", err
			}
			return data, contentTypeFor(name), nil
		}
	}
```

(`n.media` is read under no lock here because `SetMedia` installs it once at startup; if the race detector flags it, guard the read with `n.mu` as the other accessors do.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/media/ ./internal/app/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/media/service.go internal/app/node.go internal/media/service_test.go
git commit -m "media+app: host self-playback serves local Library, bypassing remote policy"
```

---

## Task 10: Node implements `bridge.Control` (adapter) + `LeaveParty` + `Self`/`Library`/`Presence`

**Files:**
- Modify: `internal/app/node.go` (store `displayName`; add `Self`, `Presence`, `Library`, `Catalog`, `LeaveParty` and the small DTO mapping)
- Modify: `internal/app/party.go` (add `partyCoordinator.LeaveParty()` — send `Envelope_LeaveParty` to host, clear viewer/swarm)
- Modify: `internal/catalog/service.go` (add `Library()` — unfiltered local titles)
- Create: `internal/app/control.go` (the `bridge.Control` method set on `*Node`, returning bridge DTOs)
- Test: `internal/app/control_test.go`

`*Node` already has `Browse`, `RequestAccess`, `PendingRequests`, `ApproveAccess`, `StartParty`, `JoinParty`, `EndParty`. This task adds the remaining methods and the DTO mapping so `*Node` satisfies `bridge.Control`.

- [ ] **Step 1: Write the failing test**

```go
// internal/app/control_test.go
package app

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

// Compile-time proof that *Node satisfies bridge.Control.
var _ bridge.Control = (*Node)(nil)

func TestNodeSelfReportsDisplayName(t *testing.T) {
	// Construct a Node with a known identity + display name via the test helper
	// used elsewhere in this package (see node_test.go / e2e harness).
	n := newTestNode(t, "Alice")
	if n.Self().DisplayName != "Alice" {
		t.Fatalf("self %+v", n.Self())
	}
}
```

Use the existing in-package Node test constructor (the slice-4/5 e2e harness builds Nodes in-process; reuse that helper, or add a thin `newTestNode` if none exists, wiring a fake signaling server as those tests do).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestNodeSelf|Control' -v`
Expected: FAIL — `*Node` does not implement `bridge.Control` (missing `Self`, `Presence`, `Library`, `Catalog`, `LeaveParty`); `displayName` unset.

- [ ] **Step 3: Write the implementation**

`internal/app/node.go` — store the display name:

```go
type Node struct {
	self        *identity.Identity
	displayName string
	// ...existing fields...
}
```

In `NewNode`, set `n.displayName = displayName`.

`internal/catalog/service.go` — unfiltered local listing:

```go
// Library returns every Title in the owner's Library, annotated (no access filter).
func (s *Service) Library() ([]*peerv1.TitleMeta, error) {
	titles, err := s.store.All()
	if err != nil {
		return nil, err
	}
	out := make([]*peerv1.TitleMeta, 0, len(titles))
	for _, t := range titles {
		out = append(out, s.toMeta(t))
	}
	return out, nil
}
```

`internal/app/party.go` — viewer-initiated leave:

```go
// LeaveParty tells the current Host this Viewer is leaving, then tears down local
// viewer + swarm state. No-op if not viewing a party.
func (pc *partyCoordinator) LeaveParty() {
	pc.mu.Lock()
	v, host, ss := pc.viewer, pc.viewerHost, pc.swarm
	pid := ""
	if pc.swarm != nil {
		pid = pc.swarm.partyID
	}
	pc.viewer, pc.swarm = nil, nil
	send := pc.send
	pc.mu.Unlock()
	if v == nil {
		return
	}
	if send != nil {
		_ = send.sendTo(host, &peerv1.Envelope{
			Body: &peerv1.Envelope_LeaveParty{LeaveParty: &peerv1.LeaveParty{PartyId: pid}},
		})
	}
	if ss != nil {
		ss.close()
	}
}
```

Create `internal/app/control.go` — the adapter to bridge DTOs:

```go
// internal/app/control.go
package app

import (
	"context"

	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

func (n *Node) Self() bridge.SelfView {
	return bridge.SelfView{NodeID: string(n.self.NodeID()), DisplayName: n.displayName}
}

func (n *Node) Presence() []bridge.PeerView {
	peers := n.client.Peers()
	out := make([]bridge.PeerView, 0, len(peers))
	for _, p := range peers {
		out = append(out, bridge.PeerView{NodeID: p.NodeID, DisplayName: p.DisplayName, Online: true})
	}
	return out
}

func (n *Node) Library() ([]bridge.TitleView, error) {
	n.mu.Lock()
	svc := n.catalog
	n.mu.Unlock()
	if svc == nil {
		return nil, nil
	}
	metas, err := svc.Library()
	if err != nil {
		return nil, err
	}
	return toTitleViews(metas), nil
}

func (n *Node) Catalog(ctx context.Context, peerID string) ([]bridge.TitleView, error) {
	metas, err := n.Browse(ctx, identity.NodeID(peerID))
	if err != nil {
		return nil, err
	}
	return toTitleViews(metas), nil
}

func (n *Node) RequestAccess(ctx context.Context, peerID, message string) error {
	return n.requestAccess(ctx, identity.NodeID(peerID), message) // see note below
}

func (n *Node) PendingRequests() []string {
	ids := n.pendingRequests() // see note below
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

func (n *Node) Approve(peerID string) error { return n.approveAccess(identity.NodeID(peerID)) } // see note
func (n *Node) JoinParty2(ctx context.Context, host, cid string) error {
	return n.joinParty(ctx, identity.NodeID(host), cid) // see note
}
func (n *Node) LeaveParty() { n.party.LeaveParty() }

func toTitleViews(metas []*peerv1.TitleMeta) []bridge.TitleView {
	out := make([]bridge.TitleView, 0, len(metas))
	for _, m := range metas {
		out = append(out, bridge.TitleView{
			ContentID:    m.GetContentId(),
			DisplayTitle: m.GetDisplayTitle(),
			DurationMs:   m.GetDurationMs(),
			PartyLive:    m.GetPartyLive(),
			PartyViewers: int(m.GetPartyViewers()),
		})
	}
	return out
}
```

**Signature note:** the existing exported methods take `identity.NodeID`, but `bridge.Control` takes `string`. Pick ONE of these to avoid duplicate/mismatched methods:
- **Preferred:** change the existing exported methods (`RequestAccess`, `ApproveAccess`, `JoinParty`, `Browse`) to keep their current `identity.NodeID` signatures, and have the `control.go` adapters convert `string → identity.NodeID` and call them. To avoid a name clash (Go has no overloading), the `Control`-facing methods that need a different signature live as the interface methods; where the name+signature already matches `Control` (e.g. `StartParty(string) string`, `EndParty(string)`), no adapter is needed. For the ones that clash on parameter type (`RequestAccess`, `JoinParty`, `Approve` vs `ApproveAccess`, `Catalog` vs `Browse`, `PendingRequests` return type), the `Control` method names differ from the existing ones (`Catalog`≠`Browse`, `Approve`≠`ApproveAccess`) OR convert in place.

  Concretely, satisfy `Control` with these names: `Self`, `Presence`, `Library`, `Catalog`, `RequestAccess(ctx, string, string)`, `PendingRequests() []string`, `Approve(string)`, `StartParty(string) string`, `JoinParty(ctx, string, string)`, `LeaveParty()`, `EndParty(string)`.

  Because `RequestAccess` and `JoinParty` would clash with the existing `identity.NodeID` versions, **rename the existing internal callers' methods**: keep `Browse(ctx, identity.NodeID)` (used by streamer paths) and add `Catalog`/`Approve`/`RequestAccess(string)`/`JoinParty(string)` as the `Control` set, having them convert and call the existing `identity.NodeID` logic (extract that logic into unexported helpers `requestAccess`, `approveAccess`, `joinParty` if a name clash arises). Update `cmd/node` (Task 12) and any tests that called the old exported names accordingly.

Resolve the clash the simplest way the compiler accepts; the **public `Control` surface above is the contract** Task 11/12 depend on. Replace the `// see note` placeholders with the real converted calls during implementation.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ ./internal/catalog/ -race`
Expected: PASS; `var _ bridge.Control = (*Node)(nil)` compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/app/control.go internal/app/node.go internal/app/party.go internal/catalog/service.go internal/app/control_test.go
git commit -m "app: Node implements bridge.Control (+ viewer LeaveParty, Library, Self)"
```

---

## Task 11: Static SPA serving + token injection (placeholder bundle)

**Files:**
- Create: `webui/dist/index.html` (committed placeholder; replaced by the real build in Slice 6b)
- Create: `internal/bridge/static.go` (embed + serve + inject)
- Modify: `internal/bridge/bridge.go` (register `/` LAST in `Start`; add `selfNodeID`/`selfName` for injection via a small setter)
- Test: `internal/bridge/static_test.go`

- [ ] **Step 1: Create the placeholder bundle**

`webui/dist/index.html`:

```html
<!doctype html>
<html>
  <head><meta charset="utf-8"><title>p2p-hls</title></head>
  <body>
    <div id="app">control plane up — UI bundle not built yet (run make webui)</div>
    <!--__P2P_BOOTSTRAP__-->
  </body>
</html>
```

- [ ] **Step 2: Write the failing test**

```go
// internal/bridge/static_test.go
package bridge_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

func TestServesSPAWithInjectedToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "Alice")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })

	resp, err := http.Get(b.BaseURL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `window.__P2P__`) || !strings.Contains(s, "secret-token") || !strings.Contains(s, "Alice") {
		t.Fatalf("token not injected: %s", s)
	}
}

func TestSPAFallbackServesIndexForUnknownRoute(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "Alice")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })
	resp, _ := http.Get(b.BaseURL() + "/peer/n2") // client route, not a file
	if resp.StatusCode != 200 {
		t.Fatalf("fallback status %d", resp.StatusCode)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run 'SPA' -v`
Expected: FAIL — `SetBootstrap` undefined / `/` not served.

- [ ] **Step 4: Write the implementation**

In `internal/bridge/bridge.go`, add fields `selfNodeID, selfName string`, a setter, and register `/` LAST in `Start` (after `/s/`, `/party/`, `/api/events`, `/api/`):

```go
	mux.HandleFunc("/", b.handleStatic)
```

Create `internal/bridge/static.go`:

```go
// internal/bridge/static.go
package bridge

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var staticFS embed.FS

// SetBootstrap supplies the values injected into index.html for the SPA.
func (b *Bridge) SetBootstrap(nodeID, name string) {
	b.mu.Lock()
	b.selfNodeID, b.selfName = nodeID, name
	b.mu.Unlock()
}

// handleStatic serves the embedded SPA. Real asset files are served as-is; any
// other path falls back to index.html (client-side routing) with the token
// bootstrap injected.
func (b *Bridge) handleStatic(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	sub, _ := fs.Sub(staticFS, "dist")
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean != "" {
		if f, err := sub.Open(clean); err == nil {
			f.Close()
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
	}
	// fallback: index.html with bootstrap injected
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "no UI bundle", http.StatusInternalServerError)
		return
	}
	b.mu.Lock()
	boot := fmt.Sprintf(`<script>window.__P2P__=%s</script>`, b.bootstrapJSON())
	b.mu.Unlock()
	out := strings.Replace(string(data), "<!--__P2P_BOOTSTRAP__-->", boot, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// bootstrapJSON builds the injected window.__P2P__ object. Caller holds b.mu.
func (b *Bridge) bootstrapJSON() string {
	// token/nodeId/name are simple values; build JSON by hand to avoid importing encoding/json here.
	return fmt.Sprintf(`{"token":%q,"nodeId":%q,"name":%q}`, b.token, b.selfNodeID, b.selfName)
}
```

Note: `//go:embed all:dist` is relative to `internal/bridge/`. Either keep the placeholder at `internal/bridge/dist/index.html`, OR embed the repo-root `webui/dist`. **Decision:** to let Slice 6b's Nuxt build write to one canonical place, change the embed to a generated copy: the Makefile (`make webui`) copies `webui/dist` → `internal/bridge/dist`. For this task, create the placeholder at `internal/bridge/dist/index.html` (so the embed compiles now) and ALSO create `webui/dist/index.html` (Slice 6b's build target). Add `internal/bridge/dist/` to `.gitignore` EXCEPT a committed `index.html` placeholder — simplest: commit `internal/bridge/dist/index.html` and have `make webui` overwrite the dir.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/bridge/ -run 'SPA' -race -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bridge/static.go internal/bridge/bridge.go internal/bridge/dist/index.html internal/bridge/static_test.go
git commit -m "bridge: serve embedded SPA with token bootstrap injection (placeholder bundle)"
```

---

## Task 12: `cmd/node` — start the bridge, print + open the URL

**Files:**
- Modify: `cmd/node/main.go`
- Create: `internal/app/token.go` (random session token helper) + test

- [ ] **Step 1: Write the failing test (token helper)**

```go
// internal/app/token_test.go
package app

import "testing"

func TestNewTokenIsRandomHex(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 16 {
		t.Fatalf("weak token a=%q b=%q", a, b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestNewToken -v`
Expected: FAIL — `NewToken` undefined.

- [ ] **Step 3: Implement the token helper**

```go
// internal/app/token.go
package app

import (
	"crypto/rand"
	"encoding/hex"
)

// NewToken returns a random 32-hex-char session token for the loopback bridge.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestNewToken -v`
Expected: PASS.

- [ ] **Step 5: Wire the bridge into `cmd/node`**

Replace the `--dial` demo tail of `cmd/node/main.go` with bridge startup. Add the `--no-open` flag and these steps after `node` is created (keep `defer node.Close()`):

```go
	token := app.NewToken()
	br := bridge.New(node, token)
	br.SetControl(node)
	br.SetEvents(node.Events())          // see Step 6
	br.SetBootstrap(string(id.NodeID()), *name)
	br.SetPartyHandler(node.PartyWS())
	if err := br.Start("127.0.0.1:0"); err != nil {
		fatal(err)
	}
	defer br.Close()

	url := br.BaseURL() + "/?token=" + token // dev/manual bootstrap convenience
	fmt.Println("UI ready:", url)
	if !*noOpen {
		_ = openBrowser(url)
	}
	select {} // serve until interrupted
```

Add the flag near the others: `noOpen := flag.Bool("no-open", false, "do not open the browser")`. Add imports `"os/exec"`, `"runtime"`, and the bridge package. Add a cross-platform opener:

```go
// openBrowser opens url in the default browser (best-effort).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
```

- [ ] **Step 6: Expose the hub on Node for the bridge**

Add to `internal/app/node.go`:

```go
// Events returns the node's event source for the bridge SSE handler.
func (n *Node) Events() bridge.Subscriber { return n.hub }
```

(Confirm `*hub` satisfies `bridge.Subscriber` via its `Subscribe()` from Task 7. Add `var _ bridge.Subscriber = (*hub)(nil)` to `events.go` as a compile guard.)

- [ ] **Step 7: Build and smoke-run**

Run:
```bash
go build ./... && go vet ./...
go build -o bin/node ./cmd/node
```
Expected: builds clean. (Full manual run needs two nodes + a signaling server — deferred to the Slice 6b `verify` pass; here just confirm it compiles and `--no-open` parses.)

- [ ] **Step 8: Commit**

```bash
git add cmd/node/main.go internal/app/token.go internal/app/token_test.go internal/app/node.go internal/app/events.go
git commit -m "cmd/node: start the loopback bridge, print + open the UI URL"
```

---

## Task 13: Full Go suite green + race

**Files:** none (verification gate)

- [ ] **Step 1: Run the whole suite under the race detector**

Run: `go test -race ./...`
Expected: PASS, no races. Fix any fallout from the new callbacks (ensure no lock is held across a hub `publish`).

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Commit (if any fixes were needed)**

```bash
git add -A
git commit -m "slice-6a: green go test -race across the control plane"
```

---

## Self-review checklist (done by the plan author)

- **Spec coverage:** control API endpoints (Tasks 2–5) ✓; SSE + hub (1,6,7) ✓; notification hooks (8) ✓; host self-playback (9) ✓; Node↔Control adapter incl. viewer leave (10) ✓; static serving + token injection (11) ✓; cmd/node launch + token + open (12) ✓. **Deferred to Slice 6b:** the Nuxt SPA, the browser actuators, the additive `driftMs` wire field, Vitest/Playwright, `make webui*` targets, replacing the placeholder bundle.
- **Placeholders:** the only `// see note` markers are in Task 10's signature-clash resolution, which is spelled out — resolve at implementation time. No `TODO`/`TBD` requirements remain.
- **Type consistency:** the `Control` interface (Shared types) is used verbatim by the fake (Task 2), the handlers (2–5), and `*Node` (10); `Subscriber` is defined in Task 6 and satisfied in Task 7/12; `SelfView`/`PeerView`/`TitleView` are stable across tasks.
