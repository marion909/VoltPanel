<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatDateTime } from '../format'
import { askConfirm } from '../stores/confirm'
import InstallHint from '../components/InstallHint.vue'
import SkeletonRows from '../components/SkeletonRows.vue'

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

// Image/Port/Speicher/CPUs (bzw. Runtime/Argumente) ließen sich bisher nur
// beim Anlegen setzen — danach half nur Löschen und neu Anlegen. Der Entwurf
// startet mit den aktuellen Werten der App, nicht leer.
const editOpen = ref({})
const editDraft = ref({})

function startEdit(app) {
  editDraft.value = {
    ...editDraft.value,
    [app.id]: {
      image: app.image || '',
      container_port: app.container_port || 8080,
      memory_mb: app.memory_mb || 0,
      cpus: app.cpus || '',
      runtime: app.runtime || 'node',
      argsText: (app.args || []).join(' '),
    },
  }
  editOpen.value = { ...editOpen.value, [app.id]: !editOpen.value[app.id] }
}

async function saveAppEdit(app) {
  const d = editDraft.value[app.id]
  if (!d) return
  await saveApp(app, {
    image: d.image,
    container_port: Number(d.container_port) || 0,
    memory_mb: Number(d.memory_mb) || 0,
    cpus: d.cpus,
    runtime: d.runtime,
    args: argsFromText(d.argsText),
  })
  editOpen.value = { ...editOpen.value, [app.id]: false }
}

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
  if (!(await askConfirm(t('apps.confirmDelete', { name: app.domain })))) return
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
  if (!(await askConfirm(t('apps.nodeConfirmDelete', { v: v.version || v.major })))) return
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
  if (!(await askConfirm(t('apps.imagesConfirmDelete', { ref: img.ref })))) return
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
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('apps.title') }}</h1>
      <button
        v-if="freieSites.length"
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white bg-accent"
        @click="showForm = !showForm"
      >
        {{ t('apps.new') }}
      </button>
    </header>

    <p
      v-if="error"
      class="mb-4 text-[13px] text-status-critical"
      role="alert"
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
      v-if="docker && !docker.installed"
      feature="docker"
      :text="docker.warnings?.[0] || t('apps.needDocker')"
      @installed="load"
    />
    <p
      v-for="w in (docker && docker.installed && docker.warnings) || []"
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
    <div class="panel-card mb-5 p-4">
      <button
        class="flex w-full items-center justify-between text-[12px] text-ink-secondary"
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
            class="underline text-status-critical"
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
        <p class="text-[11px] text-ink-muted">
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
      class="panel-card mb-5 p-4"
    >
      <button
        class="flex w-full items-center justify-between text-[12px] text-ink-secondary"
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
              class="underline text-status-critical"
              :disabled="imageBusy"
              @click="imageEntfernen(img)"
            >
              {{ t('common.delete') }}
            </button>
          </template>
        </div>
        <p class="text-[11px] text-ink-muted">
          {{ t('apps.imagesHint') }}
        </p>
      </div>
    </div>

    <form
      v-if="showForm"
      class="panel-card mb-5 grid gap-3 p-4 sm:grid-cols-3"
      @submit.prevent="createApp"
    >
      <label class="block">
        <span class="mb-1 block text-[12px] text-ink-secondary">
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
        <span class="mb-1 block text-[12px] text-ink-secondary">
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
          <span class="mb-1 block text-[12px] text-ink-secondary">
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
          <span class="mb-1 block text-[12px] text-ink-secondary">
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
          <span class="mb-1 block text-[12px] text-ink-secondary">
            {{ t('apps.image') }}
          </span>
          <input v-model="form.image" required placeholder="nginx:1.27-alpine"
                 class="w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                 :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px] text-ink-secondary">
            {{ t('apps.containerPort') }}
          </span>
          <input v-model.number="form.container_port" type="number" min="1" max="65535"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px] text-ink-secondary">
            {{ t('apps.memory') }}
          </span>
          <input v-model.number="form.memory_mb" type="number" min="0"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <label class="block">
          <span class="mb-1 block text-[12px] text-ink-secondary">
            {{ t('apps.cpus') }}
          </span>
          <input v-model="form.cpus" placeholder="0.5"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <p class="text-[11px] sm:col-span-3 text-ink-muted">
          {{ t('apps.containerNote') }}
        </p>
      </template>

      <div class="sm:col-span-3">
        <button
          type="submit"
          :disabled="busy"
          class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent"
        >
          {{ busy ? t('common.loading') : t('common.create') }}
        </button>
      </div>
    </form>

    <SkeletonRows v-if="loading" :cols="6" />
    <p
      v-else-if="!apps.length"
      class="text-[13px] text-ink-muted"
    >
      {{ t('apps.empty') }}
    </p>

    <div v-else class="panel-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-[13px]">
          <thead class="text-[12px] text-ink-muted">
            <tr class="border-b border-line-hairline">
              <th class="px-4 py-2.5 font-normal">{{ t('apps.name') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('common.status') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('apps.image') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('apps.port') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('apps.usage') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('backup.date') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="app in apps" :key="app.id">
              <tr class="border-b last:border-0 border-line-hairline">
                <td class="px-4 py-2.5">
                  <div class="font-medium">{{ app.domain }}</div>
                  <div class="text-[11px] text-ink-muted">{{ app.unit }}</div>
                </td>
                <td class="px-4 py-2.5">
                  <span class="inline-flex items-center gap-1.5" :style="{ color: app.active ? 'var(--status-good)' : 'var(--ink-muted)' }">
                    <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: app.active ? 'var(--status-good)' : 'var(--line-axis)' }"></span>
                    {{ app.active ? t('apps.running') : t('apps.stopped') }}
                  </span>
                </td>
                <td class="max-w-[220px] truncate px-4 py-2.5 font-mono text-[12px] text-ink-secondary">
                  <template v-if="app.kind === 'docker'">{{ app.image }}</template>
                  <template v-else>{{ app.runtime }} {{ (app.args || []).join(' ') }}</template>
                </td>
                <td class="px-4 py-2.5">
                  <span
                    class="whitespace-nowrap rounded-full px-2 py-0.5 font-mono text-[11px]"
                    :style="{ background: 'color-mix(in srgb, var(--status-good) 16%, transparent)', color: 'var(--status-good)' }"
                  >
                    127.0.0.1:{{ app.port }}<template v-if="app.kind === 'docker'"> &#8594; {{ app.container_port }}</template>
                  </span>
                </td>
                <td class="px-4 py-2.5 text-[12px] text-ink-secondary">
                  <template v-if="statFor(app)">
                    {{ statFor(app).cpu_perc.toFixed(1) }}% CPU &middot; {{ bytes(statFor(app).mem_used) }}
                  </template>
                  <span v-else class="text-ink-muted">&mdash;</span>
                </td>
                <td class="px-4 py-2.5 text-[12px] text-ink-secondary">
                  {{ formatDateTime(app.created_at) }}
                </td>
                <td class="px-4 py-2.5 text-right text-[12px]">
                  <button class="underline text-ink-secondary" :disabled="busy" @click="startEdit(app)">
                    {{ t('common.edit') }}
                  </button>
                  <button class="ml-3 underline text-ink-secondary" :disabled="busy" @click="envOpen[app.id] = !envOpen[app.id]">
                    {{ t('apps.editEnv') }}
                  </button>
                  <button v-if="app.kind === 'docker'" class="ml-3 underline text-ink-secondary" @click="logsLaden(app)">
                    {{ t('apps.logs') }}
                  </button>
                  <button class="ml-3 underline text-ink-secondary" :disabled="busy" @click="saveApp(app, { enabled: !app.enabled })">
                    {{ app.enabled ? t('apps.disable') : t('apps.enable') }}
                  </button>
                  <button class="ml-3 underline text-status-critical" :disabled="busy" @click="removeApp(app)">
                    {{ t('common.delete') }}
                  </button>
                </td>
              </tr>
              <tr v-if="editOpen[app.id] || envOpen[app.id] || logs[app.id] !== undefined" class="border-b last:border-0 border-line-hairline">
                <td colspan="7" class="px-4 py-3" :style="{ background: 'var(--surface-sunken)' }">
                  <div v-if="editOpen[app.id]" class="mb-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    <template v-if="app.kind === 'docker'">
                      <label class="block sm:col-span-2">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.image') }}</span>
                        <input v-model="editDraft[app.id].image" class="w-full rounded-md border px-3 py-2 font-mono text-[12px]" :style="inputStyle" />
                      </label>
                      <label class="block">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.containerPort') }}</span>
                        <input v-model.number="editDraft[app.id].container_port" type="number" min="1" max="65535" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
                      </label>
                      <label class="block">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.memory') }}</span>
                        <input v-model.number="editDraft[app.id].memory_mb" type="number" min="0" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
                      </label>
                      <label class="block">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.cpus') }}</span>
                        <input v-model="editDraft[app.id].cpus" placeholder="0.5" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
                      </label>
                    </template>
                    <template v-else>
                      <label class="block">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.runtime') }}</span>
                        <select v-model="editDraft[app.id].runtime" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
                          <option v-for="r in runtimes" :key="r.name" :value="r.name">{{ r.name }}{{ r.version ? ' ' + r.version : '' }}</option>
                        </select>
                      </label>
                      <label class="block sm:col-span-2">
                        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('apps.args') }}</span>
                        <input v-model="editDraft[app.id].argsText" placeholder="server.js" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
                      </label>
                    </template>
                    <div class="flex items-end gap-2">
                      <button class="rounded-md px-3 py-2 text-[12px] font-medium text-white disabled:opacity-60 bg-accent" :disabled="busy" @click="saveAppEdit(app)">
                        {{ t('common.save') }}
                      </button>
                      <button class="rounded-md border px-3 py-2 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="editOpen[app.id] = false">
                        {{ t('common.cancel') }}
                      </button>
                    </div>
                  </div>
                  <!--
                    Die Werte stehen hier nicht. Das Panel gibt sie nach dem
                    Speichern nicht mehr heraus — in einer App-Umgebung stehen
                    regelmäßig Datenbankpasswörter. Was hier eingetragen wird,
                    ersetzt die ganze Umgebung; das steht auch daneben, sonst
                    löscht jemand versehentlich die Hälfte.
                  -->
                  <div v-if="envOpen[app.id]" class="mb-3">
                    <div class="mb-1 text-[11px] text-ink-secondary">
                      {{ t('apps.env') }}:
                      <template v-if="app.env_keys && app.env_keys.length">{{ app.env_keys.join(', ') }}</template>
                      <template v-else>{{ t('apps.envNone') }}</template>
                    </div>
                    <textarea
                      :value="envDraft[app.id] ?? ''"
                      rows="4"
                      placeholder="DATABASE_URL=postgres://…&#10;API_TOKEN=…"
                      class="w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                      :style="inputStyle"
                      @input="envDraft[app.id] = $event.target.value"
                    ></textarea>
                    <p class="mt-1 text-[11px] text-ink-muted">{{ t('apps.envReplaces') }}</p>
                    <button
                      class="mt-1 rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60 bg-accent"
                      :disabled="busy" @click="saveEnv(app)"
                    >
                      {{ t('common.save') }}
                    </button>
                  </div>
                  <pre
                    v-if="logs[app.id] !== undefined"
                    class="max-h-64 overflow-auto rounded-md p-2 font-mono text-[11px]"
                    :style="{ background: 'var(--surface-page)', color: 'var(--ink-secondary)' }"
                  >{{ logs[app.id] }}</pre>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
