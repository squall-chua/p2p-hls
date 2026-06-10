# P2P HLS Streaming

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![WebRTC](https://img.shields.io/badge/transport-WebRTC-333?logo=webrtc)
![HLS](https://img.shields.io/badge/streaming-HLS-orange)
![status](https://img.shields.io/badge/status-prototype-yellow)

A peer-to-peer media application. Each **User** runs a **Node** that shares their local
media **Library**. Other Users discover online **Peers** through a trust-minimized
**signaling server**, browse the Libraries they're allowed to see, and stream (HLS) or
download media **directly peer-to-peer over WebRTC** — the signaling server never sees
catalogs or media bytes. Nodes can also host **Watch Parties**: hard-synced group playback
where the participating Viewers form a **Swarm** that re-serves cached segments to one
another to share distribution load.

Everything is controlled from a local browser UI that the Node serves on loopback.

> **New here?** Start with [CONTEXT.md](CONTEXT.md) — it defines the project's vocabulary
> (Node, Peer, Host, Viewer, Title, Segment, Catalog, Swarm, …). Those capitalized terms are
> used precisely throughout this README and the code.

---

## Table of contents

- [How it works](#how-it-works)
- [Repository layout](#repository-layout)
- [Prerequisites](#prerequisites)
- [Getting started](#getting-started)
  - [1. Build](#1-build)
  - [2. Start the signaling server](#2-start-the-signaling-server)
  - [3. Configure and run a Node](#3-configure-and-run-a-node)
  - [4. Run a second Node and connect them](#4-run-a-second-node-and-connect-them)
- [Configuration](#configuration)
- [Using the app](#using-the-app)
- [The control-plane API](#the-control-plane-api)
- [Development](#development)
- [Design decisions](#design-decisions)
- [Troubleshooting](#troubleshooting)
- [License](#license)

---

## How it works

There are exactly **two binaries**:

| Binary | Package | Role |
| --- | --- | --- |
| `signal-server` | [`cmd/signal-server`](cmd/signal-server) | Shared, stateless **signaling server**. Brokers WebRTC handshakes and tracks **Presence** (who's online). Sees no catalogs and no media. One per network/group. |
| `node` | [`cmd/node`](cmd/node) | The app a **User** runs. Holds the identity keypair + Library, talks P2P to Peers, and serves a local browser UI. One per User. |

A Node does three things at once:

1. **Connects to the signaling server** to learn Presence and to negotiate direct
   WebRTC connections to Peers (a STUN server is used for NAT traversal).
2. **Serves its own Library** — scans configured shared folders into a Catalog, enforces an
   access policy (public / restricted + allow / block lists), and streams Titles to permitted
   Viewers as HLS over the WebRTC data channel.
3. **Serves a loopback control plane** — an embedded Nuxt single-page app plus a REST + SSE
   bridge (bound to `127.0.0.1` by default) that the User drives from their browser.

The feature set was built in vertical slices, each backed by an ADR (see
[Design decisions](#design-decisions)):

- **Foundation** — identity, signaling, peer sessions, the peer wire protocol.
- **Library & browse** — Catalog, access policy, request-access flow.
- **HLS streaming** — `ffmpeg`-backed segmenting, on-the-fly subtitle conversion.
- **Watch Party sync** — host-authoritative play/pause/seek, Viewers follow.
- **Mesh Swarm** — Viewers re-serve verified, cached Segments to each other (BLAKE3 integrity).
- **Browser UI control plane** — the Nuxt SPA + REST/SSE bridge.

---

## Repository layout

```
cmd/
  node/             # the Node binary (User-facing app)
  signal-server/    # the signaling server binary
internal/
  app/              # Node wiring, config, party coordinator, swarm session (the I/O shell)
  signaling/        # signaling client
  signalserver/     # signaling server implementation
  peer/             # WebRTC peer sessions + peer wire protocol (control + bulk channels)
  identity/         # Ed25519 keypair, Node ID, signed-SDP identity binding
  library/          # shared-folder scanner, ffprobe metadata, SQLite store
  catalog/          # access-filtered listing + access policy + access requests
  media/            # HLS engine (ffmpeg segmenting, subtitle conversion), media service
  party/            # pure Watch Party sync engine (host + viewer, deterministic)
  swarm/            # pure mesh decision engine (peer-have, RTT-aware source selection)
  bridge/           # loopback HTTP server: REST + SSE + HLS proxy + embedded SPA
  buildcheck/       # build-time assertions
proto/peer/v1/      # protobuf wire types for the peer protocol
webui/              # Nuxt 4 single-page app (the browser UI) + Vitest + Playwright
docs/
  adr/              # Architecture Decision Records (0001–0006)
  superpowers/      # design specs + per-slice implementation plans
test/               # cross-package end-to-end tests (boot real nodes + signaling)
CONTEXT.md          # domain glossary — read this first
```

---

## Prerequisites

| Tool | Version | Needed for | Notes |
| --- | --- | --- | --- |
| **Go** | 1.25.1+ | building/running both binaries, tests | Pure-Go SQLite (`modernc.org/sqlite`) — **no CGO/C compiler required**. |
| **ffmpeg** + **ffprobe** | recent | indexing & streaming media | Must be on `PATH`. `ffprobe` reads metadata; `ffmpeg` segments to HLS and converts subtitles. |
| **Node.js** + **npm** | Node 20+ | building the browser UI | Only needed to build/develop the SPA. `npm ci` needs network access. |
| `protoc` + `protoc-gen-go` | any | regenerating protobuf | Only if you edit `proto/peer/v1/peer.proto`. |

The Go binaries embed the SPA at compile time, so **a finished `node` binary needs only Go +
ffmpeg/ffprobe at runtime** — Node.js is a build-time dependency, not a runtime one.

---

## Getting started

This walkthrough runs everything locally: one signaling server and two Nodes (a **Host** that
shares a video and a **Viewer** that watches it) on the same machine.

### 1. Build

The browser UI is embedded into the `node` binary, so the *complete* build is two steps:
build the SPA, then build the Go binaries. The `Makefile` does both:

```bash
make build      # runs `make webui` (npm ci + nuxt generate) then builds bin/node and bin/signal-server
```

This produces `bin/node` and `bin/signal-server`.

> **Go-only build (faster, no Node.js, placeholder UI).** The repo commits a placeholder
> `internal/bridge/dist/index.html`, so the Go code compiles without ever running the SPA build:
> ```bash
> go build -o bin/node ./cmd/node
> go build -o bin/signal-server ./cmd/signal-server
> ```
> The binary works for P2P/streaming, CLI, and tests, but the browser UI is a placeholder until
> you run `make webui`. Use `make build` whenever you want the real UI.

### 2. Start the signaling server

In its own terminal:

```bash
./bin/signal-server -addr :8080
# or, without building:  go run ./cmd/signal-server -addr :8080
```

It listens on `ws://<addr>/ws` (here `ws://localhost:8080/ws`). Leave it running — both Nodes
connect to it. Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `:8080` | TCP listen address. |

### 3. Configure and run a Node

Give each Node its own **config directory** (holds its identity key, config, library DB, and
segment cache). Create one for the Host and drop a sample movie into a shared folder:

```bash
mkdir -p ./host/media
cp /path/to/some-movie.mp4 ./host/media/        # .mp4 .mkv .mov .m4v .webm are indexed
```

Create `./host/config.toml`:

```toml
display_name       = "host"
signaling_url      = "ws://localhost:8080/ws"
shared_folders     = ["./host/media"]
default_visibility = "public"        # "public" = anyone may browse; "restricted" = request access
stun_servers       = ["stun:stun.l.google.com:19302"]
# allow_list = ["<node-id>"]         # always-allowed peers (restricted mode)
# block_list = ["<node-id>"]         # always-denied peers
# data_dir   = "./host"              # where library.db + cache/ live (default: the config dir)
```

Run the Host Node:

```bash
./bin/node --name host --config-dir ./host
```

On start it prints the **Node ID** — a lowercase, unpadded base32 string derived from the
Node's public key (share this with Viewers) — indexes the shared folders, and opens the
browser UI:

```
Node ID: <base32-node-id>
Library: 1 title(s) indexed from [./host/media]
UI ready: http://127.0.0.1:53124
```

`node` flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--name` | `anonymous` | Display name shown to Peers. |
| `--config-dir` | OS config dir (`~/.config/p2p-hls`, etc.) | Base dir for `identity.key`, `config.toml`, `library.db`, `cache/`. |
| `--bridge-addr` | `127.0.0.1:0` | Loopback bind address for the UI (`:0` = random free port). |
| `--no-open` | `false` | Don't auto-open a browser (useful for headless/servers). |

### 4. Run a second Node and connect them

In another terminal, create `./viewer/config.toml` (a Viewer needs no shared folders):

```toml
display_name  = "viewer"
signaling_url = "ws://localhost:8080/ws"
```

```bash
./bin/node --name viewer --config-dir ./viewer
```

Both Nodes register with the same signaling server, so each sees the other in **Presence**.
From the Viewer's browser UI you can now browse the Host, stream the movie, and start/join a
Watch Party — see below.

> **Heads-up:** start the signaling server **before** the Nodes — a Node fails to start if it
> can't reach signaling within 30 seconds.

---

## Configuration

A Node reads `config.toml` from its `--config-dir`. A missing file is fine — pure defaults are
used. All keys:

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `display_name` | string | `"anonymous"` | Name shown to Peers (the `--name` flag overrides it). |
| `signaling_url` | string | `ws://localhost:8080/ws` | WebSocket URL of the signaling server. |
| `stun_servers` | []string | `["stun:stun.l.google.com:19302"]` | STUN servers for WebRTC NAT traversal. |
| `shared_folders` | []string | `[]` | Directories scanned into the Library. Indexed extensions: `.mp4 .mkv .mov .m4v .webm`. |
| `default_visibility` | string | `"restricted"` | `"public"` (anyone may browse) or `"restricted"` (Viewers must request access). |
| `allow_list` | []string | `[]` | Node IDs always allowed (restricted mode). |
| `block_list` | []string | `[]` | Node IDs always denied. |
| `data_dir` | string | the config dir | Where `library.db` and `cache/` are stored. |

> **Note:** Subfolders are scanned recursively. Library items are identified by a
> content hash (BLAKE3), so byte-identical files in different folders collapse to a
> single library entry — copying a video into a subfolder does not create a second
> entry.

**Files created under the config/data dir:**

```
<config-dir>/identity.key     # Ed25519 keypair (this Node's stable identity) — keep private
<config-dir>/config.toml       # the file above
<data-dir>/library.db          # SQLite index of shared Titles
<data-dir>/cache/              # cached HLS segments
```

---

## Using the app

From a Node's browser UI:

1. **Presence** — see which Peers are online (left panel). Each Peer shows its display name and
   Node ID.
2. **Browse a Peer** — open a Peer to request its **Catalog**. If the Host is `public`, you see
   its Titles immediately; if `restricted`, you **request access** and the Host approves you from
   their own UI (their *Requests* panel).
3. **Stream a Title** — pick a Title to play it as HLS, pulled directly from the Host over
   WebRTC. Text subtitle tracks are converted to WebVTT on the fly (image-based subtitle
   tracks aren't rendered yet).
4. **Watch Party** — a Host **starts a party** on a Title; Viewers **join** it. The Host's
   playback (play / pause / seek) is authoritative and all Viewers stay hard-synced. While a
   party is live, Viewers form a **Swarm**: each caches Segments and re-serves them to other
   Viewers (integrity-checked against Host-published BLAKE3 hashes), offloading the Host.

The UI updates live via Server-Sent Events (Presence changes, access requests, audience changes,
party-ended, …).

---

## The control-plane API

The Node's loopback bridge exposes a small REST + SSE API (token-guarded; the served page injects
the token same-origin so it stays out of browser history). It backs the SPA, but is also handy
for scripting/automation.

| Method & path | Purpose |
| --- | --- |
| `GET /api/self` | This Node's ID and display name. |
| `GET /api/presence` | Online Peers. |
| `GET /api/library` | This Node's own shared Titles. |
| `GET /api/peers/{id}/catalog` | A Peer's access-filtered Catalog. |
| `POST /api/peers/{id}/request-access` | Ask a Host for access (`{"message": "..."}`). |
| `GET /api/requests` | Pending inbound access requests. |
| `POST /api/requests/{id}/approve` | Approve a pending request. |
| `GET /api/party/current` | The party this Node is hosting or viewing, if any. |
| `GET /api/party/audience` | Current Audience. |
| `POST /api/party/start` | Start a Watch Party (`{"contentId": "..."}`). |
| `POST /api/party/join` | Join a party (`{"hostNodeId": "...", "contentId": "..."}`). |
| `POST /api/party/leave` | Leave the party being viewed. |
| `POST /api/party/end` | End the party being hosted. |
| `GET /api/events` | Server-Sent Events stream of live updates. |
| `GET /s/{token}/{host}/{contentId}/{name}` | HLS playlists/segments proxied from a Host. |
| `GET /party/{token}` | Player ↔ sync-engine WebSocket (used by the SPA). |

---

## Development

```bash
make test          # go test ./...
go test -race ./... # the CI gate (race detector on)
go vet ./...
gofmt -l .         # must print nothing (CI fails on unformatted files)
```

Web UI:

```bash
make webui         # npm ci + nuxt generate, then copy the SPA into internal/bridge/dist for embedding
make webui-test    # vitest (unit tests for the SPA)
make webui-e2e     # Playwright smoke test (boots a signal server + 2 real Nodes)

cd webui && npm run dev   # live-reloading dev server; open the Node's printed "Dev URL (nuxt dev)"
```

> `make webui` overwrites `internal/bridge/dist/`. Everything there except `index.html` is
> gitignored; restore the committed placeholder before committing Go-only changes:
> `git restore internal/bridge/dist/index.html`.

Protobuf (only when `proto/peer/v1/peer.proto` changes):

```bash
make proto         # protoc --go_out=. ... proto/peer/v1/peer.proto
make tidy          # go mod tidy
```

**CI** ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs two jobs on push/PR: a Go job
(`gofmt` check, `go vet`, `go test -race ./...`) and a Web UI job (`npm ci`, `vitest`, `nuxt
generate`). The Playwright e2e is non-blocking.

---

## Design decisions

Architecture Decision Records live in [`docs/adr/`](docs/adr):

| ADR | Decision |
| --- | --- |
| [0001](docs/adr/0001-peer-wire-protocol.md) | Peer wire protocol (control + bulk channels). |
| [0002](docs/adr/0002-content-id-full-blake3-hash.md) | Content ID = full BLAKE3 hash of the source file. |
| [0003](docs/adr/0003-identity-binding-signed-sdp.md) | Identity binding via signed SDP. |
| [0004](docs/adr/0004-watch-party-sync-model.md) | Host-authoritative Watch Party sync model. |
| [0005](docs/adr/0005-mesh-swarm-distribution.md) | Mesh Swarm Segment distribution. |
| [0006](docs/adr/0006-browser-ui-control-plane.md) | Browser-UI control plane. |
| [0007](docs/adr/0007-watch-party-danmaku.md) | Watch Party Danmaku: ephemeral, Host-relayed. |

Per-slice design specs and implementation plans are under
[`docs/superpowers/`](docs/superpowers).

---

## Troubleshooting

- **Node exits immediately / "failed to dial signaling".** The signaling server isn't reachable
  at `signaling_url`. Start `signal-server` first and check the URL/port.
- **Library indexes 0 titles.** Confirm `ffprobe` is on `PATH`, the `shared_folders` paths exist,
  and the files use an indexed extension (`.mp4 .mkv .mov .m4v .webm`).
- **A Peer can't browse my Library.** With `default_visibility = "restricted"` they must request
  access and you must approve them (or add their Node ID to `allow_list`).
- **The browser UI is blank/placeholder.** You built Go-only. Run `make webui` (or `make build`)
  to embed the real SPA.
- **Two Nodes don't see each other.** They must use the *same* `signaling_url` and *different*
  `--config-dir`s (otherwise they share one identity).
- **Viewer↔Viewer Swarm offload doesn't kick in.** Behind symmetric NAT the mesh edges can fail
  and silently fall back to fetching from the Host (still correct, just no offload) — TURN relay
  support is not yet implemented.
