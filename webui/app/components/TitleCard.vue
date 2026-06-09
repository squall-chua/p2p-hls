<script setup lang="ts">
const props = defineProps<{
  title: string
  durationMs?: number
  live?: boolean
  viewers?: number
  thumbnail?: string
}>()

const failed = ref(false)

const duration = computed(() => {
  const ms = props.durationMs ?? 0
  if (ms <= 0) return ''
  const total = Math.round(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
})
</script>

<template>
  <div
    class="group flex flex-col overflow-hidden rounded-2xl border border-default bg-elevated shadow-sm transition duration-300 hover:-translate-y-0.5 hover:border-accented hover:shadow-lg hover:shadow-black/20"
  >
    <!-- poster -->
    <div class="poster-sheen relative aspect-video overflow-hidden">
      <img
        v-if="thumbnail && !failed"
        :src="thumbnail"
        alt=""
        loading="lazy"
        class="absolute inset-0 size-full object-cover"
        @error="failed = true"
      >
      <div v-else class="absolute inset-0 flex items-center justify-center">
        <UIcon
          name="i-lucide-clapperboard"
          class="size-9 text-dimmed transition-transform duration-300 group-hover:scale-110"
        />
      </div>

      <span
        v-if="live"
        class="absolute left-2.5 top-2.5 inline-flex items-center gap-1.5 rounded-full bg-error px-2 py-0.5 text-xs font-semibold text-white shadow-sm"
      >
        <span class="live-dot size-1.5 rounded-full bg-white" />
        Party · {{ viewers ?? 0 }}
      </span>

      <span
        v-if="duration"
        class="absolute bottom-2.5 right-2.5 rounded-md bg-black/65 px-1.5 py-0.5 text-xs font-medium tabular-nums text-white backdrop-blur-sm"
      >
        {{ duration }}
      </span>
    </div>

    <!-- info + actions -->
    <div class="flex flex-1 flex-col gap-3 p-3.5">
      <p class="truncate font-semibold text-highlighted" :title="title">{{ title }}</p>
      <div class="mt-auto flex items-center gap-2">
        <slot name="actions" />
      </div>
    </div>
  </div>
</template>
