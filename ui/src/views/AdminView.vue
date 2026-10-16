<script setup lang="ts">
import { onMounted, provide, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'openvue/button'
import Column from 'openvue/column'
import DataTable from 'openvue/datatable'
import Dialog from 'openvue/dialog'
import InputText from 'openvue/inputtext'
import Password from 'openvue/password'
import { ApiError, admin } from '@/api/client'
import type { AdminToken } from '@/api/types'
import { adminKey, createAdmin } from '@/admin'
import AdminConfig from '@/components/AdminConfig.vue'
import AdminDatabase from '@/components/AdminDatabase.vue'
import AdminPurge from '@/components/AdminPurge.vue'
import AdminTasks from '@/components/AdminTasks.vue'
import ErrorNotice from '@/components/ErrorNotice.vue'
import { formatTime } from '@/format'

// Administration: one password (CAURI_ADMIN_PASSWORD, or generated and
// logged by the API) opens a session that manages the ingestor tokens and
// the database, and shows the configuration.
const { t } = useI18n()

const signedIn = ref<boolean | null>(null) // null while checking
const error = ref<string | null>(null)

const password = ref('')
const wrongPassword = ref(false)
const signingIn = ref(false)

const tokens = ref<AdminToken[]>([])
const loading = ref(false)

const sensor = ref('')
const name = ref('')
const creating = ref(false)
// The secret of the token just created: shown once, then gone.
const created = ref<{ token: AdminToken; secret: string } | null>(null)
const copied = ref(false)

const revoking = ref<AdminToken | null>(null)

/** Runs an administration call; a 401 means the session ended. */
async function call<T>(f: () => Promise<T>): Promise<T | undefined> {
  try {
    const out = await f()
    error.value = null
    return out
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      signedIn.value = false
      return undefined
    }
    error.value = e instanceof Error ? e.message : String(e)
    return undefined
  }
}

// The sections share the calls and the background tasks.
provide(adminKey, createAdmin(call))

async function load() {
  loading.value = true
  const list = await call(admin.tokens)
  if (list) tokens.value = list
  loading.value = false
}

onMounted(async () => {
  try {
    await admin.session()
    signedIn.value = true
    await load()
  } catch {
    signedIn.value = false
  }
})

async function signIn() {
  signingIn.value = true
  wrongPassword.value = false
  try {
    await admin.login(password.value)
    password.value = ''
    signedIn.value = true
    error.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) wrongPassword.value = true
    else error.value = e instanceof Error ? e.message : String(e)
  } finally {
    signingIn.value = false
  }
}

async function signOut() {
  await call(admin.logout)
  signedIn.value = false
  tokens.value = []
}

async function create() {
  creating.value = true
  const out = await call(() => admin.createToken(sensor.value.trim(), name.value.trim()))
  creating.value = false
  if (!out) return
  created.value = out
  copied.value = false
  sensor.value = ''
  name.value = ''
  await load()
}

async function copy() {
  if (!created.value) return
  try {
    await navigator.clipboard.writeText(created.value.secret)
    copied.value = true
  } catch {
    // no clipboard (insecure context): the secret stays selectable
  }
}

async function revoke() {
  const tok = revoking.value
  revoking.value = null
  if (!tok) return
  await call(() => admin.revokeToken(tok.id))
  await load()
}
</script>

