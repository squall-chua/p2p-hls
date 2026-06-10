package app

import (
	"testing"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestToTitleViewsCopiesFolderFields(t *testing.T) {
	views := toTitleViews([]*peerv1.TitleMeta{
		{ContentId: "a", RelDir: "Movies/Action", RootLabel: "media"},
	})
	require.Equal(t, "Movies/Action", views[0].RelDir)
	require.Equal(t, "media", views[0].RootLabel)
}

func TestToTitleViewsEncodesThumbnailAsDataURL(t *testing.T) {
	views := toTitleViews([]*peerv1.TitleMeta{
		{ContentId: "a", Thumbnail: []byte("JPEG")},
		{ContentId: "b"}, // no thumbnail
	})
	require.Equal(t, "data:image/jpeg;base64,SlBFRw==", views[0].Thumbnail)
	require.Equal(t, "", views[1].Thumbnail)
}
