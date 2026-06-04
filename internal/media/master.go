package media

import (
	"fmt"
	"strings"

	"github.com/squall-chua/p2p-hls/internal/library"
)

// MasterPlaylist builds the master playlist that references the video media
// playlist (index.m3u8) and a SUBTITLES group of text tracks.
func MasterPlaylist(title library.Title) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")

	texts := TextSubtitleTracks(title.Subtitles)
	for i, sub := range texts {
		def := "NO"
		auto := "YES"
		if i == 0 {
			def = "YES"
		}
		b.WriteString(fmt.Sprintf(
			"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=%s,URI=\"sub_%s.m3u8\"\n",
			sub.Language, sub.Language, def, auto, sub.Language))
	}

	bandwidth := 6000000
	streamInf := fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d", bandwidth, title.Width, title.Height)
	if len(texts) > 0 {
		streamInf += ",SUBTITLES=\"subs\""
	}
	b.WriteString(streamInf + "\nindex.m3u8\n")
	return b.String()
}

// SubtitlePlaylist is a single-segment VOD playlist wrapping one WebVTT file.
func SubtitlePlaylist(language string) string {
	return fmt.Sprintf(
		"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:99999\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:99999.0,\nsub_%s.vtt\n#EXT-X-ENDLIST\n",
		language)
}
