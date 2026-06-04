# P2P HLS Streaming — Design Spec (Slices 1–3)

- **Date:** 2026-06-03
- **Status:** Grilled (2026-06-03) — detailed decisions locked; see `CONTEXT.md` (glossary) and `docs/adr/` (ADRs 0001–0003)
- **Scope of this spec:** Slices 1–3 (foundation + library sharing + 1:1 HLS streaming). Watch party + mesh swarm is a documented fast-follow spec.

## Vision

A peer-to-peer media application. A user runs a native app that shares their local media library. Other users can discover online peers, browse allowed libraries, and **stream** (HLS) or **download** media directly peer-to-peer. A later slice adds **watch parties**: a host plays a video, guests join with hard-synced playback, and guests share the streaming load with the host via a mesh swarm.

This spec covers everything *except* the watch party, but every interface is designed so the watch party drops in without rework.

## Client Shape

- **Native app (Go), one per user** — owns the media library (full filesystem access), holds the keypair identity, runs Pion WebRTC, runs `ffmpeg`/`ffprobe`, runs a loopback HTTP server, and serves the bundled web UI (`go:embed`). Single static binary. Nearly all logic lives here.
- **Web UI** — a standard SPA served from the loopback HTTP server. Uses `hls.js` for playback. It talks **only** to the loopback server and never knows P2P exists.
- **Signaling server (one small shared service)** — a dumb WebRTC broker + presence registry. Knows who is online and their public keys; relays SDP/ICE between nodes. Never sees catalogs or media.

## Key Decisions & Rationale

| Decision | Choice | Rationale |
|---|---|---|
| Client type | Native Go app + bundled web UI | Filesystem access to library; web UI for ergonomics; single binary via `go:embed`. |
| P2P transport | **WebRTC data channels (Pion)** | Best-in-class NAT traversal (ICE) for consumer machines, and **browser interoperability** — a future browser-only guest client can natively peer with the same network. This browser-guest future is the **deciding rationale** for WebRTC over a native-only stack like libp2p. |
| Discovery | Central signaling + presence, **P2P catalog** | Signaling server is a dumb relay + phonebook of who's online. Browsing happens by querying peers directly + peer-exchange hints. Server never sees content. |
| HLS source | **Remux when possible, transcode as fallback** | Per-stream: copy only H.264 video + AAC/MP3 audio; transcode everything else to H.264/AAC (strict, for universal browser playback). `ffprobe` derives `hlsCompatible`. See Detailed Specs §HLS. |
| Wire protocol | **Protobuf control + binary bulk over 2 data channels** | `control`+`bulk` channels, 16 KiB frames, requestId-correlated. See **ADR 0001**. |
| Identity binding | **Signed SDP + DTLS fingerprint** | Trust-minimized signaling; server cannot MITM/read media. See **ADR 0003**. |
| Access control | **Per-node identity + allow/block; restricted by default** | Ed25519 keypair per node; **default restricted** (allow-list), per-Library granularity; in-app access-request/approve flow. |
| Runtime/stack | **Go + Pion + ffmpeg/ffprobe + Vue 3/Vite SPA** | Mature pure-Go WebRTC, strong concurrency for swarm logic, single binary; SPA embedded via `go:embed`. |
| HLS-over-WebRTC | **Local HTTP bridge (Approach A)** | Loopback HTTP server (127.0.0.1, ephemeral port, Origin check + per-session token) serves a standard `hls.js` player; Go handler fetches segments over data channels and caches them. P2P hidden behind loopback; the watch-party swarm drops in invisibly at the same seam. |
| `contentId` | **Full-file BLAKE3 hash** | Stable across peers (same file → same ID), enabling dedup, the later swarm, and complete download integrity. See **ADR 0002**. |
| Index store | **SQLite** | Queryable, robust, restart-fast. |
| Peer exchange | **Minimal hints in MVP** | Browse presence-listed peers + volunteered hints. Full gossip/DHT deferred. |
| Download integrity | **Transfer original bytes as-is, verify hash == `contentId`** | Remuxing changes container bytes (breaks the content hash), so downloads must be the raw original. Hash check is a built-in integrity/tamper guard. |
| Subtitles | **Text subs → WebVTT (sidecar + embedded)** | SRT/ASS/VTT + embedded text streams converted to WebVTT; master playlist with subtitle group. Image-based (PGS/VobSub) deferred. |
| Segment cache | **Disk, LRU + TTL** on host and viewer | Default **2 GiB budget, 6h idle TTL**, segment-level eviction. Configurable. |
| Transcoding playlist | **Live/growing playlist**, finalized to VOD when done | Start watching before transcode finishes. |
| TURN | **Out of scope (MVP)** | STUN only; some NAT-to-NAT pairs won't connect — documented limitation. |

