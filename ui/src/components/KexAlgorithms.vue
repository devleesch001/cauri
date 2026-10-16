<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KexLists } from '@/api/types'
import { kexLists } from '@/kex'

// The algorithm lists of a key exchange, compact: each titled, its
// algorithms numbered in the order offered. For an overlay; the profile
// gives each list a card.
const props = defineProps<{ kex: KexLists }>()
const { t } = useI18n()
const lists = computed(() => kexLists(props.kex))
</script>

<template>
  <div class="kex-lists">
    <section
      v-for="l in lists"
      :key="l.name"
    >
      <h3>
        {{ t('attacker.lists.' + l.name) }}
        <span class="data muted cowrie">{{ l.cowrie }}<template v-if="l.hassh"> · {{ t('attacker.inHassh') }}</template></span>
      </h3>
      <ol
        v-if="l.algorithms.length"
        class="data"
      >
        <li
          v-for="(a, i) in l.algorithms"
          :key="i"
        >
          {{ a }}
        </li>
      </ol>
      <p
        v-else
        class="muted"
      >
        {{ t('attacker.none') }}
      </p>
    </section>
  </div>
</template>

<style scoped>
/* Three columns of two lists, as many as fit on narrow screens. Low
   enough to fit above or below the row it opens from, rather than cover
   it: long lists scroll. */
.kex-lists {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: var(--space-3) var(--space-4);
  max-width: min(60rem, 90vw);
  max-height: 40vh;
  overflow-y: auto;
  font-size: 0.8125rem;
}

h3 {
  margin: 0 0 var(--space-1);
  font-size: 0.875rem;
}

.cowrie {
  display: block;
  font-size: 0.6875rem;
  font-weight: normal;
}

/* Room for two-digit numbers, which the scrolling box would clip. */
ol {
  margin: 0;
  padding-left: 2.5em;
}

li {
  overflow-wrap: anywhere;
}

li::marker {
  color: var(--c-muted);
}

p {
  margin: 0;
}
</style>
