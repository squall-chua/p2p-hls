# Slice 5: Mesh Swarm Segment Distribution — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the Viewers of one Watch Party re-serve cached Segments to each other over a gossip mesh, so the Host originates each Segment roughly once instead of once-per-Viewer.

**Architecture:** A pure, deterministic `internal/swarm` engine (gossip target selection, peer-have tracking, RTT-aware source selection — driven by an injected `Clock` and `*rand.Rand`) sits behind an I/O shell in `internal/app` (the swarm session: real dials with glare resolution, gossip sends, bulk Segment pulls, BLAKE3 verification, a windowed byte cache). The Host injects a per-Segment BLAKE3 into the media playlist it serves; Viewers verify peer bytes against it. The change lives entirely behind the existing `Node.Segment` pull seam — the bridge, hls.js, and the party sync plane are untouched.

**Tech Stack:** Go 1.25.1, Pion WebRTC v4, protobuf (`make proto`), `zeebo/blake3`, `math/rand`, testify/require. Reference: spec `docs/superpowers/specs/2026-06-05-slice-5-mesh-swarm-design.md`, ADR `docs/adr/0005-mesh-swarm-distribution.md`, glossary `CONTEXT.md` (Swarm, Segment).

**Conventions:** TDD (failing test first). `go test -race ./...` stays green. Commit after every task. Commit messages: short, point-form, NO `Co-Authored-By`/footer. The pure engine takes `Clock` + `*rand.Rand` so tests are deterministic. Never hold a lock across a network send or `conn.Read/Write`.

**Protoc gotcha:** this `protoc` generates oneof wrappers WITHOUT a trailing underscore (`Envelope_SwarmHave`) EXCEPT the pre-existing `Envelope_Playlist_`. After `make proto`, grep `proto/peer/v1/peer.pb.go` for the real generated names before using them.

**Terminology (CONTEXT.md):** the Host is the Segment **origin** / last-resort source — never "seed/seeder". Viewers **relay**.

---

## Phase 1 — Wire protocol & error foundation

### Task 1: Add `SwarmHave`, `GetSwarmSegment`, and `Error.BUSY` to the proto

**Files:**
- Modify: `proto/peer/v1/peer.proto`
- Regenerate: `proto/peer/v1/peer.pb.go` (via `make proto`)

- [ ] **Step 1: Add the messages and oneof entries**

In `proto/peer/v1/peer.proto`, add two entries to the `Envelope` `oneof body` (after `party_ended = 23;`):

```proto
    SwarmHave swarm_have = 24;
    GetSwarmSegment get_swarm_segment = 25;
```

Add a `BUSY` value to the `Error.Status` enum (after `INTERNAL = 4;`):

```proto
    BUSY = 5;
```

Add the two messages at the end of the file:

```proto
// SwarmHave is the gossip have-map: the windowed set of Segments the sender holds
// for a party, as base_index + bitmap (bit i set => has Segment base_index+i).
message SwarmHave {
  string party_id = 1;
  string rendition = 2;
  uint32 base_index = 3;
  bytes bitmap = 4;
  uint64 epoch = 5; // monotonic; receivers ignore lower-or-equal epochs
}

// GetSwarmSegment requests a cached Segment from a peer Viewer in the same party.
// The bytes return on the bulk channel, correlated by request_id (like GetSegment).
message GetSwarmSegment {
  string party_id = 1;
  string rendition = 2;
  string seg_name = 3;
}
```

- [ ] **Step 2: Regenerate and verify the generated names**

Run: `make proto`
Then run: `grep -nE 'Envelope_SwarmHave|Envelope_GetSwarmSegment|Error_BUSY' proto/peer/v1/peer.pb.go`
Expected: all three names present (no trailing underscore on the two `Envelope_*`).

- [ ] **Step 3: Confirm the build still compiles**

Run: `make build`
Expected: success (no code uses the new messages yet).

- [ ] **Step 4: Commit**

```bash
git add proto/peer/v1/peer.proto proto/peer/v1/peer.pb.go
git commit -m "proto: add SwarmHave, GetSwarmSegment, Error.BUSY"
```

---

### Task 2: Add the `ErrBusy` sentinel and wire it into status mapping

**Files:**
- Modify: `internal/peer/errors.go`
- Test: `internal/peer/errors_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

In `internal/peer/errors_test.go`:

```go
package peer

