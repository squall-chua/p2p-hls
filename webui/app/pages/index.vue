<script setup lang="ts">
import { useBridge, type CurrentParty } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

const bridge = useBridge()
const me = ref(bridge.name || bridge.nodeId)
const peers = ref<any[]>([])
const requests = ref<string[]>([])
const library = ref<any[]>([])
const watching = ref<CurrentParty | null>(null)

async function refetch(kind = 'all') {
  if (kind === 'all' || kind === 'presence') peers.value = await bridge.presence()
  if (kind === 'all' || kind === 'requests') requests.value = await bridge.requests()
  if (kind === 'all') library.value = await bridge.library()
  if (kind === 'all' || kind === 'now-watching') watching.value = await bridge.currentParty()
}

const watchingLink = computed(() => {
  const w = watching.value
  if (!w?.active) return ''
  return `/watch/${w.host}/${w.contentId}${w.role === 'host' ? '?party=1' : ''}`
})

let live: { start: () => void; stop: () => void } | null = null
onMounted(async () => {
  const s = await bridge.resolveSelf()
  me.value = s.displayName || s.nodeId
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
          {{ me || 'this node' }}
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
        <div
          v-if="watching?.active"
          class="flex items-center justify-between gap-3 rounded-md bg-elevated/50 px-3 py-2"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="truncate font-medium text-highlighted">
                {{ watching.title || watching.contentId }}
              </span>
              <UBadge
                :color="watching.role === 'host' ? 'primary' : 'neutral'"
                variant="subtle"
                size="sm"
              >
                {{ watching.role === 'host' ? 'Hosting' : 'Watching' }} · {{ watching.viewers }}
              </UBadge>
            </div>
            <span class="truncate font-mono text-xs text-dimmed">{{ watching.host }}</span>
          </div>
          <UButton
            :to="watchingLink"
            label="Resume"
            icon="i-lucide-play"
            color="primary"
            variant="soft"
            size="sm"
            class="shrink-0"
          />
        </div>
        <p v-else class="text-sm text-muted">Nothing yet</p>
      </UCard>
    </main>
  </div>
</template>
