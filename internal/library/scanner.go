package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// eligibleExts are the video container extensions the Scanner indexes.
var eligibleExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".m4v": true, ".webm": true,
	".avi": true, ".ts": true, ".mpg": true, ".mpeg": true, ".wmv": true, ".flv": true,
}

// Scanner indexes Shared folders into a Store.
type Scanner struct {
	store    *Store
	prober   Prober
	roots    []string
	cacheDir string
	thumber  Thumbnailer
}

// NewScanner constructs a Scanner over the given roots.
func NewScanner(store *Store, prober Prober, roots []string) *Scanner {
	return &Scanner{store: store, prober: prober, roots: roots}
}

// SetThumbnailer enables poster generation into cacheDir. Without it, the
// Scanner indexes metadata only (no thumbnails).
func (sc *Scanner) SetThumbnailer(cacheDir string, thumber Thumbnailer) *Scanner {
	sc.cacheDir = cacheDir
	sc.thumber = thumber
	return sc
}

// ensureThumb generates {cacheDir}/{contentID}/thumb.jpg if missing. No-op when
// thumbnails are disabled or the title has no video stream. Failures are logged,
// never fatal — a missing thumbnail just falls back to the UI placeholder.
func (sc *Scanner) ensureThumb(ctx context.Context, contentID, path string, durationMS int64, height int) {
	if sc.thumber == nil || sc.cacheDir == "" || contentID == "" || height == 0 {
		return
	}
	out := ThumbPath(sc.cacheDir, contentID)
	if _, err := os.Stat(out); err == nil {
		return // already present
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		slog.Warn("thumbnail dir failed", "path", path, "err", err)
		return
	}
	if err := sc.thumber.Thumbnail(ctx, path, out, durationMS); err != nil {
		slog.Warn("thumbnail failed", "path", path, "err", err)
	}
}

// ScanOnce walks every root and indexes new or changed eligible files.
func (sc *Scanner) ScanOnce(ctx context.Context) error {
	for _, root := range sc.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !eligibleExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			sc.indexFile(ctx, path, d)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (sc *Scanner) indexFile(ctx context.Context, path string, d fs.DirEntry) {
	info, err := d.Info()
	if err != nil {
		return
	}
	// mtime cache: skip re-indexing if an existing entry matches path+size+mtime,
	// but still backfill a missing thumbnail for libraries indexed before this feature.
	if existing, ok, _ := sc.store.GetByPath(path); ok &&
		existing.Size == info.Size() && existing.ModUnix == info.ModTime().Unix() {
		sc.ensureThumb(ctx, existing.ContentID, path, existing.DurationMS, existing.Height)
		return
	}
	contentID, err := HashFile(path)
	if err != nil {
		slog.Warn("hash failed", "path", path, "err", err)
		return
	}
	probe, err := sc.prober.Probe(ctx, path)
	if err != nil {
		slog.Warn("probe failed", "path", path, "err", err)
		return
	}
	title, err := BuildTitle(path, probe, contentID, FileInfo{Size: info.Size(), ModUnix: info.ModTime().Unix()})
	if err != nil {
		return
	}
	if err := sc.store.Upsert(title); err != nil {
		slog.Warn("index upsert failed", "path", path, "err", err)
		return
	}
	sc.ensureThumb(ctx, title.ContentID, path, title.DurationMS, title.Height)
}

// Watch re-scans (debounced) whenever a root changes, until ctx is cancelled.
func (sc *Scanner) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	addTree := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = w.Add(p)
			}
			return nil
		})
	}
	for _, root := range sc.roots {
		addTree(root)
	}

	var mu sync.Mutex
	var timer *time.Timer
	debounce := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(500*time.Millisecond, func() { _ = sc.ScanOnce(ctx) })
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// A newly-created subdirectory must itself become watched, or files
			// later added inside it would go unnoticed.
			if ev.Op&fsnotify.Create != 0 {
				if info, serr := os.Stat(ev.Name); serr == nil && info.IsDir() {
					addTree(ev.Name)
				}
			}
			debounce()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watcher error", "err", err)
		}
	}
}
