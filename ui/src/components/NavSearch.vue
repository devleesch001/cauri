<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, type RouteLocationRaw } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Dialog from 'openvue/dialog'
import SearchIcon from '@openvue/icons/search'
import { api } from '@/api/client'
import { primaryPages, statsPages, trailingPages } from '@/nav'

// A search dialog, opened by the button of the bar, Ctrl+K, Cmd+K or "/":
// it lists every page from the start, and narrows as one types. Typing an
// address, an account, a command… adds links to the sessions about it
// (the pages of the session list already filter by them), and a Cowrie
// session id is looked up to open the session itself.
const { t } = useI18n()
const router = useRouter()

interface Option {
  id: string
  label: string
  hint?: string
  to: RouteLocationRaw
}
interface Group {
  label: string
  items: Option[]
}

const visible = ref(false)
const query = ref('')
const found = ref<Option | null>(null) // the session a typed id names
const active = ref(0)
const input = ref<HTMLInputElement | null>(null)

/** Lower case without accents, to match "résumé" with "resume". */
const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase()
const mac = typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.userAgent)
const shortcut = mac ? '⌘K' : 'Ctrl K'

const pages: Option[] = [
  ...primaryPages.map((p) => ({ page: p, hint: '' })),
  ...statsPages.map((p) => ({ page: p, hint: 'statistics' })),
  ...trailingPages.map((p) => ({ page: p, hint: '' })),
].map(({ page, hint }) => ({
  id: 'page-' + page.name,
  label: page.label,
  hint,
  to: { name: page.name },
}))

const isIP = (s: string) => /^(\d{1,3}\.){3}\d{1,3}$/.test(s) || (s.includes(':') && /^[0-9a-f:.]+$/i.test(s))
const sessionsWith = (q: Record<string, string>): RouteLocationRaw => ({ name: 'sessions', query: q })

const groups = computed<Group[]>(() => {
  const q = query.value.trim()
  const label = (o: Option) => t(`nav.${o.label}`)
  const matching = pages
    .filter((o) => !q || fold(label(o)).includes(fold(q)))
    .map((o) => ({ ...o, label: label(o), hint: o.hint ? t(`nav.${o.hint}`) : undefined }))
  const out: Group[] = []
  if (matching.length) out.push({ label: t('search.pages'), items: matching })
  if (!q) return out

  const links: Option[] = []
  if (found.value) links.push(found.value)
  if (isIP(q)) links.push({ id: 'ip', label: t('search.ip', { value: q }), to: sessionsWith({ ip: q }) })
  if (/^[0-9a-f]{32}$/i.test(q)) links.push({ id: 'hassh', label: t('search.hassh', { value: q }), to: sessionsWith({ hassh: q.toLowerCase() }) })
  const asn = /^(?:as)?(\d{1,10})$/i.exec(q)
  if (asn?.[1]) links.push({ id: 'asn', label: t('search.asn', { value: asn[1] }), to: sessionsWith({ asn: asn[1] }) })
  links.push(
    { id: 'username', label: t('search.username', { value: q }), to: sessionsWith({ username: q }) },
    { id: 'password', label: t('search.password', { value: q }), to: sessionsWith({ password: q }) },
    { id: 'command', label: t('search.command', { value: q }), to: sessionsWith({ command: q }) },
    { id: 'client', label: t('search.client', { value: q }), to: sessionsWith({ client: q }) },
  )
  out.push({ label: t('search.sessions'), items: links })
  return out
})

const options = computed(() => groups.value.flatMap((g) => g.items))
const offsets = computed(() => {
  let n = 0
  return groups.value.map((g) => {
    const start = n
    n += g.items.length
    return start
  })
})

// The session a Cowrie id names, looked up once typing pauses; an answer
// to an older query is dropped.
let lookup: ReturnType<typeof setTimeout> | undefined
let lookups = 0
watch(query, (value) => {
  active.value = 0
  found.value = null
  clearTimeout(lookup)
  const q = value.trim().toLowerCase()
  if (!/^[0-9a-f]{12}$/.test(q)) return
  const n = ++lookups
  lookup = setTimeout(() => {
    void api
      .sessions({ session: q })
      .then((p) => {
        const hit = p.items[0]
        if (n !== lookups || !hit) return
        found.value = {
          id: 'session',
          label: t('search.session', { value: q, ip: hit.src_ip ?? '?' }),
          to: { name: 'session', params: { id: String(hit.id) } },
        }
      })
      .catch(() => undefined)
  }, 150)
})

