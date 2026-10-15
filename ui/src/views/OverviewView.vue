<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import ToggleSwitch from 'openvue/toggleswitch'
import { api } from '@/api/client'
import type { CityCount, CountryCount, Origins, Summary } from '@/api/types'
import BarList from '@/components/BarList.vue'
import CountryFlag from '@/components/CountryFlag.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KpiCard from '@/components/KpiCard.vue'
import LiveFeed from '@/components/LiveFeed.vue'
import StatsFilters from '@/components/StatsFilters.vue'
import WorldMap from '@/components/WorldMap.vue'
import { patchQuery } from '@/router'
import { useStatsFilters } from '@/stats'

const { t } = useI18n()
const router = useRouter()
const { params, link, q } = useStatsFilters()

// Cities are shown unless hidden: the choice is kept in the URL too.
const showCities = computed({
  get: () => q('cities') !== 'hide',
  set: (v: boolean) => void patchQuery({ cities: v ? undefined : 'hide' }),
})

const summary = ref<Summary | null>(null)
const countries = ref<CountryCount[]>([])
const cities = ref<CityCount[]>([])
const origins = ref<Origins | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
watch(
  params,
  async (p) => {
    loading.value = true
    try {
      ;[summary.value, countries.value, cities.value, origins.value] = await Promise.all([
        api.summary(p), api.countries(p), api.cities(p), api.origins(p),
      ])
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)
const located = computed(() => countries.value.reduce((n, c) => n + c.sessions, 0))
// The overview shows the first few of each ranking, so that its cards
// stay of a size and none scrolls: the pages of each statistic list more.
const TOP = 10
const top = computed(() => countries.value.slice(0, TOP))
const accepted = computed(() => {
  const s = summary.value
  return s && s.logins ? Math.round((1000 * s.accepted) / s.logins) / 10 : 0
})

// Without a GeoIP database no session is located: say how to get one.
const geoip = ref(true)
onMounted(() => {
  void api.about().then((a) => (geoip.value = a.geoip !== null)).catch(() => undefined)
})

// A country or a city leads to its sessions, with the same filters.
function sessionsOf(country: string, city?: string) {
  return link('sessions', city ? { country, city } : { country })
}
function open(country: string, city?: string) {
  void router.push(sessionsOf(country, city))
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('overview.title') }}</h1>
      <span class="muted">{{ t('overview.summary', { n: located.toLocaleString(), c: countries.length }, located) }}</span>
    </div>

    <StatsFilters />

    <ErrorNotice :error="error" />

    <div
      v-if="summary"
      class="kpis"
    >
      <KpiCard
        :label="t('stats.sessions')"
        :value="summary.sessions"
        :previous="summary.previous?.sessions"
      />
      <KpiCard
        :label="t('stats.shells')"
        :value="summary.shells"
        :previous="summary.previous?.shells"
      />
      <KpiCard
        :label="t('stats.logins')"
        :value="summary.logins"
        :detail="t('stats.acceptedShare', { n: accepted })"
        :previous="summary.previous?.logins"
      />
      <KpiCard
        :label="t('stats.sourceIps')"
        :value="summary.source_ips"
        :previous="summary.previous?.source_ips"
      />
      <KpiCard
        :label="t('stats.countries')"
        :value="summary.countries"
      />
    </div>

    <p
      v-if="!geoip && !countries.length"
      class="notice card"
    >
      {{ t('overview.noGeoip') }}
    </p>

    <div class="layout">
      <section
        class="card"
        :aria-busy="loading"
      >
        <div class="map-head">
          <h2>{{ t('overview.map.title') }}</h2>
          <label class="toggle">
            <ToggleSwitch v-model="showCities" />
            {{ t('overview.map.cities') }}
          </label>
        </div>
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
        <BarList
          :items="top"
          :value="(c) => c.sessions"
          :item-key="(c) => c.country"
          :to="(c) => sessionsOf(c.country)"
        >
          <template #label="{ item }">
            <CountryFlag :code="item.country" />
          </template>
          <template #value="{ item }">
            {{ item.sessions.toLocaleString() }}
            <span
              v-if="item.shells"
              class="shell"
              :title="t('overview.shells', { n: item.shells.toLocaleString() }, item.shells)"
            > · {{ item.shells.toLocaleString() }}</span>
          </template>
        </BarList>
      </section>
    </div>

    <div class="panels below">
      <LiveFeed
        :sensor="q('sensor')"
        :verdict="q('verdict')"
      />
      <section
        v-if="origins?.sources.length"
        class="card"
      >
        <h2>{{ t('overview.sources') }}</h2>
        <BarList
          :items="origins.sources.slice(0, TOP)"
          :value="(s) => s.sessions"
          :item-key="(s) => s.ip"
          :to="(s) => link('sessions', { ip: s.ip })"
        >
          <template #label="{ item }">
            <span class="data">{{ item.ip }}</span>
            <CountryFlag
              v-if="item.country"
              :code="item.country"
              class="muted country"
            />
          </template>
        </BarList>
      </section>
      <section
        v-if="origins?.networks.length"
        class="card"
      >
        <h2>{{ t('overview.networks') }}</h2>
        <BarList
          :items="origins.networks.slice(0, TOP)"
          :value="(n) => n.sessions"
          :item-key="(n) => String(n.asn)"
          :to="(n) => link('sessions', { asn: String(n.asn) })"
        >
          <template #label="{ item }">
            <span class="data">AS{{ item.asn }}</span>
            <span class="muted org">{{ item.org }}</span>
          </template>
        </BarList>
      </section>
      <section
        v-if="origins?.clients.length"
        class="card"
      >
        <h2>{{ t('overview.clients') }}</h2>
        <BarList
          :items="origins.clients.slice(0, TOP)"
          :value="(c) => c.sessions"
          :item-key="(c) => c.client"
          :to="(c) => link('sessions', { client: c.client })"
        >
          <template #label="{ item }">
            <span class="data">{{ item.client }}</span>
          </template>
        </BarList>
      </section>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 20rem;
  gap: var(--space-4);
}

@media (width <= 900px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

/* The map's own quick filters, beside its title. */
.map-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
  margin-bottom: var(--space-3);
}

.map-head > h2 {
  font-size: 1rem;
}

.notice {
  margin: 0 0 var(--space-4);
}

/* Cards of a row take the height of the tallest. */
.below {
  align-items: stretch;
  margin-top: var(--space-4);
}

.country,
.org {
  margin-left: var(--space-2);
  font-size: 0.9em;
}
</style>
