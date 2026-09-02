<script setup>
import { ref } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { isAdmin } from '../stores/session'

// "X ist auf diesem Server nicht installiert" — und daneben der Knopf.
//
// Das Panel meldete an einem halben Dutzend Stellen, was fehlt, und bot nichts
// an. Wer das liest, greift zur Shell; genau dafür ist das Panel nicht da.
//
// Der Knopf schickt einen Namen aus einer festen Liste, keinen Paketnamen.
// Welche Pakete dazugehören, weiß der Agent — `apt-get install` mit einer
// Eingabe aus dem Browser wäre eine Rootshell mit Umweg.
const props = defineProps({
  feature: { type: String, required: true },
  text: { type: String, required: true },
})
const emit = defineEmits(['installed'])

const busy = ref(false)
const error = ref('')
const result = ref('')

async function installieren() {
  busy.value = true
  error.value = ''
  result.value = ''
  try {
    const res = await api.post(`/system/features/${props.feature}`)
    result.value = res?.log || t('feature.installed')
    emit('installed')
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div
    class="mb-4 rounded-md px-3 py-2 text-[12px]"
    :style="{
      background: 'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
      color: 'var(--ink-secondary)',
    }"
  >
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span>{{ text }}</span>
      <button
        v-if="isAdmin()"
        type="button"
        class="shrink-0 rounded-md border px-2 py-1 text-[12px] disabled:opacity-60"
        :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
        :disabled="busy"
        @click="installieren"
      >
        {{ busy ? t('feature.installing') : t('feature.install') }}
      </button>
    </div>
    <p v-if="busy" class="mt-1 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('feature.installHint') }}
    </p>
    <p v-if="result" class="mt-1 text-[11px]" :style="{ color: 'var(--status-good)' }">
      {{ result }}
    </p>
    <p v-if="error" class="mt-1 text-[11px]" :style="{ color: 'var(--status-critical)' }">
      {{ error }}
    </p>
  </div>
</template>
