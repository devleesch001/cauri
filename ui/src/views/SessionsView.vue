<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import SessionFilters from '@/components/SessionFilters.vue'
import SessionTable from '@/components/SessionTable.vue'
import { useSessionFilterParams } from '@/sessionFilters'
import { notFilters } from '@/sort'

const { t } = useI18n()
const route = useRoute()

const filtered = computed(() => Object.keys(route.query).some((k) => !notFilters.includes(k)))
const filters = useSessionFilterParams()
const csvUrl = computed(() => api.sessionsCsvUrl(filters.value))

const total = ref(0)
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

    <SessionFilters />

    <SessionTable
      v-model:total="total"
      :params="filters"
      :filtered="filtered"
    />
  </div>
</template>
