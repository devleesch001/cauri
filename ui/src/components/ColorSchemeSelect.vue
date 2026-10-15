<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import { colorScheme, type ColorScheme } from '@/colorScheme'

// One square button: its icon shows the current scheme, a click moves to
// the next one. The name of the scheme is its label and tooltip.
const { t } = useI18n()
const order: ColorScheme[] = ['light', 'dark', 'system']
const label = computed(() =>
  t('colorScheme.current', { scheme: t(`colorScheme.${colorScheme.value}`) }),
)
function next() {
  colorScheme.value = order[(order.indexOf(colorScheme.value) + 1) % order.length]!
}
</script>

<template>
  <Button
    severity="secondary"
    outlined
    :aria-label="label"
    :title="label"
    @click="next"
  >
    <template #icon>
      <svg
        class="icon"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <template v-if="colorScheme === 'light'">
          <circle
            cx="12"
            cy="12"
            r="4"
          />
          <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
        </template>
        <path
          v-else-if="colorScheme === 'dark'"
          d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z"
        />
        <template v-else>
          <rect
            x="3"
            y="4"
            width="18"
            height="12"
            rx="2"
          />
          <path d="M8 20h8M12 16v4" />
        </template>
      </svg>
    </template>
  </Button>
</template>

<style scoped>
.icon {
  width: 1.1rem;
  height: 1.1rem;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>
