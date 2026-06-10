package party_test

import (
	"strings"
	"testing"
	"time"
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
