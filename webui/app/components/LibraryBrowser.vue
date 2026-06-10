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
        v-for="folder in view.folders"
        :key="'f:' + folder"
        type="button"
        class="group flex items-center gap-3 rounded-2xl border border-default bg-elevated p-4 text-left transition duration-300 hover:-translate-y-0.5 hover:border-accented hover:shadow-lg hover:shadow-black/20"
        @click="enter(folder)"
      >
        <div class="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/15">
          <UIcon name="i-lucide-folder" class="size-5 transition-transform duration-300 group-hover:scale-110" />
        </div>
        <span class="truncate font-semibold text-highlighted" :title="folder">{{ folder }}</span>
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
