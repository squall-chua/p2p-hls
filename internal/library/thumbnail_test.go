package library_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

func TestThumbPath(t *testing.T) {
	require.Equal(t,
		filepath.Join("/cache", "abc123", "thumb.jpg"),
		library.ThumbPath("/cache", "abc123"))
}

func TestThumbnailArgsSeeksTenPercentCappedAtTenSeconds(t *testing.T) {
	// 60s duration -> 10% = 6s seek.
	args := library.ThumbnailArgs("/in.mp4", "/out.jpg", 60000)
	joined := strings.Join(args, " ")
	require.Contains(t, joined, "-ss 6.000")
	require.Contains(t, joined, "-i /in.mp4")
	require.Contains(t, joined, "-frames:v 1")
	require.Contains(t, joined, "scale=480:-2")
	require.Equal(t, "/out.jpg", args[len(args)-1])
}

func TestThumbnailArgsCapsSeekAtTenSeconds(t *testing.T) {
	// 1000s duration -> 10% = 100s, capped to 10s.
	args := library.ThumbnailArgs("/in.mp4", "/out.jpg", 1000000)
	require.Contains(t, strings.Join(args, " "), "-ss 10.000")
}

func TestThumbnailArgsZeroDurationSeeksZero(t *testing.T) {
	args := library.ThumbnailArgs("/in.mp4", "/out.jpg", 0)
	require.Contains(t, strings.Join(args, " "), "-ss 0.000")
}
