# Library Folder Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render the web UI Library (own) and Catalog (peer) as a drill-in Folder browser that mirrors the on-disk directory structure of the Shared folder(s).

**Architecture:** The peer wire (`TitleMeta`) and the local Library JSON each carry two *raw* structural facts per Title — `rel_dir` (directory relative to its Shared-folder root) and `root_label` (disambiguated basename of that root). The Go catalog Service computes them; the browser builds and navigates the Folder tree entirely client-side from those facts (per ADR 0001's client-agnostic protocol). A single new `LibraryBrowser.vue` is reused by both pages.

**Tech Stack:** Go (protobuf/protoc, SQLite-backed catalog), Nuxt 4 client-only SPA (Vue 3, `@nuxt/ui`), vitest, testify.

**Spec:** [docs/superpowers/specs/2026-06-10-library-folder-hierarchy-design.md](../specs/2026-06-10-library-folder-hierarchy-design.md)

---

## File structure

| File | Create/Modify | Responsibility |
|------|---------------|----------------|
| `proto/peer/v1/peer.proto` | Modify | add `rel_dir`, `root_label` to `TitleMeta` |
| `proto/peer/v1/peer.pb.go` | Modify (generated) | regenerated getters |
| `internal/catalog/folder.go` | Create | `buildRootLabels`, `folderFor` (pure path logic) |
| `internal/catalog/folder_test.go` | Create | white-box unit tests for the above |
| `internal/catalog/service.go` | Modify | `Service` holds roots+labels; `toMeta` sets the fields |
| `internal/catalog/service_test.go` | Modify | update helper signature; add wiring test |
| `cmd/node/main.go` | Modify | pass `cfg.SharedFolders` to `NewService` |
| `internal/bridge/api.go` | Modify | `TitleView` gains `RelDir`, `RootLabel` |
| `internal/app/control.go` | Modify | `toTitleViews` copies the two fields |
| `internal/app/titleview_test.go` | Modify | assert the copy |
| `webui/app/lib/libraryTree.ts` | Create | pure tree helper (`initialPath`, `childrenAt`) |
| `webui/test/libraryTree.spec.ts` | Create | vitest for the helper |
| `webui/app/components/LibraryBrowser.vue` | Create | breadcrumb + drill-in nav + render |
| `webui/app/components/LibraryPanel.vue` | Modify | own-Library wrapper around `LibraryBrowser` |
| `webui/app/pages/peer/[id].vue` | Modify | peer-Catalog wrapper around `LibraryBrowser` |

---

## Task 1: Proto — add `rel_dir` and `root_label` to `TitleMeta`

**Files:**
- Modify: `proto/peer/v1/peer.proto:54-69`
- Modify (generated): `proto/peer/v1/peer.pb.go`

- [ ] **Step 1: Add the two fields to the `TitleMeta` message**

In `proto/peer/v1/peer.proto`, change the end of the `TitleMeta` message (currently ending at `thumbnail = 14`):

```proto
  bool party_live = 12;     // a live Watch Party exists for this Title on this Host
  int32 party_viewers = 13; // current Audience size (Viewers, excluding the Host)
  bytes thumbnail = 14;     // small poster JPEG (480px); empty when unavailable
  string rel_dir = 15;      // Title's dir relative to its Shared-folder root; "" = root level
  string root_label = 16;   // disambiguated basename of that Shared folder
}
```

- [ ] **Step 2: Regenerate the Go code**

Run: `make proto`
Expected: no output, exit 0 (regenerates `proto/peer/v1/peer.pb.go`).

- [ ] **Step 3: Verify the getters exist and everything builds**

Run: `grep -n "func (x \*TitleMeta) GetRelDir\|func (x \*TitleMeta) GetRootLabel" proto/peer/v1/peer.pb.go && go build ./...`
Expected: two matching lines printed, then a clean build (exit 0).

- [ ] **Step 4: Commit**

```bash
git add proto/peer/v1/peer.proto proto/peer/v1/peer.pb.go
git commit -m "proto: add rel_dir + root_label to TitleMeta"
```

---

## Task 2: catalog — pure Folder helpers (`buildRootLabels`, `folderFor`)

**Files:**
- Create: `internal/catalog/folder.go`
- Test: `internal/catalog/folder_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/folder_test.go` (white-box: `package catalog`, so it can call the unexported helpers):

```go
package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRootLabelsDisambiguatesCollisions(t *testing.T) {
	labels := buildRootLabels([]string{"/a/media", "/b/media", "/c/films"})
	require.Equal(t, "media", labels["/a/media"])
	require.Equal(t, "media (2)", labels["/b/media"])
	require.Equal(t, "films", labels["/c/films"])
}

func TestFolderForSingleRoot(t *testing.T) {
	roots := []string{"/srv/media"}
	labels := buildRootLabels(roots)

	rl, rd := folderFor("/srv/media/Movies/Action/x.mkv", roots, labels)
	require.Equal(t, "media", rl)
	require.Equal(t, "Movies/Action", rd)

	rl, rd = folderFor("/srv/media/y.mkv", roots, labels)
	require.Equal(t, "media", rl)
	require.Equal(t, "", rd)
}

func TestFolderForMultiRootPicksOwningRoot(t *testing.T) {
	roots := []string{"/a/media", "/b/media"}
	labels := buildRootLabels(roots)

	rl, rd := folderFor("/b/media/Shows/x.mkv", roots, labels)
	require.Equal(t, "media (2)", rl)
	require.Equal(t, "Shows", rd)
}

func TestFolderForNoMatch(t *testing.T) {
	roots := []string{"/srv/media"}
	labels := buildRootLabels(roots)
	rl, rd := folderFor("/other/x.mkv", roots, labels)
	require.Equal(t, "", rl)
	require.Equal(t, "", rd)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/catalog/ -run 'TestFolderFor|TestBuildRootLabels' -v`
Expected: FAIL — compile error `undefined: buildRootLabels` / `undefined: folderFor`.

- [ ] **Step 3: Write the implementation**

Create `internal/catalog/folder.go`:

```go
package catalog

import (
	"fmt"
	"path/filepath"
	"strings"
)

// buildRootLabels assigns each Shared-folder root a stable, human label: the
// root's basename, disambiguated with a numeric suffix (by config order) when
// two roots share a basename — e.g. ["/a/media","/b/media"] -> "media","media (2)".
func buildRootLabels(roots []string) map[string]string {
	labels := make(map[string]string, len(roots))
	seen := make(map[string]int)
	for _, root := range roots {
		base := filepath.Base(filepath.Clean(root))
		seen[base]++
		if seen[base] == 1 {
			labels[root] = base
		} else {
			labels[root] = fmt.Sprintf("%s (%d)", base, seen[base])
		}
	}
	return labels
}

// folderFor reports which Shared folder a Title's path belongs to (its label) and
// the Title's directory relative to that root, forward-slashed ("" at root level).
// A path under no root yields ("", "") — impossible by construction, since the
// scanner only indexes files found under a root. First matching root wins.
func folderFor(path string, roots []string, labels map[string]string) (rootLabel, relDir string) {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		dir := filepath.Dir(rel)
		if dir == "." {
			dir = ""
		}
		return labels[root], filepath.ToSlash(dir)
	}
	return "", ""
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/catalog/ -run 'TestFolderFor|TestBuildRootLabels' -v`
Expected: PASS (4 tests ok).

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/folder.go internal/catalog/folder_test.go
git commit -m "catalog: folderFor + buildRootLabels for Folder hierarchy"
```

---

## Task 3: catalog — wire the helpers into `Service` and `main.go`

**Files:**
- Modify: `internal/catalog/service.go:22-36` (struct + constructor) and `:107-137` (`toMeta`)
- Modify: `cmd/node/main.go:89`
- Test: `internal/catalog/service_test.go:15-30` (helper) + new test

- [ ] **Step 1: Write the failing wiring test**

Add to `internal/catalog/service_test.go` (black-box `package catalog_test`; needs the new `NewService` signature from Step 3):

```go
func TestLibraryReportsFolderForTitle(t *testing.T) {
	root := t.TempDir()
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, store.Upsert(library.Title{
		ContentID:    "cid-1",
		DisplayTitle: "Movie",
		Path:         filepath.Join(root, "Movies", "Action", "x.mkv"),
	}))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests(), t.TempDir(), []string{root})

	titles, err := svc.Library()
	require.NoError(t, err)
	require.Equal(t, "Movies/Action", titles[0].GetRelDir())
	require.Equal(t, filepath.Base(root), titles[0].GetRootLabel())
}
```

- [ ] **Step 2: Run to verify it fails (compile error)**

Run: `go test ./internal/catalog/ -run TestLibraryReportsFolderForTitle`
Expected: FAIL — compile error: `too many arguments in call to catalog.NewService` (the test passes 5 args; the current signature takes 4).

- [ ] **Step 3: Update `Service` struct + `NewService` (`internal/catalog/service.go`)**

Replace the `Service` struct (lines ~22-28) and `NewService` (lines ~34-36) with:

```go
// Service answers browse RPCs from Viewers, enforcing the access Policy.
// It implements peer.RequestHandler.
type Service struct {
	store      *library.Store
	policy     *Policy
	reqs       *Requests
	party      PartyProvider
	cacheDir   string
	roots      []string
	rootLabels map[string]string
}
```

```go
// NewService wires the Store, Policy, Requests, the cache dir thumbnails live
// under, and the Shared folder roots (used to derive each Title's Folder).
func NewService(store *library.Store, policy *Policy, reqs *Requests, cacheDir string, roots []string) *Service {
	return &Service{
		store: store, policy: policy, reqs: reqs, cacheDir: cacheDir,
		roots: roots, rootLabels: buildRootLabels(roots),
	}
}
```

- [ ] **Step 4: Set the fields in `toMeta` (`internal/catalog/service.go`)**

In `toMeta`, immediately after the `m := &peerv1.TitleMeta{...}` literal (before the `for _, sub := range t.Subtitles` loop), add:

```go
	m.RootLabel, m.RelDir = folderFor(t.Path, s.roots, s.rootLabels)