## Architecture

### Topology

```
        ┌─────────────────┐
        │ Signaling server│   (presence + SDP/ICE relay only)
        └───────┬─────────┘
        WebSocket│ (both nodes connect here to handshake)
     ┌───────────┴───────────┐
     ▼                       ▼
┌─────────┐   WebRTC DC   ┌─────────┐
│ Node A  │◄═════════════►│ Node B  │   (catalog queries + segment transfer)
│ (host)  │   direct P2P  │ (viewer)│
└────┬────┘               └────┬────┘
     │ loopback HTTP           │ loopback HTTP
     ▼                         ▼
  Web UI A                  Web UI B  (hls.js player, browse screens)
```

### Node package decomposition (each package = one job)

| Package | Responsibility |
|---|---|
| `identity` | Generate/persist Ed25519 keypair; sign/verify; node ID = pubkey fingerprint. |
| `signal` | WebSocket client to signaling server; presence; SDP/ICE exchange. |
| `peer` | Pion peer connections + data channels; RPC framing; segment chunking/reassembly; backpressure. |
| `library` | Scan shared folders, `ffprobe` metadata, maintain local catalog index (SQLite), file-change watcher. |
| `catalog` | Browse protocol RPCs (`ListLibrary`, `GetMetadata`); allow/block access control; peer-exchange hints. |
| `media` | ffmpeg remux/transcode → HLS segmenter; playlist generation; segment cache (LRU+TTL). |
| `httpbridge` | Loopback HTTP server: serves web UI, HLS playlists, segments; dispatches cache-hit vs peer-fetch; raw-file download. |
| `app` | Wiring/lifecycle, config, embedded web UI (`go:embed`). |

**Critical seam:** `httpbridge` asks for "segment N of content X" and does not care whether it comes from the local `media` cache or a remote peer via `peer`. This seam is what lets the watch-party swarm slot in later without touching the player.

