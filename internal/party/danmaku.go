package party

import (
	"strings"
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

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