```

- [ ] **Step 5: Update the `main.go` call (`cmd/node/main.go:89`)**

Change:

```go
	catalogSvc := catalog.NewService(store, policy, catalog.NewRequests(), cacheDir)
```

to:

```go
	catalogSvc := catalog.NewService(store, policy, catalog.NewRequests(), cacheDir, cfg.SharedFolders)
```

- [ ] **Step 6: Update the existing test helper (`internal/catalog/service_test.go:29`)**

The shared `newServiceWithTitle` helper must compile against the new signature. Change its return line:

```go
	return catalog.NewService(store, policy, reqs, cache), policy, reqs, cache
```

to (no roots — its Title has no path, so `folderFor` returns `("","")`, leaving existing assertions unaffected):

```go
	return catalog.NewService(store, policy, reqs, cache, nil), policy, reqs, cache
```

- [ ] **Step 7: Run tests + build to verify pass**

Run: `go test ./internal/catalog/... && go build ./...`
Expected: PASS (all catalog tests, including the new wiring test) and a clean build.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/service.go internal/catalog/service_test.go cmd/node/main.go
git commit -m "catalog: report Folder (rel_dir + root_label) on every Title"
```

---

## Task 4: bridge — carry the fields onto `TitleView`

**Files:**
- Modify: `internal/bridge/api.go:29-36`
- Modify: `internal/app/control.go:104-121`
- Test: `internal/app/titleview_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/app/titleview_test.go`:

```go
func TestToTitleViewsCopiesFolderFields(t *testing.T) {
	views := toTitleViews([]*peerv1.TitleMeta{
		{ContentId: "a", RelDir: "Movies/Action", RootLabel: "media"},
	})
	require.Equal(t, "Movies/Action", views[0].RelDir)
	require.Equal(t, "media", views[0].RootLabel)
}
```

- [ ] **Step 2: Run to verify it fails (compile error)**

Run: `go test ./internal/app/ -run TestToTitleViewsCopiesFolderFields`
Expected: FAIL — compile error: `views[0].RelDir undefined (type bridge.TitleView has no field or method RelDir)`.

- [ ] **Step 3: Add the fields to `bridge.TitleView` (`internal/bridge/api.go`)**

Replace the `TitleView` struct with:

```go
type TitleView struct {
	ContentID    string `json:"contentId"`
	DisplayTitle string `json:"displayTitle"`
	DurationMs   int64  `json:"durationMs"`
	PartyLive    bool   `json:"partyLive"`
	PartyViewers int    `json:"partyViewers"`
	Thumbnail    string `json:"thumbnail"` // data: URL for peer entries; empty for own library (UI builds the stream URL)
	RelDir       string `json:"relDir"`    // Title's dir relative to its Shared-folder root; "" = root level
	RootLabel    string `json:"rootLabel"` // disambiguated basename of that Shared folder
}
```

