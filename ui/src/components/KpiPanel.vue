<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Summary } from '@/api/types'

// The headline numbers in one small panel, each with its change from the
// previous period when known (see KpiCard).
const props = defineProps<{ summary: Summary; accepted: number; located: number; countries: number }>()
const { t } = useI18n()

interface Figure {
  label: string
  value: number
  previous?: number | null
  detail?: string
}

const figures = computed<Figure[]>(() => {
  const s = props.summary
  const p = s.previous
  return [
    { label: t('stats.sessions'), value: s.sessions, previous: p?.sessions },
    { label: t('stats.shells'), value: s.shells, previous: p?.shells },
    { label: t('stats.logins'), value: s.logins, previous: p?.logins, detail: t('stats.acceptedShare', { n: props.accepted }) },
    { label: t('stats.sourceIps'), value: s.source_ips, previous: p?.source_ips },
    { label: t('stats.countries'), value: s.countries },
  ]
})

function note(f: Figure): string | undefined {
  if (f.detail) return f.detail
  if (!f.previous) return undefined
  const n = Math.round((100 * (f.value - f.previous)) / f.previous)
  return t('stats.change', { n: (n > 0 ? '+' : '') + n })
}
const title = (f: Figure) =>
  f.previous === null || f.previous === undefined ? undefined : t('stats.previousPeriod', { n: f.previous.toLocaleString() })
</script>

<template>
  <section
    class="card float kpi-panel"
    :aria-label="t('overview.title')"
  >
    <dl>
      <template
        v-for="f in figures"
        :key="f.label"
      >
        <dt class="muted">
          {{ f.label }}
        </dt>
        <dd>
          <span class="value">{{ f.value.toLocaleString() }}</span>
          <span
            v-if="note(f)"
            class="muted detail"
            :title="title(f)"
          >{{ note(f) }}</span>
        </dd>
      </template>
    </dl>
    <p class="muted located">
      {{ t('overview.summary', { n: located.toLocaleString(), c: countries }, located) }}
    </p>
  </section>
</template>

<style scoped>
.kpi-panel {
  width: 14rem;
  padding: var(--space-2) var(--space-3);
}

dl {
  display: grid;
  grid-template-columns: 1fr;
  margin: 0;
}

dt {
  margin-top: var(--space-1);
  font-size: 0.7rem;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

dd {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
  margin: 0;
}

.value {
  font-size: 1.25rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
}

.detail {
  font-size: 0.75rem;
}

.located {
  margin: var(--space-2) 0 0;
  font-size: 0.75rem;
}
</style>
