<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { useLiveData } from '~/composables/useLiveData'

const bridge = useBridge()
const route = useRoute()
const me = ref(bridge.name || bridge.nodeId)
const peers = ref<any[]>([])
const mobileOpen = ref(false)

async function refetch() {
  peers.value = await bridge.presence()
}

let live: { start: () => void; stop: () => void } | null = null
onMounted(async () => {
  me.value = (await bridge.resolveSelf()).displayName || bridge.nodeId
  await refetch()
  live = useLiveData((k) => {
    if (k === 'presence') refetch()
  })
  live.start()
})
onBeforeUnmount(() => live?.stop())

// Close the mobile drawer whenever navigation happens.
watch(() => route.fullPath, () => { mobileOpen.value = false })
</script>

<template>
  <div class="app-ambient min-h-dvh bg-default text-default">
    <!-- desktop sidebar -->
    <aside class="glass fixed inset-y-0 left-0 z-30 hidden w-72 border-r border-default lg:block">
      <AppSidebar :me="me" :peers="peers" />
    </aside>

    <!-- mobile top bar -->
    <header class="glass sticky top-0 z-30 flex items-center gap-2 border-b border-default px-4 py-3 lg:hidden">
      <UButton
        icon="i-lucide-menu"
        color="neutral"
        variant="ghost"
        aria-label="Open navigation"
        @click="mobileOpen = true"
      />
      <div class="flex items-center gap-2">
        <div class="flex size-7 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <UIcon name="i-lucide-radio-tower" class="size-4" />
        </div>
        <span class="text-sm font-semibold text-highlighted">P2P HLS</span>
      </div>
      <div class="ml-auto">
        <ColorModeButton />
      </div>
    </header>

    <USlideover v-model:open="mobileOpen" side="left" :ui="{ content: 'w-72 max-w-[80vw]' }">
      <template #content>
        <AppSidebar :me="me" :peers="peers" />
      </template>
    </USlideover>

    <!-- main content -->
    <div class="relative z-10 lg:pl-72">
      <slot />
    </div>
  </div>
</template>
