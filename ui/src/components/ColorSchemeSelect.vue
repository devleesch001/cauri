<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from 'openvue/select'
import { colorScheme, type ColorScheme } from '@/colorScheme'

// Light, dark, or as the system says.
const { t } = useI18n()
const options = computed(() =>
  (['light', 'dark', 'system'] as ColorScheme[]).map((v) => ({ label: t(`colorScheme.${v}`), value: v })),
)
</script>

<template>
  <Select
    v-model="colorScheme"
    :options="options"
    option-label="label"
    option-value="value"
    :aria-label="t('colorScheme.label')"
  />
</template>
