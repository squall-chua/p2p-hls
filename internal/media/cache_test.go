package media_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/stretchr/testify/require"
)

func writeDir(t *testing.T, root, name string, size int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seg.ts"), make([]byte, size), 0o600))
	mod := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(dir, mod, mod))
	return dir
}

func TestEvictByTTL(t *testing.T) {
	root := t.TempDir()
	old := writeDir(t, root, "old", 10, 7*time.Hour)
	fresh := writeDir(t, root, "fresh", 10, 1*time.Hour)

	require.NoError(t, media.EvictCache(root, 1<<30, 6*time.Hour))
	require.NoDirExists(t, old)
	require.DirExists(t, fresh)
}

func TestEvictByBudgetLRU(t *testing.T) {
	root := t.TempDir()
	older := writeDir(t, root, "older", 1000, 2*time.Hour)
	newer := writeDir(t, root, "newer", 1000, 1*time.Hour)

	// Budget fits only one dir; the older (LRU) is evicted.
	require.NoError(t, media.EvictCache(root, 1500, 24*time.Hour))
	require.NoDirExists(t, older)
	require.DirExists(t, newer)
}
