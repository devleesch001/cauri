<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import Select from 'openvue/select'
import { patchQuery, queryValue } from '@/router'
import { useSensors } from '@/stores/sensors'

// Filters every view by sensor, through the ?sensor= query parameter.
const sensors = useSensors()
const route = useRoute()

const options = computed(() => sensors.sensors.map((s) => ({ label: s.id, value: s.id })))
const selected = computed({
  get: () => queryValue(route.query, 'sensor') || null,
  set: (v: string | null) => void patchQuery({ sensor: v ?? undefined }),
})
</script>

<template>
  <Select
    v-model="selected"
    :options="options"
    option-label="label"
    option-value="value"
    placeholder="All sensors"
    show-clear
    aria-label="Sensor"
    :disabled="route.name === 'session'"
  />
</template>
