package library

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// ThumbPath is the single source of truth for where a Title's poster JPEG lives.
func ThumbPath(cacheDir, contentID string) string {
	return filepath.Join(cacheDir, contentID, "thumb.jpg")
}

// ThumbnailArgs builds the ffmpeg argument vector that grabs one frame as a
// JPEG. It seeks to 10% of the duration, capped at 10s (0 when unknown), and
// scales to 480px wide preserving aspect ratio.
func ThumbnailArgs(src, out string, durationMS int64) []string {
	seekSec := 0.0
	if durationMS > 0 {
		seekSec = float64(durationMS) / 1000.0 * 0.10
		if seekSec > 10 {
			seekSec = 10
		}
	}
	return []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", seekSec),
		"-i", src,
		"-frames:v", "1",
		"-vf", "scale=480:-2",
		"-q:v", "4",
		out,
	}
}

// Thumbnailer renders a poster JPEG for a media file. Implemented by
// FFThumbnailer; faked in tests.
type Thumbnailer interface {
	Thumbnail(ctx context.Context, src, out string, durationMS int64) error
}

// FFThumbnailer shells out to the ffmpeg binary.
type FFThumbnailer struct {
	// Binary is the ffmpeg executable; defaults to "ffmpeg".
	Binary string
}

// Thumbnail runs ffmpeg to write a single-frame JPEG at out.
func (f FFThumbnailer) Thumbnail(ctx context.Context, src, out string, durationMS int64) error {
	bin := f.Binary
	if bin == "" {
		bin = "ffmpeg"
	}
	return exec.CommandContext(ctx, bin, ThumbnailArgs(src, out, durationMS)...).Run()
}
