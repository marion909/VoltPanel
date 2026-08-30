<script setup>
import { computed } from 'vue'

const props = defineProps({
  label: { type: String, required: true },
  percent: { type: Number, default: 0 },
  caption: { type: String, default: '' },
  size: { type: Number, default: 120 },
})

const clamped = computed(() => Math.min(Math.max(props.percent || 0, 0), 100))

// Ein Ring ist eine Einzelgröße, keine Serie: die Farbe kommt deshalb aus der
// Statuspalette und meint einen Zustand, nicht eine Identität. Sie steht nie
// allein — Beschriftung und Zahl tragen die Aussage genauso.
const status = computed(() => {
  if (clamped.value >= 90) return { color: 'var(--status-critical)', key: 'kritisch' }
  if (clamped.value >= 75) return { color: 'var(--status-warning)', key: 'hoch' }
  return { color: 'var(--status-good)', key: 'normal' }
})

const stroke = 9
const radius = computed(() => (props.size - stroke) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)
// Die Lücke bleibt gleich groß, egal wie groß der Ring gezeichnet wird.
const dash = computed(() => (clamped.value / 100) * circumference.value)
</script>

<template>
  <figure class="m-0 flex flex-col items-center gap-2">
    <div class="relative" :style="{ width: `${size}px`, height: `${size}px` }">
      <svg
        :width="size"
        :height="size"
        :viewBox="`0 0 ${size} ${size}`"
        role="img"
        :aria-label="`${label}: ${clamped.toFixed(0)} Prozent, ${status.key}`"
      >
        <!-- Ring beginnt oben, nicht rechts -->
        <g :transform="`rotate(-90 ${size / 2} ${size / 2})`">
          <circle
            :cx="size / 2"
            :cy="size / 2"
            :r="radius"
            fill="none"
            stroke="var(--surface-sunken)"
            :stroke-width="stroke"
          />
          <circle
            :cx="size / 2"
            :cy="size / 2"
            :r="radius"
            fill="none"
            :stroke="status.color"
            :stroke-width="stroke"
            stroke-linecap="round"
            :stroke-dasharray="`${dash} ${circumference}`"
            style="transition: stroke-dasharray 0.4s ease-out"
          />
        </g>
      </svg>

      <div class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
        <span class="text-[26px] leading-none font-semibold">{{ clamped.toFixed(0) }}<span
          class="text-[15px] font-normal" :style="{ color: 'var(--ink-muted)' }">%</span></span>
      </div>
    </div>

    <figcaption class="text-center">
      <div class="text-[13px] font-medium">{{ label }}</div>
      <div v-if="caption" class="tabular text-[11px]" :style="{ color: 'var(--ink-muted)' }">
        {{ caption }}
      </div>
    </figcaption>
  </figure>
</template>
