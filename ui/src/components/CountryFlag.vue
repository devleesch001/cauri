<script lang="ts">
// Flags of flag-icons, as files: the browser loads only those shown.
// no-inline keeps the small ones out of the JavaScript bundle.
const flags = import.meta.glob<string>('/node_modules/flag-icons/flags/4x3/*.svg', {
  query: '?url&no-inline',
  import: 'default',
  eager: true,
})
const byCode = new Map(
  Object.entries(flags).map(([path, url]) => [path.slice(path.lastIndexOf('/') + 1, -'.svg'.length), url]),
)
</script>

<script setup lang="ts">
import { computed } from 'vue'
import { countryName } from '@/format'

// A country from its ISO 3166-1 alpha-2 code: its flag, then its name,
// then the slot (such as a city).
const props = defineProps<{ code: string }>()
const flag = computed(() => byCode.get(props.code.toLowerCase()))
</script>

<template>
  <span class="country-flag">
    <img
      v-if="flag"
      :src="flag"
      alt=""
      width="20"
      height="15"
    ><span>{{ countryName(code) }}<slot /></span>
  </span>
</template>

<style scoped>
.country-flag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  white-space: nowrap;
}

img {
  flex: none;
  border-radius: 2px;
  /* White parts of flags stay visible on a white or dark surface. */
  box-shadow: 0 0 0 1px var(--c-rule);
}
</style>