import (
	"testing"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestBusyRoundTrips(t *testing.T) {
	env := errEnvelope(7, ErrBusy)
	require.Equal(t, peerv1.Error_BUSY, env.GetError().GetStatus())
	require.ErrorIs(t, statusErr(env.GetError()), ErrBusy)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/peer/ -run TestBusyRoundTrips`
Expected: FAIL — `undefined: ErrBusy`.

- [ ] **Step 3: Implement**

In `internal/peer/errors.go`, add to the sentinel `var (...)` block:

```go
	ErrBusy = errors.New("busy")
```

In `statusOf` (the error→Status mapper), add a case mapping `ErrBusy` to `peerv1.Error_BUSY` (mirror the existing `ErrNotFound`/`ErrUnavailable` cases). In `statusErr`, add to the switch:

```go
	case peerv1.Error_BUSY:
		base = ErrBusy
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/peer/ -run TestBusyRoundTrips`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/peer/errors.go internal/peer/errors_test.go
git commit -m "peer: add ErrBusy sentinel + BUSY status mapping"
```

---

## Phase 2 — Host-side Segment integrity (hash injection)

### Task 3: Memoized per-Segment BLAKE3 hasher + playlist tag injection

**Files:**
- Create: `internal/media/hash.go`
- Test: `internal/media/hash_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/media/hash_test.go`:

```go
package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

func TestInjectHashesAddsTagBeforeEachSegment(t *testing.T) {
	dir := t.TempDir()
	seg := []byte("TSDATA-0")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seg00000.ts"), seg, 0o600))

	pl := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg00000.ts\n#EXT-X-ENDLIST\n")
	out := string(InjectHashes(pl, NewSegmentHashes(dir)))

	sum := blake3.Sum256(seg)
	wantHex := hexOf(sum[:])
	require.Contains(t, out, "#EXT-X-P2P-HASH:"+wantHex+"\nseg00000.ts\n")
	// Unknown lines and EXTINF order preserved.
	require.True(t, strings.Index(out, "#EXTINF:4.0,") < strings.Index(out, "#EXT-X-P2P-HASH:"))
}

func TestInjectHashesSkipsMissingSegment(t *testing.T) {
	dir := t.TempDir() // no seg file on disk
	pl := []byte("#EXTINF:4.0,\nseg00000.ts\n")
	out := string(InjectHashes(pl, NewSegmentHashes(dir)))
	require.NotContains(t, out, "#EXT-X-P2P-HASH")
	require.Contains(t, out, "seg00000.ts")
}

func hexOf(b []byte) string { return NewSegmentHashes("").encodeHex(b) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/media/ -run TestInjectHashes`
Expected: FAIL — `undefined: InjectHashes`.

- [ ] **Step 3: Implement**

In `internal/media/hash.go`:

```go
package media

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zeebo/blake3"
)

// SegmentHashes memoizes the BLAKE3 hash of each produced .ts Segment in a dir.
// Segments are immutable once written, so each is hashed at most once.
type SegmentHashes struct {
	dir   string
	mu    sync.Mutex
	cache map[string]string // segName -> lowercase hex
}

// NewSegmentHashes returns a hasher for Segments under dir.
func NewSegmentHashes(dir string) *SegmentHashes {
	return &SegmentHashes{dir: dir, cache: map[string]string{}}
}

func (h *SegmentHashes) encodeHex(b []byte) string { return hex.EncodeToString(b) }

// Hash returns the hex BLAKE3-256 of segName, or ("", false) if it is not on disk.
func (h *SegmentHashes) Hash(segName string) (string, bool) {
	h.mu.Lock()
	if hex, ok := h.cache[segName]; ok {
		h.mu.Unlock()
		return hex, true
	}
	h.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(h.dir, filepath.Base(segName)))
	if err != nil {
		return "", false
	}
	sum := blake3.Sum256(data)
	hexstr := h.encodeHex(sum[:])
	h.mu.Lock()
	h.cache[segName] = hexstr
	h.mu.Unlock()
	return hexstr, true
}

// InjectHashes rewrites a media playlist, inserting "#EXT-X-P2P-HASH:<hex>" on the
// line before each Segment URI whose .ts is on disk. Unknown tags are preserved;
// hls.js ignores the custom tag.
func InjectHashes(playlist []byte, h *SegmentHashes) []byte {
	lines := strings.Split(string(playlist), "\n")
	out := make([]string, 0, len(lines)+8)
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasSuffix(trimmed, ".ts") && !strings.HasPrefix(trimmed, "#") {
			if hexstr, ok := h.Hash(trimmed); ok {
				out = append(out, "#EXT-X-P2P-HASH:"+hexstr)
			}
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/media/ -run TestInjectHashes`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/media/hash.go internal/media/hash_test.go
git commit -m "media: per-Segment BLAKE3 hasher + #EXT-X-P2P-HASH playlist injection"
```

---

### Task 4: Serve the hash-injected media playlist from the Engine

**Files:**
- Modify: `internal/media/engine.go`
- Test: `internal/media/engine_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/media/engine_test.go` (mirror the existing engine test setup that writes `seg00000.ts` + `index.m3u8` into the cache dir, then calls `eng.File(ctx, cid, "index.m3u8")`):

```go
func TestEngineInjectsSegmentHashesIntoIndexPlaylist(t *testing.T) {
	// Arrange a job whose dir already has a produced segment + ffmpeg index.m3u8.
	eng, cid, dir := newEngineWithProducedSegment(t) // helper from existing tests
	_ = dir
	data, _, err := eng.File(context.Background(), cid, "index.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(data), "#EXT-X-P2P-HASH:")
}
```

If no `newEngineWithProducedSegment` helper exists, build the job inline the way `engine_test.go` already does (write `seg00000.ts` and an `index.m3u8` containing `#EXTINF:4.0,\nseg00000.ts` into the job dir, and register the job). Reuse the existing test's construction verbatim.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/media/ -run TestEngineInjectsSegmentHashes`
Expected: FAIL — playlist served raw, no `#EXT-X-P2P-HASH`.

- [ ] **Step 3: Implement**

In `internal/media/engine.go`, give each `job` a hasher and inject when serving `index.m3u8`.

Add to the `job` struct:

```go
	hashes *SegmentHashes
```

In `ensureJob`, after `j := &job{dir: dir, title: title}`, set:

```go
	j.hashes = NewSegmentHashes(dir)
```

In `File`, replace the on-disk read tail so that an `index.m3u8` read is post-processed. After the existing `data, rerr := os.ReadFile(path)` success branch, special-case the media playlist:

```go
	if rerr == nil {
		if name == "index.m3u8" {
			data = InjectHashes(data, j.hashes)
		}
		return data, e.isComplete(j), nil
	}
```

(Leave the `fs.ErrNotExist` / error branches unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/media/ -run TestEngineInjectsSegmentHashes`
Then full package: `go test ./internal/media/`
Expected: PASS; existing tests still green (the master `playlist.m3u8` path is untouched).

- [ ] **Step 5: Commit**

```bash
git add internal/media/engine.go internal/media/engine_test.go
git commit -m "media: inject per-Segment hashes when serving index.m3u8"
```

---

## Phase 3 — Pure swarm engine (`internal/swarm`)

### Task 5: Package skeleton — Clock, Config, segment-index parsing

**Files:**
- Create: `internal/swarm/swarm.go`
- Create: `internal/swarm/clock.go`
- Test: `internal/swarm/swarm_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/swarm_test.go`:

```go
package swarm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	require.Equal(t, 1*time.Second, c.GossipInterval)
	require.GreaterOrEqual(t, c.Fanout, 1)
	require.GreaterOrEqual(t, c.RandomLinks, 1)
}

func TestSegIndex(t *testing.T) {
	i, ok := SegIndex("seg00042.ts")
	require.True(t, ok)
	require.Equal(t, 42, i)
	_, ok = SegIndex("index.m3u8")
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Implement**

`internal/swarm/clock.go`:

```go
package swarm

import "time"

// Clock yields the current time; injected so tests use a virtual clock.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock returns a Clock backed by time.Now.
func RealClock() Clock { return realClock{} }
```

`internal/swarm/swarm.go`:

```go
// Package swarm is the pure, deterministic decision engine for party-scoped mesh
// distribution: peer-have tracking, RTT-aware source selection, and gossip target
// selection. It performs no I/O; the app layer supplies a Clock and a *rand.Rand.
package swarm

import (
	"strconv"
	"strings"
	"time"
)

// Config holds the tunable swarm parameters.
type Config struct {
	GossipInterval time.Duration // have-map gossip tick
	Fanout         int           // gossip targets per round (total)
	RandomLinks    int           // of Fanout, how many are uniformly random
	HaveTTL        time.Duration // peer-have entry expiry
	WindowLag      int           // Segments behind current to retain
	WindowLead     int           // Segments ahead of current to retain
	UploadCap      int           // max concurrent outbound Segment relays
	PullTimeout    time.Duration // per-peer Segment pull timeout
	BusyCooldown   time.Duration // how long a peer stays "busy" after ErrBusy
}

// DefaultConfig returns the slice defaults.
func DefaultConfig() Config {
	return Config{
		GossipInterval: 1 * time.Second,
		Fanout:         3,
		RandomLinks:    1,
		HaveTTL:        10 * time.Second,
		WindowLag:      6,
		WindowLead:     6,
		UploadCap:      4,
		PullTimeout:    5 * time.Second,
		BusyCooldown:   2 * time.Second,
	}
}

// SegIndex parses "seg00042.ts" -> (42, true). Non-segment names yield false.
func SegIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "seg") || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	num := strings.TrimSuffix(strings.TrimPrefix(name, "seg"), ".ts")
	i, err := strconv.Atoi(num)
	if err != nil {
		return 0, false
	}
	return i, true
}

// SegName is the inverse of SegIndex: 42 -> "seg00042.ts".
func SegName(i int) string {
	return "seg" + leftPad(strconv.Itoa(i), 5) + ".ts"
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: package skeleton — Clock, Config, segment-index parsing"
```

---

### Task 6: Local have-set, window eviction, and have-map encoding

**Files:**
- Create: `internal/swarm/haves.go`
- Modify: `internal/swarm/swarm.go` (add the `Swarm` struct + `New`)
- Test: `internal/swarm/haves_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/haves_test.go`:

```go
package swarm

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestSwarm() *Swarm {
	return New("self", RealClock(), DefaultConfig(), rand.New(rand.NewSource(1)))
}

func TestSetHaveAndHaveMsgRoundTrip(t *testing.T) {
	s := newTestSwarm()
	s.SetHave(3)
	s.SetHave(5)
	base, bitmap, epoch := s.HaveMsg()
	require.Equal(t, uint64(2), epoch) // bumped once per SetHave that changed state
	got := decodeBitmap(base, bitmap)
	require.ElementsMatch(t, []int{3, 5}, got)
}

func TestRetainEvictsOutsideWindow(t *testing.T) {
	s := newTestSwarm()
	for i := 0; i < 20; i++ {
		s.SetHave(i)
	}
	// Window around index 10 with lag/lead 6 keeps [4,16].
	s.Retain(4, 16)
	for i := 0; i < 20; i++ {
		require.Equal(t, i >= 4 && i <= 16, s.Have(i), "idx %d", i)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/ -run 'TestSetHave|TestRetain'`
Expected: FAIL — `New`/`SetHave`/`HaveMsg`/`Retain`/`Have`/`decodeBitmap` undefined.

- [ ] **Step 3: Implement**

Add to `internal/swarm/swarm.go` the struct and constructor:

```go
import (
	"math/rand"
	// ...existing imports...
	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Swarm is the pure decision engine for one viewed party. Not safe for concurrent
// use; the owning swarm session serializes access under its own mutex.
type Swarm struct {
	self  identity.NodeID
	clock Clock
	cfg   Config
	rng   *rand.Rand

	have      map[int]bool // local cached+verified Segment indices
	haveEpoch uint64

	peers map[identity.NodeID]*peerInfo
}

type peerInfo struct {
	have     map[int]bool
	epoch    uint64
	rtt      time.Duration
	haveRTT  bool
	lastSeen time.Time
	busyUntil time.Time
	demoted  bool
}

// New constructs an engine for self with the given Clock, Config and RNG source.
func New(self identity.NodeID, clock Clock, cfg Config, rng *rand.Rand) *Swarm {
	return &Swarm{
		self:  self,
		clock: clock,
		cfg:   cfg,
		rng:   rng,
		have:  map[int]bool{},
		peers: map[identity.NodeID]*peerInfo{},
	}
}
```

`internal/swarm/haves.go`:

```go
package swarm

// SetHave records that this node holds Segment idx (cached + verified). Idempotent;
// bumps the have epoch only when the set actually changes.
func (s *Swarm) SetHave(idx int) {
	if s.have[idx] {
		return
	}
	s.have[idx] = true
	s.haveEpoch++
}

// Have reports whether this node holds Segment idx.
func (s *Swarm) Have(idx int) bool { return s.have[idx] }

// Retain drops local haves outside the inclusive window [min,max] and returns the
// evicted indices (for the byte cache to drop). Bumps the epoch if anything changed.
func (s *Swarm) Retain(min, max int) []int {
	var evicted []int
	for idx := range s.have {
		if idx < min || idx > max {
			evicted = append(evicted, idx)
		}
	}
	if len(evicted) > 0 {
		for _, idx := range evicted {
			delete(s.have, idx)
		}
		s.haveEpoch++
	}
	return evicted
}

// HaveMsg encodes the local have-set as (base_index, bitmap, epoch) for SwarmHave.
// base is the lowest held index; bit i of the bitmap corresponds to base+i.
func (s *Swarm) HaveMsg() (base uint32, bitmap []byte, epoch uint64) {
	if len(s.have) == 0 {
		return 0, nil, s.haveEpoch
	}
	min, max := 1<<31, 0
	for idx := range s.have {
		if idx < min {
			min = idx
		}
		if idx > max {
			max = idx
		}
	}
	bits := make([]byte, (max-min)/8+1)
	for idx := range s.have {
		off := idx - min
		bits[off/8] |= 1 << uint(off%8)
	}
	return uint32(min), bits, s.haveEpoch
}

// decodeBitmap is the inverse of the (base,bitmap) encoding (test + peer-merge use).
func decodeBitmap(base uint32, bitmap []byte) []int {
	var out []int
	for i := 0; i < len(bitmap)*8; i++ {
		if bitmap[i/8]&(1<<uint(i%8)) != 0 {
			out = append(out, int(base)+i)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/ -run 'TestSetHave|TestRetain'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: local have-set, window eviction, have-map encoding"
```

---

### Task 7: Peer membership, have-map merge (epoch), and TTL expiry

**Files:**
- Create: `internal/swarm/peers.go`
- Test: `internal/swarm/peers_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/peers_test.go`:

```go
package swarm

import (
	"math/rand"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type vclock struct{ t time.Time }

func (c *vclock) Now() time.Time          { return c.t }
func (c *vclock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newAt(clk Clock) *Swarm {
	return New("self", clk, DefaultConfig(), rand.New(rand.NewSource(1)))
}

func TestPeerHaveMergeRespectsEpoch(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"p1"})

	s.OnPeerHave("p1", 0, bitmapOf(2, 4), 5, clk.Now())
	require.True(t, s.peerHas("p1", 2))
	require.True(t, s.peerHas("p1", 4))

	// Stale epoch ignored.
	s.OnPeerHave("p1", 0, bitmapOf(9), 5, clk.Now())
	require.False(t, s.peerHas("p1", 9))

	// Higher epoch replaces.
	s.OnPeerHave("p1", 0, bitmapOf(9), 6, clk.Now())
	require.True(t, s.peerHas("p1", 9))
	require.False(t, s.peerHas("p1", 2))
}

func TestExpireStaleDropsSilentPeers(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"p1"})
	s.OnPeerHave("p1", 0, bitmapOf(1), 1, clk.Now())
	clk.advance(DefaultConfig().HaveTTL + time.Second)
	s.ExpireStale(clk.Now())
	require.False(t, s.peerHas("p1", 1))
}

func bitmapOf(idxs ...int) []byte {
	max := 0
	for _, i := range idxs {
		if i > max {
			max = i
		}
	}
	b := make([]byte, max/8+1)
	for _, i := range idxs {
		b[i/8] |= 1 << uint(i%8)
	}
	return b
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/ -run 'TestPeerHave|TestExpireStale'`
Expected: FAIL — `SetPeers`/`OnPeerHave`/`peerHas`/`ExpireStale` undefined.

- [ ] **Step 3: Implement**

`internal/swarm/peers.go`:

```go
package swarm

import (
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// SetPeers reconciles the active peer set with the party Audience (minus self).
// New peers get an empty record; departed peers are dropped.
func (s *Swarm) SetPeers(members []identity.NodeID) {
	want := map[identity.NodeID]bool{}
	for _, m := range members {
		if m == s.self {
			continue
		}
		want[m] = true
		if _, ok := s.peers[m]; !ok {
			s.peers[m] = &peerInfo{have: map[int]bool{}}
		}
	}
	for id := range s.peers {
		if !want[id] {
			delete(s.peers, id)
		}
	}
}

// Peers returns the current active peer NodeIDs (excluding demoted peers).
func (s *Swarm) Peers() []identity.NodeID {
	out := make([]identity.NodeID, 0, len(s.peers))
	for id, p := range s.peers {
		if !p.demoted {
			out = append(out, id)
		}
	}
	return out
}

// OnPeerHave merges a received have-map, ignoring epochs <= the last seen for that
// peer. A higher epoch fully replaces the peer's set (have-maps are snapshots).
func (s *Swarm) OnPeerHave(node identity.NodeID, base uint32, bitmap []byte, epoch uint64, now time.Time) {
	p := s.peers[node]
	if p == nil {
		p = &peerInfo{have: map[int]bool{}}
		s.peers[node] = p
	}
	p.lastSeen = now
	if p.have != nil && epoch <= p.epoch && p.epoch != 0 {
		return
	}
	p.epoch = epoch
	p.have = map[int]bool{}
	for _, idx := range decodeBitmap(base, bitmap) {
		p.have[idx] = true
	}
}

func (s *Swarm) peerHas(node identity.NodeID, idx int) bool {
	p := s.peers[node]
	return p != nil && p.have[idx]
}

// ExpireStale drops have-maps from peers unheard-from beyond HaveTTL (keeps the peer
// record so it can re-gossip; just clears its haves).
func (s *Swarm) ExpireStale(now time.Time) {
	for _, p := range s.peers {
		if !p.lastSeen.IsZero() && now.Sub(p.lastSeen) > s.cfg.HaveTTL {
			p.have = map[int]bool{}
			p.epoch = 0
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/ -run 'TestPeerHave|TestExpireStale'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: peer membership, epoch-gated have-map merge, TTL expiry"
```

---

### Task 8: RTT tracking + RTT-aware source selection (busy / demote)

**Files:**
- Create: `internal/swarm/select.go`
- Test: `internal/swarm/select_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/select_test.go`:

```go
package swarm

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestSelectSourcePicksLowestRTTHaver(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"slow", "fast", "none"})
	s.OnPeerHave("slow", 0, bitmapOf(7), 1, clk.Now())
	s.OnPeerHave("fast", 0, bitmapOf(7), 1, clk.Now())
	s.OnRTT("slow", 200*time.Millisecond)
	s.OnRTT("fast", 20*time.Millisecond)

	got, ok := s.SelectSource(7, clk.Now())
	require.True(t, ok)
	require.Equal(t, identity.NodeID("fast"), got)
}

func TestSelectSourceSkipsBusyAndDemoted(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"a", "b"})
	s.OnPeerHave("a", 0, bitmapOf(7), 1, clk.Now())
	s.OnPeerHave("b", 0, bitmapOf(7), 1, clk.Now())
	s.OnRTT("a", 10*time.Millisecond)
	s.OnRTT("b", 50*time.Millisecond)

	s.MarkBusy("a", clk.Now()) // a is fastest but busy
	got, ok := s.SelectSource(7, clk.Now())
	require.True(t, ok)
	require.Equal(t, identity.NodeID("b"), got)

	s.Demote("b")
	_, ok = s.SelectSource(7, clk.Now())
	require.False(t, ok) // no eligible peer -> caller falls back to Host
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/ -run TestSelectSource`
Expected: FAIL — `OnRTT`/`SelectSource`/`MarkBusy`/`Demote` undefined.

- [ ] **Step 3: Implement**

`internal/swarm/select.go`:

```go
package swarm

import (
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// OnRTT records the latest round-trip time to a peer (the ADR-0004 Ping/Pong).
func (s *Swarm) OnRTT(node identity.NodeID, rtt time.Duration) {
	p := s.peers[node]
	if p == nil {
		return
	}
	p.rtt = rtt
	p.haveRTT = true
}

// MarkBusy records that a peer rejected a pull with ErrBusy; it is skipped for
// BusyCooldown. The peer is honest (just loaded) so it is NOT demoted.
func (s *Swarm) MarkBusy(node identity.NodeID, now time.Time) {
	if p := s.peers[node]; p != nil {
		p.busyUntil = now.Add(s.cfg.BusyCooldown)
	}
}

// Demote drops a peer from selection for the rest of the party (have-map lie or
// poison). It stays in the peer set but is never chosen as a source again.
func (s *Swarm) Demote(node identity.NodeID) {
	if p := s.peers[node]; p != nil {
		p.demoted = true
		p.have = map[int]bool{}
	}
}

// SelectSource returns the lowest-RTT eligible peer that advertises Segment idx, or
// (_, false) if none — in which case the caller pulls from the Host. Eligible =
// not demoted, not in busy cooldown, advertises idx. Peers without an RTT sample
// rank after those with one.
func (s *Swarm) SelectSource(idx int, now time.Time) (identity.NodeID, bool) {
	var best identity.NodeID
	var bestRTT time.Duration
	found := false
	for id, p := range s.peers {
		if p.demoted || !p.have[idx] {
			continue
		}
		if now.Before(p.busyUntil) {
			continue
		}
		rtt := p.rtt
		if !p.haveRTT {
			rtt = time.Hour // deprioritize unmeasured peers
		}
		if !found || rtt < bestRTT {
			found = true
			best = id
			bestRTT = rtt
		}
	}
	return best, found
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/ -run TestSelectSource`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: RTT tracking + RTT-aware source selection (busy/demote)"
```

---

### Task 9: Gossip target selection (latency-biased + ≥1 random, seeded RNG)

**Files:**
- Modify: `internal/swarm/select.go`
- Test: `internal/swarm/gossip_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/gossip_test.go`:

```go
package swarm

import (
	"math/rand"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestGossipTargetsAreDeterministicForSeed(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	mk := func() *Swarm {
		s := New("self", clk, DefaultConfig(), rand.New(rand.NewSource(42)))
		s.SetPeers([]identity.NodeID{"a", "b", "c", "d", "e"})
		for _, id := range []identity.NodeID{"a", "b", "c", "d", "e"} {
			s.OnRTT(id, 10*time.Millisecond)
		}
		return s
	}
	require.Equal(t, mk().GossipTargets(), mk().GossipTargets()) // same seed => same picks
}

func TestGossipTargetsRespectFanoutAndExcludeSelf(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := New("self", clk, DefaultConfig(), rand.New(rand.NewSource(7)))
	s.SetPeers([]identity.NodeID{"a", "b", "c", "d", "e"})
	tg := s.GossipTargets()
	require.LessOrEqual(t, len(tg), DefaultConfig().Fanout)
	require.NotContains(t, tg, identity.NodeID("self"))
	seen := map[identity.NodeID]bool{}
	for _, id := range tg {
		require.False(t, seen[id], "no duplicate targets")
		seen[id] = true
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/ -run TestGossipTargets`
Expected: FAIL — `GossipTargets` undefined.

- [ ] **Step 3: Implement**

Add to `internal/swarm/select.go`:

```go
import "sort"

// GossipTargets picks up to Fanout peers to push a have-map to this round: the
// (Fanout-RandomLinks) lowest-RTT peers plus RandomLinks uniformly-random others.
// The random links preserve epidemic mixing so proximity bias cannot cluster the
// mesh. Uses the injected RNG, so a seeded RNG makes selection deterministic.
func (s *Swarm) GossipTargets() []identity.NodeID {
	peers := s.Peers() // excludes demoted; excludes self (not in peers)
	if len(peers) <= s.cfg.Fanout {
		return peers
	}
	// Stable order for determinism before RTT sort / shuffle.
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })

	near := s.cfg.Fanout - s.cfg.RandomLinks
	if near < 0 {
		near = 0
	}
	byRTT := make([]identity.NodeID, len(peers))
	copy(byRTT, peers)
	sort.SliceStable(byRTT, func(i, j int) bool {
		return s.rttOf(byRTT[i]) < s.rttOf(byRTT[j])
	})

	chosen := map[identity.NodeID]bool{}
	out := []identity.NodeID{}
	for i := 0; i < near && i < len(byRTT); i++ {
		chosen[byRTT[i]] = true
		out = append(out, byRTT[i])
	}
	// Random links from the remaining peers.
	var pool []identity.NodeID
	for _, id := range peers {
		if !chosen[id] {
			pool = append(pool, id)
		}
	}
	for i := 0; i < s.cfg.RandomLinks && len(pool) > 0; i++ {
		k := s.rng.Intn(len(pool))
		out = append(out, pool[k])
		pool = append(pool[:k], pool[k+1:]...)
	}
	return out
}

func (s *Swarm) rttOf(id identity.NodeID) time.Duration {
	if p := s.peers[id]; p != nil && p.haveRTT {
		return p.rtt
	}
	return time.Hour
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/ -run TestGossipTargets`
Then full package with race: `go test -race ./internal/swarm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: latency-biased + random gossip target selection (seeded RNG)"
```

---

### Task 10: Integrity helpers — parse playlist hashes + verify a Segment

**Files:**
- Create: `internal/swarm/integrity.go`
- Test: `internal/swarm/integrity_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/swarm/integrity_test.go`:

```go
package swarm

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

func TestParseHashes(t *testing.T) {
	pl := []byte("#EXTM3U\n#EXTINF:4.0,\n#EXT-X-P2P-HASH:abc123\nseg00000.ts\n#EXTINF:4.0,\n#EXT-X-P2P-HASH:def456\nseg00001.ts\n")
	m := ParseHashes(pl)
	require.Equal(t, "abc123", m["seg00000.ts"])
	require.Equal(t, "def456", m["seg00001.ts"])
}

func TestVerifySegment(t *testing.T) {
	data := []byte("payload")
	sum := blake3.Sum256(data)
	good := hex.EncodeToString(sum[:])
	require.True(t, VerifySegment(data, good))
	require.False(t, VerifySegment(data, "deadbeef"))
	require.False(t, VerifySegment([]byte("tampered"), good))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/swarm/ -run 'TestParseHashes|TestVerifySegment'`
Expected: FAIL — `ParseHashes`/`VerifySegment` undefined.

- [ ] **Step 3: Implement**

`internal/swarm/integrity.go`:

```go
package swarm

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/zeebo/blake3"
)

const hashTag = "#EXT-X-P2P-HASH:"

// ParseHashes extracts segName -> hex BLAKE3 from a Host-served media playlist. A
// hash tag applies to the next Segment URI line.
func ParseHashes(playlist []byte) map[string]string {
	out := map[string]string{}
	var pending string
	for _, ln := range strings.Split(string(playlist), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, hashTag):
			pending = strings.TrimPrefix(t, hashTag)
		case strings.HasSuffix(t, ".ts") && !strings.HasPrefix(t, "#"):
			if pending != "" {
				out[t] = pending
				pending = ""
			}
		}
	}
	return out
}

// VerifySegment reports whether data's BLAKE3-256 equals expectHex (constant-time).
func VerifySegment(data []byte, expectHex string) bool {
	want, err := hex.DecodeString(expectHex)
	if err != nil || len(want) == 0 {
		return false
	}
	sum := blake3.Sum256(data)
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/swarm/ -run 'TestParseHashes|TestVerifySegment'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/swarm/
git commit -m "swarm: playlist hash parsing + constant-time Segment verification"
```

---

## Phase 4 — Peer session plumbing

### Task 11: `SwarmHandler` interface, handler accessors, bind cases, client methods

**Files:**
- Create: `internal/peer/swarm.go`
- Modify: `internal/peer/session.go` (struct field + `bindControl` switch)
- Test: `internal/peer/swarm_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/peer/swarm_test.go`:

```go
package peer

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

type fakeSwarm struct {
	gotHave *peerv1.SwarmHave
	segReq  *peerv1.GetSwarmSegment
}

func (f *fakeSwarm) OnSwarmHave(_ identity.NodeID, h *peerv1.SwarmHave) { f.gotHave = h }
func (f *fakeSwarm) SwarmSegment(_ identity.NodeID, r *peerv1.GetSwarmSegment) ([]byte, error) {
	f.segReq = r
	return []byte("SEG"), nil
}

func TestSetSwarmHandlerStored(t *testing.T) {
	s := &Session{}
	h := &fakeSwarm{}
	s.SetSwarmHandler(h)
	require.Equal(t, SwarmHandler(h), s.currentSwarmHandler())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/peer/ -run TestSetSwarmHandler`
Expected: FAIL — `SwarmHandler`/`SetSwarmHandler`/`currentSwarmHandler` undefined.

- [ ] **Step 3: Implement**

`internal/peer/swarm.go`:

```go
package peer

import (
	"context"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// SwarmHandler answers inbound swarm messages from a peer Viewer in a party.
type SwarmHandler interface {
	OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave)
	SwarmSegment(remote identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error)
}

// SetSwarmHandler installs the handler for inbound SwarmHave / GetSwarmSegment.
func (s *Session) SetSwarmHandler(h SwarmHandler) {
	s.mu.Lock()
	s.swarmHandler = h
	s.mu.Unlock()
}

func (s *Session) currentSwarmHandler() SwarmHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.swarmHandler
}

// SendSwarmHave gossips a have-map to this peer over the control channel.
func (s *Session) SendSwarmHave(h *peerv1.SwarmHave) error {
	return s.send(&peerv1.Envelope{Body: &peerv1.Envelope_SwarmHave{SwarmHave: h}})
}

// GetSwarmSegment pulls a cached Segment from this peer over the bulk channel.
func (s *Session) GetSwarmSegment(ctx context.Context, req *peerv1.GetSwarmSegment) ([]byte, error) {
	return s.fetchBulk(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetSwarmSegment{GetSwarmSegment: req}})
}

// handleGetSwarmSegment serves a Segment to a peer. It runs on a goroutine so a slow
// bulk upload never head-of-line-blocks this Viewer's gossip or party sync.
func (s *Session) handleGetSwarmSegment(reqID uint64, req *peerv1.GetSwarmSegment) {
	h := s.currentSwarmHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		data, err := h.SwarmSegment(s.remote, req)
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		if serr := s.sendBulk(reqID, data); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}
```

In `internal/peer/session.go`, add the struct field near `partyHandler`:

```go
	swarmHandler SwarmHandler
```

In `bindControl`'s switch, add two cases (next to the party cases):

```go
	case *peerv1.Envelope_SwarmHave:
		if h := s.currentSwarmHandler(); h != nil {
			h.OnSwarmHave(s.remote, body.SwarmHave)
		}
	case *peerv1.Envelope_GetSwarmSegment:
		s.handleGetSwarmSegment(env.RequestId, body.GetSwarmSegment)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/peer/ -run TestSetSwarmHandler`
Then: `go build ./...`
Expected: PASS, builds.

- [ ] **Step 5: Commit**

```bash
git add internal/peer/swarm.go internal/peer/session.go internal/peer/swarm_test.go
git commit -m "peer: SwarmHandler + SwarmHave/GetSwarmSegment routing (async serve)"
```

---

### Task 12: Dispatch existing Segment/Download serving to goroutines (concurrency fix)

**Files:**
- Modify: `internal/peer/session.go` (`handleGetSegment`, `handleDownload`)

This is the deferred `TODO(slice-4)` at `session.go:632`: serving must not run inline on the control read loop, or one transfer stalls all control messages — a relaying Viewer must serve while it plays, gossips, and syncs.

- [ ] **Step 1: Wrap `handleGetSegment` body in a goroutine**

Replace the body of `handleGetSegment` so the media read + bulk send run on a goroutine:

```go
func (s *Session) handleGetSegment(reqID uint64, req *peerv1.GetSegment) {
	h := s.currentMediaHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		data, err := h.Segment(s.remote, req.GetContentId(), req.GetName())
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		if serr := s.sendBulk(reqID, data); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}
```

- [ ] **Step 2: Wrap `handleDownload` body in a goroutine**

```go
func (s *Session) handleDownload(reqID uint64, req *peerv1.Download) {
	h := s.currentMediaHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		rc, _, err := h.OpenFile(s.remote, req.GetContentId())
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		defer rc.Close()
		if serr := s.sendBulkReader(reqID, rc); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}
```

- [ ] **Step 3: Remove the now-stale TODO comment**

Delete the `// TODO(slice-4): handleGetSegment/handleDownload run inline ...` comment block above `handleGetSegment` (it is now resolved).

- [ ] **Step 4: Run the peer tests with race detector**

Run: `go test -race ./internal/peer/`
Expected: PASS (existing segment/download tests still pass; serving is now concurrent).

- [ ] **Step 5: Commit**

```bash
git add internal/peer/session.go
git commit -m "peer: serve Segment/Download on goroutines (resolve slice-4 HOL-block TODO)"
```

---

## Phase 5 — Decentralized connection setup (relay nudge + glare)

### Task 13: Tagged relay envelope + swarm-dial payload

**Files:**
- Create: `internal/peer/relay.go`
- Modify: `internal/app/node.go` (`relaySignaler.SendSignal` to wrap as a tagged envelope)
- Test: `internal/peer/relay_test.go`

The current relay payload is a bare `SignedSignal` JSON, decoded directly in `routeRelays`. To carry a second payload kind (the glare "dial-me" nudge) we wrap every relay in a tagged envelope. (Local-only app, single version, all Nodes upgrade together — no wire-compat concern.)

- [ ] **Step 1: Write the failing test**

In `internal/peer/relay_test.go`:

```go
package peer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelayEnvelopeRoundTrip(t *testing.T) {
	raw, err := EncodeRelay(RelayKindSwarmDial, SwarmDial{PartyID: "p1", From: "nodeA"})
	require.NoError(t, err)
	kind, data, err := DecodeRelay(raw)
	require.NoError(t, err)
	require.Equal(t, RelayKindSwarmDial, kind)
	d, err := DecodeSwarmDial(data)
	require.NoError(t, err)
	require.Equal(t, "p1", d.PartyID)
	require.Equal(t, "nodeA", d.From)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/peer/ -run TestRelayEnvelope`
Expected: FAIL — `EncodeRelay` etc. undefined.

- [ ] **Step 3: Implement**

`internal/peer/relay.go`:

```go
package peer

import "encoding/json"

// RelayKind discriminates the opaque payloads carried inside a signaling Relay.
type RelayKind string

const (
	RelayKindSignal    RelayKind = "signal" // a SignedSignal (SDP/ICE)
	RelayKindSwarmDial RelayKind = "dial"   // a SwarmDial "dial-me" nudge
)

// RelayEnvelope wraps a relay payload with its kind.
type RelayEnvelope struct {
	Kind RelayKind       `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// SwarmDial nudges a lower-NodeID peer to dial the sender, resolving the mesh glare
// hazard (only the lower NodeID dials).
type SwarmDial struct {
	PartyID string `json:"party_id"`
	From    string `json:"from"`
}

// EncodeRelay wraps v as a tagged relay payload.
func EncodeRelay(kind RelayKind, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(RelayEnvelope{Kind: kind, Data: data})
}

// DecodeRelay unwraps a tagged relay payload.
func DecodeRelay(raw []byte) (RelayKind, json.RawMessage, error) {
	var e RelayEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", nil, err
	}
	return e.Kind, e.Data, nil
}

// DecodeSwarmDial decodes a RelayKindSwarmDial payload.
func DecodeSwarmDial(data json.RawMessage) (SwarmDial, error) {
	var d SwarmDial
	err := json.Unmarshal(data, &d)
	return d, err
}
```

In `internal/app/node.go`, change `relaySignaler.SendSignal` to wrap the signal:

```go
func (r relaySignaler) SendSignal(to identity.NodeID, s peer.SignedSignal) error {
	payload, err := s.Encode()
	if err != nil {
		return err
	}
	wrapped, err := peer.EncodeRelay(peer.RelayKindSignal, json.RawMessage(payload))
	if err != nil {
		return err
	}
	return r.client.SendRelay(to, wrapped)
}
```

Add `"encoding/json"` to `node.go` imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/peer/ -run TestRelayEnvelope`
Expected: PASS. (`routeRelays` is updated in the next task; build may be temporarily inconsistent — proceed to Task 14 before running the full app build.)

- [ ] **Step 5: Commit**

```bash
git add internal/peer/relay.go internal/peer/relay_test.go internal/app/node.go
git commit -m "peer: tagged relay envelope + SwarmDial nudge payload"
```

---

### Task 14: `routeRelays` dispatch + glare-aware `ensurePeer`

**Files:**
- Modify: `internal/app/node.go` (`routeRelays`, add `ensurePeer` + `OnSwarmDial` plumbing)
- Test: `internal/app/node_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

In `internal/app/node_test.go`:

```go
package app

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestShouldDialIsLowerNodeID(t *testing.T) {
	require.True(t, shouldDial(identity.NodeID("aaa"), identity.NodeID("bbb")))  // self < peer
	require.False(t, shouldDial(identity.NodeID("ccc"), identity.NodeID("bbb"))) // self > peer
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestShouldDial`
Expected: FAIL — `shouldDial` undefined.

- [ ] **Step 3: Implement**

In `internal/app/node.go`:

```go
// shouldDial implements the glare rule: the lower NodeID is the initiator.
func shouldDial(self, peer identity.NodeID) bool { return self < peer }

// ensurePeer makes sure a session to node exists, applying glare resolution. If we
// are the lower NodeID we dial; otherwise we send a SwarmDial nudge so the peer
// (the lower NodeID) dials us. Non-blocking for the nudge path.
func (n *Node) ensurePeer(node identity.NodeID) error {
	n.mu.Lock()
	_, have := n.sessions[node]
	n.mu.Unlock()
	if have {
		return nil
	}
	if shouldDial(n.self.NodeID(), node) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = n.Dial(ctx, node)
		}()
		return nil
	}
	payload, err := peer.EncodeRelay(peer.RelayKindSwarmDial,
		peer.SwarmDial{PartyID: n.party.activePartyID(), From: string(n.self.NodeID())})
	if err != nil {
		return err
	}
	return n.client.SendRelay(node, payload)
}
```

Rewrite `routeRelays` to unwrap the tagged envelope and dispatch by kind:

```go
func (n *Node) routeRelays() {
	for rel := range n.client.Relays() {
		from := identity.NodeID(rel.From)
		kind, data, err := peer.DecodeRelay(rel.Payload)
		if err != nil {
			continue
		}
		switch kind {
		case peer.RelayKindSignal:
			sig, derr := peer.DecodeSignedSignal(data)
			if derr != nil {
				continue
			}
			sess, serr := n.sessionFor(from, false)
			if serr != nil {
				slog.Warn("failed to create session for inbound relay", "from", from, "err", serr)
				continue
			}
			_ = sess.HandleSignal(sig)
		case peer.RelayKindSwarmDial:
			// A peer wants us to dial it. We are the lower NodeID for this edge.
			if shouldDial(n.self.NodeID(), from) {
				go func(to identity.NodeID) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, _ = n.Dial(ctx, to)
				}(from)
			}
		}
	}
}
```

Add `activePartyID()` to the party coordinator (`internal/app/party.go`) returning the viewed party id (empty if none):

```go
func (pc *partyCoordinator) activePartyID() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.swarm != nil {
		return pc.swarm.partyID
	}
	return ""
}
```

(`pc.swarm` is introduced in Task 17; if implementing strictly in order, stub `activePartyID` to `return ""` now and complete it in Task 17.)

Note: `peer.DecodeSignedSignal` now receives the unwrapped `data` (`json.RawMessage`). Confirm its signature accepts `[]byte` (it does); pass `data` directly.

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/app/ -run TestShouldDial`
Run: `go build ./...`
Expected: PASS, builds (with the `activePartyID` stub if Task 17 not yet done).

