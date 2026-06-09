# Library Thumbnails + Recursive Watch — Design

Date: 2026-06-09

## Problem

Two requests against the library:

1. **Thumbnails** — videos in the library (own and peers') show only a generic
   clapperboard icon. Add a real poster image per video.
2. **Recursive scan** — videos placed in subfolders appeared not to be indexed.

## Diagnosis of the "recursive" complaint

The initial scan is **already recursive**: [`ScanOnce`](../../../internal/library/scanner.go)
uses `filepath.WalkDir`, and the nested file `host/media/test/ElephantsDream.mp4`
is in fact indexed in `host/library.db`.

The real cause of "subfolders never appear" is **content-hash de-duplication**:
`ContentID` is a full-file BLAKE3 hash (ADR 0002) and the SQLite primary key.
`host/media/ElephantsDream.mp4` and `host/media/test/ElephantsDream.mp4` are
byte-identical, so they collapse to one row, and because the walk visits the
subfolder copy last, the stored `path` flips to the nested copy. Dropping copies
of existing videos into a subfolder therefore adds no new entries. A *unique*
video in a subfolder does appear.

The one genuine gap: the live watcher ([`Watch`](../../../internal/library/scanner.go))
registers only the top-level roots with fsnotify (non-recursive), so a file added
to a subfolder *while the app runs* is not picked up until a restart/rescan.

**Decisions (confirmed with user):**
- Keep content-hash identity / de-dup as-is (ADR 0002). Document the behavior.
- Fix the live watcher to be recursive.
- Thumbnails: generate from the video via ffmpeg; show on own library **and**
  peers' libraries; deliver peer thumbnails **inline in the catalog metadata**.

## Part A — Recursive live-watcher

[`Scanner.Watch`](../../../internal/library/scanner.go):
- On start, walk each root and `w.Add` every directory (not just the root).
- On a filesystem `Create` event for a directory, `w.Add` the new directory so
  newly-created subfolders become watched.
- The debounced handler already runs a full recursive `ScanOnce`, so any event
  re-indexes everything; no change to scan logic is needed.

De-dup behavior is unchanged. Add a short note (README and/or ADR 0002) that
byte-identical files collapse to a single library entry.

**Tests:** start `Watch` over a temp root, create a nested subdirectory, drop an
eligible file into it, and assert (poll/eventually, accounting for the 500 ms
debounce) that the store indexes it.

## Part B — Thumbnails

### B1. Generator — `internal/library/thumbnail.go` (new)

- `Thumbnailer` interface + `FFThumbnailer` default impl, mirroring the existing
  `Prober`/`FFProbe` pattern.
- Command: `ffmpeg -ss <t> -i <path> -frames:v 1 -vf scale=480:-2 -q:v 4 -y <out.jpg>`
  with `t = min(10s, 10% of duration)`.
- Skip generation when the title has no video stream (`Height == 0`).
- ffmpeg failure is non-fatal: log and continue (no thumbnail → UI placeholder).

### B2. Generation timing — eager at scan, lazy fallback

- [`Scanner.indexFile`](../../../internal/library/scanner.go): after `Upsert`,
  if `ThumbPath(cacheDir, contentID)` is absent, generate it. `Scanner` gains a
  `cacheDir` and a `Thumbnailer`, wired in [`cmd/node/main.go`](../../../cmd/node/main.go).
  The existing mtime cache means only new/changed files do work.
- Shared helper `ThumbPath(cacheDir, contentID) = filepath.Join(cacheDir, contentID, "thumb.jpg")`,
  used by scanner, catalog, and media so the location is defined once.
- Lazy fallback on first serve (B3) covers libraries indexed before this feature.

### B3. Local serving

- Extend [`bridge.handleStream`](../../../internal/bridge/bridge.go): when
  `name == "thumb.jpg"`, dispatch to a new `Streamer.Thumbnail(ctx, host, cid)`.
- `app.Node.Thumbnail` (local host only) uses the media engine: return the cached
  `thumb.jpg`, else lazily generate from the title's source path via the same
  `library` thumbnailer, cache, and return. Content-Type `image/jpeg`.
- Home page renders `<img src="/s/{token}/{self}/{cid}/thumb.jpg">` — browser-cached,
  no JSON bloat. (Peer thumbnails do not use this path; see B4.)

### B4. P2P delivery — inline in catalog metadata

- proto: add `bytes thumbnail = 14;` to `TitleMeta` in
  [`proto/peer/v1/peer.proto`](../../../proto/peer/v1/peer.proto); regenerate with
  `make proto`.
- [`catalog.Service.toMeta`](../../../internal/catalog/service.go) reads
  `ThumbPath(cacheDir, cid)` and embeds the bytes **only if present and ≤ 64 KB**
  (480px JPEGs are ~15–30 KB). `catalog.NewService` gains a `cacheDir` argument.
- [`app.toTitleViews`](../../../internal/app/control.go) converts received
  `m.GetThumbnail()` bytes into a `data:image/jpeg;base64,…` string on
  `bridge.TitleView.Thumbnail`.

### B5. Wire-up to the UI

- `bridge.TitleView` gains `Thumbnail string json:"thumbnail"` (data URL for peer
  entries; empty for local entries, where the UI builds the stream URL).
- [`TitleCard.vue`](../../../webui/app/components/TitleCard.vue): add optional
  `thumbnail?: string` prop. When set, render an `<img>` filling the poster area;
  keep the clapperboard icon as the placeholder and on-`error` fallback.
- [`LibraryPanel.vue`](../../../webui/app/components/LibraryPanel.vue) (local):
  pass `:thumbnail="/s/${token}/${self}/${t.contentId}/thumb.jpg"` (token from the
  bridge bootstrap).
- [`pages/peer/[id].vue`](../../../webui/app/pages/peer/[id].vue) (peer): pass
  `:thumbnail="t.thumbnail"`. Extend the local `TitleView` interfaces with the
  `thumbnail` field.

## Components & responsibilities

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `library.Thumbnailer`/`FFThumbnailer` | grab one frame → JPEG at a path | ffmpeg |
| `library.ThumbPath` | single source of truth for thumb location | — |
| `library.Scanner` | eager-generate thumb after indexing | Thumbnailer, cacheDir |
| `media.Engine.Thumbnail` | serve/lazy-generate local thumb | library thumbnailer, store, cacheDir |
| `catalog.Service.toMeta` | embed thumb bytes in TitleMeta (≤64 KB) | cacheDir |
| `app.Node.Thumbnail` | bridge Streamer impl (local) | media.Engine |
| `app.toTitleViews` | TitleMeta bytes → data-URL TitleView | — |
| `bridge.handleStream` | route `thumb.jpg` → Streamer.Thumbnail | — |
| `TitleCard.vue` | render `<img>` or icon placeholder | — |

## Testing strategy

- **library**: fake `Thumbnailer`/`Runner` — scanner generates to the correct
  path, skips when the file already exists, and skips video-less titles.
- **catalog**: `toMeta` with a temp cacheDir — embeds when present, omits when
  absent, omits when oversized.
- **app**: `toTitleViews` maps thumbnail bytes to a `data:` URL; empty bytes →
  empty string.
- **bridge**: `handleStream` dispatches `thumb.jpg` to `Streamer.Thumbnail` (fake
  streamer).
- **watcher**: nested-create reindex test (Part A).
- **webui**: vitest — `TitleCard` renders `<img>` when `thumbnail` is set, icon
  when not.

## Risks / prerequisites

- `make proto` needs `protoc` + `protoc-gen-go`. Verify at implementation time;
  if absent, document the install step.
- Watcher tests are timing-sensitive — use poll/eventually around the 500 ms
  debounce, not fixed sleeps.
- ffmpeg frame-grab adds per-new-file cost at scan time; acceptable since the scan
  already hashes the whole file and runs ffprobe, and the mtime cache bounds it.

## Out of scope

- Changing the content-hash identity model so duplicate files show separately
  (conflicts with ADR 0002).
- A separate on-demand P2P thumbnail fetch protocol (chose inline-in-catalog).
- Thumbnail regeneration UI, scrubbing previews, or animated posters.
