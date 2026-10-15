<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

// A panel folded to its title, which opens beside the titles when the
// mouse is on it or the keyboard focus is in it. A click or a tap on the
// title opens it when folded and folds it when open, and a tap outside
// folds it: touch screens have no hover. Escape folds it too, until the
// pointer or the focus leaves and returns.
defineProps<{ title: string }>()

const root = ref<HTMLElement | null>(null)
const hovered = ref(false)
const focused = ref(false)
const dismissed = ref(false)
const open = computed(() => (hovered.value || focused.value) && !dismissed.value)

function enter(e: PointerEvent) {
  // A touch is not a hover: a finger lifted leaves nothing to end it.
  if (e.pointerType === 'mouse') hovered.value = true
}
function leave(e: PointerEvent) {
  if (e.pointerType !== 'mouse') return
  hovered.value = false
  dismissed.value = false
}
function blur(e: FocusEvent) {
  const next = e.relatedTarget as Node | null
  if (next && root.value?.contains(next)) return
  focused.value = false
  if (!hovered.value) dismissed.value = false
}
// Pressing the title focuses it, which opens the panel before the click
// arrives: what the click toggles is what the press found. A keyboard
// click has no press, and finds the panel as it is.
let wasOpen = false
function press() {
  wasOpen = open.value
}
function toggle(e: MouseEvent) {
  if (e.detail === 0 ? open.value : wasOpen) {
    dismissed.value = true
    focused.value = false
    ;(e.currentTarget as HTMLElement).blur()
  } else {
    dismissed.value = false
    focused.value = true
  }
}
function away(e: PointerEvent) {
  if (root.value && !root.value.contains(e.target as Node)) {
    focused.value = false
    if (!hovered.value) dismissed.value = false
  }
}
onMounted(() => document.addEventListener('pointerdown', away))
onBeforeUnmount(() => document.removeEventListener('pointerdown', away))
</script>

<template>
  <div class="dock">
    <section
      ref="root"
      class="panel"
      :class="{ open }"
      @pointerenter="enter"
      @pointerleave="leave"
      @focusin="focused = true"
      @focusout="blur"
      @keydown.esc="dismissed = true"
    >
      <button
        type="button"
        class="card float head"
        :aria-expanded="open"
        @pointerdown="press"
        @click="toggle"
      >
        <h2>{{ title }}</h2>
        <span
          class="chevron"
          aria-hidden="true"
        >◂</span>
      </button>
      <div class="flyout">
        <div class="card float content">
          <slot />
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.dock {
  position: relative;
  height: 2.6rem;
}

.panel {
  position: absolute;
  inset: 0;
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  height: 100%;
  padding: var(--space-2) var(--space-3);
  color: inherit;
  font: inherit;
  cursor: default;
}

.open .head {
  border-color: var(--p-primary-color);
}

.head:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}

.head > h2 {
  margin: 0;
  font-size: 0.95rem;
}

.chevron {
  color: var(--c-muted);
}

/* Beside the folded titles, not over them: the others stay within reach.
   The pointer crosses the gap on the flyout's own bridge. */
.flyout {
  position: absolute;
  top: 0;
  right: calc(100% + var(--space-2));
  z-index: 2;
  width: min(26rem, calc(100vw - 22rem));
  opacity: 0;
  visibility: hidden;
  transform: translateX(0.5rem);
  transition:
    opacity 0.15s ease,
    transform 0.15s ease,
    visibility 0.15s;
}

/* Only a window too short for ten rows scrolls. */
.content {
  max-height: calc(100dvh - 8rem);
  overflow-y: auto;
  box-shadow: 0 8px 24px rgb(0 0 0 / 18%);
}

.flyout::after {
  content: '';
  position: absolute;
  top: 0;
  left: 100%;
  width: var(--space-2);
  height: 100%;
}

.open .flyout {
  opacity: 1;
  visibility: visible;
  transform: none;
}

@media (prefers-reduced-motion: reduce) {
  .flyout {
    transition: none;
  }
}

/* No room beside the titles on a phone: each panel opens in place. */
@media (width <= 900px) {
  .dock {
    height: auto;
  }

  .panel {
    position: static;
  }

  .head {
    height: 2.6rem;
  }

  .flyout {
    position: static;
    display: none;
    width: auto;
    margin-top: var(--space-1);
    opacity: 1;
    visibility: visible;
    transform: none;
  }

  .content {
    max-height: none;
    box-shadow: none;
  }

  .open .flyout {
    display: block;
  }
}
</style>
