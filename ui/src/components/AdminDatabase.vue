<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Column from 'openvue/column'
import DataTable from 'openvue/datatable'
import Dialog from 'openvue/dialog'
import InputNumber from 'openvue/inputnumber'
import { admin } from '@/api/client'
import type { AdminDatabase, RawLines } from '@/api/types'
import { useAdmin } from '@/admin'
import { formatBytes, formatTime } from '@/format'

// The database: what it is and where its space goes; purging its raw
// lines and compacting it, both run as tasks (see AdminTasks).
const { t } = useI18n()
const { call, running, ended, started } = useAdmin()

const db = ref<AdminDatabase | null>(null)
async function load() {
  const d = await call(admin.database)
  if (d) db.value = d
}
onMounted(load)
// A task ended: what it changed shows.
watch(ended, load)

const share = (bytes: number) => (db.value?.size ? Math.round((100 * bytes) / db.value.size) : 0)

// Raw lines: simulated, then purged after a confirmation.
const days = ref(30)
const simulated = ref<{ days: number; lines: RawLines } | null>(null)
watch(days, () => (simulated.value = null))
async function simulateRaw() {
  const d = days.value
  const lines = await call(() => admin.simulateRaw(d))
  if (lines) simulated.value = { days: d, lines }
}
const confirmRaw = ref(false)
async function purgeRaw() {
  confirmRaw.value = false
  const job = await call(() => admin.purgeRaw(days.value))
  if (job) {
    started(job)
    simulated.value = null
  }
}

// Compaction: what will happen, then a confirmation.
const confirmVacuum = ref(false)
// Measured on 107 MB: about 0.7 s per 100 MB; never less than a second.
const vacuumSeconds = computed(() => Math.max(1, Math.ceil(((db.value?.size ?? 0) / (100 * 1024 * 1024)) * 0.7)))
async function vacuum() {
  confirmVacuum.value = false
  const job = await call(admin.vacuum)
  if (job) started(job)
}
</script>

