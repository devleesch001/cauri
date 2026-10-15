<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import WindowMaximizeIcon from '@openvue/icons/windowmaximize'
import WindowMinimizeIcon from '@openvue/icons/windowminimize'
import { api } from './api/client'
import type { About } from './api/types'
import SensorSelect from './components/SensorSelect.vue'
import { useSensors } from './stores/sensors'

const { t } = useI18n()
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
      cauri
    </RouterLink>
    <nav :aria-label="t('nav.sections')">
      <RouterLink to="/">
        {{ t('nav.overview') }}
      </RouterLink>
      <RouterLink to="/activity">
        {{ t('nav.activity') }}
      </RouterLink>
      <RouterLink to="/credentials">
        {{ t('nav.credentials') }}
      </RouterLink>
      <RouterLink to="/commands">
        {{ t('nav.commands') }}
      </RouterLink>
      <RouterLink to="/payloads">
        {{ t('nav.payloads') }}
      </RouterLink>
      <RouterLink to="/sessions">
        {{ t('nav.sessions') }}
      </RouterLink>
      <RouterLink to="/login-attempts">
        {{ t('nav.failedLogins') }}
      </RouterLink>
    </nav>
    <SensorSelect class="sensor" />
  </header>
  <main>
    <RouterView />
  </main>
  <Button
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

nav a:hover {
  color: var(--c-ink);
}

nav a.router-link-active {
  color: var(--c-ink);
  box-shadow: inset 0 -2px 0 var(--p-primary-color);
  border-radius: 0;
}

.sensor {
  margin-left: auto;
}

@media (max-width: 720px) {
  .bar {
    padding: var(--space-2) var(--space-3);
  }

  .sensor {
    margin-left: 0;
    width: 100%;
  }
}
</style>
