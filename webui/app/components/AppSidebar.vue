<script setup lang="ts">
interface PeerView {
  nodeId: string
  displayName: string
  online: boolean
}

const props = defineProps<{ me: string; peers: PeerView[] }>()

const route = useRoute()
const isHome = computed(() => route.path === '/')

const onlineCount = computed(() => props.peers.filter((p) => p.online).length)

const initials = computed(() => {
  const n = (props.me || '?').trim()
  const parts = n.split(/\s+/).filter(Boolean)
  const a = parts[0]?.[0] ?? n[0] ?? '?'
  const b = parts.length > 1 ? parts[parts.length - 1]![0] : ''
  return (a + b).toUpperCase()
})
</script>

<template>
  <div class="flex h-full flex-col">
    <!-- brand -->
    <div class="flex items-center gap-3 px-5 pb-4 pt-5">
      <div class="glow-primary flex size-9 items-center justify-center rounded-xl bg-primary/10 text-primary">
        <UIcon name="i-lucide-radio-tower" class="size-5" />
      </div>
      <div class="min-w-0">
        <p class="text-sm font-semibold leading-tight text-highlighted">P2P HLS</p>
        <p class="text-xs leading-tight text-dimmed">watch together</p>
      </div>
    </div>

    <!-- identity / your library -->
    <NuxtLink
      to="/"
      class="group mx-3 flex items-center gap-3 rounded-xl border px-3 py-2.5 transition-colors duration-200"
      :class="isHome
        ? 'border-accented bg-elevated'
        : 'border-muted bg-muted/40 hover:bg-muted'"
    >
      <UAvatar
        :text="initials"
        size="sm"
        :ui="{ root: 'bg-primary/15 text-primary ring-1 ring-primary/25', fallback: 'font-semibold' }"
      />
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium text-highlighted">{{ me || 'this node' }}</p>
        <p class="flex items-center gap-1.5 text-xs text-muted">
          <span class="size-1.5 rounded-full bg-success" />
          your library
        </p>
      </div>
      <UIcon
        name="i-lucide-house"
        class="ml-auto size-4 shrink-0 transition-opacity"
        :class="isHome
          ? 'text-muted opacity-100'
          : 'text-dimmed opacity-0 group-hover:opacity-100'"
      />
    </NuxtLink>

    <!-- peers -->
    <div class="flex items-center justify-between px-5 pb-2 pt-6">
      <span class="text-xs font-semibold uppercase tracking-wider text-dimmed">Peers</span>
      <UBadge v-if="peers.length" color="neutral" variant="soft" size="sm">
        {{ onlineCount }} online
      </UBadge>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
      <PeerList :peers="peers" />
    </div>

    <!-- footer -->
    <div class="flex items-center justify-between border-t border-muted px-4 py-3">
      <span class="flex items-center gap-1.5 text-xs text-dimmed">
        <UIcon name="i-lucide-shield-check" class="size-3.5 text-success" />
        mesh connected
      </span>
      <ColorModeButton />
    </div>
  </div>
</template>
