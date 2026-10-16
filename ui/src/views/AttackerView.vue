<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ApiError, api } from '@/api/client'
import type { Activity, AttackerDetail, Commands, Credentials, KexDetail, OfferedKey, Payloads, Summary } from '@/api/types'
import BarList from '@/components/BarList.vue'
import CountryFlag from '@/components/CountryFlag.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KpiCard from '@/components/KpiCard.vue'
import SessionFilters from '@/components/SessionFilters.vue'
import SessionTable from '@/components/SessionTable.vue'
import { formatTime } from '@/format'
import { useSessionFilterParams } from '@/sessionFilters'
import { useStatsFilters } from '@/stats'

// One attacker, by one of its identities: a source address, or the key
// exchange its SSH tool offered. Who it is, what it is linked to, and
// everything it did, the statistics narrowed to its sessions. One filter
// bar, as on the sessions page: the period, verdict and outcome narrow
// the whole page, the others only its sessions (the statistics do not take
// them).
const props = defineProps<{ kind: 'ip' | 'kex'; id: string }>()
const { t } = useI18n()
const { params, link } = useStatsFilters()

const attacker = ref<AttackerDetail | null>(null)
const kex = ref<KexDetail | null>(null)
const summary = ref<Summary | null>(null)
const activity = ref<Activity | null>(null)
const credentials = ref<Credentials | null>(null)
const commands = ref<Commands | null>(null)
const payloads = ref<Payloads | null>(null)
const sessionTotal = ref(0)
const notFound = ref(false)
const error = ref<string | null>(null)

// The filter of its sessions, by its identity.
const identity = computed((): Record<string, string> => ({ [props.kind]: props.id }))

