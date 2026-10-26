<script setup lang="ts">
import { computed, onMounted, ref, watch, watchEffect } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMediaQuery } from '@vueuse/core'
import Button from 'openvue/button'
import Drawer from 'openvue/drawer'
import Menu from 'openvue/menu'
import Popover from 'openvue/popover'
import Toast, { type ToastEvent } from 'openvue/toast'
import { useToast } from 'openvue/usetoast'
import BarsIcon from '@openvue/icons/bars'
import WindowMaximizeIcon from '@openvue/icons/windowmaximize'
import WindowMinimizeIcon from '@openvue/icons/windowminimize'
import { api, onApiError } from './api/client'
import type { About } from './api/types'
import NavSearch from './components/NavSearch.vue'
import { formatTime } from './format'
import ColorSchemeSelect from './components/ColorSchemeSelect.vue'
import SensorSelect from './components/SensorSelect.vue'
import { primaryPages, statsPages, trailingPages } from './nav'
import { useSensors } from './stores/sensors'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// Every failed call to the API is notified, once while shown: a page
// polling a failing API keeps one notification, not one per poll.
const toast = useToast()
const notified = new Set<string>()
onApiError((e) => {
  if (notified.has(e.message)) return
  notified.add(e.message)
  toast.add({ id: e.message, severity: 'error', summary: t('apiErrors.title'), detail: e.message })
})
const forget = (e: ToastEvent) => notified.delete(String(e.message.id))
const reloadPage = () => window.location.reload()

// Statistics pages share one menu, which stands for the page shown. It
// opens on hover or focus (CSS) and on click, for touch screens.
const statsItems = computed(() =>
  statsPages.map((p) => ({
    label: t(`nav.${p.label}`),
    command: () => {
      statsOpen.value = false
      void router.push({ name: p.name })
    },
  })),
)
// Where cauri's source code is, as its Go module names it.
const repository = 'https://github.com/devleesch001/cauri'
// GitHub's mark (Octicons mark-github, MIT), not in @openvue/icons.
const githubMark =
  'M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z'
const statsActive = computed(() => statsPages.some((p) => route.name === p.name))
// A page below a section, such as a session or an attacker's profile,
// keeps the section marked.
const within = (path: string) => path !== '/' && route.path.startsWith(path + '/')
const statsOpen = ref(false)
const sensors = useSensors()
// The GeoIP database, when there is one, requires an attribution.
const about = ref<About | null>(null)
// Each provider whose data is shown, by the loaded databases or the stored
// locations, has its attribution shown once.
const providers = computed(() => new Set<string>(about.value?.providers ?? []))
// The version of the API, in the footer; how it was built in a popover.
const versionPanel = ref<InstanceType<typeof Popover> | null>(null)

// Pages are capped in width (style.css); this lifts the cap, remembered
// per browser. Storage can be unavailable: then it lasts for the visit.
const widthKey = 'cauri.fullWidth'
const fullWidth = ref(false)
try {
  fullWidth.value = localStorage.getItem(widthKey) === '1'
} catch {
  // private mode or blocked storage
}
watchEffect(() => {
  document.documentElement.dataset.width = fullWidth.value ? 'full' : ''
  try {
    localStorage.setItem(widthKey, fullWidth.value ? '1' : '0')
  } catch {
    // see above
  }
})

// On a phone the bar is one line, the brand and a menu button: the pages,
// the search and the settings are in a drawer, closed again by a choice.
const phone = useMediaQuery('(max-width: 720px)')
const menuOpen = ref(false)
watch(
  () => route.fullPath,
  () => (menuOpen.value = false),
)
// The section shown, beside the brand: the bar alone tells where one is.
const sectionLabel = computed(() => {
  const all = [...primaryPages, ...statsPages, ...trailingPages]
  const page = all.find((p) => route.name === p.name) ?? all.find((p) => within(p.path))
  return page ? t(`nav.${page.label}`) : ''
})

onMounted(() => {
  void sensors.load()
  void api.about().then((a) => (about.value = a)).catch(() => undefined)
})
</script>

