<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Chapter } from '@/api/types'

// The commands of a session, each with what it printed. With a replay
// (seekable), a click moves the player to the command and the current one
// is highlighted; without one, it is a plain list.
const props = defineProps<{
  chapters: Chapter[]
  current: number // index of the current chapter, -1 if none
  seekable: boolean
}>()
const emit = defineEmits<{ seek: [index: number] }>()
const { t } = useI18n()

// Long outputs are folded to their first lines.
const foldLines = 6
const unfolded = ref(new Set<number>())
const lines = (c: Chapter) => (c.output ? c.output.split('\n') : [])
function shown(c: Chapter, i: number) {
  const all = lines(c)
  return unfolded.value.has(i) ? all : all.slice(0, foldLines)
}
function toggle(i: number) {
  const s = new Set(unfolded.value)
  if (s.has(i)) s.delete(i)
  else s.add(i)
  unfolded.value = s
}

function clock(seconds: number) {
  const s = Math.floor(seconds)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// Keep the current chapter visible inside the list, without scrolling the
// page.
const list = ref<HTMLElement | null>(null)
watch(
  () => props.current,
  async (i) => {
    await nextTick()
    const box = list.value
    const item = box?.querySelector<HTMLElement>(`[data-index="${i}"]`)
    if (!box || !item) return
    const top = item.offsetTop - box.offsetTop
    if (top < box.scrollTop || top + item.offsetHeight > box.scrollTop + box.clientHeight) {
      box.scrollTop = Math.max(top - box.clientHeight / 3, 0)
    }
  },
)
</script>

<template>
  <ol
    ref="list"
    class="chapters"
  >
    <li
      v-for="(c, i) in chapters"
      :key="i"
      :data-index="i"
      :class="{ current: i === current }"
      :aria-current="i === current ? 'step' : undefined"
    >
      <component
        :is="seekable ? 'button' : 'div'"
        class="head"
        :type="seekable ? 'button' : undefined"
        @click="seekable && emit('seek', i)"
      >
        <span class="num">{{ i + 1 }}</span>
        <span class="cmd data">{{ c.command }}</span>
        <span class="time data">{{ clock(c.time) }}</span>
      </component>
      <template v-if="seekable">
        <p
          v-if="c.full_screen"
          class="note"
        >
          {{ t('session.chapters.fullScreen') }}
        </p>
        <template v-else-if="c.output">
          <pre class="output data">{{ shown(c, i).join('\n') }}</pre>
          <button
            v-if="lines(c).length > foldLines"
            type="button"
            class="fold"
            @click="toggle(i)"
          >
            {{ unfolded.has(i) ? t('session.chapters.showLess') : t('session.chapters.showAll', { n: lines(c).length }) }}
          </button>
        </template>
        <p
          v-else
          class="note"
        >
          {{ t('session.chapters.noOutput') }}
        </p>
        <p
          v-if="c.truncated"
          class="note"
        >
          {{ t('session.chapters.truncated') }}
        </p>
      </template>
    </li>
  </ol>
</template>

<style scoped>
.chapters {
  max-height: 34rem;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}

.chapters > li {
  padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
  border-left: 2px solid transparent;
}

.chapters > li + li {
  border-top: 1px solid var(--c-rule);
}

.chapters > li.current {
  border-left-color: var(--p-primary-color);
  background: color-mix(in srgb, var(--p-primary-color) 7%, transparent);
}

.head {
  display: grid;
  grid-template-columns: 1.5rem 1fr auto;
  gap: var(--space-2);
  align-items: baseline;
  width: 100%;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
}

button.head {
  cursor: pointer;
}

button.head:hover .cmd {
  text-decoration: underline;
}

.num,
.time {
  color: var(--c-muted);
  font-size: 0.85em;
}

.cmd {
  overflow-wrap: anywhere;
}

.output {
  margin: var(--space-1) 0 0 calc(1.5rem + var(--space-2));
  padding: var(--space-2);
  border-radius: 4px;
  background: var(--c-term-bg);
  color: var(--c-term-ink);
  font-size: 0.85rem;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.note,
.fold {
  margin: var(--space-1) 0 0 calc(1.5rem + var(--space-2));
  color: var(--c-muted);
  font-size: 0.9em;
}

.fold {
  padding: 0;
  border: 0;
  background: none;
  cursor: pointer;
  text-decoration: underline;
}
</style>
