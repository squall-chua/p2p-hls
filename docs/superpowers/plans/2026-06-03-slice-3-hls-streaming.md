# Slice 3 (1:1 HLS Streaming + Download) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Viewer streams an allowed Host Title — produced as HLS by ffmpeg (remux or transcode), pulled segment-by-segment over the `bulk` channel, and played by `hls.js` through a loopback HTTP bridge — and can download the original file with BLAKE3 integrity verification.

**Architecture:** Builds on Slices 1–2. The `peer` package gains a `bulk`-channel transfer layer (16 KiB frames, requestId-correlated, backpressure per ADR 0001) and streaming RPCs (`GetPlaylist`, `GetSegment`, `Download`). A `media` package resolves a Content ID to its source file, runs ffmpeg (per-stream copy/transcode, 1080p/CRF23/veryfast, 4s segments) into a per-content cache dir, generates a master playlist + WebVTT subtitle tracks, and serves files by name with LRU+TTL eviction. A `bridge` package runs a loopback HTTP server (`127.0.0.1`, ephemeral port, token + Origin check) that `hls.js` points at; it pulls playlists/segments from the Host session, hiding P2P. Download streams original bytes to disk and verifies the hash equals the Content ID.

**Tech Stack (additions):** `ffmpeg` (external, behind an interface) · stdlib `net/http`. No new modules.

**Depends on:** Slices 1–2 (`peer.Session` + RPC layer, `library.Store`/`Title`, `catalog.Policy`, `app.Node`).

**Out of scope (later):** watch parties, mesh swarm, ABR/multi-rendition, trickle ICE, TURN, image-subtitle burn-in.

---

## File Structure

| File | Responsibility |
|---|---|
| `proto/peer/v1/peer.proto` (modify) | Add GetPlaylist/Playlist/GetSegment/Download |
| `internal/peer/bulk.go` (create) | Bulk frame encode/decode + size constants |
| `internal/peer/session.go` (modify) | Bind `bulk` channel; `fetchBulk`/`fetchBulkTo`/`sendBulk`; `MediaHandler`; streaming RPC methods + handlers |
| `internal/media/ffmpeg.go` (create) | Per-stream copy/transcode decision → ffmpeg args; `Runner` interface |
| `internal/media/engine.go` (create) | Lazily run ffmpeg into a cache dir; serve files by name; completion tracking |
| `internal/media/subtitles.go` (create) | WebVTT extraction args + subtitle sub-playlists |
| `internal/media/master.go` (create) | Master playlist generation (video variant + subtitle group) |
| `internal/media/cache.go` (create) | LRU + TTL eviction over content cache dirs |
| `internal/media/service.go` (create) | `peer.MediaHandler` impl (access-checked) over the engine + Store |
| `internal/bridge/bridge.go` (create) | Loopback HTTP server: token/Origin gate, playlist/segment routes |
| `internal/app/node.go` (modify) | Install media handler; `Stream`/`Download` helpers; expose session media calls |
| `internal/app/download.go` (create) | Hash-verified download to disk |
| `test/stream_e2e_test.go` (create) | Real ffmpeg: browse → stream → download with hash check (gated) |

**Cross-task type contract (Slice 3):**
- `peer.MediaHandler`: `Playlist(remote, contentID, name) (data []byte, contentType string, complete bool, err error)`, `Segment(remote, contentID, name) ([]byte, error)`, `OpenFile(remote, contentID) (io.ReadCloser, int64, error)`.
- `(*peer.Session)`: `SetMediaHandler(MediaHandler)`, `GetPlaylist(ctx, contentID, name) (data []byte, contentType string, complete bool, err error)`, `GetSegment(ctx, contentID, name) ([]byte, error)`, `DownloadTo(ctx, contentID, w io.Writer) error`.
- `media.Runner` interface, `media.FFmpegArgs(title) ([]string, error)`, `media.Engine`, `media.Service`.
- `bridge.Bridge`, `bridge.New(streamer, token)`, `bridge.Streamer` interface.

---

## Task 1: Extend the proto for streaming

**Files:**
- Modify: `proto/peer/v1/peer.proto`

- [ ] **Step 1: Add the streaming messages**

In `proto/peer/v1/peer.proto`, add four fields to the `Envelope` `oneof body` (after `Error error = 12;`):
```proto
    GetPlaylist get_playlist = 13;
    Playlist playlist = 14;
    GetSegment get_segment = 15;
    Download download = 16;
```
And append these messages to the file:
```proto
// GetPlaylist requests a named playlist (e.g. "playlist.m3u8", "index.m3u8",
// "sub_eng.m3u8") for a Title. Small text response over the control channel.
message GetPlaylist {
  string content_id = 1;
  string name = 2;
}

message Playlist {
  bytes data = 1;
  string content_type = 2;
  bool complete = 3; // false while ffmpeg is still producing (growing playlist)
}

// GetSegment requests a named segment ("seg00003.ts", "sub_eng_0.vtt").
// The bytes return on the bulk channel, correlated by request_id.
message GetSegment {
  string content_id = 1;
  string name = 2;
}

// Download requests the original file bytes (streamed on the bulk channel).
message Download {
  string content_id = 1;
}
```

- [ ] **Step 2: Regenerate and build**

Run: `make proto && go build ./...`
Expected: regenerated `peer.pb.go`; build succeeds; existing tests still compile.

- [ ] **Step 3: Commit**

```bash
git add proto
git commit -m "feat(proto): streaming messages (playlist, segment, download)"
```

---

## Task 2: Bulk frame codec

**Files:**
- Create: `internal/peer/bulk.go`
- Test: `internal/peer/bulk_test.go`

