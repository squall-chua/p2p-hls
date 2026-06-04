# Slice 4: Watch-Party Synced Playback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Host plays one Title and access-allowed Viewers join (advertised or by invite) with playback hard-synced to the Host (~250–500 ms), across play/pause/seek and late-join catch-up; Viewers still pull segments 1:1 from the Host.

**Architecture:** A new pure-Go `internal/party` package holds the sync engines (`Host`, `Viewer`) and a `Clock` interface, fully unit-tested against a virtual clock — no goroutines, no I/O. `internal/app` owns all orchestration (a heartbeat goroutine, the loopback `/party` WebSocket loops, and routing of new party protobuf messages into the engines). New protobuf `Envelope` messages carry party state over the existing authenticated control channel (star topology, host↔each viewer). The Host's playback is the single clock authority; Viewers extrapolate the Host position from RTT/2-on-receipt and correct via gentle `playbackRate` nudges or a hard seek.

**Tech Stack:** Go 1.25, `google.golang.org/protobuf` (protoc, `make proto`), Pion WebRTC v4, `github.com/gorilla/websocket` (already a dependency), `github.com/stretchr/testify/require`. Module path `github.com/squall-chua/p2p-hls`.

**Source docs:** spec `docs/superpowers/specs/2026-06-04-slice-4-watch-party-sync-design.md`; decisions `docs/adr/0004-watch-party-sync-model.md`; glossary `CONTEXT.md` (Watch Party, Audience, Host, Viewer).

---

## Conventions (apply to every task)

- **TDD:** write the failing test first, watch it fail, implement minimally, watch it pass, commit.
- **Tests:** stdlib `testing` + `testify/require`. Test packages use the `_test` suffix (e.g. `package party_test`) except where testing unexported helpers.
- **Race:** `go test -race ./...` must stay green. Run it before each commit that touches concurrent code (Tasks 4, 5, 7, 8).
- **Commits:** short, point-form subject; **no** `Co-Authored-By`/"Generated with" footer.
- **Imports:** the generated protobuf package is imported as `peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"`.
- **Branch:** do all work on a branch off `main` (e.g. `slice-4-watch-party`), never commit slice work directly to `main`. Finish with `superpowers:finishing-a-development-branch` (the user has chosen "Merge to main locally" for prior slices).

---

## File Structure

**New files:**
- `internal/party/party.go` — `Clock`, `RealClock`, `Config`, `DefaultConfig`, `State`, `Action`, `Member`, `clamp`.
- `internal/party/viewer.go` — `Viewer` engine (correction state machine).
- `internal/party/host.go` — `Host` engine (authoritative state, interpolation, seek debounce, Audience).
- `internal/party/viewer_test.go`, `internal/party/host_test.go` — unit tests (virtual clock).
- `internal/peer/party.go` — `PartyHandler` interface + the session's party send/receive helpers (keeps `session.go` lean).
- `internal/peer/party_test.go` — two-session party-message + capability + RTT + disconnect tests.
- `internal/bridge/party_ws.go` — `/party` WebSocket endpoint (dumb conduit).
- `internal/bridge/party_ws_test.go` — WS upgrade + round-trip test.
- `internal/app/party.go` — `partyCoordinator`: orchestration, `peer.PartyHandler` + `catalog.PartyProvider` impl, heartbeat goroutine, WS loops, entry points.
- `test/party_e2e_test.go` — two in-process nodes; host opens a party, viewer joins over a real peer session, a Go fake actuator converges.

**Modified files:**
- `proto/peer/v1/peer.proto` — new messages; `Envelope` oneof fields 17–23; `TitleMeta` fields 12–13.
- `proto/peer/v1/peer.pb.go` — regenerated via `make proto` (do not hand-edit).
- `internal/peer/session.go` — dispatch new party Envelopes; advertise/record capabilities; `OnConnectionStateChange` → `onClose`; small exported send/RTT helpers.
- `internal/catalog/service.go` — `PartyProvider` field; `toMeta` populates `party_live`/`party_viewers`.
- `internal/app/node.go` — construct + wire `partyCoordinator`; install as party handler and catalog provider; expose entry points.

---

## Task 0: Protobuf — party messages + TitleMeta fields

**Files:**
- Modify: `proto/peer/v1/peer.proto`
- Regenerate: `proto/peer/v1/peer.pb.go` (via `make proto`)

- [ ] **Step 1: Add the new messages and oneof fields to `peer.proto`**

In the `Envelope` `oneof body` block, after `Download download = 16;`, add:

```proto
    JoinParty join_party = 17;
    PartyWelcome party_welcome = 18;
    PartyInvite party_invite = 19;
    PartyState party_state = 20;
    PartyAudience party_audience = 21;
    LeaveParty leave_party = 22;
    PartyEnded party_ended = 23;
```

In `message TitleMeta { ... }`, after `repeated SubtitleTrack subtitles = 11;`, add:

```proto
  bool party_live = 12;     // a live Watch Party exists for this Title on this Host
  int32 party_viewers = 13; // current Audience size (Viewers, excluding the Host)
```

At the end of the file, add the new messages:

```proto
// JoinParty asks a Host to admit this Viewer to the live Watch Party for a Title.
// content_id is only the join reference; the Host answers with the current party_id.
message JoinParty { string content_id = 1; }

// PartyWelcome admits a Viewer: the assigned party_id, the current authoritative
// state, and the current Audience.
message PartyWelcome {
  string party_id = 1;
  PartyState initial = 2;
  PartyAudience audience = 3;
}

// PartyInvite is pushed by a Host to proactively invite a Node to a Watch Party.
message PartyInvite {
  string content_id = 1;
  string party_id = 2;
  string host_display = 3;
}

// PartyState is the Host's authoritative playback state at a sample instant.
// It is both the periodic heartbeat and the on-change event (highest seq wins).
message PartyState {
  string party_id = 1;
  bool playing = 2;
  int64 position_ms = 3;
  int64 host_clock_ms = 4; // reserved for the NTP fallback; unused by the RTT/2 model
  double rate = 5;
  uint64 seq = 6;
}

// PartyAudience is the current set of Viewers in a Watch Party.
message PartyAudience {
  string party_id = 1;
  repeated AudienceMember members = 2;
}

message AudienceMember {
  string node_id = 1;
  string display_name = 2;
}

// LeaveParty tells the Host this Viewer is leaving the Audience.
message LeaveParty { string party_id = 1; }

// PartyEnded tells Viewers the Host has stopped the Watch Party.
message PartyEnded {
  string party_id = 1;
  string reason = 2;
}
```

- [ ] **Step 2: Regenerate and record the exact generated oneof wrapper names**

Run: `make proto && go build ./...`
Expected: builds clean.

