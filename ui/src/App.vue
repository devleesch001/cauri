<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { useI18n } from 'vue-i18n'
import SensorSelect from './components/SensorSelect.vue'
import { useSensors } from './stores/sensors'

const { t } = useI18n()
const sensors = useSensors()
onMounted(sensors.load)
</script>

<template>
  <header class="bar">
    <RouterLink
      to="/sessions"
      class="brand"
    >
      cauri
    </RouterLink>
    <nav :aria-label="t('nav.sections')">
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
</template>

<style scoped>
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
