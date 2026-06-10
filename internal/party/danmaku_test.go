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
