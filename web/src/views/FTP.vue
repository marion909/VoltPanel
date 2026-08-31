<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { isAdmin } from '../stores/session'

const accounts = ref([])
const sites = ref([])
const status = ref(null)
const orphans = ref([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

// Frisch erzeugte Zugangsdaten. Sie stehen genau einmal hier — danach nur noch
// verschlüsselt in der Panel-Datenbank.
const credentials = ref(null)

const showForm = ref(false)
const form = ref({ site_id: null, username: '', password: '', subdir: '', quota_mb: 0 })

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const siteName = (id) => sites.value.find((s) => s.id === id)?.domain || '—'

async function load() {
  loading.value = true
  try {
    const [list, siteList, state] = await Promise.all([
      api.get('/ftp'),
      api.get('/sites'),
      api.get('/ftp/status').catch(() => null),
    ])
    accounts.value = list
    sites.value = siteList
    status.value = state
    error.value = ''
    if (isAdmin() && state?.ready) {
      orphans.value = await api.get('/ftp/orphans').catch(() => [])
    }
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function setup() {
  busy.value = true
  error.value = ''
  try {
    status.value = await api.post('/ftp/setup')
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function create() {
  busy.value = true
  error.value = ''
  credentials.value = null
  try {
    const res = await api.post('/ftp', {
      ...form.value,
      site_id: Number(form.value.site_id),
      quota_mb: Number(form.value.quota_mb) || 0,
    })
    credentials.value = { username: res.account.username, password: res.password }
    showForm.value = false
    form.value = { site_id: null, username: '', password: '', subdir: '', quota_mb: 0 }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function reveal(account) {
  try {
    const res = await api.post(`/ftp/${account.id}/reveal`)
    credentials.value = { username: account.username, password: res.password }
  } catch (err) {
    error.value = err.message
  }
}

async function newPassword(account) {
  try {
    const res = await api.patch(`/ftp/${account.id}`, { password: '' })
    credentials.value = { username: account.username, password: res.password }
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function toggle(account) {
  try {
    await api.patch(`/ftp/${account.id}`, {
      status: account.status === 'active' ? 'disabled' : 'active',
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function remove(account) {
  if (!confirm(t('ftp.confirmDelete', { name: account.username }))) return
  try {
    await api.del(`/ftp/${account.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('ftp.title') }}</h1>
      <button
        v-if="status?.ready"
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="showForm = !showForm"
      >
        {{ showForm ? t('common.cancel') : t('ftp.new') }}
      </button>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <!-- Nicht eingerichtet: ohne den Dienst hilft eine Liste niemandem. -->
    <div
      v-if="status && !status.ready"
      class="mb-5 rounded-lg border p-5"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <h2 class="mb-1 text-[14px] font-medium">{{ t('ftp.setupTitle') }}</h2>
      <p class="mb-3 text-[12px] whitespace-pre-line" :style="{ color: 'var(--ink-secondary)' }">
        {{ t('ftp.setupHint', { from: status.passive_from, to: status.passive_to }) }}
      </p>
      <button
        v-if="isAdmin()"
        :disabled="busy"
        class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
        :style="{ background: 'var(--series-1)' }"
        @click="setup"
      >
        {{ busy ? t('ftp.settingUp') : t('ftp.setup') }}
      </button>
      <p v-else class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
        {{ t('ftp.setupAdminOnly') }}
      </p>
    </div>

    <p
      v-if="status?.firewall_hint"
      class="mb-4 text-[12px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ status.firewall_hint }}
    </p>

    <!-- Zugangsdaten stehen hervorgehoben und nur einmal. -->
    <div
      v-if="credentials"
      class="mb-4 rounded-lg border p-4"
      :style="{
        borderColor: 'var(--status-good)',
        background: 'color-mix(in srgb, var(--status-good) 8%, var(--surface-card))',
      }"
    >
      <div class="mb-2 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
        {{ t('ftp.credentials') }}
      </div>
      <dl class="tabular grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('ftp.username') }}</dt>
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
          {{ t('files.site') }}
        </span>
        <select v-model="form.site_id" required
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option :value="null" disabled>—</option>
          <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.domain }}</option>
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('ftp.username') }}
        </span>
        <input v-model="form.username" :placeholder="t('ftp.usernameAuto')"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('ftp.subdir') }}
        </span>
        <input v-model="form.subdir" placeholder="public"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <div class="flex items-end gap-3">
        <label class="block flex-1">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('ftp.quota') }}
          </span>
          <input v-model="form.quota_mb" type="number" min="0" placeholder="0"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
        <button type="submit" :disabled="busy"
                class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }">
          {{ busy ? t('common.loading') : t('sites.create') }}
        </button>
      </div>
    </form>

    <p v-if="orphans.length" class="mb-4 text-[12px]" :style="{ color: 'var(--status-warning)' }">
      {{ t('ftp.orphans', { names: orphans.join(', ') }) }}
    </p>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <div
      v-else-if="accounts.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('ftp.username') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('files.site') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('ftp.home') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('ftp.status') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="account in accounts" :key="account.id"
              class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
            <td class="px-4 py-2.5 font-medium">{{ account.username }}</td>
            <td class="px-4 py-2.5" :style="{ color: 'var(--ink-secondary)' }">
              {{ siteName(account.site_id) }}
            </td>
            <td class="px-4 py-2.5 font-mono text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ account.home_dir }}
            </td>
            <td class="px-4 py-2.5">
              <span :style="{
                color: account.status === 'active' ? 'var(--status-good)' : 'var(--ink-muted)',
              }">
                {{ t(`ftp.status_${account.status}`) }}
              </span>
            </td>
            <td class="px-4 py-2.5 text-right whitespace-nowrap">
              <button class="text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="reveal(account)">
                {{ t('db.reveal') }}
              </button>
              <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="newPassword(account)">
                {{ t('db.newPassword') }}
              </button>
              <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="toggle(account)">
                {{ account.status === 'active' ? t('ftp.disable') : t('ftp.enable') }}
              </button>
              <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--status-critical)' }"
                      @click="remove(account)">
                {{ t('sites.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-else-if="status?.ready" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('ftp.empty') }}
    </p>
  </div>
</template>
