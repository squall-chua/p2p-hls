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
	if hexstr, ok := h.cache[segName]; ok {
		h.mu.Unlock()
		return hexstr, true
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