- [ ] **Step 5: Commit**

```bash
git add internal/app/node.go internal/app/party.go internal/app/node_test.go
git commit -m "app: glare-aware ensurePeer + relay dispatch by kind"
```

---

## Phase 6 — Swarm session (I/O shell) + the pull seam

### Task 15: Windowed byte cache for verified Segments

**Files:**
- Create: `internal/app/swarm_cache.go`
- Test: `internal/app/swarm_cache_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/app/swarm_cache_test.go`:

```go
package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSegCachePutGetRetain(t *testing.T) {
	c := newSegCache()
	c.put(3, []byte("three"))
	c.put(10, []byte("ten"))
	got, ok := c.get(3)
	require.True(t, ok)
	require.Equal(t, []byte("three"), got)

	c.retain(5, 15) // drop indices outside [5,15]
	_, ok = c.get(3)
	require.False(t, ok)
	_, ok = c.get(10)
	require.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestSegCache`
Expected: FAIL — `newSegCache` undefined.

- [ ] **Step 3: Implement**

`internal/app/swarm_cache.go`:

```go
package app

import "sync"

// segCache holds verified Segment bytes for the active viewed party, keyed by
// Segment index. Bounded by the swarm window via retain().
type segCache struct {
	mu   sync.Mutex
	data map[int][]byte
}

func newSegCache() *segCache { return &segCache{data: map[int][]byte{}} }

func (c *segCache) put(idx int, b []byte) {
	c.mu.Lock()
	c.data[idx] = b
	c.mu.Unlock()
}

func (c *segCache) get(idx int) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.data[idx]
	return b, ok
}

// retain drops cached Segments outside the inclusive window [min,max].
func (c *segCache) retain(min, max int) {
	c.mu.Lock()
	for idx := range c.data {
		if idx < min || idx > max {
			delete(c.data, idx)
		}
	}
	c.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestSegCache`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/swarm_cache.go internal/app/swarm_cache_test.go
