<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DataTable, { type DataTableRowClickEvent } from 'openvue/datatable'
import Column from 'openvue/column'
import InputText from 'openvue/inputtext'
import Select from 'openvue/select'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import Button from 'openvue/button'
import Chip from 'openvue/chip'
import Popover from 'openvue/popover'
import { api } from '@/api/client'
import type { Attacker, CountryCount, KexAttacker } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import DayRange from '@/components/DayRange.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import KexAlgorithms from '@/components/KexAlgorithms.vue'
import { dayRange } from '@/dates'
import { countryName, formatTime } from '@/format'
import { patchQuery, queryValue } from '@/router'
import { notFilters, pageSizes, useTablePage, useTableSort } from '@/sort'

// Attackers, by one of their identities: the source addresses sessions came
// from, or the key exchanges their SSH tools offered, with what came with
// each. A row opens its profile, with the same period and verdict.
const props = defineProps<{ kind: 'ip' | 'kex' }>()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const q = (k: string) => queryValue(route.query, k)

// The filters both kinds share follow a change of kind.
const shared = ['sensor', 'from', 'to', 'verdict', 'logged_in']
const kindOptions = computed(() => [
  { label: t('attackers.kinds.ip'), value: 'ip' },
  { label: t('attackers.kinds.kex'), value: 'kex' },
])
const kind = computed({
  get: () => props.kind,
  set: (v: 'ip' | 'kex') => {
    const query = Object.fromEntries(shared.filter((k) => q(k)).map((k) => [k, q(k)]))
    void router.push({ name: v === 'ip' ? 'attackers' : 'kex-attackers', query })
  },
})

// The address is edited locally and applied on Enter or leaving the field.
const prefix = ref('')
watch(
  () => route.query,
  () => (prefix.value = q('ip')),
  { immediate: true },
)
function applyPrefix() {
  void patchQuery({ ip: prefix.value.trim() || undefined })
}

const countries = ref<CountryCount[]>([])
watch(
  () => q('sensor'),
  async (sensor) => {
    countries.value = await api.countries({ sensor }).catch(() => [])
  },
  { immediate: true },
)
const countryOptions = computed(() =>
  countries.value.map((c) => ({ label: countryName(c.country), value: c.country, count: c.sessions })),
)
const country = computed({
  get: () => q('country') || null,
  set: (v: string | null) => void patchQuery({ country: v ?? undefined }),
})
// Bots include uncertain sessions and those without interaction (the API counts them so).
const verdict = computed({
  get: () => q('verdict') || 'any',
  set: (v: string) => void patchQuery({ verdict: v === 'any' ? undefined : v }),
})
const verdictOptions = computed(() => [
  { label: t('sessions.filters.anyVerdict'), value: 'any' },
  { label: t('sessions.filters.humans'), value: 'human' },
  { label: t('sessions.filters.bots'), value: 'bot' },
])
const loggedIn = computed({
  get: () => q('logged_in') === 'true',
  set: (v: boolean) => void patchQuery({ logged_in: v ? 'true' : undefined }),
})
const from = computed({ get: () => q('from'), set: (v: string) => void patchQuery({ from: v }) })
const to = computed({ get: () => q('to'), set: (v: string) => void patchQuery({ to: v }) })
const { rows, page, params: pageParams, onPage } = useTablePage(50)
const filtered = computed(() => Object.keys(route.query).some((k) => !notFilters.includes(k)))
const { params: sortParams, kept, field: sortField, direction, onSort } = useTableSort('sessions')

