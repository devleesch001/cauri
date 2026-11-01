import { computed, ref, watch, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import { dayRange } from '@/dates'
import { queryValue } from '@/router'

// The filters common to statistics, kept in the URL like every filter:
// sensor (from the header), days, human or bot, and whether a login
// succeeded.
const keys = ['sensor', 'from', 'to', 'verdict', 'login'] as const

// Filters typed by hand match a start by default (see the API's match): a
// link names one value, quoted to match it exactly.
const typed = new Set(['ip', 'session', 'username', 'password', 'client', 'city', 'file', 'command', 'hassh'])
const exactly = (extra: Record<string, string>) =>
  Object.fromEntries(Object.entries(extra).map(([k, v]) => [k, typed.has(k) ? `"${v}"` : v]))

export function useStatsFilters() {
  const route = useRoute()
  const q = (k: string) => queryValue(route.query, k)
  /** Parameters of the /api/stats calls. */
  const params = computed(() => ({ sensor: q('sensor'), verdict: q('verdict'), login: q('login'), ...dayRange(q('from'), q('to')) }))
  /** A link to a page with the same filters, plus its own (and its route's params). */
  function link(name: string, extra: Record<string, string> = {}, params: Record<string, string> = {}) {
    const query: Record<string, string> = {}
    for (const k of keys) {
      if (q(k)) query[k] = q(k)
    }
    return { name, params, query: { ...query, ...exactly(extra) } }
  }
  return { params, link, q }
}

/** The parameters of the /api/stats calls, as useStatsFilters gives them. */
export type StatsParams = ReturnType<typeof useStatsFilters>['params']['value']

/**
 * Loads what the statistics filters select, and again on each change of
 * them. Filters changed quickly send several requests: only the answer to
 * the latest is kept, whatever the order they come back in. loading tells
 * that one is on its way, the former answer still shown.
 */
export function useStatsLoad<T>(load: (p: StatsParams) => Promise<T>) {
  const { params } = useStatsFilters()
  const data = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const error = ref<string | null>(null)
  let latest = 0
  watch(
    params,
    async (p) => {
      const n = ++latest
      loading.value = true
      try {
        const v = await load(p)
        if (n !== latest) return
        data.value = v
        error.value = null
      } catch (e) {
        if (n !== latest) return
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        if (n === latest) loading.value = false
      }
    },
    { immediate: true },
  )
  return { data, loading, error }
}
