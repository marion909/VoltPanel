<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatDateTime } from '../format'

const jobs = ref([])
const sites = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const showForm = ref(false)
const logFor = ref(null)
const logText = ref('')

const form = ref({ name: '', schedule: '0 3 * * *', command: '', site_id: null })

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [jobList, siteList] = await Promise.all([api.get('/cronjobs'), api.get('/sites')])
    jobs.value = jobList
    sites.value = siteList
    if (sites.value.length && form.value.site_id === null) {
      form.value.site_id = sites.value[0].id
    }
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
    await api.post('/cronjobs', form.value)
    showForm.value = false
    form.value = {
      name: '',
      schedule: '0 3 * * *',
      command: '',
      site_id: sites.value[0]?.id ?? null,
    }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function toggle(job) {
  try {
    // Ein abgeschalteter Job verschwindet aus /etc/cron.d — er ist dann
    // wirklich aus, nicht nur im Panel als inaktiv markiert.
    const updated = await api.patch(`/cronjobs/${job.id}`, { enabled: !job.enabled })
    jobs.value = jobs.value.map((j) => (j.id === updated.id ? updated : j))
  } catch (err) {
    error.value = err.message
  }
}

async function remove(job) {
  if (!confirm(t('cron.confirmDelete', { name: job.name }))) return
  try {
    await api.del(`/cronjobs/${job.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function showLog(job) {
  if (logFor.value === job.id) {
    logFor.value = null
    return
  }
  try {
    const res = await api.get(`/cronjobs/${job.id}/log?lines=200`)
    logFor.value = job.id
    logText.value = res.content || t('cron.noOutput')
  } catch (err) {
    error.value = err.message
  }
}

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('cron.title') }}</h1>
      <button
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="showForm = !showForm"
      >
        {{ showForm ? t('common.cancel') : t('cron.new') }}
      </button>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-2"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="create"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('cron.name') }}
        </span>
        <input v-model="form.name" required placeholder="Laravel Scheduler"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('files.site') }}
        </span>
        <select v-model="form.site_id" required
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.domain }}</option>
        </select>
        <span class="mt-1 block text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('cron.siteHint') }}
        </span>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('cron.schedule') }}
        </span>
        <input v-model="form.schedule" required placeholder="*/5 * * * *"
               class="tabular w-full rounded-md border px-3 py-2 font-mono text-[13px]" :style="inputStyle" />
        <span class="mt-1 block text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('cron.scheduleHint') }}
        </span>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('cron.command') }}
        </span>
        <input v-model="form.command" required placeholder="/usr/bin/php8.3 /var/www/example.at/artisan schedule:run"
               class="w-full rounded-md border px-3 py-2 font-mono text-[12px]" :style="inputStyle" />
      </label>

      <div class="sm:col-span-2">
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
      v-else-if="jobs.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('cron.name') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('cron.schedule') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('cron.runAs') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('cron.lastRun') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('cron.enabled') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="job in jobs" :key="job.id">
            <tr class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
              <td class="px-4 py-2.5 font-medium">
                {{ job.name }}
                <div class="truncate font-mono text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                  {{ job.command }}
                </div>
              </td>
              <td class="tabular px-4 py-2.5 font-mono text-[12px]">{{ job.schedule }}</td>
              <td class="px-4 py-2.5 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
                {{ job.run_as }}
              </td>
              <td class="tabular px-4 py-2.5 text-[12px]" :style="{ color: 'var(--ink-muted)' }">
                {{ job.last_run_at ? formatDateTime(job.last_run_at) : t('cron.never') }}
                <span
                  v-if="job.last_exit_code !== null && job.last_exit_code !== undefined"
                  :style="{
                    color: job.last_exit_code === 0 ? 'var(--status-good)' : 'var(--status-critical)',
                  }"
                >
                  ({{ job.last_exit_code }})
                </span>
              </td>
              <td class="px-4 py-2.5">
                <button
                  class="inline-flex items-center gap-1.5 text-[12px]"
                  :style="{ color: job.enabled ? 'var(--status-good)' : 'var(--ink-muted)' }"
                  @click="toggle(job)"
                >
                  <span class="inline-block h-2 w-2 rounded-full"
                        :style="{ background: job.enabled ? 'var(--status-good)' : 'var(--line-axis)' }"
                        aria-hidden="true" />
                  {{ job.enabled ? t('common.yes') : t('common.no') }}
                </button>
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap text-[12px]">
                <button class="underline" :style="{ color: 'var(--ink-secondary)' }" @click="showLog(job)">
                  {{ t('cron.log') }}
                </button>
                <button class="ml-3 underline" :style="{ color: 'var(--status-critical)' }"
                        @click="remove(job)">
                  {{ t('sites.delete') }}
                </button>
              </td>
            </tr>

            <tr v-if="logFor === job.id" :style="{ background: 'var(--surface-sunken)' }">
              <td colspan="6" class="px-4 py-3">
                <pre class="max-h-64 overflow-auto font-mono text-[11px] leading-relaxed"
                     :style="{ color: 'var(--ink-secondary)' }">{{ logText }}</pre>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('cron.empty') }}</p>
  </div>
</template>
