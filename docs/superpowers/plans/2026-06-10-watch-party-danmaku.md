# Watch Party Danmaku Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let any Watch Party participant post a short text "Danmaku" that scrolls right-to-left over the video for the whole Audience, live and ephemeral.

**Architecture:** A new `PartyDanmaku` message rides the existing peer control channel. A Viewer sends its Danmaku only to the Host; the Host validates, caps, rate-limits, stamps sender identity, and fans it out to every Audience member (the same star fan-out used by `PartyState`/`PartyAudience`). Each Node pushes received Danmaku to its loopback `/party` WebSocket; the browser renders them in a lane-managed overlay. Pure logic (length cap, rate bucket, lane allocation, message parsing) lives in clock-/input-driven helpers that are unit-tested without a network or DOM.

**Tech Stack:** Go (protobuf over WebRTC data channels, gorilla/websocket loopback, testify), Nuxt 4 / Vue 3 + TypeScript + Tailwind, vitest.

---

## Read first

- Spec: [`docs/superpowers/specs/2026-06-10-watch-party-danmaku-design.md`](./../specs/2026-06-10-watch-party-danmaku-design.md)
- ADR: [`docs/adr/0007-watch-party-danmaku.md`](./../../adr/0007-watch-party-danmaku.md)
- Glossary term **Danmaku**: [`CONTEXT.md`](./../../../CONTEXT.md)

## Invariants the code must preserve (from the spec)

