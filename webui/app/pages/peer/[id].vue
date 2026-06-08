<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'

interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
}

const route = useRoute()
const bridge = useBridge()
const toast = useToast()
const id = route.params.id as string

const titles = ref<TitleView[]>([])
const denied = ref(false)
const message = ref('')
const loading = ref(true)
const requesting = ref(false)
const joining = ref<string>('')

async function load() {
  loading.value = true
  try {
    titles.value = await bridge.catalog(id)
    denied.value = false
  } catch (e: any) {
    if (e?.status === 403) denied.value = true
    else toast.add({ title: 'Failed to load catalog', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    loading.value = false
  }
}

async function request() {
  requesting.value = true
  try {
    await bridge.requestAccess(id, message.value)
    toast.add({ title: 'Request sent', icon: 'i-lucide-send', color: 'success' })
  } catch {
    toast.add({ title: 'Request failed', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    requesting.value = false
  }
}

async function join(cid: string) {
  joining.value = cid
  try {
    await bridge.joinParty(id, cid)
    await navigateTo(`/watch/${id}/${cid}`)
  } catch {
    toast.add({ title: 'Could not join party', icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    joining.value = ''
  }
}

onMounted(load)
</script>

<template>
  <div class="min-h-screen bg-default">
    <header class="border-b border-default px-6 py-4">
      <div class="flex items-center gap-3">
        <UButton
          to="/"
          icon="i-lucide-arrow-left"
          color="neutral"
          variant="ghost"
          size="sm"
          aria-label="Back to dashboard"
        />
        <UIcon name="i-lucide-folder-open" class="size-5 text-primary" />
        <h1 class="truncate text-lg font-semibold text-highlighted">Peer Library</h1>
        <span class="ml-auto truncate font-mono text-xs text-muted">{{ id }}</span>
      </div>
    </header>

    <main class="p-6">
      <!-- restricted -->
      <UCard v-if="denied" class="mx-auto max-w-md">
        <div class="flex flex-col items-center gap-4 py-6 text-center">
          <div class="rounded-full bg-elevated p-3">
            <UIcon name="i-lucide-lock" class="size-6 text-muted" />
          </div>
          <div class="space-y-1">
            <p class="font-semibold text-highlighted">This peer's Library is restricted</p>
            <p class="text-sm text-muted">
              Send a request to ask this peer for access to their Library.
            </p>
          </div>
          <div class="flex w-full flex-col gap-3">
            <UInput
              v-model="message"
              placeholder="Add a message (optional)"
              icon="i-lucide-message-square"
            />
            <UButton
              label="Request access"
              icon="i-lucide-send"
              color="primary"
              block
              :loading="requesting"
              @click="request"
            />
          </div>
        </div>
      </UCard>

      <!-- catalog list -->
      <div v-else class="grid gap-3">
        <UCard v-for="t in titles" :key="t.contentId">
          <div class="flex items-center justify-between gap-3">
            <div class="flex min-w-0 items-center gap-2">
              <span class="truncate font-medium text-highlighted">{{ t.displayTitle }}</span>
              <UBadge
                v-if="t.partyLive"
                color="primary"
                variant="subtle"
                size="sm"
              >
                ● Party · {{ t.partyViewers }}
              </UBadge>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <UButton
                :to="`/watch/${id}/${t.contentId}`"
                label="Watch"
                icon="i-lucide-play"
                color="neutral"
                variant="soft"
                size="sm"
              />
              <UButton
                v-if="t.partyLive"
                label="Join"
                icon="i-lucide-users"
                color="primary"
                variant="soft"
                size="sm"
                :loading="joining === t.contentId"
                @click="join(t.contentId)"
              />
            </div>
          </div>
        </UCard>

        <p v-if="!loading && !titles.length" class="text-sm text-muted">No titles</p>
      </div>
    </main>
  </div>
</template>
