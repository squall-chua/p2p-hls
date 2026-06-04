package library_test

import (
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

func sampleTitle() library.Title {
	return library.Title{
		ContentID:     "cid-1",
		Path:          "/media/movie.mkv",
		DisplayTitle:  "Movie",
		Size:          123,
		ModUnix:       1000,
		DurationMS:    5000,
		Container:     "matroska",
		VideoCodec:    "h264",
		AudioCodecs:   []string{"aac"},
		Width:         1920,
		Height:        1080,
		HLSCompatible: true,
		Subtitles:     []library.SubtitleTrack{{ID: "embedded:2", Language: "eng", Kind: "text", Index: 2}},
	}
}

func TestStoreUpsertGetAll(t *testing.T) {
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "index.db"))
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, store.Upsert(sampleTitle()))

	got, ok, err := store.Get("cid-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Movie", got.DisplayTitle)
	require.Equal(t, []string{"aac"}, got.AudioCodecs)
	require.Len(t, got.Subtitles, 1)
	require.Equal(t, "embedded:2", got.Subtitles[0].ID)

	all, err := store.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestStoreUpsertIsIdempotentByContentID(t *testing.T) {
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "index.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Upsert(sampleTitle()))
	updated := sampleTitle()
	updated.DisplayTitle = "Renamed"
	require.NoError(t, store.Upsert(updated))
	all, err := store.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "Renamed", all[0].DisplayTitle)
}

func TestStoreGetByPathForMtimeCache(t *testing.T) {
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "index.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Upsert(sampleTitle()))
	got, ok, err := store.GetByPath("/media/movie.mkv")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(1000), got.ModUnix)
}