- **Host is the only broadcaster.** A Viewer never sends a Danmaku to another Viewer.
- **Authoritative on the Host:** anti-spoof (drop Danmaku from a non-Audience sender), rune-length cap, per-sender rate bucket.
- **Cooldown ≥ refill, burst ≥ 1** so an honest client never has its own Danmaku dropped (it renders on the Host's echo, not optimistically).
- **Ephemeral / wall-clock:** nothing stored; overlay motion is independent of `video.currentTime`.

## File structure

**Create:**
- `internal/party/danmaku.go` — `MaxDanmakuLen`, `CapText`, `DanmakuGate` (pure, clock-driven).
- `internal/party/danmaku_test.go` — tests for the above.
- `webui/app/lib/danmaku.ts` — constants, `LaneAllocator`, `pushBounded` (pure).
- `webui/test/danmaku.spec.ts` — tests for `danmaku.ts`.
- `webui/app/components/DanmakuOverlay.vue` — the scrolling overlay.

**Modify:**
- `proto/peer/v1/peer.proto` (+ regenerated `proto/peer/v1/peer.pb.go`) — add `PartyDanmaku`.
- `internal/peer/party.go` — add `OnPartyDanmaku` to `PartyHandler`.
- `internal/peer/session.go` — dispatch the new oneof case.
- `internal/peer/party_test.go` — fake handler gains `OnPartyDanmaku` + a delivery test.
- `internal/party/host.go` — add `Host.Member` (name + membership lookup).
- `internal/party/party.go` — add danmaku tunables to `Config`/`DefaultConfig`.
- `internal/app/party.go` — coordinator broadcast/receive, sink, WS writer goroutine, danmaku read-branch.
- `internal/app/party_test.go` — coordinator + WS-seam tests.
- `webui/app/lib/actuator.ts` — `parsePartyMessage` discriminator.
- `webui/test/actuator.spec.ts` — tests for `parsePartyMessage`.
- `webui/app/lib/player.ts` — `onDanmaku` option, `sendDanmaku`, danmaku-aware `onmessage`.
- `webui/app/pages/watch/[host]/[contentId].vue` — overlay + input + cooldown.

---

## Task 1: `party.CapText` — authoritative rune-length cap

**Files:**
- Create: `internal/party/danmaku.go`
- Test: `internal/party/danmaku_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/party/danmaku_test.go`:

```go
package party_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

func TestCapTextTrimsAndDropsEmpty(t *testing.T) {
	require.Equal(t, "hi", party.CapText("  hi  "))
	require.Equal(t, "", party.CapText("   "))
	require.Equal(t, "", party.CapText(""))
}

func TestCapTextTruncatesToMaxRunes(t *testing.T) {
	out := party.CapText(strings.Repeat("a", 150))
	require.Equal(t, party.MaxDanmakuLen, utf8.RuneCountInString(out))
}

func TestCapTextIsRuneSafeForCJK(t *testing.T) {
	out := party.CapText(strings.Repeat("あ", 150))
	require.True(t, utf8.ValidString(out), "must not split a multibyte rune")
	require.Equal(t, party.MaxDanmakuLen, utf8.RuneCountInString(out))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestCapText -v`
Expected: FAIL — `undefined: party.CapText` / `party.MaxDanmakuLen`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/party/danmaku.go`:

```go
package party

import "strings"

// MaxDanmakuLen caps a Danmaku's length in runes (not bytes), so CJK/emoji are
// treated fairly and never split mid-character.
const MaxDanmakuLen = 100

// CapText trims surrounding whitespace and truncates to MaxDanmakuLen runes on a
// rune boundary. Returns "" for empty/whitespace-only input.
func CapText(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > MaxDanmakuLen {
		r = r[:MaxDanmakuLen]
	}
	return strings.TrimSpace(string(r))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/party/ -run TestCapText -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/party/danmaku.go internal/party/danmaku_test.go
git commit -m "feat: add party.CapText rune-length cap for danmaku"
```

---

## Task 2: `party.DanmakuGate` — per-sender token bucket

**Files:**
- Modify: `internal/party/party.go:24-47` (add Config fields + defaults)
- Modify: `internal/party/danmaku.go` (add the gate)
- Test: `internal/party/danmaku_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/party/danmaku_test.go`:

```go
func TestDanmakuGateBurstThenThrottleThenRefill(t *testing.T) {
	clk := newFakeClock() // defined in viewer_test.go (package party_test)
	cfg := party.DefaultConfig()
	g := party.NewDanmakuGate(cfg)
	const sender = "alice"

	// Burst of 3 is allowed immediately, the 4th is throttled.
	require.True(t, g.Allow(sender, clk.Now()))
	require.True(t, g.Allow(sender, clk.Now()))
	require.True(t, g.Allow(sender, clk.Now()))
	require.False(t, g.Allow(sender, clk.Now()))

	// One second later, exactly one token has refilled.
	clk.advance(time.Second)
	require.True(t, g.Allow(sender, clk.Now()))
	require.False(t, g.Allow(sender, clk.Now()))
}

func TestDanmakuGateIsPerSender(t *testing.T) {
	clk := newFakeClock()
	g := party.NewDanmakuGate(party.DefaultConfig())
	for i := 0; i < 3; i++ {
		require.True(t, g.Allow("alice", clk.Now()))
	}
	require.False(t, g.Allow("alice", clk.Now()))
	// A different sender has its own full bucket.
	require.True(t, g.Allow("bob", clk.Now()))
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestDanmakuGate -v`
Expected: FAIL — `undefined: party.NewDanmakuGate` / `cfg.DanmakuBurst`.

- [ ] **Step 3: Add Config fields + defaults**

In `internal/party/party.go`, add two fields to the `Config` struct (after `SeekDebounce`):

```go
	SeekDebounce      time.Duration // host scrub settle window before committing a seek
	DanmakuBurst        float64     // per-sender token-bucket capacity
	DanmakuRefillPerSec float64     // per-sender token refill rate (tokens/second)
```

In `DefaultConfig()`, add the defaults (after `SeekDebounce: ...`):

```go
		SeekDebounce:      175 * time.Millisecond,
		DanmakuBurst:        3,
		DanmakuRefillPerSec: 1, // cooldown>=refill invariant: client cooldown is ~1/s
```

- [ ] **Step 4: Implement the gate**

Append to `internal/party/danmaku.go`:

```go
import (
	"strings"
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)
```

(Replace the existing single-line `import "strings"` with this import block.) Then add:

```go
// DanmakuGate is a per-sender token bucket. Allow reports whether a sender may post
// a Danmaku at time `now`; over-budget sends are throttled. Safe for concurrent use.
type DanmakuGate struct {
	mu     sync.Mutex
	burst  float64
	refill float64 // tokens per second
	state  map[identity.NodeID]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewDanmakuGate builds a gate from Config's danmaku tunables.
func NewDanmakuGate(cfg Config) *DanmakuGate {
	return &DanmakuGate{burst: cfg.DanmakuBurst, refill: cfg.DanmakuRefillPerSec, state: map[identity.NodeID]*tokenBucket{}}
}

// Allow consumes one token for sender at `now`, refilling first. Returns false when
// the bucket is empty (the Danmaku should be dropped, not broadcast).
func (g *DanmakuGate) Allow(sender identity.NodeID, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.state[sender]
	if b == nil {
		b = &tokenBucket{tokens: g.burst, last: now}
		g.state[sender] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * g.refill
		if b.tokens > g.burst {
			b.tokens = g.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/party/ -v`
Expected: PASS (all party tests, including the new gate + CapText tests).

- [ ] **Step 6: Commit**

```bash
git add internal/party/party.go internal/party/danmaku.go internal/party/danmaku_test.go
git commit -m "feat: add party.DanmakuGate per-sender token bucket"
```

---

## Task 3: Proto + Go receive/broadcast path (Host validate + fan-out, Viewer push)

This task adds the wire message, the handler plumbing, and the coordinator's inbound/broadcast logic together so the build stays green (the `PartyHandler` interface and its two implementers change in lockstep).

**Files:**
- Modify: `proto/peer/v1/peer.proto` (+ regen `proto/peer/v1/peer.pb.go`)
- Modify: `internal/peer/party.go:17-24`
- Modify: `internal/peer/session.go` (dispatch, after the `PartyEnded` case ~line 240)
- Modify: `internal/peer/party_test.go`
- Modify: `internal/party/host.go`
- Modify: `internal/app/party.go`
- Test: `internal/app/party_test.go`

- [ ] **Step 1: Add the proto message**

In `proto/peer/v1/peer.proto`, add to the `Envelope` `oneof body` (after `GetSwarmSegment get_swarm_segment = 25;`):

```proto
    PartyDanmaku party_danmaku = 26;
```

After the `GetSwarmSegment` message definition (end of file), add:

```proto
// PartyDanmaku carries one Danmaku. A Viewer->Host send fills only `text`; the Host
// overwrites sender_* with the true remote identity before fanning out.
message PartyDanmaku {
  string party_id       = 1;
  string sender_node_id = 2; // set by the Host on fan-out; ignored on inbound
  string sender_display = 3; // set by the Host on fan-out
  string text           = 4; // sender-provided, length-capped (runes) on the Host
}
```

- [ ] **Step 2: Regenerate Go protobuf**

Run: `make proto`
Expected: no output, `proto/peer/v1/peer.pb.go` now defines `PartyDanmaku`, `Envelope_PartyDanmaku`, and `Envelope.GetPartyDanmaku()`.
Verify: `git diff --stat proto/peer/v1/peer.pb.go` shows the file changed.

- [ ] **Step 3: Write the failing peer-delivery test**

In `internal/peer/party_test.go`, (a) add a capture channel to the fake and the new method, (b) add a delivery test.

Change the `fakePartyHandler` struct + methods:

```go
type fakePartyHandler struct {
	states    chan *peerv1.PartyState
	danmaku   chan *peerv1.PartyDanmaku
	joinedCID string
}
```

Add the method (near the other `On*` no-ops):

```go
func (f *fakePartyHandler) OnPartyDanmaku(_ identity.NodeID, d *peerv1.PartyDanmaku) {
	if f.danmaku != nil {
		f.danmaku <- d
	}
}
```

Add the test:

```go
func TestPartyDanmakuDeliveredToHandler(t *testing.T) {
	a, b, _ := connectPair(t) // a=viewer, b=host session
	h := &fakePartyHandler{danmaku: make(chan *peerv1.PartyDanmaku, 1)}
	b.SetPartyHandler(h)

	require.NoError(t, a.SendControl(&peerv1.Envelope{
		Body: &peerv1.Envelope_PartyDanmaku{PartyDanmaku: &peerv1.PartyDanmaku{PartyId: "p1", Text: "lol"}},
	}))

	select {
	case d := <-h.danmaku:
		require.Equal(t, "lol", d.GetText())
	case <-time.After(5 * time.Second):
		t.Fatal("OnPartyDanmaku not invoked")
	}
}
```

- [ ] **Step 4: Run test to verify it fails (compile error)**

Run: `go test ./internal/peer/ -run TestPartyDanmaku -v`
Expected: FAIL — `OnPartyDanmaku` not in `PartyHandler` interface; `*partyCoordinator` and the fake do not both satisfy it yet.

- [ ] **Step 5: Extend the `PartyHandler` interface**

In `internal/peer/party.go`, add to the `PartyHandler` interface (after `OnPartyEnded`):

```go
	OnPartyEnded(remote identity.NodeID, e *peerv1.PartyEnded)
	OnPartyDanmaku(remote identity.NodeID, d *peerv1.PartyDanmaku)
```

- [ ] **Step 6: Dispatch the new oneof case**

In `internal/peer/session.go`, add after the `*peerv1.Envelope_PartyEnded` case:

```go
		case *peerv1.Envelope_PartyDanmaku:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyDanmaku(s.remote, body.PartyDanmaku)
			}
```

- [ ] **Step 7: Add `Host.Member` lookup**

In `internal/party/host.go`, add (after `Members()`):

```go
// Member returns a Viewer's stored display name and whether it is in the Audience.
func (h *Host) Member(node identity.NodeID) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name, ok := h.audience[node]
	return name, ok
}
```

- [ ] **Step 8: Add the coordinator fields, broadcast, sink push, and inbound handler**

In `internal/app/party.go`:

(a) Add two fields to the `partyCoordinator` struct (after `stopHB chan struct{}`):

```go
	stopHB       chan struct{}
	gate         *party.DanmakuGate
	out          chan []byte // active /party browser sink; nil when no watch page open
```

(b) In `newPartyCoordinator`, construct the gate:

```go
func newPartyCoordinator(s sender, self identity.NodeID, clk party.Clock, cfg party.Config) *partyCoordinator {
	return &partyCoordinator{send: s, self: self, clock: clk, cfg: cfg, gate: party.NewDanmakuGate(cfg)}
}
```

(c) Add the broadcast helper, the sink push, and the inbound handler (place near the other handler methods, e.g. after `OnPartyEnded`):

```go
// danmakuPush is the Node->browser message pushed over the /party WS on receipt.
type danmakuPush struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Sender string `json:"sender,omitempty"`
}

// pushDanmaku writes a received Danmaku to the local browser sink, if a watch page
// is connected. Non-blocking: drops if the writer is backed up.
func (pc *partyCoordinator) pushDanmaku(text, sender string) {
	b, _ := json.Marshal(danmakuPush{Type: "danmaku", Text: text, Sender: sender})
	pc.mu.Lock()
	out := pc.out
	pc.mu.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- b:
	default:
	}
}

// broadcastDanmaku is the single fan-out point: cap the text, then send it to every
// Audience member and push it to the local browser. No-op if the text caps to empty.
func (pc *partyCoordinator) broadcastDanmaku(h *party.Host, senderNode identity.NodeID, senderDisplay, text string) {
	text = party.CapText(text)
	if text == "" {
		return
	}
	env := &peerv1.Envelope{Body: &peerv1.Envelope_PartyDanmaku{PartyDanmaku: &peerv1.PartyDanmaku{
		PartyId:       h.PartyID(),
		SenderNodeId:  string(senderNode),
		SenderDisplay: senderDisplay,
		Text:          text,
	}}}
	if pc.send != nil {
		for _, m := range h.Members() {
			_ = pc.send.sendTo(m.NodeID, env)
		}
	}
	pc.pushDanmaku(text, senderDisplay)
}

// OnPartyDanmaku (peer.PartyHandler): Host validates + rate-limits + fans out; a
// Viewer pushes a Danmaku from its Host to the local browser, ignoring all others.
func (pc *partyCoordinator) OnPartyDanmaku(remote identity.NodeID, d *peerv1.PartyDanmaku) {
	pc.mu.Lock()
	h := pc.host
	v, vh := pc.viewer, pc.viewerHost
	pc.mu.Unlock()
	if h != nil {
		name, ok := h.Member(remote)
		if !ok {
			return // anti-spoof: not in the Audience
		}
		if !pc.gate.Allow(remote, pc.clock.Now()) {
			return // rate-limited
		}
		pc.broadcastDanmaku(h, remote, name, d.GetText())
		return
	}
	if v != nil && remote == vh {
		if text := party.CapText(d.GetText()); text != "" {
			pc.pushDanmaku(text, d.GetSenderDisplay())
		}
	}
}
```

- [ ] **Step 9: Write the failing coordinator tests**

In `internal/app/party_test.go`, add a deterministic clock, a capturing sender, and the tests. Add to the imports: `"context"`, `"sync"` (if not present).

```go
// stepClock is a frozen, manually-advanced clock for deterministic rate-gate tests.
type stepClock struct{ t time.Time }

func (c *stepClock) Now() time.Time          { return c.t }
func (c *stepClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// captureSender records every Envelope sent, per destination node.
type captureSender struct {
	mu   sync.Mutex
	sent []capturedEnv
}
type capturedEnv struct {
	to  identity.NodeID
	env *peerv1.Envelope
}

func (c *captureSender) sendTo(to identity.NodeID, env *peerv1.Envelope) error {
	c.mu.Lock()
	c.sent = append(c.sent, capturedEnv{to, env})
	c.mu.Unlock()
	return nil
}
func (c *captureSender) measureRTT(context.Context, identity.NodeID) (time.Duration, error) {
	return 0, nil
}

// danmakusTo counts PartyDanmaku envelopes addressed to `node`.
func (c *captureSender) danmakusTo(node identity.NodeID) []*peerv1.PartyDanmaku {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*peerv1.PartyDanmaku
	for _, s := range c.sent {
		if s.to == node {
			if d := s.env.GetPartyDanmaku(); d != nil {
				out = append(out, d)
			}
		}
	}
	return out
}

func TestHostBroadcastsDanmakuToAllMembers(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")
	pc.OnJoinParty("bob", "cid")
	pc.out = make(chan []byte, 4) // local sink so the host sees its own

	pc.OnPartyDanmaku("alice", &peerv1.PartyDanmaku{Text: "  hello  "})

	for _, who := range []identity.NodeID{"alice", "bob"} {
		ds := cs.danmakusTo(who)
		require.Len(t, ds, 1)
		require.Equal(t, "hello", ds[0].GetText())               // trimmed
		require.Equal(t, "alice", ds[0].GetSenderNodeId())       // Host-stamped identity
	}
	require.NotEmpty(t, pc.out) // host's own browser was pushed to
}

func TestHostDropsDanmakuFromNonAudienceSender(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	pc.OnPartyDanmaku("mallory", &peerv1.PartyDanmaku{Text: "spoof"}) // not in Audience

	require.Empty(t, cs.danmakusTo("alice"))
}

func TestHostRateLimitsDanmaku(t *testing.T) {
	cs := &captureSender{}
	clk := &stepClock{t: time.Unix(1_700_000_000, 0)} // frozen => no refill
	pc := newPartyCoordinator(cs, identity.NodeID("host"), clk, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	for i := 0; i < 5; i++ {
		pc.OnPartyDanmaku("alice", &peerv1.PartyDanmaku{Text: "x"})
	}
	require.Len(t, cs.danmakusTo("alice"), 3) // burst of 3, rest throttled
}

func TestViewerPushesDanmakuFromHostOnly(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("self"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host1"))
	pc.out = make(chan []byte, 4)

	pc.OnPartyDanmaku("host1", &peerv1.PartyDanmaku{Text: "hi", SenderDisplay: "host1"})
	require.Len(t, pc.out, 1)

	pc.OnPartyDanmaku("stranger", &peerv1.PartyDanmaku{Text: "no"}) // not our host
	require.Len(t, pc.out, 1)                                       // unchanged
}
```

- [ ] **Step 10: Run the Go tests**

Run: `go test ./internal/peer/ ./internal/party/ ./internal/app/ -run 'Danmaku|PartyDanmaku' -v`
Expected: PASS — peer delivery test + the 4 coordinator tests.

- [ ] **Step 11: Run the full Go suite (nothing else broke)**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add proto/ internal/peer/party.go internal/peer/session.go internal/peer/party_test.go internal/party/host.go internal/app/party.go internal/app/party_test.go
git commit -m "feat: host-relayed PartyDanmaku receive + fan-out path"
```

---

## Task 4: Loopback WS — originate path + single-writer goroutine + sink registration

**Files:**
- Modify: `internal/app/party.go` (`playerMsg`, `serveWS`, `serveHostWS`, `serveViewerWS`, new `handlePlayerDanmaku`)
- Test: `internal/app/party_test.go`

- [ ] **Step 1: Write the failing tests for the routing seam**

In `internal/app/party_test.go`, add:

```go
func TestHandlePlayerDanmakuHostBroadcasts(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("host"), &stepClock{t: time.Unix(1_700_000_000, 0)}, party.DefaultConfig())
	pc.StartParty("cid")
	defer pc.close()
	pc.OnJoinParty("alice", "cid")

	pc.handlePlayerDanmaku("hey")

	ds := cs.danmakusTo("alice")
	require.Len(t, ds, 1)
	require.Equal(t, "hey", ds[0].GetText())
	require.Equal(t, "host", ds[0].GetSenderNodeId()) // host originates as itself
}

func TestHandlePlayerDanmakuViewerSendsToHostOnly(t *testing.T) {
	cs := &captureSender{}
	pc := newPartyCoordinator(cs, identity.NodeID("self"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host1"))

	pc.handlePlayerDanmaku("hi there")

	all := cs.danmakusTo("host1")
	require.Len(t, all, 1)
	require.Equal(t, "hi there", all[0].GetText())
	require.Empty(t, all[0].GetSenderNodeId()) // viewer leaves identity for the Host to stamp
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/ -run TestHandlePlayerDanmaku -v`
Expected: FAIL — `pc.handlePlayerDanmaku` undefined.

- [ ] **Step 3: Implement `handlePlayerDanmaku`**

In `internal/app/party.go`, add:

```go
// handlePlayerDanmaku routes a Danmaku the local browser posted. A Host broadcasts it
// to the Audience as itself; a Viewer forwards it to its Host (which re-broadcasts).
func (pc *partyCoordinator) handlePlayerDanmaku(text string) {
	pc.mu.Lock()
	h := pc.host
	v, vHost, send := pc.viewer, pc.viewerHost, pc.send
	pid := ""
	if pc.swarm != nil {
		pid = pc.swarm.partyID
	}
	pc.mu.Unlock()
	if h != nil {
		pc.broadcastDanmaku(h, pc.self, string(pc.self), text)
		return
	}
	if v == nil || vHost == "" || send == nil {
		return
	}
	t := party.CapText(text)
	if t == "" {
		return
	}
	_ = send.sendTo(vHost, &peerv1.Envelope{Body: &peerv1.Envelope_PartyDanmaku{PartyDanmaku: &peerv1.PartyDanmaku{PartyId: pid, Text: t}}})
}
```

- [ ] **Step 4: Run the seam tests**

Run: `go test ./internal/app/ -run TestHandlePlayerDanmaku -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Add the `Text` field to `playerMsg`**

In `internal/app/party.go`, extend `playerMsg`:

```go
type playerMsg struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	PosMS   int64  `json:"posMs"`
	Playing bool   `json:"playing"`
	Text    string `json:"text,omitempty"`
}
```

- [ ] **Step 6: Rewrite `serveWS` to own a single writer goroutine + register the sink**

Replace the body of `serveWS` with:

```go
func (pc *partyCoordinator) serveWS(conn *websocket.Conn) {
	defer conn.Close()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello playerMsg
	if json.Unmarshal(raw, &hello) != nil || hello.Type != "hello" {
		return
	}

	// Single writer goroutine: the only place conn is written, so the Action ticker
	// and the async Danmaku sink never race on the socket.
	out := make(chan []byte, 32)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case b := <-out:
				if conn.WriteMessage(websocket.TextMessage, b) != nil {
					return
				}
			}
		}
	}()
	pc.mu.Lock()
	pc.out = out
	pc.mu.Unlock()
	defer func() {
		pc.mu.Lock()
		if pc.out == out {
			pc.out = nil
		}
		pc.mu.Unlock()
		close(done)
	}()

	if hello.Role == "host" {
		pc.serveHostWS(conn)
		return
	}
	pc.serveViewerWS(conn, out)
}
```

- [ ] **Step 7: Add the danmaku branch to `serveHostWS`**

In `serveHostWS`, after `if json.Unmarshal(raw, &m) != nil { continue }`, add:

```go
		if m.Type == "danmaku" {
			pc.handlePlayerDanmaku(m.Text)
			continue
		}
```

(The host loop does not write directly; host pushes go through `pc.out` via `pushDanmaku`.)

- [ ] **Step 8: Route viewer writes through `out` and add the danmaku branch**

Change `serveViewerWS`'s signature to `func (pc *partyCoordinator) serveViewerWS(conn *websocket.Conn, out chan []byte)`.

In its reader goroutine, after `if json.Unmarshal(raw, &m) == nil {`, branch on danmaku before queuing a report:

```go
			var m playerMsg
			if json.Unmarshal(raw, &m) == nil {
				if m.Type == "danmaku" {
					pc.handlePlayerDanmaku(m.Text)
					continue
				}
				select {
				case reports <- m:
				default:
				}
			}
```

In the ticker case, replace the direct `conn.WriteMessage(...)` with a non-blocking send to `out`:

```go
		case <-t.C:
			act := pc.viewerDecide(last.PosMS, last.Playing, pc.clock.Now())
			b, _ := json.Marshal(act)
			select {
			case out <- b:
			default:
			}
		}
```

(Remove the old `if conn.WriteMessage(...) != nil { return }` line; the writer goroutine now owns writes, and the reader goroutine closing `reports` ends this loop on disconnect.)

- [ ] **Step 9: Run the full Go suite**

Run: `go test ./...`
Expected: PASS (existing party WS behavior intact; new seam covered).

- [ ] **Step 10: Commit**

```bash
git add internal/app/party.go internal/app/party_test.go
git commit -m "feat: danmaku over /party WS with single-writer goroutine"
```

---

## Task 5: Front-end `danmaku.ts` — lanes + bounded queue (pure)

**Files:**
- Create: `webui/app/lib/danmaku.ts`
- Test: `webui/test/danmaku.spec.ts`

- [ ] **Step 1: Write the failing test**

Create `webui/test/danmaku.spec.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { LaneAllocator, pushBounded, LANES, LANE_GAP_MS } from '../app/lib/danmaku'

describe('LaneAllocator', () => {
  it('hands out each lane once, then reports full', () => {
    const la = new LaneAllocator()
    const seen = new Set<number>()
    for (let i = 0; i < LANES; i++) seen.add(la.allocate(0))
    expect(seen.size).toBe(LANES)
    expect(la.allocate(0)).toBe(-1) // all lanes busy at the same instant
  })

  it('frees a lane after the gap elapses', () => {
    const la = new LaneAllocator()
    for (let i = 0; i < LANES; i++) la.allocate(0)
    expect(la.allocate(LANE_GAP_MS)).toBeGreaterThanOrEqual(0)
  })
})

describe('pushBounded', () => {
  it('appends within the cap', () => {
    expect(pushBounded([1, 2], 3, 5)).toEqual([1, 2, 3])
  })
  it('drops the oldest past the cap', () => {
    expect(pushBounded([1, 2, 3], 4, 3)).toEqual([2, 3, 4])
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd webui && npx vitest run test/danmaku.spec.ts`
Expected: FAIL — cannot resolve `../app/lib/danmaku`.

- [ ] **Step 3: Implement `danmaku.ts`**

Create `webui/app/lib/danmaku.ts`:

```ts
// Pure helpers for the Danmaku overlay: lane allocation and a bounded queue.
// No DOM, no Vue — unit-tested in isolation.

export const MAX_DANMAKU_LEN = 100
export const LANES = 10
export const QUEUE_MAX = 30
export const TRAVEL_MS = 7000 // keep in sync with the CSS animation in DanmakuOverlay.vue
export const LANE_GAP_MS = 1500 // min spacing before a lane accepts the next item

// LaneAllocator hands out a free vertical lane for a new Danmaku, or -1 when every
// lane is still busy. `now` is wall-clock ms (e.g. performance.now()).
export class LaneAllocator {
  private freeAt: number[]
  constructor(private lanes = LANES, private gapMs = LANE_GAP_MS) {
    this.freeAt = new Array(lanes).fill(0)
  }
  allocate(now: number): number {
    for (let i = 0; i < this.lanes; i++) {
      if (this.freeAt[i] <= now) {
        this.freeAt[i] = now + this.gapMs
        return i
      }
    }
    return -1
  }
}

// pushBounded appends item, dropping oldest entries so the result is at most `max`.
export function pushBounded<T>(queue: T[], item: T, max = QUEUE_MAX): T[] {
  const next = queue.length >= max ? queue.slice(queue.length - max + 1) : queue.slice()
  next.push(item)
  return next
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd webui && npx vitest run test/danmaku.spec.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add webui/app/lib/danmaku.ts webui/test/danmaku.spec.ts
git commit -m "feat: pure danmaku lane allocator + bounded queue"
```

---

## Task 6: Front-end `parsePartyMessage` — discriminate Danmaku vs Action (pure)

**Files:**
- Modify: `webui/app/lib/actuator.ts`
- Test: `webui/test/actuator.spec.ts`

- [ ] **Step 1: Write the failing test**

Append to `webui/test/actuator.spec.ts`:

```ts
import { parsePartyMessage } from '../app/lib/actuator'

describe('parsePartyMessage', () => {
  it('parses a danmaku push', () => {
    const m = parsePartyMessage(JSON.stringify({ type: 'danmaku', text: 'hi', sender: 'alice' }))
    expect(m).toEqual({ kind: 'danmaku', danmaku: { text: 'hi', sender: 'alice' } })
  })
  it('parses a viewer action (no type field)', () => {
    const m = parsePartyMessage(JSON.stringify({ play: true, seek: false, seekMs: 0, rate: 1, driftMs: 12 }))
    expect(m?.kind).toBe('action')
    if (m?.kind === 'action') expect(m.action.driftMs).toBe(12)
  })
  it('returns null on malformed JSON', () => {
    expect(parsePartyMessage('not json')).toBeNull()
  })
})
```

Ensure `actuator.spec.ts` imports `describe, it, expect` from `vitest` (it already tests actuator functions; reuse the existing import line).

- [ ] **Step 2: Run to verify it fails**

Run: `cd webui && npx vitest run test/actuator.spec.ts`
Expected: FAIL — `parsePartyMessage` is not exported.

- [ ] **Step 3: Implement `parsePartyMessage`**

Append to `webui/app/lib/actuator.ts`:

```ts
export interface DanmakuMsg { text: string; sender?: string }
export type PartyDown =
  | { kind: 'action'; action: ViewerAction }
  | { kind: 'danmaku'; danmaku: DanmakuMsg }

// parsePartyMessage discriminates a Node->browser /party WS message: a `type:"danmaku"`
// push vs the existing viewer Action. Returns null on malformed input.
export function parsePartyMessage(raw: string): PartyDown | null {
  let m: unknown
  try {
    m = JSON.parse(raw)
  } catch {
    return null
  }
  if (!m || typeof m !== 'object') return null
  const obj = m as Record<string, unknown>
  if (obj.type === 'danmaku') {
    return { kind: 'danmaku', danmaku: { text: String(obj.text ?? ''), sender: obj.sender as string | undefined } }
  }
  return { kind: 'action', action: obj as unknown as ViewerAction }
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd webui && npx vitest run test/actuator.spec.ts`
Expected: PASS (existing actuator tests + 3 new).

- [ ] **Step 5: Commit**

```bash
git add webui/app/lib/actuator.ts webui/test/actuator.spec.ts
git commit -m "feat: parsePartyMessage discriminates danmaku vs action"
```

---

## Task 7: `player.ts` — `onDanmaku`, `sendDanmaku`, danmaku-aware `onmessage`

**Files:**
- Modify: `webui/app/lib/player.ts`

No unit test (this wires `WebSocket` + `<video>`); its parsing is covered by Task 6 and behavior is verified manually in Task 10. Keep the change a thin delegation to `parsePartyMessage`.

- [ ] **Step 1: Extend the options + imports**

In `webui/app/lib/player.ts`, update the import and the `opts` type:

```ts
import { hostMessageFor, planViewerActuation, parsePartyMessage, type ViewerAction } from './actuator'
```

```ts
export function attachPlayer(opts: {
  video: HTMLVideoElement
  src: string
  role: Role
  wsURL: string
  onDrift?: (driftMs: number) => void
  onDanmaku?: (d: { text: string; sender?: string }) => void
}) {
```

- [ ] **Step 2: Return a uniform handle for `solo`**

Change the solo early-return so the handle type is consistent:

```ts
  if (opts.role === 'solo') return { close: destroyHls, sendDanmaku: (_text: string) => {} }
```

- [ ] **Step 3: Add `sendDanmaku` and danmaku-aware `onmessage` for both roles**

After `ws.onopen = ...`, define the sender:

```ts
  const sendDanmaku = (text: string) => {
    if (ws.readyState === ws.OPEN) ws.send(JSON.stringify({ type: 'danmaku', text }))
  }
```

In the `host` branch, add an `onmessage` (host previously had none):

```ts
    ws.onmessage = (m) => {
      const msg = parsePartyMessage(m.data)
      if (msg?.kind === 'danmaku') opts.onDanmaku?.(msg.danmaku)
    }
```

In the `viewer` branch, replace the existing `ws.onmessage` body with a discriminating one:

```ts
    ws.onmessage = (m) => {
      const msg = parsePartyMessage(m.data)
      if (!msg) return
      if (msg.kind === 'danmaku') { opts.onDanmaku?.(msg.danmaku); return }
      const a = msg.action
      const plan = planViewerActuation(a)
      if (plan.seekTo !== null) opts.video.currentTime = plan.seekTo
      opts.video.playbackRate = plan.rate
      if (plan.play && opts.video.paused) opts.video.play().catch(() => {})
      if (!plan.play && !opts.video.paused) opts.video.pause()
      opts.onDrift?.(a.driftMs)
    }
```

- [ ] **Step 4: Return `sendDanmaku` from the non-solo handle**

Change the final return:

```ts
  return { close: () => { ws.close(); destroyHls() }, sendDanmaku }
```

- [ ] **Step 5: Typecheck**

Run: `cd webui && npx nuxt typecheck`
Expected: no errors. (If `nuxt typecheck` is unavailable, run `npx vue-tsc --noEmit`.)

- [ ] **Step 6: Commit**

```bash
git add webui/app/lib/player.ts
git commit -m "feat: player.ts sendDanmaku + danmaku onmessage routing"
```

---

## Task 8: `DanmakuOverlay.vue` — the scrolling overlay

**Files:**
- Create: `webui/app/components/DanmakuOverlay.vue`

Verified manually in Task 10 (consistent with the repo: `.vue` components have no unit tests; the pure lane logic it uses is tested in Task 5).

- [ ] **Step 1: Create the component**

Create `webui/app/components/DanmakuOverlay.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { LaneAllocator, pushBounded } from '~/lib/danmaku'

interface Flying { id: number; text: string; lane: number }

const flying = ref<Flying[]>([])
let queue: { text: string }[] = []
let nextId = 1
const lanes = new LaneAllocator()
let pumping = false

// add enqueues a Danmaku and tries to place queued items into free lanes.
function add(d: { text: string; sender?: string }) {
  queue = pushBounded(queue, { text: d.text })
  pump()
}

function pump() {
  const now = performance.now()
  const remaining: { text: string }[] = []
  for (const item of queue) {
    const lane = lanes.allocate(now)
    if (lane < 0) { remaining.push(item); continue }
    flying.value.push({ id: nextId++, text: item.text, lane })
  }
  queue = remaining
  if (queue.length && !pumping) {
    pumping = true
    setTimeout(() => { pumping = false; pump() }, 200)
  }
}

function onEnd(id: number) {
  flying.value = flying.value.filter((f) => f.id !== id)
}

defineExpose({ add })
</script>

<template>
  <div class="pointer-events-none absolute inset-0 z-10 overflow-hidden">
    <span
      v-for="f in flying"
      :key="f.id"
      class="danmaku-item"
      :style="{ top: f.lane * 6 + '%' }"
      @animationend="onEnd(f.id)"
    >{{ f.text }}</span>
  </div>
</template>

<style scoped>
.danmaku-item {
  position: absolute;
  left: 100%;
  white-space: nowrap;
  color: #fff;
  font-weight: 600;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9), 0 0 4px rgba(0, 0, 0, 0.7);
  will-change: transform;
  animation: danmaku-fly 7s linear forwards; /* keep in sync with TRAVEL_MS in danmaku.ts */
}
@keyframes danmaku-fly {
  from { transform: translateX(0); }
  to { transform: translateX(calc(-100vw - 100%)); }
}
</style>
```

- [ ] **Step 2: Typecheck**

Run: `cd webui && npx nuxt typecheck`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add webui/app/components/DanmakuOverlay.vue
git commit -m "feat: DanmakuOverlay scrolling overlay component"
```

---

## Task 9: Watch page — overlay + input pill + client cooldown

**Files:**
- Modify: `webui/app/pages/watch/[host]/[contentId].vue`

- [ ] **Step 1: Import the cap, add overlay ref + draft state + cooldown**

In the `<script setup>` block, add the import and state:

```ts
import { MAX_DANMAKU_LEN } from '~/lib/danmaku'
```

```ts
const overlay = ref<{ add: (d: { text: string; sender?: string }) => void }>()
const draft = ref('')
let lastSent = 0

function sendDanmaku() {
  const text = draft.value.trim()
  if (!text) return
  const now = performance.now()
  if (now - lastSent < 1000) return // client cooldown ~1/s (>= host bucket refill)
  lastSent = now
  handle?.sendDanmaku(text)
  draft.value = ''
}
```

- [ ] **Step 2: Pass `onDanmaku` into `attachPlayer`**

In `onMounted`, add the `onDanmaku` option to the `attachPlayer({ ... })` call (alongside `onDrift`):

```ts
  handle = attachPlayer({
    video: video.value!,
    src: bridge.streamURL(host, cid),
    role: role.value,
    wsURL: bridge.partyWSURL(),
    onDrift: (d) => (drift.value = d),
    onDanmaku: (d) => overlay.value?.add(d),
  })
```

Note: `handle` is typed by `attachPlayer`'s return; `handle.sendDanmaku` now exists for all roles (no-op in solo).

- [ ] **Step 3: Add the overlay over the video**

In the template, immediately after the `<video ... />` element (inside the `group relative` player div), add:

```html
        <DanmakuOverlay ref="overlay" />
```

- [ ] **Step 4: Add the input pill in the hover chrome**

In the template, after the top chrome block (the `<div class="pointer-events-none absolute inset-x-3 top-3 ...">...</div>`), add a bottom-center reveal-on-hover input, shown only in a party:

```html
        <!-- danmaku input: slim bottom-center pill, revealed with the hover chrome -->
        <div
          v-if="role !== 'solo'"
          class="pointer-events-none absolute inset-x-0 bottom-3 z-20 flex justify-center opacity-0 transition-opacity duration-200 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100"
        >
          <form
            class="pointer-events-auto flex w-[min(70%,32rem)] items-center gap-2 rounded-full bg-black/55 px-3 py-1.5 ring-1 ring-white/10 backdrop-blur"
            @submit.prevent="sendDanmaku"
          >
            <UIcon name="i-lucide-message-circle" class="size-4 shrink-0 text-white/70" />
            <input
              v-model="draft"
              :maxlength="MAX_DANMAKU_LEN"
              placeholder="Send a danmaku…"
              aria-label="Send a danmaku"
              class="min-w-0 flex-1 bg-transparent text-sm text-white placeholder:text-white/40 focus:outline-none"
            />
            <button type="submit" class="shrink-0 text-sm font-medium text-primary">Send</button>
          </form>
        </div>
```

(`DanmakuOverlay` and `UIcon` auto-import via Nuxt; `UIcon` is already used elsewhere on this page.)

- [ ] **Step 5: Typecheck**

Run: `cd webui && npx nuxt typecheck`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add "webui/app/pages/watch/[host]/[contentId].vue"
git commit -m "feat: danmaku overlay + input on the watch page"
```

---

## Task 10: Build, full test sweep, and manual two-Node verification

**Files:** none (build + verify)

- [ ] **Step 1: Front-end unit tests**

Run: `cd webui && npx vitest run`
Expected: PASS (danmaku, actuator, libraryTree, store, useBridge specs).

- [ ] **Step 2: Go unit tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 3: Rebuild the embedded UI bundle + binaries**

Run: `make webui && make build`
Expected: `webui` builds, the bundle is copied into `internal/bridge/dist/`, and `bin/node` + `bin/signal-server` compile.
(Per ADR 0006 / `.gitignore`, the built bundle is not committed — this is for local manual testing only.)

- [ ] **Step 4: Manual verification (two Nodes, one Watch Party)**

Follow the README "Getting started" to run the signaling server and two Nodes. Then:
1. On Node A, start a Watch Party on a Title; join it from Node B.
2. Type a Danmaku on B → confirm it scrolls right-to-left on **both** A and B (one round-trip).
3. Type a Danmaku on A (Host) → confirm it appears on both.
4. Hold Enter / paste rapidly on B → confirm the screen does **not** flood (rate bucket + lane cap), and a burst beyond ~3/s is throttled.
5. Paste a >100-character string → confirm it is truncated to 100 runes for everyone.
6. Pause the video → confirm in-flight Danmaku keep scrolling (wall-clock, playback-independent).
7. Leave / end the party → confirm the input disappears and no overlay remains; open a `solo` watch → confirm no input is shown.

- [ ] **Step 5: Final commit (if any manual-fix tweaks were needed)**

```bash
git add -A
git commit -m "chore: danmaku manual-verification fixups"
```

(Skip if step 4 needed no changes.)

---

## Self-review (completed by plan author)

**Spec coverage:**
- Part A Proto → Task 3 (steps 1-2). ✅
- Part B Peer session (interface + dispatch) → Task 3 (steps 3-6). ✅
- Part C Coordinator (broadcastDanmaku, OnPartyDanmaku, sink, single-writer, WS read-branch) → Tasks 3-4. ✅
- Part D Pure limiter + length helper → Tasks 1-2. ✅
- Part E Loopback WS protocol (browser↔Node `type:"danmaku"`) → Task 4 + Task 7. ✅
- Part F Front-end (`attachPlayer`/`onDanmaku`/`sendDanmaku`, `danmaku.ts`, `DanmakuOverlay.vue`, watch input) → Tasks 5-9. ✅
- Flood control: Host bucket (Task 2/3), density cap (Task 5/8), client cooldown + invariant (Task 9). ✅
- Anti-spoof, rate-limit, length cap (authoritative) → Task 3. ✅
- Playback-independent (wall-clock overlay) → Task 8 (CSS animation) + manual step 4.6. ✅
- Solo: no input/overlay → Task 7 (no-op handle) + Task 9 (`v-if="role !== 'solo'"`) + manual step 4.7. ✅

**Type consistency:** `PartyDanmaku`/`party_danmaku`/`Envelope_PartyDanmaku`/`GetPartyDanmaku` consistent; `handlePlayerDanmaku`, `broadcastDanmaku`, `pushDanmaku`, `Host.Member`, `DanmakuGate.Allow`, `CapText`, `MaxDanmakuLen`/`MAX_DANMAKU_LEN`, `LaneAllocator.allocate`, `pushBounded`, `parsePartyMessage`/`PartyDown`/`DanmakuMsg`, `attachPlayer(...).sendDanmaku`/`onDanmaku` all referenced consistently across tasks. ✅

**Placeholder scan:** no TBD/TODO; every code step shows complete code; every test step shows the assertions. ✅
