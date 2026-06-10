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