<template>
  <header
    v-if="phone"
    class="bar bar-phone"
  >
    <RouterLink
      to="/"
      class="brand"
    >
      <img
        src="/cauri.svg"
        alt=""
        width="28"
        height="28"
      >
      cauri
    </RouterLink>
    <span
      v-if="sectionLabel"
      class="section muted"
    >{{ sectionLabel }}</span>
    <Button
      class="menu-button"
      severity="secondary"
      text
      :aria-label="t('nav.menu')"
      :aria-expanded="menuOpen"
      aria-controls="nav-drawer"
      @click="menuOpen = true"
    >
      <template #icon="{ class: c }">
        <BarsIcon :class="c" />
      </template>
    </Button>
    <Drawer
      id="nav-drawer"
      v-model:visible="menuOpen"
      position="right"
      class="nav-drawer"
      :header="t('nav.menu')"
    >
      <div class="drawer-body">
        <NavSearch class="drawer-search" />
        <nav
          class="drawer-nav"
          :aria-label="t('nav.sections')"
        >
          <RouterLink
            v-for="p in primaryPages"
            :key="p.name"
            :to="p.path"
            :class="{ under: within(p.path) }"
          >
            {{ t(`nav.${p.label}`) }}
          </RouterLink>
          <span
            class="drawer-heading muted"
            role="presentation"
          >{{ t('nav.statistics') }}</span>
          <RouterLink
            v-for="p in statsPages"
            :key="p.name"
            :to="p.path"
            class="indented"
          >
            {{ t(`nav.${p.label}`) }}
          </RouterLink>
          <RouterLink
            v-for="p in trailingPages"
            :key="p.name"
            :to="p.path"
            :class="{ under: within(p.path) }"
          >
            {{ t(`nav.${p.label}`) }}
          </RouterLink>
        </nav>
        <SensorSelect class="drawer-sensor" />
        <div class="drawer-settings">
          <ColorSchemeSelect class="scheme" />
          <a
            :href="repository"
            class="repository"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="t('nav.repository')"
            :title="t('nav.repository')"
          >
            <svg
              viewBox="0 0 16 16"
              width="20"
              height="20"
              fill="currentColor"
              aria-hidden="true"
            >
              <path :d="githubMark" />
            </svg>
          </a>
        </div>
      </div>
    </Drawer>
  </header>
  <header
    v-else
    class="bar"
  >
    <RouterLink
      to="/"
      class="brand"
    >
      <img
        src="/cauri.svg"
        alt=""
        width="28"
        height="28"
      >
      cauri
    </RouterLink>
    <nav :aria-label="t('nav.sections')">
      <RouterLink
        v-for="p in primaryPages"
        :key="p.name"
        :to="p.path"
        :class="{ under: within(p.path) }"
      >
        {{ t(`nav.${p.label}`) }}
      </RouterLink>
      <div
        class="nav-group"
        :class="{ open: statsOpen }"
        @keydown.esc="statsOpen = false"
      >
        <button
          type="button"
          class="menu-trigger"
          :class="{ active: statsActive }"
          aria-haspopup="menu"
          :aria-expanded="statsOpen"
          @click="statsOpen = !statsOpen"
        >
          {{ t('nav.statistics') }}
          <span aria-hidden="true">▾</span>
        </button>
        <div class="nav-dropdown">
          <Menu :model="statsItems" />
        </div>
      </div>
      <RouterLink
        v-for="p in trailingPages"
        :key="p.name"
        :to="p.path"
      >
        {{ t(`nav.${p.label}`) }}
      </RouterLink>
    </nav>
    <NavSearch class="search" />
    <SensorSelect class="sensor" />
    <ColorSchemeSelect class="scheme" />
    <!-- The source code: GitHub's mark (Octicons mark-github, MIT), not in
         @openvue/icons, drawn in the text colour so that themes apply. -->
    <a
      :href="repository"
      class="repository"
      target="_blank"
      rel="noopener noreferrer"
      :aria-label="t('nav.repository')"
      :title="t('nav.repository')"
    >
      <svg
        viewBox="0 0 16 16"
        width="20"
        height="20"
        fill="currentColor"
        aria-hidden="true"
      >
        <path :d="githubMark" />
      </svg>
    </a>
  </header>
  <main>
    <RouterView />
  </main>
  <Toast
    @close="forget"
    @life-end="forget"
  >
    <template #message="{ message }">
      <div class="p-toast-message-text">
        <span class="p-toast-summary">{{ message.summary }}</span>
        <div class="p-toast-detail">
          {{ message.detail }}
        </div>
        <Button
          :label="t('apiErrors.reload')"
          severity="danger"
          size="small"
          class="toast-reload"
          @click="reloadPage"
        />
      </div>
    </template>
  </Toast>
  <Button
    v-if="route.name !== 'overview'"
    class="width-toggle"
    severity="secondary"
    text
    rounded
    size="small"
    :aria-label="fullWidth ? t('layout.limitWidth') : t('layout.fullWidth')"
    :title="fullWidth ? t('layout.limitWidth') : t('layout.fullWidth')"
    :aria-pressed="fullWidth"
    @click="fullWidth = !fullWidth"
  >
    <template #icon="{ class: c }">
      <WindowMinimizeIcon
        v-if="fullWidth"
        :class="c"
      />
      <WindowMaximizeIcon
        v-else
        :class="c"
      />
    </template>
  </Button>
  <footer
    v-if="about"
    class="attribution muted"
  >
    <a
      v-if="providers.has('db-ip')"
      href="https://db-ip.com"
    >{{ t('attribution.dbip') }}</a>
    <span v-if="providers.has('maxmind')">
      {{ t('attribution.maxmind') }}
      <a href="https://www.maxmind.com">https://www.maxmind.com</a>
    </span>
    <Button
      class="version"
      :label="t('version.label', { tag: about.version.tag })"
      :aria-label="t('version.show')"
      severity="secondary"
      text
      size="small"
      @click="versionPanel?.toggle($event)"
    />
    <Popover ref="versionPanel">
      <dl class="facts">
        <dt>{{ t('version.tag') }}</dt>
        <dd class="data">
          {{ about.version.tag }}
        </dd>
        <dt>{{ t('version.commit') }}</dt>
        <dd>
          <span class="data">{{ about.version.commit || t('version.unknown') }}</span>
          <span
            v-if="about.version.dirty"
            class="muted"
          > ({{ t('version.modified') }})</span>
        </dd>
        <dt>{{ t('version.built') }}</dt>
        <dd>{{ about.version.date ? formatTime(about.version.date) : t('version.unknown') }}</dd>
      </dl>
    </Popover>
  </footer>
