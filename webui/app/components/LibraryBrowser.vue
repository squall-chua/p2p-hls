<script setup lang="ts">
import { initialPath, childrenAt, titlesUnder, type TreeTitle } from '~/lib/libraryTree'

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

// Each Folder card shows its item count and a mosaic of the first few thumbnails
// of the Titles in its subtree (glyph fallback when none are available).
const folderCards = computed(() =>
  view.value.folders.map((name) => {
    const under = titlesUnder(props.titles, [...path.value, name])
    return {
      name,
      count: under.length,
      previews: under.map(thumbOf).filter(Boolean).slice(0, 4),
    }
  }),
)

// A missing/broken preview thumbnail just reveals the tinted cell behind it.
function onPreviewError(e: Event) {
  ;(e.target as HTMLImageElement).style.visibility = 'hidden'
}
</script>

<template>
  <div>
    <!-- breadcrumb -->
    <nav class="mb-4 flex flex-wrap items-center gap-0.5 text-sm">
      <template v-for="(crumb, i) in crumbs" :key="i">
        <button
          v-if="i < crumbs.length - 1"
          type="button"
          class="rounded px-1.5 py-0.5 text-muted transition hover:text-highlighted"
          @click="goTo(i)"
        >
          {{ crumb }}
        </button>
        <span v-else class="rounded px-1.5 py-0.5 font-semibold text-highlighted">{{ crumb }}</span>
        <UIcon v-if="i < crumbs.length - 1" name="i-lucide-chevron-right" class="size-3.5 text-dimmed" />
      </template>
    </nav>

    <div
      v-if="view.folders.length || view.titles.length"
      class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <!-- folders first -->
      <button
        v-for="f in folderCards"
        :key="'f:' + f.name"
        type="button"
        class="group flex flex-col overflow-hidden rounded-2xl border border-default bg-elevated text-left shadow-sm transition duration-300 hover:-translate-y-0.5 hover:border-accented hover:shadow-lg hover:shadow-black/20"
        @click="enter(f.name)"
      >
        <div class="poster-sheen relative aspect-video overflow-hidden">
          <!-- content mosaic of the folder's titles -->
          <div v-if="f.previews.length" class="grid size-full grid-cols-2 grid-rows-2 gap-0.5">
            <div v-for="i in 4" :key="i" class="relative overflow-hidden bg-primary/5">
              <img
                v-if="f.previews[i - 1]"
                :src="f.previews[i - 1]"
                alt=""
                loading="lazy"
                class="absolute inset-0 size-full object-cover transition-transform duration-300 group-hover:scale-105"
                @error="onPreviewError"
              >
            </div>
          </div>
          <!-- glyph fallback when no thumbnails are available -->
          <div v-else class="flex size-full items-center justify-center text-primary">
            <UIcon name="i-lucide-folder" class="size-9 transition-transform duration-300 group-hover:scale-110" />
          </div>
          <!-- folder affordance, so a mosaic never reads as a single title -->
          <span class="absolute left-2.5 top-2.5 inline-flex items-center gap-1 rounded-full bg-black/55 px-2 py-0.5 text-xs font-medium text-white backdrop-blur-sm">
            <UIcon name="i-lucide-folder" class="size-3" />
            Folder
          </span>
        </div>
        <div class="flex flex-1 flex-col gap-1 p-3.5">
          <p class="truncate font-semibold text-highlighted" :title="f.name">{{ f.name }}</p>
          <p class="text-xs text-muted">{{ f.count }} {{ f.count === 1 ? 'item' : 'items' }}</p>
        </div>
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
