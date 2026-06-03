# Content ID is a full-file BLAKE3 hash

**Status:** accepted

A Title's Content ID is the BLAKE3 hash of its **entire** source file, not a sampled hash. Hashes are cached by `(path, size, mtime)` so rescans only hash new or changed files.

**Why:**
- **Dedup + Swarm correctness** depend on identical files producing identical Content IDs across Nodes. A sampled hash carries a small collision risk that would corrupt this.
- **Download integrity** verifies a received file's hash equals its Content ID. Only a full hash verifies every byte; a sampled hash would leave most of the file unchecked.
- BLAKE3 is fast enough that full hashing is disk-read-bound, so the cost is "read each file once" — acceptable as a background job with the mtime cache making rescans cheap.

**Consequences:**
- First-time indexing of a very large Library reads every byte once (minutes on slow disks) — mitigated by background indexing with progress UI and the mtime cache.
- Do **not** later switch to sampled hashing for indexing speed: it would break cross-Node dedup and weaken download integrity. If indexing speed becomes a real problem, address it with concurrency/throttling, not by weakening the hash.