Then inspect the generated wrapper type names (protoc appends a trailing `_` when a oneof field's Go name equals its message type name — this already happened for `Envelope_Playlist_` in slice 3, and will happen for every party field below):

Run: `grep -nE "type Envelope_(JoinParty|PartyWelcome|PartyInvite|PartyState|PartyAudience|LeaveParty|PartyEnded)" proto/peer/v1/peer.pb.go`

Expected (verify the EXACT names — later tasks assume trailing underscores; if protoc named them differently, use whatever this grep prints):
```
type Envelope_JoinParty_ struct {...}
type Envelope_PartyWelcome_ struct {...}
type Envelope_PartyInvite_ struct {...}
type Envelope_PartyState_ struct {...}
type Envelope_PartyAudience_ struct {...}
type Envelope_LeaveParty_ struct {...}
type Envelope_PartyEnded_ struct {...}
```
The struct field inside each wrapper is the non-underscore name (e.g. `Envelope_PartyState_{PartyState: ...}`). Confirm with: `grep -n "PartyState \*PartyState" proto/peer/v1/peer.pb.go`.

- [ ] **Step 3: Commit**

```bash
git add proto/peer/v1/peer.proto proto/peer/v1/peer.pb.go
git commit -m "proto: add watch-party messages and TitleMeta party fields"
```

---

## Task 1: Party core types + Clock

**Files:**
- Create: `internal/party/party.go`
- Test: `internal/party/party_test.go`

- [ ] **Step 1: Write the failing test**

`internal/party/party_test.go`:
```go
package party_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigSane(t *testing.T) {
	c := party.DefaultConfig()
	require.Equal(t, int64(1000), c.SeekThresholdMS)
	require.Equal(t, int64(80), c.DeadbandMS)
	require.Less(t, c.MinRate, 1.0)
	require.Greater(t, c.MaxRate, 1.0)
	require.Positive(t, c.HeartbeatInterval)
}

func TestRealClockMonotonicish(t *testing.T) {
	c := party.RealClock()
	a := c.Now()
	require.False(t, a.IsZero())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestDefaultConfigSane -v`
Expected: FAIL — package/symbols not defined.

- [ ] **Step 3: Write minimal implementation**

`internal/party/party.go`:
```go
// Package party holds the watch-party sync engines. The Host engine is the
// authoritative clock; the Viewer engine extrapolates the Host position and
// emits actuator Actions. Both are pure: a Clock is the only ambient input, so
// tests drive them with a virtual clock. All orchestration (goroutines, the
// loopback WebSocket, peer I/O) lives in package app.
package party

import (
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Clock abstracts the wall clock so engines are deterministically testable.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock returns a Clock backed by time.Now.
func RealClock() Clock { return realClock{} }

// Config holds the sync tunables (see ADR 0004 for the chosen defaults).
type Config struct {
	HeartbeatInterval time.Duration // how often the Host emits PartyState
	SeekThresholdMS   int64         // |drift| above this => hard seek
	DeadbandMS        int64         // |drift| below this => no correction
	MinRate, MaxRate  float64       // playbackRate clamp for nudging
	Kp                float64       // proportional gain (per second of drift)
	MaxOWDMS          int64         // cap on the one-way-delay estimate (spike guard)
	SeekDebounce      time.Duration // host scrub settle window before committing a seek
}

// DefaultConfig returns the spec defaults.
func DefaultConfig() Config {
	return Config{
		HeartbeatInterval: 500 * time.Millisecond,
		SeekThresholdMS:   1000,
		DeadbandMS:        80,
		MinRate:           0.92,
		MaxRate:           1.08,
		Kp:                0.2,
		MaxOWDMS:          250,
		SeekDebounce:      175 * time.Millisecond,
	}
}

// State is the Host's authoritative playback state at a sample instant; it
// mirrors the PartyState wire message.
type State struct {
	PartyID    string
	Playing    bool
	PositionMS int64
	Rate       float64
	Seq        uint64
}

// Action is the actuator instruction for one Viewer tick. The player applies it
// idempotently: if Seek, seek to SeekMS; set playbackRate to Rate; then play or
// pause to match Play.
type Action struct {
	Play   bool
	Seek   bool
	SeekMS int64
	Rate   float64
}

// Member is one entry in the Audience.
type Member struct {
	NodeID      identity.NodeID
	DisplayName string
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/party/ -run 'TestDefaultConfigSane|TestRealClockMonotonicish' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/party/party.go internal/party/party_test.go
git commit -m "feat(party): core types, Config defaults, Clock interface"
```

---

## Task 2: Viewer correction engine (the heart)

The Viewer ingests `PartyState` (rejecting stale `seq`), updates its one-way-delay estimate from RTT, and on each tick computes the actuator `Action` from the drift between its player position and the extrapolated Host position.

**Files:**
- Create: `internal/party/viewer.go`
- Test: `internal/party/viewer_test.go`

- [ ] **Step 1: Write the failing test**

`internal/party/viewer_test.go`:
```go
package party_test

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

// fakeClock is a virtual clock for deterministic engine tests.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }
func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestViewerNoStateIsNoop(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	a := v.Decide(0, false, clk.Now())
	require.False(t, a.Seek)
	require.Equal(t, 1.0, a.Rate)
}

func TestViewerSeeksWhenDriftExceedsThreshold(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(20 * time.Millisecond) // owd ~10ms
	// Host paused at 30_000ms; player is at 10_000ms => 20s behind => seek.
	v.OnState(party.State{Playing: false, PositionMS: 30_000, Rate: 1, Seq: 1}, clk.Now())
	a := v.Decide(10_000, false, clk.Now())
	require.True(t, a.Seek)
	require.Equal(t, int64(30_000), a.SeekMS)
	require.Equal(t, 1.0, a.Rate)
}

func TestViewerNudgesRateForSmallDrift(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(0)
	// Host paused at 10_000ms; player 300ms AHEAD => slow down (rate<1), no seek.
	v.OnState(party.State{Playing: false, PositionMS: 10_000, Rate: 1, Seq: 1}, clk.Now())
	a := v.Decide(10_300, false, clk.Now())
	require.False(t, a.Seek)
	require.Less(t, a.Rate, 1.0)
	require.GreaterOrEqual(t, a.Rate, party.DefaultConfig().MinRate)

	// Player 300ms BEHIND => speed up (rate>1).
	a = v.Decide(9_700, false, clk.Now())
	require.Greater(t, a.Rate, 1.0)
}

func TestViewerDeadbandNoCorrection(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(0)
	v.OnState(party.State{Playing: false, PositionMS: 10_000, Rate: 1, Seq: 1}, clk.Now())
	a := v.Decide(10_040, false, clk.Now()) // 40ms < 80ms deadband
	require.False(t, a.Seek)
	require.Equal(t, 1.0, a.Rate)
}

func TestViewerMatchesPlayPause(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(0)
	v.OnState(party.State{Playing: true, PositionMS: 5_000, Rate: 1, Seq: 1}, clk.Now())
	a := v.Decide(5_000, false, clk.Now())
	require.True(t, a.Play)
}

func TestViewerExtrapolatesWhilePlaying(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(40 * time.Millisecond) // owd 20ms
	v.OnState(party.State{Playing: true, PositionMS: 10_000, Rate: 1, Seq: 1}, clk.Now())
	clk.advance(1 * time.Second)
	// Expected host pos = 10_000 + 20(owd) + 1000(elapsed) = 11_020.
	// Player exactly there => deadband, no seek, rate 1.
	a := v.Decide(11_020, true, clk.Now())
	require.False(t, a.Seek)
	require.Equal(t, 1.0, a.Rate)
}

func TestViewerRejectsStaleSeq(t *testing.T) {
	clk := newFakeClock()
	v := party.NewViewer(clk, party.DefaultConfig())
	v.OnRTT(0)
	v.OnState(party.State{Playing: false, PositionMS: 20_000, Rate: 1, Seq: 5}, clk.Now())
	v.OnState(party.State{Playing: false, PositionMS: 0, Rate: 1, Seq: 4}, clk.Now()) // stale, ignored
	a := v.Decide(20_000, false, clk.Now())
	require.False(t, a.Seek) // still synced to seq 5 at 20_000
}

// Convergence: a behind Viewer nudges up and closes the gap over time.
func TestViewerConvergesViaNudge(t *testing.T) {
	clk := newFakeClock()
	cfg := party.DefaultConfig()
	v := party.NewViewer(clk, cfg)
	v.OnRTT(0)
	// Host playing from 60_000ms at t0.
	hostPos := int64(60_000)
	v.OnState(party.State{Playing: true, PositionMS: hostPos, Rate: 1, Seq: 1}, clk.Now())
	player := int64(59_500) // 500ms behind (within nudge band)
	step := 100 * time.Millisecond
	for i := 0; i < 80; i++ {
		a := v.Decide(player, true, clk.Now())
		require.False(t, a.Seek)
		// advance virtual time; player integrates at its applied rate; host at 1.0.
		clk.advance(step)
		player += int64(a.Rate * float64(step.Milliseconds()))
		hostPos += step.Milliseconds()
	}
	drift := player - (hostPos + 0 /*owd*/)
	if drift < 0 {
		drift = -drift
	}
	require.LessOrEqual(t, drift, cfg.DeadbandMS, "viewer should converge into the deadband")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestViewer -v`
Expected: FAIL — `NewViewer` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/party/viewer.go`:
```go
package party

import (
	"sync"
	"time"
)

// Viewer is a follower's sync engine. It is safe for concurrent use: OnState and
// OnRTT are called from the peer read path while Decide is called from the WS
// tick loop.
type Viewer struct {
	clock Clock
	cfg   Config

	mu        sync.Mutex
	haveState bool
	last      State
	recvAt    time.Time
	owdMS     int64
}

// NewViewer constructs a Viewer engine.
func NewViewer(clock Clock, cfg Config) *Viewer {
	return &Viewer{clock: clock, cfg: cfg}
}

// OnState ingests a received PartyState, ignoring stale (<= current) seq values.
func (v *Viewer) OnState(s State, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.haveState && s.Seq <= v.last.Seq {
		return
	}
	v.last = s
	v.recvAt = now
	v.haveState = true
}

// OnRTT updates the one-way-delay estimate (RTT/2, capped) from a round trip.
func (v *Viewer) OnRTT(rtt time.Duration) {
	owd := rtt.Milliseconds() / 2
	if owd > v.cfg.MaxOWDMS {
		owd = v.cfg.MaxOWDMS
	}
	if owd < 0 {
		owd = 0
	}
	v.mu.Lock()
	v.owdMS = owd
	v.mu.Unlock()
}

// Decide computes the actuator Action for the player's current position/state.
func (v *Viewer) Decide(playerPosMS int64, playerPlaying bool, now time.Time) Action {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.haveState {
		return Action{Play: playerPlaying, Rate: 1.0}
	}
	h := v.expectedHostPosLocked(now)
	drift := playerPosMS - h // + => viewer ahead
	abs := drift
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs > v.cfg.SeekThresholdMS:
		return Action{Play: v.last.Playing, Seek: true, SeekMS: h, Rate: 1.0}
	case abs > v.cfg.DeadbandMS:
		rate := clamp(1.0-v.cfg.Kp*(float64(drift)/1000.0), v.cfg.MinRate, v.cfg.MaxRate)
		return Action{Play: v.last.Playing, Rate: rate}
	default:
		return Action{Play: v.last.Playing, Rate: 1.0}
	}
}

