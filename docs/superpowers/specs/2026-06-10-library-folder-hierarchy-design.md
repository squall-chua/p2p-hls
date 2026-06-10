# Library Folder Hierarchy — Design

Date: 2026-06-10

## Problem

The web UI renders the library as a **flat** grid of cards on both the own
[Library](../../../CONTEXT.md) page ([`index.vue`](../../../webui/app/pages/index.vue) →
[`LibraryPanel.vue`](../../../webui/app/components/LibraryPanel.vue)) and the peer
[Catalog](../../../CONTEXT.md) page ([`pages/peer/[id].vue`](../../../webui/app/pages/peer/[id].vue)).
It should instead mirror the **actual folder hierarchy** of the Shared folder(s)
on disk, so subdirectories appear as navigable **Folders** (see CONTEXT.md).

## Decisions (confirmed with user; grilled 2026-06-10)

- **Scope:** both the own Library and peers' Catalogs get the hierarchy.
- **Presentation:** a **drill-in Folder browser** — click a Folder to enter it; a
  breadcrumb shows the path. Keeps the existing thumbnail `TitleCard` grid, with
  Folders shown as cards alongside Titles.
- **Layering (key decision):** the wire carries **faithful structure**, and the
  **client owns all presentation**. `TitleMeta` gains two *raw* fields rather than
  one field with UI policy baked in. This honors **ADR 0001** ("the protocol stays
  client-agnostic... a browser guest can consume the same RPCs") and *simplifies*
  the server — `toMeta` reports facts, no `len(roots)` branching.
- **Roots → top-level Folders:** with **one** Shared folder the browser auto-enters
  it (its subdirectories are the top level); with **several**, each Shared folder is
  a separate top-level Folder. This auto-enter choice is a *client* decision,
  inferred from the data (exactly one distinct `root_label` present).
- **Duplicate Titles:** one Title, one path, one Folder (see Known behavior).
- **Faithfulness:** Folders with no Title beneath them are omitted (see Known
  behavior).

## The two fields

For every Title the Catalog/Library now reports:

- **`rel_dir`** — the Title's directory **relative to its Shared-folder root**,
  forward-slashed, *raw* (no root prefix). `""` when the Title sits directly in the
  root.
  - root `/home/me/media`, file `…/media/Movies/Action/x.mkv` → `Movies/Action`
  - root `/home/me/media`, file `…/media/y.mkv` → `""`
- **`root_label`** — a stable, human label identifying *which* Shared folder the
  Title belongs to: the root's **basename** (not its absolute path, so home-dir
  paths don't leak). When two roots share a basename they are disambiguated with a
  numeric suffix by config order (e.g. `media`, `media (2)`). The root → label map
  is precomputed once in `NewService`.

The client composes a Title's tree location as `[root_label, ...rel_dir segments]`
and derives the whole Folder tree from that. Because the wire is raw, the same
client tree-builder serves both the own Library (local REST) and a peer Catalog
(over-wire `TitleMeta`), with no server-side asymmetry.

## Part A — Proto

Add to `TitleMeta` in [`proto/peer/v1/peer.proto`](../../../proto/peer/v1/peer.proto):

```proto
string rel_dir    = 15; // Title's dir relative to its Shared-folder root; "" = root level
string root_label = 16; // disambiguated basename of that Shared folder
```

Regenerate with `make proto`.

## Part B — Go (catalog Service)

- [`catalog.NewService`](../../../internal/catalog/service.go) gains a
  `roots []string` argument and precomputes `rootLabels map[string]string`
  (root path → unique label; basename, numeric-suffix on collision).
- New helper `folderFor(path string, roots []string, labels map[string]string) (rootLabel, relDir string)`:
  find the root that prefixes `path`; `relDir = filepath.ToSlash(filepath.Dir(filepath.Rel(root, path)))`
  with `.` normalized to `""`; `rootLabel = labels[root]`. A path under no root
  (impossible by construction — the scanner only indexes files under roots) yields
  `("", "")`.
- [`Service.toMeta`](../../../internal/catalog/service.go#L107) sets `m.RelDir` and
  `m.RootLabel`. `toMeta` already serves **both** `Library()` (own) and `Browse()`
  (peer), so both views are covered from one place. **No** `len(roots)` branching.
- Wire `cfg.SharedFolders` into `NewService(...)` in
  [`cmd/node/main.go`](../../../cmd/node/main.go#L89).

## Part C — Bridge

- [`bridge.TitleView`](../../../internal/bridge/api.go#L29) gains `RelDir`
  (`json:"relDir"`) and `RootLabel` (`json:"rootLabel"`).
- [`app.toTitleViews`](../../../internal/app/control.go#L104) copies
  `m.GetRelDir()` / `m.GetRootLabel()`. Identical code path for own (local
  `toMeta`) and peer (over-wire `TitleMeta` → `toTitleViews`).

## Part D — UI tree helper (pure, unit-tested)

A pure module in `webui/app/utils/` (e.g. `libraryTree.ts`). A Title's tree
location is `[rootLabel, ...(relDir ? relDir.split('/') : [])]`. The helper
exposes:

- `initialPath(titles)` → the starting `currentPath`: `[theRootLabel]` when exactly
  **one** distinct `rootLabel` is present (auto-enter the single Shared folder),
  else `[]`.
- `childrenAt(titles, path)` → `{ folders: string[], titles: TitleView[] }`:
  `folders` = distinct next-segment names of Titles whose tree location strictly
  descends `path` (alpha-sorted, de-duplicated); `titles` = Titles whose tree
  location *equals* `path` (alpha-sorted by `displayTitle`).

All tree logic lives here (not in the component) and is unit-tested in
`webui/test/` per the existing vitest convention.

## Part E — `LibraryBrowser.vue` (new component)

- Props: `titles: TitleView[]`, `baseLabel: string` (breadcrumb home crumb —
  `"Library"` for own, `"Catalog"` for peer).
- State: `currentPath = ref(initialPath(props.titles))`.
- Renders:
  - a **breadcrumb** (`baseLabel / Movies / Action`) whose crumbs (and the home
    crumb → `[]`) are clickable to jump up any number of levels;
  - **Folder cards** for `childrenAt(...).folders` → click pushes the segment;
  - the existing [`TitleCard`](../../../webui/app/components/TitleCard.vue) for each
    Title in `childrenAt(...).titles`.
- Folders sort first, then Titles.
- Exposes a scoped slot `#actions="{ title }"` forwarded to `TitleCard`'s
  `#actions`, so each page injects its own buttons.
- Empty state when a level has no Folders and no Titles.

## Part F — Wiring the two pages (reuse, no duplication)

- [`LibraryPanel.vue`](../../../webui/app/components/LibraryPanel.vue) (own) becomes
  a thin wrapper around `LibraryBrowser` (`baseLabel="Library"`), keeping its
  `startParty` logic and the `bridge.thumbURL(self, …)` thumbnail, supplying
  **Watch + Start party** actions via the slot.
  [`index.vue`](../../../webui/app/pages/index.vue) is unchanged.
- [`pages/peer/[id].vue`](../../../webui/app/pages/peer/[id].vue) replaces its inline
  grid with `LibraryBrowser` (`baseLabel="Catalog"`), supplying **Watch + Join
  (when live)** actions and the `t.thumbnail` data URL. Loading / denied /
  empty-request states are untouched. Both pages' local `TitleView` interfaces gain
  `relDir` and `rootLabel`.

## Components & responsibilities

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `catalog.folderFor` | path + roots → (`rootLabel`, `relDir`) facts | — |
| `catalog.NewService` | hold roots, precompute disambiguated labels | shared folders |
| `catalog.Service.toMeta` | set `RelDir` + `RootLabel` on every TitleMeta | folderFor |
| `app.toTitleViews` | copy both fields onto TitleView | — |
| `libraryTree.ts` | titles → initial path, children at a path | — |
| `LibraryBrowser.vue` | breadcrumb + drill-in nav + render | libraryTree, TitleCard |
| `LibraryPanel.vue` | own-Library actions wrapper | LibraryBrowser |
| `pages/peer/[id].vue` | peer-Catalog actions wrapper | LibraryBrowser |

## Known behavior (documented, by design)

- **Duplicate Titles (ADR 0002):** byte-identical files share a Content ID and
  collapse to one Title whose stored `path` is the lexically-last copy the scan
  wrote. The Folder tree therefore shows such a Title in exactly **one** Folder; a
  copy living in another Folder is not separately represented. Keeping
  one-Title-one-path-one-Folder is intentional — multi-Folder placement would mean
  tracking all paths per Content ID, fighting ADR 0002's identity model.
- **Empty / video-less directories omitted:** a Folder appears only if it holds at
  least one Title somewhere beneath it. Intermediate Folders on the path to a Title
  are kept; directories with no eligible video anywhere below them never appear.
  Representing them would require the scanner to index directories, not just Titles.

## Security note

Access is **all-or-nothing per Node** ([`Service.Browse`](../../../internal/catalog/service.go)):
an approved Viewer receives the entire Catalog. With this feature the Catalog also
carries each Title's `root_label` (Shared-folder basename) and subfolder names, so
an approved Viewer sees the Host's full Folder structure (e.g. a Folder named
`Unreleased screeners`). This is the same category of exposure as the already-shared
cleaned filenames, to the same already-trusted Viewers. The control for hiding a
Folder's name remains the existing one: don't put it in a Shared folder, or don't
grant that Node access. Per-Folder access control is **out of scope**.

## Testing strategy

- **catalog** (`service_test.go`): `folderFor`/`toMeta` for a nested path, a
  root-level path (`relDir == ""`), multiple roots (correct `rootLabel` per root),
  basename collision (numeric-suffix disambiguation), and a path under no root
  (`("", "")`).
- **webui** (`test/libraryTree.spec.ts`): `initialPath` auto-enters on one distinct
  `rootLabel` and stays at `[]` on several; `childrenAt` returns correct
  Folders/Titles at root, nested, and root-level (`relDir === ""`); folders-first
  alpha sort; sibling-Folder de-duplication.
- **Manual** (after `make webui` + `make build`): run a Node over a Shared folder
  with subdirectories; confirm drill-in + breadcrumb on the own page and on a peer
  page; confirm a single-root library auto-enters and a two-root config shows two
  top-level Folders.

## Risks / prerequisites

- `make proto` needs `protoc` + `protoc-gen-go`. Verify at implementation time; if
  absent, document the install step.
- UI changes only appear in the served binary after `make webui` rebuilds the
  embedded bundle (the tracked bundle is a placeholder; ADR 0006 / recent commits).
- Folder navigation is component-local state (no URL routing for Folders); a page
  refresh resets to the top level. Acceptable for a single-page view.

## Out of scope

- Deep-linking / routing to a specific Folder via URL.
- Item counts or sizes on Folder cards (Folder cards are name + icon).
- Recursive auto-enter beyond the single-root case.
- Representing empty / video-less directories, or multi-Folder placement of
  duplicate Titles.
- Per-Folder access control, and renaming the peer page's "Peer library" heading to
  "Catalog".
- Any change to indexing, content-hash identity, or the on-disk layout.
