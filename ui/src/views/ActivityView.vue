<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@/api/client'
import type { Activity } from '@/api/types'
import ErrorNotice from '@/components/ErrorNotice.vue'
import StatsFilters from '@/components/StatsFilters.vue'
import { useStatsFilters } from '@/stats'

// When attacks happen, in UTC: the hours of the week, then the days.
const { t } = useI18n()
const { params } = useStatsFilters()

const activity = ref<Activity | null>(null)
const error = ref<string | null>(null)
watch(
  params,
  async (p) => {
    try {
      activity.value = await api.activity(p)
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

// 2026-10-11 was a Sunday, weekday 0 as in the API.
const dayName = new Intl.DateTimeFormat('en', { weekday: 'short', timeZone: 'UTC' }) // the language of the UI
const weekdays = [0, 1, 2, 3, 4, 5, 6].map((d) => dayName.format(Date.UTC(2026, 9, 11 + d)))
const hours = Array.from({ length: 24 }, (_, h) => h)

const grid = computed(() => {
  const cells = Array.from({ length: 7 }, () => new Array<number>(24).fill(0))
  for (const c of activity.value?.hours ?? []) cells[c.weekday]![c.hour] = c.sessions
  return cells
})
const most = computed(() => Math.max(1, ...grid.value.flat()))
const shade = (n: number) =>
  n ? `color-mix(in oklab, var(--p-primary-color) ${Math.round(12 + 88 * (n / most.value))}%, var(--c-map-land))` : undefined
const pad = (h: number) => String(h).padStart(2, '0')

const busiest = computed(() =>
  [...(activity.value?.hours ?? [])].sort((a, b) => b.sessions - a.sessions).slice(0, 5),
)
const days = computed(() => activity.value?.days ?? [])
const mostDay = computed(() => Math.max(1, ...days.value.map((d) => d.sessions)))
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('activity.title') }}</h1>
      <span class="muted">{{ t('activity.utc') }}</span>
    </div>
    <StatsFilters />
    <ErrorNotice :error="error" />

    <div class="layout">
      <section class="card">
        <h2>{{ t('activity.week') }}</h2>
        <div
          class="heat"
          role="table"
          :aria-label="t('activity.week')"
        >
          <div
            class="row"
            role="row"
          >
            <span role="columnheader" />
            <span
              v-for="h in hours"
              :key="h"
              role="columnheader"
              class="muted hour"
            >{{ h % 6 === 0 ? pad(h) : '' }}</span>
          </div>
          <div
            v-for="(day, d) in weekdays"
            :key="day"
            class="row"
            role="row"
          >
            <span
              role="rowheader"
              class="muted"
            >{{ day }}</span>
            <span
              v-for="h in hours"
              :key="h"
              role="cell"
              class="cell"
              :style="{ background: shade(grid[d]![h]!) }"
              :title="t('activity.cell', { day, from: pad(h), to: pad(h), n: grid[d]![h]!.toLocaleString() }, grid[d]![h]!)"
              :aria-label="t('activity.cell', { day, from: pad(h), to: pad(h), n: grid[d]![h]!.toLocaleString() }, grid[d]![h]!)"
            />
          </div>
        </div>
      </section>

      <section class="card">
        <h2>{{ t('activity.busiest') }}</h2>
        <ol class="busiest">
          <li
            v-for="b in busiest"
            :key="`${b.weekday}-${b.hour}`"
          >
            <span>{{ weekdays[b.weekday] }} {{ pad(b.hour) }}:00</span>
            <span class="num">{{ b.sessions.toLocaleString() }}</span>
          </li>
        </ol>
        <p
          v-if="!busiest.length"
          class="muted"
        >
          {{ t('activity.empty') }}
        </p>
      </section>
    </div>

    <section class="card days">
      <h2>{{ t('activity.days') }}</h2>
      <div
        v-if="days.length"
        class="chart"
      >
        <span
          v-for="d in days"
          :key="d.day"
          class="day"
          :title="t('activity.day', { day: d.day, n: d.sessions.toLocaleString(), loggedIn: d.shells.toLocaleString() })"
        >
          <span
            class="col"
            :style="{ height: `${(100 * d.sessions) / mostDay}%` }"
          >
            <span
              class="shells"
              :style="{ height: `${d.sessions ? (100 * d.shells) / d.sessions : 0}%` }"
            />
          </span>
        </span>
      </div>
      <div
        v-if="days.length"
        class="axis muted"
      >
        <span>{{ days[0]!.day }}</span>
        <span>{{ days[days.length - 1]!.day }}</span>
      </div>
      <p
        v-else
        class="muted"
      >
        {{ t('activity.empty') }}
      </p>
      <p class="muted legend">
        <span class="swatch" /> {{ t('activity.legendSessions') }}
        <span class="swatch shell-swatch" /> {{ t('activity.legendLoggedIn') }}
      </p>
    </section>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 18rem;
  gap: var(--space-4);
  align-items: start;
}

@media (width <= 900px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

.heat {
  display: grid;
  gap: 3px;
}

.row {
  display: grid;
  grid-template-columns: 3rem repeat(24, minmax(0, 1fr));
  gap: 3px;
  align-items: center;
}

.hour {
  font-size: 0.8em;
}

.cell {
  aspect-ratio: 1;
  border-radius: 3px;
  background: var(--c-map-land);
}

.busiest {
  margin: 0;
  padding: 0;
  list-style: none;
}

.busiest li {
  display: flex;
  justify-content: space-between;
  padding: var(--space-1) 0;
  border-bottom: 1px solid var(--c-rule);
}

.num {
  font-variant-numeric: tabular-nums;
}

.days {
  margin-top: var(--space-4);
}

.chart {
  display: flex;
  align-items: flex-end;
  gap: 2px;
  height: 10rem;
}

.day {
  display: flex;
  flex: 1 1 0;
  align-items: flex-end;
  height: 100%;
  min-width: 2px;
}

.col {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  width: 100%;
  min-height: 1px;
  border-radius: 2px 2px 0 0;
  background: var(--p-primary-color);
  overflow: hidden;
}

/* Shells, red as everywhere: the share of the day's sessions. */
.shells {
  background: var(--c-shell);
}

.axis {
  display: flex;
  justify-content: space-between;
  font-size: 0.85em;
}

.legend {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.85em;
}

.swatch {
  display: inline-block;
  width: 0.8rem;
  height: 0.8rem;
  border-radius: 2px;
  background: var(--p-primary-color);
}

.shell-swatch {
  margin-left: var(--space-3);
  background: var(--c-shell);
}
</style>
