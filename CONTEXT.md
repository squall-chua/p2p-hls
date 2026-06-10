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
A Node consuming a stream. A Viewer that re-serves cached Segments in the Swarm is still a Viewer, acting as a relay.
_Avoid_: guest, client, leecher.

### Content

**Title**:
One shareable media item in a Library — one source file, identified by one Content ID. The unit a Viewer browses, streams, or downloads.
_Avoid_: content, media, item, file, video.

**Segment**:
A short, contiguous chunk of a Title's stream — the granular unit a Viewer fetches, caches, verifies, and re-serves in the Swarm. Many Segments make up one Title's playback.
_Avoid_: chunk, piece, part.

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
A directory a User configures for the Node to scan; every eligible file found becomes a Title in the Library. The scan root, not necessarily a top-level Folder (see Folder).
_Avoid_: watch folder, source dir.

**Folder**:
A navigable directory node in the hierarchical view of a Library or Catalog, derived from Titles' on-disk locations. With a single Shared folder, the top-level Folders are its immediate subdirectories; with several Shared folders, each Shared folder is itself a top-level Folder. A Folder is a presentation grouping of Titles, not a stored entity.
_Avoid_: directory, path, node.

### Network

**Signaling server**:
The shared, trust-minimized service that brokers WebRTC connections between Nodes and tracks who is online. It relays connection handshakes and Presence; it never sees Catalogs or media.
_Avoid_: tracker, broker, coordinator.

**Presence**:
The set of Nodes currently online and reachable via the Signaling server, with their Node IDs and public keys. A Node receives a snapshot on connect and incremental join/leave updates thereafter.
_Avoid_: roster, online list.

### Watch Party

**Watch Party**:
A Host playing one Title in sync for a set of Viewers. The Host's playback position is authoritative; the participating Viewers' playback follows it (play, pause, seek). A Viewer joins and leaves an active Watch Party without affecting the others. Only the Host drives playback.
_Avoid_: session, room, screening, party (alone).

**Audience**:
The set of Viewers currently in a Watch Party. Narrower than Presence (which is global online Nodes) and local to one Host's Watch Party. The Host plays to the Audience but is not part of it.
_Avoid_: roster, room, members.

**Swarm**:
The Viewers of a Watch Party acting as a mesh that re-serves cached Segments to one another to share distribution load. Same membership as the Audience, seen as a distribution network rather than a sync group. The Host originates Segments to the Swarm but is not part of it.
_Avoid_: cluster, mesh (alone), torrent.
