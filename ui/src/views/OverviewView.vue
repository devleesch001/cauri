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
import FloatingPanel from '@/components/FloatingPanel.vue'
import KpiPanel from '@/components/KpiPanel.vue'
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

// Off by default: every session; on, only those whose login succeeded.
const loginSuccess = computed({
  get: () => q('login') === 'success',
  set: (v: boolean) => void patchQuery({ login: v ? 'success' : undefined }),
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

// The rankings are an accordion: one open at a time, none at first.
const opened = ref<string | null>(null)
const toggle = (key: string) => (opened.value = opened.value === key ? null : key)

// A country or a city leads to its sessions, with the same filters.
function sessionsOf(country: string, city?: string) {
  return link('sessions', city ? { country, city } : { country })
}
function open(country: string, city?: string) {
  void router.push(sessionsOf(country, city))
}
</script>

<template>
  <div class="scene">
    <h1 class="sr-only">
      {{ t('overview.title') }}
    </h1>

    <WorldMap
      class="map"
      :countries="countries"
      :cities="showCities ? cities : []"
      :aria-busy="loading"
      @select="open"
      @select-city="open"
    />

    <KpiPanel
      v-if="summary"
      class="corner"
      :summary="summary"
      :accepted="accepted"
      :located="located"
    />

    <div
      class="docks"
      :class="{ wide: opened }"
    >
      <FloatingPanel
        v-if="top.length"
        :title="t('overview.ranking.title')"
        :open="opened === 'countries'"
        @toggle="toggle('countries')"
      >
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
              :title="t('overview.loggedIn', { n: item.shells.toLocaleString() }, item.shells)"
            > · {{ item.shells.toLocaleString() }}</span>
          </template>
        </BarList>
      </FloatingPanel>
      <FloatingPanel
        :title="t('overview.live.title')"
        :open="opened === 'live'"
        @toggle="toggle('live')"
      >
        <LiveFeed
          :sensor="q('sensor')"
          :verdict="q('verdict')"
        />
      </FloatingPanel>
      <FloatingPanel
        v-if="origins?.sources.length"
        :title="t('overview.sources')"
        :open="opened === 'sources'"
        @toggle="toggle('sources')"
      >
        <BarList
          :items="origins.sources.slice(0, TOP)"
          :value="(s) => s.sessions"
          :item-key="(s) => s.ip"
          :to="(s) => ({ ...link('attacker'), params: { ip: s.ip } })"
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
      </FloatingPanel>
      <FloatingPanel
        v-if="origins?.networks.length"
        :title="t('overview.networks')"
        :open="opened === 'networks'"
        @toggle="toggle('networks')"
      >
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
      </FloatingPanel>
      <FloatingPanel
        v-if="origins?.clients.length"
        :title="t('overview.clients')"
        :open="opened === 'clients'"
        @toggle="toggle('clients')"
      >
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
      </FloatingPanel>
    </div>

    <div class="bottom">
      <StatsFilters>
        <label class="toggle">
          <ToggleSwitch v-model="loginSuccess" />
          {{ t('sessions.filters.loginSuccess') }}
        </label>
        <label class="toggle">
          <ToggleSwitch v-model="showCities" />
          {{ t('overview.map.cities') }}
        </label>
      </StatsFilters>
    </div>

    <div class="notices">
      <ErrorNotice :error="error" />
      <p
        v-if="!geoip && !countries.length"
        class="notice card float"
      >
        {{ t('overview.noGeoip') }}
      </p>
      <p
        v-else-if="geoip && !loading && !countries.length"
        class="notice card float muted"
      >
        {{ t('overview.empty') }}
      </p>
    </div>
  </div>
</template>

<style scoped>
/* The map fills the page; everything else floats over it. */
.scene {
  position: relative;
  height: 100%;
  min-height: 34rem;
  overflow: hidden;
}

.corner {
  position: absolute;
  top: var(--space-3);
  left: var(--space-3);
}

.docks {
  position: absolute;
  top: var(--space-3);
  right: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  width: 15.5rem;
  max-height: calc(100% - 2 * var(--space-3) - 3.5rem);
  overflow-y: auto;
  transition: width 0.18s ease;
}

.docks.wide {
  width: 24rem;
}

@media (prefers-reduced-motion: reduce) {
  .docks {
    transition: none;
  }
}

/* Each control sits in a small card of its own over the map. */
.bottom {
  position: absolute;
  bottom: var(--space-3);
  left: var(--space-3);
  right: 24rem;
}

.bottom :deep(.filters) {
  margin: 0;
}

.bottom :deep(.filters > *) {
  display: flex;
  align-items: center;
  box-sizing: border-box;
  height: 2.9rem;
  padding: 3px;
  background: color-mix(in srgb, var(--c-surface) 90%, transparent);
  backdrop-filter: blur(6px);
  border: 1px solid var(--c-rule);
  border-radius: 8px;
  box-shadow: 0 2px 10px rgb(0 0 0 / 8%);
}

/* The card is the frame: the field inside needs none of its own. */
.bottom :deep(.filters .p-inputtext) {
  border-color: transparent;
  box-shadow: none;
  background: transparent;
}

.bottom :deep(.filters .toggle) {
  padding: 3px var(--space-3);
}

/* The clear button is a card like the others, not a bare text button. */
.bottom :deep(.filters > .p-button) {
  padding: 3px var(--space-3);
  color: var(--c-ink);
}

.notices {
  position: absolute;
  top: var(--space-3);
  left: 50%;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  width: min(32rem, calc(100% - 32rem));
  transform: translateX(-50%);
}

.notice {
  margin: 0;
}

.toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.country,
.org {
  margin-left: var(--space-2);
  font-size: 0.9em;
}

/* On a phone the page scrolls: the pieces follow one another, the map
   keeping a height of its own. */
@media (width <= 900px) {
  .scene {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    height: auto;
    padding: var(--space-3);
    overflow: visible;
  }

  .map {
    order: 2;
    height: 55dvh;
  }

  .corner,
  .docks,
  .docks.wide,
  .bottom,
  .notices {
    position: static;
    width: auto;
    transform: none;
  }

  .corner {
    order: 1;
  }

  .bottom {
    order: 1;
  }

  .docks {
    order: 3;
    max-height: none;
    overflow: visible;
  }

  .notices {
    order: 0;
  }
}
</style>
