<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Menu from 'openvue/menu'
import WindowMaximizeIcon from '@openvue/icons/windowmaximize'
import WindowMinimizeIcon from '@openvue/icons/windowminimize'
import { api } from './api/client'
import type { About } from './api/types'
import NavSearch from './components/NavSearch.vue'
import ColorSchemeSelect from './components/ColorSchemeSelect.vue'
import SensorSelect from './components/SensorSelect.vue'
import { primaryPages, statsPages, trailingPages } from './nav'
import { useSensors } from './stores/sensors'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

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

onMounted(() => {
  void sensors.load()
  void api.about().then((a) => (about.value = a)).catch(() => undefined)
})
</script>

<template>
  <header class="bar">
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
  </header>
  <main>
    <RouterView />
  </main>
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
    v-if="providers.size"
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
  </footer>
</template>

<style scoped>
.attribution {
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--c-rule);
  font-size: 0.85em;
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

@media (max-width: 720px) {
  .bar {
    padding: var(--space-2) var(--space-3);
  }

  .search,
  .sensor {
    margin-left: 0;
    width: 100%;
  }
}
</style>
