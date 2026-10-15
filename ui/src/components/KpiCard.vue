<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// A headline number, with its change from the previous period when known.
const props = defineProps<{ label: string; value: number; detail?: string; previous?: number | null }>()
const { t } = useI18n()

const change = computed(() => {
  if (props.previous === null || props.previous === undefined) return null
  if (props.previous === 0) return props.value === 0 ? 0 : null
  return Math.round((100 * (props.value - props.previous)) / props.previous)
})
</script>

<template>
  <div class="card kpi">
    <span class="label muted">{{ label }}</span>
    <span class="value">{{ value.toLocaleString() }}</span>
    <span class="muted detail">
      {{ detail }}
      <span
        v-if="change !== null"
        :title="t('stats.previousPeriod', { n: (previous ?? 0).toLocaleString() })"
      >{{ t('stats.change', { n: (change > 0 ? '+' : '') + change }) }}</span>
    </span>
  </div>
</template>

<style scoped>
.kpi {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.label {
  font-size: 0.85em;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.value {
  font-size: 1.8rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}

.detail {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-size: 0.85em;
}
</style>
