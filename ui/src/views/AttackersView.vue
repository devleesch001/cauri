<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DataTable, { type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import Select from 'openvue/select'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import Chip from 'openvue/chip'
import { api } from '@/api/client'
import type { Attacker, CountryCount } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { dayRange } from '@/dates'
import { countryName, formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'
import { notFilters, pageSizes, useTablePage, useTableSort } from '@/sort'

// Attackers: the source addresses sessions came from, with what came from
// each. A row opens the attacker's profile, with the same period and verdict.
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const q = (k: string) => queryValue(route.query, k)

// The address prefix is edited locally and applied on Enter or leaving the field.
const prefix = ref('')
watch(
  () => route.query,
  () => (prefix.value = q('ip_prefix')),
  { immediate: true },
)
function applyPrefix() {
  void patchQuery({ ip_prefix: prefix.value.trim() || undefined })
}

const countries = ref<CountryCount[]>([])
watch(
  () => q('sensor'),
  async (sensor) => {
    countries.value = await api.countries({ sensor }).catch(() => [])
  },
  { immediate: true },
)
const countryOptions = computed(() =>
  countries.value.map((c) => ({ label: countryName(c.country), value: c.country, count: c.sessions })),
)
const country = computed({
  get: () => q('country') || null,
  set: (v: string | null) => void patchQuery({ country: v ?? undefined }),
})
// Bots include uncertain sessions and those without interaction (the API counts them so).
const verdict = computed({
  get: () => q('verdict') || 'any',
  set: (v: string) => void patchQuery({ verdict: v === 'any' ? undefined : v }),
})
const verdictOptions = computed(() => [
  { label: t('sessions.filters.anyVerdict'), value: 'any' },
  { label: t('sessions.filters.humans'), value: 'human' },
  { label: t('sessions.filters.bots'), value: 'bot' },
])
const loggedIn = computed({
  get: () => q('logged_in') === 'true',
  set: (v: boolean) => void patchQuery({ logged_in: v ? 'true' : undefined }),
})
const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const { rows, page, params: pageParams, onPage } = useTablePage(50)
const filtered = computed(() => Object.keys(route.query).some((k) => !notFilters.includes(k)))
const { params: sortParams, kept, field: sortField, direction, onSort } = useTableSort('sessions')

const items = ref<Attacker[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const res = await api.attackers({
        sensor: q('sensor'), verdict: q('verdict'), country: q('country'), asn: q('asn'),
        ip_prefix: q('ip_prefix'), logged_in: q('logged_in'), ...sortParams.value,
        ...dayRange(q('from'), q('to')),
        ...pageParams.value,
      })
      items.value = res.items
      total.value = res.total
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

function open(e: DataTableRowClickEvent<Attacker>) {
  const query: Record<string, string> = {}
  for (const k of ['sensor', 'from', 'to', 'verdict']) {
    if (q(k)) query[k] = q(k)
  }
  void router.push({ name: 'attacker', params: { ip: e.data.ip }, query })
}
// The verdict most of its sessions got.
function mostly(a: Attacker) {
  const counts: [string, number][] = [
    ['human', a.humans], ['bot', a.bots], ['uncertain', a.uncertain], ['no_interaction', a.no_interaction],
  ]
  const [verdict, n] = counts.reduce((best, c) => (c[1] > best[1] ? c : best))
  return n > 0 ? verdict : null
}
function clearFilters() {
  void router.replace({ query: kept.value })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('attackers.title') }}</h1>
      <span class="muted">{{ t('attackers.count', { n: total.toLocaleString() }, total) }}</span>
    </div>

    <div
      class="filter-rows"
      role="search"
    >
      <div class="filters">
        <InputText
          v-model="prefix"
          class="data"
          :placeholder="t('attackers.filters.ipPrefix')"
          :aria-label="t('attackers.filters.ipPrefix')"
          @keyup.enter="applyPrefix"
          @blur="applyPrefix"
        />
        <Select
          v-model="country"
          :options="countryOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('sessions.filters.country')"
          :aria-label="t('sessions.columns.country')"
          show-clear
          filter
        >
          <template #value="{ value, placeholder }">
            <CountryFlag
              v-if="value"
              :code="value"
            />
            <span v-else>{{ placeholder }}</span>
          </template>
          <template #option="{ option }">
            <CountryFlag :code="option.value" />
            <span class="muted count">{{ option.count }}</span>
          </template>
        </Select>
        <Chip
          v-if="q('asn')"
          removable
          :label="`AS${q('asn')}`"
          :aria-label="t('sessions.filters.asn', { asn: q('asn') })"
          @remove="patchQuery({ asn: undefined })"
        />
        <DayRange
          v-model:from="from"
          v-model:to="to"
        />
      </div>
      <div class="filters">
        <SelectButton
          v-model="verdict"
          :options="verdictOptions"
          option-label="label"
          option-value="value"
          :allow-empty="false"
          :aria-label="t('sessions.filters.verdict')"
        />
        <label class="toggle">
          <ToggleSwitch v-model="loggedIn" />
          {{ t('attackers.filters.loggedIn') }}
        </label>
        <Button
          v-if="filtered"
          :label="t('filters.clear')"
          severity="secondary"
          text
          @click="clearFilters"
        />
      </div>
    </div>

    <ErrorNotice :error="error" />

    <DataTable
      :value="items"
      :loading="loading"
      lazy
      paginator
      :rows="rows"
      :rows-per-page-options="pageSizes"
      :first="(page - 1) * rows"
      :total-records="total"
      :sort-field="sortField"
      :sort-order="direction"
      data-key="ip"
      size="small"
      striped-rows
      scrollable
      @page="onPage"
      @sort="onSort"
      @row-click="open"
    >
      <template #empty>
        <p class="muted">
          {{ filtered ? t('attackers.empty.filtered') : t('attackers.empty.none') }}
        </p>
      </template>
      <Column :header="t('attackers.columns.address')">
        <template #body="{ data }">
          <RouterLink
            :to="{ name: 'attacker', params: { ip: data.ip } }"
            class="data"
            @click.stop
          >
            {{ data.ip }}
          </RouterLink>
        </template>
      </Column>
      <Column :header="t('sessions.columns.country')">
        <template #body="{ data }">
          <CountryFlag
            v-if="data.country"
            :code="data.country"
          />
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column :header="t('attackers.columns.network')">
        <template #body="{ data }">
          <span
            v-if="data.asn"
            class="network"
          >AS{{ data.asn }} <span class="muted">{{ data.as_org }}</span></span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column
        field="sessions"
        :header="t('attackers.columns.sessions')"
        sortable
        class="num"
      >
        <template #body="{ data }">
          {{ data.sessions.toLocaleString() }}
        </template>
      </Column>
      <Column
        field="logged_in"
        :header="t('attackers.columns.loggedIn')"
        sortable
        class="num"
      >
        <template #body="{ data }">
          <span
            v-if="data.logged_in"
            class="logged-in"
          >{{ data.logged_in.toLocaleString() }}</span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column
        :header="t('attackers.columns.logins')"
        class="num"
      >
        <template #body="{ data }">
          {{ data.logins ? data.logins.toLocaleString() : '—' }}
        </template>
      </Column>
      <Column :header="t('attackers.columns.firstSeen')">
        <template #body="{ data }">
          {{ formatTime(data.first_seen) }}
        </template>
      </Column>
      <Column
        field="last_seen"
        :header="t('attackers.columns.lastSeen')"
        sortable
      >
        <template #body="{ data }">
          {{ formatTime(data.last_seen) }}
        </template>
      </Column>
      <Column :header="t('attackers.columns.verdict')">
        <template #body="{ data }">
          <span
            v-if="mostly(data)"
            class="badge"
            :class="'badge-' + mostly(data)"
          >{{ t('session.verdict.' + mostly(data)) }}</span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.count {
  margin-left: auto;
  padding-left: var(--space-3);
}

.network {
  white-space: nowrap;
}

:deep(.num) {
  text-align: right;
}
</style>
