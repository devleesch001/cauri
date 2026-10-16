<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Summary } from '@/api/types'
import StatBubble, { type Detail } from '@/components/StatBubble.vue'

// The headline numbers, one bubble each, with details on hover: the
// previous period of the same length, and what the numbers say together.
const props = defineProps<{ summary: Summary; accepted: number; located: number }>()
const { t } = useI18n()

const n = (v: number) => v.toLocaleString()
const percent = (part: number, whole: number) => (whole ? Math.round((1000 * part) / whole) / 10 : 0)

/** The change from the previous period, null when there is none to compare. */
function change(value: number, previous?: number | null): string | null {
  if (!previous) return null
  const c = Math.round((100 * (value - previous)) / previous)
  return t('stats.change', { n: (c > 0 ? '+' : '') + c })
}

/** The previous period's value and the change, as details. */
function history(value: number, previous?: number | null): Detail[] {
  if (previous === null || previous === undefined) return []
  const c = change(value, previous)
  return [{ label: t('overview.details.previous'), value: c ? `${n(previous)} · ${c}` : n(previous) }]
}

interface Bubble {
  key: string
  label: string
  value: string
  note?: string
  details: Detail[]
}

const bubbles = computed<Bubble[]>(() => {
  const s = props.summary
  const p = s.previous
  return [
    {
      key: 'sessions', label: t('stats.sessions'), value: n(s.sessions), note: change(s.sessions, p?.sessions) ?? undefined,
      details: [
        ...history(s.sessions, p?.sessions),
        { label: t('overview.details.located'), value: n(props.located) },
      ],
    },
    {
      key: 'loggedIn', label: t('stats.loggedIn'), value: n(s.logged_in), note: change(s.logged_in, p?.logged_in) ?? undefined,
      details: [
        { label: t('overview.details.ofSessions'), value: `${percent(s.logged_in, s.sessions)} %` },
        ...history(s.logged_in, p?.logged_in),
      ],
    },
    {
      key: 'logins', label: t('stats.logins'), value: n(s.logins), note: change(s.logins, p?.logins) ?? undefined,
      details: [
        { label: t('overview.details.accepted'), value: `${n(s.accepted)} · ${props.accepted} %` },
        { label: t('overview.details.perSession'), value: String(Math.round((10 * s.logins) / Math.max(s.sessions, 1)) / 10) },
        ...history(s.logins, p?.logins),
      ],
    },
    {
      key: 'sources', label: t('stats.sourceIps'), value: n(s.source_ips), note: change(s.source_ips, p?.source_ips) ?? undefined,
      details: [
        { label: t('overview.details.perAddress'), value: String(Math.round((10 * s.sessions) / Math.max(s.source_ips, 1)) / 10) },
        ...history(s.source_ips, p?.source_ips),
      ],
    },
    {
      key: 'countries', label: t('stats.countries'), value: n(s.countries),
      details: [{ label: t('overview.details.perCountry'), value: String(Math.round((10 * props.located) / Math.max(s.countries, 1)) / 10) }],
    },
  ]
})
</script>

<template>
  <div class="bubbles">
    <StatBubble
      v-for="b in bubbles"
      :key="b.key"
      :label="b.label"
      :value="b.value"
      :note="b.note"
      :details="b.details"
    />
  </div>
</template>

<style scoped>
.bubbles {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
</style>
