<script setup lang="ts">
import { useId } from 'vue'

// A headline number in a small bubble. Its details appear beside it while
// the pointer is on it or the keyboard focus is in it (a tap on a touch
// screen focuses it), and fold again on Escape.
export interface Detail {
  label: string
  value: string
}
defineProps<{ label: string; value: string; note?: string; details?: Detail[] }>()
const tip = useId()
</script>

<template>
  <div
    class="bubble card float"
    :tabindex="details?.length ? 0 : undefined"
    :aria-describedby="details?.length ? tip : undefined"
    @keydown.esc="($event.currentTarget as HTMLElement).blur()"
  >
    <span class="label muted">{{ label }}</span>
    <span class="figure">
      <span class="value">{{ value }}</span>
      <span
        v-if="note"
        class="muted note"
      >{{ note }}</span>
    </span>
    <dl
      v-if="details?.length"
      :id="tip"
      role="tooltip"
      class="tip card float"
    >
      <template
        v-for="d in details"
        :key="d.label"
      >
        <dt class="muted">
          {{ d.label }}
        </dt>
        <dd>{{ d.value }}</dd>
      </template>
    </dl>
  </div>
</template>

<style scoped>
.bubble {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 11.5rem;
  padding: var(--space-2) var(--space-3);
  border-radius: 12px;
}

.bubble:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}

.label {
  font-size: 0.7rem;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.figure {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.value {
  font-size: 1.25rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
}

.note {
  font-size: 0.75rem;
}

/* Beside the bubble, over the map; the gap is bridged so that the pointer
   does not leave the bubble on its way. */
.tip {
  position: absolute;
  top: 0;
  left: calc(100% + var(--space-2));
  z-index: 3;
  display: grid;
  grid-template-columns: auto auto;
  gap: var(--space-1) var(--space-3);
  min-width: 15rem;
  margin: 0;
  background: var(--c-surface);
  padding: var(--space-2) var(--space-3);
  box-shadow: 0 8px 24px rgb(0 0 0 / 18%);
  opacity: 0;
  visibility: hidden;
  transform: translateX(-0.5rem);
  transition:
    opacity 0.15s ease,
    transform 0.15s ease,
    visibility 0.15s;
  pointer-events: none;
}

.bubble:hover .tip,
.bubble:focus-within .tip,
.bubble:focus .tip {
  opacity: 1;
  visibility: visible;
  transform: none;
}

dt {
  font-size: 0.8rem;
  white-space: nowrap;
}

dd {
  margin: 0;
  font-size: 0.85rem;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

@media (prefers-reduced-motion: reduce) {
  .tip {
    transition: none;
  }
}

/* No room beside a bubble on a phone: the details open below it. */
@media (width <= 900px) {
  .bubble {
    width: auto;
  }

  .tip {
    position: static;
    display: none;
    margin-top: var(--space-2);
    box-shadow: none;
    opacity: 1;
    visibility: visible;
    transform: none;
  }

  .bubble:hover .tip,
  .bubble:focus-within .tip,
  .bubble:focus .tip {
    display: grid;
  }
}
</style>
