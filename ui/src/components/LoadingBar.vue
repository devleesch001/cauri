<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import ProgressBar from 'openvue/progressbar'

// A thin bar while the numbers of a page are computed again, after a change
// of its filters: what is shown meanwhile is the former answer.
defineProps<{ active: boolean }>()
const { t } = useI18n()
</script>

<template>
  <div class="loading-bar">
    <ProgressBar
      v-if="active"
      mode="indeterminate"
      :aria-label="t('stats.loading')"
    />
  </div>
</template>

<style scoped>
/* Its room is kept while hidden: the page does not move when it shows. */
.loading-bar {
  height: 3px;
}

.loading-bar :deep(.p-progressbar) {
  height: 3px;
  border-radius: 0;
}
</style>
