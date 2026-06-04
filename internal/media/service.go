package media

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
)

// Service answers streaming RPCs over the Engine, gated by the access Policy.
// It implements peer.MediaHandler.
type Service struct {
	engine *Engine
	policy *catalog.Policy
}

// NewService wires the Engine and Policy.
func NewService(engine *Engine, policy *catalog.Policy) *Service {
	return &Service{engine: engine, policy: policy}
}

// Playlist returns a named playlist (access-checked).
func (s *Service) Playlist(remote identity.NodeID, contentID, name string) ([]byte, string, bool, error) {
	if !s.policy.Allowed(remote) {
		return nil, "", false, peer.ErrDenied
	}
	data, complete, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, "", false, err
	}
	s.touch(contentID)
	return data, contentType(name), complete, nil
}

// Segment returns a named segment (access-checked).
func (s *Service) Segment(remote identity.NodeID, contentID, name string) ([]byte, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	data, _, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, err
	}
	s.touch(contentID)
	return data, nil
}

// OpenFile opens the original source file for download (access-checked).
func (s *Service) OpenFile(remote identity.NodeID, contentID string) (io.ReadCloser, int64, error) {
	if !s.policy.Allowed(remote) {
		return nil, 0, peer.ErrDenied
	}
	title, ok, err := s.engine.store.Get(contentID)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, peer.ErrNotFound
	}
	f, err := os.Open(title.Path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *Service) touch(contentID string) {
	dir := filepath.Join(s.engine.cacheDir, contentID)
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(name, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(name, ".vtt"):
		return "text/vtt"
	default:
		return "application/octet-stream"
	}
}
