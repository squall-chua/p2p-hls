<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'

defineProps<{ requests: string[] }>()
const emit = defineEmits<{ approved: [] }>()

const bridge = useBridge()
const toast = useToast()
const pending = ref<Set<string>>(new Set())

async function approve(id: string) {
  pending.value = new Set(pending.value).add(id)
  try {
    await bridge.approve(id)
    toast.add({ title: 'Approved', icon: 'i-lucide-check', color: 'success' })
    emit('approved')
  } catch {
    toast.add({ title: 'Approve failed', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    const next = new Set(pending.value)
    next.delete(id)
    pending.value = next
  }
}
</script>

<template>
  <div v-if="requests.length" class="grid gap-2 sm:grid-cols-2">
    <div
      v-for="id in requests"
      :key="id"
      class="flex items-center gap-3 rounded-xl border border-default bg-elevated px-3 py-2.5"
    >
      <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-warning/10 text-warning">
        <UIcon name="i-lucide-user-round-plus" class="size-4" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="text-sm font-medium text-highlighted">Wants library access</p>
        <p class="truncate font-mono text-xs text-muted">{{ id }}</p>
      </div>
      <UButton
        label="Approve"
        icon="i-lucide-check"
        color="primary"
        size="sm"
        class="shrink-0"
        :loading="pending.has(id)"
        @click="approve(id)"
      />
    </div>
  </div>
  <p v-else class="text-sm text-muted">No pending requests</p>
</template>
