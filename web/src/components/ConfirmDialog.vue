<script setup>
import { nextTick, watch, useTemplateRef } from 'vue'
import { confirmState, resolveConfirm } from '../stores/confirm'
import { t } from '../i18n'

const confirmBtn = useTemplateRef('confirmBtn')

// Der native confirm()-Dialog, den das hier ersetzt, hatte den Fokus schon
// auf einer Schaltfläche — ohne das nachzubilden, müsste ein
// Tastaturnutzer nach dem Öffnen erst manuell zum Dialog tabben.
watch(
  () => confirmState.open,
  async (open) => {
    if (!open) return
    await nextTick()
    confirmBtn.value?.focus()
  },
)
</script>

<template>
  <div
    v-if="confirmState.open"
    class="fixed inset-0 z-50 flex items-center justify-center p-4"
    role="presentation"
    @keydown.esc="resolveConfirm(false)"
  >
    <div class="absolute inset-0 bg-black/50" @click="resolveConfirm(false)" />

    <div
      class="panel-card relative w-full max-w-sm p-5"
      role="alertdialog"
      aria-modal="true"
      :aria-label="confirmState.title || t('common.confirmTitle')"
    >
      <h2 class="text-[15px] font-semibold">
        {{ confirmState.title || t('common.confirmTitle') }}
      </h2>
      <p class="mt-2 text-[13px] text-ink-secondary">
        {{ confirmState.message }}
      </p>
      <ul
        v-if="confirmState.details.length"
        class="mt-2 list-disc space-y-0.5 pl-5 text-[12px] text-ink-secondary"
      >
        <li v-for="(d, i) in confirmState.details" :key="i">{{ d }}</li>
      </ul>

      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border px-3 py-1.5 text-[13px]"
          :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
          @click="resolveConfirm(false)"
        >
          {{ confirmState.cancelLabel || t('common.cancel') }}
        </button>
        <button
          ref="confirmBtn"
          type="button"
          class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
          :style="{ background: confirmState.danger ? 'var(--status-critical)' : 'var(--accent)' }"
          @click="resolveConfirm(true)"
        >
          {{ confirmState.confirmLabel || (confirmState.danger ? t('common.delete') : t('common.confirm')) }}
        </button>
      </div>
    </div>
  </div>
</template>
