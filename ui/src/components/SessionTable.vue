<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DataTable, { type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import { api, type Params } from '@/api/client'
import type { SessionSummary } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import CredentialsTried from '@/components/CredentialsTried.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { endpoint, formatMillis, formatTime } from '@/format'
import { pageSizes, useTablePage, useTableSort } from '@/sort'

// The list of sessions: the sessions page, and an attacker's profile with
// the address. The filters are the caller's; the page and the sort are in
// the URL, like the filters.
const props = defineProps<{
  /** Parameters of GET /api/sessions, without page or sort. */
  params: Params
  /** Whether the caller filters, to say why the list may be empty. */
  filtered?: boolean
}>()
/** The number of sessions matching, for the caller's heading. */
const total = defineModel<number>('total', { default: 0 })
const { t } = useI18n()
const router = useRouter()

// Characters of the SSH client shown in the table ("SSH-2.0-libssh"); a
// longer one is cut, whole in a tooltip. Matches the column's CSS width.
const clientShown = 14

const { rows, page, params: pageParams, onPage } = useTablePage(50)
const { params: sortParams, field: sortField, direction, onSort } = useTableSort('started')

const items = ref<SessionSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  () => ({ ...props.params, ...sortParams.value, ...pageParams.value }),
  async (params, previous) => {
    if (previous && JSON.stringify(params) === JSON.stringify(previous)) return
    loading.value = true
    try {
      const res = await api.sessions(params)
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

function open(e: DataTableRowClickEvent<SessionSummary>) {
  void router.push({ name: 'session', params: { id: String(e.data.id) } })
}
</script>

<template>
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
        <CredentialsTried
          :credentials="data.credentials"
          :attempts="data.login_attempts"
        />
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
          class="logged-in"
          style="white-space: nowrap;"
        >{{ t('sessions.outcome.loggedIn') }}</span>
        <span
          v-else-if="data.login_attempts"
          class="muted"
          style="white-space: nowrap;"
        >{{ t('sessions.outcome.refused') }}</span>
        <span
          v-else
          class="muted"
          style="white-space: nowrap;"
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
</template>

<style scoped>
.time {
  white-space: nowrap;
  text-decoration: none;
}

.time:hover {
  text-decoration: underline;
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