git commit -m "app: windowed verified-Segment byte cache"
```

---

### Task 16: Swarm transport interface + Node implementations

**Files:**
- Modify: `internal/app/party.go` (extend the `sender` interface OR add a `swarmTransport` interface)
- Modify: `internal/app/node.go` (implement the new methods)
- Test: `internal/app/node_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/app/node_test.go`:

```go
func TestNodeImplementsSwarmTransport(t *testing.T) {
	var _ swarmTransport = (*Node)(nil) // compile-time interface satisfaction
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestNodeImplementsSwarmTransport`
Expected: FAIL — `swarmTransport` undefined / not satisfied.

- [ ] **Step 3: Implement**

In `internal/app/party.go`, define the transport the swarm session needs (superset of the existing `sender`):

```go
type swarmTransport interface {
	sendTo(node identity.NodeID, env *peerv1.Envelope) error
	measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error)
	fetchSwarmSegment(ctx context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error)
	ensurePeer(node identity.NodeID) error
	hostPlaylist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error)
	hostSegment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error)
}
```

In `internal/app/node.go`, add the missing methods (`sendTo`, `measureRTT`, `ensurePeer` already exist):

```go
// fetchSwarmSegment pulls a Segment from a peer Viewer over its bulk channel.
func (n *Node) fetchSwarmSegment(ctx context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	s, err := n.session(ctx, node)
	if err != nil {
		return nil, err
	}
	return s.GetSwarmSegment(ctx, req)
}

