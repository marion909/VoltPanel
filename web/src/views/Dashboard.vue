<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes, formatUptime, statusForPercent } from '../format'
import RingGauge from '../components/RingGauge.vue'
import TrafficChart from '../components/TrafficChart.vue'
import QuotaBar from '../components/QuotaBar.vue'

const latest = ref({})
const series = ref([])
const info = ref(null)
const quota = ref(null)
const error = ref('')

let socket = null
let reconnectTimer = null

// Der Verlauf bleibt auf vier Minuten begrenzt — genug für den Blick "läuft
// gerade etwas Ungewöhnliches", ohne den Speicher des Browsers zu füllen.
const maxPoints = 120

function pushSnapshot(snap) {
  if (!snap || !snap.timestamp) return
  latest.value = snap
  series.value = [...series.value, snap].slice(-maxPoints)
}

function connect() {
  socket = api.metricsSocket()
  socket.onmessage = (event) => {
    try {
      pushSnapshot(JSON.parse(event.data))
    } catch {
      /* eine unlesbare Nachricht ist kein Grund, den Stream aufzugeben */
    }
  }
  socket.onclose = () => {
    // Nach einem Neustart von volt-web soll das Dashboard von selbst
    // zurückkommen, ohne dass jemand neu lädt.
    reconnectTimer = setTimeout(connect, 3000)
  }
}

onMounted(async () => {
  try {
    const [metrics, sysInfo, quotaStatus] = await Promise.all([
      api.get('/system/metrics'),
      api.get('/system/info').catch(() => null),
      api.get('/quota').catch(() => null),
    ])
    series.value = (metrics.series || []).slice(-maxPoints)
    latest.value = metrics.latest || {}
    info.value = sysInfo
    quota.value = quotaStatus
  } catch (err) {
    error.value = err.message
  }
  connect()
})

onUnmounted(() => {
  clearTimeout(reconnectTimer)
  if (socket) {
    socket.onclose = null
    socket.close()
  }
})

// Die Wurzelpartition ist die, die im Zweifel volläuft.
const rootDisk = computed(() => {
  const disks = latest.value.disks || []
  return disks.find((d) => d.mountpoint === '/') || disks[0] || null
})

const counts = computed(() => info.value?.counts || {})
const system = computed(() => info.value?.system || {})

function quotaUsed(resource) {
  const entry = quota.value?.entries?.find((e) => e.resource === resource)
  return entry ? entry.used : null
}