// expectedHostPosLocked extrapolates the Host position at `now`. While paused the
// Host is not advancing, so owd is irrelevant and the position is exact (ADR 0004).
func (v *Viewer) expectedHostPosLocked(now time.Time) int64 {
	if !v.last.Playing {
		return v.last.PositionMS
	}
	return v.last.PositionMS + v.owdMS + now.Sub(v.recvAt).Milliseconds()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/party/ -run TestViewer -race -v`
Expected: PASS (all Viewer tests).

- [ ] **Step 5: Commit**

```bash
git add internal/party/viewer.go internal/party/viewer_test.go
git commit -m "feat(party): viewer correction engine (rate-nudge + seek)"
```

---

## Task 3: Host engine (authoritative state, interpolation, seek debounce, Audience)

The Host engine reads its own player's events/reports, holds the authoritative state, interpolates position between reports, debounces scrubs, and tracks the Audience.

**Files:**
- Create: `internal/party/host.go`
- Test: `internal/party/host_test.go`

- [ ] **Step 1: Write the failing test**

`internal/party/host_test.go`:
```go
package party_test

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

func TestHostSnapshotInterpolatesWhilePlaying(t *testing.T) {
	clk := newFakeClock()
	h := party.NewHost(clk, party.DefaultConfig(), "p1", "cid")
	h.OnPlay(1_000, clk.Now())
	clk.advance(2 * time.Second)
	s := h.Snapshot(clk.Now())
	require.True(t, s.Playing)
	require.Equal(t, int64(3_000), s.PositionMS) // 1_000 + 2_000 elapsed
	require.Equal(t, "p1", s.PartyID)
}

func TestHostPlayPauseBumpsSeqImmediately(t *testing.T) {
	clk := newFakeClock()
	h := party.NewHost(clk, party.DefaultConfig(), "p1", "cid")
	s1 := h.Snapshot(clk.Now())
	h.OnPlay(0, clk.Now())
	s2 := h.Snapshot(clk.Now())
	h.OnPause(5_000, clk.Now())
	s3 := h.Snapshot(clk.Now())
	require.Greater(t, s2.Seq, s1.Seq)
	require.Greater(t, s3.Seq, s2.Seq)
	require.False(t, s3.Playing)
	require.Equal(t, int64(5_000), s3.PositionMS)
}

func TestHostSeekIsDebounced(t *testing.T) {
	clk := newFakeClock()
	cfg := party.DefaultConfig()
	h := party.NewHost(clk, cfg, "p1", "cid")
	h.OnPlay(0, clk.Now())
	before := h.Snapshot(clk.Now()).Seq

	// Rapid scrub: three seeks within the debounce window.
	h.OnSeek(10_000, clk.Now())
	clk.advance(50 * time.Millisecond)
	h.OnSeek(20_000, clk.Now())
	clk.advance(50 * time.Millisecond)
	h.OnSeek(30_000, clk.Now())

	// During the scrub the Host holds (paused), seq not yet committed.
	mid := h.Snapshot(clk.Now())
	require.False(t, mid.Playing, "host holds (buffering) during scrub")
	_, committed := h.Tick(clk.Now())
	require.False(t, committed, "seek not committed before settle")

	// Settle: advance past the debounce window, then Tick commits the final seek.
	clk.advance(cfg.SeekDebounce + 10*time.Millisecond)
	st, committed := h.Tick(clk.Now())
	require.True(t, committed)
	require.Greater(t, st.Seq, before)
	require.Equal(t, int64(30_000), st.PositionMS) // final scrub position
	require.True(t, st.Playing, "resumes prior playing state after the scrub")
}

func TestHostAudienceJoinLeaveCount(t *testing.T) {
	clk := newFakeClock()
	h := party.NewHost(clk, party.DefaultConfig(), "p1", "cid")
	require.Equal(t, 0, h.ViewerCount())
	h.Join(identity.NodeID("alice"), "Alice")
	h.Join(identity.NodeID("bob"), "Bob")
	h.Join(identity.NodeID("alice"), "Alice") // idempotent
	require.Equal(t, 2, h.ViewerCount())
	require.Len(t, h.Members(), 2)
	h.Leave(identity.NodeID("alice"))
	require.Equal(t, 1, h.ViewerCount())
}

func TestHostReportKeepsPositionFresh(t *testing.T) {
	clk := newFakeClock()
	h := party.NewHost(clk, party.DefaultConfig(), "p1", "cid")
	h.OnPlay(0, clk.Now())
	seqAfterPlay := h.Snapshot(clk.Now()).Seq
	clk.advance(1 * time.Second)
	h.OnReport(1_000, clk.Now()) // periodic position report, no state change
	s := h.Snapshot(clk.Now())
	require.Equal(t, seqAfterPlay, s.Seq, "a plain report must not bump seq")
	require.Equal(t, int64(1_000), s.PositionMS)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/party/ -run TestHost -v`
Expected: FAIL — `NewHost` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/party/host.go`:
```go
package party

import (
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Host is the authoritative sync engine. It reads the local player's events via
// On* and exposes the current authoritative state via Snapshot. It is safe for
// concurrent use (player input, heartbeat ticks, and Audience changes race).
type Host struct {
	clock Clock
	cfg   Config

	mu        sync.Mutex
	partyID   string
	contentID string

	playing   bool
	posMS     int64
	sampledAt time.Time
	rate      float64
	seq       uint64

	// seek debounce
	scrubbing     bool
	pendingSeekMS int64
	lastSeekInput time.Time

	audience map[identity.NodeID]string
}

// NewHost constructs a Host engine for a party on contentID.
func NewHost(clock Clock, cfg Config, partyID, contentID string) *Host {
	return &Host{
		clock:     clock,
		cfg:       cfg,
		partyID:   partyID,
		contentID: contentID,
		rate:      1.0,
		sampledAt: clock.Now(),
		audience:  map[identity.NodeID]string{},
	}
}

func (h *Host) PartyID() string   { return h.partyID }
func (h *Host) ContentID() string { return h.contentID }

// OnPlay/OnPause are immediate state changes (bump seq now).
func (h *Host) OnPlay(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.playing = true
	h.posMS = posMS
	h.sampledAt = now
	h.scrubbing = false
	h.seq++
}

func (h *Host) OnPause(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.playing = false
	h.posMS = posMS
	h.sampledAt = now
	h.scrubbing = false
	h.seq++
}

// OnReport is a periodic position report while playing; it keeps the
// interpolation anchor fresh but does NOT bump seq (no observable change).
func (h *Host) OnReport(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.scrubbing {
		return
	}
	h.posMS = posMS
	h.sampledAt = now
}

// OnSeek records a (possibly mid-scrub) seek. It does not commit immediately;
// Tick commits the final position once the scrub settles.
func (h *Host) OnSeek(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scrubbing = true
	h.pendingSeekMS = posMS
	h.lastSeekInput = now
}

// Tick commits a settled seek. It returns the new Snapshot and true iff a seek
// was committed this tick. Call it from the heartbeat loop.
func (h *Host) Tick(now time.Time) (State, bool) {
	h.mu.Lock()
	committed := false
	if h.scrubbing && now.Sub(h.lastSeekInput) >= h.cfg.SeekDebounce {
		h.posMS = h.pendingSeekMS
		h.sampledAt = now
		h.scrubbing = false
		h.seq++
		committed = true
	}
	h.mu.Unlock()
	return h.Snapshot(now), committed
}

// Snapshot returns the current authoritative state with interpolated position.
func (h *Host) Snapshot(now time.Time) State {
	h.mu.Lock()
	defer h.mu.Unlock()
	pos := h.posMS
	playing := h.playing
	if h.scrubbing {
		// Hold viewers during a scrub: report paused at the latest scrub target.
		return State{PartyID: h.partyID, Playing: false, PositionMS: h.pendingSeekMS, Rate: h.rate, Seq: h.seq}
	}
	if playing {
		pos += now.Sub(h.sampledAt).Milliseconds()
	}
	return State{PartyID: h.partyID, Playing: playing, PositionMS: pos, Rate: h.rate, Seq: h.seq}
}

// Join adds a Viewer to the Audience (idempotent).
func (h *Host) Join(node identity.NodeID, name string) {
	h.mu.Lock()
	h.audience[node] = name
	h.mu.Unlock()
}

// Leave removes a Viewer from the Audience.
func (h *Host) Leave(node identity.NodeID) {
	h.mu.Lock()
	delete(h.audience, node)
	h.mu.Unlock()
}

// Members returns the current Audience.
func (h *Host) Members() []Member {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Member, 0, len(h.audience))
	for n, name := range h.audience {
		out = append(out, Member{NodeID: n, DisplayName: name})
	}
	return out
}

// ViewerCount is the current Audience size.
func (h *Host) ViewerCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.audience)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/party/ -run TestHost -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/party/host.go internal/party/host_test.go
git commit -m "feat(party): host engine (interpolation, seek debounce, audience)"
```

---

## Task 4: Peer protocol plumbing (dispatch, capability, RTT, disconnect)

Add a `PartyHandler` interface and route the new party Envelopes to it; advertise/record the `party/v1` capability; expose RTT measurement; fire a disconnect callback; and add small exported send helpers. **Use the exact generated wrapper names recorded in Task 0, Step 2** (shown below with the expected trailing `_`).

**Files:**
- Create: `internal/peer/party.go`
- Modify: `internal/peer/session.go` (dispatch switch ~lines 159–200; `NewSession` ~lines 49–75; `OnOpen` handshake send ~lines 147–153; struct fields ~lines 25–47)
- Test: `internal/peer/party_test.go`

- [ ] **Step 1: Write the failing test**

`internal/peer/party_test.go` (reuse the existing `connectPair` helper from the peer test package — it returns two connected `*Session`):
```go
package peer

import (
	"context"
	"testing"
	"time"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakePartyHandler struct {
	states    chan *peerv1.PartyState
	joinedCID string
}

func (f *fakePartyHandler) OnJoinParty(_ identity.NodeID, contentID string) (*peerv1.PartyWelcome, error) {
	f.joinedCID = contentID
	return &peerv1.PartyWelcome{PartyId: "p1", Initial: &peerv1.PartyState{PartyId: "p1", PositionMs: 42}}, nil
}
func (f *fakePartyHandler) OnLeaveParty(identity.NodeID, string)             {}
func (f *fakePartyHandler) OnPartyState(_ identity.NodeID, s *peerv1.PartyState) { f.states <- s }
func (f *fakePartyHandler) OnPartyAudience(identity.NodeID, *peerv1.PartyAudience) {}
func (f *fakePartyHandler) OnPartyInvite(identity.NodeID, *peerv1.PartyInvite)     {}
func (f *fakePartyHandler) OnPartyEnded(identity.NodeID, *peerv1.PartyEnded)       {}

func TestPartyStateDeliveredToHandler(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	h := &fakePartyHandler{states: make(chan *peerv1.PartyState, 1)}
	b.SetPartyHandler(h)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.SendControl(&peerv1.Envelope{
		Body: &peerv1.Envelope_PartyState_{PartyState: &peerv1.PartyState{PartyId: "p1", PositionMs: 1234, Seq: 7}},
	}))
	select {
	case s := <-h.states:
		require.Equal(t, int64(1234), s.GetPositionMs())
		require.Equal(t, uint64(7), s.GetSeq())
	case <-ctx.Done():
		t.Fatal("PartyState not delivered")
	}
}

func TestJoinPartyRequestResponse(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	h := &fakePartyHandler{states: make(chan *peerv1.PartyState, 1)}
	b.SetPartyHandler(h)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := a.JoinParty(ctx, "cid-xyz")
	require.NoError(t, err)
	require.Equal(t, "p1", w.GetPartyId())
	require.Equal(t, int64(42), w.GetInitial().GetPositionMs())
	require.Equal(t, "cid-xyz", h.joinedCID)
}

func TestCapabilityAdvertised(t *testing.T) {
	a, b, _ := connectPair(t) // (viewer, host, hostHandler); b is the host session
	require.Eventually(t, func() bool { return a.HasCapability(CapParty) && b.HasCapability(CapParty) },
		3*time.Second, 25*time.Millisecond)
}

func TestMeasureRTTPositive(t *testing.T) {
	a, _, _ := connectPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rtt, err := a.MeasureRTT(ctx)
	require.NoError(t, err)
	require.Positive(t, rtt)
}
```

> Note: this test file is `package peer` (internal) so it can use the existing `connectPair` helper, confirmed declared in `internal/peer/rpc_test.go` (`package peer`) as `func connectPair(t *testing.T) (viewer, host *Session, hostHandler *fakeHandler)`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/peer/ -run 'TestParty|TestJoinParty|TestCapability|TestMeasureRTT' -v`
Expected: FAIL — `SetPartyHandler`/`SendControl`/`JoinParty`/`HasCapability`/`CapParty`/`MeasureRTT` undefined.

- [ ] **Step 3a: Add the PartyHandler interface and helpers in `internal/peer/party.go`**

```go
package peer

import (
	"context"
	"time"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

// CapParty is the handshake capability string for watch-party support.
const CapParty = "party/v1"

// PartyHandler receives inbound watch-party messages. A Node implements it once
// and the same handler covers both host-side (OnJoinParty/OnLeaveParty) and
// viewer-side (OnParty*) messages, since which role applies depends on the peer.
type PartyHandler interface {
	OnJoinParty(remote identity.NodeID, contentID string) (*peerv1.PartyWelcome, error)
	OnLeaveParty(remote identity.NodeID, partyID string)
	OnPartyState(remote identity.NodeID, s *peerv1.PartyState)
	OnPartyAudience(remote identity.NodeID, a *peerv1.PartyAudience)
	OnPartyInvite(remote identity.NodeID, inv *peerv1.PartyInvite)
	OnPartyEnded(remote identity.NodeID, e *peerv1.PartyEnded)
}

// SetPartyHandler installs the handler for inbound party messages.
func (s *Session) SetPartyHandler(h PartyHandler) {
	s.mu.Lock()
	s.partyHandler = h
	s.mu.Unlock()
}

func (s *Session) currentPartyHandler() PartyHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partyHandler
}

// SendControl sends a fire-and-forget Envelope on the control channel. Used for
// PartyState/PartyAudience/PartyInvite/PartyEnded/LeaveParty.
func (s *Session) SendControl(env *peerv1.Envelope) error { return s.send(env) }

// JoinParty sends a JoinParty request and awaits the PartyWelcome response.
func (s *Session) JoinParty(ctx context.Context, contentID string) (*peerv1.PartyWelcome, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{
		Body: &peerv1.Envelope_JoinParty_{JoinParty: &peerv1.JoinParty{ContentId: contentID}},
	})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	w := resp.GetPartyWelcome()
	if w == nil {
		return nil, ErrUnavailable
	}
	return w, nil
}

// MeasureRTT times a Ping round trip.
func (s *Session) MeasureRTT(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if _, err := s.Ping(ctx, "rtt"); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// HasCapability reports whether the remote advertised the given capability.
func (s *Session) HasCapability(cap string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.remoteCaps {
		if c == cap {
			return true
		}
	}
	return false
}

// SetOnClose registers a callback fired when the peer connection drops.
func (s *Session) SetOnClose(fn func(identity.NodeID)) {
	s.mu.Lock()
	s.onClose = fn
	s.mu.Unlock()
}

func (s *Session) handleJoinParty(reqID uint64, contentID string) {
	h := s.currentPartyHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	w, err := h.OnJoinParty(s.remote, contentID)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_PartyWelcome_{PartyWelcome: w}})
}
```

- [ ] **Step 3b: Add struct fields in `internal/peer/session.go`**

In the `Session` struct (after `mediaHandler MediaHandler`), add:
```go
	partyHandler PartyHandler
	remoteCaps   []string
	onClose      func(identity.NodeID)
