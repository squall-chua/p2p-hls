# Peer wire protocol over WebRTC data channels

**Status:** accepted

Peers communicate over two reliable, ordered WebRTC data channels per connection: `control` and `bulk`. Control messages (browse, metadata, playlist requests, responses, aborts) are **Protobuf**, length-delimited, and correlated by a requester-allocated `uint64` `requestId`. Large binary payloads (HLS segments, file-download bytes) stream on `bulk` as frames — a compact header `{requestId, seq, lastFlag}` + raw bytes — interleaved across concurrent transfers for app-level multiplexing.

**Why this shape:**
- The protocol is long-lived and must be implemented by multiple client types — the native Go Node now, a browser-only guest later. Protobuf gives schema + versioning rigor and first-class Go + TS/JS codegen. (This is why not JSON.)
- Two channels keep small control RPCs responsive while large transfers run, with no head-of-line blocking between them; interleaved framing on `bulk` multiplexes concurrent transfers without per-transfer channel churn.

**Locked defaults:**
- **Frame size:** 16 KiB on `bulk` — the safe cross-implementation WebRTC max message size. Chosen for browser-guest interop rather than relying on Pion's large-message support.
- **Backpressure:** sender pauses when `bufferedAmount` exceeds 1 MiB, resumes on `bufferedamountlow` (256 KiB threshold).
- **Error model:** every response carries a `status` enum (`OK`, `DENIED`, `NOT_FOUND`, `UNAVAILABLE`, `INTERNAL`) + optional detail. Transfers abort via an `abort{requestId, reason}` control message.
- **Timeouts:** requester-side per-RPC timeout; on fire, send `abort` and surface the failure.

**Consequences:**
- A Protobuf schema + codegen step is part of the build for both Go and the (future) browser client.
- The protocol stays client-agnostic: it assumes nothing about the consumer being the native loopback HTTP bridge, so a browser guest can consume the same RPCs and feed MSE directly.
