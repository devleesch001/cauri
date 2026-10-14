<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ToggleSwitch from 'openvue/toggleswitch'
import * as AsciinemaPlayer from 'asciinema-player'
import type { Player } from 'asciinema-player'
import 'asciinema-player/dist/bundle/asciinema-player.css'

// Replays a session from GET /api/sessions/{id}/cast. The cast keeps the
// real timing; by default its header asks the player to shorten pauses
// over 2 s. "Real time" reloads it without that limit: the real rhythm of
// typing tells a human from a bot.
const props = defineProps<{ sessionId: string }>()
const { t } = useI18n()

const container = ref<HTMLElement | null>(null)
const realTime = ref(false)
let player: Player | null = null

function load() {
  player?.dispose()
  player = null
  if (!container.value) return
  const url = `/api/sessions/${encodeURIComponent(props.sessionId)}/cast` + (realTime.value ? '?idle_time_limit=0' : '')
  player = AsciinemaPlayer.create({ url }, container.value, {
    theme: 'cauri',
    fit: 'width',
    preload: true,
    terminalFontFamily: "'IBM Plex Mono', ui-monospace, monospace",
  })
}

onMounted(load)
watch([() => props.sessionId, realTime], load)
onBeforeUnmount(() => player?.dispose())
</script>

<template>
  <div class="replay">
    <div
      ref="container"
      class="player"
      :aria-label="t('session.replay.player')"
    />
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

/* fit: 'width' scales the font to the width: cap it to stay readable. */
.player {
  max-width: 52rem;
  border-radius: 6px;
  overflow: hidden;
}

.real-time {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
</style>
