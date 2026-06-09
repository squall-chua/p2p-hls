<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

defineProps<{ host: string; contentId: string }>()

const bridge = useBridge()
const members = ref<any[]>([])

async function refetch() {
  try { members.value = await bridge.audience() } catch { /* ignore */ }
}

function initials(m: any): string {
  const n = String(m.displayName || m.nodeId || '?').trim()
  return (n[0] ?? '?').toUpperCase()
}

let live: { start: () => void; stop: () => void } | null = null

onMounted(() => {
  refetch()
  live = useLiveData((k) => { if (k === 'audience') refetch() })
  live.start()
})
onBeforeUnmount(() => live?.stop())
</script>

<template>
  <div class="mt-6 rounded-2xl border border-default bg-elevated p-4 sm:p-5">
    <div class="mb-3 flex items-center gap-2">
      <UIcon name="i-lucide-users" class="size-4 text-primary" />
      <span class="text-sm font-medium text-highlighted">{{ members.length }} watching</span>
    </div>

    <div v-if="members.length" class="flex flex-wrap items-center gap-2">
      <span
        v-for="m in members"
        :key="m.nodeId"
        class="inline-flex items-center gap-2 rounded-full border border-muted bg-muted/50 py-1 pl-1 pr-3"
      >
        <UAvatar :text="initials(m)" size="2xs" :ui="{ root: 'bg-primary/15 text-primary', fallback: 'font-semibold' }" />
        <span class="max-w-[10rem] truncate text-sm text-default">{{ m.displayName || m.nodeId }}</span>
      </span>
    </div>
    <p v-else class="text-sm text-muted">Waiting for viewers to join…</p>
  </div>
</template>
