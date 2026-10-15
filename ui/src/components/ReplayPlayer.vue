<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import SelectButton from 'openvue/selectbutton'
import ToggleSwitch from 'openvue/toggleswitch'
import * as AsciinemaPlayer from 'asciinema-player'
import type { Player } from 'asciinema-player'
import 'asciinema-player/dist/bundle/asciinema-player.css'
import { api } from '@/api/client'
import { markerTimes, terminalSize } from '@/replay'

// Replays a session from GET /api/sessions/{id}/cast, with chapters: the
// cast holds a marker per command. The cast keeps the real timing; by
// default its header asks the player to shorten pauses over 2 s, and "Real
// time" reloads it without that limit: the real rhythm of typing tells a
// human from a bot.
//
// The cast is fetched here rather than by the player, to compute where the
// markers fall on the player's timeline (see replay.ts); the page uses
// them to follow the current chapter.
const props = defineProps<{ sessionId: string }>()
const emit = defineEmits<{
  /** Current position, in seconds of the player's timeline. */
  time: [seconds: number]
  /** Positions of the chapters (markers), on the same timeline. */
  markers: [seconds: number[]]
}>()
const { t } = useI18n()

const container = ref<HTMLElement | null>(null)
const realTime = ref(false)
const speed = ref(1)
const speeds = [0.5, 1, 2, 4, 8].map((s) => ({ label: `${s}×`, value: s }))
const failed = ref(false)
// The frame has the terminal's proportions (a cell is about 0.6 × 1.33
// em in the player), so that the player, which fits it in both
// directions, fills it unless the frame's height cap applies.
const aspectRatio = ref<string | undefined>()

const player = shallowRef<Player | null>(null)
let cast = ''
let playing = false

async function fetchCast() {
  failed.value = false
  try {
    const res = await fetch(api.castUrl(props.sessionId, realTime.value))
    if (!res.ok) throw new Error(res.statusText)
    cast = await res.text()
  } catch {
    failed.value = true
    cast = ''
  }
  const size = cast ? terminalSize(cast) : null
  aspectRatio.value = size ? `${size.cols * 0.6} / ${size.rows * 1.3333}` : undefined
  emit('markers', cast ? markerTimes(cast) : [])
}

/** Creates the player, optionally at a position and playing. */
async function mount(at = 0, play = false) {
  player.value?.dispose()
  player.value = null
  playing = false
  if (!container.value || !cast) return
  const p = AsciinemaPlayer.create({ data: cast }, container.value, {
    theme: 'cauri',
    fit: 'both',
    preload: true,
    speed: speed.value,
    terminalFontFamily: "'IBM Plex Mono', ui-monospace, monospace",
  })
  p.addEventListener('play', () => (playing = true))
  p.addEventListener('playing', () => (playing = true))
  p.addEventListener('pause', () => (playing = false))
  p.addEventListener('ended', () => (playing = false))
  player.value = p
  if (at > 0) await p.seek(at)
  if (play) await p.play()
}

async function load() {
  await fetchCast()
  await mount()
}

// The player cannot change speed while running: recreate it in place.
watch(speed, () => void mount(player.value?.getCurrentTime() ?? 0, playing))
watch([() => props.sessionId, realTime], () => void load())
watch(container, (el) => el && void load())

const ticker = setInterval(() => {
  if (player.value) emit('time', player.value.getCurrentTime())
}, 200)
onBeforeUnmount(() => {
  clearInterval(ticker)
  player.value?.dispose()
})

function skip(seconds: number) {
  const p = player.value
  if (!p) return
  const end = p.getDuration() ?? Infinity
  void p.seek(Math.min(Math.max(p.getCurrentTime() + seconds, 0), end))
}

/** Moves to a chapter, by its index among the markers. */
function seekChapter(index: number) {
  void player.value?.seek({ marker: index })
}

defineExpose({ seekChapter })
</script>

<template>
  <div class="replay">
    <div
      ref="container"
      class="player"
      :style="{ aspectRatio }"
      :aria-label="t('session.replay.player')"
    />
    <p
      v-if="failed"
      class="muted"
    >
      {{ t('session.replay.failed') }}
    </p>
    <div
      class="controls"
      role="toolbar"
      :aria-label="t('session.replay.controls')"
    >
      <span class="group">
        <Button
          v-for="s in [-30, -10, 10, 30]"
          :key="s"
          :label="s < 0 ? `−${-s} s` : `+${s} s`"
          :aria-label="t(s < 0 ? 'session.replay.back' : 'session.replay.forward', { n: Math.abs(s) })"
          severity="secondary"
          size="small"
          text
          @click="skip(s)"
        />
      </span>
      <span class="group">
        <Button
          :label="t('session.replay.previousCommand')"
          severity="secondary"
          size="small"
          outlined
          @click="player?.seek({ marker: 'prev' })"
        />
        <Button
          :label="t('session.replay.nextCommand')"
          severity="secondary"
          size="small"
          outlined
          @click="player?.seek({ marker: 'next' })"
        />
      </span>
      <SelectButton
        v-model="speed"
        :options="speeds"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        size="small"
        :aria-label="t('session.replay.speed')"
      />
    </div>
    <label class="real-time">
      <ToggleSwitch v-model="realTime" />
      {{ t('session.replay.realTime') }}
      <span class="muted">{{ realTime ? t('session.replay.realTimeOn') : t('session.replay.realTimeOff') }}</span>
    </label>
  </div>
</template>

<style scoped>
.replay {
  margin: var(--space-2) 0 var(--space-4);
}

/* At most 60 % of the screen's height, so that the controls below stay in
   view; the terminal's background fills the margins this leaves. */
.player {
  /* Explicit, or the height cap would narrow the frame through its ratio
     and leave a gap beside the replay's column. */
  width: 100%;
  max-height: 60vh;
  background: var(--c-term-bg);
  border-radius: 6px;
  overflow: hidden;
}

/* The player sizes itself to the scaled terminal and centres it, which
   leaves bands and a shorter control bar: stretched, it reads as one
   terminal, text from the left edge, controls along the whole bottom. */
.player :deep(.ap-player) {
  width: 100% !important;
}

.controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  margin-top: var(--space-2);
}

.group {
  display: inline-flex;
  gap: var(--space-1);
}

.real-time {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
</style>
