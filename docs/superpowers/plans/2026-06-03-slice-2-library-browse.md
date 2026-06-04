# Slice 2 (Library + Browse) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Host indexes its Shared folders into a Library; a Viewer (once allowed) browses the Host's Catalog over the `control` channel — including the restricted-by-default access model and the in-app access-request/approve flow.

**Architecture:** Builds on Slice 1. A new `library` package scans Shared folders, content-hashes files with BLAKE3 (ADR 0002), probes metadata with `ffprobe`, detects subtitles, and persists a SQLite index. A new `catalog` package holds the access Policy + pending access Requests and implements a `peer.RequestHandler` that answers `Browse`/`GetMetadata`/`RequestAccess` RPCs after an access check. `peer.Session` gains a generic request/response RPC layer (correlated `Envelope`s with a typed `Error` status) plus the new RPC methods. The `app.Node` wires it together and exposes Browse / RequestAccess / pending-request / approve operations.

**Tech Stack (additions to Slice 1):** `github.com/zeebo/blake3` · `modernc.org/sqlite` (pure-Go, no cgo) · `github.com/fsnotify/fsnotify` · `ffprobe` (external binary, behind an interface).

**Depends on:** Slice 1 (identity, signaling, `peer.Session`, proto Envelope, `app.Node`).

**Out of scope (Slice 3):** ffmpeg remux/transcode, HLS playlists/segments, the loopback HTTP bridge, `bulk`-channel chunking, raw-file download, streaming.

---

## File Structure

| File | Responsibility |
|---|---|
| `proto/peer/v1/peer.proto` (modify) | Add Browse/Catalog/GetMetadata/TitleMeta/SubtitleTrack/RequestAccess/AccessGranted/Ack/Error to the Envelope |
| `internal/peer/errors.go` (create) | Sentinel RPC errors + `Error` status mapping |
| `internal/peer/session.go` (modify) | Generic `call`/dispatch, `RequestHandler` interface, Browse/GetMetadata/RequestAccess/AccessGranted methods |
| `internal/library/hash.go` (create) | BLAKE3 file hashing → Content ID |
| `internal/library/probe.go` (create) | `Prober` interface + `FFProbe` impl + raw ffprobe JSON types |
| `internal/library/metadata.go` (create) | `Title`/`SubtitleTrack` types; build a `Title` from a probe + sidecar scan; `hlsCompatible` rule |
| `internal/library/store.go` (create) | SQLite index: upsert/get/all/delete, mtime cache lookups |
| `internal/library/scanner.go` (create) | Walk roots, eligibility filter, incremental index, fsnotify watcher |
| `internal/catalog/policy.go` (create) | Access Policy (default visibility + allow/block) |
| `internal/catalog/requests.go` (create) | Pending access Requests register |
| `internal/catalog/service.go` (create) | `peer.RequestHandler` impl: access-checked Browse/GetMetadata + RequestAccess; `Title`→`TitleMeta` mapping |
| `internal/app/config.go` (modify) | Add SharedFolders, DefaultVisibility, AllowList, BlockList, DataDir |
| `internal/app/node.go` (modify) | Build library+catalog; install handler; Browse/RequestAccess/PendingRequests/ApproveAccess |
| `test/browse_e2e_test.go` (create) | Two Nodes: denied → request → approve → browse |

**Cross-task type contract (Slice 2):**
- `peer.ErrDenied`, `peer.ErrNotFound`, `peer.ErrUnavailable` (sentinels).
- `peer.RequestHandler` interface: `Browse(remote) ([]*peerv1.TitleMeta, error)`, `GetMetadata(remote, contentID) (*peerv1.TitleMeta, error)`, `RequestAccess(remote, message) error`.
- `(*peer.Session)`: `SetHandler(RequestHandler)`, `OnAccessGranted(func(identity.NodeID))`, `Browse(ctx) ([]*peerv1.TitleMeta, error)`, `GetMetadata(ctx, contentID) (*peerv1.TitleMeta, error)`, `RequestAccess(ctx, message) error`, `SendAccessGranted() error`.
- `library.Title`, `library.SubtitleTrack`, `library.HashFile(path) (string, error)`, `library.Prober`, `library.Probe`, `library.BuildTitle(path, probe, hash, info) (Title, error)`, `library.Store`, `library.Scanner`.
- `catalog.Policy`, `catalog.Requests`, `catalog.Service`, `catalog.Visibility` (`VisibilityRestricted`/`VisibilityPublic`).

---

## Task 1: Extend the proto for browse + access

**Files:**
- Modify: `proto/peer/v1/peer.proto`
- Regenerate: `proto/peer/v1/peer.pb.go`

- [ ] **Step 1: Add the new messages**

Replace the `Envelope` message in `proto/peer/v1/peer.proto` and append the new messages, so the file reads:
```proto
syntax = "proto3";

package peer.v1;

option go_package = "github.com/squallchua/p2p-hls/proto/peer/v1;peerv1";

message Envelope {
  uint64 request_id = 1;
  oneof body {
    Handshake handshake = 2;
    Ping ping = 3;
    Pong pong = 4;
    Browse browse = 5;
    Catalog catalog = 6;
    GetMetadata get_metadata = 7;
    TitleMeta title_meta = 8;
    RequestAccess request_access = 9;
    AccessGranted access_granted = 10;
    Ack ack = 11;
    Error error = 12;
  }
}

message Handshake {
  uint32 protocol_version = 1;
  repeated string capabilities = 2;
}

message Ping { string nonce = 1; }
message Pong { string nonce = 1; }

message Browse {}

message Catalog { repeated TitleMeta titles = 1; }

message GetMetadata { string content_id = 1; }

message TitleMeta {
  string content_id = 1;
  string display_title = 2;
  int64 duration_ms = 3;
  string container = 4;
  string video_codec = 5;
  repeated string audio_codecs = 6;
  int32 width = 7;
  int32 height = 8;
  int64 size_bytes = 9;
  bool hls_compatible = 10;
  repeated SubtitleTrack subtitles = 11;
}

message SubtitleTrack {
  string id = 1;       // "embedded:<index>" or "sidecar:<lang>"
  string language = 2; // ISO code or "und"
  string label = 3;
  string kind = 4;     // "text" | "image"
}

message RequestAccess { string message = 1; }
message AccessGranted {}
message Ack {}

message Error {
  Status status = 1;
  string detail = 2;
  enum Status {
    STATUS_UNSPECIFIED = 0;
    DENIED = 1;
    NOT_FOUND = 2;
    UNAVAILABLE = 3;
    INTERNAL = 4;
  }
}
```

