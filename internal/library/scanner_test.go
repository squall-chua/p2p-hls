package library_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

// fakeProber returns canned metadata regardless of file contents.
type fakeProber struct{}

func (fakeProber) Probe(_ context.Context, _ string) (library.Probe, error) {
	return library.Probe{
		DurationMS: 1000,
		Container:  "matroska",
		Video:      []library.VideoStream{{Codec: "h264", Width: 1280, Height: 720}},
		Audio:      []library.AudioStream{{Codec: "aac"}},
	}, nil
}

func TestScanOnceIndexesEligibleFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Movie.mkv"), []byte("video-bytes"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore me"), 0o600))

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	defer store.Close()

	sc := library.NewScanner(store, fakeProber{}, []string{root})
	require.NoError(t, sc.ScanOnce(context.Background()))

	all, err := store.All()
	require.NoError(t, err)
	require.Len(t, all, 1, "only the .mkv should be indexed")
	require.Equal(t, "Movie", all[0].DisplayTitle)
	require.True(t, all[0].HLSCompatible)
}

func TestScanOnceSkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "movie.mp4")
	require.NoError(t, os.WriteFile(p, []byte("v"), 0o600))

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	defer store.Close()
	sc := library.NewScanner(store, fakeProber{}, []string{root})

	require.NoError(t, sc.ScanOnce(context.Background()))
	first, _, _ := store.Get(firstContentID(t, store))
	require.NoError(t, sc.ScanOnce(context.Background())) // second pass: unchanged
	second, _, _ := store.Get(first.ContentID)
	require.Equal(t, first.AddedAt.Unix(), second.AddedAt.Unix(), "unchanged file not re-indexed")
}

func firstContentID(t *testing.T, s *library.Store) string {
	t.Helper()
	all, err := s.All()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	return all[0].ContentID
}