```

- [ ] **Step 3c: Advertise capabilities on handshake (`OnOpen`, ~line 147)**

Change the handshake send to advertise the party capability:
```go
	dc.OnOpen(func() {
		_ = s.send(&peerv1.Envelope{
			Body: &peerv1.Envelope_Handshake{Handshake: &peerv1.Handshake{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    []string{CapParty},
			}},
		})
	})
```

- [ ] **Step 3d: Record capabilities + dispatch party messages (switch ~line 159)**

In the `case *peerv1.Envelope_Handshake:` branch, record the remote capabilities:
```go
	case *peerv1.Envelope_Handshake:
		s.mu.Lock()
		s.remoteCaps = body.Handshake.GetCapabilities()
		s.mu.Unlock()
		if body.Handshake.GetProtocolVersion() == ProtocolVersion {
			s.readyOnce.Do(func() { close(s.ready) })
		}
```

Add new cases before the final `deliver`-group case:
```go
	case *peerv1.Envelope_JoinParty_:
		s.handleJoinParty(env.RequestId, body.JoinParty.GetContentId())
	case *peerv1.Envelope_LeaveParty_:
		if h := s.currentPartyHandler(); h != nil {
			h.OnLeaveParty(s.remote, body.LeaveParty.GetPartyId())
		}
	case *peerv1.Envelope_PartyState_:
		if h := s.currentPartyHandler(); h != nil {
			h.OnPartyState(s.remote, body.PartyState)
		}
	case *peerv1.Envelope_PartyAudience_:
		if h := s.currentPartyHandler(); h != nil {
			h.OnPartyAudience(s.remote, body.PartyAudience)
		}
	case *peerv1.Envelope_PartyInvite_:
		if h := s.currentPartyHandler(); h != nil {
			h.OnPartyInvite(s.remote, body.PartyInvite)
		}
	case *peerv1.Envelope_PartyEnded_:
		if h := s.currentPartyHandler(); h != nil {
			h.OnPartyEnded(s.remote, body.PartyEnded)
		}
