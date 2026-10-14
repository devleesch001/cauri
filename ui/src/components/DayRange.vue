<script setup lang="ts">
import { computed } from 'vue'
import DatePicker from 'openvue/datepicker'
import { useI18n } from 'vue-i18n'
import { fromDay, toDay } from '@/dates'

// A range of days bound to two YYYY-MM-DD strings.
const { t } = useI18n()
const from = defineModel<string>('from', { required: true })
const to = defineModel<string>('to', { required: true })

const range = computed<(Date | null)[] | null>({
  get: () => {
    const f = fromDay(from.value)
    return f ? [f, fromDay(to.value)] : null
  },
  set: (v) => {
    const [f, t] = v ?? []
    // Wait for the second click of a range before filtering.
    if (f && !t) return
    from.value = f ? toDay(f) : ''
    to.value = t ? toDay(t) : ''
  },
})
</script>

<template>
  <DatePicker
    v-model="range"
    selection-mode="range"
    date-format="yy-mm-dd"
    :placeholder="t('filters.anyDate')"
    show-button-bar
    :manual-input="false"
    :aria-label="t('filters.dateRange')"
  />
</template>
