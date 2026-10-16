<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import InputText from 'openvue/inputtext'
import Select from 'openvue/select'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import Chip from 'openvue/chip'
import { api } from '@/api/client'
import type { CountryCount } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import SessionTable from '@/components/SessionTable.vue'
import { dayRange } from '@/dates'
import { countryName } from '@/format'
import { patchQuery, queryValue } from '@/router'
import { notFilters, useTableSort } from '@/sort'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// Text filters are edited locally and applied on Enter or when leaving the field.
const text = ref({ ip: '', username: '', password: '', client: '' })
const q = (k: string) => queryValue(route.query, k)
watch(
  () => route.query,
  () => (text.value = { ip: q('ip'), username: q('username'), password: q('password'), client: q('client') }),
  { immediate: true },
)
function applyText() {
  void patchQuery({ ...text.value })
}

// Countries seen, for the filter: those of the selected sensor.
const countries = ref<CountryCount[]>([])
watch(
  () => q('sensor'),
  async (sensor) => {
    countries.value = await api.countries({ sensor }).catch(() => [])
  },
  { immediate: true },
)
const countryOptions = computed(() =>
  countries.value.map((c) => ({
    label: countryName(c.country), // shown when selected, searched by the filter
    value: c.country,
    count: c.sessions,
  })),
)
// A city comes from the map, with its country: changing the country drops it.
const country = computed({
  get: () => q('country') || null,
  set: (v: string | null) => void patchQuery({ country: v ?? undefined, city: undefined }),
})

const login = computed({
  get: () => q('login') || 'any',
  set: (v: string) => void patchQuery({ login: v === 'any' ? undefined : v }),
})
const loginOptions = computed(() => [
  { label: t('sessions.filters.anyOutcome'), value: 'any' },
  { label: t('sessions.filters.loginSuccess'), value: 'success' },
  { label: t('sessions.filters.loginFailed'), value: 'failed' },
])
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
const withCommands = computed({
  get: () => q('has_commands') === 'true',
  set: (v: boolean) => void patchQuery({ has_commands: v ? 'true' : undefined }),
})
const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const filtered = computed(() => Object.keys(route.query).some((k) => !notFilters.includes(k)))
const { kept } = useTableSort('started')

// The API parameters of the filters, shared by the list and its export.
const filters = computed(() => ({
  sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
  client: q('client'), country: q('country'), city: q('city'), file: q('file'), asn: q('asn'),
  command: q('command'), hassh: q('hassh'), login: q('login'), verdict: q('verdict'),
  has_commands: q('has_commands'),
  ...dayRange(q('from'), q('to')),
}))
const csvUrl = computed(() => api.sessionsCsvUrl(filters.value))

const total = ref(0)

function clearFilters() {
  void router.replace({ query: kept.value })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('sessions.title') }}</h1>
      <span class="muted">
        {{ t('sessions.count', { n: total.toLocaleString() }, total) }}
        <a
          :href="csvUrl"
          download
          class="export"
        >{{ t('export.csv') }}</a>
      </span>
    </div>

    <div
      class="filter-rows"
      role="search"
    >
      <div class="filters">
        <InputText
          v-model="text.ip"
          :placeholder="t('filters.sourceIp')"
          :aria-label="t('filters.sourceIp')"
          @keyup.enter="applyText"
          @blur="applyText"
        />
        <InputText
          v-model="text.username"
          :placeholder="t('sessions.filters.username')"
          :aria-label="t('sessions.filters.username')"
          @keyup.enter="applyText"
          @blur="applyText"
        />
        <InputText
          v-model="text.password"
          :placeholder="t('sessions.filters.password')"
          :aria-label="t('sessions.filters.password')"
          @keyup.enter="applyText"
          @blur="applyText"
        />
        <InputText
          v-model="text.client"
          :placeholder="t('sessions.filters.client')"
          :aria-label="t('sessions.filters.clientLabel')"
          @keyup.enter="applyText"
          @blur="applyText"
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
          v-if="q('city')"
          removable
          :label="q('city')"
          :aria-label="t('sessions.filters.city', { city: q('city') })"
          @remove="patchQuery({ city: undefined })"
        />
        <Chip
          v-if="q('command')"
          removable
          class="data"
          :label="q('command')"
          :aria-label="t('sessions.filters.command', { command: q('command') })"
          @remove="patchQuery({ command: undefined })"
        />
        <Chip
          v-if="q('hassh')"
          removable
          :label="t('sessions.filters.hasshChip', { hassh: q('hassh').slice(0, 12) })"
          :aria-label="t('sessions.filters.hassh', { hassh: q('hassh') })"
          @remove="patchQuery({ hassh: undefined })"
        />
        <Chip
          v-if="q('asn')"
          removable
          :label="`AS${q('asn')}`"
          :aria-label="t('sessions.filters.asn', { asn: q('asn') })"
          @remove="patchQuery({ asn: undefined })"
        />
        <Chip
          v-if="q('file')"
          removable
          :label="t('sessions.filters.fileChip', { sha: q('file').slice(0, 12) })"
          :aria-label="t('sessions.filters.file', { sha: q('file') })"
          @remove="patchQuery({ file: undefined })"
        />
        <DayRange
          v-model:from="from"
          v-model:to="to"
        />
      </div>
      <div class="filters">
        <SelectButton
          v-model="login"
          :options="loginOptions"
          option-label="label"
          option-value="value"
          :allow-empty="false"
          :aria-label="t('sessions.filters.outcome')"
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
          <ToggleSwitch v-model="withCommands" />
          {{ t('sessions.filters.typedCommands') }}
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

    <SessionTable
      v-model:total="total"
      :params="filters"
      :filtered="filtered"
    />
  </div>
</template>

<style scoped>
.count {
  margin-left: auto;
  padding-left: var(--space-3);
}
</style>
