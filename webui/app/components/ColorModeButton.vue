<script setup lang="ts">
const colorMode = useColorMode()
const isDark = computed({
  get: () => colorMode.value === 'dark',
  set: (v) => { colorMode.preference = v ? 'dark' : 'light' },
})
</script>

<template>
  <ClientOnly>
    <UButton
      :icon="isDark ? 'i-lucide-moon' : 'i-lucide-sun'"
      color="neutral"
      variant="ghost"
      size="sm"
      :aria-label="isDark ? 'Switch to light theme' : 'Switch to dark theme'"
      @click="isDark = !isDark"
    />
    <template #fallback>
      <UButton icon="i-lucide-moon" color="neutral" variant="ghost" size="sm" aria-label="Toggle theme" />
    </template>
  </ClientOnly>
</template>
