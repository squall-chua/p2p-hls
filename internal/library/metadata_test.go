package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

func TestBuildTitleDerivesHLSCompatibleAndSubtitles(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "Some.Movie.2021.mkv")
	require.NoError(t, os.WriteFile(videoPath, []byte("x"), 0o600))
	// sidecar subtitle
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Some.Movie.2021.eng.srt"), []byte("1"), 0o600))

	probe := library.Probe{
		DurationMS: 5000,
		Container:  "matroska",
		Video:      []library.VideoStream{{Codec: "h264", Width: 1920, Height: 1080}},
		Audio:      []library.AudioStream{{Codec: "aac"}},
		Subtitles:  []library.SubStream{{Index: 3, Codec: "subrip", Language: "eng"}},
	}
	title, err := library.BuildTitle(videoPath, probe, "cid123", library.FileInfo{Size: 1, ModUnix: 100})
	require.NoError(t, err)

	require.Equal(t, "cid123", title.ContentID)
	require.Equal(t, "Some Movie 2021", title.DisplayTitle) // filename-derived
	require.True(t, title.HLSCompatible)
	require.Equal(t, "h264", title.VideoCodec)
	require.Equal(t, []string{"aac"}, title.AudioCodecs)
	require.Equal(t, 1920, title.Width)

	// one embedded text sub + one sidecar text sub
	require.Len(t, title.Subtitles, 2)
	kinds := map[string]string{}
	for _, s := range title.Subtitles {
		kinds[s.ID] = s.Kind
	}
	require.Equal(t, "text", kinds["embedded:3"])
	require.Equal(t, "text", kinds["sidecar:eng"])
}

func TestBuildTitleIncompatibleCodecs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	probe := library.Probe{
		Video: []library.VideoStream{{Codec: "hevc", Width: 3840, Height: 2160}},
		Audio: []library.AudioStream{{Codec: "ac3"}},
	}
	title, err := library.BuildTitle(p, probe, "cid", library.FileInfo{})
	require.NoError(t, err)
	require.False(t, title.HLSCompatible)
}

func TestImageSubtitleKind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.mkv")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	probe := library.Probe{
		Video:     []library.VideoStream{{Codec: "h264"}},
		Audio:     []library.AudioStream{{Codec: "aac"}},
		Subtitles: []library.SubStream{{Index: 2, Codec: "hdmv_pgs_subtitle", Language: "und"}},
	}
	title, err := library.BuildTitle(p, probe, "cid", library.FileInfo{})
	require.NoError(t, err)
	require.Len(t, title.Subtitles, 1)
	require.Equal(t, "image", title.Subtitles[0].Kind)
}

func TestSidecarSubtitleMatchingBoundary(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "Movie.mkv")
	require.NoError(t, os.WriteFile(videoPath, []byte("x"), 0o600))
	// language-less sidecar for THIS movie -> "und"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Movie.srt"), []byte("1"), 0o600))
	// sidecar for a DIFFERENT movie that merely shares a name prefix -> must NOT match
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MovieExtended.eng.srt"), []byte("1"), 0o600))

	probe := library.Probe{
		Video: []library.VideoStream{{Codec: "h264"}},
		Audio: []library.AudioStream{{Codec: "aac"}},
	}
	title, err := library.BuildTitle(videoPath, probe, "cid", library.FileInfo{})
	require.NoError(t, err)
	require.Len(t, title.Subtitles, 1, "only Movie.srt should match, not MovieExtended.eng.srt")
	require.Equal(t, "sidecar:und", title.Subtitles[0].ID)
	require.Equal(t, "und", title.Subtitles[0].Language)
}