```

Add `*peerv1.Envelope_PartyWelcome_` to the existing `deliver`-group case (it is a response to JoinParty):
```go
	case *peerv1.Envelope_Pong, *peerv1.Envelope_Catalog,
		*peerv1.Envelope_TitleMeta, *peerv1.Envelope_Ack, *peerv1.Envelope_Playlist_,
		*peerv1.Envelope_PartyWelcome_:
		s.deliver(env)
```

- [ ] **Step 3e: Fire a disconnect callback (`NewSession`, after `pc` is created ~line 60)**

Add, alongside the existing `pc.OnDataChannel(...)`:
```go
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateDisconnected,
			webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed:
			s.mu.Lock()
			fn := s.onClose
			s.mu.Unlock()
			if fn != nil {
				fn(s.remote)
			}
		}
	})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/peer/ -race -v`
Expected: PASS (new party tests + all existing peer tests stay green).

- [ ] **Step 5: Commit**

```bash
git add internal/peer/party.go internal/peer/session.go internal/peer/party_test.go
git commit -m "feat(peer): party message dispatch, capability, RTT, disconnect hook"
```

---

## Task 5: Bridge `/party` WebSocket endpoint (dumb conduit)

The bridge only upgrades, token-gates, origin-checks, and hands the raw `*websocket.Conn` to a handler the app registers. **No party logic in the bridge.**

**Files:**
- Create: `internal/bridge/party_ws.go`
- Modify: `internal/bridge/bridge.go` (`Bridge` struct ~lines 21–27; route registration in `Start` ~line 51)
- Test: `internal/bridge/party_ws_test.go`

- [ ] **Step 1: Write the failing test**

`internal/bridge/party_ws_test.go`:
```go
package bridge_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/stretchr/testify/require"
)

func TestPartyWSUpgradesAndEchoes(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	got := make(chan string, 1)
	b.SetPartyHandler(func(c *websocket.Conn) {
		_, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		got <- string(msg)
		_ = c.WriteMessage(websocket.TextMessage, []byte("ack"))
	})
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()

	url := "ws" + strings.TrimPrefix(b.BaseURL(), "http") + "/party/secret-token"
	c, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://127.0.0.1"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer c.Close()

	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte("hello")))
	require.Equal(t, "hello", <-got)
	_, ack, err := c.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "ack", string(ack))
}

func TestPartyWSRejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetPartyHandler(func(*websocket.Conn) {})
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	url := "ws" + strings.TrimPrefix(b.BaseURL(), "http") + "/party/wrong"
	_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://127.0.0.1"}})
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_ = time.Second
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestPartyWS -v`
Expected: FAIL — `SetPartyHandler` undefined.

- [ ] **Step 3a: Add the WS endpoint in `internal/bridge/party_ws.go`**

```go
package bridge

import (
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// SetPartyHandler registers the function invoked with each upgraded /party
// WebSocket connection. The bridge stays a dumb conduit: it authenticates and
// upgrades, then hands the raw connection to the app, which owns the protocol.
func (b *Bridge) SetPartyHandler(fn func(*websocket.Conn)) {
	b.mu.Lock()
	b.partyHandler = fn
	b.mu.Unlock()
}

// handleParty serves /party/{token}: origin-checked, token-gated, then upgraded.
func (b *Bridge) handleParty(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/party/")
	if token != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	fn := b.partyHandler
	b.mu.Unlock()
	if fn == nil {
		http.NotFound(w, r)
		return
	}
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error response
	}
	fn(conn)
}
```

- [ ] **Step 3b: Extend the `Bridge` struct and `Start` in `internal/bridge/bridge.go`**

Add fields to the `Bridge` struct (it currently has `streamer`, `token`, `srv`, `ln`):
```go
	mu           sync.Mutex
	partyHandler func(*websocket.Conn)
	upgrader     websocket.Upgrader