function open() {
  query.value = ''
  active.value = 0
  visible.value = true
}

function go(option: Option | undefined) {
  if (!option) return
  visible.value = false
  void router.push(option.to)
}

function move(by: number) {
  const n = options.value.length
  if (!n) return
  active.value = (active.value + by + n) % n
  void nextTick(() => document.getElementById('search-option-' + active.value)?.scrollIntoView({ block: 'nearest' }))
}

function onKey(e: KeyboardEvent) {
  const el = e.target as HTMLElement | null
  const typing = !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    if (visible.value) visible.value = false
    else open()
  } else if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !visible.value) {
    e.preventDefault()
    open()
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  clearTimeout(lookup)
})
</script>

<template>
  <div>
    <Button
      class="search-trigger"
      severity="secondary"
      outlined
      :aria-label="t('search.label')"
      aria-haspopup="dialog"
      @click="open"
    >
      <SearchIcon aria-hidden="true" />
      <span class="search-label">{{ t('search.label') }}</span>
      <kbd aria-hidden="true">{{ shortcut }}</kbd>
    </Button>

    <Dialog
      v-model:visible="visible"
      modal
      dismissable-mask
      :show-header="false"
      position="top"
      :draggable="false"
      :aria-label="t('search.label')"
      class="search-dialog"
      @show="nextTick(() => input?.focus())"
    >
      <div class="search-box">
        <SearchIcon aria-hidden="true" />
        <input
          ref="input"
          v-model="query"
          type="text"
          role="combobox"
          autocomplete="off"
          spellcheck="false"
          aria-expanded="true"
          aria-controls="search-results"
          :aria-activedescendant="options.length ? 'search-option-' + active : undefined"
          :aria-label="t('search.label')"
          :placeholder="t('search.placeholder')"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="go(options[active])"
        >
        <kbd aria-hidden="true">Esc</kbd>
      </div>
      <div
        id="search-results"
        class="search-results"
        role="listbox"
      >
        <p
          v-if="!options.length"
          class="muted search-none"
        >
          {{ t('search.none') }}
        </p>
        <template
          v-for="(g, gi) in groups"
          :key="g.label"
        >
          <h3 class="search-group">
            {{ g.label }}
          </h3>
          <div
            v-for="(o, i) in g.items"
            :id="'search-option-' + ((offsets[gi] ?? 0) + i)"
            :key="o.id"
            role="option"
            class="search-option"
            :class="{ active: (offsets[gi] ?? 0) + i === active }"
            :aria-selected="(offsets[gi] ?? 0) + i === active"
            @mousemove="active = (offsets[gi] ?? 0) + i"
            @click="go(o)"
          >
            <span>{{ o.label }}</span>
            <span
              v-if="o.hint"
              class="muted"
            >{{ o.hint }}</span>
          </div>
        </template>
      </div>
    </Dialog>
  </div>
</template>

<style scoped>
.search-trigger {
  gap: var(--space-2);
  min-width: 11rem;
  justify-content: flex-start;
}

.search-label {
  flex: 1;
  text-align: left;
}

kbd {
  padding: 0 var(--space-1);
  border: 1px solid var(--c-rule);
  border-radius: 4px;
  color: var(--c-muted);
  font: inherit;
  font-size: 0.75rem;
}

.search-box {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3);
  border-bottom: 1px solid var(--c-rule);
}

.search-box input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: 0;
  background: none;
  color: var(--c-ink);
  font: inherit;
  font-size: 1.05rem;
}

.search-results {
  max-height: min(60vh, 28rem);
  overflow-y: auto;
  padding: var(--space-2);
}

.search-group {
  margin: var(--space-2) var(--space-2) var(--space-1);
  color: var(--c-muted);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.search-option {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-2);
  border-radius: 6px;
  cursor: pointer;
}

.search-option.active {
  background: color-mix(in srgb, var(--p-primary-color) 14%, transparent);
}

.search-none {
  padding: var(--space-3);
  text-align: center;
}

@media (max-width: 720px) {
  .search-trigger {
    width: 100%;
  }
}
</style>

<style>
/* The dialog is teleported to the body: not reached by scoped styles. */
.search-dialog {
  width: min(40rem, 92vw);
  margin-top: 10vh;
}

.search-dialog .p-dialog-content {
  padding: 0;
}
</style>
