import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { Sensor } from '@/api/types'

// The sensor filter itself lives in the URL (?sensor=), like every filter;
// this store only holds the list to choose from.
export const useSensors = defineStore('sensors', () => {
  const sensors = ref<Sensor[]>([])
  const error = ref<string | null>(null)

  async function load() {
    try {
      sensors.value = await api.sensors()
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  return { sensors, error, load }
})