```
Add the imports `"sync"` and `"github.com/gorilla/websocket"`.

In `New(...)`, initialise the upgrader to reuse the loopback origin check:
```go
func New(streamer Streamer, token string) *Bridge {
	b := &Bridge{streamer: streamer, token: token}
	b.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return b.originOK(r) }}
	return b
}
```

In `Start(...)`, register the route next to the existing `/s/` route:
```go
	mux.HandleFunc("/s/", b.handleStream)
	mux.HandleFunc("/party/", b.handleParty)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bridge/ -race -v`
Expected: PASS (new WS tests + existing bridge tests).

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/party_ws.go internal/bridge/bridge.go internal/bridge/party_ws_test.go
git commit -m "feat(bridge): loopback /party websocket conduit (token + origin gated)"
```

---

## Task 6: Catalog — annotate TitleMeta with live-party state

`toMeta` gains access to an optional `PartyProvider` so a browsing Viewer sees `party_live`/`party_viewers`.

**Files:**
- Modify: `internal/catalog/service.go` (`Service` struct; `toMeta` ~lines 69–88; `Browse` ~lines 24–37)
- Test: `internal/catalog/party_test.go`

- [ ] **Step 1: Write the failing test**

`internal/catalog/party_test.go`:
```go
package catalog_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeParty struct {
	live    bool
	viewers int
	cid     string
}

func (f fakeParty) LiveParty(contentID string) (bool, int) {
	if contentID == f.cid {
		return f.live, f.viewers
	}
	return false, 0
}

// newServiceWithTitle (in service_test.go) returns (*Service, *Policy, *Requests)
// and seeds one Title whose Content ID is "cid-1".
const e2eTitleCID = "cid-1"

func TestBrowseAnnotatesLiveParty(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	svc.SetPartyProvider(fakeParty{live: true, viewers: 3, cid: e2eTitleCID})

	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.True(t, titles[0].GetPartyLive())
	require.Equal(t, int32(3), titles[0].GetPartyViewers())
}

func TestBrowseNoProviderLeavesPartyFalse(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.False(t, titles[0].GetPartyLive())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/catalog/ -run TestBrowse -v`
Expected: FAIL — `SetPartyProvider`/`GetPartyLive` undefined.

- [ ] **Step 3: Implement**

In `internal/catalog/service.go`, add the interface + a field + setter, and populate in `toMeta`.

Add near the top of the file:
```go
// PartyProvider reports live Watch Party state for a Title so Browse can annotate
// it. Optional: a nil provider means "no parties".
type PartyProvider interface {
	LiveParty(contentID string) (live bool, viewers int)
}
```

Add a `party PartyProvider` field to the `Service` struct, and a setter:
```go
// SetPartyProvider installs the source of live-party annotations for Browse.
func (s *Service) SetPartyProvider(p PartyProvider) { s.party = p }
```

Convert `toMeta` from a free function to a method so it can read `s.party`, and populate the fields. There are **two** call sites — `service.go:34` (in `Browse`) and `service.go:51` (in `GetMetadata`) — change both `toMeta(t)` → `s.toMeta(t)`:
```go
func (s *Service) toMeta(t library.Title) *peerv1.TitleMeta {
	m := &peerv1.TitleMeta{
		ContentId:     t.ContentID,
		DisplayTitle:  t.DisplayTitle,
		DurationMs:    t.DurationMS,
		Container:     t.Container,
		VideoCodec:    t.VideoCodec,
		AudioCodecs:   t.AudioCodecs,
		Width:         int32(t.Width),
		Height:        int32(t.Height),
		SizeBytes:     t.Size,
		HlsCompatible: t.HLSCompatible,
	}
	for _, sub := range t.Subtitles {
		m.Subtitles = append(m.Subtitles, &peerv1.SubtitleTrack{
			Id: sub.ID, Language: sub.Language, Label: sub.Label, Kind: sub.Kind,
		})
	}
	if s.party != nil {
		if live, viewers := s.party.LiveParty(t.ContentID); live {
			m.PartyLive = true
			m.PartyViewers = int32(viewers)
		}
	}
	return m
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -v`
Expected: PASS (new tests + existing catalog tests stay green).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/service.go internal/catalog/party_test.go
git commit -m "feat(catalog): annotate TitleMeta with live-party state"
```

---

## Task 7: App orchestration (partyCoordinator: heartbeat, WS loops, wiring, entry points)

This is the glue. The coordinator owns the active `Host` or `Viewer` engine, implements `peer.PartyHandler` and `catalog.PartyProvider`, runs the heartbeat goroutine and the WS loops, and exposes entry points.

The loopback WS JSON protocol (player ↔ engine), defined here:
- player → engine: `{"type":"report","posMs":N,"playing":B}`, `{"type":"play","posMs":N}`, `{"type":"pause","posMs":N}`, `{"type":"seek","posMs":N}`, and a first `{"type":"hello","role":"host"|"viewer"}`.
- engine → player (viewer only): `{"play":B,"seek":B,"seekMs":N,"rate":F}` (an `Action`).

**Files:**
- Create: `internal/app/party.go`
- Modify: `internal/app/node.go` (`Node` struct ~lines 19–29; `NewNode` ~lines 44–62; `sessionFor` ~lines 82–103 to install the party handler + onClose)
- Test: `internal/app/party_test.go`

- [ ] **Step 1: Write the failing test (engine-routing + provider, no real WebRTC)**

`internal/app/party_test.go` — tests the coordinator's pure routing/state without a network, by calling its methods directly:
```go
package app

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorHostLifecycleAndProvider(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("host"), party.RealClock(), party.DefaultConfig())

	// No party yet => provider reports not live.
	live, n := pc.LiveParty("cid")
	require.False(t, live)
	require.Equal(t, 0, n)

	pid := pc.StartParty("cid")
	require.NotEmpty(t, pid)

	// A remote viewer joins via the inbound handler path.
	w, err := pc.OnJoinParty(identity.NodeID("alice"), "cid")
	require.NoError(t, err)
	require.Equal(t, pid, w.GetPartyId())

	live, n = pc.LiveParty("cid")
	require.True(t, live)
	require.Equal(t, 1, n)

	// Joining a content with no live party is rejected.
	_, err = pc.OnJoinParty(identity.NodeID("bob"), "other")
	require.Error(t, err)

	pc.OnLeaveParty(identity.NodeID("alice"), pid)
	_, n = pc.LiveParty("cid")
	require.Equal(t, 0, n)
}

func TestCoordinatorViewerIngestsState(t *testing.T) {
	pc := newPartyCoordinator(nil, identity.NodeID("viewer"), party.RealClock(), party.DefaultConfig())
	pc.beginViewer(identity.NodeID("host"), "p1")
	pc.OnPartyState(identity.NodeID("host"), &peerv1.PartyState{PartyId: "p1", Playing: false, PositionMs: 7_000, Seq: 1})

	// The viewer engine should now want to seek a far-off player to 7_000.
	act := pc.viewerDecide(0, false, time.Now())
	require.True(t, act.Seek)
	require.Equal(t, int64(7_000), act.SeekMS)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestCoordinator -v`
Expected: FAIL — `newPartyCoordinator` undefined.

- [ ] **Step 3a: Implement the coordinator in `internal/app/party.go`**

```go
package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// sender abstracts the Node's session access so the coordinator can be unit-
// tested with a nil sender (host/viewer state paths don't touch the network).
type sender interface {
	sendTo(node identity.NodeID, env *peerv1.Envelope) error
	measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error)
}

type partyCoordinator struct {
	send  sender
	self  identity.NodeID
	clock party.Clock
	cfg   party.Config

	mu         sync.Mutex
	host       *party.Host
	viewer     *party.Viewer
	viewerHost identity.NodeID
	stopHB     chan struct{}
}

func newPartyCoordinator(s sender, self identity.NodeID, clk party.Clock, cfg party.Config) *partyCoordinator {
	return &partyCoordinator{send: s, self: self, clock: clk, cfg: cfg}
}

// --- entry points ---

