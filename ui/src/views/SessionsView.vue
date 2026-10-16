<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DataTable, { type DataTablePageEvent, type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import Select from 'openvue/select'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import Chip from 'openvue/chip'
import { api } from '@/api/client'
import type { CountryCount, SessionSummary } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { dayRange } from '@/dates'
import { countryName, endpoint, formatMillis, formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'
import { notFilters, useTableSort } from '@/sort'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const rows = 50

// Text filters are edited locally and applied on Enter or when leaving the field.
// Characters of the SSH client shown in the table ("SSH-2.0-libssh"); a
// longer one is cut, whole in a tooltip. Matches the column's CSS width.
const clientShown = 14

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
  { label: t('sessions.filters.shellObtained'), value: 'success' },
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
const page = computed(() => Math.max(Number(q('page')) || 1, 1))
const filtered = computed(() => Object.keys(route.query).some((k) => !notFilters.includes(k)))
const { params: sortParams, kept, field: sortField, direction, onSort } = useTableSort('started')

// The API parameters of the filters, shared by the list and its export.
const filters = computed(() => ({
  sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
  client: q('client'), country: q('country'), city: q('city'), file: q('file'), asn: q('asn'),
  command: q('command'), hassh: q('hassh'), login: q('login'), verdict: q('verdict'),
  has_commands: q('has_commands'),
  ...dayRange(q('from'), q('to')),
}))
const csvUrl = computed(() => api.sessionsCsvUrl(filters.value))

const items = ref<SessionSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const res = await api.sessions({ ...filters.value, ...sortParams.value, limit: rows, offset: (page.value - 1) * rows })
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

function onPage(e: DataTablePageEvent) {
  void patchQuery({ page: e.page > 0 ? String(e.page + 1) : undefined }, true)
}
function open(e: DataTableRowClickEvent<SessionSummary>) {
  void router.push({ name: 'session', params: { id: String(e.data.id) } })
}
// The credentials that opened a shell come first: they are what matters.
function shown(s: SessionSummary) {
  return [...s.credentials].sort((a, b) => Number(b.success) - Number(a.success)).slice(0, 3)
}
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

    <ErrorNotice :error="error" />

    <DataTable
      :value="items"
      :loading="loading"
      lazy
      paginator
      :rows="rows"
      :first="(page - 1) * rows"
      :total-records="total"
      data-key="id"
      size="small"
      striped-rows
      scrollable
      :sort-field="sortField"
      :sort-order="direction"
      @page="onPage"
      @sort="onSort"
      @row-click="open"
    >
      <template #empty>
        <p
          v-if="filtered"
          class="muted"
        >
          {{ t('sessions.empty.filtered') }}
        </p>
        <p
          v-else
          class="muted"
        >
          {{ t('sessions.empty.none') }}
        </p>
      </template>
      <Column
        field="started"
        :header="t('sessions.columns.started')"
        sortable
      >
        <template #body="{ data }">
          <RouterLink
            :to="{ name: 'session', params: { id: String(data.id) } }"
            class="time"
            @click.stop
          >
            {{ formatTime(data.started_at) }}
          </RouterLink>
        </template>
      </Column>
      <Column
        field="sensor"
        sortable
        :header="t('sessions.columns.sensor')"
      />
      <Column
        field="source"
        :header="t('sessions.columns.source')"
        sortable
      >
        <template #body="{ data }">
          <RouterLink
            v-if="data.src_ip"
            :to="{ name: 'attacker', params: { ip: data.src_ip } }"
            class="data"
            @click.stop
          >
            {{ endpoint(data.src_ip, data.src_port) }}
          </RouterLink>
          <span
            v-else
            class="data"
          >{{ endpoint(data.src_ip, data.src_port) }}</span>
        </template>
      </Column>
      <Column :header="t('sessions.columns.credentials')">
        <template #body="{ data }">
          <span
            v-for="(c, i) in shown(data)"
            :key="i"
            class="data cred"
            :class="{ shell: c.success }"
          >{{ c.username }} / {{ c.password }}</span>
          <span
            v-if="data.login_attempts > 3"
            class="muted"
          >{{ t('sessions.moreCredentials', { n: data.login_attempts - 3 }) }}</span>
        </template>
      </Column>
      <Column
        field="country"
        :header="t('sessions.columns.country')"
        sortable
      >
        <template #body="{ data }">
          <CountryFlag
            v-if="data.geo_country"
            :code="data.geo_country"
            :title="[data.geo_country, data.geo_city].filter(Boolean).join(', ')"
          />
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column
        field="duration"
        :header="t('sessions.columns.duration')"
        sortable
      >
        <template #body="{ data }">
          {{ formatMillis(data.duration_ms) }}
        </template>
      </Column>
      <Column
        field="commands"
        :header="t('sessions.columns.commands')"
        sortable
        class="num"
      >
        <template #body="{ data }">
          {{ data.commands || '—' }}
        </template>
      </Column>
      <Column
        field="verdict"
        :header="t('sessions.columns.verdict')"
        sortable
      >
        <template #body="{ data }">
          <span
            v-if="data.verdict"
            class="badge"
            :class="'badge-' + data.verdict"
          >{{ t('session.verdict.' + data.verdict) }}</span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column :header="t('sessions.columns.outcome')">
        <template #body="{ data }">
          <span
            v-if="data.login_success"
            class="shell"
          >{{ t('sessions.outcome.shell') }}</span>
          <span
            v-else-if="data.login_attempts"
            class="muted"
          >{{ t('sessions.outcome.refused') }}</span>
          <span
            v-else
            class="muted"
          >{{ t('sessions.outcome.noLogin') }}</span>
        </template>
      </Column>
      <!-- Last: a detail, cut after "SSH-2.0-libssh", whole on hover. -->
      <Column
        field="client"
        :header="t('sessions.columns.client')"
        sortable
      >
        <template #body="{ data }">
          <span
            v-tooltip.top="(data.client_version?.length ?? 0) > clientShown ? data.client_version : null"
            class="data client"
          >{{ data.client_version ?? '—' }}</span>
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.time {
  white-space: nowrap;
  text-decoration: none;
}

.time:hover {
  text-decoration: underline;
}

.cred {
  display: inline-block;
  margin-right: var(--space-3);
  white-space: nowrap;
}

.count {
  margin-left: auto;
  padding-left: var(--space-3);
}

/* Room for "SSH-2.0-libssh" (clientShown characters), then cut. */
.client {
  display: inline-block;
  max-width: 15ch;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

:deep(.num) {
  text-align: right;
}
</style>
