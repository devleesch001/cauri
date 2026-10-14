<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent, type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import { api } from '@/api/client'
import type { SessionSummary } from '@/api/types'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { dayRange } from '@/dates'
import { endpoint, formatDuration, formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'

const route = useRoute()
const router = useRouter()
const rows = 50

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

const login = computed({
  get: () => q('login') || 'any',
  set: (v: string) => void patchQuery({ login: v === 'any' ? undefined : v }),
})
const loginOptions = [
  { label: 'Any outcome', value: 'any' },
  { label: 'Shell obtained', value: 'success' },
  { label: 'Login failed', value: 'failed' },
]
const withCommands = computed({
  get: () => q('has_commands') === 'true',
  set: (v: boolean) => void patchQuery({ has_commands: v ? 'true' : undefined }),
})
const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const page = computed(() => Math.max(Number(q('page')) || 1, 1))
const filtered = computed(() => Object.keys(route.query).some((k) => k !== 'page'))

const items = ref<SessionSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const res = await api.sessions({
        sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
        client: q('client'), login: q('login'), has_commands: q('has_commands'),
        ...dayRange(q('from'), q('to')),
        limit: rows, offset: (page.value - 1) * rows,
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

function onPage(e: DataTablePageEvent) {
  void patchQuery({ page: e.page > 0 ? String(e.page + 1) : undefined }, true)
}
function open(e: DataTableRowClickEvent<SessionSummary>) {
  void router.push({ name: 'session', params: { id: e.data.id } })
}
// The credentials that opened a shell come first: they are what matters.
function shown(s: SessionSummary) {
  return [...s.credentials].sort((a, b) => Number(b.success) - Number(a.success)).slice(0, 3)
}
function clearFilters() {
  void router.replace({ query: {} })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>Sessions</h1>
      <span class="muted">{{ total.toLocaleString() }} {{ total === 1 ? 'session' : 'sessions' }}</span>
    </div>

    <div
      class="filters"
      role="search"
    >
      <InputText
        v-model="text.ip"
        placeholder="Source IP"
        aria-label="Source IP"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <InputText
        v-model="text.username"
        placeholder="Username tried"
        aria-label="Username tried"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <InputText
        v-model="text.password"
        placeholder="Password tried"
        aria-label="Password tried"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <InputText
        v-model="text.client"
        placeholder="SSH client"
        aria-label="SSH client contains"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <DayRange
        v-model:from="from"
        v-model:to="to"
      />
      <SelectButton
        v-model="login"
        :options="loginOptions"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        aria-label="Login outcome"
      />
      <label class="toggle">
        <ToggleSwitch v-model="withCommands" />
        Typed commands
      </label>
      <Button
        v-if="filtered"
        label="Clear filters"
        severity="secondary"
        text
        @click="clearFilters"
      />
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
      @page="onPage"
      @row-click="open"
    >
      <template #empty>
        <p
          v-if="filtered"
          class="muted"
        >
          No session matches these filters.
        </p>
        <p
          v-else
          class="muted"
        >
          No session yet. Sessions appear here once an ingestor sends Cowrie events to this API.
        </p>
      </template>
      <Column header="Started">
        <template #body="{ data }">
          <RouterLink
            :to="{ name: 'session', params: { id: data.id } }"
            class="time"
            @click.stop
          >
            {{ formatTime(data.started_at) }}
          </RouterLink>
        </template>
      </Column>
      <Column
        field="sensor_id"
        header="Sensor"
      />
      <Column header="Source">
        <template #body="{ data }">
          <span class="data">{{ endpoint(data.src_ip, data.src_port) }}</span>
        </template>
      </Column>
      <Column header="Credentials tried">
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
          >+{{ data.login_attempts - 3 }} more</span>
        </template>
      </Column>
      <Column header="SSH client">
        <template #body="{ data }">
          <span class="data client">{{ data.client_version ?? '—' }}</span>
        </template>
      </Column>
      <Column header="Duration">
        <template #body="{ data }">
          {{ formatDuration(data.duration) }}
        </template>
      </Column>
      <Column
        header="Commands"
        class="num"
      >
        <template #body="{ data }">
          {{ data.commands || '—' }}
        </template>
      </Column>
      <Column header="Outcome">
        <template #body="{ data }">
          <span
            v-if="data.login_success"
            class="shell"
          >Shell</span>
          <span
            v-else-if="data.login_attempts"
            class="muted"
          >Refused</span>
          <span
            v-else
            class="muted"
          >No login</span>
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--c-muted);
}

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

.client {
  display: inline-block;
  max-width: 16rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

:deep(.num) {
  text-align: right;
}
</style>
