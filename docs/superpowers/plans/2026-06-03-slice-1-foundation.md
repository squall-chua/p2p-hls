# Slice 1 (Foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two Nodes, started on different machines, can find each other via a signaling server and open a direct, identity-verified WebRTC connection, proven by a Ping/Pong over the `control` data channel.

**Architecture:** A Go monorepo with two binaries — the `node` app and the `signal-server`. Nodes connect to the signaling server over JSON/WebSocket (presence + relay only). They establish a Pion WebRTC PeerConnection using **non-trickle ICE** (full SDP exchanged once, candidates embedded). Every SDP is **signed with the Node's Ed25519 key including the DTLS fingerprint** (ADR 0003), so a malicious server cannot MITM. Two reliable data channels open per pair — `control` (Protobuf RPCs) and `bulk` (opened now, used in Slice 3). A protocol-version handshake runs on `control`, then a Ping RPC proves the round-trip.

**Tech Stack:** Go 1.23 · `github.com/pion/webrtc/v4` · `github.com/gorilla/websocket` · `google.golang.org/protobuf` · `github.com/pelletier/go-toml/v2` · `github.com/stretchr/testify` · stdlib `crypto/ed25519`, `log/slog`.

**Module path:** `github.com/squallchua/p2p-hls`

**Out of scope (later slices):** library scan/index, catalog/browse, ffmpeg/HLS, the loopback HTTP bridge, bulk-channel chunking/reassembly, trickle ICE, TURN, access-control policy, the web UI.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `Makefile`, `.gitignore` | Module, build/test/proto targets, ignores |
| `proto/peer/v1/peer.proto` | Control-channel message schema (Envelope, Handshake, Ping, Pong) |
| `proto/peer/v1/peer.pb.go` | Generated Go (do not hand-edit) |
| `internal/identity/identity.go` | Ed25519 keypair, Node ID, load/generate, sign/verify |
| `internal/signaling/message.go` | JSON message types + envelope marshal helpers |
| `internal/signaling/client.go` | WS client: challenge/register, presence stream, relay send/recv |
| `internal/signalserver/server.go` | WS server: challenge, registry, presence push, relay routing |
| `internal/peer/binding.go` | Signed signal (SDP) create/verify (ADR 0003) |
| `internal/peer/framing.go` | Encode/decode control Envelopes; request-id allocation |
| `internal/peer/session.go` | Pion connection, channels, version handshake, Ping RPC |
| `internal/app/config.go` | `config.toml` load + defaults; config/data dir helpers |
| `internal/app/node.go` | Orchestrator: wires signaling client ↔ peer sessions |
| `cmd/signal-server/main.go` | Signaling server entrypoint |
| `cmd/node/main.go` | Node entrypoint (connect, list presence, dial+ping a Node) |
| `test/e2e_test.go` | Two Nodes + real signaling server, loopback, Ping round-trip |

**Cross-task type contract** (names that MUST stay consistent):
- `identity.NodeID` (string), `identity.Identity`, `identity.Generate()`, `identity.LoadOrCreate(path)`, `(*Identity).NodeID()`, `(*Identity).PublicKey()`, `(*Identity).Sign(msg)`, `identity.NodeIDFromPublicKey(pub)`, `identity.Verify(pub, msg, sig)`.
- `signaling.PeerInfo{NodeID string; PublicKey []byte; DisplayName string}`.
- `peer.SignedSignal{From string; PublicKey []byte; SDP []byte; Signature []byte}`, `peer.SignSignal(id, desc)`, `peer.VerifySignal(s)`.
- `peer.Signaler` interface with `SendSignal(to identity.NodeID, s SignedSignal) error`.
- `peer.Session`, `peer.NewSession(...)`, `(*Session).Start(ctx, initiator bool) error`, `(*Session).HandleSignal(s SignedSignal) error`, `(*Session).Ready() <-chan struct{}`, `(*Session).Ping(ctx, nonce string) (string, error)`, `(*Session).Close() error`.

---

## Task 0: Project scaffolding & dependencies

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`, `internal/buildcheck/buildcheck_test.go`

- [ ] **Step 1: Initialize the module and add dependencies**

Run:
```bash
cd /home/mwchua/p2p-hls
go mod init github.com/squallchua/p2p-hls
go get github.com/pion/webrtc/v4@latest
go get github.com/gorilla/websocket@latest
go get google.golang.org/protobuf@latest
go get github.com/pelletier/go-toml/v2@latest
go get github.com/stretchr/testify@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
```
Expected: `go.mod` and `go.sum` created with the five `require` entries. (`protoc-gen-go` lands in `$(go env GOPATH)/bin` — ensure that's on `PATH`.)

- [ ] **Step 2: Create `.gitignore`**

```gitignore
/bin/
*.test
*.out
/dist/
node_modules/
```

- [ ] **Step 3: Create the `Makefile`**

```makefile
.PHONY: proto test build tidy

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/peer/v1/peer.proto

test:
	go test ./...

build:
	go build -o bin/node ./cmd/node
	go build -o bin/signal-server ./cmd/signal-server

tidy:
	go mod tidy
```

- [ ] **Step 4: Add a build-sanity test**

Create `internal/buildcheck/buildcheck_test.go`:
```go
package buildcheck

import "testing"

func TestModuleBuilds(t *testing.T) {
	// Presence of this passing test proves the module compiles and `go test` runs.
}
```

- [ ] **Step 5: Verify the toolchain**

Run: `go test ./... && protoc --version`
Expected: `ok github.com/squallchua/p2p-hls/internal/buildcheck`, and protoc prints a version (e.g. `libprotoc 3.x`). If `protoc` is missing, install it (`apt install -y protobuf-compiler` or `brew install protobuf`).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum Makefile .gitignore internal/buildcheck
git commit -m "chore: scaffold Go module, deps, Makefile"
```

---

## Task 1: Identity package

**Files:**
- Create: `internal/identity/identity.go`
- Test: `internal/identity/identity_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/identity/identity_test.go`:
```go
package identity_test

import (
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestNodeIDIsStableFingerprintOfPublicKey(t *testing.T) {
	id, err := identity.Generate()
	require.NoError(t, err)
	require.Equal(t, id.NodeID(), identity.NodeIDFromPublicKey(id.PublicKey()))
	require.Len(t, string(id.NodeID()), 52) // base32(sha256) unpadded
}

func TestSignVerifyRoundTrip(t *testing.T) {
	id, err := identity.Generate()
	require.NoError(t, err)
	msg := []byte("hello watch party")
	sig := id.Sign(msg)
	require.True(t, identity.Verify(id.PublicKey(), msg, sig))
	require.False(t, identity.Verify(id.PublicKey(), []byte("tampered"), sig))
}

func TestLoadOrCreatePersistsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	first, err := identity.LoadOrCreate(path)
	require.NoError(t, err)
	second, err := identity.LoadOrCreate(path)
	require.NoError(t, err)
	require.Equal(t, first.NodeID(), second.NodeID(), "reloading must yield the same identity")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/identity/ -v`
