<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import SelectButton from 'openvue/selectbutton'
import Button from 'openvue/button'
import DayRange from '@/components/DayRange.vue'
import { patchQuery, queryValue } from '@/router'

// The filter bar of statistics pages: days and human or bot; the sensor
// is chosen in the header. Extra controls go in the slot.
const { t } = useI18n()
const route = useRoute()
const q = (k: string) => queryValue(route.query, k)

const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const verdict = computed({
  get: () => q('verdict') || 'any',
  set: (v: string) => void patchQuery({ verdict: v === 'any' ? undefined : v }),
})
const options = computed(() => [
  { label: t('sessions.filters.anyVerdict'), value: 'any' },
  { label: t('sessions.filters.humans'), value: 'human' },
  { label: t('sessions.filters.bots'), value: 'bot' },
])
const filtered = computed(() => ['from', 'to', 'verdict'].some((k) => q(k)))
function clear() {
  void patchQuery({ from: undefined, to: undefined, verdict: undefined })
}
</script>

<template>
  <div
    class="filters"
    role="search"
  >
    <DayRange
      v-model:from="from"
      v-model:to="to"
    />
    <SelectButton
      v-model="verdict"
      :options="options"
      option-label="label"
      option-value="value"
      :allow-empty="false"
      :aria-label="t('sessions.filters.verdict')"
    />
    <slot />
    <Button
      v-if="filtered"
      :label="t('filters.clear')"
      severity="secondary"
      text
      @click="clear"
    />
  </div>
</template>