A bulk frame is a fixed 13-byte header (`requestId` u64, `seq` u32, `flags` u8) + raw payload, ≤ 16 KiB total (ADR 0001's browser-interop ceiling).

- [ ] **Step 1: Write the failing test**

Create `internal/peer/bulk_test.go`:
```go
package peer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBulkFrameRoundTrip(t *testing.T) {
	payload := []byte("segment-bytes")
	raw := encodeBulkFrame(7, 3, true, payload)
	require.LessOrEqual(t, len(raw), FrameSize)

	id, seq, last, got, ok := decodeBulkFrame(raw)
	require.True(t, ok)
	require.Equal(t, uint64(7), id)
	require.Equal(t, uint32(3), seq)
	require.True(t, last)
	require.Equal(t, payload, got)
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	_, _, _, _, ok := decodeBulkFrame([]byte{1, 2, 3})
	require.False(t, ok)
}

func TestPayloadMaxFitsInFrame(t *testing.T) {
	require.Equal(t, FrameSize, payloadMax+bulkHeaderSize)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/peer/ -run TestBulkFrame -v`
Expected: FAIL — `undefined: encodeBulkFrame`.

- [ ] **Step 3: Implement**

Create `internal/peer/bulk.go`:
```go
package peer

import "encoding/binary"

const (
	// FrameSize is the max bulk-channel message size (browser-interop ceiling).
	FrameSize      = 16 * 1024
	bulkHeaderSize = 13 // 8 (requestId) + 4 (seq) + 1 (flags)
	payloadMax     = FrameSize - bulkHeaderSize
	flagLast       = 0x01
	bulkHighWater  = 1 << 20  // pause sending above 1 MiB buffered
	bulkLowWater   = 256 << 10 // resume below 256 KiB
)

func encodeBulkFrame(requestID uint64, seq uint32, last bool, payload []byte) []byte {
	frame := make([]byte, bulkHeaderSize+len(payload))
	binary.BigEndian.PutUint64(frame[0:8], requestID)
	binary.BigEndian.PutUint32(frame[8:12], seq)
	if last {
		frame[12] = flagLast
	}
	copy(frame[bulkHeaderSize:], payload)
	return frame
}

func decodeBulkFrame(raw []byte) (requestID uint64, seq uint32, last bool, payload []byte, ok bool) {
	if len(raw) < bulkHeaderSize {
		return 0, 0, false, nil, false
	}
	requestID = binary.BigEndian.Uint64(raw[0:8])
	seq = binary.BigEndian.Uint32(raw[8:12])
	last = raw[12]&flagLast != 0
	payload = raw[bulkHeaderSize:]
	return requestID, seq, last, payload, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/peer/ -run 'TestBulkFrame|TestDecodeRejects|TestPayloadMax' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/peer/bulk.go internal/peer/bulk_test.go
git commit -m "feat(peer): bulk frame codec"
```

---

## Task 3: Bulk transfer + streaming RPCs on the session

**Files:**
- Modify: `internal/peer/session.go`
- Test: `internal/peer/stream_test.go`

Adds: binding the `bulk` channel, a bulk-sink registry, `fetchBulk`/`fetchBulkTo`/`sendBulk*` with backpressure, the `MediaHandler` interface, and `GetPlaylist`/`GetSegment`/`DownloadTo` plus their inbound handlers.

- [ ] **Step 1: Write the failing test**

Create `internal/peer/stream_test.go`:
```go
package peer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeMedia struct {
	playlist []byte
	segment  []byte
	file     string
}

func (m *fakeMedia) Playlist(_ identity.NodeID, _, name string) ([]byte, string, bool, error) {
	if name == "missing.m3u8" {
		return nil, "", false, ErrNotFound
	}
	return m.playlist, "application/vnd.apple.mpegurl", true, nil
}
func (m *fakeMedia) Segment(_ identity.NodeID, _, _ string) ([]byte, error) { return m.segment, nil }
func (m *fakeMedia) OpenFile(_ identity.NodeID, _ string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader(m.file)), int64(len(m.file)), nil
}

func TestStreamingRPCs(t *testing.T) {
	viewer, host, _ := connectPair(t) // from rpc_test.go
	mh := &fakeMedia{
		playlist: []byte("#EXTM3U\n#EXT-X-ENDLIST\n"),
		segment:  bytes.Repeat([]byte("S"), 40000), // > 2 frames, exercises chunking
		file:     strings.Repeat("ORIGINAL", 10000),
	}
	host.SetMediaHandler(mh)
	ctx := context.Background()

	data, ct, complete, err := viewer.GetPlaylist(ctx, "cid", "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(data), "ENDLIST")
	require.Equal(t, "application/vnd.apple.mpegurl", ct)
	require.True(t, complete)

	_, _, _, err = viewer.GetPlaylist(ctx, "cid", "missing.m3u8")
	require.ErrorIs(t, err, ErrNotFound)

	seg, err := viewer.GetSegment(ctx, "cid", "seg00000.ts")
	require.NoError(t, err)
	require.Equal(t, mh.segment, seg, "multi-frame segment reassembled exactly")

	var buf bytes.Buffer
	require.NoError(t, viewer.DownloadTo(ctx, "cid", &buf))
	require.Equal(t, mh.file, buf.String())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/peer/ -run TestStreamingRPCs -v`
Expected: FAIL — `host.SetMediaHandler undefined`.

- [ ] **Step 3: Add fields and bulk init to the Session**

In `internal/peer/session.go`, add to the `Session` struct:
```go
	bulk         *webrtc.DataChannel
	bulkSinks    map[uint64]*bulkSink
	mediaHandler MediaHandler
	lowSig       chan struct{}
```
In `NewSession`, after `pending: make(map[uint64]chan *peerv1.Envelope),`, add to the struct literal:
```go
		bulkSinks: make(map[uint64]*bulkSink),
		lowSig:    make(chan struct{}, 1),
```
Replace the `pc.OnDataChannel(...)` block in `NewSession` with:
```go
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "control":
			s.bindControl(dc)
		case "bulk":
			s.bindBulk(dc)
		}
	})
```

- [ ] **Step 4: Bind the bulk channel on the initiator**

In `internal/peer/session.go` `Start`, replace the bulk-channel creation line:
```go
	if _, err := s.pc.CreateDataChannel("bulk", nil); err != nil { // opened now, used in Slice 3
		return err
	}
```
with:
```go
	bulk, err := s.pc.CreateDataChannel("bulk", nil)
	if err != nil {
		return err
	}
	s.bindBulk(bulk)
```

- [ ] **Step 5: Add the bulk transfer machinery**

Append to `internal/peer/session.go`:
```go
import (
	"bytes"
	"io"
	"time"
)
// NOTE: merge these into the existing import block at the top of session.go.

// bulkSink accumulates an inbound bulk transfer (segment buffer or download writer).
type bulkSink struct {
	w    io.Writer
	done chan error
}

func (s *Session) bindBulk(dc *webrtc.DataChannel) {
	s.bulk = dc
	dc.SetBufferedAmountLowThreshold(bulkLowWater)
	dc.OnBufferedAmountLow(func() {
		select {
		case s.lowSig <- struct{}{}:
		default:
		}
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		id, _, last, payload, ok := decodeBulkFrame(msg.Data)
		if !ok {
			return
		}
		s.mu.Lock()
		sink := s.bulkSinks[id]
		s.mu.Unlock()
		if sink == nil {
			return
		}
		if len(payload) > 0 {
			_, _ = sink.w.Write(payload)
		}
		if last {
			sink.done <- nil
		}
	})
}

// fetchBulkTo issues a bulk request and streams the response into w.
func (s *Session) fetchBulkTo(ctx context.Context, env *peerv1.Envelope, w io.Writer) error {
	id := s.ids.next()
	env.RequestId = id
	sink := &bulkSink{w: w, done: make(chan error, 1)}
	s.mu.Lock()
	s.bulkSinks[id] = sink
	s.mu.Unlock()
	cleanup := func() {
		s.mu.Lock()
		delete(s.bulkSinks, id)
		s.mu.Unlock()
	}
	if err := s.send(env); err != nil {
		cleanup()
		return err
	}
	select {
	case err := <-sink.done:
		cleanup()
		return err
	case <-ctx.Done():
		cleanup()
		return ctx.Err()
	}
}

// fetchBulk buffers a bulk response in memory (for segments).
func (s *Session) fetchBulk(ctx context.Context, env *peerv1.Envelope) ([]byte, error) {
	var buf bytes.Buffer
	if err := s.fetchBulkTo(ctx, env, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Session) sendBulkReader(reqID uint64, r io.Reader) error {
	buf := make([]byte, payloadMax)
	var seq uint32
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := s.sendFrameFlow(reqID, seq, false, buf[:n]); werr != nil {
				return werr
			}
			seq++
		}
		if err == io.EOF {
			return s.sendFrameFlow(reqID, seq, true, nil)
		}
		if err != nil {
			return err
		}
	}
}

func (s *Session) sendBulk(reqID uint64, data []byte) error {
	return s.sendBulkReader(reqID, bytes.NewReader(data))
}

func (s *Session) sendFrameFlow(reqID uint64, seq uint32, last bool, payload []byte) error {
	for s.bulk != nil && s.bulk.BufferedAmount() > bulkHighWater {
		select {
		case <-s.lowSig:
		case <-time.After(10 * time.Second):
			return fmt.Errorf("peer: bulk backpressure timeout")
		}
	}
	if s.bulk == nil {
		return fmt.Errorf("peer: bulk channel not open")
	}
	return s.bulk.Send(encodeBulkFrame(reqID, seq, last, payload))
}
```
Add `"fmt"` to the import block if not already present.

- [ ] **Step 6: Add the MediaHandler interface, setter, and RPC methods**

Append to `internal/peer/session.go`:
```go
// MediaHandler answers streaming RPCs. Installed per Session by the app layer.
type MediaHandler interface {
	Playlist(remote identity.NodeID, contentID, name string) (data []byte, contentType string, complete bool, err error)
	Segment(remote identity.NodeID, contentID, name string) ([]byte, error)
	OpenFile(remote identity.NodeID, contentID string) (io.ReadCloser, int64, error)
}

// SetMediaHandler installs the streaming handler.
func (s *Session) SetMediaHandler(h MediaHandler) { s.mediaHandler = h }

// GetPlaylist fetches a named playlist.
func (s *Session) GetPlaylist(ctx context.Context, contentID, name string) ([]byte, string, bool, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetPlaylist{GetPlaylist: &peerv1.GetPlaylist{ContentId: contentID, Name: name}}})
	if err != nil {
		return nil, "", false, err
	}
	if e := resp.GetError(); e != nil {
		return nil, "", false, statusErr(e)
	}
	p := resp.GetPlaylist()
	return p.GetData(), p.GetContentType(), p.GetComplete(), nil
}

// GetSegment fetches a named segment over the bulk channel.
func (s *Session) GetSegment(ctx context.Context, contentID, name string) ([]byte, error) {
	return s.fetchBulk(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetSegment{GetSegment: &peerv1.GetSegment{ContentId: contentID, Name: name}}})
}

// DownloadTo streams the original file bytes into w.
func (s *Session) DownloadTo(ctx context.Context, contentID string, w io.Writer) error {
	return s.fetchBulkTo(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Download{Download: &peerv1.Download{ContentId: contentID}}}, w)
}
```

- [ ] **Step 7: Route inbound streaming requests + bulk errors**

In `internal/peer/session.go` `bindControl`'s `OnMessage` switch, add these cases (before the response/`deliver` case):
```go
		case *peerv1.Envelope_GetPlaylist:
			s.handleGetPlaylist(env.RequestId, body.GetPlaylist)
		case *peerv1.Envelope_GetSegment:
			s.handleGetSegment(env.RequestId, body.GetSegment)
		case *peerv1.Envelope_Download:
			s.handleDownload(env.RequestId, body.Download)
```
Then change the `Error` handling. Replace the combined response case so that an `Error` for an in-flight bulk transfer cancels the sink:
```go
		case *peerv1.Envelope_Error:
			s.mu.Lock()
			sink := s.bulkSinks[env.RequestId]
			s.mu.Unlock()
			if sink != nil {
				sink.done <- statusErr(body.Error)
			} else {
				s.deliver(env)
			}
		case *peerv1.Envelope_Pong, *peerv1.Envelope_Catalog,
			*peerv1.Envelope_TitleMeta, *peerv1.Envelope_Ack, *peerv1.Envelope_Playlist:
			s.deliver(env)
```
(Remove `*peerv1.Envelope_Error` from the old combined `deliver` case so it is only handled by the new block above.)

- [ ] **Step 8: Add the inbound streaming handlers**

Append to `internal/peer/session.go`:
```go
func (s *Session) handleGetPlaylist(reqID uint64, req *peerv1.GetPlaylist) {
	if s.mediaHandler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	data, ct, complete, err := s.mediaHandler.Playlist(s.remote, req.GetContentId(), req.GetName())
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Playlist{Playlist: &peerv1.Playlist{
		Data: data, ContentType: ct, Complete: complete,
	}}})
}

func (s *Session) handleGetSegment(reqID uint64, req *peerv1.GetSegment) {
	if s.mediaHandler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	data, err := s.mediaHandler.Segment(s.remote, req.GetContentId(), req.GetName())
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.sendBulk(reqID, data)
}

func (s *Session) handleDownload(reqID uint64, req *peerv1.Download) {
	if s.mediaHandler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	rc, _, err := s.mediaHandler.OpenFile(s.remote, req.GetContentId())
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	defer rc.Close()
	_ = s.sendBulkReader(reqID, rc)
}
```

- [ ] **Step 9: Run the streaming tests + full peer suite**

Run: `go test -race ./internal/peer/ -v -timeout 90s`
Expected: PASS — streaming RPCs (incl. multi-frame segment reassembly and download), plus all Slice 1–2 peer tests.

- [ ] **Step 10: Commit**

```bash
git add internal/peer/session.go internal/peer/stream_test.go
git commit -m "feat(peer): bulk transfer and streaming rpcs"
```

---

## Task 4: ffmpeg argument builder

**Files:**
- Create: `internal/media/ffmpeg.go`
- Test: `internal/media/ffmpeg_test.go`

Pure function: given a `library.Title` + paths, produce the ffmpeg argument vector — copying H.264/AAC streams, transcoding otherwise (1080p cap, CRF 23, `veryfast`, 4s keyframe-aligned segments). A `Runner` interface abstracts execution.

- [ ] **Step 1: Write the failing test**

Create `internal/media/ffmpeg_test.go`:
```go
package media_test

import (
	"strings"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/stretchr/testify/require"
)

func argString(a []string) string { return strings.Join(a, " ") }

func TestFFmpegArgsRemuxesCompatible(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mp4", VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Width: 1920, Height: 1080, HLSCompatible: true,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v copy")
	require.Contains(t, s, "-c:a copy")
	require.Contains(t, s, "-hls_time 4")
	require.NotContains(t, s, "libx264")
}

func TestFFmpegArgsTranscodesIncompatibleWith1080Cap(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mkv", VideoCodec: "hevc", AudioCodecs: []string{"ac3"},
		Width: 3840, Height: 2160, HLSCompatible: false,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v libx264")
	require.Contains(t, s, "-crf 23")
	require.Contains(t, s, "-preset veryfast")
	require.Contains(t, s, "-c:a aac")
	require.Contains(t, s, "scale=-2:'min(1080,ih)'")
}

func TestFFmpegArgsTranscodesOnlyAudioWhenVideoOK(t *testing.T) {
	title := library.Title{
		Path: "/m/movie.mkv", VideoCodec: "h264", AudioCodecs: []string{"ac3"},
		Width: 1280, Height: 720, HLSCompatible: false,
	}
	args, err := media.FFmpegArgs(title, "/cache/cid", "/cache/cid/index.m3u8")
	require.NoError(t, err)
	s := argString(args)
	require.Contains(t, s, "-c:v copy")
	require.Contains(t, s, "-c:a aac")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/media/ -run TestFFmpegArgs -v`
Expected: FAIL — `undefined: media.FFmpegArgs`.

- [ ] **Step 3: Implement**

Create `internal/media/ffmpeg.go`:
```go
// Package media produces HLS renditions of Titles via ffmpeg and serves them.
package media

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/squallchua/p2p-hls/internal/library"
)

// Runner executes an ffmpeg invocation. Implemented by ExecRunner; faked in tests.
type Runner interface {
	Run(ctx context.Context, args []string) error
}

// videoCopyable / audioCopyable encode the strict ADR codec rule.
func videoCopyable(codec string) bool {
	c := strings.ToLower(codec)
	return c == "h264" || c == "avc1"
}
func audioCopyable(codec string) bool {
	c := strings.ToLower(codec)
	return c == "aac" || c == "mp3"
}

// FFmpegArgs builds the ffmpeg argument vector to produce an HLS rendition of
// title into outDir, writing the media playlist at playlistPath. Per-stream:
// copy H.264 video / AAC-MP3 audio, transcode otherwise (1080p cap, CRF 23,
// veryfast, 4s keyframe-aligned segments).
func FFmpegArgs(title library.Title, outDir, playlistPath string) ([]string, error) {
	args := []string{"-y", "-i", title.Path}

	if videoCopyable(title.VideoCodec) {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-profile:v", "high",
			"-preset", "veryfast",
			"-crf", "23",
			"-maxrate", "8M", "-bufsize", "16M",
			"-vf", "scale=-2:'min(1080,ih)'",
			// 4s segments at typical frame rates need keyframes every ~96 frames;
			// force a keyframe every 4s so segments cut cleanly.
			"-force_key_frames", "expr:gte(t,n_forced*4)",
		)
	}

	primaryAudio := ""
	if len(title.AudioCodecs) > 0 {
		primaryAudio = title.AudioCodecs[0]
	}
	if audioCopyable(primaryAudio) {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
		playlistPath,
	)
	return args, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/media/ -run TestFFmpegArgs -v`
Expected: PASS (all three).

- [ ] **Step 5: Add the real runner (no test — exercised by the e2e)**

Append to `internal/media/ffmpeg.go`:
```go
import "os/exec"
// NOTE: merge "os/exec" into the import block.

// ExecRunner runs the real ffmpeg binary.
type ExecRunner struct {
	Binary string // defaults to "ffmpeg"
}

// Run executes ffmpeg with args.
func (e ExecRunner) Run(ctx context.Context, args []string) error {
	bin := e.Binary
	if bin == "" {
		bin = "ffmpeg"
	}
	return exec.CommandContext(ctx, bin, args...).Run()
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/media/ffmpeg.go internal/media/ffmpeg_test.go
git commit -m "feat(media): ffmpeg argument builder (remux/transcode decision)"
```

---

## Task 5: Subtitle + master playlist generation

**Files:**
- Create: `internal/media/subtitles.go`, `internal/media/master.go`
- Test: `internal/media/master_test.go`

A master playlist wires the video media playlist to a subtitle group (required by HLS even for one video rendition). Text subtitle tracks become single-segment WebVTT sub-playlists.

- [ ] **Step 1: Write the failing test**

Create `internal/media/master_test.go`:
```go
package media_test

import (
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/media"
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/media/ -run 'TestMaster|TestSubtitle|TestText' -v`
Expected: FAIL — `undefined: media.MasterPlaylist`.

- [ ] **Step 3: Implement subtitles helper**

Create `internal/media/subtitles.go`:
```go
package media

import (
	"context"
	"fmt"

	"github.com/squallchua/p2p-hls/internal/library"
)

// TextSubtitleTracks returns only the convertible (text) subtitle tracks.
func TextSubtitleTracks(subs []library.SubtitleTrack) []library.SubtitleTrack {
	var out []library.SubtitleTrack
	for _, s := range subs {
		if s.Kind == "text" {
			out = append(out, s)
		}
	}
	return out
}

// extractVTTArgs builds ffmpeg args to convert one subtitle track to WebVTT.
// Embedded tracks map by stream index; sidecar tracks read the sidecar file.
func extractVTTArgs(title library.Title, track library.SubtitleTrack, outVTT string) []string {
	if track.Index >= 0 {
		return []string{"-y", "-i", title.Path, "-map", fmt.Sprintf("0:%d", track.Index), "-c:s", "webvtt", outVTT}
	}
	return []string{"-y", "-i", track.Source, "-c:s", "webvtt", outVTT}
}

// extractVTT runs ffmpeg to produce a WebVTT file for one text track.
func extractVTT(ctx context.Context, runner Runner, title library.Title, track library.SubtitleTrack, outVTT string) error {
	return runner.Run(ctx, extractVTTArgs(title, track, outVTT))
}
```

- [ ] **Step 4: Implement master + subtitle playlists**

Create `internal/media/master.go`:
```go
package media

import (
	"fmt"
	"strings"

	"github.com/squallchua/p2p-hls/internal/library"
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
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/media/ -run 'TestMaster|TestSubtitle|TestText' -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/media/subtitles.go internal/media/master.go internal/media/master_test.go
git commit -m "feat(media): master playlist and webvtt subtitle generation"
```

---

## Task 6: HLS engine

**Files:**
- Create: `internal/media/engine.go`
- Test: `internal/media/engine_test.go`

The Engine lazily starts an ffmpeg job for a Content ID (resolving the source via the Store), generating the master + subtitle playlists immediately and the video playlist/segments as ffmpeg produces them. It serves files by name; a not-yet-produced segment returns `peer.ErrUnavailable` ("not ready, retry").

- [ ] **Step 1: Write the failing test**

Create `internal/media/engine_test.go`:
```go
package media_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

// fakeRunner simulates ffmpeg by writing a playlist + one segment into outDir.
type fakeRunner struct{ outDir string }

func (f *fakeRunner) Run(_ context.Context, args []string) error {
	playlist := args[len(args)-1] // last arg is the playlist path
	dir := filepath.Dir(playlist)
	if err := os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("TSDATA"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(playlist,
		[]byte("#EXTM3U\n#EXTINF:4.0,\nseg00000.ts\n#EXT-X-ENDLIST\n"), 0o600)
}

func newEngineWithTitle(t *testing.T) (*media.Engine, string) {
	t.Helper()
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	src := filepath.Join(t.TempDir(), "movie.mp4")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o600))
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid", Path: src, VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Width: 1920, Height: 1080, HLSCompatible: true,
	}))
	cache := t.TempDir()
	eng := media.NewEngine(store, &fakeRunner{}, cache)
	return eng, "cid"
}

func TestEngineServesMasterAndSegment(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	ctx := context.Background()

	// The master playlist is static and available immediately.
	master, complete, err := eng.File(ctx, cid, "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(master), "#EXT-X-STREAM-INF")
	require.True(t, complete) // master is static => complete

	// The video playlist + segment are produced by the async job; poll until ready
	// (this mirrors the real "not ready, retry" growing-playlist behavior).
	require.Eventually(t, func() bool {
		idx, _, e := eng.File(ctx, cid, "index.m3u8")
		return e == nil && strings.Contains(string(idx), "seg00000.ts")
	}, 3*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		seg, _, e := eng.File(ctx, cid, "seg00000.ts")
		return e == nil && string(seg) == "TSDATA"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestEngineUnknownContentNotFound(t *testing.T) {
	eng, _ := newEngineWithTitle(t)
	_, _, err := eng.File(context.Background(), "missing", "playlist.m3u8")
	require.ErrorIs(t, err, peer.ErrNotFound)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/media/ -run TestEngine -v`
Expected: FAIL — `undefined: media.NewEngine`.

- [ ] **Step 3: Implement the engine**

Create `internal/media/engine.go`:
```go
package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/peer"
)

// Engine produces and serves HLS renditions, one cache dir per Content ID.
type Engine struct {
	store    *library.Store
	runner   Runner
	cacheDir string

	mu   sync.Mutex
	jobs map[string]*job
}

type job struct {
	dir      string
	title    library.Title
	done     chan struct{}
	complete bool
}

// NewEngine constructs an Engine writing renditions under cacheDir.
func NewEngine(store *library.Store, runner Runner, cacheDir string) *Engine {
	return &Engine{store: store, runner: runner, cacheDir: cacheDir, jobs: map[string]*job{}}
}

// File returns the bytes of a named file (playlist or segment) for a Content ID,
// starting the ffmpeg job on first request. `complete` is meaningful for playlists.
func (e *Engine) File(ctx context.Context, contentID, name string) ([]byte, bool, error) {
	j, err := e.ensureJob(ctx, contentID)
	if err != nil {
		return nil, false, err
	}

	// Statically-generated playlists.
	switch {
	case name == "playlist.m3u8":
		return []byte(MasterPlaylist(j.title)), true, nil
	case strings.HasPrefix(name, "sub_") && strings.HasSuffix(name, ".m3u8"):
		lang := strings.TrimSuffix(strings.TrimPrefix(name, "sub_"), ".m3u8")
		return []byte(SubtitlePlaylist(lang)), true, nil
	}

	path := filepath.Join(j.dir, filepath.Base(name)) // filepath.Base guards traversal
	data, rerr := os.ReadFile(path)
	if rerr == nil {
		return data, e.isComplete(j), nil
	}
	if os.IsNotExist(rerr) {
		// Not produced yet: signal "retry" unless the job already finished.
		if e.isComplete(j) {
			return nil, true, peer.ErrNotFound
		}
		return nil, false, peer.ErrUnavailable
	}
	return nil, false, rerr
}

func (e *Engine) ensureJob(ctx context.Context, contentID string) (*job, error) {
	e.mu.Lock()
	if j, ok := e.jobs[contentID]; ok {
		e.mu.Unlock()
		return j, nil
	}
	title, ok, err := e.store.Get(contentID)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if !ok {
		e.mu.Unlock()
		return nil, peer.ErrNotFound
	}
	dir := filepath.Join(e.cacheDir, contentID)
	if mkerr := os.MkdirAll(dir, 0o700); mkerr != nil {
		e.mu.Unlock()
		return nil, mkerr
	}
	j := &job{dir: dir, title: title, done: make(chan struct{})}
	e.jobs[contentID] = j
	e.mu.Unlock()

	go e.runJob(ctx, j)
	return j, nil
}

func (e *Engine) runJob(ctx context.Context, j *job) {
	defer close(j.done)
	// Extract text subtitle tracks to WebVTT (best-effort).
	for _, sub := range TextSubtitleTracks(j.title.Subtitles) {
		out := filepath.Join(j.dir, "sub_"+sub.Language+".vtt")
		_ = extractVTT(ctx, e.runner, j.title, sub, out)
	}
	args, err := FFmpegArgs(j.title, j.dir, filepath.Join(j.dir, "index.m3u8"))
	if err == nil {
		_ = e.runner.Run(ctx, args)
	}
	e.mu.Lock()
	j.complete = true
	e.mu.Unlock()
}

func (e *Engine) isComplete(j *job) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return j.complete
}
```

- [ ] **Step 4: Run the engine tests**

Run: `go test ./internal/media/ -run TestEngine -v`
Expected: PASS (both). The fake runner finishes synchronously, so the playlist/segment are present on first read.

- [ ] **Step 5: Commit**

```bash
git add internal/media/engine.go internal/media/engine_test.go
git commit -m "feat(media): lazy hls engine serving files by name"
```

---

## Task 7: Segment cache eviction

**Files:**
- Create: `internal/media/cache.go`
- Test: `internal/media/cache_test.go`

Bounds the cache: evict whole content dirs by LRU (oldest access time) when total size exceeds the budget, and by idle TTL. Default 2 GiB / 6h (spec). Access time is tracked by touching dirs on serve.

- [ ] **Step 1: Write the failing test**

Create `internal/media/cache_test.go`:
```go
package media_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/stretchr/testify/require"
)

func writeDir(t *testing.T, root, name string, size int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seg.ts"), make([]byte, size), 0o600))
	mod := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(dir, mod, mod))
	return dir
}

func TestEvictByTTL(t *testing.T) {
	root := t.TempDir()
	old := writeDir(t, root, "old", 10, 7*time.Hour)
	fresh := writeDir(t, root, "fresh", 10, 1*time.Hour)

	require.NoError(t, media.EvictCache(root, 1<<30, 6*time.Hour))
	require.NoDirExists(t, old)
	require.DirExists(t, fresh)
}

func TestEvictByBudgetLRU(t *testing.T) {
	root := t.TempDir()
	older := writeDir(t, root, "older", 1000, 2*time.Hour)
	newer := writeDir(t, root, "newer", 1000, 1*time.Hour)

	// Budget fits only one dir; the older (LRU) is evicted.
	require.NoError(t, media.EvictCache(root, 1500, 24*time.Hour))
	require.NoDirExists(t, older)
	require.DirExists(t, newer)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/media/ -run TestEvict -v`
Expected: FAIL — `undefined: media.EvictCache`.

- [ ] **Step 3: Implement**

Create `internal/media/cache.go`:
```go
package media

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

type cacheEntry struct {
	path     string
	size     int64
	accessed time.Time
}

// EvictCache enforces an idle TTL then an LRU size budget over the content dirs
// directly under root. Each content dir is evicted whole.
func EvictCache(root string, budgetBytes int64, ttl time.Duration) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	now := time.Now()
	var dirs []cacheEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		// TTL: drop idle dirs.
		if now.Sub(info.ModTime()) > ttl {
			_ = os.RemoveAll(dir)
			continue
		}
		dirs = append(dirs, cacheEntry{path: dir, size: dirSize(dir), accessed: info.ModTime()})
	}

	var total int64
	for _, d := range dirs {
		total += d.size
	}
	if total <= budgetBytes {
		return nil
	}
	// Evict least-recently-accessed first until under budget.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].accessed.Before(dirs[j].accessed) })
	for _, d := range dirs {
		if total <= budgetBytes {
			break
		}
		_ = os.RemoveAll(d.path)
		total -= d.size
	}
	return nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/media/ -run TestEvict -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/media/cache.go internal/media/cache_test.go
git commit -m "feat(media): segment cache ttl + lru eviction"
```

---

## Task 8: Media service (MediaHandler impl)

**Files:**
- Create: `internal/media/service.go`
- Test: `internal/media/service_test.go`

Implements `peer.MediaHandler`, gating every request on the access Policy and touching the content dir on serve (for LRU).

- [ ] **Step 1: Write the failing test**

Create `internal/media/service_test.go`:
```go
package media_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

func TestServiceAccessGatesStreaming(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	policy := catalog.NewPolicy(catalog.VisibilityRestricted)
	svc := media.NewService(eng, policy)
	bob := identity.NodeID("bob")
	ctx := context.Background()

	_, _, _, err := svc.Playlist(bob, cid, "playlist.m3u8")
	require.ErrorIs(t, err, peer.ErrDenied)

	policy.AddAllow(bob)
	data, ct, _, err := svc.Playlist(bob, cid, "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(data), "#EXTM3U")
	require.Equal(t, "application/vnd.apple.mpegurl", ct)

	// Segment is produced by the async job; poll until ready.
	require.Eventually(t, func() bool {
		seg, e := svc.Segment(bob, cid, "seg00000.ts")
		return e == nil && len(seg) > 0
	}, 3*time.Second, 20*time.Millisecond)
}

func TestServiceOpenFileForDownload(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	svc := media.NewService(eng, policy)
	rc, size, err := svc.OpenFile(identity.NodeID("anyone"), cid)
	require.NoError(t, err)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	require.Equal(t, int64(len(b)), size)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/media/ -run TestService -v`
Expected: FAIL — `undefined: media.NewService`.

- [ ] **Step 3: Implement the service**

Create `internal/media/service.go`:
```go
package media

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/peer"
)

// Service answers streaming RPCs over the Engine, gated by the access Policy.
// It implements peer.MediaHandler.
type Service struct {
	engine *Engine
	policy *catalog.Policy
}

// NewService wires the Engine and Policy.
func NewService(engine *Engine, policy *catalog.Policy) *Service {
	return &Service{engine: engine, policy: policy}
}

// Playlist returns a named playlist (access-checked).
func (s *Service) Playlist(remote identity.NodeID, contentID, name string) ([]byte, string, bool, error) {
	if !s.policy.Allowed(remote) {
		return nil, "", false, peer.ErrDenied
	}
	data, complete, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, "", false, err
	}
	s.touch(contentID)
	return data, contentType(name), complete, nil
}

// Segment returns a named segment (access-checked).
func (s *Service) Segment(remote identity.NodeID, contentID, name string) ([]byte, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	data, _, err := s.engine.File(context.Background(), contentID, name)
	if err != nil {
		return nil, err
	}
	s.touch(contentID)
	return data, nil
}

// OpenFile opens the original source file for download (access-checked).
func (s *Service) OpenFile(remote identity.NodeID, contentID string) (io.ReadCloser, int64, error) {
	if !s.policy.Allowed(remote) {
		return nil, 0, peer.ErrDenied
	}
	title, ok, err := s.engine.store.Get(contentID)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, peer.ErrNotFound
	}
	f, err := os.Open(title.Path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *Service) touch(contentID string) {
	dir := filepath.Join(s.engine.cacheDir, contentID)
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(name, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(name, ".vtt"):
		return "text/vtt"
	default:
		return "application/octet-stream"
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/media/ -run TestService -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/media/service.go internal/media/service_test.go
git commit -m "feat(media): access-gated streaming service"
```

---

## Task 9: Loopback HTTP bridge

**Files:**
- Create: `internal/bridge/bridge.go`
- Test: `internal/bridge/bridge_test.go`

A loopback HTTP server `hls.js` points at. Routes carry the token in the path (`/s/{token}/{node}/{cid}/{name}`) so relative HLS URIs preserve it. It validates the token + Origin, then pulls playlists/segments via a `Streamer`.

- [ ] **Step 1: Write the failing test**

Create `internal/bridge/bridge_test.go`:
```go
package bridge_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/squallchua/p2p-hls/internal/bridge"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeStreamer struct{}

func (fakeStreamer) Playlist(_ context.Context, _ identity.NodeID, _, name string) ([]byte, string, error) {
	if name == "playlist.m3u8" {
		return []byte("#EXTM3U\nindex.m3u8\n"), "application/vnd.apple.mpegurl", nil
	}
	return []byte("#EXTM3U\nseg00000.ts\n#EXT-X-ENDLIST\n"), "application/vnd.apple.mpegurl", nil
}
func (fakeStreamer) Segment(_ context.Context, _ identity.NodeID, _, _ string) ([]byte, string, error) {
	return []byte("TSBYTES"), "video/mp2t", nil
}

func TestBridgeServesPlaylistAndSegmentWithToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	base := b.BaseURL()
	node := "nodeabc"

	// Correct token + path.
	resp, err := http.Get(base + "/s/secret-token/" + node + "/cid/playlist.m3u8")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "#EXTM3U")
	require.Equal(t, "application/vnd.apple.mpegurl", resp.Header.Get("Content-Type"))

	seg, err := http.Get(base + "/s/secret-token/" + node + "/cid/seg00000.ts")
	require.NoError(t, err)
	defer seg.Body.Close()
	require.Equal(t, http.StatusOK, seg.StatusCode)
	require.Equal(t, "video/mp2t", seg.Header.Get("Content-Type"))
}

func TestBridgeRejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	resp, err := http.Get(b.BaseURL() + "/s/wrong/nodeabc/cid/playlist.m3u8")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/bridge/ -v`
Expected: FAIL — `undefined: bridge.New`.

- [ ] **Step 3: Implement the bridge**

Create `internal/bridge/bridge.go`:
```go
// Package bridge runs the loopback HTTP server that hls.js plays from, pulling
// media over P2P sessions and hiding the P2P layer from the player.
package bridge

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/squallchua/p2p-hls/internal/identity"
)

// Streamer fetches playlists and segments from a remote Host session.
type Streamer interface {
	Playlist(ctx context.Context, host identity.NodeID, contentID, name string) (data []byte, contentType string, err error)
	Segment(ctx context.Context, host identity.NodeID, contentID, name string) (data []byte, contentType string, err error)
}

// Bridge is the loopback HTTP server.
type Bridge struct {
	streamer Streamer
	token    string
	srv      *http.Server
	ln       net.Listener
}

// New constructs a Bridge that requires the given session token.
func New(streamer Streamer, token string) *Bridge {
	return &Bridge{streamer: streamer, token: token}
}

// Start binds addr (use "127.0.0.1:0" for an ephemeral port) and serves.
func (b *Bridge) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	b.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/s/", b.handleStream)
	b.srv = &http.Server{Handler: mux}
	go b.srv.Serve(ln)
	return nil
}

// BaseURL returns the http://127.0.0.1:PORT base.
func (b *Bridge) BaseURL() string { return "http://" + b.ln.Addr().String() }

// Close stops the server.
func (b *Bridge) Close() error { return b.srv.Close() }

// handleStream serves /s/{token}/{node}/{cid}/{name}.
func (b *Bridge) handleStream(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/s/"), "/", 4)
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	token, node, cid, name := parts[0], parts[1], parts[2], parts[3]
	if token != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var (
		data []byte
		ct   string
		err  error
	)
	if strings.HasSuffix(name, ".m3u8") {
		data, ct, err = b.streamer.Playlist(r.Context(), identity.NodeID(node), cid, name)
	} else {
		data, ct, err = b.streamer.Segment(r.Context(), identity.NodeID(node), cid, name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// originOK allows same-origin/no-origin requests (the embedded UI is same-origin).
func (b *Bridge) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // hls.js segment/playlist fetches are typically same-origin (no Origin header)
	}
	return strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "http://localhost")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bridge/ -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/bridge.go internal/bridge/bridge_test.go
git commit -m "feat(bridge): loopback hls http server with token gate"
```

---

## Task 10: Node wiring — stream + verified download

**Files:**
- Modify: `internal/app/node.go`
- Create: `internal/app/download.go`
- Test: `internal/app/download_test.go`

The Node installs the media handler on sessions, adapts itself to `bridge.Streamer` (resolving a session per request), and provides `Download` (stream to disk + verify hash == Content ID).

- [ ] **Step 1: Write the failing download test**

Create `internal/app/download_test.go`:
```go
package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestNodeDownloadVerifiesHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Host shares a file; Content ID = its BLAKE3 hash.
	root := t.TempDir()
	srcPath := filepath.Join(root, "clip.mp4")
	require.NoError(t, os.WriteFile(srcPath, []byte("the-original-bytes"), 0o600))
	cid, err := library.HashFile(srcPath)
	require.NoError(t, err)

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Upsert(library.Title{ContentID: cid, Path: srcPath, VideoCodec: "h264", AudioCodecs: []string{"aac"}}))

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, &fakeRunner2{}, t.TempDir()), policy)

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetMedia(mediaSvc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	dest := filepath.Join(t.TempDir(), "downloaded.mp4")
	require.NoError(t, viewer.Download(ctx, idHost.NodeID(), cid, dest))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "the-original-bytes", string(got))
}

type fakeRunner2 struct{}

func (fakeRunner2) Run(_ context.Context, _ []string) error { return nil }
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/ -run TestNodeDownloadVerifies -v`
Expected: FAIL — `host.SetMedia undefined`.

- [ ] **Step 3: Add media wiring + Streamer to the Node**

In `internal/app/node.go`, add a field to `Node`:
```go
	media *media.Service
```
Add imports `"github.com/squallchua/p2p-hls/internal/media"` and `"context"` (already present). Then add methods:
```go
// SetMedia installs the streaming handler on existing and future sessions.
func (n *Node) SetMedia(svc *media.Service) {
	n.media = svc
	n.mu.Lock()
	for _, s := range n.sessions {
		s.SetMediaHandler(svc)
	}
	n.mu.Unlock()
}

// Playlist implements bridge.Streamer.
func (n *Node) Playlist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, ct, _, err := sess.GetPlaylist(ctx, contentID, name)
	return data, ct, err
}

// Segment implements bridge.Streamer.
func (n *Node) Segment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, err := sess.GetSegment(ctx, contentID, name)
	if err != nil {
		return nil, "", err
	}
	return data, contentTypeFor(name), nil
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(name, ".vtt"):
		return "text/vtt"
	default:
		return "application/octet-stream"
	}
}
```
Add `"strings"` to the `node.go` imports.

- [ ] **Step 4: Install the media handler on session creation**

In `internal/app/node.go` `sessionFor`, after the catalog handler install, add:
```go
	if n.media != nil {
		s.SetMediaHandler(n.media)
	}
```

- [ ] **Step 5: Implement verified download**

Create `internal/app/download.go`:
```go
package app

import (
	"context"
	"fmt"
	"os"

	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
)

// Download streams the original file from host to destPath, then verifies its
// BLAKE3 hash equals the Content ID (rejecting and deleting on mismatch).
func (n *Node) Download(ctx context.Context, host identity.NodeID, contentID, destPath string) error {
	sess, err := n.session(ctx, host)
	if err != nil {
		return err
	}
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if derr := sess.DownloadTo(ctx, contentID, f); derr != nil {
		f.Close()
		os.Remove(tmp)
		return derr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(tmp)
		return cerr
	}

	got, err := library.HashFile(tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got != contentID {
		os.Remove(tmp)
		return fmt.Errorf("app: download integrity check failed (got %s, want %s)", got, contentID)
	}
	return os.Rename(tmp, destPath)
}
```

- [ ] **Step 6: Run the download test + full app suite**

Run: `go test -race ./internal/app/ -v -timeout 60s`
Expected: PASS — verified download plus Slice 1–2 app tests.

- [ ] **Step 7: Commit**

```bash
git add internal/app/node.go internal/app/download.go internal/app/download_test.go
git commit -m "feat(app): stream adapter and hash-verified download"
```

---

## Task 11: End-to-end streaming + download (real ffmpeg, gated)

**Files:**
- Create: `test/stream_e2e_test.go`

The full path with **real ffmpeg**: Host indexes a generated sample, Viewer is allowed, the bridge serves the playlist + segments pulled over P2P, and a verified download succeeds. Skips cleanly without ffmpeg.

- [ ] **Step 1: Write the end-to-end test**

Create `test/stream_e2e_test.go`:
```go
package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/squallchua/p2p-hls/internal/bridge"
	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/media"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestStreamAndDownloadEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// Generate a real 2s H.264/AAC sample.
	root := t.TempDir()
	src := filepath.Join(root, "sample.mp4")
	require.NoError(t, exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src).Run())

	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Host library (real ffprobe) + media service.
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, library.FFProbe{}, []string{root}).ScanOnce(ctx))
	all, _ := store.All()
	require.Len(t, all, 1)
	cid := all[0].ContentID

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, media.ExecRunner{}, t.TempDir()), policy)

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetMedia(mediaSvc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()
	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	// Bridge in front of the viewer.
	br := bridge.New(viewer, "tok")
	require.NoError(t, br.Start("127.0.0.1:0"))
	defer br.Close()
	prefix := br.BaseURL() + "/s/tok/" + string(idHost.NodeID()) + "/" + cid + "/"

	// Master playlist fetch through the bridge.
	resp, err := http.Get(prefix + "playlist.m3u8")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "#EXTM3U")

	// Poll the media (index) playlist until it lists a segment (growing playlist).
	var seg string
	require.Eventually(t, func() bool {
		r, e := http.Get(prefix + "index.m3u8")
		if e != nil {
			return false
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), ".ts") {
				seg = strings.TrimSpace(line)
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond)

	// Fetch the first segment through the bridge.
	sr, err := http.Get(prefix + seg)
	require.NoError(t, err)
	sb, _ := io.ReadAll(sr.Body)
	sr.Body.Close()
	require.Equal(t, http.StatusOK, sr.StatusCode)
	require.NotEmpty(t, sb)

	// Verified download of the original.
	dest := filepath.Join(t.TempDir(), "out.mp4")
	require.NoError(t, viewer.Download(ctx, idHost.NodeID(), cid, dest))
	di, _ := os.Stat(dest)
	si, _ := os.Stat(src)
	require.Equal(t, si.Size(), di.Size(), "downloaded original matches source size")
}
```

- [ ] **Step 2: Run the end-to-end test**

Run: `go test ./test/ -run TestStreamAndDownloadEndToEnd -v -timeout 120s`
Expected: PASS, or SKIP without ffmpeg.

- [ ] **Step 3: Full suite with the race detector**

Run: `go test -race ./... -timeout 240s`
Expected: All packages PASS, no data races (ffmpeg-gated tests skip if absent).

- [ ] **Step 4: Commit**

```bash
git add test/stream_e2e_test.go
git commit -m "test(e2e): stream and verified download over p2p with real ffmpeg"
```

---

## Definition of Done (Slice 3)

- [ ] `go test -race ./...` passes (Slices 1–3); ffmpeg-gated tests pass or skip.
- [ ] A Viewer can stream an allowed Host Title through the loopback bridge (remux and transcode paths), segments pulled over the `bulk` channel.
- [ ] The growing playlist lets playback begin before transcode completes; a not-yet-ready segment yields a retry (`ErrUnavailable`), not an error.
- [ ] Text subtitles appear as a selectable WebVTT track via the master playlist.
- [ ] A download reproduces the original bytes and is rejected on BLAKE3 mismatch.
- [ ] The bridge binds `127.0.0.1` on an ephemeral port and refuses requests with the wrong token or a cross-origin `Origin`.

---

## Self-Review notes

- **Spec coverage:** Implements the spec's *HLS pipeline* (per-stream strict copy/transcode, 1080p/CRF23/veryfast/4s, growing playlist, WebVTT subtitles via master playlist, segment cache 2 GiB/6h LRU+TTL), the *streaming half of the wire protocol* (GetPlaylist/GetSegment + `bulk` chunking with 16 KiB frames + backpressure, ADR 0001), the *loopback bridge security* (127.0.0.1, ephemeral port, token, Origin check), and *download integrity* (original bytes, BLAKE3 == Content ID). Together with Slices 1–2 this completes the spec's success criteria for slices 1–3.
- **Pragmatic simplifications (explicit):** non-trickle ICE and software-only x264 (per spec MVP limits); subtitle sub-playlists are single-segment VOD; backpressure uses a buffered-amount gate with a 10s safety timeout; the cache is evicted whole-dir per Content ID rather than per segment (simpler, still bounded). The Engine serves a segment only once ffmpeg has written it, returning `ErrUnavailable` otherwise — the spec's "not ready, retry."
- **Type consistency:** `peer.MediaHandler` (Task 3) is implemented by `media.Service` (Task 8) with identical signatures; `bridge.Streamer` (Task 9) is implemented by `app.Node` (Task 10); `library.HashFile` (Slice 2) verifies downloads; `peer.ErrUnavailable`/`ErrNotFound`/`ErrDenied` propagate consistently from `media`/`catalog` through the wire `Error` status to the bridge's HTTP codes and `errors.Is` checks.
```
