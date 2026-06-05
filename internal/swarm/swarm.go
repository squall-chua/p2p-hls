// Package swarm is the pure, deterministic decision engine for party-scoped mesh
// distribution: peer-have tracking, RTT-aware source selection, and gossip target
// selection. It performs no I/O; the app layer supplies a Clock and a *rand.Rand.
package swarm

import (
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
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
	have      map[int]bool
	epoch     uint64
	rtt       time.Duration
	haveRTT   bool
	lastSeen  time.Time
	busyUntil time.Time
	demoted   bool
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