Expected: FAIL — `undefined: identity.Generate` (package doesn't exist yet).

- [ ] **Step 3: Write the implementation**

Create `internal/identity/identity.go`:
```go
// Package identity provides a Node's Ed25519 keypair and its derived Node ID.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NodeID is the canonical, stable address of a Node: a fingerprint of its public key.
type NodeID string

// Identity is a Node's keypair.
type Identity struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   NodeID
}

// Generate creates a fresh random identity.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return &Identity{priv: priv, pub: pub, id: NodeIDFromPublicKey(pub)}, nil
}

// LoadOrCreate loads the identity seed from path, or generates and persists one (0600).
func LoadOrCreate(path string) (*Identity, error) {
	seed, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("identity file %s: bad seed length %d", path, len(seed))
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		return &Identity{priv: priv, pub: pub, id: NodeIDFromPublicKey(pub)}, nil
	case os.IsNotExist(err):
		id, gerr := Generate()
		if gerr != nil {
			return nil, gerr
		}
		if mkerr := os.MkdirAll(filepath.Dir(path), 0o700); mkerr != nil {
			return nil, fmt.Errorf("create identity dir: %w", mkerr)
		}
		if werr := os.WriteFile(path, id.priv.Seed(), 0o600); werr != nil {
			return nil, fmt.Errorf("write identity: %w", werr)
		}
		return id, nil
	default:
		return nil, fmt.Errorf("read identity %s: %w", path, err)
	}
}

// NodeID returns the Node's stable identifier.
func (i *Identity) NodeID() NodeID { return i.id }

// PublicKey returns the Node's Ed25519 public key.
func (i *Identity) PublicKey() ed25519.PublicKey { return i.pub }

// Sign signs msg with the Node's private key.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.priv, msg) }

// NodeIDFromPublicKey derives the Node ID from a public key:
// lowercase, unpadded base32 of SHA-256(pubkey).
func NodeIDFromPublicKey(pub ed25519.PublicKey) NodeID {
	sum := sha256.Sum256(pub)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return NodeID(strings.ToLower(enc.EncodeToString(sum[:])))
}

// Verify reports whether sig is a valid signature of msg by pub.
func Verify(pub ed25519.PublicKey, msg, sig []byte) bool {
	return ed25519.Verify(pub, msg, sig)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/identity/ -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add internal/identity
git commit -m "feat(identity): ed25519 keypair, node id, sign/verify, persistence"
```

---

## Task 2: Signaling JSON message types

**Files:**
- Create: `internal/signaling/message.go`
- Test: `internal/signaling/message_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/signaling/message_test.go`:
```go
package signaling_test

import (
	"testing"

	"github.com/squallchua/p2p-hls/internal/signaling"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	in := signaling.Register{
		NodeID:      "abc",
		PublicKey:   []byte{1, 2, 3},
		DisplayName: "alice",
		Signature:   []byte{9, 9},
	}
	raw, err := signaling.Marshal(in)
	require.NoError(t, err)

	typ, env, err := signaling.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeRegister, typ)

	out, ok := env.(*signaling.Register)
	require.True(t, ok)
	require.Equal(t, in.NodeID, out.NodeID)
	require.Equal(t, in.PublicKey, out.PublicKey)
	require.Equal(t, in.DisplayName, out.DisplayName)
}

func TestUnmarshalUnknownTypeErrors(t *testing.T) {
	_, _, err := signaling.Unmarshal([]byte(`{"type":"bogus","data":{}}`))
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/signaling/ -v`
Expected: FAIL — `undefined: signaling.Register`.

- [ ] **Step 3: Write the implementation**

Create `internal/signaling/message.go`:
```go
// Package signaling defines the JSON messages exchanged between a Node and the
// signaling server, plus a client to speak them.
package signaling

import (
	"encoding/json"
	"fmt"
)

// MsgType identifies a signaling message.
type MsgType string

const (
	TypeChallenge        MsgType = "challenge"
	TypeRegister         MsgType = "register"
	TypePresenceSnapshot MsgType = "presence_snapshot"
	TypePresenceJoin     MsgType = "presence_join"
	TypePresenceLeave    MsgType = "presence_leave"
	TypeRelay            MsgType = "relay"
	TypeError            MsgType = "error"
)

// PeerInfo describes an online Node as seen via presence.
type PeerInfo struct {
	NodeID      string `json:"node_id"`
	PublicKey   []byte `json:"public_key"`
	DisplayName string `json:"display_name"`
}

// Challenge is sent by the server on connect; the client must sign Nonce.
type Challenge struct {
	Nonce []byte `json:"nonce"`
}

// Register is the client's reply: identity plus a signature over the challenge nonce.
type Register struct {
	NodeID      string `json:"node_id"`
	PublicKey   []byte `json:"public_key"`
	DisplayName string `json:"display_name"`
	Signature   []byte `json:"signature"` // Sign(nonce)
}

// PresenceSnapshot is the full set of other online Nodes, sent right after register.
type PresenceSnapshot struct {
	Peers []PeerInfo `json:"peers"`
}

// PresenceJoin / PresenceLeave are incremental presence updates.
type PresenceJoin struct {
	Peer PeerInfo `json:"peer"`
}
type PresenceLeave struct {
	NodeID string `json:"node_id"`
}

// Relay carries an opaque payload from one Node to another. The server sets From.
type Relay struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Payload []byte `json:"payload"`
}

// Error is a server-reported failure.
type Error struct {
	Message string `json:"message"`
}

type envelope struct {
	Type MsgType         `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Marshal wraps a message value into a typed envelope.
func Marshal(msg any) ([]byte, error) {
	var typ MsgType
	switch msg.(type) {
	case Challenge, *Challenge:
		typ = TypeChallenge
	case Register, *Register:
		typ = TypeRegister
	case PresenceSnapshot, *PresenceSnapshot:
		typ = TypePresenceSnapshot
	case PresenceJoin, *PresenceJoin:
		typ = TypePresenceJoin
	case PresenceLeave, *PresenceLeave:
		typ = TypePresenceLeave
	case Relay, *Relay:
		typ = TypeRelay
	case Error, *Error:
		typ = TypeError
	default:
		return nil, fmt.Errorf("signaling: cannot marshal unknown message %T", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Type: typ, Data: data})
}

// Unmarshal decodes an envelope into its concrete message (returned as a pointer).
func Unmarshal(raw []byte) (MsgType, any, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", nil, err
	}
	var dst any
	switch env.Type {
	case TypeChallenge:
		dst = &Challenge{}
	case TypeRegister:
		dst = &Register{}
	case TypePresenceSnapshot:
		dst = &PresenceSnapshot{}
	case TypePresenceJoin:
		dst = &PresenceJoin{}
	case TypePresenceLeave:
		dst = &PresenceLeave{}
	case TypeRelay:
		dst = &Relay{}
	case TypeError:
		dst = &Error{}
	default:
		return "", nil, fmt.Errorf("signaling: unknown message type %q", env.Type)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		return "", nil, err
	}
	return env.Type, dst, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/signaling/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/signaling/message.go internal/signaling/message_test.go
git commit -m "feat(signaling): json message types and envelope codec"
```

---

## Task 3: Signaling server

**Files:**
- Create: `internal/signalserver/server.go`
- Test: `internal/signalserver/server_test.go`

The server: upgrades WS, sends a `Challenge`, verifies the `Register` signature and that `NodeID == fingerprint(pubkey)`, tracks online Nodes, pushes presence, and routes `Relay` messages. Concurrency is guarded by a single mutex over the client map; each client has a writer goroutine fed by a buffered channel.

- [ ] **Step 1: Write the failing test**

Create `internal/signalserver/server_test.go`:
```go
package signalserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/signaling"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

// dialAndRegister connects a raw WS client, completes challenge/register, and
// returns the connection plus the first presence snapshot.
func dialAndRegister(t *testing.T, wsURL string, id *identity.Identity, name string) (*websocket.Conn, *signaling.PresenceSnapshot) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	typ, env, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeChallenge, typ)
	ch := env.(*signaling.Challenge)

	reg, err := signaling.Marshal(signaling.Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: name,
		Signature:   id.Sign(ch.Nonce),
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, reg))

	_, msg, err = conn.ReadMessage()
	require.NoError(t, err)
	typ, env, err = signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypePresenceSnapshot, typ)
	return conn, env.(*signaling.PresenceSnapshot)
}

func newServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestRegisterThenPresenceJoinSeenByExistingPeer(t *testing.T) {
	wsURL := newServer(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()

	connA, snapA := dialAndRegister(t, wsURL, idA, "alice")
	require.Empty(t, snapA.Peers, "first peer sees nobody")

	_, snapB := dialAndRegister(t, wsURL, idB, "bob")
	require.Len(t, snapB.Peers, 1)
	require.Equal(t, string(idA.NodeID()), snapB.Peers[0].NodeID)

	// A should receive a PresenceJoin for B.
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := connA.ReadMessage()
	require.NoError(t, err)
	typ, env, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypePresenceJoin, typ)
	require.Equal(t, string(idB.NodeID()), env.(*signaling.PresenceJoin).Peer.NodeID)
}

func TestRelayForwardsPayloadWithFromSet(t *testing.T) {
	wsURL := newServer(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	connA, _ := dialAndRegister(t, wsURL, idA, "alice")
	connB, _ := dialAndRegister(t, wsURL, idB, "bob")

	out, err := signaling.Marshal(signaling.Relay{
		To:      string(idB.NodeID()),
		Payload: []byte("ping-payload"),
	})
	require.NoError(t, err)
	require.NoError(t, connA.WriteMessage(websocket.TextMessage, out))

	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := connB.ReadMessage()
		require.NoError(t, err)
		typ, env, err := signaling.Unmarshal(msg)
		require.NoError(t, err)
		if typ == signaling.TypeRelay {
			r := env.(*signaling.Relay)
			require.Equal(t, string(idA.NodeID()), r.From)
			require.Equal(t, []byte("ping-payload"), r.Payload)
			return
		}
	}
}

func TestRegisterRejectsBadSignature(t *testing.T) {
	wsURL := newServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_, _, err = conn.ReadMessage() // challenge
	require.NoError(t, err)
	id, _ := identity.Generate()
	reg, _ := signaling.Marshal(signaling.Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: "mallory",
		Signature:   []byte("not-a-valid-signature"),
	})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, reg))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	typ, _, err := signaling.Unmarshal(msg)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeError, typ)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/signalserver/ -v`
Expected: FAIL — `undefined: signalserver.New`.

- [ ] **Step 3: Write the implementation**

Create `internal/signalserver/server.go`:
```go
// Package signalserver implements the trust-minimized WebRTC signaling server:
// presence tracking and opaque relay routing. It never inspects relay payloads.
package signalserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/signaling"
)

type client struct {
	info signaling.PeerInfo
	send chan []byte
}

// Server tracks online clients and routes relays.
type Server struct {
	upgrader websocket.Upgrader
	mu       sync.Mutex
	clients  map[string]*client // keyed by NodeID
}

// New constructs an empty Server.
func New() *Server {
	return &Server{
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		clients:  make(map[string]*client),
	}
}

// HandleWS upgrades and serves one Node connection.
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 1. Challenge.
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	if err := writeMsg(conn, signaling.Challenge{Nonce: nonce}); err != nil {
		return
	}

	// 2. Await Register and verify.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	typ, env, err := signaling.Unmarshal(raw)
	if err != nil || typ != signaling.TypeRegister {
		_ = writeMsg(conn, signaling.Error{Message: "expected register"})
		return
	}
	reg := env.(*signaling.Register)
	if !validRegister(reg, nonce) {
		_ = writeMsg(conn, signaling.Error{Message: "invalid registration"})
		return
	}

	c := &client{
		info: signaling.PeerInfo{NodeID: reg.NodeID, PublicKey: reg.PublicKey, DisplayName: reg.DisplayName},
		send: make(chan []byte, 32),
	}

	// 3. Register: snapshot to this client, join broadcast to others.
	snapshot := s.add(c)
	_ = writeMsg(conn, signaling.PresenceSnapshot{Peers: snapshot})
	s.broadcastExcept(c.info.NodeID, signaling.PresenceJoin{Peer: c.info})
	defer func() {
		s.remove(c.info.NodeID)
		s.broadcastExcept(c.info.NodeID, signaling.PresenceLeave{NodeID: c.info.NodeID})
	}()

	// 4. Writer goroutine.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case b := <-c.send:
				if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
					return
				}
			}
		}
	}()
	defer close(done)

	// 5. Read loop: only Relay is accepted post-register.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		typ, env, err := signaling.Unmarshal(raw)
		if err != nil {
			continue
		}
		if typ != signaling.TypeRelay {
			continue
		}
		rel := env.(*signaling.Relay)
		rel.From = c.info.NodeID // server authoritatively stamps the sender
		s.routeRelay(*rel)
	}
}

