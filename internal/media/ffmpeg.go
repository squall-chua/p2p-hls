// Package media produces HLS renditions of Titles via ffmpeg and serves them.
package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/squall-chua/p2p-hls/internal/library"
)

// Runner executes an ffmpeg invocation. Implemented by ExecRunner; faked in tests.
type Runner interface {
	Run(ctx context.Context, args []string) error
}

// videoCopyable / audioCopyable encode the strict ADR codec rule.
func videoCopyable(codec string) bool {
	c := strings.ToLower(codec)
	return c == "h264" || c == "avc1"
}
func audioCopyable(codec string) bool {
	c := strings.ToLower(codec)
	return c == "aac" || c == "mp3"
}

// FFmpegArgs builds the ffmpeg argument vector to produce an HLS rendition of
// title into outDir, writing the media playlist at playlistPath. Per-stream:
// copy H.264 video / AAC-MP3 audio, transcode otherwise (1080p cap, CRF 23,
// veryfast, 4s keyframe-aligned segments).
func FFmpegArgs(title library.Title, outDir, playlistPath string) ([]string, error) {
	args := []string{"-y", "-i", title.Path}

	if videoCopyable(title.VideoCodec) {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-profile:v", "high",
			"-preset", "veryfast",
			"-crf", "23",
			"-maxrate", "8M", "-bufsize", "16M",
			"-vf", "scale=-2:'min(1080,ih)'",
			// 4s segments at typical frame rates need keyframes every ~96 frames;
			// force a keyframe every 4s so segments cut cleanly.
			"-force_key_frames", "expr:gte(t,n_forced*4)",
		)
	}

	primaryAudio := ""
	if len(title.AudioCodecs) > 0 {
		primaryAudio = title.AudioCodecs[0]
	}
	if audioCopyable(primaryAudio) {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
		playlistPath,
	)
	return args, nil
}

// ExecRunner runs the real ffmpeg binary.
type ExecRunner struct {
	Binary string // defaults to "ffmpeg"
}

// Run executes ffmpeg with args.
func (e ExecRunner) Run(ctx context.Context, args []string) error {
	bin := e.Binary
	if bin == "" {
		bin = "ffmpeg"
	}
	return exec.CommandContext(ctx, bin, args...).Run()
}