- [ ] **Step 4: Copy them in `toTitleViews` (`internal/app/control.go`)**

In the `out = append(out, bridge.TitleView{...})` literal, add the two fields:

```go
		out = append(out, bridge.TitleView{
			ContentID:    m.GetContentId(),
			DisplayTitle: m.GetDisplayTitle(),
			DurationMs:   m.GetDurationMs(),
			PartyLive:    m.GetPartyLive(),
			PartyViewers: int(m.GetPartyViewers()),
			Thumbnail:    thumb,
			RelDir:       m.GetRelDir(),
			RootLabel:    m.GetRootLabel(),
		})
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/app/... ./internal/bridge/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bridge/api.go internal/app/control.go internal/app/titleview_test.go
git commit -m "bridge: expose relDir + rootLabel on TitleView JSON"
```

---

## Task 5: webui — pure Folder-tree helper

**Files:**
- Create: `webui/app/lib/libraryTree.ts`
- Test: `webui/test/libraryTree.spec.ts`

- [ ] **Step 1: Write the failing test**

Create `webui/test/libraryTree.spec.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { initialPath, childrenAt, type TreeTitle } from '../app/lib/libraryTree'

const t = (rootLabel: string, relDir: string, displayTitle: string): TreeTitle => ({ rootLabel, relDir, displayTitle })

describe('initialPath', () => {
  it('auto-enters the single root', () => {
    expect(initialPath([t('media', 'Movies', 'A'), t('media', '', 'B')])).toEqual(['media'])
  })
  it('stays at top with multiple roots', () => {
    expect(initialPath([t('media', '', 'A'), t('films', '', 'B')])).toEqual([])
  })
  it('returns [] for empty', () => {
    expect(initialPath([])).toEqual([])
  })
})

describe('childrenAt', () => {
  const titles = [
    t('media', 'Movies/Action', 'Mad Max'),
    t('media', 'Movies/Action', 'Die Hard'),
    t('media', 'Movies', 'Top Gun'),
    t('media', '', 'Home Video'),
  ]
  it('lists root-level folders and titles at [media]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media'])
    expect(folders).toEqual(['Movies'])
    expect(here.map((x) => x.displayTitle)).toEqual(['Home Video'])
  })
  it('shows subfolder once and the title at [media, Movies]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media', 'Movies'])
    expect(folders).toEqual(['Action'])
    expect(here.map((x) => x.displayTitle)).toEqual(['Top Gun'])
  })
  it('descends and sorts titles alpha at [media, Movies, Action]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media', 'Movies', 'Action'])
    expect(folders).toEqual([])
    expect(here.map((x) => x.displayTitle)).toEqual(['Die Hard', 'Mad Max'])
  })
  it('lists distinct roots alpha-sorted at []', () => {
    const multi = [t('media', '', 'A'), t('films', 'X', 'B')]
    expect(childrenAt(multi, []).folders).toEqual(['films', 'media'])
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd webui && npx vitest run libraryTree`
Expected: FAIL — cannot find module `../app/lib/libraryTree`.

- [ ] **Step 3: Write the implementation**

Create `webui/app/lib/libraryTree.ts`:

```ts
// A Title's place in the Folder tree, derived from the two raw wire facts. The
// tree is [rootLabel, ...relDir segments]; presentation lives entirely here.
export interface TreeTitle {
  rootLabel: string
  relDir: string
  displayTitle: string
}

// treeDir is the Folder path a Title lives in: its root, then each relDir segment.
function treeDir(t: TreeTitle): string[] {
  return [t.rootLabel, ...(t.relDir ? t.relDir.split('/') : [])]
}

// initialPath auto-enters the single Shared folder (one distinct rootLabel),
// else starts at the top listing each root as a Folder.
export function initialPath(titles: TreeTitle[]): string[] {
  const roots = new Set<string>()
  for (const t of titles) roots.add(t.rootLabel)
  return roots.size === 1 ? [[...roots][0]!] : []
}

// childrenAt returns the immediate child Folders (alpha, de-duplicated) and the
// Titles that sit exactly at `path` (alpha by displayTitle).
export function childrenAt<T extends TreeTitle>(titles: T[], path: string[]): { folders: string[]; titles: T[] } {
  const folders = new Set<string>()
  const here: T[] = []
  for (const t of titles) {
    const dir = treeDir(t)
    if (!startsWith(dir, path)) continue
    if (dir.length === path.length) here.push(t)
    else folders.add(dir[path.length]!)
  }
  return {
    folders: [...folders].sort((a, b) => a.localeCompare(b)),
    titles: here.sort((a, b) => a.displayTitle.localeCompare(b.displayTitle)),
  }
}

// startsWith reports whether dir has path as a prefix (dir at or below path).
function startsWith(dir: string[], path: string[]): boolean {
  if (dir.length < path.length) return false
  for (let i = 0; i < path.length; i++) if (dir[i] !== path[i]) return false
  return true
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd webui && npx vitest run libraryTree`
Expected: PASS (8 tests).

- [ ] **Step 5: Commit**

```bash
git add webui/app/lib/libraryTree.ts webui/test/libraryTree.spec.ts
git commit -m "webui: pure Folder-tree helper (initialPath, childrenAt)"
```

---

## Task 6: webui — `LibraryBrowser.vue` (drill-in browser)

**Files:**
- Create: `webui/app/components/LibraryBrowser.vue`

No unit test — the repo has no component unit tests; the pure logic is covered by Task 5 and compilation is verified by the build. (`ref`/`computed`/`watch`/`UButton`/`UIcon`/`TitleCard` are Nuxt auto-imports.)

- [ ] **Step 1: Create the component**

Create `webui/app/components/LibraryBrowser.vue`:

```vue
<script setup lang="ts">
import { initialPath, childrenAt, type TreeTitle } from '~/lib/libraryTree'

interface BrowserTitle extends TreeTitle {
  contentId: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
  thumbnail?: string
}

const props = defineProps<{
  titles: BrowserTitle[]
  baseLabel: string
  thumbnailFor?: (t: BrowserTitle) => string
}>()

const path = ref<string[]>(initialPath(props.titles))

// Auto-enter the single root once Titles first arrive (they load async), but do
// NOT reset the user's navigation on later live-refetches of the same library.
let pinned = props.titles.length > 0
watch(
  () => props.titles,
  (next) => {
    if (!pinned && next.length) {
      path.value = initialPath(next)
      pinned = true
    }
  },
)

const view = computed(() => childrenAt(props.titles, path.value))
const crumbs = computed(() => [props.baseLabel, ...path.value])

function goTo(index: number) {
  // crumb 0 is the base label (empty path); deeper crumbs slice the path.
  path.value = path.value.slice(0, index)
}
function enter(folder: string) {
  path.value = [...path.value, folder]
}
function thumbOf(t: BrowserTitle): string {
  return props.thumbnailFor ? props.thumbnailFor(t) : (t.thumbnail ?? '')
}
</script>

<template>
  <div>
    <!-- breadcrumb -->
    <nav class="mb-4 flex flex-wrap items-center gap-0.5 text-sm">
      <template v-for="(crumb, i) in crumbs" :key="i">
        <button
          type="button"
          class="rounded px-1.5 py-0.5 transition"
          :class="i === crumbs.length - 1 ? 'font-semibold text-highlighted' : 'text-muted hover:text-highlighted'"
          @click="goTo(i)"
        >
          {{ crumb }}
        </button>
        <UIcon v-if="i < crumbs.length - 1" name="i-lucide-chevron-right" class="size-3.5 text-dimmed" />
      </template>
    </nav>

    <div
      v-if="view.folders.length || view.titles.length"
      class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <!-- folders first -->
      <button
        v-for="folder in view.folders"
        :key="'f:' + folder"
        type="button"
        class="group flex items-center gap-3 rounded-2xl border border-default bg-elevated p-4 text-left transition duration-300 hover:-translate-y-0.5 hover:border-accented hover:shadow-lg hover:shadow-black/20"
        @click="enter(folder)"
      >
        <div class="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/15">
          <UIcon name="i-lucide-folder" class="size-5 transition-transform duration-300 group-hover:scale-110" />
        </div>
        <span class="truncate font-semibold text-highlighted">{{ folder }}</span>
      </button>

      <!-- then titles -->
      <TitleCard
        v-for="t in view.titles"
        :key="t.contentId"
        :title="t.displayTitle"
        :duration-ms="t.durationMs"
        :live="t.partyLive"
        :viewers="t.partyViewers"
        :thumbnail="thumbOf(t)"
      >
        <template #actions>
          <slot name="actions" :title="t" />
        </template>
      </TitleCard>
    </div>

    <slot v-else name="empty" />
  </div>
</template>
```