func validRegister(reg *signaling.Register, nonce []byte) bool {
	pub := ed25519.PublicKey(reg.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	if identity.NodeIDFromPublicKey(pub) != identity.NodeID(reg.NodeID) {
		return false
	}
	return identity.Verify(pub, nonce, reg.Signature)
}

func (s *Server) add(c *client) []signaling.PeerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	others := make([]signaling.PeerInfo, 0, len(s.clients))
	for _, existing := range s.clients {
		others = append(others, existing.info)
	}
	s.clients[c.info.NodeID] = c
	return others
}

func (s *Server) remove(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, nodeID)
}

func (s *Server) broadcastExcept(exceptNodeID string, msg any) {
	b, err := signaling.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		if id == exceptNodeID {
			continue
		}
		select {
		case c.send <- b:
		default:
			slog.Warn("dropping message to slow client", "node", id)
		}
	}
}

func (s *Server) routeRelay(rel signaling.Relay) {
	b, err := signaling.Marshal(rel)
	if err != nil {
		return
	}
	s.mu.Lock()
	target, ok := s.clients[rel.To]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case target.send <- b:
	default:
		slog.Warn("dropping relay to slow client", "node", rel.To)
	}
}

func writeMsg(conn *websocket.Conn, msg any) error {
	b, err := signaling.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/signalserver/ -v`
Expected: PASS (all four tests). The race detector matters here: also run `go test -race ./internal/signalserver/`.

- [ ] **Step 5: Commit**

```bash
git add internal/signalserver
git commit -m "feat(signalserver): challenge/register, presence, relay routing"
```

---

## Task 4: Signaling client

**Files:**
- Create: `internal/signaling/client.go`
- Test: `internal/signaling/client_test.go`

The client connects, completes challenge/register, maintains a live presence map, exposes incoming relays via a channel, and sends relays. It implements the dependency the `peer` package needs to ship signed signals.

- [ ] **Step 1: Write the failing test**

Create `internal/signaling/client_test.go`:
```go
package signaling_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/signaling"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func clientServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestClientConnectsRegistersAndRelays(t *testing.T) {
	wsURL := clientServerURL(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ca, err := signaling.Dial(ctx, wsURL, idA, "alice")
	require.NoError(t, err)
	defer ca.Close()

	cb, err := signaling.Dial(ctx, wsURL, idB, "bob")
	require.NoError(t, err)
	defer cb.Close()

	// B should appear in A's presence within a moment.
	require.Eventually(t, func() bool {
		for _, p := range ca.Peers() {
			if p.NodeID == string(idB.NodeID()) {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond)

	// A relays a payload to B.
	require.NoError(t, ca.SendRelay(idB.NodeID(), []byte("hi-bob")))
	select {
	case rel := <-cb.Relays():
		require.Equal(t, string(idA.NodeID()), rel.From)
		require.Equal(t, []byte("hi-bob"), rel.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("B never received the relay")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/signaling/ -run TestClientConnects -v`
Expected: FAIL — `undefined: signaling.Dial`.

- [ ] **Step 3: Write the implementation**

Create `internal/signaling/client.go`:
```go
package signaling

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/squallchua/p2p-hls/internal/identity"
)

// Client is a Node's connection to the signaling server.
type Client struct {
	conn   *websocket.Conn
	self   identity.NodeID
	relays chan Relay

	mu      sync.RWMutex
	peers   map[string]PeerInfo
	closed  bool
	writeMu sync.Mutex
}

// Dial connects, completes challenge/register, and starts the read loop.
func Dial(ctx context.Context, url string, id *identity.Identity, displayName string) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial signaling: %w", err)
	}

	_, raw, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, err
	}
	typ, env, err := Unmarshal(raw)
	if err != nil || typ != TypeChallenge {
		conn.Close()
		return nil, fmt.Errorf("expected challenge, got %q", typ)
	}
	ch := env.(*Challenge)

	reg, _ := Marshal(Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: displayName,
		Signature:   id.Sign(ch.Nonce),
	})
	if err := conn.WriteMessage(websocket.TextMessage, reg); err != nil {
		conn.Close()
		return nil, err
	}

	_, raw, err = conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, err
	}
	typ, env, err = Unmarshal(raw)
	if err != nil || typ != TypePresenceSnapshot {
		conn.Close()
		return nil, fmt.Errorf("expected snapshot, got %q", typ)
	}

	c := &Client{
		conn:   conn,
		self:   id.NodeID(),
		relays: make(chan Relay, 32),
		peers:  make(map[string]PeerInfo),
	}
	for _, p := range env.(*PresenceSnapshot).Peers {
		c.peers[p.NodeID] = p
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()
			close(c.relays)
			return
		}
		typ, env, err := Unmarshal(raw)
		if err != nil {
			continue
		}
		switch typ {
		case TypePresenceJoin:
			p := env.(*PresenceJoin).Peer
			c.mu.Lock()
			c.peers[p.NodeID] = p
			c.mu.Unlock()
		case TypePresenceLeave:
			c.mu.Lock()
			delete(c.peers, env.(*PresenceLeave).NodeID)
			c.mu.Unlock()
		case TypeRelay:
			c.relays <- *env.(*Relay)
		}
	}
}

// Peers returns a snapshot of currently-online Nodes.
func (c *Client) Peers() []PeerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]PeerInfo, 0, len(c.peers))
	for _, p := range c.peers {
		out = append(out, p)
	}
	return out
}

// Relays delivers incoming relay messages. Closed when the connection ends.
func (c *Client) Relays() <-chan Relay { return c.relays }

