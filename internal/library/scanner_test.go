package library_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// fakeThumber records calls and writes a stub JPEG so "already present" logic works.
type fakeThumber struct{ calls int }

func (f *fakeThumber) Thumbnail(_ context.Context, _, out string, _ int64) error {
	f.calls++
	return os.WriteFile(out, []byte("JPEG"), 0o600)
}

func TestScanGeneratesThumbnailOncePerFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Movie.mkv"), []byte("video-bytes"), 0o600))

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	defer store.Close()

	cache := t.TempDir()
	thumb := &fakeThumber{}
	sc := library.NewScanner(store, fakeProber{}, []string{root}).SetThumbnailer(cache, thumb)

	require.NoError(t, sc.ScanOnce(context.Background()))

	all, err := store.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.FileExists(t, library.ThumbPath(cache, all[0].ContentID))
	require.Equal(t, 1, thumb.calls)

	// Second scan: file unchanged AND thumb already present -> no regeneration.
	require.NoError(t, sc.ScanOnce(context.Background()))
	require.Equal(t, 1, thumb.calls, "thumbnail must not be regenerated when present")
}

func TestWatchIndexesFilesAddedToSubfolders(t *testing.T) {
	root := t.TempDir()

	// Create the subfolder BEFORE the watcher starts so no Create(dir) event
	// fires on root — only recursive watching of the subfolder itself will
	// detect a file written into it.
	sub := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	defer store.Close()

	sc := library.NewScanner(store, fakeProber{}, []string{root})
	go func() { _ = sc.Watch(t.Context()) }()

	time.Sleep(150 * time.Millisecond) // let the watcher finish setup

	require.NoError(t, os.WriteFile(filepath.Join(sub, "Movie.mkv"), []byte("v"), 0o600))

	require.Eventually(t, func() bool {
		all, _ := store.All()
		return len(all) == 1
	}, 3*time.Second, 50*time.Millisecond)
}
