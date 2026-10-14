<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import Skeleton from 'openvue/skeleton'
import { ApiError, api } from '@/api/client'
import type { SessionDetail } from '@/api/types'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { endpoint, formatBytes, formatDuration, formatOffset, formatTime } from '@/format'
import { timeline } from '@/timeline'

const props = defineProps<{ id: string }>()

const session = ref<SessionDetail | null>(null)
const missing = ref(false)
const error = ref<string | null>(null)

watch(
  () => props.id,
  async (id) => {
    session.value = null
    missing.value = false
    try {
      session.value = await api.session(id)
      error.value = null
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) missing.value = true
      else error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

const entries = computed(() => (session.value ? timeline(session.value.events) : []))
const offset = (ts: string) => (session.value ? formatOffset(session.value.started_at, ts) : '')
</script>

<template>
  <div class="page">
    <RouterLink
      :to="{ name: 'sessions' }"
      class="back muted"
    >
      All sessions
    </RouterLink>

    <ErrorNotice :error="error" />

    <template v-if="missing">
      <h1>Session not found</h1>
      <p class="muted">
        No session has the identifier {{ id }}. It may come from another cauri instance.
      </p>
    </template>

    <template v-else-if="session">
      <div class="page-head">
        <h1>
          <span class="data">{{ session.src_ip ?? 'Unknown source' }}</span>
          <span
            v-if="session.login_success"
            class="shell verdict"
          >got a shell</span>
          <span
            v-else
            class="muted verdict"
          >no shell</span>
        </h1>
        <span class="muted">{{ formatTime(session.started_at) }} on {{ session.sensor_id }}</span>
      </div>

      <div class="layout">
        <aside>
          <dl class="facts">
            <dt>Source</dt>
            <dd class="data">
              {{ endpoint(session.src_ip, session.src_port) }}
            </dd>
            <dt>Honeypot port</dt>
            <dd>{{ session.dst_port ?? '—' }} <span class="muted">{{ session.protocol }}</span></dd>
            <dt>SSH client</dt>
            <dd class="data">
              {{ session.client_version ?? '—' }}
            </dd>
            <dt>HASSH</dt>
            <dd class="data">
              {{ session.hassh ?? '—' }}
            </dd>
            <dt>Duration</dt>
            <dd>{{ formatDuration(session.duration) }}</dd>
            <dt>Cowrie session</dt>
            <dd class="data">
              {{ session.session }}
            </dd>
          </dl>

          <h2>Recording</h2>
          <p
            v-if="session.tty_available"
            class="recording"
          >
            Stored, {{ formatBytes(session.tty_size) }} over {{ formatDuration(session.tty_duration) }}.
            <span class="muted">Replay is not available in this version.</span>
          </p>
          <p
            v-else-if="session.tty_log"
            class="muted recording"
          >
            Cowrie recorded the terminal; the file has not reached the API yet.
          </p>
          <p
            v-else
            class="muted recording"
          >
            No terminal was opened.
          </p>

          <template v-if="session.transfers.length">
            <h2>Files</h2>
            <ul class="files">
              <li
                v-for="t in session.transfers"
                :key="t.id"
              >
                <span>{{ t.direction === 'download' ? 'Downloaded' : 'Uploaded' }}</span>
                <span class="data">{{ t.url ?? t.filename }}</span>
                <span
                  v-if="t.sha256"
                  class="data muted"
                >sha256 {{ t.sha256 }}</span>
              </li>
            </ul>
          </template>
        </aside>

        <section aria-labelledby="timeline-title">
          <h2 id="timeline-title">
            Timeline
          </h2>
          <ol class="timeline">
            <li
              v-for="(entry, i) in entries"
              :key="i"
              :class="entry.kind"
            >
              <template v-if="entry.kind === 'terminal'">
                <div
                  class="terminal"
                  role="log"
                  aria-label="Commands typed"
                >
                  <div
                    v-for="(line, j) in entry.lines"
                    :key="j"
                    class="line"
                  >
                    <span class="offset">{{ offset(line.ts) }}</span>
                    <span class="prompt">$</span>
                    <span class="input">{{ line.input }}</span>
                    <span
                      v-if="line.failed"
                      class="failed"
                    >command not found</span>
                  </div>
                </div>
              </template>
              <template v-else>
                <span class="offset">{{ offset(entry.ts) }}</span>
                <span :class="entry.tone">
                  {{ entry.text }}
                  <span
                    v-if="entry.detail"
                    class="data"
                  >{{ entry.detail }}</span>
                </span>
              </template>
            </li>
          </ol>
        </section>
      </div>
    </template>

    <template v-else-if="!error">
      <Skeleton
        width="20rem"
        height="2rem"
      />
      <Skeleton
        height="20rem"
        class="skeleton"
      />
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

.back:hover {
  color: var(--c-ink);
}

.verdict {
  margin-left: var(--space-3);
  font-size: 1rem;
  font-weight: 500;
}

.layout {
  display: grid;
  grid-template-columns: minmax(16rem, 22rem) 1fr;
  gap: var(--space-5);
  align-items: start;
}

.facts {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-1) var(--space-3);
  margin: 0 0 var(--space-4);
}

.facts dt {
  color: var(--c-muted);
}

.facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}

aside h2 {
  margin-bottom: var(--space-2);
}

.recording {
  margin: 0 0 var(--space-4);
}

.files {
  margin: 0;
  padding: 0;
  list-style: none;
}

.files li {
  display: flex;
  flex-direction: column;
  margin-bottom: var(--space-2);
  overflow-wrap: anywhere;
}

.timeline {
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.timeline > li {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-1) 0;
  overflow-wrap: anywhere;
}

/* Aligned with the text of the other entries, after their offset column. */
.timeline > li.terminal {
  display: block;
  margin: var(--space-2) 0 var(--space-2) calc(4.5rem + var(--space-3));
}

.offset {
  flex: none;
  width: 4.5rem;
  color: var(--c-muted);
  font-family: var(--font-data);
  font-size: 0.85em;
  text-align: right;
  padding-top: 0.1em;
}

/* The commands as the attacker typed them. */
.terminal {
  padding: var(--space-3);
  border-radius: 6px;
  background: var(--c-term-bg);
  color: var(--c-term-ink);
  font-family: var(--font-data);
  font-size: 0.93rem;
  line-height: 1.7;
  overflow-x: auto;
}

.terminal .line {
  display: flex;
  gap: var(--space-2);
  white-space: pre-wrap;
}

.terminal .offset {
  width: auto;
  min-width: 3.5rem;
  color: var(--c-term-dim);
}

.terminal .prompt {
  color: var(--c-term-prompt);
}

.terminal .failed {
  color: var(--c-term-dim);
  font-style: italic;
}

.skeleton {
  margin-top: var(--space-3);
}

@media (max-width: 600px) {
  .timeline > li.terminal {
    margin-left: 0;
  }
}

@media (max-width: 900px) {
  .layout {
    grid-template-columns: 1fr;
    gap: var(--space-4);
  }
}
</style>