- [ ] **Step 2: Regenerate and verify it builds**

Run: `make proto && go build ./...`
Expected: `proto/peer/v1/peer.pb.go` regenerated; build succeeds. (The existing `framing_test.go` still passes: `go test ./internal/peer/ -run TestEncodeDecodeEnvelope`.)

- [ ] **Step 3: Commit**

```bash
git add proto
git commit -m "feat(proto): browse, metadata, and access-request messages"
```

---

## Task 2: Peer RPC layer (generic call, dispatch, handler)

**Files:**
- Create: `internal/peer/errors.go`
- Modify: `internal/peer/session.go`
- Test: `internal/peer/rpc_test.go`

This generalizes Slice 1's Ping/Pong into a request/response RPC layer: any request `Envelope` correlates to a response `Envelope` (typed result or `Error`). Inbound requests are dispatched to an installed `RequestHandler`.

- [ ] **Step 1: Create the error model**

Create `internal/peer/errors.go`:
```go
package peer

import (
	"errors"
	"fmt"

	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
)

// Sentinel RPC errors. Handlers return these; the wire layer maps them to/from
// Error.Status codes.
var (
	ErrDenied      = errors.New("access denied")
	ErrNotFound    = errors.New("not found")
	ErrUnavailable = errors.New("unavailable")
)

func statusOf(err error) peerv1.Error_Status {
	switch {
	case errors.Is(err, ErrDenied):
		return peerv1.Error_DENIED
	case errors.Is(err, ErrNotFound):
		return peerv1.Error_NOT_FOUND
	case errors.Is(err, ErrUnavailable):
		return peerv1.Error_UNAVAILABLE
	default:
		return peerv1.Error_INTERNAL
	}
}

func statusErr(e *peerv1.Error) error {
	base := fmt.Errorf("remote error: %s", e.GetDetail())
	switch e.GetStatus() {
	case peerv1.Error_DENIED:
		base = ErrDenied
	case peerv1.Error_NOT_FOUND:
		base = ErrNotFound
	case peerv1.Error_UNAVAILABLE:
		base = ErrUnavailable
	}
	if e.GetDetail() != "" {
		return fmt.Errorf("%w: %s", base, e.GetDetail())
	}
	return base
}

func errEnvelope(reqID uint64, err error) *peerv1.Envelope {
	return &peerv1.Envelope{
		RequestId: reqID,
		Body:      &peerv1.Envelope_Error{Error: &peerv1.Error{Status: statusOf(err), Detail: err.Error()}},
	}
}
```

- [ ] **Step 2: Write the failing RPC test**

Create `internal/peer/rpc_test.go`:
```go
package peer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

// fakeHandler answers RPCs for the Host side of a test.
type fakeHandler struct {
	allowed   bool
	titles    []*peerv1.TitleMeta
	requested chan string
}

func (h *fakeHandler) Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	if !h.allowed {
		return nil, ErrDenied
	}
	return h.titles, nil
}
func (h *fakeHandler) GetMetadata(remote identity.NodeID, id string) (*peerv1.TitleMeta, error) {
	for _, t := range h.titles {
		if t.GetContentId() == id {
			return t, nil
		}
	}
	return nil, ErrNotFound
}
func (h *fakeHandler) RequestAccess(remote identity.NodeID, msg string) error {
	h.requested <- msg
	return nil
}

func connectPair(t *testing.T) (viewer, host *Session, hostHandler *fakeHandler) {
	t.Helper()
	cfg := webrtc.Configuration{}
	sig := &directSignaler{dest: map[identity.NodeID]*Session{}}
	idV, _ := identity.Generate()
	idH, _ := identity.Generate()

	viewer, err := NewSession(idV, idH.NodeID(), cfg, sig)
	require.NoError(t, err)
	host, err = NewSession(idH, idV.NodeID(), cfg, sig)
	require.NoError(t, err)

	hostHandler = &fakeHandler{requested: make(chan string, 1)}
	host.SetHandler(hostHandler)

	sig.register(idV.NodeID(), viewer)
	sig.register(idH.NodeID(), host)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, host.Start(ctx, false))
	require.NoError(t, viewer.Start(ctx, true))
	select {
	case <-viewer.Ready():
	case <-ctx.Done():
		t.Fatal("not ready")
	}
	return viewer, host, hostHandler
}

func TestBrowseDeniedThenAllowed(t *testing.T) {
	viewer, _, h := connectPair(t)
	ctx := context.Background()

	_, err := viewer.Browse(ctx)
	require.ErrorIs(t, err, ErrDenied)

	h.allowed = true
	h.titles = []*peerv1.TitleMeta{{ContentId: "cid1", DisplayTitle: "Movie"}}
	got, err := viewer.Browse(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Movie", got[0].GetDisplayTitle())
}

func TestGetMetadataNotFound(t *testing.T) {
	viewer, _, h := connectPair(t)
	h.allowed = true
	_, err := viewer.GetMetadata(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRequestAccessThenAccessGranted(t *testing.T) {
	viewer, host, h := connectPair(t)
	var granted sync.WaitGroup
	granted.Add(1)
	viewer.OnAccessGranted(func(identity.NodeID) { granted.Done() })

	require.NoError(t, viewer.RequestAccess(context.Background(), "please?"))
	require.Equal(t, "please?", <-h.requested)

	require.NoError(t, host.SendAccessGranted())
	done := make(chan struct{})
	go func() { granted.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("viewer never received AccessGranted")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/peer/ -run TestBrowseDenied -v`
