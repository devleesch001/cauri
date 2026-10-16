<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Dialog from 'openvue/dialog'
import InputText from 'openvue/inputtext'
import { admin, type Params } from '@/api/client'
import type { PurgeCounts } from '@/api/types'
import { useAdmin } from '@/admin'
import DayRange from '@/components/DayRange.vue'
import { dayRange } from '@/dates'

// Deleting sessions, as cauri purge does: criteria, a simulation of what
// would go, then the deletion, run as a task, once a word is typed.
const { t } = useI18n()
const { call, running, started } = useAdmin()

const texts = ['ip', 'session', 'username', 'password', 'command', 'hassh', 'file', 'sensor'] as const
const criteria = ref<Record<(typeof texts)[number], string>>({
  ip: '', session: '', username: '', password: '', command: '', hassh: '', file: '', sensor: '',
})
const from = ref('')
const to = ref('')
const filters = computed<Params>(() => {
  const out: Params = {}
  for (const k of texts) {
    const v = criteria.value[k].trim()
    if (v) out[k] = v
  }
  return { ...out, ...dayRange(from.value, to.value) }
})
const given = computed(() => Object.values(filters.value).some((v) => v))

// The simulation holds for the criteria it was run with only.
const simulated = ref<PurgeCounts | null>(null)
watch(filters, () => (simulated.value = null), { deep: true })
async function simulate() {
  const c = await call(() => admin.simulateSessions(filters.value))
  if (c) simulated.value = c
}
const counts = computed(() =>
  simulated.value ? (Object.entries(simulated.value) as [keyof PurgeCounts, number][]).filter(([, n]) => n > 0) : [],
)

const confirming = ref(false)
const typed = ref('')
const word = computed(() => t('admin.purge.word'))
function confirm() {
  typed.value = ''
  confirming.value = true
}
async function purge() {
  confirming.value = false
  const job = await call(() => admin.purgeSessions(filters.value))
  if (job) {
    started(job)
    simulated.value = null
  }
}
</script>

<template>
  <section class="card">
    <h2>{{ t('admin.purge.title') }}</h2>
    <p class="muted">
      {{ t('admin.purge.intro') }}
    </p>
    <form
      class="criteria"
      @submit.prevent="simulate"
    >
      <InputText
        v-for="k in texts"
        :key="k"
        v-model="criteria[k]"
        :class="{ data: k !== 'sensor' }"
        :placeholder="t('admin.purge.' + k)"
        :aria-label="t('admin.purge.' + k)"
      />
      <DayRange
        v-model:from="from"
        v-model:to="to"
      />
      <div class="buttons">
        <Button
          type="submit"
          :label="t('admin.purge.simulate')"
          severity="secondary"
          outlined
          :disabled="!given"
        />
        <Button
          :label="t('admin.purge.delete')"
          :disabled="!simulated?.sessions || !!running"
          @click="confirm"
        />
      </div>
    </form>
    <p
      v-if="!given"
      class="muted"
    >
      {{ t('admin.purge.noCriteria') }}
    </p>
    <div
      v-if="simulated"
      role="status"
    >
      <template v-if="simulated.sessions">
        <h3>{{ t('admin.purge.wouldDelete') }}</h3>
        <dl class="facts">
          <template
            v-for="[k, n] in counts"
            :key="k"
          >
            <dt>{{ t('admin.purge.counts.' + k) }}</dt>
            <dd>{{ n.toLocaleString() }}</dd>
          </template>
        </dl>
      </template>
      <p v-else>
        {{ t('admin.purge.nothing') }}
      </p>
    </div>

    <Dialog
      v-model:visible="confirming"
      modal
      :header="t('admin.purge.confirmTitle')"
      :style="{ width: 'min(32rem, 100vw - 2rem)' }"
    >
      <p v-if="simulated">
        {{ t('admin.purge.confirmText', { n: simulated.sessions.toLocaleString() }) }}
      </p>
      <label for="purge-word">{{ t('admin.purge.typeWord', { word }) }}</label>
      <InputText
        id="purge-word"
        v-model="typed"
        class="data"
        autocomplete="off"
        fluid
        @keyup.enter="typed === word && purge()"
      />
      <template #footer>
        <Button
          :label="t('admin.cancel')"
          severity="secondary"
          text
          @click="confirming = false"
        />
        <Button
          :label="t('admin.purge.delete')"
          :disabled="typed !== word"
          @click="purge"
        />
      </template>
    </Dialog>
  </section>
</template>

<style scoped>
.criteria {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.buttons {
  display: flex;
  gap: var(--space-2);
}

h3 {
  margin: var(--space-3) 0 var(--space-2);
  font-size: 1rem;
}
</style>