**Protocol neutrality:** the wire protocol (`ListLibrary`/`GetMetadata`/`GetPlaylist`/`GetSegment`/file-transfer) must stay **client-agnostic** — no assumption that the consumer is the native loopback bridge. A future browser guest (which can't run a loopback server) consumes the same RPCs and feeds MSE directly.

## Identity & Signaling

### Identity (`identity`)
- On first launch, generate an **Ed25519 keypair**, persisted to the app config dir (e.g. `~/.config/p2p-hls/identity.key`).
- **Node ID** = fingerprint of the public key (e.g. truncated base32 of SHA-256) — stable network address, used in allow/block lists.
- Private key signs signaling messages so peers can verify an offer's origin (access control keys off node ID, so identity must be unforgeable).
- Optional self-asserted display name (not trusted for auth — only the key is).

### Signaling server (`signal` + the server)
- **Transport:** WebSocket. On startup a node registers `{nodeID, pubkey, displayName, signature}`.
- **Presence:** server keeps an in-memory map of online nodes; nodes can request the presence list to get candidate peers. This is the only global directory — a phonebook of who's online, not a content catalog.
- **Brokering:** node A sends `{to: B, sdpOffer, signature}`; server relays to B; B answers; ICE candidates trickle through the same relay. After that the data channel is direct and the server is out of the loop.
- **Statelessness:** no DB for MVP; only the live presence map. On restart, nodes reconnect and re-register.
- **NAT:** Pion + public STUN configured. TURN is a documented config option, not built. Some symmetric-NAT pairs won't connect — known limitation.

### Trust model
The signaling server is **trust-minimized, not trusted**. It can lie about presence or misroute relays, but cannot forge node identity (signatures) and cannot see catalogs or media. The "cannot see media" guarantee depends on **identity binding**: a Node signs its SDP *including the DTLS certificate fingerprint* with its Ed25519 key, and the Peer verifies the negotiated cert matches — so a malicious server cannot swap the fingerprint and MITM the connection. See **ADR 0003**. That is the deliberate "dumb relay" boundary.

## Library Indexing & P2P Catalog

### Local indexing (`library`)
- User configures shared folders in settings; node scans for video files.
- Per file, run `ffprobe` once → duration, container, video/audio codecs, resolution, and derived `hlsCompatible` (H.264+AAC → remux path; else transcode path).
- Build a local catalog index: `contentId → {title, path, size, duration, codecs, hlsCompatible, addedAt}`.
- `contentId` = **full-file BLAKE3 hash** (same file across peers → same ID; enables dedup, the later swarm, and complete download integrity). Hashes cached by `(path, size, mtime)` so rescans skip unchanged files. See **ADR 0002**.
- Index persisted in **SQLite**; a background watcher re-scans on file changes.

### Browse protocol (`catalog`) — RPC over the data channel
- `ListLibrary` → catalog entries the verified requester is allowed to see (title, contentId, duration, codecs, size — **metadata only**).
- `GetMetadata(contentId)` → details for one item.
- **Peer-exchange hints:** responses may include hints about other online sharing peers. Combined with the signaling presence list, this is how the P2P catalog spreads without a central index. MVP keeps this minimal; full gossip/DHT deferred.

### Access control (`catalog`)
- Per-node policy: library default **public or restricted**, plus explicit **allow-list / block-list** by node ID.
- Every `ListLibrary`/`GetMetadata`/stream/download request checks the **verified** requester node ID against policy before answering. Denied peers get empty/denied responses.
- Requests arrive on an authenticated data channel (identity verified at signaling handshake), so access decisions are trustworthy.

## HLS Streaming Pipeline

### Host node (`media`)
- A stream request names a `contentId`. The host produces an HLS rendition:
  - `hlsCompatible == true` → **remux** (`ffmpeg -c copy`, fMP4/TS segmentation). Cheap, near-instant.
  - `hlsCompatible == false` → **transcode** to H.264/AAC, then segment. CPU-heavy; playback starts as segments become ready.
- ffmpeg emits a **media playlist** + numbered segments into a per-content **segment cache** (disk, LRU+TTL). Second viewer reuses them.
- RPCs: `GetPlaylist(contentId)` → playlist bytes; `GetSegment(contentId, index)` → segment bytes (produced if needed).
- **Concurrency cap:** concurrent transcodes capped (configurable, default small); excess requests queue.

### Viewer node (`httpbridge` + `peer`)
- Web UI's `hls.js` points at `http://127.0.0.1:port/stream/{contentId}/playlist.m3u8`.
- **Playlist request** → bridge calls `GetPlaylist` over the data channel → segment URLs rewritten to loopback → returned to `hls.js`.
- **Segment request** → bridge checks local segment cache → miss → `GetSegment(contentId, index)` over the data channel → cache → return bytes.
- `hls.js` handles buffering, ordering, and the `<video>` element. The bridge hides whether bytes came from cache or peer — the seam the swarm reuses later.
- **Growing playlist:** while transcoding, `hls.js` re-fetches the playlist until the host marks it complete (VOD). If the viewer outruns the transcode, the host returns a "not ready, retry" signal and the player waits rather than erroring.

### Download (separate path)
- "Directly download" = transfer the **original file bytes as-is** (chunked over the data channel), then verify the received file's hash equals `contentId`. On mismatch, reject with a corruption/tamper warning. Fully separate from the HLS path.

### Transport framing (`peer`)
- Segments and file chunks exceed the data channel's max message size, so `peer` chunks each into ordered frames with a small header (`{contentId, segIndex/offset, frameIndex, total}`) and reassembles on the other side.
- Flow control respects the data channel's buffered-amount threshold (backpressure) so a slow viewer can't blow up host memory.

## End-to-End Data Flow

Scenario: viewer V streams a `.mkv` (transcode path) from host H.

1. **Startup (both):** load/generate identity → connect to signaling server (WS) → register presence.
2. **Discover:** V requests presence list → sees H → initiates WebRTC (SDP/ICE via relay) → direct data channel open.
3. **Browse:** V sends `ListLibrary` → H checks V's verified node ID against policy → returns visible catalog → UI shows H's library.
4. **Play:** user clicks a title → web UI loads `http://127.0.0.1:port/stream/{contentId}/playlist.m3u8` in `hls.js`.
5. **Playlist:** bridge → `GetPlaylist` → H starts ffmpeg transcode, emits a growing playlist → returned → URLs rewritten → parsed by `hls.js`.
6. **Segments:** `hls.js` requests segment 0 → cache miss → `GetSegment(contentId, 0)` → H returns segment → `peer` frames it → V reassembles → bridge caches + returns → `<video>` plays. Repeat with buffering.
7. **Refresh:** `hls.js` re-fetches the growing playlist until H marks it complete.
8. **Download (alt):** user clicks download → bridge streams original bytes chunked from H → writes to disk → verifies hash == `contentId`.

**Invariant:** everything the viewer needs (`ListLibrary`, `GetPlaylist`, `GetSegment`, raw-file download) is an RPC over the authenticated data channel, and the loopback HTTP bridge is the only thing the web UI talks to.

## Error Handling & Edge Cases

**Connectivity**
- Signaling server down → existing P2P sessions continue; UI shows "offline from discovery," retries with backoff; no new peers until back.
- NAT traversal fails (no TURN) → attempt times out; UI shows a clear "couldn't connect (NAT)" message. Documented limitation.
- Peer disconnects mid-stream → in-flight `GetSegment` fails; bridge surfaces a stall; viewer attempts reconnect; if it fails, playback errors gracefully.

**Media pipeline**
- ffprobe/ffmpeg missing or erroring → host returns a typed RPC error; viewer UI shows "can't be streamed" with reason. ffmpeg presence checked at startup, surfaced in settings.
- Transcode slower than playback → `hls.js` hits live edge; host returns "not ready, retry"; player waits/buffers.
- Unsupported/corrupt file → flagged `unplayable`, hidden from streamable list (still downloadable as raw bytes).

**Integrity & access**
- Download hash mismatch → reject file, surface corruption/tamper warning, don't keep it.
- Access revoked mid-session → subsequent RPCs denied; active stream may cut at next segment. Acceptable for MVP.
- Two peers, same `contentId` → fine by design; viewer may pull from either (slices 1–3 pick one source; swarm uses both later).

**Resource safety**
- Segment cache bounded by LRU + TTL on host and viewer.
- Concurrent transcodes capped; excess queues.
- Data-channel backpressure respected via buffered-amount thresholds.

## Testing Strategy

Per-package isolation at the seams (standard `testing` + testify, table-driven; `ffmpeg`/`ffprobe` behind interfaces):

- **`identity`** — keygen determinism, sign/verify round-trip, fingerprint stability.
- **`signal`** — register/presence/relay framing against a fake WS server; the real signaling server gets an integration test (two fake nodes handshake through it).
- **`peer`** — RPC framing + segment chunking/reassembly round-trips (oversized payloads, out-of-order frames, backpressure). Heaviest coverage — fiddliest code.
- **`library`** — ffprobe parsing, `hlsCompatible` derivation, content-hash stability, SQLite persistence. ffprobe stubbed via interface.
- **`catalog`** — allow/block policy decisions, RPC handlers, peer-exchange hint propagation.
- **`media`** — remux-vs-transcode decision, playlist generation, cache LRU+TTL eviction. ffmpeg behind an interface; a couple of integration tests run real ffmpeg on tiny samples.
- **`httpbridge`** — HTTP routes, playlist URL rewriting, cache-hit vs peer-fetch dispatch (peer mocked).
- **End-to-end** — two real nodes in one test process + a real signaling server, connected over loopback WebRTC; assert browse → stream a sample (remux *and* transcode) → download-with-hash-verification. Proves the seams connect.

## Out of Scope (this spec)

- **Watch parties, hard-sync playback, mesh swarm** → fast-follow spec. The `httpbridge`↔`peer` seam is designed for drop-in.
- **TURN server / relay** → documented NAT limitation; STUN only.
- **Full gossip/DHT** → minimal peer-exchange hints only.
- **Browser-only guest client** → future; protocol kept client-agnostic to enable it.
- **Mobile, accounts/cloud, recommendations, ABR/transcoding ladder** → single rendition for MVP.

## Success Criteria (Slices 1–3)

Two users running the app on different machines can:
1. See each other online.
2. Browse each other's allowed libraries.
3. Stream a video (both remux and transcode paths) that plays in the browser UI with buffering.
4. Download a file with verified integrity.

All media moves directly peer-to-peer over WebRTC; the signaling server only brokers connections.

## Detailed Specifications (grilled 2026-06-03)

All open questions from brainstorming are resolved below. Domain vocabulary lives in `CONTEXT.md`; architectural decisions in `docs/adr/`.

### Vocabulary (see `CONTEXT.md`)
Canonical terms: **Node**, **Peer**, **User**, **Host**, **Viewer**, **Title** (one media item = one source file = one Content ID), **Library** (a User's full shared set), **Catalog** (the access-filtered slice a specific Viewer may browse), **Content ID**, **Shared folder**, **Signaling server**, **Presence**.

### Wire protocol (ADR 0001)
- Two reliable, ordered data channels per Peer pair: **`control`** (Protobuf RPCs, requestId-correlated) and **`bulk`** (binary payload frames, interleaved for app-level multiplexing).
- **16 KiB** frame size (browser-interop ceiling). Backpressure: pause at `bufferedAmount` > 1 MiB, resume on `bufferedamountlow` (256 KiB). `requestId` uint64, monotonic per connection, allocated by requester.
- Error model: response `status` enum (`OK`, `DENIED`, `NOT_FOUND`, `UNAVAILABLE`, `INTERNAL`) + optional detail; transfers abort via `abort{requestId, reason}`. Requester-side per-RPC timeouts.
- **Protocol version + capability handshake** on data-channel open; incompatible versions refuse cleanly (enables evolution + future HEVC/browser-guest capability negotiation).
- RPCs: `Browse` (→ Catalog), `GetMetadata`, `GetPlaylist`, `GetSegment` (generalized to address subtitle tracks too), raw-file download, `RequestAccess`, `AccessGranted`.

### Identity & signaling (ADR 0003)
- **Ed25519** keypair at `~/.config/p2p-hls/identity.key` (OS config dir, `0600`, unencrypted MVP). Node ID = pubkey fingerprint. Export/import for backup.
- **Identity binding:** Node signs SDP **including the DTLS fingerprint**; Peer verifies `nodeId == fingerprint(pubkey)`, the signature, and the negotiated cert. Trust-minimized server.
- Signaling: **JSON over WebSocket**. On register: full **Presence snapshot**, then incremental `presenceJoin`/`presenceLeave` pushes. WS ping/pong liveness. Messages: `register`, `presenceSnapshot`, `presenceJoin`, `presenceLeave`, `relay`, `error`.
- **STUN:** default public list, overridable. **No TURN** (documented NAT limitation).

### Content ID & library indexing (ADR 0002)
- **Full-file BLAKE3** hash; cached by `(path, size, mtime)`. Background indexing with progress UI; bounded worker pool.
- **Eligibility:** recursive scan of Shared folders, extension allowlist (`.mp4 .mkv .mov .m4v .webm .avi .ts .mpg .mpeg .wmv .flv`) → `ffprobe` confirms a decodable video stream; skip rejects.
- **Per-Title metadata:** Content ID, path, size, mtime, duration, container, video/audio codecs, width×height, `hlsCompatible`, subtitle tracks (text/image, language), `addedAt`, `displayTitle` (filename-derived, or container `title` tag; no online lookup).
- **Watcher:** `fsnotify`, debounced; re-hash only on `(size, mtime)` change.

### Access control
- Per-Node policy `{ defaultVisibility, allowList[], blockList[] }`. **Default `restricted`** (allow-list), **per-Library** granularity. Evaluation: blockList denies; restricted → only allowList; public → all except blockList.
- Every `Browse`/`GetMetadata`/stream/download request checks the **verified** requester Node ID.
- **In-app access-request flow:** a verified-but-denied Viewer may send `RequestAccess`; Host surfaces it for Approve/Deny; Approve adds the Node ID to the allow-list and notifies via `AccessGranted`.

### HLS pipeline
- **Per-stream copy/transcode (strict):** copy iff video ∈ {H.264} or audio ∈ {AAC, MP3}; else transcode that stream to H.264 / AAC-LC. A Title is fully remuxable only when all streams are copyable.
- **Transcode rendition (single):** preserve source resolution capped at **1080p** (downscale, never upscale); H.264 High, **CRF 23**, maxrate ~6–8 Mbps; preset **`veryfast`** (must run ≥1× realtime; software x264 MVP, hardware accel deferred); AAC-LC stereo downmix 128–160 kbps.
- **Segmentation (both paths):** **4-second** segments, keyframe-aligned.
- **Subtitles:** text subs (SRT/ASS/VTT sidecars + embedded text streams) → **WebVTT**, exposed as selectable tracks via a **master playlist** with a `TYPE=SUBTITLES` group (required on both remux and transcode paths). Image-based subs (PGS/VobSub) detected/listed but **not served** (deferred).
- **Segment cache:** content-addressed by `(contentId, rendition, segmentIndex)`; **2 GiB budget, 6h idle TTL**, segment-level LRU; uniform Host/Viewer; under `~/.local/share/p2p-hls/cache/`.

### Loopback bridge security
- Bind `127.0.0.1`, **ephemeral port**, **Origin/Host check**, **per-session token** (handed to the embedded UI at launch). Blocks DNS-rebinding and other local processes.

### Project layout
Single Go module `github.com/squall-chua/p2p-hls`, monorepo, two binaries:
```
cmd/{node, signal-server}
internal/{identity, signaling, signalserver, peer, library, catalog, media, bridge, app}
proto/   web/   docs/
```
Config: `config.toml` in config dir; SQLite for the Library index.

### Web UI (Vue 3 + Vite, embedded via `go:embed`)
Screens: (1) First-run setup (Node ID, display name, Shared folders); (2) Settings (folders, default visibility, allow/block, cache budget, signaling URL, STUN, identity export/import, ffmpeg status); (3) Peers/Presence → Browse Catalog; (4) Player (hls.js, subtitle track selection); (5) Downloads (progress + integrity status); (6) My Library + indexing progress. Plus access-request elements: a "Request access" action on the Viewer side and an incoming-requests Approve/Deny view on the Host.

### Deferred (beyond slices 1–3)
Watch parties + hard-sync + mesh swarm; TURN; full gossip/DHT; browser-only guest client; image-subtitle burn-in/OCR; passphrase-encrypted identity; online metadata enrichment; per-Title visibility; hardware-accelerated transcode; ABR/multi-rendition ladder; mobile.
