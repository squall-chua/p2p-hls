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
	Play    bool    `json:"play"`
	Seek    bool    `json:"seek"`
	SeekMS  int64   `json:"seekMs"`
	Rate    float64 `json:"rate"`
	DriftMS int64   `json:"driftMs"` // viewer-ahead(+)/behind(-) gap vs the host target
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
