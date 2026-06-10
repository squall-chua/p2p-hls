<script setup lang="ts">
import { useBridge, type AccessRequest } from '~/composables/useBridge'

defineProps<{ requests: AccessRequest[] }>()
const emit = defineEmits<{ resolved: [] }>()

const bridge = useBridge()
const toast = useToast()
const approving = ref<Set<string>>(new Set())
const rejecting = ref<Set<string>>(new Set())

async function approve(id: string) {
  approving.value = new Set(approving.value).add(id)
  try {
    await bridge.approve(id)
    toast.add({ title: 'Approved', icon: 'i-lucide-check', color: 'success' })
    emit('resolved')
  } catch {
    toast.add({ title: 'Approve failed', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    const next = new Set(approving.value)
    next.delete(id)
    approving.value = next
  }
}

async function reject(id: string) {
  rejecting.value = new Set(rejecting.value).add(id)
  try {
    await bridge.reject(id)
    toast.add({ title: 'Request dismissed', icon: 'i-lucide-user-round-x', color: 'neutral' })
    emit('resolved')
  } catch {
    toast.add({ title: 'Dismiss failed', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    const next = new Set(rejecting.value)
    next.delete(id)
    rejecting.value = next
  }
}
</script>

<template>
  <div v-if="requests.length" class="grid gap-2 sm:grid-cols-2">
    <div
      v-for="r in requests"
      :key="r.nodeId"
      class="flex items-center gap-3 rounded-xl border border-default bg-elevated px-3 py-2.5"
    >
      <div class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-warning/10 text-warning">
        <UIcon name="i-lucide-user-round-plus" class="size-4" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium text-highlighted">
          {{ r.displayName || 'Unknown peer' }}
        </p>
        <p class="truncate font-mono text-xs text-muted">{{ r.nodeId }}</p>
        <p v-if="r.message" class="mt-1 truncate text-xs italic text-dimmed">
          &ldquo;{{ r.message }}&rdquo;
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-1.5">
        <UButton
          label="Approve"
          icon="i-lucide-check"
          color="primary"
          size="sm"
          :loading="approving.has(r.nodeId)"
          :disabled="rejecting.has(r.nodeId)"
          @click="approve(r.nodeId)"
        />
        <UButton
          label="Reject"
          icon="i-lucide-x"
          color="error"
          variant="soft"
          size="sm"
          :loading="rejecting.has(r.nodeId)"
          :disabled="approving.has(r.nodeId)"
          @click="reject(r.nodeId)"
        />
      </div>
    </div>
  </div>
  <p v-else class="text-sm text-muted">No pending requests</p>
</template>
