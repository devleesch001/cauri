<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import { api } from '@/api/client'
import type { SessionSummary } from '@/api/types'
import CountryFlag from '@/components/CountryFlag.vue'
import { endpoint } from '@/format'

// The latest sessions, polled: simple, and works through any proxy. The
// sensor and human/bot filters apply; days do not, "latest" being now.
const props = defineProps<{ sensor: string; verdict: string }>()
const { t } = useI18n()

const every = 10_000
const items = ref<SessionSummary[]>([])
const paused = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

async function load() {
  if (paused.value || document.hidden) return
  try {
    items.value = (await api.sessions({ sensor: props.sensor, verdict: props.verdict, limit: 8 })).items
  } catch {
    // the next poll retries; the pages show API errors
  }
}
onMounted(() => {
  void load()
  timer = setInterval(() => void load(), every)
  document.addEventListener('visibilitychange', load)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', load)
})
watch(() => [props.sensor, props.verdict], () => void load())
watch(paused, (p) => p || void load())

const time = (s: string) => new Date(s).toLocaleTimeString()
</script>

<template>
  <section class="card">
    <div class="head">
      <h2>{{ t('overview.live.title') }}</h2>
      <Button
        :label="paused ? t('overview.live.resume') : t('overview.live.pause')"
        severity="secondary"
        text
        size="small"
        @click="paused = !paused"
      />
    </div>
    <ol class="feed">
      <li
        v-for="s in items"
        :key="s.id"
      >
        <RouterLink
          :to="{ name: 'session', params: { id: String(s.id) } }"
          class="data time"
        >
          {{ time(s.started_at) }}
        </RouterLink>
        <CountryFlag
          v-if="s.geo_country"
          :code="s.geo_country"
          class="where"
        />
        <span
          v-else
          class="where muted"
        >—</span>
        <span class="data ip">{{ endpoint(s.src_ip, null) }}</span>
        <span
          v-if="s.login_success"
          class="shell"
        >{{ t('sessions.outcome.shell') }}</span>
        <span
          v-else
          class="muted"
        >{{ s.login_attempts ? t('sessions.outcome.refused') : t('sessions.outcome.noLogin') }}</span>
      </li>
    </ol>
    <p
      v-if="!items.length"
      class="muted"
    >
      {{ t('overview.live.empty') }}
    </p>
  </section>
</template>

<style scoped>
.head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

h2 {
  margin: 0 0 var(--space-3);
  font-size: 1rem;
}

.feed {
  margin: 0;
  padding: 0;
  list-style: none;
}

li {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  gap: var(--space-3);
  align-items: baseline;
  padding: var(--space-1) 0;
  border-bottom: 1px solid var(--c-rule);
}

li:last-child {
  border-bottom: 0;
}

.time {
  text-decoration: none;
}

.where {
  overflow: hidden;
}
</style>
