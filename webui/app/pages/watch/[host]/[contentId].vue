<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { attachPlayer, type Role } from '~/lib/player'
import { formatDrift } from '~/lib/actuator'

const route = useRoute()
const bridge = useBridge()
const toast = useToast()
const host = route.params.host as string
const cid = route.params.contentId as string
const isSelf = host === bridge.nodeId
const role: Role = !isSelf ? 'viewer' : (route.query.party ? 'host' : 'solo')

const video = ref<HTMLVideoElement>()
const drift = ref(0)
let handle: { close: () => void } | null = null

onMounted(() => {
  handle = attachPlayer({
    video: video.value!,
    src: bridge.streamURL(host, cid),
    role,
    wsURL: bridge.partyWSURL(),
    onDrift: (d) => (drift.value = d),
  })
})
onBeforeUnmount(() => handle?.close())

async function leave() {
  try { await bridge.leaveParty() } finally { navigateTo('/') }
}
async function end() {
  try {
    await bridge.endParty()
    toast.add({ title: 'Party ended', icon: 'i-lucide-circle-stop', color: 'success' })
  } finally {
    navigateTo('/')
  }
}

const roleBadge = {
  host: { label: 'Hosting', color: 'primary', icon: 'i-lucide-radio' },
  viewer: { label: 'Watching', color: 'neutral', icon: 'i-lucide-eye' },
  solo: { label: 'Solo', color: 'neutral', icon: 'i-lucide-play' },
}[role] as { label: string; color: 'primary' | 'neutral'; icon: string }
</script>

<template>
  <div class="min-h-screen bg-default">
    <header class="border-b border-default px-6 py-4">
      <div class="flex items-center gap-3">
        <UButton
          to="/"
          icon="i-lucide-arrow-left"
          color="neutral"
          variant="ghost"
          size="sm"
          aria-label="Back to dashboard"
        />
        <UIcon name="i-lucide-monitor-play" class="size-5 text-primary" />
        <h1 class="truncate text-lg font-semibold text-highlighted">Watch</h1>
        <UBadge :color="roleBadge.color" :icon="roleBadge.icon" variant="subtle" size="sm">
          {{ roleBadge.label }}
        </UBadge>
        <span class="ml-auto truncate font-mono text-xs text-muted">{{ host }}</span>
      </div>
    </header>

    <main class="mx-auto max-w-4xl p-6">
      <div class="overflow-hidden rounded-lg border border-default bg-black shadow-sm">
        <video
          ref="video"
          :controls="role !== 'viewer'"
          class="aspect-video w-full bg-black"
        />
      </div>

      <div v-if="role === 'viewer'" class="mt-3 flex items-center gap-1.5 text-sm text-muted">
        <UIcon name="i-lucide-radio-tower" class="size-4 text-primary" />
        <span>Synced · {{ formatDrift(drift) }}</span>
      </div>

      <AudienceStrip v-if="role !== 'solo'" :host="host" :content-id="cid" />

      <div v-if="role !== 'solo'" class="mt-6">
        <UButton
          v-if="role === 'viewer'"
          label="Leave"
          icon="i-lucide-log-out"
          color="neutral"
          variant="soft"
          @click="leave"
        />
        <UButton
          v-else-if="role === 'host'"
          label="End party"
          icon="i-lucide-circle-stop"
          color="error"
          variant="soft"
          @click="end"
        />
      </div>
    </main>
  </div>
</template>
