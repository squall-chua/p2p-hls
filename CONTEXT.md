# P2P HLS Streaming

A peer-to-peer media application. Each User runs a Node that shares their local media Library. Other Users discover online Peers, browse allowed Libraries, and stream (HLS) or download media directly Peer-to-Peer over WebRTC. A later phase adds Watch Parties with hard-synced playback and a load-sharing Swarm.

## Language

### Actors

**Node**:
One running instance of the app, one per User. Holds the identity keypair, the Library, and all P2P/streaming logic.
_Avoid_: client, instance.

**Peer**:
Another Node that this Node holds, or could hold, a direct WebRTC connection to. Purely relational.
_Avoid_: neighbor.

**User**:
The human operating a Node.
_Avoid_: account.

**Host**:
The Node that owns an original file and originates a stream (and later, a Watch Party).
_Avoid_: seeder, server, owner.

**Viewer**:
A Node consuming a stream. A Viewer that re-serves cached segments in the Swarm is still a Viewer, acting as a relay.
_Avoid_: guest, client, leecher.

### Content

**Title**:
One shareable media item in a Library — one source file, identified by one Content ID. The unit a Viewer browses, streams, or downloads.
_Avoid_: content, media, item, file, video.

**Library**:
The full set of Titles a User shares from their Node. The owner's complete view.
_Avoid_: collection.

**Catalog**:
The access-filtered listing of a Host's Library that a specific Viewer is permitted to browse. What a browse request returns. Two Viewers may receive different Catalogs from the same Library.
_Avoid_: index, listing.

**Content ID**:
The content-hash identifier of a Title's source file. Stable across Nodes — the same file shared by two Nodes has the same Content ID.
_Avoid_: hash, file ID.

**Shared folder**:
A directory a User configures for the Node to scan; every eligible file found becomes a Title in the Library.
_Avoid_: watch folder, source dir.

### Network

**Signaling server**:
The shared, trust-minimized service that brokers WebRTC connections between Nodes and tracks who is online. It relays connection handshakes and Presence; it never sees Catalogs or media.
_Avoid_: tracker, broker, coordinator.

**Presence**:
The set of Nodes currently online and reachable via the Signaling server, with their Node IDs and public keys. A Node receives a snapshot on connect and incremental join/leave updates thereafter.
_Avoid_: roster, online list.
