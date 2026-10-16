<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Column from 'openvue/column'
import DataTable from 'openvue/datatable'
import { admin } from '@/api/client'
import type { AdminDownload } from '@/api/types'
import { useAdmin } from '@/admin'
import { formatBytes, formatTime } from '@/format'

// The third-party databases: what is loaded, where it comes from, and a
// download on request, run as a task (see AdminTasks).
const { t } = useI18n()
const { call, running, ended, started } = useAdmin()

const downloads = ref<AdminDownload[]>([])
async function load() {
  const d = await call(admin.downloads)
  if (d) downloads.value = d
}
onMounted(load)
// A download ended: the database it loaded shows.
watch(ended, load)

const envOf = { geoip: 'CAURI_GEOIP_DB_URL', asn: 'CAURI_GEOIP_ASN_DB_URL' } as const

async function download(d: AdminDownload) {
  const job = await call(() => admin.download(d.name))
  if (job) started(job)
}
</script>

<template>
  <section class="card">
    <h2>{{ t('admin.downloads.title') }}</h2>
    <p class="muted">
      {{ t('admin.downloads.intro') }}
    </p>
    <DataTable
      :value="downloads"
      data-key="name"
      size="small"
      scrollable
    >
      <Column :header="t('admin.downloads.database')">
        <template #body="{ data }">
          {{ t('admin.downloads.names.' + data.name) }}
          <span class="data muted file">{{ data.path }}</span>
        </template>
      </Column>
      <Column :header="t('admin.downloads.loaded')">
        <template #body="{ data }">
          <template v-if="data.loaded">
            <span class="data">{{ data.loaded.type }}</span>
            <span class="muted file">{{ t('admin.downloads.built', { date: data.loaded.built.slice(0, 10) }) }}</span>
          </template>
          <span
            v-else
            class="muted"
          >{{ t('admin.downloads.notLoaded') }}</span>
        </template>
      </Column>
      <Column :header="t('admin.downloads.size')">
        <template #body="{ data }">
          <template v-if="data.size !== null">
            {{ formatBytes(data.size) }}
            <span class="muted file">{{ formatTime(data.modified) }}</span>
          </template>
        </template>
      </Column>
      <Column :header="t('admin.downloads.from')">
        <template #body="{ data }">
          <span
            v-for="u in data.urls"
            :key="u"
            class="data url"
          >{{ u }}</span>
          <span class="muted file">{{ t('admin.downloads.fromHint', { env: envOf[data.name as AdminDownload['name']] }) }}</span>
        </template>
      </Column>
      <Column>
        <template #body="{ data }">
          <Button
            v-tooltip.top="t('admin.downloads.downloadHint')"
            :label="t('admin.downloads.download')"
            :disabled="!!running"
            size="small"
            @click="download(data)"
          />
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.file,
.url {
  display: block;
  font-size: 0.8125rem;
}

.url {
  overflow-wrap: anywhere;
}
</style>
