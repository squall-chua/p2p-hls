# Identity binding via signed SDP + DTLS fingerprint

**Status:** accepted

A Node proves its Ed25519 identity to a Peer by **signing its SDP — including the DTLS certificate fingerprint — with its Ed25519 private key**. The receiver verifies that `nodeId == fingerprint(pubkey)`, verifies the signature over the SDP+fingerprint+timestamp, and confirms the negotiated DTLS cert matches the signed fingerprint. This binds "the Peer I am encrypted with" to "Ed25519 Node ID A."

**Threat model:** the signaling server is **trust-minimized**, not trusted. It may lie about Presence or misroute relays, but it must not be able to read or tamper with media.

**Why:**
- WebRTC's DTLS uses self-signed certs whose fingerprint travels in the SDP. Without binding, a malicious signaling server could swap the fingerprint and MITM the connection — terminating DTLS with both sides and reading all traffic.
- Signing the fingerprint is the only option that makes the trust-model guarantee ("the signaling server cannot see Catalogs or media") actually true, and it makes every data-channel RPC attributable to a verified Node ID — which is what allow/block access control depends on.

**Rejected alternative:** a post-connect challenge-response (sign a nonce) proves key ownership but does **not** prevent a malicious server from MITMing the media, since the server still terminated both DTLS sessions. Acceptable only under a trusted-server model, which we explicitly reject.

**Consequences:**
- Requires extracting and verifying the remote DTLS certificate fingerprint from Pion.
- Signaling messages carry `{nodeId, pubkey, signature}` alongside the relayed SDP/ICE.
