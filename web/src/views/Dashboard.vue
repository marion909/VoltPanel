<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes, formatUptime } from '../format'
import RingGauge from '../components/RingGauge.vue'
import StatTile from '../components/StatTile.vue'
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
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex flex-wrap items-baseline justify-between gap-2">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('nav.dashboard') }}</h1>
      <p v-if="system.hostname" class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
        {{ system.hostname }} · {{ system.platform }} · {{ system.arch }}
      </p>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }">
      {{ t('common.error') }}: {{ error }}
    </p>

    <section
      class="mb-4 grid grid-cols-2 gap-4 rounded-lg border px-4 py-5 sm:grid-cols-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <RingGauge
        :label="t('dash.cpu')"
        :percent="latest.cpu_percent"
        :caption="latest.cpu_cores ? `${latest.cpu_cores} Kerne` : ''"
      />
      <RingGauge
        :label="t('dash.memory')"
        :percent="latest.mem_percent"
        :caption="`${formatBytes(latest.mem_used)} / ${formatBytes(latest.mem_total)}`"
      />
      <RingGauge
        :label="t('dash.disk')"
        :percent="rootDisk?.percent || 0"
        :caption="rootDisk ? `${formatBytes(rootDisk.used)} / ${formatBytes(rootDisk.total)}` : ''"
      />
      <RingGauge
        :label="t('dash.load')"
        :percent="latest.load_percent"
        :caption="`${(latest.load_1 || 0).toFixed(2)} · ${(latest.load_5 || 0).toFixed(2)} · ${(latest.load_15 || 0).toFixed(2)}`"
      />
    </section>

    <div class="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
      <StatTile :label="t('dash.sites')" :value="counts.sites ?? '—'" />
      <StatTile
        :label="t('dash.certs')"
        :value="counts.certs ?? '—'"
        :hint="counts.certs_expiring ? `${counts.certs_expiring} ${t('dash.expiring')}` : ''"
        :tone="counts.certs_expiring ? 'warning' : ''"
      />
      <StatTile :label="t('dash.uptime')" :value="formatUptime(latest.uptime)" />
      <StatTile :label="t('dash.processes')" :value="latest.process_count ?? '—'" />
    </div>

    <TrafficChart :series="series" />

    <!-- Verbrauch des eigenen Mandanten gegen sein Paket -->
    <section
      v-if="quota"
      class="mt-4 rounded-lg border px-4 py-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <header class="mb-3 flex items-baseline justify-between gap-2">
        <h2 class="text-[13px] font-medium">{{ t('quota.title') }}</h2>
        <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ quota.plan_id ? `${t('quota.plan')}: ${quota.plan_name}` : t('quota.noPlan') }}
        </span>
      </header>

      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
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

    <section v-if="(latest.disks || []).length" class="mt-4">
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <div
          v-for="disk in latest.disks"
          :key="disk.mountpoint"
          class="rounded-lg border px-4 py-3"
          :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        >
          <div class="flex items-baseline justify-between gap-2">
            <span class="truncate text-[13px] font-medium">{{ disk.mountpoint }}</span>
            <span class="tabular text-[12px]" :style="{ color: 'var(--ink-muted)' }">
              {{ disk.percent.toFixed(0) }}%
            </span>
          </div>
          <div
            class="mt-2 h-1.5 overflow-hidden rounded-full"
            :style="{ background: 'var(--surface-sunken)' }"
          >
            <div
              class="h-full rounded-full"
              :style="{
                width: `${Math.min(disk.percent, 100)}%`,
                background:
                  disk.percent >= 90
                    ? 'var(--status-critical)'
                    : disk.percent >= 75
                      ? 'var(--status-warning)'
                      : 'var(--status-good)',
              }"
            />
          </div>
          <div class="tabular mt-1.5 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ formatBytes(disk.used) }} / {{ formatBytes(disk.total) }} · {{ disk.fstype }}
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
