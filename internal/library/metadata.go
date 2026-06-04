package library

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SubtitleTrack describes one subtitle available for a Title.
type SubtitleTrack struct {
	ID       string // "embedded:<index>" or "sidecar:<lang>"
	Language string
	Label    string
	Kind     string // "text" | "image"
	Source   string // sidecar file path, or "" for embedded
	Index    int    // ffprobe stream index for embedded; -1 for sidecar
}

// Title is one indexed media item.
type Title struct {
	ContentID     string
	Path          string
	DisplayTitle  string
	Size          int64
	ModUnix       int64
	DurationMS    int64
	Container     string
	VideoCodec    string
	AudioCodecs   []string
	Width         int
	Height        int
	HLSCompatible bool
	Subtitles     []SubtitleTrack
	AddedAt       time.Time
}

// FileInfo carries the filesystem facts BuildTitle needs (testable without stat).
type FileInfo struct {
	Size    int64
	ModUnix int64
}

// textSubCodecs are subtitle codecs convertible to WebVTT (Slice 3). Others are image-based.
var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"webvtt": true, "vtt": true, "mov_text": true, "text": true,
}

var sidecarExts = map[string]bool{".srt": true, ".ass": true, ".vtt": true, ".ssa": true}

// BuildTitle assembles a Title from a probe result plus a sidecar-subtitle scan.
func BuildTitle(path string, p Probe, contentID string, fi FileInfo) (Title, error) {
	t := Title{
		ContentID:    contentID,
		Path:         path,
		DisplayTitle: displayTitleFromPath(path),
		Size:         fi.Size,
		ModUnix:      fi.ModUnix,
		DurationMS:   p.DurationMS,
		Container:    p.Container,
		Width:        firstVideoWidth(p),
		Height:       firstVideoHeight(p),
		AddedAt:      time.Now(),
	}
	if len(p.Video) > 0 {
		t.VideoCodec = p.Video[0].Codec
	}
	for _, a := range p.Audio {
		t.AudioCodecs = append(t.AudioCodecs, a.Codec)
	}
	t.HLSCompatible = isHLSCompatible(p)

	for _, sub := range p.Subtitles {
		t.Subtitles = append(t.Subtitles, SubtitleTrack{
			ID:       "embedded:" + strconv.Itoa(sub.Index),
			Language: sub.Language,
			Label:    sub.Language,
			Kind:     subKind(sub.Codec),
			Index:    sub.Index,
		})
	}
	t.Subtitles = append(t.Subtitles, scanSidecarSubs(path)...)
	return t, nil
}

// isHLSCompatible is the strict rule: primary video H.264 and primary audio AAC/MP3.
func isHLSCompatible(p Probe) bool {
	if len(p.Video) == 0 || len(p.Audio) == 0 {
		return false
	}
	v := strings.ToLower(p.Video[0].Codec)
	a := strings.ToLower(p.Audio[0].Codec)
	videoOK := v == "h264" || v == "avc1"
	audioOK := a == "aac" || a == "mp3"
	return videoOK && audioOK
}

func subKind(codec string) string {
	if textSubCodecs[strings.ToLower(codec)] {
		return "text"
	}
	return "image"
}

func scanSidecarSubs(videoPath string) []SubtitleTrack {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SubtitleTrack
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !sidecarExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem != base && !strings.HasPrefix(stem, base+".") {
			continue
		}
		// language = the segment after the video base, e.g. "Movie.eng" -> "eng".
		lang := strings.TrimPrefix(stem, base)
		lang = strings.Trim(lang, ".")
		if lang == "" {
			lang = "und"
		}
		out = append(out, SubtitleTrack{
			ID:       "sidecar:" + lang,
			Language: lang,
			Label:    lang,
			Kind:     "text",
			Source:   filepath.Join(dir, name),
			Index:    -1,
		})
	}
	return out
}

func displayTitleFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = strings.NewReplacer(".", " ", "_", " ").Replace(base)
	return strings.Join(strings.Fields(base), " ")
}

func firstVideoWidth(p Probe) int {
	if len(p.Video) > 0 {
		return p.Video[0].Width
	}
	return 0
}
func firstVideoHeight(p Probe) int {
	if len(p.Video) > 0 {
		return p.Video[0].Height
	}
	return 0
}
