<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

defineProps<{ host: string; contentId: string }>()

const bridge = useBridge()
const members = ref<any[]>([])

async function refetch() {
  try { members.value = await bridge.audience() } catch { /* ignore */ }
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
  <div class="mt-4 flex flex-wrap items-center gap-2">
    <span class="flex items-center gap-1.5 text-sm text-muted">
      <UIcon name="i-lucide-users" class="size-4" />
      {{ members.length }} watching
    </span>
    <UBadge
      v-for="m in members"
      :key="m.nodeId"
      color="neutral"
      variant="soft"
      size="sm"
    >
      {{ m.displayName || m.nodeId }}
    </UBadge>
  </div>
</template>