// SendRelay sends an opaque payload to another Node.
func (c *Client) SendRelay(to identity.NodeID, payload []byte) error {
	b, err := Marshal(Relay{To: string(to), Payload: payload})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

// Close shuts the connection.
func (c *Client) Close() error { return c.conn.Close() }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/signaling/ -v`
Expected: PASS (message tests + client test), no data races.

- [ ] **Step 5: Commit**

```bash
git add internal/signaling/client.go internal/signaling/client_test.go
git commit -m "feat(signaling): websocket client with presence and relay"
```

---

## Task 5: Control-channel Protobuf + framing

**Files:**
- Create: `proto/peer/v1/peer.proto`, generated `proto/peer/v1/peer.pb.go`
- Create: `internal/peer/framing.go`
- Test: `internal/peer/framing_test.go`

- [ ] **Step 1: Write the proto schema**

Create `proto/peer/v1/peer.proto`:
```proto
syntax = "proto3";

package peer.v1;

option go_package = "github.com/squallchua/p2p-hls/proto/peer/v1;peerv1";

// Envelope is one control-channel message. Each WebRTC data-channel send carries
// exactly one Envelope (SCTP preserves message boundaries; no length prefix needed).
message Envelope {
  uint64 request_id = 1;
  oneof body {
    Handshake handshake = 2;
    Ping ping = 3;
    Pong pong = 4;
  }
}

// Handshake is exchanged once when the control channel opens.
message Handshake {
  uint32 protocol_version = 1;
  repeated string capabilities = 2;
}

message Ping { string nonce = 1; }
message Pong { string nonce = 1; }
```

- [ ] **Step 2: Generate the Go code**

Run: `make proto`
Expected: `proto/peer/v1/peer.pb.go` created. Then `go build ./...` succeeds.

- [ ] **Step 3: Write the failing framing test**

Create `internal/peer/framing_test.go`:
```go
package peer

import (
	"testing"

	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeEnvelope(t *testing.T) {
	env := &peerv1.Envelope{
		RequestId: 42,
		Body:      &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: "xyz"}},
	}
	raw, err := encodeEnvelope(env)
	require.NoError(t, err)

	got, err := decodeEnvelope(raw)
	require.NoError(t, err)
	require.Equal(t, uint64(42), got.RequestId)
	require.Equal(t, "xyz", got.GetPing().GetNonce())
}

func TestRequestIDsAreMonotonic(t *testing.T) {
	var a requestIDs
	require.Equal(t, uint64(1), a.next())
	require.Equal(t, uint64(2), a.next())
	require.Equal(t, uint64(3), a.next())
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/peer/ -run TestEncodeDecodeEnvelope -v`
Expected: FAIL — `undefined: encodeEnvelope`.

- [ ] **Step 5: Write the implementation**

Create `internal/peer/framing.go`:
```go
package peer

import (
	"sync/atomic"

	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
	"google.golang.org/protobuf/proto"
)

// encodeEnvelope marshals one control-channel message.
func encodeEnvelope(env *peerv1.Envelope) ([]byte, error) {
	return proto.Marshal(env)
}

// decodeEnvelope parses one control-channel message.
func decodeEnvelope(raw []byte) (*peerv1.Envelope, error) {
	var env peerv1.Envelope
	if err := proto.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// requestIDs allocates monotonic, per-connection request IDs starting at 1.
type requestIDs struct{ n atomic.Uint64 }

func (r *requestIDs) next() uint64 { return r.n.Add(1) }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/peer/ -v`
Expected: PASS (both framing tests).

- [ ] **Step 7: Commit**

```bash
git add proto internal/peer/framing.go internal/peer/framing_test.go Makefile
git commit -m "feat(peer): control-channel protobuf schema and framing"
```

---

## Task 6: Identity binding (signed signals)

**Files:**
- Create: `internal/peer/binding.go`
- Test: `internal/peer/binding_test.go`

A `SignedSignal` wraps a WebRTC SDP (offer or answer) with the sender's public key and an Ed25519 signature over the SDP bytes. Because the SDP contains the DTLS fingerprint, signing the SDP binds the Node's identity to the connection (ADR 0003). Verification rejects any signal whose `From` ≠ `fingerprint(pubkey)` or whose signature is invalid.

- [ ] **Step 1: Write the failing test**

Create `internal/peer/binding_test.go`:
```go
package peer

import (
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestSignVerifySignalRoundTrip(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "v=0\r\na=fingerprint:sha-256 AA:BB\r\n"}

	s, err := SignSignal(id, desc)
	require.NoError(t, err)
	require.Equal(t, string(id.NodeID()), s.From)

	gotDesc, err := VerifySignal(s)
	require.NoError(t, err)
	require.Equal(t, desc.SDP, gotDesc.SDP)
	require.Equal(t, desc.Type, gotDesc.Type)
}

func TestVerifyRejectsTamperedSDP(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "original"}
	s, _ := SignSignal(id, desc)
	s.SDP = []byte(`{"type":"offer","sdp":"tampered"}`)
	_, err := VerifySignal(s)
	require.Error(t, err)
}

func TestVerifyRejectsMismatchedNodeID(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "x"}
	s, _ := SignSignal(id, desc)
	s.From = "not-the-real-fingerprint"
	_, err := VerifySignal(s)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/peer/ -run Signal -v`
Expected: FAIL — `undefined: SignSignal`.

- [ ] **Step 3: Write the implementation**

Create `internal/peer/binding.go`:
```go
package peer

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
)

// SignedSignal is a WebRTC SDP carried over the (untrusted) signaling server,
// signed by the sender's Ed25519 key. Signing the SDP binds the Node identity to
// the DTLS fingerprint inside it (ADR 0003).
type SignedSignal struct {
	From      string `json:"from"`       // sender NodeID
	PublicKey []byte `json:"public_key"` // sender Ed25519 public key
	SDP       []byte `json:"sdp"`        // json-encoded webrtc.SessionDescription
	Signature []byte `json:"signature"`  // Sign(SDP)
}

// SignSignal serializes and signs a session description.
func SignSignal(id *identity.Identity, desc webrtc.SessionDescription) (SignedSignal, error) {
	sdp, err := json.Marshal(desc)
	if err != nil {
		return SignedSignal{}, err
	}
	return SignedSignal{
		From:      string(id.NodeID()),
		PublicKey: id.PublicKey(),
		SDP:       sdp,
		Signature: id.Sign(sdp),
	}, nil
}

// VerifySignal checks the identity binding and returns the session description.
func VerifySignal(s SignedSignal) (webrtc.SessionDescription, error) {
	pub := ed25519.PublicKey(s.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: bad public key length")
	}
	if identity.NodeIDFromPublicKey(pub) != identity.NodeID(s.From) {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: From does not match public key fingerprint")
	}
	if !identity.Verify(pub, s.SDP, s.Signature) {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: signature verification failed")
	}
	var desc webrtc.SessionDescription
	if err := json.Unmarshal(s.SDP, &desc); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return desc, nil
}

