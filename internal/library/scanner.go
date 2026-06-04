package library

import (
	"context"
	"io/fs"
	"log/slog"
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
	store  *Store
	prober Prober
	roots  []string
}

// NewScanner constructs a Scanner over the given roots.
func NewScanner(store *Store, prober Prober, roots []string) *Scanner {
	return &Scanner{store: store, prober: prober, roots: roots}
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
	// mtime cache: skip if an existing entry matches path+size+mtime.
	if existing, ok, _ := sc.store.GetByPath(path); ok &&
		existing.Size == info.Size() && existing.ModUnix == info.ModTime().Unix() {
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
	}
}

// Watch re-scans (debounced) whenever a root changes, until ctx is cancelled.
func (sc *Scanner) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	for _, root := range sc.roots {
		_ = w.Add(root)
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
		case _, ok := <-w.Events:
			if !ok {
				return nil
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
