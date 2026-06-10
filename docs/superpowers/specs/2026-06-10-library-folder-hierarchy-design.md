# Library Folder Hierarchy — Design

Date: 2026-06-10

## Problem

The web UI renders the library as a **flat** grid of cards on both the own
library page ([`index.vue`](../../../webui/app/pages/index.vue) →
[`LibraryPanel.vue`](../../../webui/app/components/LibraryPanel.vue)) and the peer
library page ([`pages/peer/[id].vue`](../../../webui/app/pages/peer/[id].vue)). It
should instead mirror the **actual folder hierarchy** of the shared folder(s) on
disk, so subdirectories appear as navigable folders.

## Decisions (confirmed with user)

- **Scope:** both the own library and peers' libraries get the hierarchy. (Peer
  views therefore require carrying the structure over the WebRTC control channel.)
- **Presentation:** a **drill-in folder browser** — click a folder to enter it, a
  breadcrumb shows the path. Keeps the existing thumbnail `TitleCard` grid, with
  folders shown as cards alongside titles.
- **Roots:** each configured shared root is a **separate top-level folder** (no
  merging of same-named folders across roots) — *except* when there is exactly
  **one** root, in which case the browser opens directly on that root's contents
  (auto-enter, no redundant single-folder click).

The folder hierarchy is encoded by each file's **directory relative to its shared
root**. That is the only new piece of data needed; the tree is derived from it.

## The `rel_dir` field

A title's `rel_dir` is the forward-slashed directory of its file, relative to the
matching shared root, with the rule:

