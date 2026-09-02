<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

const apps = ref([])
const sites = ref([])
const runtimes = ref([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const showForm = ref(false)
const form = ref({ site_id: null, runtime: 'node', argsText: 'server.js' })

// Die Umgebung wird je App bearbeitet. Die Werte kommen nie zurück — das Panel
// gibt sie nach dem Speichern nicht mehr heraus —, deshalb ist das Feld immer
// leer und was darin steht, ersetzt beim Speichern die ganze Umgebung.
const envDraft = ref({})
const envOpen = ref({})

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

// Nur Proxy-Sites können eine App haben, und nur solche ohne. Bei einer PHP-
// oder Static-Site zeigte der Vhost weiter auf das Verzeichnis, und die App
// liefe für niemanden.
const freieSites = computed(() => {
  const belegt = new Set(apps.value.map((a) => a.site_id))
  return sites.value.filter((s) => s.type === 'proxy' && !belegt.has(s.id))
})

const nodeFehlt = computed(() => {
  const node = runtimes.value.find((r) => r.name === 'node')
  return node && !node.available
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [appList, siteList, runtimeList] = await Promise.all([
      api.get('/apps'),
      api.get('/sites'),
      // Ohne laufenden Agent gibt es keine Auskunft über Laufzeitumgebungen.
      // Die Liste der Apps soll deswegen nicht leer bleiben.
      api.get('/apps/runtimes').catch(() => []),
    ])
    apps.value = appList
    sites.value = siteList
    runtimes.value = runtimeList
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

// Argumente stehen als eine Zeile im Feld, gehen aber einzeln über die
// Leitung. Ein Argument mit Leerzeichen gibt es nicht — systemd zerlegt
// ExecStart selbst, und wer diese Zerlegung nachbaut, vertut sich.
function argsFromText(text) {
  return text.split(/\s+/).filter(Boolean)
}

function envFromText(text) {
  const env = {}
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const i = trimmed.indexOf('=')
    if (i <= 0) continue
    env[trimmed.slice(0, i).trim()] = trimmed.slice(i + 1)
  }
  return env
}

async function createApp() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/apps', {
      site_id: Number(form.value.site_id),
      runtime: form.value.runtime,
      args: argsFromText(form.value.argsText),
    })
    showForm.value = false
    form.value = { site_id: null, runtime: 'node', argsText: 'server.js' }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function saveApp(app, patch) {
  busy.value = true
  error.value = ''
  try {
    await api.patch(`/apps/${app.id}`, {
      runtime: app.runtime,
      args: app.args || [],
      enabled: app.enabled,
      ...patch,
    })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

function saveEnv(app) {
  const text = envDraft.value[app.id] ?? ''
  saveApp(app, { env: envFromText(text) })
  envDraft.value = { ...envDraft.value, [app.id]: '' }
  envOpen.value = { ...envOpen.value, [app.id]: false }
}

async function removeApp(app) {
  if (!confirm(t('apps.confirmDelete', { name: app.domain }))) return
  busy.value = true
  try {
    await api.del(`/apps/${app.id}`)
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
  <section class="fade-in">
    <header class="mb-5 flex items-center justify-between gap-3">
      <div>
        <h1 class="text-[17px] font-semibold tracking-tight">{{ t('apps.title') }}</h1>
        <p class="mt-0.5 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('apps.subtitle') }}
        </p>
      </div>
      <button
        v-if="freieSites.length"
        class="rounded-md border px-3 py-1.5 text-[12px]"
        :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
        @click="showForm = !showForm"
      >
        {{ t('apps.new') }}
      </button>
    </header>

    <p
      v-if="error"
      class="mb-4 rounded-md px-3 py-2 text-[12px]"
      :style="{
        background: 'color-mix(in srgb, var(--status-critical) 12%, var(--surface-card))',
        color: 'var(--ink-primary)',
      }"
    >
      {{ error }}
    </p>

    <!-- Ohne Node lässt sich zwar eine App anlegen, aber sie startet nicht.
         Das gehört vorher gesagt, nicht als Fehler hinterher. -->
    <p
      v-if="nodeFehlt"
      class="mb-4 rounded-md px-3 py-2 text-[12px]"
      :style="{
        background: 'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
        color: 'var(--ink-secondary)',
      }"
    >
      {{ t('apps.noRuntime') }}
    </p>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-3"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="createApp"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('apps.site') }}
        </span>
        <select
          v-model="form.site_id"
          required
          class="w-full rounded-md border px-3 py-2 text-[13px]"
          :style="inputStyle"
        >
          <option v-for="s in freieSites" :key="s.id" :value="s.id">{{ s.domain }}</option>
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('apps.runtime') }}
        </span>
        <select
          v-model="form.runtime"
          class="w-full rounded-md border px-3 py-2 text-[13px]"
          :style="inputStyle"
        >
          <option v-for="r in runtimes" :key="r.name" :value="r.name">
            {{ r.name }}{{ r.version ? ' ' + r.version : '' }}
          </option>
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('apps.args') }}
        </span>
        <input
          v-model="form.argsText"
          placeholder="server.js"
          class="w-full rounded-md border px-3 py-2 text-[13px]"
          :style="inputStyle"
        />
      </label>

      <div class="sm:col-span-3">
        <button
          type="submit"
          :disabled="busy"
          class="rounded-md px-3 py-1.5 text-[12px]"
          :style="{ background: 'var(--series-1)', color: 'var(--surface-page)' }"
        >
          {{ t('common.create') }}
        </button>
      </div>
    </form>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-secondary)' }">
      {{ t('common.loading') }}
    </p>
    <p
      v-else-if="!apps.length"
      class="rounded-lg border p-6 text-center text-[13px]"
      :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
    >
      {{ t('apps.empty') }}
    </p>

    <div v-else class="grid gap-3 lg:grid-cols-2">
      <article
        v-for="app in apps"
        :key="app.id"
        class="rounded-lg border p-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <header class="mb-3 flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span
                class="h-1.5 w-1.5 shrink-0 rounded-full"
                :style="{ background: app.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
                :title="app.active ? t('apps.running') : t('apps.stopped')"
              ></span>
              <span class="truncate text-[14px] font-medium">{{ app.domain }}</span>
            </div>
            <div class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ app.unit }} &middot; 127.0.0.1:{{ app.port }}
            </div>
          </div>
          <span class="shrink-0 text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ app.runtime }} {{ (app.args || []).join(' ') }}
          </span>
        </header>

        <div class="text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('apps.env') }}:
          <template v-if="app.env_keys && app.env_keys.length">
            {{ app.env_keys.join(', ') }}
          </template>
          <template v-else>{{ t('apps.envNone') }}</template>
        </div>

        <!--
          Die Werte stehen hier nicht. Das Panel gibt sie nach dem Speichern
          nicht mehr heraus — in einer App-Umgebung stehen regelmäßig
          Datenbankpasswörter. Was hier eingetragen wird, ersetzt die ganze
          Umgebung; das steht auch daneben, sonst löscht jemand versehentlich
          die Hälfte.
        -->
        <div v-if="envOpen[app.id]" class="mt-2">
          <textarea
            :value="envDraft[app.id] ?? ''"
            rows="4"
            placeholder="DATABASE_URL=postgres://…&#10;API_TOKEN=…"
            class="w-full rounded-md border px-3 py-2 font-mono text-[12px]"
            :style="inputStyle"
            @input="envDraft[app.id] = $event.target.value"
          ></textarea>
          <p class="mt-1 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ t('apps.envReplaces') }}
          </p>
          <button
            class="mt-1 rounded-md border px-2 py-1 text-[11px]"
            :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
            :disabled="busy"
            @click="saveEnv(app)"
          >
            {{ t('common.save') }}
          </button>
        </div>

        <footer class="mt-3 flex gap-3 text-[11px]">
          <button
            class="underline"
            :style="{ color: 'var(--ink-secondary)' }"
            :disabled="busy"
            @click="envOpen[app.id] = !envOpen[app.id]"
          >
            {{ t('apps.editEnv') }}
          </button>
          <button
            class="underline"
            :style="{ color: 'var(--ink-secondary)' }"
            :disabled="busy"
            @click="saveApp(app, { enabled: !app.enabled })"
          >
            {{ app.enabled ? t('apps.disable') : t('apps.enable') }}
          </button>
          <button
            class="underline"
            :style="{ color: 'var(--status-critical)' }"
            :disabled="busy"
            @click="removeApp(app)"
          >
            {{ t('common.delete') }}
          </button>
        </footer>
      </article>
    </div>
  </section>
</template>
