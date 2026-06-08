<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'

interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
}

defineProps<{ titles: TitleView[] }>()

const bridge = useBridge()
const toast = useToast()
const self = ref(bridge.nodeId)
const starting = ref<Set<string>>(new Set())

onMounted(async () => {
  self.value = (await bridge.resolveSelf()).nodeId
})

function fmtDuration(ms: number): string {
  if (!ms || ms <= 0) return ''
  const total = Math.round(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

async function startParty(contentId: string) {
  starting.value = new Set(starting.value).add(contentId)
  try {
    const sid = self.value || (await bridge.resolveSelf()).nodeId
    await bridge.startParty(contentId)
    toast.add({ title: 'Party started', icon: 'i-lucide-party-popper', color: 'success' })
    await navigateTo(`/watch/${sid}/${contentId}?party=1`)
  } catch {
    toast.add({ title: 'Could not start party', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    const next = new Set(starting.value)
    next.delete(contentId)
    starting.value = next
  }
}
</script>

<template>
  <div v-if="titles.length" class="flex flex-col gap-2">
    <div
      v-for="t in titles"
      :key="t.contentId"
      class="flex items-center justify-between gap-3 rounded-md bg-elevated/50 px-3 py-2"
    >
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="truncate font-medium text-highlighted">{{ t.displayTitle }}</span>
          <UBadge
            v-if="t.partyLive"
            color="error"
            variant="subtle"
            size="sm"
          >
            ● Party · {{ t.partyViewers }}
          </UBadge>
        </div>
        <span v-if="fmtDuration(t.durationMs)" class="text-xs text-dimmed">
          {{ fmtDuration(t.durationMs) }}
        </span>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <UButton
          :to="`/watch/${self}/${t.contentId}`"
          label="Watch"
          icon="i-lucide-play"
          color="neutral"
          variant="soft"
          size="sm"
        />
        <UButton
          label="Start party"
          icon="i-lucide-users"
          color="primary"
          variant="soft"
          size="sm"
          :loading="starting.has(t.contentId)"
          @click="startParty(t.contentId)"
        />
      </div>
    </div>
  </div>
  <p v-else class="text-sm text-muted">Your library is empty</p>
</template>
