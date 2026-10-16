<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Column from 'openvue/column'
import DataTable from 'openvue/datatable'
import ProgressBar from 'openvue/progressbar'
import Tag from 'openvue/tag'
import { admin } from '@/api/client'
import type { AdminJob } from '@/api/types'
import { useAdmin } from '@/admin'
import { formatBytes, formatMillis, formatTime } from '@/format'

// The background tasks of the administration: the one running, followed
// every second (see admin.ts), and the last ones.
const { t, te } = useI18n()
const { call, tasks, started, refresh } = useAdmin()
onMounted(refresh)

const severity = { running: 'info', done: 'success', failed: 'warn', canceled: 'secondary' } as const
const percent = (j: AdminJob) => (j.progress.total ? Math.min(100, Math.round((100 * j.progress.done) / j.progress.total)) : 0)
const duration = (j: AdminJob) => formatMillis(new Date(j.finished ?? Date.now()).getTime() - new Date(j.started).getTime())
// What a task was asked, in one line: its query or its parameters.
const params = (j: AdminJob) =>
  Object.entries(j.params ?? {})
    .map(([k, v]) => `${k}=${Array.isArray(v) ? v.join(',') : String(v)}`)
    .join(' ')
// What it did, in one line: what was deleted, or the size of the database
// before and after.
function result(j: AdminJob): string {
  const r = j.result ?? {}
  if ('size_before' in r) return `${formatBytes(r.size_before)} → ${formatBytes(r.size_after)}`
  const label = (k: string) =>
    te('admin.purge.counts.' + k) ? t('admin.purge.counts.' + k) : te('admin.tasks.results.' + k) ? t('admin.tasks.results.' + k) : k
  return Object.entries(r)
    .filter(([, n]) => n)
    .map(([k, n]) => `${label(k)} ${n.toLocaleString()}`)
    .join(', ')
}

async function cancel(j: AdminJob) {
  const out = await call(() => admin.cancelJob(j.id))
  if (out) started(out)
}
</script>

<template>
  <section class="card">
    <h2>{{ t('admin.tasks.title') }}</h2>
    <p class="muted">
      {{ t('admin.tasks.intro') }}
    </p>
    <DataTable
      :value="tasks"
      data-key="id"
      size="small"
      striped-rows
      scrollable
    >
      <template #empty>
        <p class="muted">
          {{ t('admin.tasks.none') }}
        </p>
      </template>
      <Column :header="t('admin.tasks.columns.task')">
        <template #body="{ data }">
          <span class="muted">#{{ data.id }}</span> {{ t('admin.tasks.kinds.' + data.kind) }}
          <span
            v-if="params(data)"
            class="data muted params"
          >{{ params(data) }}</span>
        </template>
      </Column>
      <Column :header="t('admin.tasks.columns.status')">
        <template #body="{ data }">
          <Tag
            :value="t('admin.tasks.status.' + data.status)"
            :severity="severity[data.status as AdminJob['status']]"
          />
        </template>
      </Column>
      <Column :header="t('admin.tasks.columns.progress')">
        <template #body="{ data }">
          <template v-if="data.status === 'running'">
            <ProgressBar
              :value="percent(data)"
              :mode="data.progress.total ? 'determinate' : 'indeterminate'"
              :show-value="false"
              class="bar"
            />
            <span
              v-if="data.progress.total"
              class="muted"
            >{{ t('admin.tasks.progress', { done: data.progress.done.toLocaleString(), total: data.progress.total.toLocaleString() }) }}</span>
          </template>
          <span v-else-if="data.progress.total">{{ t('admin.tasks.progress', { done: data.progress.done.toLocaleString(), total: data.progress.total.toLocaleString() }) }}</span>
        </template>
      </Column>
      <Column :header="t('admin.tasks.columns.started')">
        <template #body="{ data }">
          {{ formatTime(data.started) }}
        </template>
      </Column>
      <Column :header="t('admin.tasks.columns.duration')">
        <template #body="{ data }">
          {{ duration(data) }}
        </template>
      </Column>
      <Column :header="t('admin.tasks.columns.result')">
        <template #body="{ data }">
          <span
            v-if="data.error"
            class="data"
          >{{ data.error }}</span>
          <span
            v-else
            class="data"
          >{{ result(data) }}</span>
        </template>
      </Column>
      <Column>
        <template #body="{ data }">
          <Button
            v-if="data.status === 'running' && data.cancelable"
            :label="t('admin.tasks.cancel')"
            severity="secondary"
            size="small"
            outlined
            @click="cancel(data)"
          />
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.params {
  display: block;
  font-size: 0.8125rem;
}

.bar {
  height: 0.5rem;
  min-width: 8rem;
  margin-bottom: var(--space-1);
}
</style>
