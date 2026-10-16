<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ApiError, api } from '@/api/client'
import type { Activity, AttackerDetail, Commands, Credentials, Payloads, Summary } from '@/api/types'
import BarList from '@/components/BarList.vue'
import CountryFlag from '@/components/CountryFlag.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KpiCard from '@/components/KpiCard.vue'
import SessionFilters from '@/components/SessionFilters.vue'
import SessionTable from '@/components/SessionTable.vue'
import { formatTime } from '@/format'
import { useSessionFilterParams } from '@/sessionFilters'
import { useStatsFilters } from '@/stats'

// One attacker, a source address: who it is and everything it did, the
// statistics narrowed to its sessions. One filter bar, as on the sessions
// page: the period, verdict and outcome narrow the whole page, the others
// only its sessions (the statistics do not take them).
const props = defineProps<{ ip: string }>()
const { t } = useI18n()
const { params, link } = useStatsFilters()

const attacker = ref<AttackerDetail | null>(null)
const summary = ref<Summary | null>(null)
const activity = ref<Activity | null>(null)
const credentials = ref<Credentials | null>(null)
const commands = ref<Commands | null>(null)
const payloads = ref<Payloads | null>(null)
const sessionTotal = ref(0)
const notFound = ref(false)
const error = ref<string | null>(null)

