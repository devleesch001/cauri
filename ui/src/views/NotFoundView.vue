<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="page">
    <h1>{{ t('notFound.title') }}</h1>
    <p>
      <RouterLink to="/sessions">
        {{ t('notFound.back') }}
      </RouterLink>
    </p>
  </div>
</template>