// StartParty opens a Watch Party on contentID and returns the new party_id.
// party_id is derived from the host node + content (stable, opaque to viewers).
func (pc *partyCoordinator) StartParty(contentID string) string {
	pid := string(pc.self) + ":" + contentID
	pc.mu.Lock()
	pc.host = party.NewHost(pc.clock, pc.cfg, pid, contentID)
	pc.stopHB = make(chan struct{})
	stop := pc.stopHB
	h := pc.host
	pc.mu.Unlock()
	go pc.heartbeat(h, stop)
	return pid
}

// EndParty stops the active party and notifies the Audience.
func (pc *partyCoordinator) EndParty(reason string) {
	pc.mu.Lock()
	h := pc.host
	pc.host = nil
	if pc.stopHB != nil {
		close(pc.stopHB)
		pc.stopHB = nil
	}
	pc.mu.Unlock()
	if h == nil {
		return
	}
	for _, m := range h.Members() {
		_ = pc.send.sendTo(m.NodeID, &peerv1.Envelope{
			Body: &peerv1.Envelope_PartyEnded_{PartyEnded: &peerv1.PartyEnded{PartyId: h.PartyID(), Reason: reason}},
		})
	}
}

func (pc *partyCoordinator) beginViewer(host identity.NodeID, partyID string) {
	pc.mu.Lock()
	pc.viewer = party.NewViewer(pc.clock, pc.cfg)
	pc.viewerHost = host
	pc.mu.Unlock()
}

// JoinParty connects (must already have a session) and joins host's party for
// contentID. The caller passes a function that performs the JoinParty RPC.
func (pc *partyCoordinator) JoinParty(ctx context.Context, host identity.NodeID,
	do func(ctx context.Context) (*peerv1.PartyWelcome, error)) error {
	w, err := do(ctx)
	if err != nil {
		return err
	}
	pc.beginViewer(host, w.GetPartyId())
	if init := w.GetInitial(); init != nil {
		pc.OnPartyState(host, init)
	}
	return nil
}

// --- catalog.PartyProvider ---

func (pc *partyCoordinator) LiveParty(contentID string) (bool, int) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.host != nil && pc.host.ContentID() == contentID {
		return true, pc.host.ViewerCount()
	}
	return false, 0
}

// --- peer.PartyHandler ---

func (pc *partyCoordinator) OnJoinParty(remote identity.NodeID, contentID string) (*peerv1.PartyWelcome, error) {
	pc.mu.Lock()
	h := pc.host
	pc.mu.Unlock()
	if h == nil || h.ContentID() != contentID {
		return nil, errors.New("no live party for content")
	}
	h.Join(remote, string(remote))
	pc.broadcastAudience(h)
	st := h.Snapshot(pc.clock.Now())
	return &peerv1.PartyWelcome{
		PartyId:  h.PartyID(),
		Initial:  toWireState(st),
		Audience: toWireAudience(h),
	}, nil
}

func (pc *partyCoordinator) OnLeaveParty(remote identity.NodeID, _ string) {
	pc.mu.Lock()
	h := pc.host
	pc.mu.Unlock()
	if h == nil {
		return
	}
	h.Leave(remote)
	pc.broadcastAudience(h)
}

func (pc *partyCoordinator) OnPartyState(remote identity.NodeID, s *peerv1.PartyState) {
	pc.mu.Lock()
	v, vh := pc.viewer, pc.viewerHost
	pc.mu.Unlock()
	if v == nil || remote != vh {
		return
	}
	v.OnState(fromWireState(s), pc.clock.Now())
}

func (pc *partyCoordinator) OnPartyAudience(identity.NodeID, *peerv1.PartyAudience) {}

func (pc *partyCoordinator) OnPartyInvite(remote identity.NodeID, inv *peerv1.PartyInvite) {
	// Invite is a UI signal; recording it is enough for this slice. A real UI
	// would surface it and let the user call JoinParty. No-op acceptance here.
}

func (pc *partyCoordinator) OnPartyEnded(remote identity.NodeID, _ *peerv1.PartyEnded) {
	pc.mu.Lock()
	if remote == pc.viewerHost {
		pc.viewer = nil // drop to solo playback; player keeps running
	}
	pc.mu.Unlock()
}

// --- heartbeat ---

func (pc *partyCoordinator) heartbeat(h *party.Host, stop chan struct{}) {
	t := time.NewTicker(pc.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			now := pc.clock.Now()
			st, _ := h.Tick(now) // commits any settled seek; bumps seq if so
			env := &peerv1.Envelope{Body: &peerv1.Envelope_PartyState_{PartyState: toWireState(st)}}
			for _, m := range h.Members() {
				_ = pc.send.sendTo(m.NodeID, env)
			}
		}
	}
}

func (pc *partyCoordinator) broadcastAudience(h *party.Host) {
	a := toWireAudience(h)
	env := &peerv1.Envelope{Body: &peerv1.Envelope_PartyAudience_{PartyAudience: a}}
	for _, m := range h.Members() {
		_ = pc.send.sendTo(m.NodeID, env)
	}
}

// viewerDecide is a test seam for the viewer correction (used by the WS loop).
func (pc *partyCoordinator) viewerDecide(posMS int64, playing bool, now time.Time) party.Action {
	pc.mu.Lock()
	v := pc.viewer
	pc.mu.Unlock()
	if v == nil {
		return party.Action{Play: playing, Rate: 1.0}
	}
	return v.Decide(posMS, playing, now)
}

// --- wire conversions ---

func toWireState(s party.State) *peerv1.PartyState {
	return &peerv1.PartyState{PartyId: s.PartyID, Playing: s.Playing, PositionMs: s.PositionMS, Rate: s.Rate, Seq: s.Seq}
}
func fromWireState(s *peerv1.PartyState) party.State {
	return party.State{PartyID: s.GetPartyId(), Playing: s.GetPlaying(), PositionMS: s.GetPositionMs(), Rate: s.GetRate(), Seq: s.GetSeq()}
}
func toWireAudience(h *party.Host) *peerv1.PartyAudience {
	a := &peerv1.PartyAudience{PartyId: h.PartyID()}
	for _, m := range h.Members() {
		a.Members = append(a.Members, &peerv1.AudienceMember{NodeId: string(m.NodeID), DisplayName: m.DisplayName})
	}
	return a
}

// --- WS loop (player <-> engine over the loopback /party socket) ---

type playerMsg struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	PosMS   int64  `json:"posMs"`
	Playing bool   `json:"playing"`
}

// serveWS runs the loopback player loop. Host role: read player events into the
// host engine. Viewer role: read reports and push Actions on a ticker.
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
	if hello.Role == "host" {
		pc.serveHostWS(conn)
		return
	}
	pc.serveViewerWS(conn)
}

func (pc *partyCoordinator) serveHostWS(conn *websocket.Conn) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m playerMsg
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		pc.mu.Lock()
		h := pc.host
		pc.mu.Unlock()
		if h == nil {
			return
		}
		now := pc.clock.Now()
		switch m.Type {
		case "play":
			h.OnPlay(m.PosMS, now)
		case "pause":
			h.OnPause(m.PosMS, now)
		case "seek":
			h.OnSeek(m.PosMS, now)
		case "report":
			h.OnReport(m.PosMS, now)
		}
	}
}

func (pc *partyCoordinator) serveViewerWS(conn *websocket.Conn) {
	reports := make(chan playerMsg, 8)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				close(reports)
				return
			}
			var m playerMsg
			if json.Unmarshal(raw, &m) == nil {
				select {
				case reports <- m:
				default:
				}
			}
		}
	}()
	last := playerMsg{Type: "report"}
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case m, ok := <-reports:
			if !ok {
				return
			}
			last = m
		case <-t.C:
			act := pc.viewerDecide(last.PosMS, last.Playing, pc.clock.Now())
			b, _ := json.Marshal(act)
			if conn.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		}
	}
}
```

- [ ] **Step 3b: Wire the coordinator into `internal/app/node.go`**

Add a `party *partyCoordinator` field to `Node`. In `NewNode`, construct it and make `Node` satisfy `sender`:
```go
	n.party = newPartyCoordinator(n, self.NodeID(), party.RealClock(), party.DefaultConfig())
