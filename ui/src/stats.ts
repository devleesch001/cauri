import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { dayRange } from '@/dates'
import { queryValue } from '@/router'

// The filters common to statistics, kept in the URL like every filter:
// sensor (from the header), days, human or bot.
const keys = ['sensor', 'from', 'to', 'verdict'] as const

export function useStatsFilters() {
  const route = useRoute()
  const q = (k: string) => queryValue(route.query, k)
  /** Parameters of the /api/stats calls. */
  const params = computed(() => ({ sensor: q('sensor'), verdict: q('verdict'), ...dayRange(q('from'), q('to')) }))
  /** A link to a list with the same filters, plus its own. */
  function link(name: string, extra: Record<string, string> = {}) {
    const query: Record<string, string> = {}
    for (const k of keys) {
      if (q(k)) query[k] = q(k)
    }
    return { name, query: { ...query, ...extra } }
  }
  return { params, link, q }
}