<template>
  <section class="card">
    <h2>{{ t('admin.database.title') }}</h2>
    <template v-if="db">
      <dl class="facts">
        <dt>{{ t('admin.database.engine') }}</dt>
        <dd>{{ db.engine }} {{ db.version }}</dd>
        <dt>{{ t('admin.database.schema') }}</dt>
        <dd class="data">
          {{ db.schema }}
        </dd>
        <dt>{{ t('admin.database.size') }}</dt>
        <dd>{{ formatBytes(db.size) }}</dd>
        <template v-if="db.free">
          <dt v-tooltip.top="t('admin.database.freeHint')">
            {{ t('admin.database.free') }}
          </dt>
          <dd>{{ formatBytes(db.free) }}</dd>
        </template>
        <dt>{{ t('admin.database.raw') }}</dt>
        <dd>
          <template v-if="db.raw.lines">
            {{ t('admin.database.rawValue', { n: db.raw.lines.toLocaleString(), size: formatBytes(db.raw.bytes), oldest: formatTime(db.raw.oldest) }) }}
          </template>
          <span
            v-else
            class="muted"
          >{{ t('admin.database.rawNone') }}</span>
        </dd>
        <dt>{{ t('admin.database.recordings') }}</dt>
        <dd>
          <template v-if="db.recordings">
            {{ t('admin.database.recordingsValue', { n: db.recordings.files.toLocaleString(), size: formatBytes(db.recordings.bytes) }) }}
          </template>
          <span
            v-else
            class="muted"
          >{{ t('admin.database.unreadable') }}</span>
        </dd>
      </dl>

      <DataTable
        :value="db.tables"
        data-key="name"
        size="small"
        striped-rows
        scrollable
        sort-field="bytes"
        :sort-order="-1"
      >
        <Column
          field="name"
          :header="t('admin.database.table')"
          sortable
        >
          <template #body="{ data }">
            <span class="data">{{ data.name }}</span>
          </template>
        </Column>
        <Column
          field="rows"
          :header="t('admin.database.rows')"
          sortable
          class="num"
        >
          <template #body="{ data }">
            {{ data.rows.toLocaleString() }}
          </template>
        </Column>
        <Column
          field="bytes"
          :header="t('admin.database.bytes')"
          sortable
          class="num"
        >
          <template #body="{ data }">
            {{ formatBytes(data.bytes) }}
          </template>
        </Column>
        <Column
          :header="t('admin.database.share')"
          class="num"
        >
          <template #body="{ data }">
            {{ share(data.bytes) }} %
          </template>
        </Column>
      </DataTable>

      <div class="actions">
        <section>
          <h3>{{ t('admin.rawPurge.title') }}</h3>
          <p class="muted">
            {{ t('admin.rawPurge.intro') }}
          </p>
          <div class="row">
            <label for="raw-days">{{ t('admin.rawPurge.days') }}</label>
            <InputNumber
              v-model="days"
              input-id="raw-days"
              :min="0"
              :max="3650"
              show-buttons
            />
            <Button
              :label="t('admin.rawPurge.simulate')"
              severity="secondary"
              outlined
              @click="simulateRaw"
            />
            <Button
              :label="t('admin.rawPurge.purge')"
              :disabled="!simulated?.lines.lines || !!running"
              @click="confirmRaw = true"
            />
          </div>
          <p
            v-if="simulated"
            role="status"
          >
            <template v-if="simulated.lines.lines">
              {{ t('admin.rawPurge.result', { n: simulated.lines.lines.toLocaleString(), size: formatBytes(simulated.lines.bytes), oldest: formatTime(simulated.lines.oldest) }) }}
            </template>
            <template v-else>
              {{ t('admin.rawPurge.nothing') }}
            </template>
          </p>
        </section>
        <section>
          <h3>{{ t('admin.vacuum.title') }}</h3>
          <p class="muted">
            {{ t('admin.vacuum.intro') }}
          </p>
          <Button
            :label="t('admin.vacuum.compact')"
            :disabled="!!running"
            @click="confirmVacuum = true"
          />
        </section>
      </div>
    </template>

    <Dialog
      v-model:visible="confirmRaw"
      modal
      :header="t('admin.rawPurge.confirmTitle')"
      :style="{ width: 'min(32rem, 100vw - 2rem)' }"
    >
      <p v-if="simulated">
        {{ t('admin.rawPurge.confirmText', { n: simulated.lines.lines.toLocaleString(), days: simulated.days }) }}
      </p>
      <template #footer>
        <Button
          :label="t('admin.cancel')"
          severity="secondary"
          text
          @click="confirmRaw = false"
        />
        <Button
          :label="t('admin.rawPurge.purge')"
          @click="purgeRaw"
        />
      </template>
    </Dialog>

    <Dialog
      v-model:visible="confirmVacuum"
      modal
      :header="t('admin.vacuum.confirmTitle')"
      :style="{ width: 'min(36rem, 100vw - 2rem)' }"
    >
      <template v-if="db?.engine === 'sqlite'">
        <p>{{ t('admin.vacuum.sqlite', { size: formatBytes(db.size), free: formatBytes(db.free) }) }}</p>
        <p>{{ t('admin.vacuum.sqliteHold') }}</p>
        <p>{{ t('admin.vacuum.sqliteDisk', { size: formatBytes(db.size) }) }}</p>
        <p>{{ t('admin.vacuum.sqliteTime', { seconds: vacuumSeconds }) }}</p>
      </template>
      <p v-else>
        {{ t('admin.vacuum.postgres') }}
      </p>
      <template #footer>
        <Button
          :label="t('admin.cancel')"
          severity="secondary"
          text
          @click="confirmVacuum = false"
        />
        <Button
          :label="t('admin.vacuum.confirm')"
          @click="vacuum"
        />
      </template>
    </Dialog>
  </section>
</template>

<style scoped>
.facts {
  margin-bottom: var(--space-3);
}

.actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(22rem, 1fr));
  gap: var(--space-4);
  margin-top: var(--space-4);
}

h3 {
  margin: 0 0 var(--space-2);
  font-size: 1rem;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

:deep(.num) {
  text-align: right;
}
</style>
