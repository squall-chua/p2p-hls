package media

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/peer"
)

// Engine produces and serves HLS renditions, one cache dir per Content ID.
type Engine struct {
	store    *library.Store
	runner   Runner
	cacheDir string

	mu   sync.Mutex
	jobs map[string]*job
}

type job struct {
	dir      string
	title    library.Title
	complete bool
	hashes   *SegmentHashes
}

// NewEngine constructs an Engine writing renditions under cacheDir.
func NewEngine(store *library.Store, runner Runner, cacheDir string) *Engine {
	return &Engine{store: store, runner: runner, cacheDir: cacheDir, jobs: map[string]*job{}}
}

// File returns the bytes of a named file (playlist or segment) for a Content ID,
// starting the ffmpeg job on first request. `complete` is meaningful for playlists.
func (e *Engine) File(ctx context.Context, contentID, name string) ([]byte, bool, error) {
	j, err := e.ensureJob(ctx, contentID)
	if err != nil {
		return nil, false, err
	}

	// Statically-generated playlists.
	switch {
	case name == "playlist.m3u8":
		return []byte(MasterPlaylist(j.title)), true, nil
	case strings.HasPrefix(name, "sub_") && strings.HasSuffix(name, ".m3u8"):
		lang := strings.TrimSuffix(strings.TrimPrefix(name, "sub_"), ".m3u8")
		return []byte(SubtitlePlaylist(lang)), true, nil
	}

	path := filepath.Join(j.dir, filepath.Base(name)) // filepath.Base guards traversal
	data, rerr := os.ReadFile(path)
	if rerr == nil {
		if name == "index.m3u8" {
			data = InjectHashes(data, j.hashes)
		}
		return data, e.isComplete(j), nil
	}
	if errors.Is(rerr, fs.ErrNotExist) {
		// Not produced yet: signal "retry" unless the job already finished.
		if e.isComplete(j) {
			return nil, true, peer.ErrNotFound
		}
		return nil, false, peer.ErrUnavailable
	}
	return nil, false, rerr
}

// ensureJob returns the job for contentID, starting it on first request. The
// mutex is held across the store lookup and dir creation so the check-and-create
// is atomic (two concurrent File calls for the same new content must not launch
// two ffmpeg jobs). ctx drives the ffmpeg job and so must outlive any single
// request — callers pass context.Background(), not a per-request context.
func (e *Engine) ensureJob(ctx context.Context, contentID string) (*job, error) {
	e.mu.Lock()
	if j, ok := e.jobs[contentID]; ok {
		e.mu.Unlock()
		return j, nil
	}
	title, ok, err := e.store.Get(contentID)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if !ok {
		e.mu.Unlock()
		return nil, peer.ErrNotFound
	}
	dir := filepath.Join(e.cacheDir, contentID)
	if mkerr := os.MkdirAll(dir, 0o700); mkerr != nil {
		e.mu.Unlock()
		return nil, mkerr
	}
	j := &job{dir: dir, title: title}
	j.hashes = NewSegmentHashes(dir)
	e.jobs[contentID] = j
	e.mu.Unlock()

	go e.runJob(ctx, j)
	return j, nil
}

func (e *Engine) runJob(ctx context.Context, j *job) {
	// Extract text subtitle tracks to WebVTT (best-effort).
	for _, sub := range TextSubtitleTracks(j.title.Subtitles) {
		out := filepath.Join(j.dir, "sub_"+sub.Language+".vtt")
		_ = extractVTT(ctx, e.runner, j.title, sub, out)
	}
	args, err := FFmpegArgs(j.title, j.dir, filepath.Join(j.dir, "index.m3u8"))
	if err == nil {
		_ = e.runner.Run(ctx, args)
	}
	e.mu.Lock()
	j.complete = true
	e.mu.Unlock()
}

func (e *Engine) isComplete(j *job) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return j.complete
}
