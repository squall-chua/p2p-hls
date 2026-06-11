<script setup lang="ts">
import { ref } from 'vue'
import { LaneAllocator, pushBounded } from '~/lib/danmaku'

interface Flying { id: number; text: string; lane: number }

const flying = ref<Flying[]>([])
let queue: { text: string }[] = []
let nextId = 1
const lanes = new LaneAllocator()
let pumping = false

// add enqueues a Danmaku and tries to place queued items into free lanes.
function add(d: { text: string; sender?: string }) {
  queue = pushBounded(queue, { text: d.text })
  pump()
}

function pump() {
  const now = performance.now()
  const remaining: { text: string }[] = []
  for (const item of queue) {
    const lane = lanes.allocate(now)
    if (lane < 0) { remaining.push(item); continue }
    flying.value.push({ id: nextId++, text: item.text, lane })
  }
  queue = remaining
  if (queue.length && !pumping) {
    pumping = true
    setTimeout(() => { pumping = false; pump() }, 200)
  }
}

function onEnd(id: number) {
  flying.value = flying.value.filter((f) => f.id !== id)
}

defineExpose({ add })
</script>

<template>
  <div class="pointer-events-none absolute inset-0 z-10 overflow-hidden">
    <span
      v-for="f in flying"
      :key="f.id"
      class="danmaku-item"
      :style="{ top: f.lane * 6 + 2 + '%' }"
      @animationend="onEnd(f.id)"
    >{{ f.text }}</span>
  </div>
</template>

<style scoped>
.danmaku-item {
  position: absolute;
  left: 100%;
  white-space: nowrap;
  color: #fff;
  font-weight: 600;
  font-size: 1.25rem;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9), 0 0 4px rgba(0, 0, 0, 0.7);
  will-change: transform;
  animation: danmaku-fly 7s linear forwards; /* keep in sync with TRAVEL_MS in danmaku.ts */
}
@keyframes danmaku-fly {
  from { transform: translateX(0); }
  to { transform: translateX(calc(-100vw - 100%)); }
}

/* A Danmaku's scroll IS the content, not decoration, so exempt it from the global
   reduced-motion animation clamp (main.css) that would otherwise zero its duration
   and make it flash off-screen instantly. */
@media (prefers-reduced-motion: reduce) {
  .danmaku-item { animation-duration: 7s !important; }
}
</style>
