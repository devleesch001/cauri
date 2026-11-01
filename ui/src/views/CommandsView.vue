<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import type { Commands } from '@/api/types'
import BarList from '@/components/BarList.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import LoadingBar from '@/components/LoadingBar.vue'
import KpiCard from '@/components/KpiCard.vue'
import StatsFilters from '@/components/StatsFilters.vue'
import { useStatsFilters, useStatsLoad } from '@/stats'

// What attackers run once in, and with which clients.
const { t } = useI18n()
const { link } = useStatsFilters()
const { data: commands, loading, error } = useStatsLoad<Commands>((p) => api.commands(p))
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('commands.title') }}</h1>
    </div>
    <StatsFilters />
    <LoadingBar :active="loading" />
    <ErrorNotice :error="error" />

    <div
      v-if="commands"
      :class="{ stale: loading }"
      :aria-busy="loading"
    >
      <div class="kpis">
        <KpiCard
          :label="t('commands.total')"
          :value="commands.total"
        />
        <KpiCard
          :label="t('commands.distinct')"
          :value="commands.distinct"
        />
        <KpiCard
          :label="t('commands.sessions')"
          :value="commands.sessions"
        />
      </div>

      <div class="layout">
        <section class="card">
          <h2>{{ t('commands.top') }}</h2>
          <BarList
            v-if="commands.top.length"
            :items="commands.top"
            :value="(c) => c.sessions"
            :item-key="(c) => c.input"
            :to="(c) => link('sessions', { command: c.input })"
          >
            <template #label="{ item }">
              <span
                class="data"
                :title="item.input"
              >{{ item.input }}</span>
            </template>
            <template #value="{ item }">
              <span :title="t('commands.ranBy', { sessions: item.sessions, sources: item.sources, n: item.count })">
                {{ item.sessions.toLocaleString() }}
              </span>
            </template>
          </BarList>
          <p
            v-else
            class="muted"
          >
            {{ t('commands.empty') }}
          </p>
        </section>

        <section class="card">
          <h2>{{ t('commands.hassh') }}</h2>
          <p class="muted hint">
            {{ t('commands.hasshHint') }}
          </p>
          <BarList
            :items="commands.hassh"
            :value="(h) => h.sessions"
            :item-key="(h) => h.hassh"
            :to="(h) => link('sessions', { hassh: h.hassh, has_commands: 'true' })"
          >
            <template #label="{ item }">
              <span
                class="data"
                :title="item.hassh"
              >{{ item.hassh.slice(0, 12) }}</span>
              <span class="muted client">{{ item.client }}<template v-if="item.clients > 1"> {{ t('commands.moreClients', { n: item.clients - 1 }) }}</template></span>
            </template>
          </BarList>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

@media (width <= 900px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

.hint {
  margin: 0 0 var(--space-3);
  font-size: 0.85em;
}

.client {
  margin-left: var(--space-2);
  font-size: 0.9em;
}
</style>
