<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import type { Credentials } from '@/api/types'
import BarList from '@/components/BarList.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KpiCard from '@/components/KpiCard.vue'
import StatsFilters from '@/components/StatsFilters.vue'
import { useStatsFilters } from '@/stats'

// What attackers try to log in with, and what let them in.
const { t } = useI18n()
const { params, link } = useStatsFilters()

const cred = ref<Credentials | null>(null)
const error = ref<string | null>(null)
watch(
  params,
  async (p) => {
    try {
      cred.value = await api.credentials(p)
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)
const accepted = computed(() => {
  const c = cred.value
  return c && c.attempts ? Math.round((1000 * c.accepted) / c.attempts) / 10 : 0
})
const mostLength = computed(() => Math.max(1, ...(cred.value?.password_lengths ?? []).map((l) => l.count)))
// The last length is "that many and more".
const lengthLabel = (n: number, i: number, all: unknown[]) => (i === all.length - 1 && n >= 16 ? `${n}+` : String(n))
// An empty password is a real value: show it.
const shown = (v: string) => (v === '' ? t('session.logins.emptyPassword') : v)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('credentials.title') }}</h1>
    </div>
    <StatsFilters />
    <ErrorNotice :error="error" />

    <template v-if="cred">
      <div class="kpis">
        <KpiCard
          :label="t('credentials.attempts')"
          :value="cred.attempts"
          :detail="t('stats.acceptedShare', { n: accepted })"
        />
        <KpiCard
          :label="t('credentials.accepted')"
          :value="cred.accepted"
        />
        <KpiCard
          :label="t('credentials.usernames')"
          :value="cred.distinct_usernames"
        />
        <KpiCard
          :label="t('credentials.passwords')"
          :value="cred.distinct_passwords"
        />
      </div>

      <div class="panels">
        <section class="card">
          <h2>{{ t('credentials.topUsernames') }}</h2>
          <BarList
            :items="cred.top_usernames"
            :value="(v) => v.count"
            :item-key="(v) => v.value"
            :to="(v) => link('login-attempts', { username: v.value })"
          >
            <template #label="{ item }">
              <span class="data">{{ shown(item.value) }}</span>
            </template>
          </BarList>
        </section>
        <section class="card">
          <h2>{{ t('credentials.topPasswords') }}</h2>
          <BarList
            :items="cred.top_passwords"
            :value="(v) => v.count"
            :item-key="(v) => v.value"
            :to="(v) => link('login-attempts', { password: v.value })"
          >
            <template #label="{ item }">
              <span class="data">{{ shown(item.value) }}</span>
            </template>
          </BarList>
        </section>
        <section class="card">
          <h2>{{ t('credentials.topPairs') }}</h2>
          <BarList
            :items="cred.top_pairs"
            :value="(p) => p.count"
            :item-key="(p) => `${p.username}\u0000${p.password}`"
            :to="(p) => link('login-attempts', { username: p.username, password: p.password })"
          >
            <template #label="{ item }">
              <span class="data">{{ shown(item.username) }} / {{ shown(item.password) }}</span>
            </template>
          </BarList>
        </section>
        <section class="card">
          <h2>{{ t('credentials.worked') }}</h2>
          <BarList
            v-if="cred.worked.length"
            :items="cred.worked"
            :value="(p) => p.accepted"
            :item-key="(p) => `${p.username}\u0000${p.password}`"
            :to="(p) => link('sessions', { username: p.username, password: p.password, login: 'success' })"
          >
            <template #label="{ item }">
              <span class="data shell">{{ shown(item.username) }} / {{ shown(item.password) }}</span>
            </template>
          </BarList>
          <p
            v-else
            class="muted"
          >
            {{ t('credentials.noneWorked') }}
          </p>
        </section>
        <section class="card">
          <h2>{{ t('credentials.lengths') }}</h2>
          <div
            v-if="cred.password_lengths.length"
            class="lengths"
          >
            <span
              v-for="(l, i) in cred.password_lengths"
              :key="l.length"
              class="len"
              :title="t('credentials.length', { n: lengthLabel(l.length, i, cred.password_lengths), count: l.count.toLocaleString() })"
            >
              <span
                class="col"
                :style="{ height: `${(100 * l.count) / mostLength}%` }"
              />
              <span class="muted tick">{{ lengthLabel(l.length, i, cred.password_lengths) }}</span>
            </span>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>

<style scoped>
.lengths {
  display: flex;
  align-items: flex-end;
  gap: 3px;
  height: 9rem;
}

.len {
  display: flex;
  flex: 1 1 0;
  flex-direction: column;
  justify-content: flex-end;
  align-items: center;
  height: 100%;
}

.col {
  width: 100%;
  min-height: 1px;
  border-radius: 2px 2px 0 0;
  background: var(--p-primary-color);
}

.tick {
  font-size: 0.75em;
}
</style>