// hostPlaylist fetches a playlist directly from the Host (the integrity trust anchor).
func (n *Node) hostPlaylist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error) {
	s, err := n.session(ctx, host)
	if err != nil {
		return nil, err
	}
	data, _, _, err := s.GetPlaylist(ctx, contentID, name)
	return data, err
}

// hostSegment fetches a Segment directly from the Host (last-resort source).
func (n *Node) hostSegment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error) {
	s, err := n.session(ctx, host)
	if err != nil {
		return nil, err
	}
	return s.GetSegment(ctx, contentID, name)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestNodeImplementsSwarmTransport`
Run: `go build ./...`
Expected: PASS, builds.

- [ ] **Step 5: Commit**

```bash
git add internal/app/party.go internal/app/node.go internal/app/node_test.go
git commit -m "app: swarmTransport interface + Node bulk/host fetch implementations"
```

---

### Task 17: The swarm session — gossip loop, serve-from-cache, the FetchSegment pull

**Files:**
- Create: `internal/app/swarm_session.go`
- Modify: `internal/app/party.go` (add `swarm *swarmSession` field; create on join; tear down on end/leave; route SwarmHandler; bootstrap peers from PartyAudience; `swarmFor` accessor)
- Modify: `internal/app/node.go` (install SwarmHandler on sessions; make `Node.Segment` swarm-aware)
- Test: `internal/app/swarm_session_test.go`

- [ ] **Step 1: Write the failing test (serve-from-cache + verify pull)**

In `internal/app/swarm_session_test.go`:

```go
package app

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

// fakeTransport records sends and serves canned peer/host segments.
type fakeTransport struct {
	peerSeg map[string][]byte // node|segName -> bytes
	hostSeg map[string][]byte // segName -> bytes
	playlist []byte
}

func (f *fakeTransport) sendTo(identity.NodeID, *peerv1.Envelope) error { return nil }
func (f *fakeTransport) measureRTT(context.Context, identity.NodeID) (time.Duration, error) {
	return 10 * time.Millisecond, nil
}
func (f *fakeTransport) fetchSwarmSegment(_ context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	b, ok := f.peerSeg[string(node)+"|"+req.GetSegName()]
	if !ok {
		return nil, peer.ErrNotFound
	}
	return b, nil
}
func (f *fakeTransport) ensurePeer(identity.NodeID) error { return nil }
func (f *fakeTransport) hostPlaylist(context.Context, identity.NodeID, string, string) ([]byte, error) {
	return f.playlist, nil
}
func (f *fakeTransport) hostSegment(_ context.Context, _ identity.NodeID, _ , name string) ([]byte, error) {
	b, ok := f.hostSeg[name]
	if !ok {
		return nil, peer.ErrNotFound
	}
	return b, nil
}

func plHash(seg []byte, name string) []byte {
	sum := blake3.Sum256(seg)
	return []byte("#EXTINF:4.0,\n#EXT-X-P2P-HASH:" + hex.EncodeToString(sum[:]) + "\n" + name + "\n")
}

func TestFetchSegmentPrefersVerifiedPeerThenCaches(t *testing.T) {
	seg := []byte("good-segment-bytes")
	ft := &fakeTransport{
		peerSeg:  map[string][]byte{"peerX|seg00002.ts": seg},
		hostSeg:  map[string][]byte{},
		playlist: plHash(seg, "seg00002.ts"),
	}
	ss := newSwarmSession(ft, "self", "host", "cid", swarm.RealClock(), swarm.DefaultConfig())
	ss.setPeers([]identity.NodeID{"peerX"})
	ss.eng.OnPeerHave("peerX", 2, []byte{0x01}, 1, time.Now())

	got, err := ss.FetchSegment(context.Background(), "seg00002.ts")
	require.NoError(t, err)
	require.Equal(t, seg, got)

	// Cached now: served locally and advertised.
	require.True(t, ss.eng.Have(2))
	cached, ok := ss.cache.get(2)
	require.True(t, ok)
	require.Equal(t, seg, cached)
}

func TestFetchSegmentRejectsPoisonAndDemotesThenHostFallback(t *testing.T) {
	good := []byte("the-real-bytes")
	ft := &fakeTransport{
		peerSeg:  map[string][]byte{"liar|seg00002.ts": []byte("POISON")},
		hostSeg:  map[string][]byte{"seg00002.ts": good},
		playlist: plHash(good, "seg00002.ts"),
	}
	ss := newSwarmSession(ft, "self", "host", "cid", swarm.RealClock(), swarm.DefaultConfig())
	ss.setPeers([]identity.NodeID{"liar"})
	ss.eng.OnPeerHave("liar", 2, []byte{0x01}, 1, time.Now())

	got, err := ss.FetchSegment(context.Background(), "seg00002.ts")
	require.NoError(t, err)
	require.Equal(t, good, got) // poison rejected, Host fallback served the real bytes
	_, ok := ss.eng.peerHasForTest("liar", 2)
	require.False(t, ok) // demoted

	_ = errors.Is // keep import
}
```

Add a tiny test accessor to the swarm engine so the app test can assert demotion. In `internal/swarm/peers.go`:

```go
// peerHasForTest exposes peer-have state for cross-package tests.
func (s *Swarm) peerHasForTest(node identity.NodeID, idx int) (bool, bool) {
	p := s.peers[node]
	if p == nil || p.demoted {
		return false, false
	}
	return p.have[idx], true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestFetchSegment`
Expected: FAIL — `newSwarmSession` undefined.

- [ ] **Step 3: Implement the swarm session**

`internal/app/swarm_session.go`:

```go
package app

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

const swarmRendition = "v0" // single rendition this slice; ABR will key by real rendition

// swarmSession is the I/O shell around the pure swarm engine for one viewed party.
type swarmSession struct {
	t         swarmTransport
	self      identity.NodeID
	host      identity.NodeID
	contentID string
	partyID   string
	clock     swarm.Clock
	cfg       swarm.Config

	mu        sync.Mutex
	eng       *swarm.Swarm
	cache     *segCache
	hashes    map[string]string // segName -> hex (from Host playlist)
	lastIdx   int               // most-recently-requested Segment index (window center)
	uploadSem chan struct{}
	stop      chan struct{}
}

func newSwarmSession(t swarmTransport, self, host identity.NodeID, contentID string,
	clk swarm.Clock, cfg swarm.Config) *swarmSession {
	return &swarmSession{
		t:         t,
		self:      self,
		host:      host,
		contentID: contentID,
		clock:     clk,
		cfg:       cfg,
		eng:       swarm.New(self, clk, cfg, rand.New(rand.NewSource(seedFor(self)))),
		cache:     newSegCache(),
		hashes:    map[string]string{},
		uploadSem: make(chan struct{}, cfg.UploadCap),
		stop:      make(chan struct{}),
	}
}

// seedFor derives a deterministic-per-Node RNG seed from the NodeID so different
// Nodes pick different random links while each Node is reproducible in tests.
func seedFor(id identity.NodeID) int64 {
	var h int64 = 1469598103934665603
	for _, b := range []byte(id) {
		h ^= int64(b)
		h *= 1099511628211
	}
	if h < 0 {
		h = -h
	}
	return h
}

func (ss *swarmSession) setPartyID(id string) { ss.mu.Lock(); ss.partyID = id; ss.mu.Unlock() }

// setPeers updates the engine's peer set from the party Audience.
func (ss *swarmSession) setPeers(members []identity.NodeID) {
	ss.mu.Lock()
	ss.eng.SetPeers(members)
	ss.mu.Unlock()
	for _, m := range members {
		if m != ss.self {
			_ = ss.t.ensurePeer(m)
		}
	}
}

// OnSwarmHave merges a peer's gossiped have-map.
func (ss *swarmSession) OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave) {
	ss.mu.Lock()
	ss.eng.OnPeerHave(remote, h.GetBaseIndex(), h.GetBitmap(), h.GetEpoch(), ss.clock.Now())
	ss.mu.Unlock()
}

// SwarmSegment serves a cached Segment to a peer, bounded by the upload cap.
func (ss *swarmSession) SwarmSegment(_ identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	idx, ok := swarm.SegIndex(req.GetSegName())
	if !ok {
		return nil, peer.ErrNotFound
	}
	select {
	case ss.uploadSem <- struct{}{}:
		defer func() { <-ss.uploadSem }()
	default:
		return nil, peer.ErrBusy
	}
	if b, ok := ss.cache.get(idx); ok {
		return b, nil
	}
	return nil, peer.ErrNotFound
}

// FetchSegment is the pull seam: serve from cache, else lowest-RTT verified peer,
// else the Host. Verified bytes are cached and advertised.
func (ss *swarmSession) FetchSegment(ctx context.Context, segName string) ([]byte, error) {
	idx, ok := swarm.SegIndex(segName)
	if !ok {
		// Non-segment (e.g. a .vtt): always go to the Host.
		return ss.t.hostSegment(ctx, ss.host, ss.contentID, segName)
	}
	ss.mu.Lock()
	ss.lastIdx = idx
	if b, ok := ss.cache.get(idx); ok {
		ss.mu.Unlock()
		return b, nil
	}
	ss.mu.Unlock()

	want := ss.expectedHash(ctx, segName)

	// Try peers in RTT order until one verifies.
	for {
		ss.mu.Lock()
		src, ok := ss.eng.SelectSource(idx, ss.clock.Now())
		ss.mu.Unlock()
		if !ok {
			break
		}
		data, err := ss.t.fetchSwarmSegment(ctx, src, &peerv1.GetSwarmSegment{
			PartyId: ss.partyID, Rendition: swarmRendition, SegName: segName,
		})
		if err != nil {
			ss.mu.Lock()
			if isBusy(err) {
				ss.eng.MarkBusy(src, ss.clock.Now())
			} else {
				ss.eng.Demote(src)
			}
			ss.mu.Unlock()
			continue
		}
		if want != "" && !swarm.VerifySegment(data, want) {
			ss.mu.Lock()
			ss.eng.Demote(src)
			ss.mu.Unlock()
			continue
		}
		ss.store(idx, data)
		return data, nil
	}

	// Host fallback (last-resort source).
	data, err := ss.t.hostSegment(ctx, ss.host, ss.contentID, segName)
	if err != nil {
		return nil, err
	}
	if want != "" && !swarm.VerifySegment(data, want) {
		return nil, peer.ErrUnavailable // Host bytes failed their own hash; refuse
	}
	ss.store(idx, data)
	return data, nil
}

func (ss *swarmSession) store(idx int, data []byte) {
	ss.cache.put(idx, data)
	ss.mu.Lock()
	ss.eng.SetHave(idx)
	lo := ss.lastIdx - ss.cfg.WindowLag
	hi := ss.lastIdx + ss.cfg.WindowLead
	if lo < 0 {
		lo = 0
	}
	ss.eng.Retain(lo, hi)
	ss.mu.Unlock()
	ss.cache.retain(lo, hi)
}

// expectedHash returns the Host-published hash for segName, refreshing from the Host
// playlist (the trust anchor) if not yet known.
func (ss *swarmSession) expectedHash(ctx context.Context, segName string) string {
	ss.mu.Lock()
	h, ok := ss.hashes[segName]
	ss.mu.Unlock()
	if ok {
		return h
	}
	pl, err := ss.t.hostPlaylist(ctx, ss.host, ss.contentID, "index.m3u8")
	if err != nil {
		return ""
	}
	parsed := swarm.ParseHashes(pl)
	ss.mu.Lock()
	for k, v := range parsed {
		ss.hashes[k] = v
	}
	h = ss.hashes[segName]
	ss.mu.Unlock()
	return h
}

func isBusy(err error) bool { return errors.Is(err, peer.ErrBusy) }
```

(Add `"errors"` to the import block at the top of `swarm_session.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestFetchSegment`
Expected: PASS.

- [ ] **Step 5: Wire the session into the party coordinator + the pull seam, then commit**

In `internal/app/party.go`:
- Add field `swarm *swarmSession` to `partyCoordinator`.
- Where the viewer is set up (`pc.viewer = party.NewViewer(...)`, `pc.viewerHost = host` at the join path, ~line 92), also create and start the swarm session:

```go
	if tr, ok := pc.send.(swarmTransport); ok { // fake senders in unit tests skip the swarm
		pc.swarm = newSwarmSession(tr, pc.self, host, contentID, swarm.RealClock(), swarm.DefaultConfig())
		pc.swarm.setPartyID(welcome.GetPartyId()) // partyID from the PartyWelcome
		pc.swarm.start()
	}
```

(Thread `contentID` and the received `*peerv1.PartyWelcome` — call it `welcome` — into that code path; the `JoinParty` flow at `party.go:99` already has the contentID argument and the welcome returned by `do(ctx)`.)

- In `OnPartyAudience`, feed the membership to the swarm (replacing the current no-op):

```go
func (pc *partyCoordinator) OnPartyAudience(_ identity.NodeID, a *peerv1.PartyAudience) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss == nil {
		return
	}
	members := make([]identity.NodeID, 0, len(a.GetMembers()))
	for _, m := range a.GetMembers() {
		members = append(members, identity.NodeID(m.GetNodeId()))
	}
	ss.setPeers(members)
}
```

- On teardown (`OnPartyEnded` where `pc.viewer = nil`, line 177; and the self-leave path), call `pc.swarm.close()` and set `pc.swarm = nil`.
- Add a `SwarmHandler` delegation on the coordinator and a `swarmFor` accessor:

```go
func (pc *partyCoordinator) OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss != nil {
		ss.OnSwarmHave(remote, h)
	}
}

