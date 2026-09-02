<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import InstallHint from '../components/InstallHint.vue'

const apps = ref([])
const sites = ref([])
const runtimes = ref([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const showForm = ref(false)
const form = ref({
  site_id: null,
  kind: 'native',
  runtime: 'node',
  argsText: 'server.js',
  image: '',
  container_port: 8080,
  memory_mb: 0,
  cpus: '',
})
// Der Zustand von Docker. Nur Administratoren bekommen ihn — für alle anderen
// bleibt er null, und die Warnung darüber steht dann eben nicht da.
const docker = ref(null)
const logs = ref({})
// Die installierten Node-Fassungen. Sie liegen systemweit; installieren und
// entfernen darf sie nur ein Administrator, ansehen jeder.
const nodes = ref([])
const stats = ref([])
const images = ref([])
const showImages = ref(false)
const imageBusy = ref(false)
const nodeWunsch = ref('')
const nodeBusy = ref(false)
const showNodes = ref(false)

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
  const eigeneNode =
    nodes.value.length > 0 ||
    runtimes.value.some((r) => /^node[0-9]+$/.test(r.name) && r.available)
  return node && !node.available && !eigeneNode
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [appList, siteList, runtimeList, dockerState, nodeList, statList, imageList] =
      await Promise.all([
        api.get('/apps'),
        api.get('/sites'),
        // Ohne laufenden Agent gibt es keine Auskunft über Laufzeitumgebungen.
        // Die Liste der Apps soll deswegen nicht leer bleiben.
        api.get('/apps/runtimes').catch(() => []),
        // Scheitert für alle außer Administratoren an der Rolle, und das ist in
        // Ordnung.
        api.get('/apps/docker').catch(() => null),
        api.get('/apps/node').catch(() => []),
        // Auslastung und Images sind Zugaben: ohne Docker auf dem Server
        // bleiben sie leer, und die Übersicht steht trotzdem.
        api.get('/apps/stats').catch(() => []),
        api.get('/apps/images').catch(() => []),
      ])
    apps.value = appList
    sites.value = siteList
    runtimes.value = runtimeList
    docker.value = dockerState
    nodes.value = nodeList
    stats.value = statList
    images.value = imageList
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
    await api.post('/apps', appPayload())
    showForm.value = false
    form.value = {
      site_id: null, kind: 'native', runtime: 'node', argsText: 'server.js',
      image: '', container_port: 8080, memory_mb: 0, cpus: '',
    }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

// appPayload baut, was der Server erwartet — je nach Art andere Felder.
// Die Felder der jeweils anderen Art gehen mit, aber leer: der Server
// entscheidet an "kind", welche er ansieht.
function appPayload() {
  const f = form.value
  return {
    site_id: Number(f.site_id),
    kind: f.kind,
    runtime: f.runtime,
    args: argsFromText(f.argsText),
    image: f.image,
    container_port: Number(f.container_port) || 0,
    memory_mb: Number(f.memory_mb) || 0,
    cpus: f.cpus,
  }
}

async function saveApp(app, patch) {
  busy.value = true
  error.value = ''
  try {
    await api.patch(`/apps/${app.id}`, {
      kind: app.kind,
      runtime: app.runtime,
      args: app.args || [],
      image: app.image,
      volumes: app.volumes || [],
      memory_mb: app.memory_mb,
      cpus: app.cpus,
      container_port: app.container_port,
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

async function nodeInstallieren() {
  nodeBusy.value = true
  error.value = ''
  try {
    await api.post('/apps/node', { version: nodeWunsch.value.trim() })
    nodeWunsch.value = ''
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    nodeBusy.value = false
  }
}

async function nodeEntfernen(v) {
  if (!confirm(t('apps.nodeConfirmDelete', { v: v.version || v.major }))) return
  nodeBusy.value = true
  error.value = ''
  try {
    await api.del(`/apps/node/${v.major}`)
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    nodeBusy.value = false
  }
}

// statFor sucht die Auslastung zu einer App.
//
// Über die App-Kennung, nicht über den Namen: der Name des Containers entsteht
// zwar aus dem der App, aber der Server hat ihn schon zugeordnet — das hier
// noch einmal nachzubauen hieße, dieselbe Frage zweimal zu beantworten.
function statFor(app) {
  return stats.value.find((s) => s.app_id === app.id)
}

// Bytes in etwas, das man lesen kann. Zweierpotenzen, wie `docker stats` sie
// meint.
function bytes(n) {
  if (!n) return '0 B'
  const einheiten = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let v = n
  while (v >= 1024 && i < einheiten.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${einheiten[i]}`
}

function imagesGesamt() {
  return images.value.reduce((n, i) => n + (i.size || 0), 0)
}

async function imageEntfernen(img) {
  if (!confirm(t('apps.imagesConfirmDelete', { ref: img.ref }))) return
  imageBusy.value = true
  error.value = ''
  try {
    await api.post('/apps/images/remove', { ref: img.ref })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    imageBusy.value = false
  }
}

async function logsLaden(app) {
  if (logs.value[app.id] !== undefined) {
    logs.value = { ...logs.value, [app.id]: undefined }
    return
  }
  try {
    const res = await api.get(`/apps/${app.id}/logs?lines=200`)
    logs.value = { ...logs.value, [app.id]: res.log || t('apps.logsEmpty') }
  } catch (err) {
    error.value = err.message
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

    <!--
      Die Trennung, auf die es bei Containern ankommt, ist eine Einstellung des
      Docker-Daemons. Sie lässt sich nicht je Container nachholen, deshalb steht
      hier ein Hinweis und keine Schaltfläche.
    -->
    <!-- Fehlt Docker ganz, gehört der Knopf daneben; die übrigen Hinweise
         sind Einstellungen des Daemons und lassen sich nicht nachinstallieren. -->
    <InstallHint
      v-if="docker && !docker.available"
      feature="docker"
      :text="t('apps.needDocker')"
      @installed="load"
    />
    <p
      v-for="w in (docker && docker.available && docker.warnings) || []"
      :key="w"
      class="mb-4 rounded-md px-3 py-2 text-[12px]"
      :style="{
        background: 'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
        color: 'var(--ink-secondary)',
      }"
    >
      {{ w }}
    </p>

    <!--
      Node-Fassungen liegen systemweit unter /opt/volt/node. Mehrere
      nebeneinander, damit eine alte Anwendung weiterläuft, während eine neue
      schon auf der nächsten baut.
    -->
    <div
      class="mb-5 rounded-lg border p-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <button
        class="flex w-full items-center justify-between text-[12px]"
        :style="{ color: 'var(--ink-secondary)' }"
        @click="showNodes = !showNodes"
      >
        <span>
          {{ t('apps.nodeVersions') }}
          <template v-if="nodes.length">
            — {{ nodes.map((n) => 'node' + n.major).join(', ') }}
          </template>
          <template v-else>— {{ t('apps.nodeNone') }}</template>
        </span>
        <span aria-hidden="true">{{ showNodes ? '\u2212' : '+' }}</span>
      </button>

      <div v-if="showNodes" class="mt-3 space-y-2">
        <div
          v-for="n in nodes"
          :key="n.major"
          class="flex items-center gap-3 text-[12px]"
        >
          <code class="font-mono">node{{ n.major }}</code>
          <span :style="{ color: 'var(--ink-muted)' }">{{ n.version }}</span>
          <button
            class="underline"
            :style="{ color: 'var(--status-critical)' }"
            :disabled="nodeBusy"
            @click="nodeEntfernen(n)"
          >
            {{ t('common.delete') }}
          </button>
        </div>

        <div class="flex gap-2">
          <input
            v-model="nodeWunsch"
            placeholder="22.12.0"
            class="w-40 rounded-md border px-2 py-1 font-mono text-[12px]"
            :style="inputStyle"
          />
          <button
            class="rounded-md border px-2 py-1 text-[12px]"
            :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
            :disabled="nodeBusy || !nodeWunsch.trim()"
            @click="nodeInstallieren"
          >
            {{ nodeBusy ? t('apps.nodeInstalling') : t('apps.nodeInstall') }}
          </button>
        </div>
        <p class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('apps.nodeHint') }}
        </p>
      </div>
    </div>

    <!--
      Images liegen einmal auf der Platte und gehören keinem Mandanten. Die
      Liste ist deshalb Administratoren vorbehalten; für alle anderen scheitert
      der Aufruf an der Rolle, und die Klappe erscheint gar nicht.
    -->
    <div
      v-if="images.length"
      class="mb-5 rounded-lg border p-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <button
        class="flex w-full items-center justify-between text-[12px]"
        :style="{ color: 'var(--ink-secondary)' }"
        @click="showImages = !showImages"
      >
        <span>{{ t('apps.images') }} — {{ images.length }}, {{ bytes(imagesGesamt()) }}</span>
        <span aria-hidden="true">{{ showImages ? '\u2212' : '+' }}</span>
      </button>

      <div v-if="showImages" class="mt-3 space-y-2">
        <div
          v-for="img in images"
          :key="img.id"
          class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]"
        >
          <code class="font-mono">{{ img.dangling ? img.id : img.ref }}</code>
          <span :style="{ color: 'var(--ink-muted)' }">{{ bytes(img.size) }}</span>
          <span v-if="img.dangling" :style="{ color: 'var(--ink-muted)' }">
            {{ t('apps.imagesDangling') }}
          </span>
          <span v-if="img.used_by && img.used_by.length" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('apps.imagesUsedBy') }} {{ img.used_by.join(', ') }}
          </span>
          <template v-else>
            <span :style="{ color: 'var(--ink-muted)' }">{{ t('apps.imagesUnused') }}</span>
            <button
              class="underline"
              :style="{ color: 'var(--status-critical)' }"
              :disabled="imageBusy"
              @click="imageEntfernen(img)"
            >
              {{ t('common.delete') }}
            </button>
          </template>
        </div>
        <p class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('apps.imagesHint') }}
        </p>
      </div>
    </div>

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
          {{ t('apps.kind') }}
        </span>
        <select v-model="form.kind" class="w-full rounded-md border px-3 py-2 text-[13px]"
                :style="inputStyle">
          <option value="native">{{ t('apps.kindNative') }}</option>
          <option value="docker">{{ t('apps.kindDocker') }}</option>
        </select>
      </label>

      <template v-if="form.kind === 'native'">
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
      </template>

      <template v-else>
        <label class="block sm:col-span-2">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('apps.image') }}
          </span>
          <input v-model="form.image" required placeholder="nginx:1.27-alpine"
                 class="w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                 :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('apps.containerPort') }}
          </span>
          <input v-model.number="form.container_port" type="number" min="1" max="65535"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('apps.memory') }}
          </span>
          <input v-model.number="form.memory_mb" type="number" min="0"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('apps.cpus') }}
          </span>
          <input v-model="form.cpus" placeholder="0.5"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <p class="text-[11px] sm:col-span-3" :style="{ color: 'var(--ink-muted)' }">
          {{ t('apps.containerNote') }}
        </p>
      </template>

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
          <span class="shrink-0 font-mono text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
            <template v-if="app.kind === 'docker'">{{ app.image }}</template>
            <template v-else>{{ app.runtime }} {{ (app.args || []).join(' ') }}</template>
          </span>
        </header>

        <!--
          Die Auslastung steht nur da, wenn wirklich etwas läuft. Ein Balken
          mit Null darin sähe aus wie eine Messung und wäre keine.
        -->
        <div v-if="statFor(app)" class="mb-3">
          <div class="flex items-baseline justify-between text-[11px]">
            <span :style="{ color: 'var(--ink-secondary)' }">{{ t('apps.usage') }}</span>
            <span class="font-mono" :style="{ color: 'var(--ink-secondary)' }">
              {{ statFor(app).cpu_perc.toFixed(1) }}% CPU &middot;
              {{ bytes(statFor(app).mem_used) }}
              <template v-if="statFor(app).mem_max">
                / {{ bytes(statFor(app).mem_max) }}
              </template>
            </span>
          </div>
          <div
            class="mt-1 h-1 w-full overflow-hidden rounded-full"
            :style="{ background: 'var(--surface-sunken)' }"
            role="img"
            :aria-label="statFor(app).mem_perc.toFixed(0) + '%'"
          >
            <div
              class="h-full rounded-full"
              :style="{
                width: Math.min(100, statFor(app).mem_perc) + '%',
                background:
                  statFor(app).mem_perc > 90 ? 'var(--status-critical)' : 'var(--series-1)',
              }"
            ></div>
          </div>
        </div>

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

        <pre
          v-if="logs[app.id] !== undefined"
          class="mt-2 max-h-64 overflow-auto rounded-md p-2 font-mono text-[11px]"
          :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
        >{{ logs[app.id] }}</pre>

        <footer class="mt-3 flex flex-wrap gap-3 text-[11px]">
          <button
            class="underline"
            :style="{ color: 'var(--ink-secondary)' }"
            :disabled="busy"
            @click="envOpen[app.id] = !envOpen[app.id]"
          >
            {{ t('apps.editEnv') }}
          </button>
          <button
            v-if="app.kind === 'docker'"
            class="underline"
            :style="{ color: 'var(--ink-secondary)' }"
            @click="logsLaden(app)"
          >
            {{ t('apps.logs') }}
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
