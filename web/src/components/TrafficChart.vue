<script setup>
import { computed, ref } from 'vue'
import { t } from '../i18n'
import { formatRate, formatClock } from '../format'

const props = defineProps({
  // [{ timestamp, net_rx_per_sec, net_tx_per_sec }]
  series: { type: Array, default: () => [] },
})

const width = 760
const height = 200
const pad = { top: 12, right: 68, bottom: 22, left: 8 }

// Beide Reihen sind Durchsatz in Byte/s — dieselbe Einheit, also eine Achse.
// Zwei Skalen nebeneinander würden Verhältnisse vortäuschen, die es nicht gibt.
const lines = [
  { key: 'net_rx_per_sec', color: 'var(--series-1)', labelKey: 'dash.in' },
  { key: 'net_tx_per_sec', color: 'var(--series-2)', labelKey: 'dash.out' },
]

const points = computed(() => props.series.filter((s) => s && s.timestamp))

const maxValue = computed(() => {
  let peak = 0
  for (const p of points.value) {
    peak = Math.max(peak, p.net_rx_per_sec || 0, p.net_tx_per_sec || 0)
  }
  // Nie durch null teilen, und eine ruhige Leitung soll nicht als Vollausschlag
  // erscheinen: 64 KiB/s als Bodensatz der Skala.
  return Math.max(peak * 1.15, 65536)
})

const plotW = width - pad.left - pad.right
const plotH = height - pad.top - pad.bottom

function xAt(index) {
  const n = points.value.length
  if (n <= 1) return pad.left
  return pad.left + (index / (n - 1)) * plotW
}

function yAt(value) {
  return pad.top + plotH - ((value || 0) / maxValue.value) * plotH
}

function path(key) {
  return points.value.map((p, i) => `${i === 0 ? 'M' : 'L'}${xAt(i).toFixed(1)},${yAt(p[key]).toFixed(1)}`).join(' ')
}

function areaPath(key) {
  if (points.value.length < 2) return ''
  const base = pad.top + plotH
  return `${path(key)} L${xAt(points.value.length - 1).toFixed(1)},${base} L${pad.left},${base} Z`
}

// Vier Hilfslinien reichen, um Größenordnungen abzulesen.
const gridLines = computed(() =>
  [0, 0.25, 0.5, 0.75, 1].map((f) => ({
    y: pad.top + plotH - f * plotH,
    value: maxValue.value * f,
  })),
)

const latest = computed(() => points.value.at(-1) || {})

// --- Fadenkreuz und Tooltip ---
const hoverIndex = ref(null)

function onMove(event) {
  const n = points.value.length
  if (n === 0) return
  const rect = event.currentTarget.getBoundingClientRect()
  // Auf das viewBox-Koordinatensystem umrechnen, damit das Fadenkreuz auch
  // bei skalierter Darstellung am richtigen Punkt sitzt.
  const x = ((event.clientX - rect.left) / rect.width) * width
  const ratio = (x - pad.left) / plotW
  hoverIndex.value = Math.min(Math.max(Math.round(ratio * (n - 1)), 0), n - 1)
}

const hovered = computed(() =>
  hoverIndex.value === null ? null : points.value[hoverIndex.value] || null,
)

// Der Tooltip springt vor dem rechten Rand auf die andere Seite des Fadenkreuzes.
const tooltipX = computed(() => {
  if (hoverIndex.value === null) return 0
  const x = xAt(hoverIndex.value)
  return x > pad.left + plotW * 0.6 ? x - 138 : x + 10
})

const showTable = ref(false)
</script>

