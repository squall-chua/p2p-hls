package media

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type cacheEntry struct {
	path     string
	size     int64
	accessed time.Time
}

// EvictCache enforces an idle TTL then an LRU size budget over the content dirs
// directly under root. Each content dir is evicted whole.
func EvictCache(root string, budgetBytes int64, ttl time.Duration) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	now := time.Now()
	var dirs []cacheEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		// TTL: drop idle dirs.
		if now.Sub(info.ModTime()) > ttl {
			_ = os.RemoveAll(dir)
			continue
		}
		dirs = append(dirs, cacheEntry{path: dir, size: dirSize(dir), accessed: info.ModTime()})
	}

	var total int64
	for _, d := range dirs {
		total += d.size
	}
	if total <= budgetBytes {
		return nil
	}
	// Evict least-recently-accessed first until under budget.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].accessed.Before(dirs[j].accessed) })
	for _, d := range dirs {
		if total <= budgetBytes {
			break
		}
		_ = os.RemoveAll(d.path)
		total -= d.size
	}
	return nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
