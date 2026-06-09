<script setup lang="ts">
import { useBridge, type CurrentParty } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

const bridge = useBridge()
const me = ref(bridge.name || bridge.nodeId)
const requests = ref<string[]>([])
const library = ref<any[]>([])
const watching = ref<CurrentParty | null>(null)

async function refetch(kind = 'all') {
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
  <div class="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
    <!-- page header -->
    <div class="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold tracking-tight text-highlighted sm:text-2xl">
          Your library
        </h1>
        <p class="mt-1 text-sm text-muted">Host a watch party, or play something solo.</p>
      </div>
      <span
        class="hidden items-center gap-2 rounded-full border border-default bg-elevated px-3 py-1.5 text-xs text-muted sm:inline-flex"
      >
        <UIcon name="i-lucide-fingerprint" class="size-3.5 text-primary" />
        <span class="max-w-[16rem] truncate font-mono">{{ me || 'this node' }}</span>
      </span>
    </div>

    <!-- now watching -->
    <section v-if="watching?.active" class="mb-8">
      <div class="relative overflow-hidden rounded-2xl border border-default bg-elevated p-5 sm:p-6">
        <div class="pointer-events-none absolute -right-16 -top-20 size-56 rounded-full bg-primary/20 blur-3xl" />
        <div class="relative flex flex-col gap-4 sm:flex-row sm:items-center">
          <div
            class="flex size-14 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-primary ring-1 ring-primary/20"
          >
            <UIcon
              :name="watching.role === 'host' ? 'i-lucide-radio' : 'i-lucide-monitor-play'"
              class="size-7"
            />
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-xs font-semibold uppercase tracking-wider text-primary">
                Continue
              </span>
              <UBadge
                :color="watching.role === 'host' ? 'error' : 'primary'"
                variant="soft"
                size="sm"
              >
                <span
                  class="live-dot mr-1.5 inline-block size-1.5 rounded-full"
                  :class="watching.role === 'host' ? 'bg-error' : 'bg-primary'"
                />
                {{ watching.role === 'host' ? 'Hosting' : 'Watching' }} · {{ watching.viewers }}
              </UBadge>
            </div>
            <p class="mt-1.5 truncate text-lg font-semibold text-highlighted">
              {{ watching.title || watching.contentId }}
            </p>
            <p class="truncate font-mono text-xs text-dimmed">{{ watching.host }}</p>
          </div>
          <UButton
            label="Resume"
            icon="i-lucide-play"
            color="primary"
            size="lg"
            class="glow-primary shrink-0 justify-center"
            @click="navigateTo(watchingLink)"
          />
        </div>
      </div>
    </section>

    <!-- access requests -->
    <section v-if="requests.length" class="mb-8">
      <div class="mb-3 flex items-center gap-2">
        <UIcon name="i-lucide-inbox" class="size-4 text-warning" />
        <h2 class="text-sm font-semibold uppercase tracking-wider text-dimmed">
          Access requests
        </h2>
        <UBadge color="warning" variant="soft" size="sm">{{ requests.length }}</UBadge>
      </div>
      <RequestList :requests="requests" @approved="refetch('requests')" />
    </section>

    <!-- library -->
    <section>
      <div class="mb-3 flex items-center gap-2">
        <UIcon name="i-lucide-library" class="size-4 text-primary" />
        <h2 class="text-sm font-semibold uppercase tracking-wider text-dimmed">Library</h2>
        <UBadge v-if="library.length" color="neutral" variant="soft" size="sm">
          {{ library.length }}
        </UBadge>
      </div>
      <LibraryPanel :titles="library" />
    </section>
  </div>
</template>
