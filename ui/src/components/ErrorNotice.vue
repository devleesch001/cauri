<script setup lang="ts">
import Message from 'openvue/message'

defineProps<{ error: string | null }>()
</script>

<template>
  <Message
    v-if="error"
    severity="error"
    class="notice"
  >
    The API did not answer: {{ error }}. Check that "cauri api" is running and reachable under /api.
  </Message>
</template>

<style scoped>
.notice {
  margin-bottom: var(--space-3);
}
</style>
