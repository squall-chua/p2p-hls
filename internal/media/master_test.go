package media_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/stretchr/testify/require"
)

func TestMasterPlaylistWithSubtitles(t *testing.T) {
	title := library.Title{
		Width: 1920, Height: 1080, VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Subtitles: []library.SubtitleTrack{
			{ID: "embedded:2", Language: "eng", Kind: "text"},
			{ID: "sidecar:fre", Language: "fre", Kind: "text"},
			{ID: "embedded:3", Language: "und", Kind: "image"}, // skipped
		},
	}
	master := media.MasterPlaylist(title)
	require.Contains(t, master, "#EXTM3U")
	require.Contains(t, master, `TYPE=SUBTITLES`)
	require.Contains(t, master, `LANGUAGE="eng"`)
	require.Contains(t, master, `LANGUAGE="fre"`)
	require.NotContains(t, master, "image")
	require.Contains(t, master, `SUBTITLES="subs"`)
	require.Contains(t, master, "index.m3u8")
}

func TestSubtitlePlaylistWrapsVTT(t *testing.T) {
	pl := media.SubtitlePlaylist("eng")
	require.Contains(t, pl, "#EXT-X-ENDLIST")
	require.Contains(t, pl, "sub_eng.vtt")
}

func TestTextSubtitleTracks(t *testing.T) {
	subs := []library.SubtitleTrack{
		{Language: "eng", Kind: "text"},
		{Language: "spa", Kind: "image"},
	}
	got := media.TextSubtitleTracks(subs)
	require.Len(t, got, 1)
	require.Equal(t, "eng", got[0].Language)
}
