package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRootLabelsDisambiguatesCollisions(t *testing.T) {
	labels := buildRootLabels([]string{"/a/media", "/b/media", "/c/films"})
	require.Equal(t, "media", labels["/a/media"])
	require.Equal(t, "media (2)", labels["/b/media"])
	require.Equal(t, "films", labels["/c/films"])
}

func TestFolderForSingleRoot(t *testing.T) {
	roots := []string{"/srv/media"}
	labels := buildRootLabels(roots)

	rl, rd := folderFor("/srv/media/Movies/Action/x.mkv", roots, labels)
	require.Equal(t, "media", rl)
	require.Equal(t, "Movies/Action", rd)

	rl, rd = folderFor("/srv/media/y.mkv", roots, labels)
	require.Equal(t, "media", rl)
	require.Equal(t, "", rd)
}

func TestFolderForMultiRootPicksOwningRoot(t *testing.T) {
	roots := []string{"/a/media", "/b/media"}
	labels := buildRootLabels(roots)

	rl, rd := folderFor("/b/media/Shows/x.mkv", roots, labels)
	require.Equal(t, "media (2)", rl)
	require.Equal(t, "Shows", rd)
}

func TestFolderForNoMatch(t *testing.T) {
	roots := []string{"/srv/media"}
	labels := buildRootLabels(roots)
	rl, rd := folderFor("/other/x.mkv", roots, labels)
	require.Equal(t, "", rl)
	require.Equal(t, "", rd)
}

func TestFolderForRejectsStringPrefixFalseMatch(t *testing.T) {
	roots := []string{"/a/me", "/a/media"}
	labels := buildRootLabels(roots)
	rl, rd := folderFor("/a/media/x.mkv", roots, labels)
	require.Equal(t, "media", rl)
	require.Equal(t, "", rd)
}
