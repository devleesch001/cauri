<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DataTable, { type DataTablePageEvent, type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import { api } from '@/api/client'
import type { LoginAttemptRow } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { dayRange } from '@/dates'
import { formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'

// Failed logins. By default only those of sessions that never obtained a
// shell: the credential spraying that leaves no other trace.
const { t } = useI18n()
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

// The API parameters of the filters, shared by the list and its export.
const filters = computed(() => ({
  sensor: q('sensor'), ip: q('ip'), username: q('username'), password: q('password'),
  success: false, interactive: withShells.value ? undefined : false,
  ...dayRange(q('from'), q('to')),
}))
const csvUrl = computed(() => api.loginAttemptsCsvUrl(filters.value))

const items = ref<LoginAttemptRow[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => route.query,
  async () => {
    loading.value = true
    try {
      const res = await api.loginAttempts({ ...filters.value, limit: rows, offset: (page.value - 1) * rows })
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
  void router.push({ name: 'session', params: { id: String(e.data.session_id) } })
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('logins.title') }}</h1>
      <span class="muted">
        {{ t('logins.count', { n: total.toLocaleString() }, total) }}
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
          :placeholder="t('logins.username')"
          :aria-label="t('logins.username')"
          @keyup.enter="applyText"
          @blur="applyText"
        />
        <InputText
          v-model="text.password"
          :placeholder="t('logins.password')"
          :aria-label="t('logins.password')"
          @keyup.enter="applyText"
          @blur="applyText"
        />
        <DayRange
          v-model:from="from"
          v-model:to="to"
        />
      </div>
      <div class="filters">
        <label class="toggle">
          <ToggleSwitch v-model="withShells" />
          {{ t('logins.includeShells') }}
        </label>
        <Button
          v-if="filtered"
          :label="t('filters.clear')"
          severity="secondary"
          text
          @click="router.replace({ query: {} })"
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
      @page="onPage"
      @row-click="open"
    >
      <template #empty>
        <p class="muted">
          {{ filtered ? t('logins.empty.filtered') : t('logins.empty.none') }}
        </p>
      </template>
      <Column :header="t('logins.columns.time')">
        <template #body="{ data }">
          <RouterLink
            :to="{ name: 'session', params: { id: String(data.session_id) } }"
            class="time"
            @click.stop
          >
            {{ formatTime(data.ts) }}
          </RouterLink>
        </template>
      </Column>
      <Column
        field="sensor"
        :header="t('logins.columns.sensor')"
      />
      <Column :header="t('logins.columns.sourceIp')">
        <template #body="{ data }">
          <RouterLink
            v-if="data.src_ip"
            :to="{ name: 'attacker', params: { ip: data.src_ip } }"
            class="data"
            @click.stop
          >
            {{ data.src_ip }}
          </RouterLink>
          <span
            v-else
            class="data"
          >—</span>
        </template>
      </Column>
      <Column :header="t('logins.columns.username')">
        <template #body="{ data }">
          <span class="data">{{ data.username }}</span>
        </template>
      </Column>
      <Column :header="t('logins.columns.password')">
        <template #body="{ data }">
          <span class="data">{{ data.password }}</span>
        </template>
      </Column>
      <Column :header="t('logins.columns.country')">
        <template #body="{ data }">
          <CountryFlag
            v-if="data.geo_country"
            :code="data.geo_country"
          />
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column :header="t('logins.columns.client')">
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