<template>
  <section
    class="rounded-lg border"
    :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
  >
    <header class="flex flex-wrap items-center justify-between gap-3 px-4 pt-3.5 pb-1">
      <h2 class="text-[13px] font-medium">{{ t('dash.traffic') }}</h2>

      <div class="flex items-center gap-4">
        <!-- Legende: bei zwei Reihen immer sichtbar, damit die Zuordnung nie
             allein an der Farbe hängt. -->
        <div v-for="line in lines" :key="line.key" class="flex items-center gap-1.5">
          <span
            class="inline-block h-2 w-2 rounded-full"
            :style="{ background: line.color }"
            aria-hidden="true"
          />
          <span class="text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t(line.labelKey) }}
          </span>
        </div>

        <button
          class="text-[11px] underline"
          :style="{ color: 'var(--ink-muted)' }"
          @click="showTable = !showTable"
        >
          {{ showTable ? t('dash.hideTable') : t('dash.table') }}
        </button>
      </div>
    </header>

    <p v-if="points.length < 2" class="px-4 pt-6 pb-8 text-center text-[13px]"
       :style="{ color: 'var(--ink-muted)' }">
      {{ t('dash.noData') }}
    </p>

    <svg
      v-else
      :viewBox="`0 0 ${width} ${height}`"
      class="block w-full"
      :style="{ height: `${height}px` }"
      preserveAspectRatio="none"
      role="img"
      :aria-label="t('dash.traffic')"
      @mousemove="onMove"
      @mouseleave="hoverIndex = null"
    >
      <!-- Raster tritt zurück: Haarlinien, keine vertikalen Linien -->
      <g>
        <line
          v-for="(g, i) in gridLines"
          :key="i"
          :x1="pad.left"
          :x2="pad.left + plotW"
          :y1="g.y"
          :y2="g.y"
          stroke="var(--line-hairline)"
          stroke-width="1"
        />
        <text
          v-for="(g, i) in gridLines"
          :key="`l${i}`"
          :x="pad.left + plotW + 6"
          :y="g.y + 3.5"
          class="tabular"
          font-size="10"
          fill="var(--ink-muted)"
        >
          {{ formatRate(g.value) }}
        </text>
      </g>

      <g v-for="line in lines" :key="line.key">
        <path :d="areaPath(line.key)" :fill="line.color" opacity="0.1" />
        <path
          :d="path(line.key)"
          fill="none"
          :stroke="line.color"
          stroke-width="2"
          stroke-linejoin="round"
          stroke-linecap="round"
        />
      </g>

      <!-- Fadenkreuz -->
      <g v-if="hovered">
        <line
          :x1="xAt(hoverIndex)"
          :x2="xAt(hoverIndex)"
          :y1="pad.top"
          :y2="pad.top + plotH"
          stroke="var(--line-axis)"
          stroke-width="1"
        />
        <circle
          v-for="line in lines"
          :key="line.key"
          :cx="xAt(hoverIndex)"
          :cy="yAt(hovered[line.key])"
          r="4"
          :fill="line.color"
          stroke="var(--surface-card)"
          stroke-width="2"
        />

        <g :transform="`translate(${tooltipX}, ${pad.top + 4})`">
          <rect
            width="128"
            height="54"
            rx="6"
            fill="var(--surface-card)"
            stroke="var(--line-axis)"
            stroke-width="1"
          />
          <text x="10" y="17" font-size="10" fill="var(--ink-muted)" class="tabular">
            {{ formatClock(hovered.timestamp) }}
          </text>
          <g v-for="(line, i) in lines" :key="line.key">
            <circle :cx="13" :cy="30 + i * 14" r="3" :fill="line.color" />
            <text :x="22" :y="33 + i * 14" font-size="11" fill="var(--ink-primary)" class="tabular">
              {{ formatRate(hovered[line.key]) }}
            </text>
          </g>
        </g>
      </g>

      <!-- Direktbeschriftung am Linienende: der aktuelle Wert, ohne Blick zur Legende -->
      <g v-if="!hovered">
        <text
          v-for="(line, i) in lines"
          :key="line.key"
          :x="pad.left + plotW + 6"
          :y="pad.top + 12 + i * 15"
          font-size="11"
          :fill="line.color"
          class="tabular"
          font-weight="600"
        >
          {{ formatRate(latest[line.key]) }}
        </text>
      </g>
    </svg>

    <!-- Tabellensicht: die Werte auch ohne Farbwahrnehmung zugänglich -->
    <div v-if="showTable && points.length" class="max-h-56 overflow-y-auto px-4 pb-4">
      <table class="w-full text-left text-[12px]">
        <thead :style="{ color: 'var(--ink-muted)' }">
          <tr>
            <th class="py-1 font-normal">{{ t('dash.time') }}</th>
            <th class="py-1 text-right font-normal">{{ t('dash.in') }}</th>
            <th class="py-1 text-right font-normal">{{ t('dash.out') }}</th>
          </tr>
        </thead>
        <tbody class="tabular">
          <tr
            v-for="p in points.slice().reverse()"
            :key="p.timestamp"
            class="border-t"
            :style="{ borderColor: 'var(--line-hairline)' }"
          >
            <td class="py-1">{{ formatClock(p.timestamp) }}</td>
            <td class="py-1 text-right">{{ formatRate(p.net_rx_per_sec) }}</td>
            <td class="py-1 text-right">{{ formatRate(p.net_tx_per_sec) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
