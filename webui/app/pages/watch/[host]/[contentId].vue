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
const members = ref<any[]>([])
let handle: { close: () => void } | null = null
let live: { start: () => void; stop: () => void } | null = null

async function refetchAudience() {
  try { members.value = await bridge.audience() } catch { /* ignore */ }
}

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
  if (role.value !== 'solo') {
    refetchAudience()
    live = useLiveData(
      (kind) => { if (kind === 'audience') refetchAudience() },
      (type) => {
        if (type === 'party-ended' && role.value === 'viewer') {
          toast.add({ title: 'The host ended the party' })
          navigateTo('/')
        }
      },
    )
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
    <main>
      <!-- player fills the full window; the 16:9 video letterboxes within it -->
      <div class="group relative h-dvh w-full bg-black">
        <video
          ref="video"
          :controls="role !== 'viewer'"
          class="h-full w-full bg-black object-contain"
        />

        <!-- chrome overlaid on the player; revealed on hover (top bar removed) -->
        <div
          class="pointer-events-none absolute inset-x-3 top-3 z-10 flex items-start justify-between gap-2 opacity-0 transition-opacity duration-200 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100"
        >
          <!-- left: back + title + audience count -->
          <div class="flex items-center gap-2">
            <UButton
              to="/"
              icon="i-lucide-arrow-left"
              color="neutral"
              variant="ghost"
              size="sm"
              aria-label="Back to dashboard"
              class="pointer-events-auto bg-black/55 text-white ring-1 ring-white/10 backdrop-blur hover:bg-black/70 hover:text-white"
            />
            <div class="flex items-center gap-2 rounded-full bg-black/55 px-3 py-1.5 ring-1 ring-white/10 backdrop-blur">
              <UIcon name="i-lucide-monitor-play" class="size-4 text-primary" />
              <span class="text-sm font-semibold text-white">Watch</span>
              <UBadge :color="roleBadge.color" :icon="roleBadge.icon" variant="soft" size="sm">
                {{ roleBadge.label }}
              </UBadge>
              <template v-if="role !== 'solo'">
                <span class="text-white/30">·</span>
                <span class="text-sm text-white/80">{{ members.length }} watching</span>
              </template>
            </div>
          </div>

          <!-- right: sync status + leave/end action -->
          <div v-if="role !== 'solo'" class="flex items-center gap-2">
            <span
              v-if="role === 'viewer'"
              class="flex items-center gap-2 rounded-full bg-black/55 px-3 py-1.5 text-sm ring-1 ring-white/10 backdrop-blur"
            >
              <span class="live-dot size-2 rounded-full bg-success" />
              <span class="font-medium text-white">Synced</span>
              <span class="tabular-nums text-white/60">· {{ formatDrift(drift) }}</span>
            </span>
            <UButton
              v-if="role === 'viewer'"
              label="Leave"
              icon="i-lucide-log-out"
              color="neutral"
              variant="solid"
              size="sm"
              class="pointer-events-auto"
              @click="leave"
            />
            <UButton
              v-else-if="role === 'host'"
              label="End party"
              icon="i-lucide-circle-stop"
              color="error"
              variant="solid"
              size="sm"
              class="pointer-events-auto"
              @click="end"
            />
          </div>
        </div>
      </div>
    </main>
  </div>
</template>
