<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent, type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import { api } from '@/api/client'
import type { LoginAttemptRow } from '@/api/types'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { dayRange } from '@/dates'
import { formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'

// Failed logins. By default only those of sessions that never obtained a
// shell: the credential spraying that leaves no other trace.
const route = useRoute()
const router = useRouter()
const rows = 100
const q = (k: string) => queryValue(route.query, k)

const text = ref({ ip: '', username: '', password: '' })
watch(
  () => route.query,
  () => (text.value = { ip: q('ip'), username: q('username'), password: q('password') }),
  { immediate: true },
)
function applyText() {
  void patchQuery({ ...text.value })
}

const withShells = computed({
  get: () => q('with_shells') === 'true',
  set: (v: boolean) => void patchQuery({ with_shells: v ? 'true' : undefined }),
})
const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const page = computed(() => Math.max(Number(q('page')) || 1, 1))
const filtered = computed(() => Object.keys(route.query).some((k) => k !== 'page'))

const items = ref<LoginAttemptRow[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const res = await api.loginAttempts({
        sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
        success: false, interactive: withShells.value ? undefined : false,
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
function open(e: DataTableRowClickEvent<LoginAttemptRow>) {
  void router.push({ name: 'session', params: { id: e.data.session_id } })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>Failed logins</h1>
      <span class="muted">{{ total.toLocaleString() }} {{ total === 1 ? 'attempt' : 'attempts' }}</span>
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
        placeholder="Username"
        aria-label="Username"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <InputText
        v-model="text.password"
        placeholder="Password"
        aria-label="Password"
        @keyup.enter="applyText"
        @blur="applyText"
      />
      <DayRange
        v-model:from="from"
        v-model:to="to"
      />
      <label class="toggle">
        <ToggleSwitch v-model="withShells" />
        Include sessions that later got a shell
      </label>
      <Button
        v-if="filtered"
        label="Clear filters"
        severity="secondary"
        text
        @click="router.replace({ query: {} })"
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
        <p class="muted">
          {{ filtered ? 'No failed login matches these filters.' : 'No failed login yet.' }}
        </p>
      </template>
      <Column header="Time">
        <template #body="{ data }">
          <RouterLink
            :to="{ name: 'session', params: { id: data.session_id } }"
            class="time"
            @click.stop
          >
            {{ formatTime(data.ts) }}
          </RouterLink>
        </template>
      </Column>
      <Column
        field="sensor_id"
        header="Sensor"
      />
      <Column header="Source IP">
        <template #body="{ data }">
          <span class="data">{{ data.src_ip ?? '—' }}</span>
        </template>
      </Column>
      <Column header="Username">
        <template #body="{ data }">
          <span class="data">{{ data.username }}</span>
        </template>
      </Column>
      <Column header="Password">
        <template #body="{ data }">
          <span class="data">{{ data.password }}</span>
        </template>
      </Column>
      <Column header="SSH client">
        <template #body="{ data }">
          <span class="data">{{ data.client_version ?? '—' }}</span>
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
</style>
