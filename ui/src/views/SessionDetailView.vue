<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Skeleton from 'openvue/skeleton'
import { ApiError, api } from '@/api/client'
import type { Chapter, Chapters, SessionDetail, Verdict } from '@/api/types'
import ChapterList from '@/components/ChapterList.vue'
import CountryFlag from '@/components/CountryFlag.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import ReplayPlayer from '@/components/ReplayPlayer.vue'
import { endpoint, formatBytes, formatMillis, formatOffset, formatTime } from '@/format'
import { timeline } from '@/timeline'

const props = defineProps<{ id: string }>()
const { t } = useI18n()

const session = ref<SessionDetail | null>(null)
const chapters = ref<Chapters | null>(null)
const verdict = ref<Verdict | null>(null)
// Declared before the watch below, which runs at once.
const forceSimulated = ref(false) // the viewer asked for the commands alone
const recordingGone = ref(false) // the API found the recording's file missing
// Position of the player and of its markers, on its own timeline.
const playerTime = ref(0)
const markers = ref<number[]>([])
const missing = ref(false)
const error = ref<string | null>(null)

watch(
  () => props.id,
  async (id) => {
    session.value = null
    missing.value = false
    chapters.value = null
    verdict.value = null
    markers.value = []
    forceSimulated.value = false
    recordingGone.value = false
    try {
      session.value = await api.session(id)
      error.value = null
      // Optional: the page works without it.
      void api.verdict(id).then((v) => (verdict.value = v)).catch(() => undefined)
      if (replayable.value) {
        // Without chapters, the commands are listed without their output.
        chapters.value = await api.chapters(id).catch(() => null)
        // A recording listed whose file is gone: the API played the commands.
        recordingGone.value = !!session.value.tty_available && !!chapters.value?.simulated
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) missing.value = true
      else error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

// With a recording the replay is the terminal's; without one, but with
// commands, the API simulates it from them.
const replayable = computed(() => !!session.value && (session.value.tty_available || session.value.command_list.length > 0))
// What is played: the stored recording when there is one and its file is
// still there, else the commands alone. The viewer may ask for the commands
// alone even where a recording exists.
const hasRecording = computed(() => !!session.value?.tty_available && !recordingGone.value)
const simulated = computed(() => !hasRecording.value || forceSimulated.value)

// Chapters follow what is played: with their output, or without.
watch(forceSimulated, async (forced) => {
  if (!session.value || !replayable.value) return
  chapters.value = await api.chapters(props.id, forced).catch(() => null)
})

const entries = computed(() => (session.value ? timeline(session.value.events) : []))
const offset = (ts: string) => (session.value ? formatOffset(session.value.started_at, ts) : '')

// Replay and chapters: the player reports its position and where its
// markers (one per command, in order) fall, on its own timeline.
const player = ref<InstanceType<typeof ReplayPlayer> | null>(null)
const currentChapter = computed(() => {
  let current = -1
  markers.value.forEach((m, i) => {
    if (m <= playerTime.value + 0.05) current = i
  })
  return current
})

// The commands, with their output when the recording gives it.
const commandChapters = computed<Chapter[]>(() => {
  if (chapters.value) return chapters.value.chapters
  const s = session.value
  if (!s) return []
  const start = new Date(s.started_at).getTime()
  return s.command_list.map((c) => ({
    time: Math.max((new Date(c.ts).getTime() - start) / 1000, 0),
    command: c.input,
    output: '',
    truncated: false,
    full_screen: false,
  }))
})

const signed = (n: number) => (n > 0 ? `+${n}` : String(n))

const eventsOf = (id: string) => session.value?.events.filter((e) => e.eventid === id) ?? []

const user = computed(() => session.value?.login_attempt_list.find((l) => l.success)?.username)

const terminal = computed(() => {
  const size = eventsOf('cowrie.client.size')[0]?.data
  return size ? `${String(size.width)}×${String(size.height)}` : undefined
})

// A stored recording means one shell channel; commands without it come
// from several channels (one per command, as bots do) or a purged file.
const mode = computed(() => {
  const s = session.value
  if (!s) return '—'
  if (s.tty_available) return t('session.mode.shell')
  if (s.command_list.length) return t('session.mode.exec')
  return '—'
})

// Passwords and public keys tried, in time order. Cowrie logs offered keys
// (cowrie.client.fingerprint) without telling whether they were accepted.
const logins = computed(() => {
  const s = session.value
  if (!s) return []
  const rows = [
    ...s.login_attempt_list.map((l) => ({
      ts: l.ts, key: false, user: l.username, secret: l.password,
      result: l.success ? 'accepted' : 'refused',
    })),
    ...eventsOf('cowrie.client.fingerprint').map((e) => ({
      ts: e.ts, key: true, user: String(e.data?.username ?? ''),
      secret: [e.data?.type, e.data?.fingerprint].filter(Boolean).join(' '),
      result: 'offered',
    })),
  ]
  return rows.sort((a, b) => a.ts.localeCompare(b.ts))
})
</script>

<template>
  <div class="page">
    <RouterLink
      :to="{ name: 'sessions' }"
      class="back muted"
    >
      {{ t('session.back') }}
    </RouterLink>

    <ErrorNotice :error="error" />

    <template v-if="missing">
      <h1>{{ t('session.notFound') }}</h1>
      <p class="muted">
        {{ t('session.notFoundText', { id }) }}
      </p>
    </template>

    <template v-else-if="session">
      <div class="page-head">
        <h1>
          <span class="data">{{ session.src_ip ?? t('session.unknownSource') }}</span>
          <span
            v-if="session.login_success"
            class="shell verdict"
          >{{ t('session.gotShell') }}</span>
          <span
            v-else
            class="muted verdict"
          >{{ t('session.noShell') }}</span>
          <span
            v-if="verdict"
            class="badge"
            :class="'badge-' + verdict.verdict"
          >{{ t('session.verdict.' + verdict.verdict) }}</span>
        </h1>
        <span class="muted">{{ t('session.startedOn', { time: formatTime(session.started_at), sensor: session.sensor }) }}</span>
      </div>

      <div class="layout">
        <section
          v-if="replayable"
          class="replay-area"
          aria-labelledby="replay-title"
        >
          <h2 id="replay-title">
            {{ t('session.replay.title') }}
            <span
              class="badge"
              :title="simulated ? t('session.replay.simulatedHint') : t('session.replay.recordingHint')"
            >{{ simulated ? t('session.replay.simulated') : t('session.replay.recorded') }}</span>
          </h2>
          <ReplayPlayer
            ref="player"
            v-model:simulated="forceSimulated"
            :session-id="id"
            :locked="!hasRecording"
            @time="(s) => (playerTime = s)"
            @markers="(m) => (markers = m)"
          />
        </section>

        <aside class="side">
          <section
            class="card"
            aria-labelledby="session-title"
          >
            <h2 id="session-title">
              {{ t('session.side.session') }}
            </h2>
            <dl class="facts">
              <dt>{{ t('session.facts.source') }}</dt>
              <dd class="data">
                {{ endpoint(session.src_ip, session.src_port) }}
              </dd>
              <template v-if="session.geo_source">
                <dt>{{ t('session.facts.location') }}</dt>
                <dd v-if="session.geo_country">
                  <CountryFlag :code="session.geo_country">
                    {{ session.geo_city ? `, ${session.geo_city}` : '' }}
                  </CountryFlag>
                  <span class="muted source">{{ t('session.facts.locationSource', { source: session.geo_source }) }}</span>
                </dd>
                <dd
                  v-else
                  class="muted"
                >
                  {{ t('session.facts.locationUnknown') }}
                </dd>
              </template>
              <template v-if="session.asn">
                <dt>{{ t('session.facts.network') }}</dt>
                <dd>
                  <RouterLink :to="{ name: 'sessions', query: { asn: String(session.asn) } }">
                    AS{{ session.asn }}
                  </RouterLink>
                  {{ session.as_org ?? '' }}
                </dd>
              </template>
              <dt>{{ t('session.facts.sensor') }}</dt>
              <dd>{{ session.sensor }}</dd>
              <dt>{{ t('session.facts.user') }}</dt>
              <dd class="data">
                {{ user ?? '—' }}
              </dd>
              <dt>{{ t('session.facts.client') }}</dt>
              <dd class="data">
                {{ session.client_version ?? '—' }}
              </dd>
              <dt>{{ t('session.facts.hassh') }}</dt>
              <dd class="data">
                {{ session.hassh ?? '—' }}
              </dd>
              <dt>{{ t('session.facts.started') }}</dt>
              <dd>{{ formatTime(session.started_at) }}</dd>
              <dt>{{ t('session.facts.duration') }}</dt>
              <dd>{{ formatMillis(session.duration_ms) }}</dd>
              <dt>{{ t('session.facts.port') }}</dt>
              <dd>{{ session.dst_port ?? '—' }} <span class="muted">{{ session.protocol }}</span></dd>
              <dt>{{ t('session.facts.mode') }}</dt>
              <dd>{{ mode }}</dd>
              <dt>{{ t('session.facts.terminal') }}</dt>
              <dd class="data">
                {{ terminal ?? '—' }}
              </dd>
              <dt>{{ t('session.recording.title') }}</dt>
              <dd v-if="session.tty_available">
                {{ t('session.recording.stored', { size: formatBytes(session.tty_size), duration: formatMillis(session.tty_duration_ms) }) }}
                <span
                  v-if="chapters && !chapters.simulated"
                  class="muted"
                >{{ t('session.recording.records', { n: chapters.records }) }}</span>
              </dd>
              <dd
                v-else
                class="muted"
              >
                {{ t('session.recording.none') }}
              </dd>
              <template v-if="verdict">
                <dt>{{ t('session.verdict.label') }}</dt>
                <dd>
                  <span
                    class="badge"
                    :class="'badge-' + verdict.verdict"
                  >{{ t('session.verdict.' + verdict.verdict) }}</span>
                  <span class="muted score">{{ t('session.verdict.score', { score: signed(verdict.score) }) }}</span>
                  <ul
                    v-if="verdict.reasons.length"
                    class="reasons"
                  >
                    <li
                      v-for="r in verdict.reasons"
                      :key="r.code"
                    >
                      {{ t('session.verdict.reasons.' + r.code) }}
                      <span class="data muted">{{ signed(r.points) }}</span>
                    </li>
                  </ul>
                </dd>
              </template>
              <dt>{{ t('session.facts.cowrieSession') }}</dt>
              <dd class="data">
                {{ session.session }}
              </dd>
            </dl>
          </section>

          <section
            v-if="commandChapters.length"
            class="card"
            aria-labelledby="commands-title"
          >
            <h2 id="commands-title">
              {{ t('session.side.commands', { n: commandChapters.length }) }}
            </h2>
            <ChapterList
              :chapters="commandChapters"
              :current="currentChapter"
              :seekable="!!chapters"
              @seek="(i) => player?.seekChapter(i)"
            />
          </section>

          <section
            class="card"
            aria-labelledby="logins-title"
          >
            <h2 id="logins-title">
              {{ t('session.side.logins') }}
            </h2>
            <table
              v-if="logins.length"
              class="logins"
            >
              <thead>
                <tr>
                  <th>{{ t('session.logins.method') }}</th>
                  <th>{{ t('session.logins.user') }}</th>
                  <th>{{ t('session.logins.secret') }}</th>
                  <th>{{ t('session.logins.result') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="(l, i) in logins"
                  :key="i"
                >
                  <td>{{ l.key ? t('session.logins.key') : t('session.logins.password') }}</td>
                  <td class="data">
                    {{ l.user }}
                  </td>
                  <td
                    class="data secret"
                    :class="{ muted: !l.secret }"
                  >
                    {{ l.secret || t('session.logins.emptyPassword') }}
                  </td>
                  <td :class="{ shell: l.result === 'accepted', muted: l.result !== 'accepted' }">
                    {{ t('session.logins.' + l.result) }}
                  </td>
                </tr>
              </tbody>
            </table>
            <p
              v-else
              class="muted"
            >
              {{ t('session.logins.none') }}
            </p>
          </section>

          <section
            v-if="session.transfers.length"
            class="card"
            aria-labelledby="files-title"
          >
            <h2 id="files-title">
              {{ t('session.files.title') }}
            </h2>
            <ul class="files">
              <li
                v-for="x in session.transfers"
                :key="x.id"
              >
                <span>{{ x.direction === 'download' ? t('session.files.download') : t('session.files.upload') }}</span>
                <span class="data">{{ x.url ?? x.filename }}</span>
                <span
                  v-if="x.sha256"
                  class="data muted"
                >{{ t('session.files.sha256', { sha: x.sha256 }) }}</span>
              </li>
            </ul>
          </section>
        </aside>

        <section
          class="timeline-area"
          aria-labelledby="timeline-title"
        >
          <h2 id="timeline-title">
            {{ t('session.timeline.title') }}
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
                  :aria-label="t('session.timeline.commands')"
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
                    >{{ t('session.timeline.commandNotFound') }}</span>
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
  grid-template-columns: minmax(0, 1fr) minmax(20rem, 27rem);
  grid-template-areas:
    'replay side'
    'timeline side';
  gap: var(--space-4) var(--space-5);
  align-items: start;
}

.replay-area {
  grid-area: replay;
}

.side {
  grid-area: side;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.timeline-area {
  grid-area: timeline;
}

/* No replay: the timeline takes the replay's place. */
.layout:not(:has(.replay-area)) {
  grid-template-areas: 'timeline side';
}

h1 > .badge {
  margin-left: var(--space-2);
}

.source {
  display: block;
  font-size: 0.85em;
}

.score {
  margin-left: var(--space-2);
}

.reasons {
  margin: var(--space-1) 0 0;
  padding: 0;
  list-style: none;
  font-size: 0.93em;
}

.card {
  padding: var(--space-3);
  border: 1px solid var(--c-rule);
  border-radius: 6px;
  background: var(--c-surface);
}

.card h2 {
  margin-bottom: var(--space-2);
}

.logins {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.93em;
}

.logins th {
  color: var(--c-muted);
  font-weight: 500;
  text-align: left;
}

.logins th,
.logins td {
  padding: var(--space-1) var(--space-2) var(--space-1) 0;
  vertical-align: top;
}

.logins .secret {
  overflow-wrap: anywhere;
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

/* One column: the replay first, then the side cards, then the timeline. */
@media (max-width: 1100px) {
  .layout,
  .layout:not(:has(.replay-area)) {
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas:
      'replay'
      'side'
      'timeline';
  }
}
</style>
