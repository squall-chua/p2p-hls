<script setup lang="ts">
interface PeerView {
  nodeId: string
  displayName: string
  online: boolean
}

defineProps<{ peers: PeerView[] }>()
</script>

<template>
  <div v-if="peers.length" class="flex flex-col gap-1">
    <UButton
      v-for="p in peers"
      :key="p.nodeId"
      :to="`/peer/${p.nodeId}`"
      color="neutral"
      variant="ghost"
      class="justify-start gap-3 py-2"
    >
      <span
        class="size-2 shrink-0 rounded-full"
        :class="p.online ? 'bg-success' : 'bg-muted'"
        :title="p.online ? 'online' : 'offline'"
      />
      <span class="truncate text-highlighted">{{ p.displayName || p.nodeId }}</span>
    </UButton>
  </div>
  <p v-else class="text-sm text-muted">No peers online</p>
</template>
