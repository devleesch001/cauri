<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import { api } from '@/api/client'
import type { CityCount, CountryCount } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import WorldMap from '@/components/WorldMap.vue'
import { dayRange } from '@/dates'
import { patchQuery, queryValue } from '@/router'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const q = (k: string) => queryValue(route.query, k)

const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const verdict = computed({
  get: () => q('verdict') || 'any',
  set: (v: string) => void patchQuery({ verdict: v === 'any' ? undefined : v }),
})
const verdictOptions = computed(() => [
  { label: t('sessions.filters.anyVerdict'), value: 'any' },
  { label: t('sessions.filters.humans'), value: 'human' },
  { label: t('sessions.filters.bots'), value: 'bot' },
])
// Cities are shown unless hidden: the choice is kept in the URL too.
const showCities = computed({
  get: () => q('cities') !== 'hide',
  set: (v: boolean) => void patchQuery({ cities: v ? undefined : 'hide' }),
})
const filtered = computed(() => ['from', 'to', 'verdict'].some((k) => q(k)))

const countries = ref<CountryCount[]>([])
const cities = ref<CityCount[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const params = { sensor: q('sensor'), verdict: q('verdict'), ...dayRange(q('from'), q('to')) }
      ;[countries.value, cities.value] = await Promise.all([api.countries(params), api.cities(params)])
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)
const total = computed(() => countries.value.reduce((n, c) => n + c.sessions, 0))
const top = computed(() => countries.value.slice(0, 20))
const most = computed(() => top.value[0]?.sessions ?? 1)

// Without a GeoIP database no session is located: say how to get one.
const geoip = ref(true)
onMounted(() => {
  void api.about().then((a) => (geoip.value = a.geoip !== null)).catch(() => undefined)
})

// A country or a city leads to its sessions, with the same filters.
function sessionsOf(country: string, city?: string) {
  const query: Record<string, string> = { country }
  if (city) query.city = city
  for (const k of ['sensor', 'from', 'to', 'verdict']) {
    if (q(k)) query[k] = q(k)
  }
  return { name: 'sessions', query }
}
function open(country: string, city?: string) {
  void router.push(sessionsOf(country, city))
}
function clearFilters() {
  void patchQuery({ from: undefined, to: undefined, verdict: undefined })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('overview.title') }}</h1>
      <span class="muted">{{ t('overview.summary', { n: total.toLocaleString(), c: countries.length }, total) }}</span>
    </div>

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
        :options="verdictOptions"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('sessions.filters.verdict')"
      />
      <label class="toggle">
        <ToggleSwitch v-model="showCities" />
        {{ t('overview.map.cities') }}
      </label>
      <Button
        v-if="filtered"
        :label="t('filters.clear')"
        severity="secondary"
        text
        @click="clearFilters"
      />
    </div>

    <ErrorNotice :error="error" />
    <p
      v-if="!geoip && !countries.length"
      class="notice"
    >
      {{ t('overview.noGeoip') }}
    </p>

    <div class="layout">
      <section
        class="card map-card"
        :aria-busy="loading"
      >
        <h2>{{ t('overview.map.title') }}</h2>
        <WorldMap
          :countries="countries"
          :cities="showCities ? cities : []"
          @select="open"
          @select-city="open"
        />
        <p
          v-if="geoip && !loading && !countries.length"
          class="muted"
        >
          {{ t('overview.empty') }}
        </p>
      </section>

      <section
        v-if="top.length"
        class="card"
      >
        <h2>{{ t('overview.ranking.title') }}</h2>
        <table class="ranking">
          <thead>
            <tr>
              <th scope="col">
                {{ t('overview.ranking.country') }}
              </th>
              <th
                scope="col"
                class="num"
              >
                {{ t('overview.ranking.sessions') }}
              </th>
              <th
                scope="col"
                class="num"
              >
                {{ t('overview.ranking.shells') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="c in top"
              :key="c.country"
            >
              <td>
                <RouterLink :to="sessionsOf(c.country)">
                  <CountryFlag :code="c.country" />
                </RouterLink>
                <span
                  class="bar"
                  :style="{ width: `${(100 * c.sessions) / most}%` }"
                  aria-hidden="true"
                />
              </td>
              <td class="num">
                {{ c.sessions.toLocaleString() }}
              </td>
              <td class="num">
                <span
                  v-if="c.shells"
                  class="shell"
                >{{ c.shells.toLocaleString() }}</span>
                <span
                  v-else
                  class="muted"
                >—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </section>
    </div>
  </div>
</template>

<style scoped>
.toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--c-muted);
}

.layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 20rem;
  gap: var(--space-4);
  align-items: start;
}

@media (width <= 900px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

.card {
  padding: var(--space-3);
  background: var(--c-surface);
  border: 1px solid var(--c-rule);
  border-radius: 8px;
}

h2 {
  margin: 0 0 var(--space-3);
  font-size: 1rem;
}

.notice {
  padding: var(--space-3);
  background: var(--c-surface);
  border: 1px solid var(--c-rule);
  border-radius: 8px;
}

.ranking {
  width: 100%;
  border-collapse: collapse;
}

.ranking th {
  font-weight: 500;
  color: var(--c-muted);
  text-align: left;
}

.ranking th,
.ranking td {
  padding: var(--space-1) 0;
}

.ranking td:first-child {
  position: relative;
  padding-bottom: var(--space-2);
}

.ranking a {
  text-decoration: none;
}

.ranking a:hover {
  text-decoration: underline;
}

/* Share of the first country, under the name. */
.bar {
  position: absolute;
  bottom: 2px;
  left: 0;
  height: 3px;
  border-radius: 2px;
  background: var(--p-primary-color);
  opacity: 0.5;
}

.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
</style>