</template>

<style scoped>
.toast-reload {
  margin-top: var(--space-2);
}

.attribution {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--c-rule);
  font-size: 0.85em;
}

.version {
  margin-left: auto;
}

.width-toggle {
  position: fixed;
  bottom: var(--space-2);
  left: var(--space-2);
  z-index: 2;
}

/* Useful only where the page cap (1600 px, style.css) leaves margins. */
@media (width <= 1440px) {
  .width-toggle {
    display: none;
  }
}

@media (width > 1600px) {
  .attribution {
    padding-left: calc(var(--space-4) + 2.5rem);
  }
}

.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  padding: var(--space-2) var(--space-4);
  background: var(--c-surface);
  border-bottom: 1px solid var(--c-rule);
}

.repository {
  display: inline-flex;
  color: var(--c-muted);
}

.repository:hover {
  color: var(--c-ink);
}

.repository:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
  border-radius: 4px;
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-weight: 600;
  font-size: 1.15rem;
  text-decoration: none;
}

nav {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

nav a {
  padding: var(--space-1) var(--space-2);
  border-radius: 4px;
  color: var(--c-muted);
  text-decoration: none;
}

.nav-group {
  position: relative;
}

/* Directly under the button, with no gap, so that the pointer can reach it. */
.nav-dropdown {
  display: none;
  position: absolute;
  top: 100%;
  left: 0;
  z-index: 3;
  min-width: 10rem;
}

.nav-group:hover .nav-dropdown,
.nav-group:focus-within .nav-dropdown,
.nav-group.open .nav-dropdown {
  display: block;
}

.menu-trigger {
  padding: var(--space-1) var(--space-2);
  border: 0;
  background: none;
  color: var(--c-muted);
  font: inherit;
  cursor: pointer;
}

nav a:hover,
.menu-trigger:hover {
  color: var(--c-ink);
}

.menu-trigger:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}

.menu-trigger.active,
nav a.router-link-active,
nav a.under {
  color: var(--c-ink);
  box-shadow: inset 0 -2px 0 var(--p-primary-color);
  border-radius: 0;
}

.search {
  margin-left: auto;
  width: 16rem;
}

/* As tall as the selects beside it, and as wide: square. */
.scheme {
  align-self: stretch;
  width: 2.6667rem;
  min-height: 2.6667rem;
}

/* The phone's bar: one line, the brand, the section, the menu button. */
.bar-phone {
  flex-wrap: nowrap;
  gap: var(--space-3);
  padding: var(--space-1) var(--space-2) var(--space-1) var(--space-3);
}

.section {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu-button {
  margin-left: auto;
  flex: none;
}

.drawer-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.drawer-search,
.drawer-sensor {
  width: 100%;
}

.drawer-nav {
  flex-direction: column;
  flex-wrap: nowrap;
  gap: 0;
}

/* Rows a finger can hit. */
.drawer-nav a {
  display: block;
  padding: var(--space-2) var(--space-3);
  border-radius: 4px;
}

.drawer-nav a.router-link-active,
.drawer-nav a.under {
  box-shadow: inset 2px 0 0 var(--p-primary-color);
}

.drawer-nav a:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: -2px;
}

.drawer-heading {
  padding: var(--space-3) var(--space-3) var(--space-1);
  font-size: 0.85em;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.drawer-nav a.indented {
  padding-left: var(--space-4);
}

.drawer-settings {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}
</style>
