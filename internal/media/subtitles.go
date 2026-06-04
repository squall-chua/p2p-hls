package media

import (
	"context"
	"fmt"

	"github.com/squall-chua/p2p-hls/internal/library"
)

// TextSubtitleTracks returns only the convertible (text) subtitle tracks.
func TextSubtitleTracks(subs []library.SubtitleTrack) []library.SubtitleTrack {
	var out []library.SubtitleTrack
	for _, s := range subs {
		if s.Kind == "text" {
			out = append(out, s)
		}
	}
	return out
}

// extractVTTArgs builds ffmpeg args to convert one subtitle track to WebVTT.
// Embedded tracks map by stream index; sidecar tracks read the sidecar file.
func extractVTTArgs(title library.Title, track library.SubtitleTrack, outVTT string) []string {
	if track.Index >= 0 {
		return []string{"-y", "-i", title.Path, "-map", fmt.Sprintf("0:%d", track.Index), "-c:s", "webvtt", outVTT}
	}
	return []string{"-y", "-i", track.Source, "-c:s", "webvtt", outVTT}
}

// extractVTT runs ffmpeg to produce a WebVTT file for one text track.
func extractVTT(ctx context.Context, runner Runner, title library.Title, track library.SubtitleTrack, outVTT string) error {
	return runner.Run(ctx, extractVTTArgs(title, track, outVTT))
}
