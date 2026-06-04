package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// VideoStream / AudioStream / SubStream are the parts of a probe we care about.
type VideoStream struct {
	Codec  string
	Width  int
	Height int
}
type AudioStream struct {
	Codec string
}
type SubStream struct {
	Index    int
	Codec    string
	Language string
}

// Probe is the normalized result of inspecting a media file.
type Probe struct {
	DurationMS int64
	Container  string
	Video      []VideoStream
	Audio      []AudioStream
	Subtitles  []SubStream
}

// Prober inspects a media file. Implemented by FFProbe; faked in tests.
type Prober interface {
	Probe(ctx context.Context, path string) (Probe, error)
}

// FFProbe shells out to the ffprobe binary.
type FFProbe struct {
	// Binary is the ffprobe executable; defaults to "ffprobe".
	Binary string
}

type ffprobeOutput struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Tags      struct {
			Language string `json:"language"`
		} `json:"tags"`
	} `json:"streams"`
}

// Probe runs `ffprobe -show_format -show_streams -of json`.
func (f FFProbe) Probe(ctx context.Context, path string) (Probe, error) {
	bin := f.Binary
	if bin == "" {
		bin = "ffprobe"
	}
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-show_format", "-show_streams",
		"-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return Probe{}, fmt.Errorf("ffprobe %s: %w: %s", path, err, bytes.TrimSpace(exitErr.Stderr))
		}
		return Probe{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return Probe{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	p := Probe{Container: raw.Format.FormatName}
	if secs, perr := strconv.ParseFloat(raw.Format.Duration, 64); perr == nil {
		p.DurationMS = int64(secs * 1000)
	}
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			p.Video = append(p.Video, VideoStream{Codec: s.CodecName, Width: s.Width, Height: s.Height})
		case "audio":
			p.Audio = append(p.Audio, AudioStream{Codec: s.CodecName})
		case "subtitle":
			lang := s.Tags.Language
			if lang == "" {
				lang = "und"
			}
			p.Subtitles = append(p.Subtitles, SubStream{Index: s.Index, Codec: strings.ToLower(s.CodecName), Language: lang})
		}
	}
	return p, nil
}
