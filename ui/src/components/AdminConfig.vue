<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Column from 'openvue/column'
import DataTable from 'openvue/datatable'
import Message from 'openvue/message'
import Tag from 'openvue/tag'
import { admin } from '@/api/client'
import type { AdminConfig } from '@/api/types'
import { useAdmin } from '@/admin'

// The configuration of this API: each option, its variable, its value and
// where it came from; the API masks the secrets.
const { t } = useI18n()
const { call } = useAdmin()

const config = ref<AdminConfig | null>(null)
onMounted(async () => {
  const c = await call(admin.config)
  if (c) config.value = c
})
const severity = { flag: 'info', env: 'success', default: 'secondary' } as const
</script>

<template>
  <section class="card">
    <h2>{{ t('admin.config.title') }}</h2>
    <p class="muted">
      {{ t('admin.config.intro') }}
    </p>
    <template v-if="config">
      <Message
        v-if="config.unknown_env.length"
        severity="warn"
        class="unknown"
      >
        {{ t('admin.config.unknown') }}
        <span class="data">{{ config.unknown_env.join(', ') }}</span>
      </Message>
      <DataTable
        :value="config.options"
        data-key="name"
        size="small"
        striped-rows
        scrollable
      >
        <Column :header="t('admin.config.option')">
          <template #body="{ data }">
            <span
              v-tooltip.top="data.usage"
              class="data"
            >--{{ data.name }}</span>
            <span class="data muted env">{{ data.env }}</span>
          </template>
        </Column>
        <Column :header="t('admin.config.value')">
          <template #body="{ data }">
            <span
              v-if="data.value !== ''"
              class="data"
            >{{ data.value }}</span>
            <span
              v-else
              class="muted"
            >—</span>
          </template>
        </Column>
        <Column :header="t('admin.config.default')">
          <template #body="{ data }">
            <span
              v-if="data.default !== ''"
              class="data muted"
            >{{ data.default }}</span>
            <span
              v-else
              class="muted"
            >—</span>
          </template>
        </Column>
        <Column :header="t('admin.config.source')">
          <template #body="{ data }">
            <Tag
              :value="t('admin.config.sources.' + data.source)"
              :severity="severity[data.source as keyof typeof severity]"
            />
          </template>
        </Column>
      </DataTable>
    </template>
  </section>
</template>

<style scoped>
.env {
  display: block;
  font-size: 0.8125rem;
}

.unknown {
  margin-bottom: var(--space-3);
}
</style>
