<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatDateTime } from '../format'

const state = ref(null)
const error = ref('')
const notice = ref('')
const busy = ref(false)

async function load() {
  try {
    state.value = await api.get('/system/panel-certificate')
  } catch (err) {
    error.value = err.message
  }
}

async function issue() {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const res = await api.post('/system/panel-certificate')
    notice.value = t('panelCert.issued', { date: formatDateTime(res.not_after) })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <section
    v-if="state"
    class="mb-4 rounded-lg border p-5"
    :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
  >
    <h2 class="mb-1 text-[14px] font-medium">{{ t('panelCert.title') }}</h2>

    <p v-if="!state.domain" class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('panelCert.noDomain') }}
    </p>

    <template v-else>
      <dl class="mb-3 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-[13px]">
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('panelCert.domain') }}</dt>
        <dd>{{ state.domain }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('panelCert.state') }}</dt>
        <dd :style="{ color: state.self_signed ? 'var(--status-warning)' : 'var(--status-good)' }">
          {{ state.self_signed ? t('panelCert.selfSigned') : t('panelCert.trusted') }}
        </dd>
        <template v-if="state.not_after">
          <dt :style="{ color: 'var(--ink-muted)' }">{{ t('panelCert.expires') }}</dt>
          <dd class="tabular">
            {{ formatDateTime(state.not_after) }} ({{ state.days_left }} {{ t('panelCert.days') }})
          </dd>
        </template>
      </dl>

      <!-- Der Hinweis steht hier, weil die Prüfung von außen kommt: schlägt sie
           fehl, sieht man erst hinterher, woran es lag. -->
      <p class="mb-3 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
        {{ t('panelCert.hint') }}
      </p>

      <button
        :disabled="busy"
        class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
        :style="{ background: 'var(--series-1)' }"
        @click="issue"
      >
        {{ busy ? t('panelCert.issuing') : t('panelCert.issue') }}
      </button>
    </template>

    <p v-if="notice" class="mt-3 text-[12px]" :style="{ color: 'var(--status-good)' }" role="status">
      {{ notice }}
    </p>
    <p v-if="error" class="mt-3 text-[12px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>
  </section>
</template>