func (pc *partyCoordinator) SwarmSegment(remote identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss == nil {
		return nil, peer.ErrUnavailable
	}
	return ss.SwarmSegment(remote, req)
}

// swarmFor returns the active viewer swarm session iff it matches (host, contentID).
func (pc *partyCoordinator) swarmFor(host identity.NodeID, contentID string) *swarmSession {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.swarm != nil && pc.swarm.host == host && pc.swarm.contentID == contentID {
		return pc.swarm
	}
	return nil
}
```

Add the gossip loop + close to `internal/app/swarm_session.go`:

```go
func (ss *swarmSession) start() { go ss.gossipLoop() }

func (ss *swarmSession) close() { close(ss.stop) }

func (ss *swarmSession) gossipLoop() {
	t := time.NewTicker(ss.cfg.GossipInterval)
	defer t.Stop()
	for {
		select {
		case <-ss.stop:
			return
		case <-t.C:
			ss.mu.Lock()
			ss.eng.ExpireStale(ss.clock.Now())
			base, bitmap, epoch := ss.eng.HaveMsg()
			targets := ss.eng.GossipTargets()
			ss.mu.Unlock()
			have := &peerv1.SwarmHave{
				PartyId: ss.partyID, Rendition: swarmRendition,
				BaseIndex: base, Bitmap: bitmap, Epoch: epoch,
			}
			for _, tgt := range targets {
				_ = ss.t.sendTo(tgt, &peerv1.Envelope{Body: &peerv1.Envelope_SwarmHave{SwarmHave: have}})
				if rtt, err := ss.t.measureRTT(context.Background(), tgt); err == nil {
					ss.mu.Lock()
					ss.eng.OnRTT(tgt, rtt)
					ss.mu.Unlock()
				}
			}
		}
	}
}
```

In `internal/app/node.go`:
- In `sessionFor`, install the swarm handler alongside the party handler:

```go
	if n.party != nil {
		s.SetPartyHandler(n.party)
		s.SetSwarmHandler(n.party)
		s.SetOnClose(func(node identity.NodeID) { n.party.OnLeaveParty(node, "") })
	}