- [ ] **Step 2: Verify it compiles**

Run: `cd webui && npm run build`
Expected: build succeeds (exit 0) with no Vue/TS errors referencing `LibraryBrowser.vue`.

- [ ] **Step 3: Commit**

```bash
git add webui/app/components/LibraryBrowser.vue
git commit -m "webui: LibraryBrowser drill-in Folder component"
```

---

## Task 7: webui — own Library uses `LibraryBrowser`

**Files:**
- Modify: `webui/app/components/LibraryPanel.vue` (full rewrite)

- [ ] **Step 1: Rewrite `LibraryPanel.vue`**

Replace the entire file with (keeps `startParty` + self-resolution; delegates layout to `LibraryBrowser`; supplies own-Library thumbnail via `bridge.thumbURL`):

```vue
<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'

interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
  relDir: string
  rootLabel: string
  thumbnail?: string
}

defineProps<{ titles: TitleView[] }>()

const bridge = useBridge()
const toast = useToast()
const self = ref(bridge.nodeId)
const starting = ref<Set<string>>(new Set())

onMounted(async () => {
  self.value = (await bridge.resolveSelf()).nodeId
})

const thumbnailFor = (t: TitleView) => (self.value ? bridge.thumbURL(self.value, t.contentId) : '')

async function startParty(contentId: string) {
  starting.value = new Set(starting.value).add(contentId)
  try {
    const sid = self.value || (await bridge.resolveSelf()).nodeId
    await bridge.startParty(contentId)
    toast.add({ title: 'Party started', icon: 'i-lucide-party-popper', color: 'success' })
    await navigateTo(`/watch/${sid}/${contentId}?party=1`)
  } catch {
    toast.add({ title: 'Could not start party', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    const next = new Set(starting.value)
    next.delete(contentId)
    starting.value = next
  }
}
</script>

<template>
  <LibraryBrowser :titles="titles" base-label="Library" :thumbnail-for="thumbnailFor">
    <template #actions="{ title: t }">
      <UButton
        :to="`/watch/${self}/${t.contentId}`"
        label="Watch"
        icon="i-lucide-play"
        color="neutral"
        variant="soft"
        size="sm"
        class="flex-1 justify-center"
      />
      <UButton
        label="Start party"
        icon="i-lucide-users"
        color="primary"
        variant="solid"
        size="sm"
        class="flex-1 justify-center"
        :loading="starting.has(t.contentId)"
        @click="startParty(t.contentId)"
      />
    </template>
    <template #empty>
      <div class="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-default px-6 py-14 text-center">
        <div class="flex size-11 items-center justify-center rounded-full bg-muted text-dimmed">
          <UIcon name="i-lucide-film" class="size-5" />
        </div>
        <div class="space-y-1">
          <p class="font-medium text-highlighted">Your library is empty</p>
          <p class="text-sm text-muted">Shared titles you can watch or host will show up here.</p>
        </div>
      </div>
    </template>
  </LibraryBrowser>
</template>
```

- [ ] **Step 2: Verify it compiles**

Run: `cd webui && npm run build`
Expected: build succeeds (exit 0).

- [ ] **Step 3: Commit**

```bash
git add webui/app/components/LibraryPanel.vue
git commit -m "webui: own Library renders via LibraryBrowser"
```

---

## Task 8: webui — peer Catalog uses `LibraryBrowser`

**Files:**
- Modify: `webui/app/pages/peer/[id].vue` (TitleView interface ~lines 4-11; catalog `<template v-else>` block ~lines 161-212)

- [ ] **Step 1: Extend the local `TitleView` interface**

