import { computed, inject, onUnmounted, ref, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import { admin } from '@/api/client'
import type { AdminJob } from '@/api/types'

// What the sections of the administration page share, provided by the
// page: its calls, which sign out on a 401, and the background tasks,
// followed every second while one runs.
export interface AdminContext {
  /** Runs an administration call; undefined if it failed (the page shows why). */
  call: <T>(f: () => Promise<T>) => Promise<T | undefined>
  /** The tasks remembered by the API, latest first. */
  tasks: Ref<AdminJob[]>
  /** The task running, if any: only one runs at a time. */
  running: ComputedRef<AdminJob | undefined>
  /** Counts the tasks that ended: what they changed is to be read again. */
  ended: Ref<number>
  /** A task just started: follow it. */
  started: (job: AdminJob) => void
  /** Reads the tasks again. */
  refresh: () => Promise<void>
}

export const adminKey: InjectionKey<AdminContext> = Symbol('admin')

/** The context of the administration page's sections. */
export function useAdmin(): AdminContext {
  const c = inject(adminKey)
  if (!c) throw new Error('useAdmin outside the administration page')
  return c
}

// Seconds between two reads of a running task.
const pollEvery = 1000

/** Creates the context, for the page to provide. */
export function createAdmin(call: AdminContext['call']): AdminContext {
  const tasks = ref<AdminJob[]>([])
  const ended = ref(0)
  const running = computed(() => tasks.value.find((j) => j.status === 'running'))
  let timer: ReturnType<typeof setTimeout> | undefined

  async function refresh() {
    const before = running.value?.id
    const list = await call(admin.jobs)
    if (!list) return
    tasks.value = list
    if (before !== undefined && list.find((j) => j.id === before)?.status !== 'running') ended.value++
    schedule()
  }
  function schedule() {
    clearTimeout(timer)
    if (running.value) timer = setTimeout(() => void refresh(), pollEvery)
  }
  function started(job: AdminJob) {
    tasks.value = [job, ...tasks.value.filter((j) => j.id !== job.id)]
    schedule()
  }
  onUnmounted(() => clearTimeout(timer))
  return { call, tasks, running, ended, started, refresh }
}