const items = ref<Attacker[]>([])
const kexItems = ref<KexAttacker[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

watch(
  [() => route.query, () => props.kind],
  async () => {
    loading.value = true
    const common = {
      sensor: q('sensor'), verdict: q('verdict'), logged_in: q('logged_in'), ...sortParams.value,
      ...dayRange(q('from'), q('to')), ...pageParams.value,
    }
    try {
      if (props.kind === 'kex') {
        const res = await api.kexAttackers({ ...common, hassh: q('hassh') })
        kexItems.value = res.items
        total.value = res.total
      } else {
        const res = await api.attackers({ ...common, country: q('country'), asn: q('asn'), ip: q('ip') })
        items.value = res.items
        total.value = res.total
        void measureNetworks()
      }
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

// A network longer than its column is cut, whole in a tooltip. The text is
// proportional: which ones are cut is measured once the rows are drawn
// (and the fonts loaded), not guessed from their length.
const root = ref<HTMLElement | null>(null)
const cutNetworks = ref(new Set<string>())
async function measureNetworks() {
  await nextTick()
  await document.fonts.ready
  const cut = new Set<string>()
  root.value?.querySelectorAll<HTMLElement>('.network[data-ip]').forEach((el) => {
    if (el.scrollWidth > el.clientWidth) cut.add(el.dataset.ip!)
  })
  cutNetworks.value = cut
}

function open(e: DataTableRowClickEvent<Attacker | KexAttacker>) {
  const query: Record<string, string> = {}
  for (const k of ['sensor', 'from', 'to', 'verdict']) {
    if (q(k)) query[k] = q(k)
  }
  if ('ip' in e.data) {
    void router.push({ name: 'attacker', params: { ip: e.data.ip }, query })
  } else {
    void router.push({ name: 'kex-attacker', params: { id: String(e.data.id) }, query })
  }
}
// The verdict most of its sessions got.
function mostly(a: Attacker | KexAttacker) {
  const counts: [string, number][] = [
    ['human', a.humans], ['bot', a.bots], ['uncertain', a.uncertain], ['no_interaction', a.no_interaction],
  ]
  const [verdict, n] = counts.reduce((best, c) => (c[1] > best[1] ? c : best))
  return n > 0 ? verdict : null
}
// A key exchange's lists, over its row, on hover or focus of its host keys.
const listsOverlay = ref<InstanceType<typeof Popover> | null>(null)
const shownKex = ref<KexAttacker | null>(null)
function showLists(e: Event, k: KexAttacker) {
  shownKex.value = k
  listsOverlay.value?.show(e, e.currentTarget)
}
// Without room above or below, the lists cover their row: going from one
// to the other must not close them, or they would close and reopen under
// the pointer, flickering. They close once it leaves both.
function hideLists(e: Event) {
  const to = e instanceof MouseEvent ? (e.relatedTarget as Element | null) : null
  if (to?.closest('.p-popover, .algs')) return
  listsOverlay.value?.hide()
}
function clearFilters() {
  void router.replace({ query: kept.value })
}
</script>

<template>
  <div
    ref="root"
    class="page"
  >
    <div class="page-head">
      <h1>{{ t('attackers.title') }}</h1>
      <span class="muted">{{ t(kind === 'kex' ? 'attackers.countKex' : 'attackers.count', { n: total.toLocaleString() }, total) }}</span>
    </div>
    <SelectButton
      v-model="kind"
      class="kinds"
      :options="kindOptions"
      option-label="label"
      option-value="value"
      :allow-empty="false"
      :aria-label="t('attackers.by')"
    />

    <div
      class="filter-rows"
      role="search"
    >
      <div
        v-if="kind === 'ip'"
        class="filters"
      >
        <InputText
          v-model="prefix"
          class="data"
          :placeholder="t('attackers.filters.address')"
          :aria-label="t('attackers.filters.address')"
          @keyup.enter="applyPrefix"
          @blur="applyPrefix"
        />
        <Select
          v-model="country"
          :options="countryOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('sessions.filters.country')"
          :aria-label="t('sessions.columns.country')"
          show-clear
          filter
        >
          <template #value="{ value, placeholder }">
            <CountryFlag
              v-if="value"
              :code="value"
            />
            <span v-else>{{ placeholder }}</span>
          </template>
          <template #option="{ option }">
            <CountryFlag :code="option.value" />
            <span class="muted count">{{ option.count }}</span>
          </template>
        </Select>
        <Chip
          v-if="q('asn')"
          removable
          :label="`AS${q('asn')}`"
          :aria-label="t('sessions.filters.asn', { asn: q('asn') })"
          @remove="patchQuery({ asn: undefined })"
        />
        <DayRange
          v-model:from="from"
          v-model:to="to"
        />
      </div>
      <div
        v-else
        class="filters"
      >
        <Chip
          v-if="q('hassh')"
          removable
          class="data"
          :label="t('attackers.filters.hasshChip', { hassh: q('hassh').slice(0, 12) })"
          :aria-label="t('attackers.filters.hassh', { hassh: q('hassh') })"
          @remove="patchQuery({ hassh: undefined })"
        />
        <DayRange
          v-model:from="from"
          v-model:to="to"
        />
      </div>
      <div class="filters">
        <SelectButton
          v-model="verdict"
          :options="verdictOptions"
          option-label="label"
          option-value="value"
          :allow-empty="false"
          :aria-label="t('sessions.filters.verdict')"
        />
        <label class="toggle">
          <ToggleSwitch v-model="loggedIn" />
          {{ t('attackers.filters.loggedIn') }}
        </label>
        <Button
          v-if="filtered"
          :label="t('filters.clear')"
          severity="secondary"
          text
          @click="clearFilters"
        />
      </div>
    </div>

    <ErrorNotice :error="error" />

    <DataTable
      :value="kind === 'kex' ? kexItems : items"
      :loading="loading"
      lazy
      paginator
      :rows="rows"
      :rows-per-page-options="pageSizes"
      :first="(page - 1) * rows"
      :total-records="total"
      :sort-field="sortField"
      :sort-order="direction"
      :data-key="kind === 'kex' ? 'id' : 'ip'"
      size="small"
      striped-rows
      scrollable
      @page="onPage"
      @sort="onSort"
      @row-click="open"
    >
      <template #empty>
        <p class="muted">
          {{ filtered ? t('attackers.empty.filtered') : t('attackers.empty.none') }}
        </p>
      </template>
      <template v-if="kind === 'kex'">
        <Column :header="t('attackers.columns.hassh')">
          <template #body="{ data }">
            <RouterLink
              v-tooltip.top="data.hassh"
              :to="{ name: 'kex-attacker', params: { id: String(data.id) } }"
              class="data"
              @click.stop
            >
              {{ data.hassh ? data.hassh.slice(0, 12) : '#' + data.id }}
            </RouterLink>
          </template>
        </Column>
        <!-- What tells apart the combinations of one HASSH, cut; all the
             lists on hover or focus. -->
        <Column :header="t('attackers.columns.keyAlgs')">
          <template #body="{ data }">
            <span
              class="data algs"
              tabindex="0"
              :aria-label="t('attackers.listsLabel')"
              @mouseenter="showLists($event, data)"
              @mouseleave="hideLists"
              @focus="showLists($event, data)"
              @blur="hideLists"
            >{{ data.key_algs ?? '—' }}</span>
          </template>
        </Column>
        <Column
          field="sources"
          :header="t('attackers.columns.sources')"
          sortable
          class="num"
        >
          <template #body="{ data }">
            {{ data.sources.toLocaleString() }}
          </template>
        </Column>
      </template>
      <template v-else>
        <Column :header="t('attackers.columns.address')">
          <template #body="{ data }">
            <RouterLink
              :to="{ name: 'attacker', params: { ip: data.ip } }"
              class="data"
              @click.stop
            >
              {{ data.ip }}
            </RouterLink>
          </template>
        </Column>
        <Column :header="t('sessions.columns.country')">
          <template #body="{ data }">
            <CountryFlag
              v-if="data.country"
              :code="data.country"
            />
            <span
              v-else
              class="muted"
            >—</span>
          </template>
        </Column>
        <Column :header="t('attackers.columns.network')">
          <template #body="{ data }">
            <span
              v-if="data.asn"
              v-tooltip.top="cutNetworks.has(data.ip) ? `AS${data.asn} ${data.as_org ?? ''}` : null"
              class="network"
              :data-ip="data.ip"
            >AS{{ data.asn }} <span class="muted">{{ data.as_org }}</span></span>
            <span
              v-else
              class="muted"
            >—</span>
          </template>
        </Column>
      </template>
      <Column
        field="sessions"
        :header="t('attackers.columns.sessions')"
        sortable
        class="num"
      >
        <template #body="{ data }">
          {{ data.sessions.toLocaleString() }}
        </template>
      </Column>
      <Column
        field="logged_in"
        :header="t('attackers.columns.loggedIn')"
        sortable
        class="num"
      >
        <template #body="{ data }">
          <span
            v-if="data.logged_in"
            class="logged-in"
          >{{ data.logged_in.toLocaleString() }}</span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
      <Column
        :header="t('attackers.columns.logins')"
        class="num"
      >
        <template #body="{ data }">
          {{ data.logins ? data.logins.toLocaleString() : '—' }}
        </template>
      </Column>
      <Column
        :header="t('attackers.columns.firstSeen')"
        body-class="nowrap"
      >
        <template #body="{ data }">
          {{ formatTime(data.first_seen) }}
        </template>
      </Column>
      <Column
        field="last_seen"
        :header="t('attackers.columns.lastSeen')"
        body-class="nowrap"
        sortable
      >
        <template #body="{ data }">
          {{ formatTime(data.last_seen) }}
        </template>
      </Column>
      <Column :header="t('attackers.columns.verdict')">
        <template #body="{ data }">
          <span
            v-if="mostly(data)"
            class="badge"
            :class="'badge-' + mostly(data)"
          >{{ t('session.verdict.' + mostly(data)) }}</span>
          <span
            v-else
            class="muted"
          >—</span>
        </template>
      </Column>
    </DataTable>
    <Popover
      ref="listsOverlay"
      @mouseleave="hideLists"
    >
      <KexAlgorithms
        v-if="shownKex"
        :kex="shownKex"
      />
    </Popover>
  </div>
</template>

<style scoped>
.count {
  margin-left: auto;
  padding-left: var(--space-3);
}

/* Room for most networks ("AS197170 TechTies Inc."), then cut. */
.network {
  display: inline-block;
  max-width: 30ch;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.kinds {
  margin-bottom: var(--space-3);
}

/* Host key algorithms are long: cut, whole on hover. */
.algs {
  display: inline-block;
  max-width: 32ch;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

:deep(.num) {
  text-align: right;
}

/* Headers and dates on one line: the table scrolls rather than wrap them. */
:deep(.p-datatable-column-title),
:deep(.nowrap) {
  white-space: nowrap;
}
</style>
