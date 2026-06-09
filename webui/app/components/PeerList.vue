<script setup lang="ts">
interface PeerView {
  nodeId: string
  displayName: string
  online: boolean
}

defineProps<{ peers: PeerView[] }>()

const route = useRoute()
const activeId = computed(() => route.params.id as string | undefined)
</script>

<template>
  <nav v-if="peers.length" class="flex flex-col gap-0.5">
    <NuxtLink
      v-for="p in peers"
      :key="p.nodeId"
      :to="`/peer/${p.nodeId}`"
      class="group relative flex items-center gap-3 rounded-lg py-2 pl-3 pr-2 text-sm transition-colors duration-200"
      :class="activeId === p.nodeId
        ? 'bg-elevated text-highlighted ring-1 ring-accented'
        : 'text-muted hover:bg-muted hover:text-default'"
    >
      <span
        v-if="activeId === p.nodeId"
        class="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-primary"
      />
      <span class="relative flex size-2.5 shrink-0 items-center justify-center">
        <span
          class="size-2 rounded-full"
          :class="p.online ? 'bg-success' : 'bg-inverted/25'"
        />
        <span
          v-if="p.online"
          class="live-dot absolute size-2.5 rounded-full bg-success/40"
        />
      </span>
      <span class="truncate" :class="{ 'text-dimmed': !p.online }">
        {{ p.displayName || p.nodeId }}
      </span>
      <UIcon
        name="i-lucide-chevron-right"
        class="ml-auto size-4 shrink-0 transition-opacity"
        :class="activeId === p.nodeId
          ? 'text-muted opacity-100'
          : 'text-dimmed opacity-0 group-hover:opacity-100'"
      />
    </NuxtLink>
  </nav>

  <div v-else class="flex flex-col items-center gap-2 px-3 py-10 text-center">
    <UIcon name="i-lucide-users-round" class="size-6 text-dimmed" />
    <p class="text-sm text-muted">No peers online</p>
    <p class="text-xs text-dimmed">Peers appear here as they join the mesh.</p>
  </div>
</template>
