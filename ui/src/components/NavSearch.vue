<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter, type RouteLocationRaw } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AutoComplete, { type AutoCompleteOptionSelectEvent } from 'openvue/autocomplete'
import { api } from '@/api/client'
import { primaryPages, statsPages, trailingPages } from '@/nav'

// Reaches any page, or the sessions about an address, an account, a
// command…: it only builds links to the pages that already filter by them
// (see SessionsView), except for a Cowrie session id, looked up to open
// the session itself. Ctrl+K, Cmd+K or "/" focuses it from anywhere.
const { t } = useI18n()
const router = useRouter()

interface Option {
  label: string
  to: RouteLocationRaw
}
interface Group {
  label: string
  items: Option[]
}

const text = ref('')
const groups = ref<Group[]>([])
const inputId = 'nav-search-input'

/** Lower case without accents, to match "résumé" with "resume". */
const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase()

const pages = [...primaryPages, ...statsPages, ...trailingPages]

const isIP = (s: string) => /^(\d{1,3}\.){3}\d{1,3}$/.test(s) || (s.includes(':') && /^[0-9a-f:.]+$/i.test(s))
const sessionsWith = (query: Record<string, string>): RouteLocationRaw => ({ name: 'sessions', query })

async function complete(event: { query: string }) {
  const q = event.query.trim()
  const out: Group[] = []
  if (!q) {
    groups.value = out
    return
  }

  const found = pages.filter((p) => fold(t(`nav.${p.label}`)).includes(fold(q)))
  if (found.length) {
    out.push({
      label: t('search.pages'),
      items: found.map((p) => ({ label: t(`nav.${p.label}`), to: { name: p.name } })),
    })
  }

  const lookups: Option[] = []
  if (isIP(q)) lookups.push({ label: t('search.ip', { value: q }), to: sessionsWith({ ip: q }) })
  if (/^[0-9a-f]{32}$/i.test(q)) lookups.push({ label: t('search.hassh', { value: q }), to: sessionsWith({ hassh: q.toLowerCase() }) })
  const asn = /^(?:as)?(\d{1,10})$/i.exec(q)
  if (asn?.[1]) lookups.push({ label: t('search.asn', { value: asn[1] }), to: sessionsWith({ asn: asn[1] }) })
  if (/^[0-9a-f]{12}$/i.test(q)) {
    // The session list filters by it; one row opens the session itself.
    const hit = await api.sessions({ session: q.toLowerCase() }).then((p) => p.items[0]).catch(() => undefined)
    if (hit) {
      lookups.push({
        label: t('search.session', { value: q, ip: hit.src_ip ?? '?' }),
        to: { name: 'session', params: { id: String(hit.id) } },
      })
    }
  }
  lookups.push(
    { label: t('search.username', { value: q }), to: sessionsWith({ username: q }) },
    { label: t('search.password', { value: q }), to: sessionsWith({ password: q }) },
    { label: t('search.command', { value: q }), to: sessionsWith({ command: q }) },
    { label: t('search.client', { value: q }), to: sessionsWith({ client: q }) },
  )
  out.push({ label: t('search.sessions'), items: lookups })
  groups.value = out
}

function select(event: AutoCompleteOptionSelectEvent) {
  const option = event.value as Option
  text.value = ''
  groups.value = []
  void router.push(option.to)
}

function focus() {
  document.getElementById(inputId)?.focus()
}

function onKey(e: KeyboardEvent) {
  const el = e.target as HTMLElement | null
  const typing = !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
  if (((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') || (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey)) {
    e.preventDefault()
    focus()
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <AutoComplete
    v-model="text"
    :input-id="inputId"
    :suggestions="groups"
    option-label="label"
    option-group-label="label"
    option-group-children="items"
    :placeholder="t('search.placeholder')"
    :aria-label="t('search.label')"
    :delay="150"
    :min-length="1"
    class="nav-search"
    @complete="complete"
    @option-select="select"
  >
    <template #empty>
      {{ t('search.none') }}
    </template>
  </AutoComplete>
</template>