In `webui/app/pages/peer/[id].vue`, replace the `interface TitleView { ... }` block with:

```ts
interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
  relDir: string
  rootLabel: string
  thumbnail: string
}
```

- [ ] **Step 2: Replace the catalog render block**

Replace the entire `<!-- catalog -->` `<template v-else> ... </template>` block (the one containing the `v-if="titles.length"` grid and its empty-state `v-else`) with:

```vue
    <!-- catalog -->
    <template v-else>
      <LibraryBrowser :titles="titles" base-label="Catalog">
        <template #actions="{ title: t }">
          <UButton
            :to="`/watch/${id}/${t.contentId}`"
            label="Watch"
            icon="i-lucide-play"
            color="neutral"
            variant="soft"
            size="sm"
            class="flex-1 justify-center"
          />
          <UButton
            v-if="t.partyLive"
            label="Join"
            icon="i-lucide-users"
            color="primary"
            variant="solid"
            size="sm"
            class="flex-1 justify-center"
            :loading="joining === t.contentId"
            @click="join(t.contentId)"
          />
        </template>
        <template #empty>
          <div class="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-default px-6 py-14 text-center">
            <div class="flex size-11 items-center justify-center rounded-full bg-muted text-dimmed">
              <UIcon name="i-lucide-folder-open" class="size-5" />
            </div>
            <div class="space-y-1">
              <p class="font-medium text-highlighted">No titles shared</p>
              <p class="text-sm text-muted">This peer hasn't shared anything you can watch yet.</p>
            </div>
          </div>
        </template>
      </LibraryBrowser>
    </template>
```

(Peer Titles carry their own `thumbnail` data URL, so no `thumbnail-for` is passed — `LibraryBrowser` falls back to `t.thumbnail`.)

- [ ] **Step 3: Verify it compiles**

Run: `cd webui && npm run build`
Expected: build succeeds (exit 0).

- [ ] **Step 4: Commit**

```bash
git add "webui/app/pages/peer/[id].vue"
git commit -m "webui: peer Catalog renders via LibraryBrowser"
```

---

## Task 9: Full verification

**Files:** none (verification + embedded bundle rebuild)

- [ ] **Step 1: Run the whole Go suite**

Run: `make test`
Expected: PASS across all packages.

- [ ] **Step 2: Run the whole webui unit suite**

Run: `cd webui && npx vitest run`
Expected: PASS (existing suites + `libraryTree`).

- [ ] **Step 3: Rebuild the embedded UI bundle + binary**

Run: `make webui && make build`
Expected: `make webui` regenerates `internal/bridge/dist`; `make build` produces `bin/node` and `bin/signal-server`, exit 0.

- [ ] **Step 4: Manual smoke (drill-in + auto-enter)**

Create a Shared folder with subdirectories and at least one video, e.g.:
```
/tmp/p2p-demo/Movies/Action/<some>.mp4
/tmp/p2p-demo/Home/<some>.mp4
```
Configure a node's `shared_folders = ["/tmp/p2p-demo"]`, run `bin/node`, open the printed UI URL, and confirm:
- the Library auto-enters the single root (top level shows Folders `Movies`, `Home`, not a single `p2p-demo` Folder);
- clicking `Movies` → `Action` drills in, the breadcrumb (`Library / Movies / Action`) navigates back, and Titles render with Watch / Start party;
- a peer node granted access shows the same structure under a `Catalog` breadcrumb.

- [ ] **Step 5: Final commit (only if `make webui` produced tracked changes)**

`internal/bridge/dist` is a gitignored placeholder bundle (ADR 0006 / recent commits), so `make webui` normally leaves the tree clean. Confirm and commit only if needed:
```bash
git status --short
# if and only if tracked files changed:
# git add -A && git commit -m "chore: rebuild webui bundle for Folder browser"
```

---

## Notes for the implementer

- **Known behavior (do not 'fix'):** byte-identical Titles share a Content ID and appear in exactly one Folder (ADR 0002); directories with no Titles beneath them never appear. Both are by design — see the spec.
- **Security:** the Catalog now reveals Folder/Shared-folder names to approved Viewers. This is intended and documented; do not add per-Folder filtering.
- **Out of scope:** URL routing to Folders, item counts on Folder cards, renaming the peer page's "Peer library" heading.
