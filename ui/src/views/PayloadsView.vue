<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'openvue/datatable'
import Column from 'openvue/column'
import { api } from '@/api/client'
import type { Payloads } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KpiCard from '@/components/KpiCard.vue'
import StatsFilters from '@/components/StatsFilters.vue'
import { formatTime } from '@/format'
import { useStatsFilters } from '@/stats'

// The files attackers fetched or uploaded, by digest: how widespread each
// one is, and where to learn more about it.
const { t } = useI18n()
const { params, link } = useStatsFilters()

const payloads = ref<Payloads | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
watch(
  params,
  async (p) => {
    loading.value = true
    try {
      payloads.value = await api.payloads(p)
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)
// Looked up by its digest only: the file itself never leaves cauri.
const virusTotal = (sha: string) => `https://www.virustotal.com/gui/file/${sha}`
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('payloads.title') }}</h1>
    </div>
    <StatsFilters />
    <ErrorNotice :error="error" />

    <div
      v-if="payloads"
      class="kpis"
    >
      <KpiCard
        :label="t('payloads.files')"
        :value="payloads.files"
      />
      <KpiCard
        :label="t('payloads.sessions')"
        :value="payloads.sessions"
      />
    </div>

    <DataTable
      :value="payloads?.items ?? []"
      :loading="loading"
      data-key="sha256"
      size="small"
      striped-rows
      scrollable
    >
      <template #empty>
        <p class="muted">
          {{ t('payloads.empty') }}
        </p>
      </template>
      <Column :header="t('payloads.columns.file')">
        <template #body="{ data }">
          <span
            class="data sha"
            :title="data.sha256"
          >{{ data.sha256.slice(0, 12) }}</span>
        </template>
      </Column>
      <Column :header="t('payloads.columns.source')">
        <template #body="{ data }">
          <span
            class="data source"
            :title="data.url ?? data.filename ?? ''"
          >{{ data.url ?? data.filename ?? '—' }}</span>
        </template>
      </Column>
      <Column
        :header="t('payloads.columns.sessions')"
        class="num"
      >
        <template #body="{ data }">
          <RouterLink :to="link('sessions', { file: data.sha256 })">
            {{ data.sessions.toLocaleString() }}
          </RouterLink>
        </template>
      </Column>
      <Column
        :header="t('payloads.columns.sources')"
        class="num"
      >
        <template #body="{ data }">
          {{ data.sources.toLocaleString() }}
        </template>
      </Column>
      <Column :header="t('payloads.columns.countries')">
        <template #body="{ data }">
          <span class="flags">
            <CountryFlag
              v-for="c in data.countries"
              :key="c"
              :code="c"
            />
          </span>
        </template>
      </Column>
      <Column :header="t('payloads.columns.seen')">
        <template #body="{ data }">
          <span class="seen">{{ formatTime(data.first) }}<br><span class="muted">{{ formatTime(data.last) }}</span></span>
        </template>
      </Column>
      <Column :header="t('payloads.columns.lookup')">
        <template #body="{ data }">
          <a
            :href="virusTotal(data.sha256)"
            target="_blank"
            rel="noopener noreferrer"
          >VirusTotal</a>
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.source {
  display: inline-block;
  max-width: 28rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.flags {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
}

.seen {
  white-space: nowrap;
}

:deep(.num) {
  text-align: right;
}
</style>
