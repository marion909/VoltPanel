<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes } from '../format'

const processes = ref([])
const loading = ref(true)
const error = ref('')
const filter = ref('')
const busy = ref(null)

let timer = null

// Nur Prozesse einer Site lassen sich beenden. Für alles andere ist die
// Dienstverwaltung zuständig — ein per Signal abgeräumter nginx hinterlässt
// eine Unit, die nicht mehr weiß, was sie tun soll.
const canStop = (proc) => proc.user.startsWith('site_')

const visible = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  if (!needle) return processes.value
  return processes.value.filter(
    (p) =>
      p.command.toLowerCase().includes(needle) ||
      p.user.toLowerCase().includes(needle) ||
      String(p.pid) === needle,
  )
})

async function load() {
  try {
    processes.value = await api.get('/system/processes')
    error.value = ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function stop(proc, signal) {
  if (!confirm(t('proc.confirmStop', { pid: proc.pid, command: proc.command.slice(0, 60) }))) return
  busy.value = proc.pid
  try {
    await api.post('/system/processes/stop', { pid: proc.pid, signal })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = null
  }
}

onMounted(() => {
  load()
  // Alle zehn Sekunden: oft genug, um einen Ausreißer zu sehen, selten genug,
  // dass das Lesen von /proc nicht selbst auffällt.
  timer = setInterval(load, 10000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-[14px] font-medium">{{ t('proc.title') }}</h2>
        <p class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('proc.hint') }}</p>
      </div>
      <input
        v-model="filter"
        :placeholder="t('proc.filter')"
        class="w-56 rounded-md border px-3 py-1.5 text-[12px]"
        :style="{
          borderColor: 'var(--line-axis)',
          background: 'var(--surface-page)',
          color: 'var(--ink-primary)',
        }"
      />
    </div>

    <p v-if="error" class="mb-3 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <div
      v-else-if="visible.length"
      class="overflow-x-auto rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[12px]">
        <thead class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-3 py-2 text-right font-normal">PID</th>
            <th class="px-3 py-2 font-normal">{{ t('proc.user') }}</th>
            <th class="px-3 py-2 text-right font-normal">CPU</th>
            <th class="px-3 py-2 text-right font-normal">{{ t('proc.memory') }}</th>
            <th class="px-3 py-2 font-normal">{{ t('proc.command') }}</th>
            <th class="px-3 py-2 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="proc in visible"
            :key="proc.pid"
            class="border-b last:border-0"
            :style="{ borderColor: 'var(--line-hairline)' }"
          >
            <td class="tabular px-3 py-1.5 text-right">{{ proc.pid }}</td>
            <td class="px-3 py-1.5" :style="{ color: 'var(--ink-secondary)' }">{{ proc.user }}</td>
            <td class="tabular px-3 py-1.5 text-right">{{ proc.cpu_percent.toFixed(1) }}&thinsp;%</td>
            <td class="tabular px-3 py-1.5 text-right">{{ formatBytes(proc.mem_bytes) }}</td>
            <td class="max-w-md truncate px-3 py-1.5 font-mono" :title="proc.command">
              {{ proc.command }}
            </td>
            <td class="px-3 py-1.5 text-right whitespace-nowrap">
              <template v-if="canStop(proc)">
                <button
                  class="underline disabled:opacity-50"
                  :style="{ color: 'var(--ink-secondary)' }"
                  :disabled="busy === proc.pid"
                  @click="stop(proc, 'TERM')"
                >
                  {{ t('proc.stop') }}
                </button>
                <button
                  class="ml-3 underline disabled:opacity-50"
                  :style="{ color: 'var(--status-critical)' }"
                  :disabled="busy === proc.pid"
                  @click="stop(proc, 'KILL')"
                >
                  {{ t('proc.kill') }}
                </button>
              </template>
              <span v-else :style="{ color: 'var(--ink-muted)' }">—</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('proc.empty') }}</p>
  </div>
</template>