</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex flex-wrap items-baseline justify-between gap-2">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('nav.dashboard') }}</h1>
      <p v-if="system.hostname" class="text-[12px] text-ink-muted">
        {{ system.hostname }} · {{ system.platform }} · {{ system.arch }}
      </p>
    </header>

    <p v-if="error" class="mb-4 text-[13px] text-status-critical">
      {{ t('common.error') }}: {{ error }}
    </p>

    <!-- Sys Status + Speicher nebeneinander: die drei Kennzahlen, die sich
         von Minute zu Minute bewegen, neben dem, was sich nur langsam füllt. -->
    <div class="mb-4 grid gap-4 lg:grid-cols-[1fr_320px]">
      <section class="panel-card px-5 py-5">
        <h2 class="mb-4 text-[14px] font-medium">{{ t('dash.sysStatus') }}</h2>
        <div class="grid grid-cols-3 gap-4">
          <RingGauge
            :label="t('dash.cpu')"
            :percent="latest.cpu_percent"
            :caption="latest.cpu_cores ? t('dash.cores', { n: latest.cpu_cores }) : ''"
          />
          <RingGauge
            :label="t('dash.memory')"
            :percent="latest.mem_percent"
            :caption="`${formatBytes(latest.mem_used)} / ${formatBytes(latest.mem_total)}`"
          />
          <RingGauge
            :label="t('dash.load')"
            :percent="latest.load_percent"
            :caption="`${(latest.load_1 || 0).toFixed(2)} · ${(latest.load_5 || 0).toFixed(2)} · ${(latest.load_15 || 0).toFixed(2)}`"
          />
        </div>
      </section>

      <section class="panel-card px-5 py-5">
        <h2 class="mb-4 text-[14px] font-medium">{{ t('dash.disk') }}</h2>
        <p v-if="!(latest.disks || []).length" class="text-[12px] text-ink-muted">
          {{ t('dash.noData') }}
        </p>
        <div v-else class="flex flex-col gap-3.5">
          <div v-for="disk in latest.disks" :key="disk.mountpoint">
            <div class="flex items-baseline justify-between gap-2">
              <span class="truncate text-[12px] font-medium">{{ disk.mountpoint }}</span>
              <span class="tabular text-[12px] text-ink-muted">
                {{ disk.percent.toFixed(0) }}%
              </span>
            </div>
            <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-surface-sunken">
              <div
                class="h-full rounded-full"
                :style="{ width: `${Math.min(disk.percent, 100)}%`, background: statusForPercent(disk.percent) }"
              />
            </div>
            <div class="tabular mt-1 text-[11px] text-ink-muted">
              {{ formatBytes(disk.used) }} / {{ formatBytes(disk.total) }} · {{ disk.fstype }}
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- Overview: eine Karte, vier Spalten — Bestand statt Verbrauch (der
         steht weiter unten gegen das Paket-Limit im Verbrauchs-Panel). -->
    <section class="panel-card mb-4 grid grid-cols-2 lg:grid-cols-4">
      <RouterLink
        v-for="(tile, i) in [
          { to: '/frontend/sites', label: t('dash.sites'), value: counts.sites },
          { to: '/ftp', label: t('nav.ftp'), value: quotaUsed('ftp') },
          { to: '/databases', label: t('nav.databases'), value: quotaUsed('databases') },
          { to: '/frontend/sites', label: t('dash.certs'), value: counts.certs, hint: counts.certs_expiring ? `${counts.certs_expiring} ${t('dash.expiring')}` : '' },
        ]"
        :key="tile.label"
        :to="tile.to"
        class="group flex items-center justify-between gap-2 px-5 py-4 transition-colors hover:opacity-80"
        :style="{ borderLeft: i > 0 ? '1px solid var(--line-hairline)' : 'none' }"
      >
        <div class="min-w-0">
          <div class="text-[12px] text-ink-secondary">{{ tile.label }}</div>
          <div class="mt-1 text-[22px] leading-tight font-semibold">{{ tile.value ?? '—' }}</div>
          <div v-if="tile.hint" class="mt-0.5 text-[11px] text-status-warning">
            {{ tile.hint }}
          </div>
        </div>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
          class="shrink-0 opacity-40 transition-transform group-hover:translate-x-0.5 text-ink-muted" aria-hidden="true">
          <path d="M9 6l6 6-6 6" />
        </svg>
      </RouterLink>
    </section>

    <!-- Verbrauch (schmal) neben Traffic (breit) — dieselbe Aufteilung wie
         oben bei Sys Status/Speicher, nur seitenverkehrt. -->
    <div class="grid gap-4 lg:grid-cols-[320px_1fr]">
      <section v-if="quota" class="panel-card px-5 py-4">
        <header class="mb-3 flex items-baseline justify-between gap-2">
          <h2 class="text-[13px] font-medium">{{ t('quota.title') }}</h2>
        </header>
        <p class="mb-3 text-[11px] text-ink-muted">
          {{ quota.plan_id ? `${t('quota.plan')}: ${quota.plan_name}` : t('quota.noPlan') }}
        </p>
        <div class="flex flex-col gap-3">
          <QuotaBar
            v-for="e in quota.entries"
            :key="e.resource"
            :label="t('quota.' + e.resource)"
            :used="e.used"
            :limit="e.limit"
            :percent="e.percent"
            :bytes="e.bytes"
          />
        </div>
      </section>

      <TrafficChart :series="series" />
    </div>
  </div>
</template>
