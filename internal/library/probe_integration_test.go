package library_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

// generateSample makes a 1s H.264/AAC mp4 with ffmpeg, skipping if tools are absent.
func generateSample(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", out)
	require.NoError(t, cmd.Run())
	return out
}

func TestFFProbeReadsRealFile(t *testing.T) {
	path := generateSample(t)
	p, err := library.FFProbe{}.Probe(context.Background(), path)
	require.NoError(t, err)
	require.Greater(t, p.DurationMS, int64(500))
	require.NotEmpty(t, p.Video)
	require.Equal(t, "h264", p.Video[0].Codec)
}
