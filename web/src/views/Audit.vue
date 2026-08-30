<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatDateTime } from '../format'

const entries = ref([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    entries.value = await api.get('/audit?limit=200')
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="fade-in px-8 py-6">
    <h1 class="mb-5 text-[18px] font-semibold tracking-tight">{{ t('audit.title') }}</h1>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }">{{ error }}</p>
    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <div
      v-else-if="entries.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('audit.time') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('audit.actor') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('audit.action') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('audit.target') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('audit.result') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="entry in entries"
            :key="entry.id"
            class="border-b last:border-0"
            :style="{ borderColor: 'var(--line-hairline)' }"
          >
            <td class="tabular px-4 py-2 whitespace-nowrap" :style="{ color: 'var(--ink-secondary)' }">
              {{ formatDateTime(entry.created_at) }}
            </td>
            <td class="px-4 py-2">{{ entry.actor || '—' }}</td>
            <td class="px-4 py-2 font-medium">{{ entry.action }}</td>
            <td class="px-4 py-2" :style="{ color: 'var(--ink-secondary)' }">
              {{ entry.target_id || '—' }}
            </td>
            <td class="px-4 py-2">
              <span
                class="inline-flex items-center gap-1.5"
                :style="{ color: entry.result === 'ok' ? 'var(--status-good)' : 'var(--status-critical)' }"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path v-if="entry.result === 'ok'" d="M20 6L9 17l-5-5" />
                  <path v-else d="M12 8v5M12 17h.01M10.3 3.9L1.8 18a2 2 0 001.7 3h17a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" />
                </svg>
                {{ entry.result }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('audit.empty') }}</p>
  </div>
</template>
