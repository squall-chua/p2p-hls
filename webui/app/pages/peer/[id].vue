<script setup lang="ts">
import { useBridge } from '~/composables/useBridge'
import { applyLiveParties } from '~/lib/liveParties'

interface TitleView {
  contentId: string
  displayTitle: string
  durationMs: number
  partyLive: boolean
  partyViewers: number
  relDir: string
  rootLabel: string
  thumbnail: string
}

const route = useRoute()
const bridge = useBridge()
const toast = useToast()
const id = route.params.id as string

const titles = ref<TitleView[]>([])
const denied = ref(false)
const requested = ref(false)
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
    requested.value = true
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

let pollTimer: ReturnType<typeof setInterval> | null = null
let polling = false

// While browsing an accessible library, poll the host's live-party status so a
// party the host starts (or ends) flips the "Join" button without a manual reload.
// Lightweight: viewer count only, no thumbnails (unlike a full catalog refetch).
async function pollLiveParties() {
  if (polling || denied.value) return
  polling = true
  try {
    titles.value = applyLiveParties(titles.value, await bridge.liveParties(id))
  } catch { /* transient: keep last-known status */ } finally {
    polling = false
  }
}
function stopPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

onMounted(async () => {
  await load()
  if (!denied.value) {
    pollTimer = setInterval(() => { if (!document.hidden) pollLiveParties() }, 5000)
  }
})
onBeforeUnmount(stopPolling)
</script>

<template>
  <div class="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
    <!-- header -->
    <div class="mb-6 flex items-center gap-3">
      <UButton
        to="/"
        icon="i-lucide-arrow-left"
        color="neutral"
        variant="ghost"
        size="sm"
        aria-label="Back to dashboard"
        class="lg:hidden"
      />
      <div class="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/15">
        <UIcon name="i-lucide-folder-open" class="size-5" />
      </div>
      <div class="min-w-0">
        <h1 class="text-xl font-semibold tracking-tight text-highlighted sm:text-2xl">
          Peer library
        </h1>
        <p class="truncate font-mono text-xs text-muted">{{ id }}</p>
      </div>
    </div>

    <!-- loading skeleton -->
    <div
      v-if="loading"
      class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <div
        v-for="n in 4"
        :key="n"
        class="overflow-hidden rounded-2xl border border-default bg-elevated"
      >
        <USkeleton class="aspect-video w-full rounded-none" />
        <div class="space-y-3 p-3.5">
          <USkeleton class="h-4 w-3/4" />
          <div class="flex gap-2">
            <USkeleton class="h-8 flex-1" />
            <USkeleton class="h-8 flex-1" />
          </div>
        </div>
      </div>
    </div>

    <!-- restricted -->
    <div v-else-if="denied" class="mx-auto max-w-md">
      <div class="overflow-hidden rounded-2xl border border-default bg-elevated p-6 sm:p-8">
        <!-- pending approval (after a request has been sent) -->
        <div v-if="requested" class="flex flex-col items-center gap-4 text-center">
          <div class="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
            <UIcon name="i-lucide-clock" class="size-6" />
          </div>
          <div class="space-y-1">
            <p class="font-semibold text-highlighted">Request pending approval</p>
            <p class="text-sm text-muted">
              Waiting for this peer to approve your request. You'll get access to their library once they do.
            </p>
          </div>
        </div>

        <!-- request form -->
        <div v-else class="flex flex-col items-center gap-4 text-center">
          <div class="flex size-14 items-center justify-center rounded-full bg-warning/10 text-warning ring-1 ring-warning/20">
            <UIcon name="i-lucide-lock" class="size-6" />
          </div>
          <div class="space-y-1">
            <p class="font-semibold text-highlighted">This library is restricted</p>
            <p class="text-sm text-muted">
              Send a request to ask this peer for access to their library.
            </p>
          </div>
          <div class="flex w-full flex-col gap-3">
            <UInput
              v-model="message"
              placeholder="Add a message (optional)"
              icon="i-lucide-message-square"
              size="lg"
            />
            <UButton
              label="Request access"
              icon="i-lucide-send"
              color="primary"
              size="lg"
              block
              :loading="requesting"
              @click="request"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- catalog -->
    <template v-else>
      <LibraryBrowser :titles="titles" base-label="Catalog">
        <template #actions="{ title: t }">
          <UButton
            :to="`/watch/${id}/${t.contentId}`"
            label="Watch"
            icon="i-lucide-play"
            color="neutral"
            variant="soft"
            size="sm"
            class="flex-1 justify-center"
          />
          <UButton
            v-if="t.partyLive"
            label="Join"
            icon="i-lucide-users"
            color="primary"
            variant="solid"
            size="sm"
            class="flex-1 justify-center"
            :loading="joining === t.contentId"
            @click="join(t.contentId)"
          />
        </template>
        <template #empty>
          <div class="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-default px-6 py-14 text-center">
            <div class="flex size-11 items-center justify-center rounded-full bg-muted text-dimmed">
              <UIcon name="i-lucide-folder-open" class="size-5" />
            </div>
            <div class="space-y-1">
              <p class="font-medium text-highlighted">No titles shared</p>
              <p class="text-sm text-muted">This peer hasn't shared anything you can watch yet.</p>
            </div>
          </div>
        </template>
      </LibraryBrowser>
    </template>
  </div>
</template>