// Encode/Decode move a SignedSignal through a relay payload (JSON).
func (s SignedSignal) Encode() ([]byte, error) { return json.Marshal(s) }

func DecodeSignedSignal(raw []byte) (SignedSignal, error) {
	var s SignedSignal
	err := json.Unmarshal(raw, &s)
	return s, err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/peer/ -run Signal -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/peer/binding.go internal/peer/binding_test.go
git commit -m "feat(peer): signed SDP identity binding (ADR 0003)"
```

---

## Task 7: Peer session (Pion connection, channels, handshake, Ping)

**Files:**
- Create: `internal/peer/session.go`
- Test: `internal/peer/session_test.go`

A `Session` wraps one Pion `PeerConnection`. The initiator creates the `control` + `bulk` channels and an offer; the answerer responds. **Non-trickle ICE**: each side waits for ICE gathering to complete, then signs and sends the full SDP. When `control` opens, both sides send a `Handshake`; once received, `Ready()` fires. `Ping` sends an `Envelope{Ping}` and awaits the correlated `Pong`.

- [ ] **Step 1: Write the failing test**

Create `internal/peer/session_test.go`:
```go
package peer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

// directSignaler delivers signals straight to a peer's HandleSignal (no server),
// so we can test the session in isolation.
type directSignaler struct {
	mu   sync.Mutex
	dest map[identity.NodeID]*Session
}

func (d *directSignaler) register(id identity.NodeID, s *Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dest[id] = s
}

func (d *directSignaler) SendSignal(to identity.NodeID, s SignedSignal) error {
	d.mu.Lock()
	dst := d.dest[to]
	d.mu.Unlock()
	return dst.HandleSignal(s)
}

func TestTwoSessionsHandshakeAndPing(t *testing.T) {
	cfg := webrtc.Configuration{} // loopback: host candidates only, no STUN needed
	sig := &directSignaler{dest: map[identity.NodeID]*Session{}}

	idA, _ := identity.Generate()
	idB, _ := identity.Generate()

	sessA, err := NewSession(idA, idB.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessA.Close()
	sessB, err := NewSession(idB, idA.NodeID(), cfg, sig)
	require.NoError(t, err)
	defer sessB.Close()

	sig.register(idA.NodeID(), sessA)
	sig.register(idB.NodeID(), sessB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, sessB.Start(ctx, false)) // answerer first (waits for offer)
	require.NoError(t, sessA.Start(ctx, true))  // initiator

	select {
	case <-sessA.Ready():
	case <-ctx.Done():
		t.Fatal("session A never became ready")
	}

	pong, err := sessA.Ping(ctx, "nonce-123")
	require.NoError(t, err)
	require.Equal(t, "nonce-123", pong)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/peer/ -run TestTwoSessions -v`
Expected: FAIL — `undefined: NewSession`.

- [ ] **Step 3: Write the implementation**

Create `internal/peer/session.go`:
```go
package peer

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
	peerv1 "github.com/squallchua/p2p-hls/proto/peer/v1"
)

// ProtocolVersion is the wire-protocol version exchanged at handshake.
const ProtocolVersion = 1

// Signaler ships a signed signal to a remote Node (over the signaling server).
type Signaler interface {
	SendSignal(to identity.NodeID, s SignedSignal) error
}

// Session is one identity-verified WebRTC connection to a remote Node.
type Session struct {
	self     *identity.Identity
	remote   identity.NodeID
	signaler Signaler

	pc      *webrtc.PeerConnection
	control *webrtc.DataChannel

	ids     requestIDs
	mu      sync.Mutex
	pending map[uint64]chan *peerv1.Envelope

	readyOnce sync.Once
	ready     chan struct{}
}

// NewSession builds a Session and its underlying PeerConnection.
func NewSession(self *identity.Identity, remote identity.NodeID, cfg webrtc.Configuration, sig Signaler) (*Session, error) {
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}
	s := &Session{
		self:     self,
		remote:   remote,
		signaler: sig,
		pc:       pc,
		pending:  make(map[uint64]chan *peerv1.Envelope),
		ready:    make(chan struct{}),
	}
	// Answerer receives channels created by the initiator.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "control" {
			s.bindControl(dc)
		}
	})
	return s, nil
}

// Start drives the offer/answer exchange. initiator=true creates channels + offer.
func (s *Session) Start(ctx context.Context, initiator bool) error {
	if !initiator {
		return nil // answerer acts on the incoming offer in HandleSignal
	}
	control, err := s.pc.CreateDataChannel("control", nil)
	if err != nil {
		return err
	}
	s.bindControl(control)
	if _, err := s.pc.CreateDataChannel("bulk", nil); err != nil { // opened now, used in Slice 3
		return err
	}
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	return s.setLocalAndSend(ctx, offer)
}

// HandleSignal processes an inbound offer or answer.
func (s *Session) HandleSignal(sig SignedSignal) error {
	if sig.From != string(s.remote) {
		return fmt.Errorf("peer: signal from unexpected node %s", sig.From)
	}
	desc, err := VerifySignal(sig)
	if err != nil {
		return err
	}
	if err := s.pc.SetRemoteDescription(desc); err != nil {
		return err
	}
	if desc.Type == webrtc.SDPTypeOffer {
		answer, err := s.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		return s.setLocalAndSend(context.Background(), answer)
	}
	return nil
}

// setLocalAndSend sets the local description, waits for non-trickle ICE gathering,
// then signs and relays the complete SDP.
func (s *Session) setLocalAndSend(ctx context.Context, desc webrtc.SessionDescription) error {
	gatherComplete := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(desc); err != nil {
		return err
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return ctx.Err()
	}
	signed, err := SignSignal(s.self, *s.pc.LocalDescription())
	if err != nil {
		return err
	}
	return s.signaler.SendSignal(s.remote, signed)
}

// bindControl wires the control channel: send a Handshake on open, dispatch
// inbound Envelopes (Handshake -> ready, Ping -> Pong, Pong -> pending).
func (s *Session) bindControl(dc *webrtc.DataChannel) {
	s.control = dc
	dc.OnOpen(func() {
		_ = s.send(&peerv1.Envelope{
			Body: &peerv1.Envelope_Handshake{Handshake: &peerv1.Handshake{
				ProtocolVersion: ProtocolVersion,
			}},
		})
	})
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
		case *peerv1.Envelope_Pong:
			s.mu.Lock()
			ch, ok := s.pending[env.RequestId]
			delete(s.pending, env.RequestId)
			s.mu.Unlock()
			if ok {
				ch <- env
			}
		}
	})
}

