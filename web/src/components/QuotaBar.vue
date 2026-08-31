<script setup>
import { computed } from 'vue'
import { formatBytes } from '../format'

const props = defineProps({
  label: { type: String, required: true },
  used: { type: Number, default: 0 },
  limit: { type: Number, default: 0 },
  // percent === -1 heißt: keine Grenze gesetzt.
  percent: { type: Number, default: -1 },
  bytes: { type: Boolean, default: false },
})

const unlimited = computed(() => props.percent < 0 || props.limit <= 0)

const usedText = computed(() => (props.bytes ? formatBytes(props.used) : String(props.used)))
const limitText = computed(() => (props.bytes ? formatBytes(props.limit) : String(props.limit)))

// Wie bei den Ring-Gauges: die Farbe meint einen Zustand, keine Identität —
// deshalb aus der Statuspalette. Sie steht nie allein, die Zahlen daneben
// tragen dieselbe Aussage.
const color = computed(() => {
  if (unlimited.value) return 'var(--series-1)'
  if (props.percent >= 90) return 'var(--status-critical)'
  if (props.percent >= 75) return 'var(--status-warning)'
  return 'var(--status-good)'
})
</script>

<template>
  <div>
    <div class="mb-1 flex items-baseline justify-between gap-2">
      <span class="text-[12px]" :style="{ color: 'var(--ink-secondary)' }">{{ label }}</span>
      <span class="tabular text-[12px]" :style="{ color: 'var(--ink-muted)' }">
        <template v-if="unlimited">{{ usedText }}</template>
        <template v-else>{{ usedText }} / {{ limitText }}</template>
      </span>
    </div>

    <div
      class="h-1.5 overflow-hidden rounded-full"
      :style="{ background: 'var(--surface-sunken)' }"
      role="meter"
      :aria-valuenow="unlimited ? undefined : Math.round(percent)"
      :aria-valuemin="0"
      :aria-valuemax="100"
      :aria-label="`${label}: ${usedText}${unlimited ? '' : ' von ' + limitText}`"
    >
      <!-- Ohne Grenze bleibt der Balken leer statt bei 0 % zu suggerieren,
           es sei etwas knapp. -->
      <div
        v-if="!unlimited"
        class="h-full rounded-full transition-[width] duration-300"
        :style="{ width: `${Math.min(Math.max(percent, 2), 100)}%`, background: color }"
      />
    </div>
  </div>
</template>
