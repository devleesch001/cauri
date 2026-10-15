<script setup lang="ts" generic="T">
import type { RouteLocationRaw } from 'vue-router'

// A ranked list, each row with a bar proportional to the first. The label
// slot draws the item; to, when given, makes it a link.
const props = defineProps<{
  items: T[]
  value: (item: T) => number
  itemKey: (item: T) => string
  to?: (item: T) => RouteLocationRaw
}>()
const most = (): number => Math.max(1, ...props.items.map(props.value))
</script>

<template>
  <ol class="bars">
    <li
      v-for="item in items"
      :key="itemKey(item)"
    >
      <span class="label">
        <RouterLink
          v-if="to"
          :to="to(item)"
        >
          <slot
            name="label"
            :item="item"
          />
        </RouterLink>
        <slot
          v-else
          name="label"
          :item="item"
        />
      </span>
      <span class="count">
        <slot
          name="value"
          :item="item"
        >{{ value(item).toLocaleString() }}</slot>
      </span>
      <span
        class="bar"
        :style="{ width: `${(100 * value(item)) / most()}%` }"
        aria-hidden="true"
      />
    </li>
  </ol>
</template>

<style scoped>
.bars {
  margin: 0;
  padding: 0;
  list-style: none;
}

li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  column-gap: var(--space-3);
  padding: var(--space-1) 0;
}

.label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.label a {
  text-decoration: none;
}

.label a:hover {
  text-decoration: underline;
}

.count {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.bar {
  grid-column: 1 / -1;
  height: 3px;
  margin-top: 2px;
  border-radius: 2px;
  background: var(--p-primary-color);
  opacity: 0.5;
}
</style>
