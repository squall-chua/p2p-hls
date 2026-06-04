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
