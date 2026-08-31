<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes } from '../format'

const databases = ref([])
const sites = ref([])
const usersByDB = ref({})
const expanded = ref(null)
const loading = ref(true)
const error = ref('')
const busy = ref(false)

// Frisch erzeugte Zugangsdaten. Sie stehen genau einmal im Klartext hier —
// danach nur noch verschlüsselt in der Panel-Datenbank.
const credentials = ref(null)

const showForm = ref(false)
const form = ref({ name: '', site_id: null, with_user: true, username: '', password: '' })

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [dbs, siteList] = await Promise.all([api.get('/databases'), api.get('/sites')])
    databases.value = dbs
    sites.value = siteList
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function toggleUsers(db) {
  if (expanded.value === db.id) {
    expanded.value = null
    return
  }
  expanded.value = db.id
  try {
    usersByDB.value = { ...usersByDB.value, [db.id]: await api.get(`/databases/${db.id}/users`) }
  } catch (err) {
    error.value = err.message
  }
}

async function create() {
  busy.value = true
  error.value = ''
  credentials.value = null
  try {
    const res = await api.post('/databases', {
      ...form.value,
      site_id: form.value.site_id || null,
    })
    if (res.password) {
      credentials.value = {
        database: res.database.name,
        username: res.user?.username,
        password: res.password,
      }
    }
    showForm.value = false
    form.value = { name: '', site_id: null, with_user: true, username: '', password: '' }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function remove(db) {
  if (!confirm(t('db.confirmDelete', { name: db.name }))) return
  try {
    await api.del(`/databases/${db.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function dump(db) {
  try {
    const res = await api.post(`/databases/${db.id}/dump`)
    error.value = ''
    credentials.value = null
    alert(`${res.path}\n${formatBytes(res.size_bytes)}`)
  } catch (err) {
    error.value = err.message
  }
}

async function addUser(db) {
  const username = prompt(t('db.username'), db.name)
  if (!username) return
  try {
    const res = await api.post(`/databases/${db.id}/users`, { username })
    credentials.value = {
      database: db.name,
      username: res.user.username,
      password: res.password,
    }
    usersByDB.value = { ...usersByDB.value, [db.id]: await api.get(`/databases/${db.id}/users`) }
  } catch (err) {
    error.value = err.message
  }
}

async function revealPassword(user) {
  try {
    const res = await api.post(`/db-users/${user.id}/reveal`)
    credentials.value = { username: user.username, password: res.password }
  } catch (err) {
    error.value = err.message
  }
}

async function resetPassword(user) {
  try {
    const res = await api.patch(`/db-users/${user.id}`, { password: '' })
    credentials.value = { username: user.username, password: res.password }
  } catch (err) {
    error.value = err.message
  }
}

async function setGrants(user, grants) {
  try {
    await api.patch(`/db-users/${user.id}`, { grants })
    usersByDB.value = {
      ...usersByDB.value,
      [user.database_id]: await api.get(`/databases/${user.database_id}/users`),
    }
  } catch (err) {
    error.value = err.message
  }
}

async function removeUser(user) {
  if (!confirm(t('db.confirmDeleteUser', { name: user.username }))) return
  try {
    await api.del(`/db-users/${user.id}`)
    usersByDB.value = {
      ...usersByDB.value,
      [user.database_id]: await api.get(`/databases/${user.database_id}/users`),
    }
  } catch (err) {
    error.value = err.message
  }
}

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('db.title') }}</h1>
      <button
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="showForm = !showForm"
      >
        {{ showForm ? t('common.cancel') : t('db.new') }}
      </button>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <!-- Zugangsdaten stehen bewusst hervorgehoben und nur einmal. -->
    <div
      v-if="credentials"
      class="mb-4 rounded-lg border p-4"
      :style="{
        borderColor: 'var(--status-good)',
        background: 'color-mix(in srgb, var(--status-good) 8%, var(--surface-card))',
      }"
    >
      <div class="mb-2 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
        {{ t('db.credentialsOnce') }}
      </div>
      <dl class="tabular grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
        <template v-if="credentials.database">
          <dt :style="{ color: 'var(--ink-muted)' }">{{ t('db.name') }}</dt>
          <dd>{{ credentials.database }}</dd>
        </template>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('db.username') }}</dt>
        <dd>{{ credentials.username }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('db.password') }}</dt>
        <dd class="font-medium break-all">{{ credentials.password }}</dd>
      </dl>
      <button class="mt-2 text-[11px] underline" :style="{ color: 'var(--ink-muted)' }"
              @click="credentials = null">
        {{ t('common.cancel') }}
      </button>
    </div>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-2 lg:grid-cols-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="create"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('db.name') }}
        </span>
        <input v-model="form.name" required placeholder="wordpress"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('files.site') }}
        </span>
        <select v-model="form.site_id" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option :value="null">—</option>
          <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.domain }}</option>
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('db.password') }}
        </span>
        <input v-model="form.password" type="text" :placeholder="t('db.passwordGenerated')"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <div class="flex items-end gap-3">
        <label class="flex items-center gap-2 pb-2 text-[12px]">
          <input v-model="form.with_user" type="checkbox" />
          {{ t('db.withUser') }}
        </label>
        <button type="submit" :disabled="busy"
                class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }">
          {{ busy ? t('common.loading') : t('sites.create') }}
        </button>
      </div>
    </form>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <div
      v-else-if="databases.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('db.name') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('db.size') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('db.users') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="db in databases" :key="db.id">
            <tr class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
              <td class="px-4 py-2.5 font-medium">{{ db.name }}</td>
              <td class="tabular px-4 py-2.5 text-right" :style="{ color: 'var(--ink-secondary)' }">
                {{ formatBytes(db.size_bytes) }}
              </td>
              <td class="px-4 py-2.5">
                <button class="text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                        @click="toggleUsers(db)">
                  {{ expanded === db.id ? '▾' : '▸' }} {{ t('db.users') }}
                </button>
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap">
                <button class="text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                        @click="dump(db)">
                  {{ t('db.dump') }}
                </button>
                <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--status-critical)' }"
                        @click="remove(db)">
                  {{ t('sites.delete') }}
                </button>
              </td>
            </tr>

            <tr v-if="expanded === db.id" :style="{ background: 'var(--surface-sunken)' }">
              <td colspan="4" class="px-4 py-3">
                <div v-if="(usersByDB[db.id] || []).length" class="space-y-2">
                  <div
                    v-for="user in usersByDB[db.id]"
                    :key="user.id"
                    class="flex flex-wrap items-center gap-3 text-[12px]"
                  >
                    <span class="tabular font-medium">{{ user.username }}@{{ user.host_pattern }}</span>
                    <select
                      :value="user.grants"
                      class="rounded-md border px-2 py-1 text-[11px]"
                      :style="inputStyle"
                      @change="setGrants(user, $event.target.value)"
                    >
                      <option value="ALL">ALL</option>
                      <option value="READWRITE">READWRITE</option>
                      <option value="READONLY">READONLY</option>
                    </select>
                    <button class="underline" :style="{ color: 'var(--ink-secondary)' }"
                            @click="revealPassword(user)">
                      {{ t('db.reveal') }}
                    </button>
                    <button class="underline" :style="{ color: 'var(--ink-secondary)' }"
                            @click="resetPassword(user)">
                      {{ t('db.newPassword') }}
                    </button>
                    <button class="underline" :style="{ color: 'var(--status-critical)' }"
                            @click="removeUser(user)">
                      {{ t('sites.delete') }}
                    </button>
                  </div>
                </div>
                <button class="mt-2 text-[12px] underline" :style="{ color: 'var(--series-1)' }"
                        @click="addUser(db)">
                  + {{ t('db.addUser') }}
                </button>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('db.empty') }}</p>
  </div>
</template>