```
Implement `sender` on `*Node` (it already holds `sessions`):
```go
func (n *Node) sendTo(node identity.NodeID, env *peerv1.Envelope) error {
	s, err := n.session(context.Background(), node)
	if err != nil {
		return err
	}
	return s.SendControl(env)
}

func (n *Node) measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error) {
	s, err := n.session(ctx, node)
	if err != nil {
		return 0, err
	}
	return s.MeasureRTT(ctx)
}
```
In `sessionFor`, install the party handler + the disconnect cleanup right where `SetHandler`/`SetMediaHandler` are installed:
```go
	if n.party != nil {
		s.SetPartyHandler(n.party)
		s.SetOnClose(func(node identity.NodeID) { n.party.OnLeaveParty(node, "") })
	}
```
Register the catalog provider where the catalog is installed (`SetCatalog`): after `n.catalog = svc`, call `svc.SetPartyProvider(n.party)`.

Expose node-level entry points used by the e2e test and (later) a UI:
```go
func (n *Node) StartParty(contentID string) string { return n.party.StartParty(contentID) }

func (n *Node) JoinParty(ctx context.Context, host identity.NodeID, contentID string) error {
	s, err := n.session(ctx, host)
	if err != nil {
		return err
	}
	return n.party.JoinParty(ctx, host, func(ctx context.Context) (*peerv1.PartyWelcome, error) {
		return s.JoinParty(ctx, contentID)
	})
}

// PartyViewerDecide exposes the viewer correction for tests/e2e (a Go actuator).
func (n *Node) PartyViewerDecide(posMS int64, playing bool) party.Action {
	return n.party.viewerDecide(posMS, playing, party.RealClock().Now())
}

// IngestHostPlayer feeds host player events from a Go actuator (tests/e2e).
func (n *Node) IngestHostPlayer(kind string, posMS int64) {
	now := party.RealClock().Now()
	n.party.mu.Lock()
	h := n.party.host
	n.party.mu.Unlock()
	if h == nil {
		return
	}
	switch kind {
	case "play":
		h.OnPlay(posMS, now)
	case "pause":
		h.OnPause(posMS, now)
	case "seek":
		h.OnSeek(posMS, now)
	case "report":
		h.OnReport(posMS, now)
	}
}
```
Add the necessary imports to `node.go`: `"time"`, `"github.com/squall-chua/p2p-hls/internal/party"`, and `peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"` (if not already present). Wire `b.SetPartyHandler(n.party.serveWS)` wherever the bridge is constructed in the app (search for `bridge.New(`); if the bridge is constructed outside `Node`, expose `n.PartyWS()` returning `n.party.serveWS` and register it at the bridge construction site.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestCoordinator -race -v`
Then the whole package: `go test ./internal/app/ -race`
Expected: PASS (new coordinator tests + existing app tests stay green).

- [ ] **Step 5: Commit**

```bash
git add internal/app/party.go internal/app/node.go internal/app/party_test.go
git commit -m "feat(app): party coordinator — heartbeat, ws loops, routing, wiring"
```

---

## Task 8: End-to-end integration (two nodes, real peer session, Go actuator)

Reuse the e2e harness pattern (`test/browse_e2e_test.go`): an in-process signal server, two `app.NewNode`s, `require.Eventually` for presence. The host opens a party; the viewer joins over a real peer session; a Go fake actuator drives convergence.

**Files:**
- Create: `test/party_e2e_test.go`

- [ ] **Step 1: Write the failing test**

`test/party_e2e_test.go`:
```go
package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestWatchPartySyncEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Host shares one Title and allows the viewer.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "The.Matrix.1999.mkv"), []byte("video"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "host.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, e2eProber{}, []string{root}).ScanOnce(ctx))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityPublic), catalog.NewRequests())

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetCatalog(svc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	// Find the host's content ID via Browse.
	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	cid := titles[0].GetContentId()

	// Host opens a party and starts playing; a Go actuator reports its position.
	host.StartParty(cid)
	host.IngestHostPlayer("play", 60_000)

	// Viewer joins; party_id welcome should arrive and seed initial state.
	require.NoError(t, viewer.JoinParty(ctx, idHost.NodeID(), cid))

	// Browse again: the Title is now annotated as a live party with 1 viewer.
	require.Eventually(t, func() bool {
		ts, err := viewer.Browse(ctx, idHost.NodeID())
		return err == nil && len(ts) == 1 && ts[0].GetPartyLive() && ts[0].GetPartyViewers() == 1
	}, 5*time.Second, 100*time.Millisecond)

	// A Go actuator: a far-behind viewer player must be told to SEEK toward ~60s.
	require.Eventually(t, func() bool {
		act := viewer.PartyViewerDecide(0, true)
		return act.Seek && act.SeekMS >= 59_000
	}, 5*time.Second, 100*time.Millisecond)
}
```

> Reuse the existing `e2eProber` test helper (defined in the `test` package for the browse/stream e2e tests). If its name differs, grep `test/` for the prober type used by the browse e2e and use that.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/ -run TestWatchPartySyncEndToEnd -v`
Expected: FAIL until Tasks 0–7 are integrated (then PASS). If it fails on a missing helper, fix the helper reference per the note above.

- [ ] **Step 3: Make it pass**

No new product code should be required — this test exercises Tasks 0–7. If it fails, debug against the real flow (common culprits: party handler not installed on the answerer session; heartbeat not started; `LiveParty` content-ID mismatch). Fix in the relevant task's files, not here.

- [ ] **Step 4: Run the full race suite**

Run: `go test -race ./...`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add test/party_e2e_test.go
git commit -m "test(e2e): watch-party join + sync convergence over real peer session"
```

---

## Self-Review (completed by plan author)

**Spec coverage** — every spec section maps to a task:
- Two wires (loopback WS / peer control channel) → Tasks 5 (WS) + 4 (control-channel dispatch).
- `party` package + Host/Viewer engines → Tasks 1–3.
- Wire protocol (Envelope 17–23, TitleMeta 12–13, capability) → Tasks 0 + 4.
- Clock model (RTT/2 on receipt, gated on playing) → Task 2 (`OnRTT`, `expectedHostPosLocked`).
- Correction state machine (deadband/nudge/seek thresholds) → Task 2.
- Host interpolation + seek debounce → Task 3.
- Party identity `(Host, party_id)`, `content_id` as join reference, `NOT_FOUND` when no live party → Tasks 4 (`JoinParty`) + 7 (`OnJoinParty` rejects unknown content).
- Advertised discovery via TitleMeta → Task 6; invite path (`PartyInvite`) → Tasks 0/4/7.
- Audience + "N watching" → Tasks 3 (`ViewerCount`) + 6 (annotation) + 7 (broadcast).
- Host-ends/leave → Task 7 (`EndParty`/`OnPartyEnded` drop to solo; `OnLeaveParty`).
- Disconnect auto-removal → Task 4 (`OnConnectionStateChange`) + 7 (`SetOnClose` → `OnLeaveParty`).
- Testing strategy (virtual-clock unit + real-session e2e, no browser) → Tasks 2/3 + 8.

**Deferred, intentionally (not gaps):** real `<video>`/hls.js actuator JS (manual demo only — the WS contract is exercised by the Go actuator); host self-playback via its own bridge/media (the Go actuator stands in for the host player; the production wiring of the host's hls.js page is UI work, out of this slice); the NTP fallback (`host_clock_ms` carried but unused).

**Placeholder scan:** no TBD/TODO; every code step has complete code; every run step has an exact command + expected outcome.

**Type consistency:** `State`/`Action`/`Member`/`Config` are defined once (Task 1) and used unchanged in Tasks 2–3, 7. `party.State` ↔ `peerv1.PartyState` conversions live only in Task 7 (`toWireState`/`fromWireState`). `PartyHandler` (Task 4) method set matches the coordinator's implementations (Task 7). Generated oneof wrapper names (`Envelope_PartyState_`, etc.) are recorded in Task 0 and used identically in Task 4.

**Known risk flagged in-plan:** the generated protobuf wrapper names depend on protoc's disambiguation; Task 0 Step 2 records the exact names and Task 4 must use them verbatim.
