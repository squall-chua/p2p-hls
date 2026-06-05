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
	for i := 0; i < 120; i++ {
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