func (s *Session) send(env *peerv1.Envelope) error {
	raw, err := encodeEnvelope(env)
	if err != nil {
		return err
	}
	if s.control == nil {
		return fmt.Errorf("peer: control channel not open")
	}
	return s.control.Send(raw)
}

// Ready is closed once the version handshake succeeds.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Ping sends a ping and returns the echoed nonce.
func (s *Session) Ping(ctx context.Context, nonce string) (string, error) {
	id := s.ids.next()
	ch := make(chan *peerv1.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.send(&peerv1.Envelope{
		RequestId: id,
		Body:      &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: nonce}},
	}); err != nil {
		return "", err
	}
	select {
	case env := <-ch:
		return env.GetPong().GetNonce(), nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return "", ctx.Err()
	}
}

// Close tears down the connection.
func (s *Session) Close() error { return s.pc.Close() }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/peer/ -run TestTwoSessions -v -timeout 30s`
Expected: PASS — two in-process PeerConnections complete the offer/answer, handshake, and a Ping/Pong. Also run `go test -race ./internal/peer/`.

- [ ] **Step 5: Commit**

```bash
git add internal/peer/session.go internal/peer/session_test.go
git commit -m "feat(peer): pion session with signed handshake and ping rpc"
```

---

## Task 8: App config

**Files:**
- Create: `internal/app/config.go`
- Test: `internal/app/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/app/config_test.go`:
```go
package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`display_name = "alice"`+"\n"), 0o600))

	cfg, err := app.LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "alice", cfg.DisplayName)
	require.Equal(t, "ws://localhost:8080/ws", cfg.SignalingURL) // default
	require.NotEmpty(t, cfg.STUNServers)                          // default
}

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := app.LoadConfig(filepath.Join(t.TempDir(), "nope.toml"))
	require.NoError(t, err)
	require.Equal(t, "ws://localhost:8080/ws", cfg.SignalingURL)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app/ -v`
Expected: FAIL — `undefined: app.LoadConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/app/config.go`:
```go
// Package app wires Nodes together and loads configuration.
package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config is the Node's settings, loaded from config.toml.
type Config struct {
	DisplayName  string   `toml:"display_name"`
	SignalingURL string   `toml:"signaling_url"`
	STUNServers  []string `toml:"stun_servers"`
}

func defaults() Config {
	return Config{
		DisplayName:  "anonymous",
		SignalingURL: "ws://localhost:8080/ws",
		STUNServers:  []string{"stun:stun.l.google.com:19302"},
	}
}

// LoadConfig reads path over the defaults. A missing file yields pure defaults.
func LoadConfig(path string) (Config, error) {
	cfg := defaults()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ConfigDir returns the per-user config directory for the app.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "p2p-hls"), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/config.go internal/app/config_test.go
git commit -m "feat(app): config.toml loading with defaults"
```

---

## Task 9: Node orchestrator + entrypoints

**Files:**
- Create: `internal/app/node.go`
- Create: `cmd/signal-server/main.go`
- Create: `cmd/node/main.go`
- Test: `internal/app/node_test.go`

`app.Node` ties the signaling client to peer sessions: it converts a Node's `WebRTC` config, ships `SignedSignal`s through `SendRelay`, and routes inbound relays to the right session — auto-creating an answerer session for an unknown remote.

- [ ] **Step 1: Write the failing test**

Create `internal/app/node_test.go`:
```go
package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestNodeDialAndPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	nodeA, err := app.NewNode(ctx, idA, "alice", cfg)
	require.NoError(t, err)
	defer nodeA.Close()
	nodeB, err := app.NewNode(ctx, idB, "bob", cfg)
	require.NoError(t, err)
	defer nodeB.Close()

	// A waits to see B online, then dials and pings.
	require.Eventually(t, func() bool { return nodeA.Sees(idB.NodeID()) }, 3*time.Second, 25*time.Millisecond)

	sess, err := nodeA.Dial(ctx, idB.NodeID())
	require.NoError(t, err)
	pong, err := sess.Ping(ctx, "hello")
	require.NoError(t, err)
	require.Equal(t, "hello", pong)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app/ -run TestNodeDial -v`
Expected: FAIL — `undefined: app.NewNode`.

- [ ] **Step 3: Write the orchestrator**

Create `internal/app/node.go`:
```go
package app

import (
	"context"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/peer"
	"github.com/squallchua/p2p-hls/internal/signaling"
)

// Node is a running app instance: a signaling client plus its peer sessions.
type Node struct {
	self   *identity.Identity
	client *signaling.Client
	rtcCfg webrtc.Configuration

	mu       sync.Mutex
	sessions map[identity.NodeID]*peer.Session
}

// relaySignaler adapts the signaling client to peer.Signaler.
type relaySignaler struct {
	client *signaling.Client
}

func (r relaySignaler) SendSignal(to identity.NodeID, s peer.SignedSignal) error {
	payload, err := s.Encode()
	if err != nil {
		return err
	}
	return r.client.SendRelay(to, payload)
}

// NewNode connects to signaling and starts routing inbound relays.
func NewNode(ctx context.Context, self *identity.Identity, displayName string, cfg Config) (*Node, error) {
	client, err := signaling.Dial(ctx, cfg.SignalingURL, self, displayName)
	if err != nil {
		return nil, err
	}
	rtcCfg := webrtc.Configuration{}
	for _, s := range cfg.STUNServers {
		rtcCfg.ICEServers = append(rtcCfg.ICEServers, webrtc.ICEServer{URLs: []string{s}})
	}
	n := &Node{
		self:     self,
		client:   client,
		rtcCfg:   rtcCfg,
		sessions: make(map[identity.NodeID]*peer.Session),
	}
	go n.routeRelays()
	return n, nil
}

func (n *Node) routeRelays() {
	for rel := range n.client.Relays() {
		from := identity.NodeID(rel.From)
		sig, err := peer.DecodeSignedSignal(rel.Payload)
		if err != nil {
			continue
		}
		sess := n.sessionFor(from, false)
		_ = sess.HandleSignal(sig)
	}
}