Expected: FAIL — `host.SetHandler undefined`.

- [ ] **Step 4: Modify `session.go` — add fields**

In `internal/peer/session.go`, add these fields to the `Session` struct (after `ready chan struct{}`):
```go
	handler         RequestHandler
	onAccessGranted func(identity.NodeID)
```

- [ ] **Step 5: Add the handler interface, setters, and generic call**

Append to `internal/peer/session.go`:
```go
// RequestHandler answers inbound RPCs. Installed per Session by the app layer.
type RequestHandler interface {
	Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error)
	GetMetadata(remote identity.NodeID, contentID string) (*peerv1.TitleMeta, error)
	RequestAccess(remote identity.NodeID, message string) error
}

// SetHandler installs the inbound-request handler.
func (s *Session) SetHandler(h RequestHandler) { s.handler = h }

// OnAccessGranted registers a callback fired when the remote grants us access.
func (s *Session) OnAccessGranted(fn func(identity.NodeID)) { s.onAccessGranted = fn }

// call sends a request envelope and waits for the correlated response.
func (s *Session) call(ctx context.Context, env *peerv1.Envelope) (*peerv1.Envelope, error) {
	id := s.ids.next()
	env.RequestId = id
	ch := make(chan *peerv1.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.send(env); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Session) deliver(env *peerv1.Envelope) {
	s.mu.Lock()
	ch, ok := s.pending[env.RequestId]
	delete(s.pending, env.RequestId)
	s.mu.Unlock()
	if ok {
		ch <- env
	}
}

// Browse fetches the remote's Catalog.
func (s *Session) Browse(ctx context.Context) ([]*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Browse{Browse: &peerv1.Browse{}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetCatalog().GetTitles(), nil
}

// GetMetadata fetches one Title's metadata.
func (s *Session) GetMetadata(ctx context.Context, contentID string) (*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetMetadata{GetMetadata: &peerv1.GetMetadata{ContentId: contentID}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetTitleMeta(), nil
}

// RequestAccess asks the remote Host to allow this Node.
func (s *Session) RequestAccess(ctx context.Context, message string) error {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_RequestAccess{RequestAccess: &peerv1.RequestAccess{Message: message}}})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return statusErr(e)
	}
	return nil // Ack
}

// SendAccessGranted notifies the remote Viewer that access was approved.
func (s *Session) SendAccessGranted() error {
	return s.send(&peerv1.Envelope{Body: &peerv1.Envelope_AccessGranted{AccessGranted: &peerv1.AccessGranted{}}})
}
```

- [ ] **Step 6: Replace the control-channel dispatch**

In `internal/peer/session.go`, replace the entire `dc.OnMessage(func(msg webrtc.DataChannelMessage) { ... })` block inside `bindControl` with:
```go
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		env, err := decodeEnvelope(msg.Data)
		if err != nil {
			return
		}
		switch body := env.Body.(type) {
		case *peerv1.Envelope_Handshake:
			if body.Handshake.GetProtocolVersion() == ProtocolVersion {
				s.readyOnce.Do(func() { close(s.ready) })
			}
		case *peerv1.Envelope_Ping:
			_ = s.send(&peerv1.Envelope{
				RequestId: env.RequestId,
				Body:      &peerv1.Envelope_Pong{Pong: &peerv1.Pong{Nonce: body.Ping.GetNonce()}},
			})
		case *peerv1.Envelope_Browse:
			s.handleBrowse(env.RequestId)
		case *peerv1.Envelope_GetMetadata:
			s.handleGetMetadata(env.RequestId, body.GetMetadata.GetContentId())
		case *peerv1.Envelope_RequestAccess:
			s.handleRequestAccess(env.RequestId, body.RequestAccess.GetMessage())
		case *peerv1.Envelope_AccessGranted:
			if s.onAccessGranted != nil {
				s.onAccessGranted(s.remote)
			}
		case *peerv1.Envelope_Pong, *peerv1.Envelope_Catalog,
			*peerv1.Envelope_TitleMeta, *peerv1.Envelope_Ack, *peerv1.Envelope_Error:
			s.deliver(env)
		}
	})
```

- [ ] **Step 7: Add the inbound request handlers**

Append to `internal/peer/session.go`:
```go
func (s *Session) handleBrowse(reqID uint64) {
	if s.handler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	titles, err := s.handler.Browse(s.remote)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Catalog{Catalog: &peerv1.Catalog{Titles: titles}}})
}

func (s *Session) handleGetMetadata(reqID uint64, contentID string) {
	if s.handler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	meta, err := s.handler.GetMetadata(s.remote, contentID)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_TitleMeta{TitleMeta: meta}})
}

func (s *Session) handleRequestAccess(reqID uint64, message string) {
	if s.handler == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	if err := s.handler.RequestAccess(s.remote, message); err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Ack{Ack: &peerv1.Ack{}}})
}
```

- [ ] **Step 8: Remove the now-duplicated Ping**

The old `Ping` method (from Slice 1) still works but uses the old pending pattern. Replace the Slice 1 `Ping` method body with the generic version:
```go
// Ping sends a ping and returns the echoed nonce.
func (s *Session) Ping(ctx context.Context, nonce string) (string, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: nonce}}})
	if err != nil {
		return "", err
	}
	if e := resp.GetError(); e != nil {
		return "", statusErr(e)
	}
	return resp.GetPong().GetNonce(), nil
}
```

- [ ] **Step 9: Run the tests**

Run: `go test -race ./internal/peer/ -v -timeout 60s`
Expected: PASS — the new RPC tests plus the existing Slice 1 `TestTwoSessionsHandshakeAndPing` (Ping still works through the generic path).

- [ ] **Step 10: Commit**

```bash
git add internal/peer/errors.go internal/peer/session.go internal/peer/rpc_test.go
git commit -m "feat(peer): generic rpc layer, browse/metadata/access handlers"
```

---

## Task 3: BLAKE3 content hashing

**Files:**
- Create: `internal/library/hash.go`
- Test: `internal/library/hash_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/zeebo/blake3@latest`
Expected: `go.mod` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/library/hash_test.go`:
```go
package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

