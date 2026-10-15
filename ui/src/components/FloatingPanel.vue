<script setup lang="ts">
import { useId } from 'vue'

// One section of an accordion: its title, which folds and unfolds the
// content beneath it. The parent keeps which one is open, so that opening
// one folds the others. Escape folds it. The content stays in the page
// while folded (a feed goes on updating), hidden from the keyboard.
defineProps<{ title: string; open: boolean }>()
const emit = defineEmits<{ toggle: [] }>()
const body = useId()
</script>

<template>
  <section
    class="card float panel"
    :class="{ open }"
    @keydown.esc="open && emit('toggle')"
  >
    <button
      type="button"
      class="head"
      :aria-expanded="open"
      :aria-controls="body"
      @click="emit('toggle')"
    >
      <h2>{{ title }}</h2>
      <span
        class="chevron"
        aria-hidden="true"
      >▸</span>
    </button>
    <div
      :id="body"
      class="body"
    >
      <div class="inner">
        <slot />
      </div>
    </div>
  </section>
</template>

<style scoped>
.panel {
  padding: 0;
  overflow: hidden;
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 2.6rem;
  padding: var(--space-2) var(--space-3);
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.head:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: -2px;
}

.head > h2 {
  margin: 0;
  font-size: 0.95rem;
}

.chevron {
  color: var(--c-muted);
  transition: transform 0.18s ease;
}

.open .chevron {
  transform: rotate(90deg);
}

/* From no height to that of the content, which a plain height cannot animate. */
.body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.18s ease;
}

.open .body {
  grid-template-rows: 1fr;
}

.inner {
  min-height: 0;
  overflow: hidden;
  padding: 0 var(--space-3);
  visibility: hidden;
  transition: visibility 0.18s;
}

.open .inner {
  padding-bottom: var(--space-3);
  visibility: visible;
}

@media (prefers-reduced-motion: reduce) {
  .chevron,
  .body,
  .inner {
    transition: none;
  }
}
</style>