- **One shared root** (`len(roots) == 1`): no prefix.
  `rel_dir` = dir relative to that root.
  - `/home/me/media/Movies/Action/x.mkv` → `Movies/Action`
  - `/home/me/media/y.mkv` → `""` (sits at the browser's top level → auto-entered)
- **Multiple shared roots** (`len(roots) > 1`): prefix with the root's label.
  - root `/home/me/media` (label `media`): `…/media/Movies/Action/x.mkv` → `media/Movies/Action`
  - a root-level file `…/media/y.mkv` → `media`
- **No matching root** (should not happen): `""`.

Because the rule keys off `len(roots)` — known authoritatively by the Service —
the "auto-enter single root" behavior is decided server-side and the UI stays
generic (split `rel_dir` on `/`, no special cases). A peer browsing us sees the
rule applied to **our** root count, so their view mirrors exactly what we share.

Root label = the root's **basename** (not its absolute path, so home-directory
paths don't leak to peers). When two roots share a basename, labels are
disambiguated with a numeric suffix (e.g. `media`, `media (2)`). The
root → label map is precomputed once in `NewService`.

## Part A — Proto

Add to `TitleMeta` in [`proto/peer/v1/peer.proto`](../../../proto/peer/v1/peer.proto):

```proto
string rel_dir = 15; // dir relative to the shared root; "" = root level
```

Regenerate with `make proto`.

## Part B — Go (catalog Service)

- [`catalog.NewService`](../../../internal/catalog/service.go) gains a
  `roots []string` argument and precomputes a `rootLabels map[string]string`
  (only populated/used when `len(roots) > 1`).
- New helper `relDirFor(path string, roots []string, labels map[string]string) string`
  implementing the rule above (find the root that prefixes `path`,
  `filepath.Rel` + `filepath.Dir` + `filepath.ToSlash`; `Dir` of a root-level
  file normalizes `.` → `""`).
- [`Service.toMeta`](../../../internal/catalog/service.go#L107) sets
  `m.RelDir = relDirFor(t.Path, …)`. `toMeta` already serves **both**
  `Library()` (own) and `Browse()` (peer), so both views get the field from one
  place.
- Wire `cfg.SharedFolders` into `NewService(...)` in
  [`cmd/node/main.go`](../../../cmd/node/main.go#L89).

## Part C — Bridge

- [`bridge.TitleView`](../../../internal/bridge/api.go#L29) gains a
  `RelDir` field with JSON tag `relDir`.
- [`app.toTitleViews`](../../../internal/app/control.go#L104) copies
  `m.GetRelDir()` → `TitleView.RelDir`. Identical code path for own (local
  `toMeta`) and peer (over-wire `TitleMeta` → `toTitleViews`).

## Part D — UI tree helper (pure, unit-tested)

A pure function in `webui/app/utils/` (e.g. `libraryTree.ts`). Given the flat
`TitleView[]` (each with `relDir`) and a current path (array of segments), it
returns:

- `folders: string[]` — immediate child folder names at the current path,
  alpha-sorted, de-duplicated.
- `titles: TitleView[]` — titles whose `relDir` equals exactly the current path,
  alpha-sorted by `displayTitle`.

This keeps all tree logic out of the component and lets it be unit-tested in
`webui/test/` per the existing vitest convention.

## Part E — `LibraryBrowser.vue` (new component)

- Prop: `titles: TitleView[]`.
- Internal state: `currentPath = ref<string[]>([])`.
- Renders:
  - a **breadcrumb** (`Library / Movies / Action`) whose crumbs are clickable to
    jump up any number of levels;
  - **folder cards** for `folders` at the current path → clicking pushes the
    segment onto `currentPath`;
  - the existing [`TitleCard`](../../../webui/app/components/TitleCard.vue) for
    each title at the current path.
- Folders sort first, then titles.
- Exposes a scoped slot `#actions="{ title }"` forwarded to `TitleCard`'s
  `#actions` slot, so each page injects its own buttons.
- Empty state when there are no folders and no titles.

## Part F — Wiring the two pages (reuse, no duplication)

- [`LibraryPanel.vue`](../../../webui/app/components/LibraryPanel.vue) (own)
  becomes a thin wrapper around `LibraryBrowser`, keeping its `startParty` logic
  and the `bridge.thumbURL(self, …)` thumbnail, supplying **Watch + Start party**
  actions via the slot. [`index.vue`](../../../webui/app/pages/index.vue) is
  unchanged (`<LibraryPanel :titles="library" />`).
- [`pages/peer/[id].vue`](../../../webui/app/pages/peer/[id].vue) replaces its
  inline grid with `LibraryBrowser`, supplying **Watch + Join (when live)**
  actions and the `t.thumbnail` data URL. Loading / denied / empty-request states
  are untouched. Both pages' local `TitleView` interfaces gain `relDir`.

## Components & responsibilities

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `catalog.relDirFor` | path + roots → `rel_dir` string | — |
| `catalog.NewService` | hold roots, precompute disambiguated labels | shared folders |
| `catalog.Service.toMeta` | set `RelDir` on every TitleMeta | relDirFor |
| `app.toTitleViews` | copy `RelDir` onto TitleView | — |
| `libraryTree.ts` | flat titles + path → child folders + titles | — |
| `LibraryBrowser.vue` | breadcrumb + drill-in nav + render | libraryTree, TitleCard |
| `LibraryPanel.vue` | own-library actions wrapper | LibraryBrowser |
| `pages/peer/[id].vue` | peer actions wrapper | LibraryBrowser |

## Testing strategy

- **catalog** (`service_test.go`): `relDirFor`/`toMeta` for single root (no
  prefix; nested + root-level), multiple roots (prefixed; basename collision
  disambiguation), and a path under no root (`""`).
- **webui** (`test/libraryTree.spec.ts`): child folders + titles at root, nested
  paths, root-level titles (`relDir === ""`), folders-first alpha sort,
  de-duplication of sibling folder names.
- **Manual**: `make build`, run a node over a shared folder with subdirectories;
  confirm drill-in + breadcrumb on the own page and on a peer page; confirm a
  single-root library auto-enters and a two-root library shows two top folders.

## Risks / prerequisites

- `make proto` needs `protoc` + `protoc-gen-go`. Verify at implementation time;
  if absent, document the install step.
- Folder navigation is component-local state (no URL routing for folders); a page
  refresh resets to the top level. Acceptable for a single-page library view.

## Out of scope

- Deep-linking / routing to a specific folder via URL.
- Showing item counts or sizes on folder cards (folder cards are name + icon).
- Recursive auto-enter beyond the single-root case (only the one configured root
  is auto-entered, not nested single-child folders).
- Any change to indexing, content-hash identity, or the on-disk layout.
