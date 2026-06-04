package media_test

import (
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/stretchr/testify/require"
)

func argString(a []string) string { return strings.Join(a, " ") }

func TestFFmpegArgsRemuxesCompatible(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mp4", VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Width: 1920, Height: 1080, HLSCompatible: true,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v copy")
	require.Contains(t, s, "-c:a copy")
	require.Contains(t, s, "-hls_time 4")
	require.NotContains(t, s, "libx264")
}

func TestFFmpegArgsTranscodesIncompatibleWith1080Cap(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mkv", VideoCodec: "hevc", AudioCodecs: []string{"ac3"},
		Width: 3840, Height: 2160, HLSCompatible: false,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v libx264")
	require.Contains(t, s, "-crf 23")
	require.Contains(t, s, "-preset veryfast")
	require.Contains(t, s, "-c:a aac")
	require.Contains(t, s, "scale=-2:'min(1080,ih)'")
}

func TestFFmpegArgsTranscodesOnlyAudioWhenVideoOK(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mkv", VideoCodec: "h264", AudioCodecs: []string{"ac3"},
		Width: 1280, Height: 720, HLSCompatible: false,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v copy")
	require.Contains(t, s, "-c:a aac")
}