func TestHashFileIsStableAndContentAddressed(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	require.NoError(t, os.WriteFile(a, []byte("identical bytes"), 0o600))
	require.NoError(t, os.WriteFile(b, []byte("identical bytes"), 0o600))

	ha, err := library.HashFile(a)
	require.NoError(t, err)
	hb, err := library.HashFile(b)
	require.NoError(t, err)
	require.Equal(t, ha, hb, "same content => same Content ID")
	require.Len(t, ha, 64) // 32-byte BLAKE3 hex

	require.NoError(t, os.WriteFile(b, []byte("different"), 0o600))
	hb2, err := library.HashFile(b)
	require.NoError(t, err)
	require.NotEqual(t, ha, hb2)
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/library/ -run TestHashFile -v`
Expected: FAIL — `undefined: library.HashFile`.

- [ ] **Step 4: Implement**

Create `internal/library/hash.go`:
```go
// Package library scans Shared folders into an indexed set of Titles.
package library

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// HashFile returns the BLAKE3 hash of the file's full contents, hex-encoded.
// This is a Title's Content ID (ADR 0002).
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/library/ -run TestHashFile -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/library/hash.go internal/library/hash_test.go go.mod go.sum
git commit -m "feat(library): blake3 content hashing"
```

---

## Task 4: Probe + metadata + subtitles

**Files:**
- Create: `internal/library/probe.go`, `internal/library/metadata.go`
- Test: `internal/library/metadata_test.go`, `internal/library/probe_integration_test.go`

`Prober` abstracts `ffprobe` so unit tests use a fake. `BuildTitle` turns a probe result + sidecar scan into a `Title`, deriving `hlsCompatible` (strict: H.264 video + AAC/MP3 audio) and the subtitle track list.

- [ ] **Step 1: Write the failing metadata test**

Create `internal/library/metadata_test.go`:
```go
package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/library/ -run TestBuildTitle -v`
Expected: FAIL — `undefined: library.Probe`.

- [ ] **Step 3: Implement the probe types + interface**

Create `internal/library/probe.go`:
```go
package library

import (
	"context"
	"encoding/json"
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
```

- [ ] **Step 4: Implement metadata building**

Create `internal/library/metadata.go`:
```go
package library

import (
	"os"
	"path/filepath"
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
	ContentID    string
	Path         string
	DisplayTitle string
	Size         int64
	ModUnix      int64
	DurationMS   int64
	Container    string
	VideoCodec   string
	AudioCodecs  []string
	Width        int
	Height       int
	HLSCompatible bool
	Subtitles    []SubtitleTrack
	AddedAt      time.Time
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
			ID:       "embedded:" + itoa(sub.Index),
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
		if !strings.HasPrefix(stem, base) {
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

func itoa(n int) string {
	return strconv.Itoa(n)
}
```

Add the missing import to `metadata.go` — change the import block to:
```go
import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)
```

- [ ] **Step 5: Run the metadata tests**

Run: `go test ./internal/library/ -run 'TestBuildTitle|TestImageSubtitle' -v`
Expected: PASS (all three).

- [ ] **Step 6: Add a real-ffprobe integration test (gated)**

Create `internal/library/probe_integration_test.go`:
```go
package library_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/stretchr/testify/require"
)

// generateSample makes a 1s H.264/AAC mp4 with ffmpeg, skipping if tools are absent.
func generateSample(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", out)
	require.NoError(t, cmd.Run())
	return out
}

func TestFFProbeReadsRealFile(t *testing.T) {
	path := generateSample(t)
	p, err := library.FFProbe{}.Probe(context.Background(), path)
	require.NoError(t, err)
	require.Greater(t, p.DurationMS, int64(500))
	require.NotEmpty(t, p.Video)
	require.Equal(t, "h264", p.Video[0].Codec)
}
```

- [ ] **Step 7: Run the integration test**

Run: `go test ./internal/library/ -run TestFFProbeReadsRealFile -v`
Expected: PASS, or SKIP if ffmpeg/ffprobe are not installed.

- [ ] **Step 8: Commit**

```bash
git add internal/library/probe.go internal/library/metadata.go internal/library/metadata_test.go internal/library/probe_integration_test.go
git commit -m "feat(library): ffprobe metadata, hls-compat rule, subtitle detection"
```

---

## Task 5: SQLite index store

**Files:**
- Create: `internal/library/store.go`
- Test: `internal/library/store_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@latest`
Expected: `go.mod` updated (pure-Go SQLite driver, no cgo).

- [ ] **Step 2: Write the failing test**

Create `internal/library/store_test.go`:
```go
package library_test

import (
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
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
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/library/ -run TestStore -v`
Expected: FAIL — `undefined: library.OpenStore`.

- [ ] **Step 4: Implement the store**

Create `internal/library/store.go`:
```go
package library

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed Library index. Complex fields (audio codecs,
// subtitles) are stored as JSON blobs — fine for the read patterns we have.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS titles (
	content_id     TEXT PRIMARY KEY,
	path           TEXT NOT NULL,
	display_title  TEXT NOT NULL,
	size           INTEGER NOT NULL,
	mod_unix       INTEGER NOT NULL,
	duration_ms    INTEGER NOT NULL,
	container      TEXT NOT NULL,
	video_codec    TEXT NOT NULL,
	audio_codecs   TEXT NOT NULL,
	width          INTEGER NOT NULL,
	height         INTEGER NOT NULL,
	hls_compatible INTEGER NOT NULL,
	subtitles      TEXT NOT NULL,
	added_unix     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_titles_path ON titles(path);
`

// OpenStore opens (creating if needed) the SQLite index at path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts or replaces a Title by Content ID.
func (s *Store) Upsert(t Title) error {
	audio, _ := json.Marshal(t.AudioCodecs)
	subs, _ := json.Marshal(t.Subtitles)
	_, err := s.db.Exec(`
		INSERT INTO titles
			(content_id, path, display_title, size, mod_unix, duration_ms,
			 container, video_codec, audio_codecs, width, height, hls_compatible, subtitles, added_unix)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(content_id) DO UPDATE SET
			path=excluded.path, display_title=excluded.display_title, size=excluded.size,
			mod_unix=excluded.mod_unix, duration_ms=excluded.duration_ms, container=excluded.container,
			video_codec=excluded.video_codec, audio_codecs=excluded.audio_codecs, width=excluded.width,
			height=excluded.height, hls_compatible=excluded.hls_compatible, subtitles=excluded.subtitles`,
		t.ContentID, t.Path, t.DisplayTitle, t.Size, t.ModUnix, t.DurationMS,
		t.Container, t.VideoCodec, string(audio), t.Width, t.Height, boolToInt(t.HLSCompatible),
		string(subs), t.AddedAt.Unix())
	return err
}

// Get returns the Title with the given Content ID.
func (s *Store) Get(contentID string) (Title, bool, error) {
	return s.queryOne(`WHERE content_id = ?`, contentID)
}

// GetByPath returns the Title indexed at path (for the mtime cache).
func (s *Store) GetByPath(path string) (Title, bool, error) {
	return s.queryOne(`WHERE path = ?`, path)
}

// All returns every indexed Title.
func (s *Store) All() ([]Title, error) {
	rows, err := s.db.Query(selectCols + ` FROM titles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete removes a Title by Content ID.
func (s *Store) Delete(contentID string) error {
	_, err := s.db.Exec(`DELETE FROM titles WHERE content_id = ?`, contentID)
	return err
}

const selectCols = `SELECT content_id, path, display_title, size, mod_unix, duration_ms,
	container, video_codec, audio_codecs, width, height, hls_compatible, subtitles, added_unix`

func (s *Store) queryOne(where string, arg any) (Title, bool, error) {
	rows, err := s.db.Query(selectCols+` FROM titles `+where, arg)
	if err != nil {
		return Title{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Title{}, false, rows.Err()
	}
	t, err := scanTitle(rows)
	return t, err == nil, err
}

func scanTitle(rows *sql.Rows) (Title, error) {
	var t Title
	var audioJSON, subsJSON string
	var hls int
	var addedUnix int64
	if err := rows.Scan(&t.ContentID, &t.Path, &t.DisplayTitle, &t.Size, &t.ModUnix, &t.DurationMS,
		&t.Container, &t.VideoCodec, &audioJSON, &t.Width, &t.Height, &hls, &subsJSON, &addedUnix); err != nil {
		return Title{}, err
	}
	t.HLSCompatible = hls != 0
	_ = json.Unmarshal([]byte(audioJSON), &t.AudioCodecs)
	_ = json.Unmarshal([]byte(subsJSON), &t.Subtitles)
	return t, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run the store tests**

Run: `go test ./internal/library/ -run TestStore -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/library/store.go internal/library/store_test.go go.mod go.sum
git commit -m "feat(library): sqlite index store"
```

---

## Task 6: Scanner + watcher

**Files:**
- Create: `internal/library/scanner.go`
- Test: `internal/library/scanner_test.go`

The Scanner walks Shared folders, filters by extension, skips unchanged files via the `(path, size, mtime)` cache, and hashes+probes new/changed files into the Store. `Watch` re-scans on fsnotify events (debounced).

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/fsnotify/fsnotify@latest`
Expected: `go.mod` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/library/scanner_test.go`:
```go
package library_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/library"
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
	require.NoError(t, os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("video-bytes"), 0o600))
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
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/library/ -run TestScanOnce -v`
Expected: FAIL — `undefined: library.NewScanner`.

- [ ] **Step 4: Implement the scanner**

Create `internal/library/scanner.go`:
```go
package library

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// eligibleExts are the video container extensions the Scanner indexes.
var eligibleExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".m4v": true, ".webm": true,
	".avi": true, ".ts": true, ".mpg": true, ".mpeg": true, ".wmv": true, ".flv": true,
}

// Scanner indexes Shared folders into a Store.
type Scanner struct {
	store  *Store
	prober Prober
	roots  []string
}

// NewScanner constructs a Scanner over the given roots.
func NewScanner(store *Store, prober Prober, roots []string) *Scanner {
	return &Scanner{store: store, prober: prober, roots: roots}
}

// ScanOnce walks every root and indexes new or changed eligible files.
func (sc *Scanner) ScanOnce(ctx context.Context) error {
	for _, root := range sc.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !eligibleExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			sc.indexFile(ctx, path, d)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (sc *Scanner) indexFile(ctx context.Context, path string, d fs.DirEntry) {
	info, err := d.Info()
	if err != nil {
		return
	}
	// mtime cache: skip if an existing entry matches path+size+mtime.
	if existing, ok, _ := sc.store.GetByPath(path); ok &&
		existing.Size == info.Size() && existing.ModUnix == info.ModTime().Unix() {
		return
	}
	contentID, err := HashFile(path)
	if err != nil {
		slog.Warn("hash failed", "path", path, "err", err)
		return
	}
	probe, err := sc.prober.Probe(ctx, path)
	if err != nil {
		slog.Warn("probe failed", "path", path, "err", err)
		return
	}
	title, err := BuildTitle(path, probe, contentID, FileInfo{Size: info.Size(), ModUnix: info.ModTime().Unix()})
	if err != nil {
		return
	}
	if err := sc.store.Upsert(title); err != nil {
		slog.Warn("index upsert failed", "path", path, "err", err)
	}
}

// Watch re-scans (debounced) whenever a root changes, until ctx is cancelled.
func (sc *Scanner) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	for _, root := range sc.roots {
		_ = w.Add(root)
	}

	var mu sync.Mutex
	var timer *time.Timer
	debounce := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(500*time.Millisecond, func() { _ = sc.ScanOnce(ctx) })
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-w.Events:
			if !ok {
				return nil
			}
			debounce()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watcher error", "err", err)
		}
	}
}
```

- [ ] **Step 5: Run the scanner tests**

Run: `go test ./internal/library/ -run TestScanOnce -v`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
git add internal/library/scanner.go internal/library/scanner_test.go go.mod go.sum
git commit -m "feat(library): scanner with mtime cache and fsnotify watcher"
```

---

## Task 7: Access policy + requests

**Files:**
- Create: `internal/catalog/policy.go`, `internal/catalog/requests.go`
- Test: `internal/catalog/policy_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/policy_test.go`:
```go
package catalog_test

import (
	"testing"

	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestRestrictedPolicyAllowsOnlyAllowList(t *testing.T) {
	a, b := identity.NodeID("alice"), identity.NodeID("bob")
	p := catalog.NewPolicy(catalog.VisibilityRestricted)
	require.False(t, p.Allowed(a))
	p.AddAllow(a)
	require.True(t, p.Allowed(a))
	require.False(t, p.Allowed(b))
}

func TestPublicPolicyAllowsExceptBlocked(t *testing.T) {
	a := identity.NodeID("alice")
	p := catalog.NewPolicy(catalog.VisibilityPublic)
	require.True(t, p.Allowed(a))
	p.AddBlock(a)
	require.False(t, p.Allowed(a), "block overrides public")
}

func TestRequestsAddListApprove(t *testing.T) {
	r := catalog.NewRequests()
	n := identity.NodeID("bob")
	r.Add(n, "let me in")
	require.Equal(t, []identity.NodeID{n}, r.List())
	msg, ok := r.Take(n)
	require.True(t, ok)
	require.Equal(t, "let me in", msg)
	require.Empty(t, r.List())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -v`
Expected: FAIL — `undefined: catalog.NewPolicy`.

- [ ] **Step 3: Implement policy**

Create `internal/catalog/policy.go`:
```go
// Package catalog enforces access control and answers browse RPCs.
package catalog

import (
	"sync"

	"github.com/squallchua/p2p-hls/internal/identity"
)

// Visibility is a Library's default access posture.
type Visibility string

const (
	VisibilityRestricted Visibility = "restricted"
	VisibilityPublic     Visibility = "public"
)

// Policy decides whether a remote Node may see this Node's Catalog.
type Policy struct {
	mu      sync.RWMutex
	def     Visibility
	allow   map[identity.NodeID]bool
	block   map[identity.NodeID]bool
}

// NewPolicy creates a Policy with the given default visibility.
func NewPolicy(def Visibility) *Policy {
	return &Policy{def: def, allow: map[identity.NodeID]bool{}, block: map[identity.NodeID]bool{}}
}

// Allowed evaluates: block always denies; restricted => allow-list only;
// public => everyone except block.
func (p *Policy) Allowed(node identity.NodeID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.block[node] {
		return false
	}
	if p.def == VisibilityPublic {
		return true
	}
	return p.allow[node]
}

func (p *Policy) AddAllow(node identity.NodeID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allow[node] = true
}

func (p *Policy) AddBlock(node identity.NodeID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.block[node] = true
}
```

- [ ] **Step 4: Implement requests**

Create `internal/catalog/requests.go`:
```go
package catalog

import (
	"sync"

	"github.com/squallchua/p2p-hls/internal/identity"
)

// Requests holds pending access requests awaiting the User's approval.
type Requests struct {
	mu      sync.Mutex
	pending map[identity.NodeID]string
}

// NewRequests creates an empty register.
func NewRequests() *Requests {
	return &Requests{pending: map[identity.NodeID]string{}}
}

// Add records (or updates) a pending request from node with an optional message.
func (r *Requests) Add(node identity.NodeID, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[node] = message
}

// List returns the Node IDs with pending requests.
func (r *Requests) List() []identity.NodeID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]identity.NodeID, 0, len(r.pending))
	for n := range r.pending {
		out = append(out, n)
	}
	return out
}

// Take removes and returns a pending request's message.
func (r *Requests) Take(node identity.NodeID) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg, ok := r.pending[node]
	delete(r.pending, node)
	return msg, ok
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/catalog/ -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/policy.go internal/catalog/requests.go internal/catalog/policy_test.go
git commit -m "feat(catalog): access policy and pending-request register"
```

---

## Task 8: Catalog service (RequestHandler impl)

**Files:**
- Create: `internal/catalog/service.go`
- Test: `internal/catalog/service_test.go`

The Service implements `peer.RequestHandler`: it access-checks each request against the Policy, reads Titles from the Store, maps them to `TitleMeta`, and records access requests.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/service_test.go`:
```go
package catalog_test

import (
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/catalog"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

func newServiceWithTitle(t *testing.T) (*catalog.Service, *catalog.Policy, *catalog.Requests) {
	t.Helper()
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid-1", DisplayTitle: "Movie", DurationMS: 5000,
		VideoCodec: "h264", AudioCodecs: []string{"aac"}, Width: 1920, Height: 1080,
		HLSCompatible: true,
		Subtitles:     []library.SubtitleTrack{{ID: "embedded:2", Language: "eng", Kind: "text"}},
	}))
	policy := catalog.NewPolicy(catalog.VisibilityRestricted)
	reqs := catalog.NewRequests()
	return catalog.NewService(store, policy, reqs), policy, reqs
}

func TestServiceBrowseDeniedByDefault(t *testing.T) {
	svc, _, _ := newServiceWithTitle(t)
	_, err := svc.Browse(identity.NodeID("bob"))
	require.ErrorIs(t, err, peer.ErrDenied)
}

func TestServiceBrowseAfterAllow(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "cid-1", titles[0].GetContentId())
	require.True(t, titles[0].GetHlsCompatible())
	require.Len(t, titles[0].GetSubtitles(), 1)
	require.Equal(t, "eng", titles[0].GetSubtitles()[0].GetLanguage())
}

func TestServiceGetMetadataNotFound(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	_, err := svc.GetMetadata(identity.NodeID("bob"), "nope")
	require.ErrorIs(t, err, peer.ErrNotFound)
}

func TestServiceRequestAccessRecorded(t *testing.T) {
	svc, _, reqs := newServiceWithTitle(t)
	require.NoError(t, svc.RequestAccess(identity.NodeID("bob"), "pls"))
	require.Equal(t, []identity.NodeID{identity.NodeID("bob")}, reqs.List())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestService -v`
Expected: FAIL — `undefined: catalog.NewService`.

- [ ] **Step 3: Implement the service**

Create `internal/catalog/service.go`:
```go
package catalog

import (
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/library"
	"github.com/squallchua/p2p-hls/internal/peer"
	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
)

// Service answers browse RPCs from Viewers, enforcing the access Policy.
// It implements peer.RequestHandler.
type Service struct {
	store  *library.Store
	policy *Policy
	reqs   *Requests
}

// NewService wires the Store, Policy, and Requests together.
func NewService(store *library.Store, policy *Policy, reqs *Requests) *Service {
	return &Service{store: store, policy: policy, reqs: reqs}
}

// Browse returns the Catalog visible to remote, or peer.ErrDenied.
func (s *Service) Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	titles, err := s.store.All()
	if err != nil {
		return nil, err
	}
	out := make([]*peerv1.TitleMeta, 0, len(titles))
	for _, t := range titles {
		out = append(out, toMeta(t))
	}
	return out, nil
}

// GetMetadata returns one Title's metadata, or peer.ErrDenied/peer.ErrNotFound.
func (s *Service) GetMetadata(remote identity.NodeID, contentID string) (*peerv1.TitleMeta, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	t, ok, err := s.store.Get(contentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, peer.ErrNotFound
	}
	return toMeta(t), nil
}

// RequestAccess records a pending access request from remote.
func (s *Service) RequestAccess(remote identity.NodeID, message string) error {
	s.reqs.Add(remote, message)
	return nil
}

func toMeta(t library.Title) *peerv1.TitleMeta {
	m := &peerv1.TitleMeta{
		ContentId:     t.ContentID,
		DisplayTitle:  t.DisplayTitle,
		DurationMs:    t.DurationMS,
		Container:     t.Container,
		VideoCodec:    t.VideoCodec,
		AudioCodecs:   t.AudioCodecs,
		Width:         int32(t.Width),
		Height:        int32(t.Height),
		SizeBytes:     t.Size,
		HlsCompatible: t.HLSCompatible,
	}
	for _, sub := range t.Subtitles {
		m.Subtitles = append(m.Subtitles, &peerv1.SubtitleTrack{
			Id: sub.ID, Language: sub.Language, Label: sub.Label, Kind: sub.Kind,
		})
	}
	return m
}
```

- [ ] **Step 4: Run the service tests**

Run: `go test ./internal/catalog/ -run TestService -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/service.go internal/catalog/service_test.go
git commit -m "feat(catalog): access-checked browse/metadata service"
```

---

## Task 9: App wiring (config, node operations)

**Files:**
- Modify: `internal/app/config.go`, `internal/app/node.go`
- Test: `internal/app/node_browse_test.go`

- [ ] **Step 1: Extend the config**

In `internal/app/config.go`, add fields to `Config`:
```go
	SharedFolders     []string `toml:"shared_folders"`
	DefaultVisibility string   `toml:"default_visibility"` // "restricted" | "public"
	AllowList         []string `toml:"allow_list"`
	BlockList         []string `toml:"block_list"`
	DataDir           string   `toml:"data_dir"`
```
And in `defaults()`, add:
```go
		DefaultVisibility: "restricted",
```

- [ ] **Step 2: Write the failing node test**

Create `internal/app/node_browse_test.go`:
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
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

type stubProber struct{}

func (stubProber) Probe(context.Context, string) (library.Probe, error) {
	return library.Probe{
		DurationMS: 1000, Container: "mp4",
		Video: []library.VideoStream{{Codec: "h264", Width: 1280, Height: 720}},
		Audio: []library.AudioStream{{Codec: "aac"}},
	}, nil
}

func hostLibrary(t *testing.T) *catalog.Service {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "film.mp4"), []byte("bytes"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, library.NewScanner(store, stubProber{}, []string{root}).ScanOnce(context.Background()))
	return catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests())
}

func TestNodeBrowseDeniedThenApprovedFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetCatalog(hostLibrary(t))

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	// Denied first.
	_, err = viewer.Browse(ctx, idHost.NodeID())
	require.ErrorIs(t, err, peer.ErrDenied)

	// Request access; host sees it and approves.
	require.NoError(t, viewer.RequestAccess(ctx, idHost.NodeID(), "friend here"))
	require.Eventually(t, func() bool { return len(host.PendingRequests()) == 1 }, 3*time.Second, 25*time.Millisecond)
	require.NoError(t, host.ApproveAccess(idViewer.NodeID()))

	// Now browse succeeds.
	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "Film", titles[0].GetDisplayTitle())
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/app/ -run TestNodeBrowseDenied -v`
Expected: FAIL — `host.SetCatalog undefined`.

- [ ] **Step 4: Extend the Node**

In `internal/app/node.go`, add a `catalog` field and a guard to the `Node` struct:
```go
	catalog *catalog.Service
```
Add the import `"github.com/squallchua/p2p-hls/internal/catalog"` and `peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"`.

Then add these methods to `internal/app/node.go`:
```go
// SetCatalog installs the Service that answers inbound browse RPCs. Existing and
// future sessions use it as their request handler.
func (n *Node) SetCatalog(svc *catalog.Service) {
	n.catalog = svc
	n.mu.Lock()
	for _, s := range n.sessions {
		s.SetHandler(svc)
	}
	n.mu.Unlock()
}

// Browse returns the remote Host's Catalog.
func (n *Node) Browse(ctx context.Context, remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	sess, err := n.session(ctx, remote)
	if err != nil {
		return nil, err
	}
	return sess.Browse(ctx)
}

// RequestAccess asks the remote Host to allow this Node.
func (n *Node) RequestAccess(ctx context.Context, remote identity.NodeID, message string) error {
	sess, err := n.session(ctx, remote)
	if err != nil {
		return err
	}
	return sess.RequestAccess(ctx, message)
}

// PendingRequests lists Node IDs awaiting our approval.
func (n *Node) PendingRequests() []identity.NodeID {
	if n.catalog == nil {
		return nil
	}
	return n.catalog.Requests().List()
}

// ApproveAccess allows remote, then notifies it via AccessGranted.
func (n *Node) ApproveAccess(remote identity.NodeID) error {
	if n.catalog == nil {
		return fmt.Errorf("app: no catalog installed")
	}
	n.catalog.Approve(remote)
	n.mu.Lock()
	sess := n.sessions[remote]
	n.mu.Unlock()
	if sess != nil {
		return sess.SendAccessGranted()
	}
	return nil
}

// session returns a ready session to remote, dialing if necessary.
func (n *Node) session(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	n.mu.Lock()
	s, ok := n.sessions[remote]
	n.mu.Unlock()
	if ok {
		return s, nil
	}
	return n.Dial(ctx, remote)
}
```
Add the import `"fmt"` to `node.go` if not already present.

- [ ] **Step 5: Install the handler when sessions are created**

In `internal/app/node.go`, modify `sessionFor` so that immediately after `n.sessions[remote] = s`, the catalog handler is installed:
```go
	n.sessions[remote] = s
	if n.catalog != nil {
		s.SetHandler(n.catalog)
	}
	if !initiator {
		_ = s.Start(context.Background(), false)
	}
	return s
```

- [ ] **Step 6: Expose Requests + Approve on the catalog Service**

In `internal/catalog/service.go`, add:
```go
// Requests exposes the pending-request register.
func (s *Service) Requests() *Requests { return s.reqs }

// Approve allows the Node and clears its pending request.
func (s *Service) Approve(node identity.NodeID) {
	s.reqs.Take(node)
	s.policy.AddAllow(node)
}
```

- [ ] **Step 7: Run the node browse test**

Run: `go test ./internal/app/ -run TestNodeBrowseDenied -v -timeout 60s`
Expected: PASS — the full denied → request → approve → browse flow over a real signaling server.

- [ ] **Step 8: Run the whole package suite**

Run: `go test -race ./... -timeout 120s`
Expected: All packages PASS (Slice 1 tests still green).

- [ ] **Step 9: Commit**

```bash
git add internal/app/config.go internal/app/node.go internal/catalog/service.go internal/app/node_browse_test.go
git commit -m "feat(app): node browse, request-access, and approve operations"
```

---

## Task 10: End-to-end browse-after-approval

**Files:**
- Create: `test/browse_e2e_test.go`

This mirrors the real product flow with Nodes built from config + a real Library scan (using a stub prober so it needs no ffmpeg).

- [ ] **Step 1: Write the end-to-end test**

Create `test/browse_e2e_test.go`:
```go
package e2e_test

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
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

type e2eProber struct{}

func (e2eProber) Probe(context.Context, string) (library.Probe, error) {
	return library.Probe{
		DurationMS: 2000, Container: "matroska",
		Video: []library.VideoStream{{Codec: "h264", Width: 1920, Height: 1080}},
		Audio: []library.AudioStream{{Codec: "aac"}},
	}, nil
}

func TestBrowseAfterApprovalEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Host library with one Title.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "The.Matrix.1999.mkv"), []byte("video"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "host.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, e2eProber{}, []string{root}).ScanOnce(ctx))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests())

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetCatalog(svc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	_, err = viewer.Browse(ctx, idHost.NodeID())
	require.ErrorIs(t, err, peer.ErrDenied)

	require.NoError(t, viewer.RequestAccess(ctx, idHost.NodeID(), "hi"))
	require.Eventually(t, func() bool { return len(host.PendingRequests()) == 1 }, 3*time.Second, 25*time.Millisecond)
	require.NoError(t, host.ApproveAccess(idViewer.NodeID()))

	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "The Matrix 1999", titles[0].GetDisplayTitle())
	require.True(t, titles[0].GetHlsCompatible())
}
```

- [ ] **Step 2: Run the end-to-end test**

Run: `go test ./test/ -run TestBrowseAfterApprovalEndToEnd -v -timeout 60s`
Expected: PASS.

- [ ] **Step 3: Full suite with race detector**

Run: `go test -race ./... -timeout 180s`
Expected: All PASS, no data races.

- [ ] **Step 4: Commit**

```bash
git add test/browse_e2e_test.go
git commit -m "test(e2e): browse host catalog after access approval"
```

---

## Definition of Done (Slice 2)

- [ ] `go test -race ./...` passes (Slices 1 + 2).
- [ ] A Host's Shared folder scans into a SQLite Library (BLAKE3 Content IDs, ffprobe metadata, subtitle tracks, `hlsCompatible`).
- [ ] A restricted Host denies an unknown Viewer's Browse; the Viewer can RequestAccess; the Host can approve; Browse then returns the Catalog.
- [ ] Real `ffprobe` integration test passes (or skips cleanly without ffmpeg).

---

## Self-Review notes

- **Spec coverage:** Implements the spec's *Content ID & library indexing* (BLAKE3 full hash, mtime cache, eligibility, ffprobe metadata, subtitle detection text/image), *Access control* (restricted default, allow/block, per-Library, access-request/approve via `RequestAccess`/`AccessGranted`), and the browse half of the wire protocol (`Browse`/`GetMetadata` + `Error` status model from ADR 0001). `Title`→`TitleMeta` carries everything Slice 3's streaming needs (codecs, `hlsCompatible`, subtitle tracks).
- **Deferred (Slice 3):** ffmpeg/HLS, the `bulk` channel chunking, the loopback bridge, raw-file download. The watcher's `Watch` is implemented but only `ScanOnce` is unit-tested (watcher behavior is timing-dependent; covered by manual smoke).
- **Type consistency:** `peer.RequestHandler` (Task 2) is implemented by `catalog.Service` (Task 8) with identical signatures; `peer.ErrDenied`/`ErrNotFound` flow from `catalog` returns through the wire `Error` status and back to `errors.Is` checks in tests; `library.Title` fields map 1:1 into `peerv1.TitleMeta` in `toMeta`.