watch(
  [identity, params],
  async ([id, p]) => {
    const mineOnly = { ...p, ...id }
    try {
      if (props.kind === 'kex') {
        kex.value = await api.kexAttacker(props.id, p)
        attacker.value = null
      } else {
        attacker.value = await api.attacker(props.id, p)
        kex.value = null
      }
      notFound.value = false
      ;[summary.value, activity.value, credentials.value, commands.value, payloads.value] = await Promise.all([
        api.summary(mineOnly), api.activity(mineOnly), api.credentials(mineOnly), api.commands(mineOnly), api.payloads(mineOnly),
      ])
      error.value = null
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        notFound.value = true
        attacker.value = null
        kex.value = null
        return
      }
      error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

const found = computed(() => attacker.value ?? kex.value)
const keys = computed<OfferedKey[]>(() => found.value?.public_keys ?? [])
const accepted = computed(() => {
  const s = summary.value
  return s && s.logins ? Math.round((1000 * s.accepted) / s.logins) / 10 : 0
})
// Days with sessions, latest first.
const days = computed(() => [...(activity.value?.days ?? [])].reverse())
const shown = (v: string) => (v === '' ? t('session.logins.emptyPassword') : v)
const mine = (extra: Record<string, string>) => link('sessions', { ...identity.value, ...extra })
// The credentials it tried: the failed logins of an address, the sessions
// of a key exchange (failed logins are not filtered by it).
const tried = (username: string, password: string) =>
  props.kind === 'ip' ? link('login-attempts', { ip: props.id, username, password }) : mine({ username, password })
// The six lists of a key exchange, in Cowrie's names, those the HASSH
// hashes first.
const lists = computed(() => {
  const k = kex.value
  if (!k) return []
  return [
    { name: 'kex', cowrie: 'kexAlgs', value: k.kex_algs, hassh: true },
    { name: 'enc', cowrie: 'encCS', value: k.enc_algs, hassh: true },
    { name: 'mac', cowrie: 'macCS', value: k.mac_algs, hassh: true },
    { name: 'comp', cowrie: 'compCS', value: k.comp_algs, hassh: true },
    { name: 'key', cowrie: 'keyAlgs', value: k.key_algs, hassh: false },
    { name: 'lang', cowrie: 'langCS', value: k.lang_algs, hassh: false },
  ]
})
const short = (hassh: string | null, id: number) => (hassh ? hassh.slice(0, 12) : `#${id}`)
// Its sessions, listed and filtered as on the sessions page, but for the
// identity the page sets; the export is theirs.
const sessionFilters = useSessionFilterParams()
const sessionParams = computed(() => ({ ...sessionFilters.value, ...identity.value }))
const csvUrl = computed(() => api.sessionsCsvUrl(sessionParams.value))
</script>

<template>
  <div class="page">
    <RouterLink
      :to="{ name: kind === 'kex' ? 'kex-attackers' : 'attackers' }"
      class="back muted"
    >
      {{ t(kind === 'kex' ? 'attacker.backKex' : 'attacker.back') }}
    </RouterLink>

    <div class="page-head">
      <h1 v-if="kind === 'kex'">
        {{ t('attacker.kexTitle') }}
        <span
          v-if="kex?.hassh"
          class="data place"
        >{{ kex.hassh }}</span>
      </h1>
      <h1 v-else>
        <span class="data">{{ id }}</span>
        <CountryFlag
          v-if="attacker?.country"
          :code="attacker.country"
          class="place"
        >
          {{ attacker.city ? `, ${attacker.city}` : '' }}
        </CountryFlag>
      </h1>
      <span
        v-if="found"
        class="muted"
      >{{ formatTime(found.first_seen) }} – {{ formatTime(found.last_seen) }}</span>
    </div>

    <SessionFilters :hide="kind === 'kex' ? [] : ['ip', 'country']" />
    <ErrorNotice :error="error" />

    <template v-if="notFound">
      <h2>{{ t(kind === 'kex' ? 'attacker.notFoundKex' : 'attacker.notFound') }}</h2>
      <p class="muted">
        {{ kind === 'kex' ? t('attacker.notFoundKexText') : t('attacker.notFoundText', { ip: id }) }}
      </p>
    </template>

    <template v-else-if="found && summary">
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
            <dd>{{ formatTime(found.first_seen) }}</dd>
            <dt>{{ t('attacker.lastSeen') }}</dt>
            <dd>{{ formatTime(found.last_seen) }}</dd>
            <template v-if="attacker">
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
            </template>
            <template v-if="kex">
              <dt>{{ t('attacker.hassh') }}</dt>
              <dd>
                <RouterLink
                  v-if="kex.hassh"
                  :to="link('kex-attackers', { hassh: kex.hassh })"
                  class="data"
                >
                  {{ kex.hassh }}
                </RouterLink>
                <span
                  v-else
                  class="muted"
                >—</span>
              </dd>
            </template>
            <dt>{{ t('attacker.sensors') }}</dt>
            <dd>{{ found.sensors.join(', ') }}</dd>
            <dt>{{ t('attacker.clients') }}</dt>
            <dd>
              <span
                v-for="c in found.clients"
                :key="c.client"
                class="data line"
              >{{ c.client }} <span class="muted">× {{ c.sessions }}</span></span>
              <span
                v-if="!found.clients.length"
                class="muted"
              >—</span>
            </dd>
          </dl>
        </section>

        <!-- What it is linked to: the other identities it came with. -->
        <section
          v-if="attacker"
          class="card"
        >
          <h2 v-tooltip.top="t('attacker.kexHint')">
            {{ t('attacker.kex') }}
          </h2>
          <BarList
            v-if="attacker.kex.length"
            class="body"
            :items="attacker.kex"
            :value="(k) => k.sessions"
            :item-key="(k) => String(k.id)"
            :to="(k) => link('kex-attacker', {}, { id: String(k.id) })"
          >
            <template #label="{ item }">
              <span class="data">{{ short(item.hassh, item.id) }}</span>
              <span class="muted algs">{{ item.key_algs }}</span>
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
          v-if="kex"
          class="card"
        >
          <h2>{{ t('attacker.addresses') }}</h2>
          <div class="body">
            <BarList
              :items="kex.addresses"
              :value="(a) => a.sessions"
              :item-key="(a) => a.ip"
              :to="(a) => link('attacker', {}, { ip: a.ip })"
            >
              <template #label="{ item }">
                <span class="data">{{ item.ip }}</span>
                <CountryFlag
                  v-if="item.country"
                  :code="item.country"
                  class="flag"
                />
              </template>
              <template #value="{ item }">
                {{ item.sessions.toLocaleString() }}
                <span
                  v-if="item.logged_in"
                  class="logged-in"
                > · {{ item.logged_in.toLocaleString() }}</span>
              </template>
            </BarList>
            <template v-if="kex.variants.length">
              <h2 class="next">
                {{ t('attacker.variants') }}
              </h2>
              <BarList
                :items="kex.variants"
                :value="(v) => v.sessions"
                :item-key="(v) => String(v.id)"
                :to="(v) => link('kex-attacker', {}, { id: String(v.id) })"
              >
                <template #label="{ item }">
                  <span class="data">{{ item.key_algs ?? '—' }}</span>
                </template>
              </BarList>
            </template>
          </div>
        </section>

        <section
          v-if="kex"
          class="card"
        >
          <h2 v-tooltip.top="t('attacker.kexHint')">
            {{ t('attacker.algorithms') }}
          </h2>
          <dl class="facts body">
            <template
              v-for="l in lists"
              :key="l.name"
            >
              <dt>
                {{ t('attacker.lists.' + l.name) }}
                <span class="data muted cowrie">{{ l.cowrie }}</span>
                <span
                  v-if="l.hassh"
                  class="muted cowrie"
                >{{ t('attacker.inHassh') }}</span>
              </dt>
              <dd class="data list">
                {{ l.value ? l.value.replaceAll(',', ', ') : '—' }}
              </dd>
            </template>
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
            :to="(p) => tried(p.username, p.password)"
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
          v-if="keys.length"
          class="card"
        >
          <h2>{{ t('attacker.publicKeys') }}</h2>
          <BarList
            class="body"
            :items="keys"
            :value="(k) => k.sessions"
            :item-key="(k) => k.fingerprint"
          >
            <template #label="{ item }">
              <span
                v-tooltip.top="item.key"
                class="data"
              >{{ item.fingerprint }}</span>
              <span class="muted"> {{ item.type }}</span>
            </template>
          </BarList>
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

.flag {
  margin-left: var(--space-2);
}

/* A key exchange's host key algorithms, after its HASSH. */
.algs {
  margin-left: var(--space-2);
  font-size: 0.875em;
}

/* Cowrie's name of a list, under ours. */
.cowrie {
  display: block;
  font-size: 0.75rem;
  font-weight: normal;
}

/* An algorithm list: long, broken at its commas. */
.list {
  overflow-wrap: anywhere;
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
