<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from 'openvue/badge'
import Popover from 'openvue/popover'
import type { Credential } from '@/api/types'

// The credentials a session tried, in a table cell: the first, the one that
// logged in if any, cut at a width, and a badge with how many other pairs;
// each pair listed once in a popover, on hover or focus, with how many
// times it was tried. Attackers choose them: long passwords must not widen
// the table. A popover, not a tooltip: the theme sizes tooltips for a few
// words, popovers for content.
const props = defineProps<{
  /** The credentials listed by the API (a bounded number). */
  credentials: Credential[]
  /** All the session's login attempts, beyond those listed. */
  attempts: number
}>()
const { t } = useI18n()

const width = 20 // characters of the pair shown, as .pair's max-width
const shown = (s: string) => (s === '' ? t('session.logins.emptyPassword') : s)
const pair = (c: Credential) => `${shown(c.username)} / ${shown(c.password)}`

// Each pair once, with how many times it was tried; the one that logged
// in first.
const distinct = computed(() => {
  const seen = new Map<string, { c: Credential; n: number }>()
  for (const c of props.credentials) {
    const key = `${c.username}\u0000${c.password}`
    const d = seen.get(key)
    if (d) {
      d.n++
      d.c = d.c.success ? d.c : c
    } else {
      seen.set(key, { c, n: 1 })
    }
  }
  return [...seen.values()].sort((a, b) => Number(b.c.success) - Number(a.c.success))
})
const first = computed(() => distinct.value[0]?.c)
// Attempts beyond those the API lists may repeat a pair: counted as others.
const unlisted = computed(() => Math.max(props.attempts - props.credentials.length, 0))
const others = computed(() => Math.max(distinct.value.length - 1, 0) + unlisted.value)
// The list, when there is more to see than the cell shows.
const more = computed(() => others.value > 0 || (first.value ? pair(first.value).length > width : false))
const label = computed(() => {
  const n = distinct.value.length + unlisted.value
  return t('sessions.credentialsLabel', { n }, n)
})

const overlay = ref<InstanceType<typeof Popover> | null>(null)
function show(e: Event) {
  if (more.value) overlay.value?.show(e, e.currentTarget)
}
function hide() {
  overlay.value?.hide()
}
</script>

<template>
  <span
    v-if="first"
    class="credentials"
    :tabindex="more ? 0 : undefined"
    :aria-label="more ? label : undefined"
    @mouseenter="show"
    @mouseleave="hide"
    @focus="show"
    @blur="hide"
  >
    <span
      class="data pair"
      :class="{ 'logged-in': first.success }"
    >{{ pair(first) }}</span>
    <Badge
      v-if="others"
      :value="`+${others}`"
      severity="secondary"
      size="small"
    />
  </span>
  <Popover
    v-if="more"
    ref="overlay"
  >
    <ul class="pairs data">
      <li
        v-for="d in distinct"
        :key="d.c.username + '\u0000' + d.c.password"
        :class="{ 'logged-in': d.c.success }"
      >
        {{ pair(d.c) }}<span
          v-if="d.n > 1"
          class="muted"
        > ×{{ d.n }}</span>
      </li>
      <li
        v-if="unlisted"
        class="muted"
      >
        {{ t('sessions.moreCredentials', { n: unlisted }) }}
      </li>
    </ul>
  </Popover>
</template>

<style scoped>
/* The whole cell: the badge against its right edge, aligned row to row. */
.credentials {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  white-space: nowrap;
}

.credentials > :deep(.p-badge) {
  margin-left: auto;
}

/* Attackers' passwords can be long: cut at a width, whole in the popover. */
.pair {
  display: inline-block;
  max-width: 20ch;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}

/* One pair per line, never wrapped. */
.pairs {
  margin: 0;
  padding: 0;
  list-style: none;
  white-space: nowrap;
}

.pairs li + li {
  margin-top: var(--space-1);
}
</style>