<template>
  <div class="page">
    <div class="page-head">
      <h1>{{ t('admin.title') }}</h1>
      <Button
        v-if="signedIn"
        :label="t('admin.signOut')"
        severity="secondary"
        size="small"
        text
        @click="signOut"
      />
    </div>

    <ErrorNotice :error="error" />

    <form
      v-if="signedIn === false"
      class="card sign-in"
      @submit.prevent="signIn"
    >
      <label for="admin-password">{{ t('admin.password') }}</label>
      <Password
        v-model="password"
        input-id="admin-password"
        :feedback="false"
        toggle-mask
        autocomplete="current-password"
        :invalid="wrongPassword"
        fluid
      />
      <p
        v-if="wrongPassword"
        class="muted"
        role="alert"
      >
        {{ t('admin.wrongPassword') }}
      </p>
      <Button
        type="submit"
        :label="t('admin.signIn')"
        :loading="signingIn"
        :disabled="!password"
      />
    </form>

    <div
      v-else-if="signedIn"
      class="sections"
    >
      <section class="card">
        <h2>{{ t('admin.tokens.title') }}</h2>
        <p class="muted">
          {{ t('admin.tokens.intro') }}
        </p>
        <form
          class="create"
          @submit.prevent="create"
        >
          <InputText
            v-model="sensor"
            :placeholder="t('admin.tokens.sensor')"
            :aria-label="t('admin.tokens.sensor')"
            maxlength="64"
          />
          <InputText
            v-model="name"
            :placeholder="t('admin.tokens.name')"
            :aria-label="t('admin.tokens.name')"
            maxlength="200"
          />
          <Button
            type="submit"
            :label="t('admin.tokens.create')"
            :loading="creating"
            :disabled="!sensor.trim()"
          />
        </form>

        <DataTable
          :value="tokens"
          :loading="loading"
          data-key="id"
          size="small"
          striped-rows
          scrollable
        >
          <template #empty>
            <p class="muted">
              {{ t('admin.tokens.none') }}
            </p>
          </template>
          <Column
            field="id"
            :header="t('admin.tokens.columns.id')"
          />
          <Column :header="t('admin.tokens.columns.sensor')">
            <template #body="{ data }">
              <span class="data">{{ data.sensor }}</span>
            </template>
          </Column>
          <Column
            field="name"
            :header="t('admin.tokens.columns.name')"
          />
          <Column :header="t('admin.tokens.columns.created')">
            <template #body="{ data }">
              {{ formatTime(data.created_at) }}
            </template>
          </Column>
          <Column :header="t('admin.tokens.columns.lastUsed')">
            <template #body="{ data }">
              {{ data.last_used_at ? formatTime(data.last_used_at) : t('admin.tokens.never') }}
            </template>
          </Column>
          <Column :header="t('admin.tokens.columns.state')">
            <template #body="{ data }">
              <span
                v-if="data.revoked_at"
                class="muted"
              >{{ t('admin.tokens.revokedOn', { time: formatTime(data.revoked_at) }) }}</span>
              <Button
                v-else
                :label="t('admin.tokens.revoke')"
                severity="secondary"
                size="small"
                outlined
                @click="revoking = data"
              />
            </template>
          </Column>
        </DataTable>
      </section>
      <AdminTasks />
      <AdminDatabase />
      <AdminPurge />
      <AdminConfig />
    </div>

    <Dialog
      :visible="!!created"
      modal
      :header="t('admin.created.title')"
      :style="{ width: 'min(40rem, 100vw - 2rem)' }"
      @update:visible="(v: boolean) => v || (created = null)"
    >
      <template v-if="created">
        <p>{{ t('admin.created.intro', { sensor: created.token.sensor }) }}</p>
        <code class="data secret">{{ created.secret }}</code>
        <p class="muted">
          {{ t('admin.created.once') }}
        </p>
      </template>
      <template #footer>
        <Button
          :label="copied ? t('admin.created.copied') : t('admin.created.copy')"
          severity="secondary"
          outlined
          @click="copy"
        />
        <Button
          :label="t('admin.created.done')"
          @click="created = null"
        />
      </template>
    </Dialog>

    <Dialog
      :visible="!!revoking"
      modal
      :header="t('admin.revoke.title')"
      :style="{ width: 'min(32rem, 100vw - 2rem)' }"
      @update:visible="(v: boolean) => v || (revoking = null)"
    >
      <p v-if="revoking">
        {{ t('admin.revoke.text', { id: revoking.id, sensor: revoking.sensor }) }}
      </p>
      <template #footer>
        <Button
          :label="t('admin.revoke.cancel')"
          severity="secondary"
          text
          @click="revoking = null"
        />
        <Button
          :label="t('admin.revoke.confirm')"
          @click="revoke"
        />
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
/* The sections of a signed-in administrator, one below the other. */
.sections {
  display: grid;
  gap: var(--space-4);
}

.sign-in {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-width: 28rem;
}

.sign-in p {
  margin: 0;
}

.hint {
  font-size: 0.9em;
}

.create {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.secret {
  display: block;
  padding: var(--space-2) var(--space-3);
  overflow-wrap: anywhere;
  user-select: all;
  background: var(--c-paper);
  border: 1px solid var(--c-rule);
  border-radius: 4px;
}
</style>