```

- Make `Node.Segment` swarm-aware (delegate when a matching viewer swarm is active):

```go
func (n *Node) Segment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	if ss := n.party.swarmFor(host, contentID); ss != nil {
		data, err := ss.FetchSegment(ctx, name)
		if err != nil {
			return nil, "", err
		}
		return data, contentTypeFor(name), nil
	}
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, err := sess.GetSegment(ctx, contentID, name)
	if err != nil {
		return nil, "", err
	}
	return data, contentTypeFor(name), nil
}
```

Run: `go build ./...` then `go test -race ./internal/app/ ./internal/peer/ ./internal/swarm/ ./internal/media/`
Expected: builds; all pass.

```bash
git add internal/app/ internal/peer/ internal/swarm/
git commit -m "app: swarm session — gossip loop, serve-from-cache, FetchSegment pull seam"
```

---

## Phase 7 — Integration / end-to-end

### Task 18: e2e — Host offload, poison rejection, mid-join catch-up

**Files:**
- Create: `test/swarm_e2e_test.go` (mirror the existing slice-4 party e2e harness in `test/`)

- [ ] **Step 1: Write the failing e2e test**

In `test/swarm_e2e_test.go`, build on the existing e2e harness (real signaling server + N `app.Node`s, as used by the watch-party e2e). Structure:

```go
//go:build e2e

