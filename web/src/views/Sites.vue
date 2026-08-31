<script setup>
import { ref, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'

const sites = ref([])
const loading = ref(true)
const error = ref('')
const showForm = ref(false)
const busy = ref(false)

const form = ref({ domain: '', type: 'static', php_version: '8.3', proxy_target: '', document_root: 'public' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    sites.value = await api.get('/sites')
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function create() {
  busy.value = true
  error.value = ''
  try {
    const payload = { ...form.value }
    // Nur mitschicken, was zum Typ gehört — der Server lehnt eine PHP-Version
    // an einer Proxy-Site sonst zu Recht ab.
    if (payload.type !== 'php') payload.php_version = ''
    if (payload.type !== 'proxy') payload.proxy_target = ''

    await api.post('/sites', payload)
    showForm.value = false
    form.value = { domain: '', type: 'static', php_version: '8.3', proxy_target: '', document_root: 'public' }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function rebuild(site) {
  try {
    await api.post(`/sites/${site.id}/rebuild`)
  } catch (err) {
    error.value = err.message
  }
}

async function remove(site) {
  if (!confirm(t('sites.confirmDelete', { domain: site.domain }))) return
  try {
    await api.del(`/sites/${site.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

onMounted(load)

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('sites.title') }}</h1>
      <button
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="showForm = !showForm"
      >
        {{ showForm ? t('common.cancel') : t('sites.new') }}
      </button>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-2 lg:grid-cols-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="create"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('sites.domain') }}
        </span>
        <input v-model="form.domain" required placeholder="example.at"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('sites.type') }}
        </span>
        <select v-model="form.type" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option value="static">static</option>
          <option value="php">php</option>
          <option value="proxy">proxy</option>
        </select>
      </label>

      <label v-if="form.type === 'php'" class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('sites.php') }}
        </span>
        <select v-model="form.php_version" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option v-for="v in ['7.4', '8.0', '8.1', '8.2', '8.3', '8.4']" :key="v" :value="v">{{ v }}</option>
        </select>
      </label>

      <label v-if="form.type === 'proxy'" class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">Proxy-Ziel</span>
        <input v-model="form.proxy_target" required placeholder="http://127.0.0.1:3000"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <div class="flex items-end">
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
      v-else-if="sites.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('sites.domain') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('sites.type') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('sites.php') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('sites.ssl') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('sites.status') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="site in sites"
            :key="site.id"
            class="border-b last:border-0"
            :style="{ borderColor: 'var(--line-hairline)' }"
          >
            <td class="px-4 py-2.5">
              <RouterLink :to="`/sites/${site.id}`" class="font-medium hover:underline">
                {{ site.domain }}
              </RouterLink>
            </td>
            <td class="px-4 py-2.5" :style="{ color: 'var(--ink-secondary)' }">{{ site.type }}</td>
            <td class="px-4 py-2.5 tabular" :style="{ color: 'var(--ink-secondary)' }">
              {{ site.php_version || '—' }}
            </td>
            <td class="px-4 py-2.5">
              <span
                class="inline-flex items-center gap-1.5"
                :style="{ color: site.ssl_enabled ? 'var(--status-good)' : 'var(--ink-muted)' }"
              >
                <!-- Symbol plus Wort: der Zustand hängt nie allein an der Farbe -->
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="2.2" aria-hidden="true">
                  <path v-if="site.ssl_enabled" d="M20 6L9 17l-5-5" stroke-linecap="round" stroke-linejoin="round" />
                  <path v-else d="M18 6L6 18M6 6l12 12" stroke-linecap="round" />
                </svg>
                {{ site.ssl_enabled ? t('common.yes') : t('common.no') }}
              </span>
            </td>
            <td class="px-4 py-2.5" :style="{ color: 'var(--ink-secondary)' }">{{ site.status }}</td>
            <td class="px-4 py-2.5 text-right whitespace-nowrap">
              <RouterLink :to="`/sites/${site.id}`" class="text-[12px] underline"
                          :style="{ color: 'var(--ink-secondary)' }">
                {{ t('site.settings') }}
              </RouterLink>
              <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="rebuild(site)">
                {{ t('sites.rebuild') }}
              </button>
              <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--status-critical)' }"
                      @click="remove(site)">
                {{ t('sites.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('sites.empty') }}</p>
  </div>
</template>
