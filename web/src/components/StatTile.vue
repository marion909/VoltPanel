<script setup>
// Eine einzelne Kennzahl ist kein Diagramm — eine große Zahl mit Beschriftung
// liest sich schneller als jede grafische Umsetzung davon.
defineProps({
  label: { type: String, required: true },
  value: { type: [String, Number], required: true },
  hint: { type: String, default: '' },
  tone: { type: String, default: '' }, // '' | 'warning' | 'critical'
})

const toneColor = {
  warning: 'var(--status-warning)',
  critical: 'var(--status-critical)',
}
</script>

<template>
  <div
    class="rounded-lg border px-4 py-3.5"
    :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
  >
    <div class="text-[12px]" :style="{ color: 'var(--ink-secondary)' }">{{ label }}</div>
    <div class="mt-1 text-[24px] leading-tight font-semibold">{{ value }}</div>
    <div
      v-if="hint"
      class="mt-0.5 text-[11px]"
      :style="{ color: toneColor[tone] || 'var(--ink-muted)' }"
    >
      {{ hint }}
    </div>
  </div>
</template>
