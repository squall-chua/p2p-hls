<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'

interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
  relDir: string
  rootLabel: string
  thumbnail?: string
}

defineProps<{ titles: TitleView[] }>()

const bridge = useBridge()
const toast = useToast()
const self = ref(bridge.nodeId)
const starting = ref<Set<string>>(new Set())

onMounted(async () => {
  self.value = (await bridge.resolveSelf()).nodeId
})

const thumbnailFor = (t: TitleView) => (self.value ? bridge.thumbURL(self.value, t.contentId) : '')

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
  <LibraryBrowser :titles="titles" base-label="Library" :thumbnail-for="thumbnailFor">
    <template #actions="{ title: t }">
      <UButton
        :to="`/watch/${self}/${t.contentId}`"
        label="Watch"
        icon="i-lucide-play"
        color="neutral"
        variant="soft"
        size="sm"
        class="flex-1 justify-center"
      />
      <UButton
        label="Start party"
        icon="i-lucide-users"
        color="primary"
        variant="solid"
        size="sm"
        class="flex-1 justify-center"
        :loading="starting.has(t.contentId)"
        @click="startParty(t.contentId)"
      />
    </template>
    <template #empty>
      <div class="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-default px-6 py-14 text-center">
        <div class="flex size-11 items-center justify-center rounded-full bg-muted text-dimmed">
          <UIcon name="i-lucide-film" class="size-5" />
        </div>
        <div class="space-y-1">
          <p class="font-medium text-highlighted">Your library is empty</p>
          <p class="text-sm text-muted">Shared titles you can watch or host will show up here.</p>
        </div>
      </div>
    </template>
  </LibraryBrowser>
</template>
