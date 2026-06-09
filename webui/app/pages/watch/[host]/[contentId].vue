<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'
import { attachPlayer, type Role } from '~/lib/player'
import { formatDrift } from '~/lib/actuator'

definePageMeta({ layout: 'theater' })

const route = useRoute()
const bridge = useBridge()
const toast = useToast()
const host = route.params.host as string
const cid = route.params.contentId as string
// role depends on whether host is this node; in dev the bootstrap nodeId is
// empty, so resolve self async in onMounted before deciding the role.
const role = ref<Role>('solo')

const video = ref<HTMLVideoElement>()
const drift = ref(0)
let handle: { close: () => void } | null = null
let live: { start: () => void; stop: () => void } | null = null

onMounted(async () => {
  const isSelf = host === (await bridge.resolveSelf()).nodeId
  role.value = !isSelf ? 'viewer' : (route.query.party ? 'host' : 'solo')
  handle = attachPlayer({
    video: video.value!,
    src: bridge.streamURL(host, cid),
    role: role.value,
    wsURL: bridge.partyWSURL(),
    onDrift: (d) => (drift.value = d),
  })
  if (role.value === 'viewer') {
    live = useLiveData(() => {}, (type) => {
      if (type === 'party-ended') {
        toast.add({ title: 'The host ended the party' })
        navigateTo('/')
      }
    })
    live.start()
  }
})
onBeforeUnmount(() => {
  handle?.close()
  live?.stop()
})

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

const roleBadge = computed(() => ({
  host: { label: 'Hosting', color: 'error', icon: 'i-lucide-radio' },
  viewer: { label: 'Watching', color: 'primary', icon: 'i-lucide-eye' },
  solo: { label: 'Solo', color: 'neutral', icon: 'i-lucide-play' },
}[role.value] as { label: string; color: 'primary' | 'neutral' | 'error'; icon: string }))
</script>

<template>
  <div class="min-h-dvh">
    <!-- floating chrome -->
    <header class="glass sticky top-0 z-30 border-b border-default">
      <div class="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3 sm:px-6">
        <UButton
          to="/"
          icon="i-lucide-arrow-left"
          color="neutral"
          variant="ghost"
          size="sm"
          aria-label="Back to dashboard"
        />
        <UIcon name="i-lucide-monitor-play" class="size-5 shrink-0 text-primary" />
        <h1 class="text-sm font-semibold text-highlighted">Watch</h1>
        <UBadge :color="roleBadge.color" :icon="roleBadge.icon" variant="soft" size="sm">
          {{ roleBadge.label }}
        </UBadge>
        <span class="ml-auto hidden max-w-[14rem] truncate font-mono text-xs text-dimmed sm:block">
          {{ host }}
        </span>
        <ColorModeButton class="sm:ml-0 ml-auto" />
      </div>
    </header>

    <main class="mx-auto max-w-5xl px-4 py-6 sm:px-6 sm:py-8">
      <!-- player -->
      <div class="relative">
        <div class="pointer-events-none absolute -inset-6 -z-10 rounded-[2.5rem] bg-primary/10 blur-3xl" />
        <div class="overflow-hidden rounded-2xl border border-default bg-black shadow-2xl shadow-black/50 ring-1 ring-white/5">
          <video
            ref="video"
            :controls="role !== 'viewer'"
            class="aspect-video w-full bg-black"
          />
        </div>
      </div>

      <!-- status + actions -->
      <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
        <div
          v-if="role === 'viewer'"
          class="flex items-center gap-2 rounded-full border border-default bg-elevated px-3 py-1.5 text-sm"
        >
          <span class="live-dot size-2 rounded-full bg-success" />
          <span class="font-medium text-highlighted">Synced</span>
          <span class="tabular-nums text-dimmed">· {{ formatDrift(drift) }}</span>
        </div>
        <div v-else />

        <div v-if="role !== 'solo'">
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
      </div>

      <AudienceStrip v-if="role !== 'solo'" :host="host" :content-id="cid" />
    </main>
  </div>
</template>
