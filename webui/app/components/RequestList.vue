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
  <div v-if="requests.length" class="flex flex-col gap-2">
    <div
      v-for="id in requests"
      :key="id"
      class="flex items-center justify-between gap-3 rounded-md bg-elevated/50 px-3 py-2"
    >
      <span class="truncate font-mono text-sm text-highlighted">{{ id }}</span>
      <UButton
        label="Approve"
        icon="i-lucide-check"
        color="primary"
        size="sm"
        :loading="pending.has(id)"
        @click="approve(id)"
      />
    </div>
  </div>
  <p v-else class="text-sm text-muted">No pending requests</p>
</template>
