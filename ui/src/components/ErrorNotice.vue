<script setup lang="ts">
import Message from 'openvue/message'
import { useI18n } from 'vue-i18n'

defineProps<{ error: string | null }>()
const { t } = useI18n()
</script>

<template>
  <Message
    v-if="error"
    severity="error"
    class="notice"
  >
    {{ t('apiError', { error }) }}
  </Message>
</template>

<style scoped>
.notice {
  margin-bottom: var(--space-3);
}
</style>