package test

// TestSwarmOffloadsHost spins up a Host + N Viewers in one party, plays through M
// Segments, and asserts:
//   (a) every Viewer received every Segment, all BLAKE3-verified;
//   (b) the Host served each Segment far fewer than N times (offload);
//   (c) a poisoning Viewer is routed around (its bytes never reach a player);
//   (d) a Viewer that joins mid-stream catches up via the Swarm.
//
// Count Host segment serves via a counting MediaHandler wrapper installed on the
// Host node; assert total Host serves < N * M * offloadFactor.
```

Implement it concretely against the real harness: instrument the Host's `media.Service` with a wrapper counting `Segment` calls; drive each Viewer's bridge to fetch `seg00000.ts`..`seg0000(M-1).ts`; assert the count. Reuse the slice-4 e2e's Node bootstrap, signaling server start, and party join helpers verbatim.

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags e2e ./test/ -run TestSwarmOffloadsHost`
Expected: FAIL (initially the Host serves ~N×M; tune assertion after the swarm warms — expect Host serves to drop well below N×M once peers re-serve).

- [ ] **Step 3: Make it pass**

Iterate on swarm `Config` (gossip interval, window) and the test's warm-up (let gossip converge before measuring) until the offload assertion holds and verification/poison/mid-join sub-assertions pass. No production code change should be needed beyond Config tuning; if a real bug surfaces, fix it under TDD with a focused unit test in the relevant package first.

- [ ] **Step 4: Run the full suite with race**

Run: `go test -race ./...` and `go test -race -tags e2e ./test/`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add test/swarm_e2e_test.go
git commit -m "test(e2e): swarm offloads Host; poison rejected; mid-join catch-up"
```

---

## Final verification

- [ ] Run `make proto && make build && go test -race ./...` — all green.
- [ ] Run `go test -race -tags e2e ./test/` — all green.
- [ ] Confirm playback path unchanged: the bridge (`internal/bridge`) and `Node.Playlist` are untouched except `Node.Segment`'s swarm delegation.
- [ ] Skim `CONTEXT.md`, the spec, and ADR 0005 — implementation matches the approved decisions (party-scoped, gossip data plane, Host-anchored integrity, decentralized connection plane, RTT-aware/Host-last).
- [ ] Hand off via `superpowers:finishing-a-development-branch` (the user picks "Merge to main locally").