watch(
  [() => props.ip, params],
  async ([ip, p]) => {
    const withIP = { ...p, ip }
    try {
      attacker.value = await api.attacker(ip, p)
      notFound.value = false
      ;[summary.value, activity.value, credentials.value, commands.value, payloads.value] = await Promise.all([
        api.summary(withIP), api.activity(withIP), api.credentials(withIP), api.commands(withIP), api.payloads(withIP),
      ])
      error.value = null
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        notFound.value = true
        attacker.value = null
        return
      }
      error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

const accepted = computed(() => {
  const s = summary.value
  return s && s.logins ? Math.round((1000 * s.accepted) / s.logins) / 10 : 0
})
// Days with sessions, latest first.
const days = computed(() => [...(activity.value?.days ?? [])].reverse())
const shown = (v: string) => (v === '' ? t('session.logins.emptyPassword') : v)
const mine = (extra: Record<string, string>) => link('sessions', { ip: props.ip, ...extra })
// Its sessions, listed and filtered as on the sessions page, but for the
// address and country the page sets; the export is theirs.
const sessionFilters = useSessionFilterParams()
const sessionParams = computed(() => ({ ...sessionFilters.value, ip: props.ip }))
const csvUrl = computed(() => api.sessionsCsvUrl(sessionParams.value))
</script>

<template>
  <div class="page">
    <RouterLink
      :to="{ name: 'attackers' }"
      class="back muted"
    >
      {{ t('attacker.back') }}
    </RouterLink>

    <div class="page-head">
      <h1>
        <span class="data">{{ ip }}</span>
        <CountryFlag
          v-if="attacker?.country"
          :code="attacker.country"
          class="place"
        >
          {{ attacker.city ? `, ${attacker.city}` : '' }}
        </CountryFlag>
      </h1>
      <span
        v-if="attacker"
        class="muted"
      >{{ formatTime(attacker.first_seen) }} – {{ formatTime(attacker.last_seen) }}</span>
    </div>

    <SessionFilters :hide="['ip', 'country']" />
    <ErrorNotice :error="error" />

    <template v-if="notFound">
      <h2>{{ t('attacker.notFound') }}</h2>
      <p class="muted">
        {{ t('attacker.notFoundText', { ip }) }}
      </p>
    </template>

    <template v-else-if="attacker && summary">
      <div class="kpis">
        <KpiCard
          :label="t('stats.sessions')"
          :value="summary.sessions"
          :previous="summary.previous?.sessions"
        />
        <KpiCard
          :label="t('stats.loggedIn')"
          :value="summary.logged_in"
          :previous="summary.previous?.logged_in"
        />
        <KpiCard
          :label="t('stats.logins')"
          :value="summary.logins"
          :detail="t('stats.acceptedShare', { n: accepted })"
          :previous="summary.previous?.logins"
        />
      </div>

      <div class="panels even">
        <section class="card">
          <h2>{{ t('attacker.identity') }}</h2>
          <dl class="facts body">
            <dt>{{ t('attacker.firstSeen') }}</dt>
            <dd>{{ formatTime(attacker.first_seen) }}</dd>
            <dt>{{ t('attacker.lastSeen') }}</dt>
            <dd>{{ formatTime(attacker.last_seen) }}</dd>
            <dt>{{ t('attacker.location') }}</dt>
            <dd>
              <CountryFlag
                v-if="attacker.country"
                :code="attacker.country"
              >
                {{ attacker.city ? `, ${attacker.city}` : '' }}
              </CountryFlag>
              <span
                v-else
                class="muted"
              >—</span>
            </dd>
            <dt>{{ t('attacker.network') }}</dt>
            <dd>
              <RouterLink
                v-if="attacker.asn"
                :to="link('attackers', { asn: String(attacker.asn) })"
              >
                AS{{ attacker.asn }}
              </RouterLink>
              <span v-if="attacker.asn"> {{ attacker.as_org }}</span>
              <span
                v-else
                class="muted"
              >—</span>
            </dd>
            <dt>{{ t('attacker.sensors') }}</dt>
            <dd>{{ attacker.sensors.join(', ') }}</dd>
            <dt>{{ t('attacker.clients') }}</dt>
            <dd>
              <span
                v-for="c in attacker.clients"
                :key="c.client"
                class="data line"
              >{{ c.client }} <span class="muted">× {{ c.sessions }}</span></span>
              <span
                v-if="!attacker.clients.length"
                class="muted"
              >—</span>
            </dd>
            <dt>{{ t('attacker.hassh') }}</dt>
            <dd>
              <RouterLink
                v-for="h in attacker.hassh"
                :key="h"
                :to="mine({ hassh: h })"
                class="data line"
              >
                {{ h }}
              </RouterLink>
              <span
                v-if="!attacker.hassh.length"
                class="muted"
              >—</span>
            </dd>
          </dl>
        </section>

        <section class="card">
          <h2>{{ t('attacker.days') }}</h2>
          <BarList
            class="body"
            :items="days"
            :value="(d) => d.sessions"
            :item-key="(d) => d.day"
            :to="(d) => mine({ from: d.day, to: d.day })"
          >
            <template #label="{ item }">
              {{ item.day }}
            </template>
            <template #value="{ item }">
              {{ item.sessions.toLocaleString() }}
              <span
                v-if="item.logged_in"
                class="logged-in"
              > · {{ item.logged_in.toLocaleString() }}</span>
            </template>
          </BarList>
        </section>

        <section
          v-if="credentials"
          class="card"
        >
          <h2>{{ t('attacker.credentials') }}</h2>
          <BarList
            v-if="credentials.top_pairs.length"
            class="body"
            :items="credentials.top_pairs"
            :value="(p) => p.count"
            :item-key="(p) => p.username + '\u0000' + p.password"
            :to="(p) => link('login-attempts', { ip, username: p.username, password: p.password })"
          >
            <template #label="{ item }">
              <span
                class="data"
                :class="{ 'logged-in': item.accepted }"
              >{{ shown(item.username) }} / {{ shown(item.password) }}</span>
            </template>
          </BarList>
          <p
            v-else
            class="muted"
          >
            {{ t('attacker.none') }}
          </p>
        </section>

        <section
          v-if="commands"
          class="card"
        >
          <h2>{{ t('attacker.commands') }}</h2>
          <div class="body">
            <BarList
              v-if="commands.top.length"
              :items="commands.top"
              :value="(c) => c.count"
              :item-key="(c) => c.input"
              :to="(c) => mine({ command: c.input })"
            >
              <template #label="{ item }">
                <span class="data">{{ item.input }}</span>
              </template>
            </BarList>
            <p
              v-else
              class="muted"
            >
              {{ t('attacker.none') }}
            </p>
            <!-- What it did: the files it fetched or left, with its commands. -->
            <template v-if="payloads && payloads.items.length">
              <h2 class="next">
                {{ t('attacker.payloads') }}
              </h2>
              <BarList
                :items="payloads.items"
                :value="(p) => p.transfers"
                :item-key="(p) => p.sha256"
                :to="(p) => mine({ file: p.sha256 })"
              >
                <template #label="{ item }">
                  <span class="data">{{ item.url ?? item.filename ?? item.sha256.slice(0, 12) }}</span>
                </template>
              </BarList>
            </template>
          </div>
        </section>
      </div>

      <section class="latest">
        <div class="page-head">
          <h2>{{ t('attacker.sessions') }}</h2>
          <span class="muted">
            {{ t('sessions.count', { n: sessionTotal.toLocaleString() }, sessionTotal) }}
            <a
              :href="csvUrl"
              download
              class="export"
            >{{ t('export.csv') }}</a>
            <RouterLink
              :to="mine({})"
              class="export"
            >{{ t('attacker.allSessions') }}</RouterLink>
          </span>
        </div>
        <SessionTable
          v-model:total="sessionTotal"
          :params="sessionParams"
          filtered
        />
      </section>
    </template>
  </div>
</template>

<style scoped>
.back {
  display: inline-block;
  margin-bottom: var(--space-3);
  text-decoration: none;
}

.back::before {
  content: '‹ ';
}

.place {
  margin-left: var(--space-3);
  font-size: 1rem;
  font-weight: 500;
}

.line {
  display: block;
}

/* At most ten rows of a list in a card, the rest scrolled: a row is a
   line, its padding and its bar (BarList). */
.body {
  max-height: calc(10 * (1lh + 2 * var(--space-1) + 5px));
  overflow-y: auto;
  /* A discreet scrollbar: thin, the colour of the card's border. */
  scrollbar-width: thin;
  scrollbar-color: var(--c-rule) transparent;
}

/* A second list in a card, below the first. */
.body > .next {
  margin: var(--space-4) 0 var(--space-3);
  font-size: 1rem;
}

.latest {
  margin-top: var(--space-4);
}
</style>