// sessionFor returns the existing session for remote, or creates one. When
// created as an answerer (initiator=false) it is started immediately.
func (n *Node) sessionFor(remote identity.NodeID, initiator bool) *peer.Session {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.sessions[remote]; ok {
		return s
	}
	s, err := peer.NewSession(n.self, remote, n.rtcCfg, relaySignaler{client: n.client})
	if err != nil {
		return nil
	}
	n.sessions[remote] = s
	if !initiator {
		_ = s.Start(context.Background(), false)
	}
	return s
}

// Sees reports whether remote is currently in presence.
func (n *Node) Sees(remote identity.NodeID) bool {
	for _, p := range n.client.Peers() {
		if p.NodeID == string(remote) {
			return true
		}
	}
	return false
}

// Dial opens (or returns) a session to remote and blocks until it is ready.
func (n *Node) Dial(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	sess := n.sessionFor(remote, true)
	if err := sess.Start(ctx, true); err != nil {
		return nil, err
	}
	select {
	case <-sess.Ready():
		return sess, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts the node down.
func (n *Node) Close() error {
	n.mu.Lock()
	for _, s := range n.sessions {
		_ = s.Close()
	}
	n.mu.Unlock()
	return n.client.Close()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/app/ -run TestNodeDial -v -timeout 30s`
Expected: PASS — two Nodes connect through the real signaling server and complete a Ping. Also `go test -race ./internal/app/`.

- [ ] **Step 5: Write the signaling-server entrypoint**

Create `cmd/signal-server/main.go`:
```go
// Command signal-server runs the trust-minimized WebRTC signaling server.
package main

import (
	"flag"
	"log/slog"
	"net/http"

	"github.com/squallchua/p2p-hls/internal/signalserver"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	srv := signalserver.New()
	http.HandleFunc("/ws", srv.HandleWS)
	slog.Info("signaling server listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		slog.Error("server exited", "err", err)
	}
}
```

- [ ] **Step 6: Write the node entrypoint**

Create `cmd/node/main.go`:
```go
// Command node runs a P2P HLS Node. For Slice 1 it connects to signaling, lists
// presence, and can dial+ping a peer by Node ID.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/squallchua/p2p-hls/internal/identity"
)

func main() {
	name := flag.String("name", "anonymous", "display name")
	dial := flag.String("dial", "", "node id to dial and ping (optional)")
	flag.Parse()

	configDir, err := app.ConfigDir()
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		fatal(err)
	}
	id, err := identity.LoadOrCreate(filepath.Join(configDir, "identity.key"))
	if err != nil {
		fatal(err)
	}
	fmt.Println("Node ID:", id.NodeID())

	cfg, err := app.LoadConfig(filepath.Join(configDir, "config.toml"))
	if err != nil {
		fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	node, err := app.NewNode(ctx, id, *name, cfg)
	if err != nil {
		fatal(err)
	}
	defer node.Close()

	time.Sleep(500 * time.Millisecond) // let presence settle

	if *dial == "" {
		fmt.Println("Connected. Online peers will appear as they join. (No --dial target given.)")
		time.Sleep(10 * time.Second)
		return
	}

	sess, err := node.Dial(ctx, identity.NodeID(*dial))
	if err != nil {
		fatal(err)
	}
	pong, err := sess.Ping(ctx, "hello")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("Ping OK, echoed nonce: %q\n", pong)
}

func fatal(err error) {
	slog.Error("node failed", "err", err)
	os.Exit(1)
}
```

- [ ] **Step 7: Verify the binaries build**

Run: `make build`
Expected: `bin/node` and `bin/signal-server` produced, no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/app/node.go internal/app/node_test.go cmd
git commit -m "feat(app): node orchestrator and node/signal-server binaries"
```

---

## Task 10: End-to-end foundation test

**Files:**
- Create: `test/e2e_test.go`

This is the proof of the whole slice: a real signaling server, two real Nodes, a verified WebRTC connection over loopback, and a Ping round-trip — exercising identity, signaling, binding, the Pion session, and the orchestrator together.

- [ ] **Step 1: Write the end-to-end test**

Create `test/e2e_test.go`:
```go
package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squallchua/p2p-hls/internal/app"
	"github.com/squallchua/p2p-hls/internal/identity"
	"github.com/squallchua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestFoundationEndToEnd(t *testing.T) {
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

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	// Viewer waits until it sees the Host in presence.
	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) },
		5*time.Second, 25*time.Millisecond, "viewer should see host via presence")

	// Viewer dials Host directly over WebRTC and pings.
	sess, err := viewer.Dial(ctx, idHost.NodeID())
	require.NoError(t, err)
	defer sess.Close()

	echoed, err := sess.Ping(ctx, "foundation-ok")
	require.NoError(t, err)
	require.Equal(t, "foundation-ok", echoed)
}
```

- [ ] **Step 2: Run the end-to-end test**

Run: `go test ./test/ -run TestFoundationEndToEnd -v -timeout 60s`
Expected: PASS — full path works end to end.

- [ ] **Step 3: Run the whole suite with the race detector**

Run: `go test -race ./... -timeout 120s`
Expected: All packages PASS, no data races.

- [ ] **Step 4: Commit**

```bash
git add test/e2e_test.go
git commit -m "test(e2e): two-node foundation ping over real signaling server"
```

---

## Definition of Done (Slice 1)

- [ ] `go test -race ./...` passes.
- [ ] `make build` produces `bin/node` and `bin/signal-server`.
- [ ] Running `bin/signal-server` and two `bin/node` processes (one with `--dial <other-node-id>`) prints `Ping OK` — manual smoke test.
- [ ] Identity persists across restarts (`identity.key`, `0600`).
- [ ] Every SDP is signed and verified; a tampered signal is rejected (covered by `binding_test.go`).

---

## Self-Review notes

- **Spec coverage:** This slice implements the spec's Foundation (identity → signaling → first data channel) plus ADR 0003's identity binding and ADR 0001's control-channel framing/handshake. The `bulk` channel is opened but its chunking (ADR 0001) is deferred to Slice 3, where segments first exist (YAGNI). Library/catalog/media/bridge are explicitly out of scope (Slices 2–3).
- **Simplifications made explicit:** non-trickle ICE (trickle deferred), no TURN (STUN only, per spec), loopback-friendly empty config in tests. These match the spec's documented MVP limitations.
- **Type consistency:** `identity.NodeID`, `peer.SignedSignal`, `peer.Signaler`, `peer.Session` signatures, and `signaling.PeerInfo` are used identically across Tasks 1–10.
