<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

const bridge = useBridge()
const peers = ref<any[]>([])
const requests = ref<string[]>([])
const library = ref<any[]>([])

async function refetch(kind = 'all') {
  if (kind === 'all' || kind === 'presence') peers.value = await bridge.presence()
  if (kind === 'all' || kind === 'requests') requests.value = await bridge.requests()
  if (kind === 'all') library.value = await bridge.library()
}

let live: { start: () => void; stop: () => void } | null = null
onMounted(async () => {
  await refetch('all')
  live = useLiveData((k) => refetch(k))
  live.start()
})
onBeforeUnmount(() => live?.stop())
</script>

<template>
  <div class="min-h-screen bg-default">
    <header class="border-b border-default px-6 py-4">
      <div class="flex items-center gap-3">
        <UIcon name="i-lucide-radio-tower" class="size-5 text-primary" />
        <h1 class="text-lg font-semibold text-highlighted">P2P HLS</h1>
        <span class="ml-auto truncate text-sm text-muted">
          {{ bridge.name || bridge.nodeId || 'this node' }}
        </span>
      </div>
    </header>

    <main class="grid grid-cols-1 gap-4 p-6 lg:grid-cols-2">
      <UCard>
        <template #header>
          <div class="flex items-center gap-2">
            <UIcon name="i-lucide-users" class="size-4 text-muted" />
            <span class="font-semibold text-highlighted">Online peers</span>
          </div>
        </template>
        <PeerList :peers="peers" />
      </UCard>

      <UCard>
        <template #header>
          <div class="flex items-center gap-2">
            <UIcon name="i-lucide-inbox" class="size-4 text-muted" />
            <span class="font-semibold text-highlighted">Requests</span>
          </div>
        </template>
        <RequestList :requests="requests" @approved="refetch('requests')" />
      </UCard>

      <UCard>
        <template #header>
          <div class="flex items-center gap-2">
            <UIcon name="i-lucide-library" class="size-4 text-muted" />
            <span class="font-semibold text-highlighted">Your Library</span>
          </div>
        </template>
        <LibraryPanel :titles="library" />
      </UCard>

      <UCard>
        <template #header>
          <div class="flex items-center gap-2">
            <UIcon name="i-lucide-tv" class="size-4 text-muted" />
            <span class="font-semibold text-highlighted">Now watching</span>
          </div>
        </template>
        <p class="text-sm text-muted">Nothing yet</p>
      </UCard>
    </main>
  </div>
</template>
