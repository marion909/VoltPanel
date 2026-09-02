<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

const deploys = ref([])
const sites = ref([])
const steps = ref([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const showForm = ref(false)
const form = ref({ site_id: null, repo_url: '', ref: 'main', steps: [], auto_deploy: true })

// Das Geheimnis für die Signatur wird genau einmal angezeigt. Es liegt
// verschlüsselt, und es erneut herauszugeben hieße, ein Geheimnis aus der
// Datenbank zu holen, das dort für den Server liegt und nicht für den
// Betrachter.
const neuesGeheimnis = ref('')

const offen = ref({})
const releases = ref({})
const keys = ref({})

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const freieSites = computed(() => {
  const belegt = new Set(deploys.value.map((d) => d.site_id))
  return sites.value.filter((s) => !belegt.has(s.id))
})

const laeuftEiner = computed(() => deploys.value.some((d) => d.last_status === 'running'))
let ticker = null

async function load() {
  error.value = ''
  try {
    const [deployList, siteList, stepList] = await Promise.all([
      api.get('/deploys'),
      api.get('/sites'),
      api.get('/deploys/steps').catch(() => []),
    ])
    deploys.value = deployList
    sites.value = siteList
    steps.value = stepList
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

// Solange einer läuft, nachsehen: der Deploy läuft im Hintergrund, und ohne
// das bliebe die Anzeige auf "läuft" stehen, bis jemand neu lädt.
function starteTicker() {
  if (ticker) return
  ticker = setInterval(() => {
    if (laeuftEiner.value) load()
    else stoppeTicker()
  }, 3000)
}
function stoppeTicker() {
  if (ticker) clearInterval(ticker)
  ticker = null
}

async function speichern() {
  busy.value = true
  error.value = ''
  neuesGeheimnis.value = ''
  try {
    const res = await api.post('/deploys', {
      site_id: Number(form.value.site_id),
      repo_url: form.value.repo_url,
      ref: form.value.ref,
      steps: form.value.steps,
      auto_deploy: form.value.auto_deploy,
    })
    if (res.hook_secret) neuesGeheimnis.value = res.hook_secret
    showForm.value = false
    form.value = { site_id: null, repo_url: '', ref: 'main', steps: [], auto_deploy: true }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function starten(d) {
  busy.value = true
  error.value = ''
  try {
    await api.post(`/deploys/${d.id}/run`, {})
    await load()
    starteTicker()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function staendeLaden(d) {
  offen.value = { ...offen.value, [d.id]: !offen.value[d.id] }
  if (!offen.value[d.id]) return
  try {
    const [rel, key] = await Promise.all([
      api.get(`/deploys/${d.id}/releases`),
      api.get(`/deploys/${d.id}/key`).catch(() => null),
    ])
    releases.value = { ...releases.value, [d.id]: rel }
    if (key) keys.value = { ...keys.value, [d.id]: key.public_key }
  } catch (err) {
    error.value = err.message
  }
}

async function zurueck(d, release) {
  if (!confirm(t('deploy.confirmRollback', { r: release }))) return
  busy.value = true
  try {
    await api.post(`/deploys/${d.id}/rollback`, { release })
    await load()
    await staendeLaden(d)
    await staendeLaden(d)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function entfernen(d) {
  if (!confirm(t('deploy.confirmDelete', { name: d.domain }))) return
  busy.value = true
  try {
    await api.del(`/deploys/${d.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

function statusFarbe(status) {
  if (status === 'ok') return 'var(--status-good)'
  if (status === 'failed') return 'var(--status-critical)'
  if (status === 'running') return 'var(--status-warning)'
  return 'var(--ink-muted)'
}

onMounted(async () => {
  await load()
  if (laeuftEiner.value) starteTicker()
})
onUnmounted(stoppeTicker)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('deploy.title') }}</h1>
      <button
        v-if="freieSites.length"
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="showForm = !showForm"
      >
        {{ t('deploy.new') }}
      </button>
    </header>

    <p
      v-if="error"
      class="mb-4 text-[13px]"
      :style="{ color: 'var(--status-critical)' }"
      role="alert"
    >
      {{ error }}
    </p>

    <!--
      Einmal und nie wieder. Wer es verliert, bekommt kein zweites — er richtet
      den Deploy neu ein. Das steht auch daneben, sonst klickt jemand weiter und
      sucht es später vergeblich.
    -->
    <div
      v-if="neuesGeheimnis"
      class="mb-4 rounded-md border px-3 py-2 text-[12px]"
      :style="{
        borderColor: 'color-mix(in srgb, var(--status-warning) 40%, var(--border-ring))',
        background: 'color-mix(in srgb, var(--status-warning) 12%, var(--surface-card))',
      }"
    >
      <p :style="{ color: 'var(--ink-primary)' }">{{ t('deploy.secretOnce') }}</p>
      <code class="mt-1 block break-all font-mono text-[12px]">{{ neuesGeheimnis }}</code>
    </div>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-2"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="speichern"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('deploy.site') }}
        </span>
        <select v-model="form.site_id" required
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option v-for="s in freieSites" :key="s.id" :value="s.id">{{ s.domain }}</option>
        </select>
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('deploy.branch') }}
        </span>
        <input v-model="form.ref" placeholder="main"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block sm:col-span-2">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('deploy.repo') }}
        </span>
        <input v-model="form.repo_url" required placeholder="git@github.com:name/repo.git"
               class="w-full rounded-md border px-3 py-2 font-mono text-[12px]" :style="inputStyle" />
        <span class="mt-1 block text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('deploy.repoHint') }}
        </span>
      </label>

      <fieldset class="sm:col-span-2">
        <legend class="mb-1 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('deploy.steps') }}
        </legend>
        <div class="flex flex-wrap gap-3">
          <label v-for="s in steps" :key="s" class="flex items-center gap-1.5 text-[12px]">
            <input type="checkbox" :value="s" v-model="form.steps" />
            <code class="font-mono">{{ s }}</code>
          </label>
        </div>
      </fieldset>

      <label class="flex items-center gap-2 text-[12px] sm:col-span-2">
        <input type="checkbox" v-model="form.auto_deploy" />
        {{ t('deploy.auto') }}
      </label>

      <div class="sm:col-span-2">
        <button type="submit" :disabled="busy"
                class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }">
          {{ busy ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </form>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>
    <p v-else-if="!deploys.length"
       class="text-[13px]"
       :style="{ color: 'var(--ink-muted)' }">
      {{ t('deploy.empty') }}
    </p>

    <div v-else class="space-y-3">
      <article
        v-for="d in deploys"
        :key="d.id"
        class="rounded-lg border p-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <header class="mb-3 flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="h-1.5 w-1.5 shrink-0 rounded-full"
                    :style="{ background: statusFarbe(d.last_status) }"></span>
              <span class="truncate text-[14px] font-medium">{{ d.domain }}</span>
              <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                {{ d.ref }}
              </span>
            </div>
            <div class="truncate font-mono text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ d.repo_url }}
            </div>
          </div>
          <div class="text-right text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
            <template v-if="d.last_release">
              {{ d.last_release }}<template v-if="d.last_commit"> &middot; {{ d.last_commit }}</template>
            </template>
            <template v-else>{{ t('deploy.never') }}</template>
          </div>
        </header>

        <div class="text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('deploy.hookUrl') }}:
          <code class="break-all font-mono">{{ d.hook_url }}</code>
        </div>

        <!-- Das Protokoll des letzten Laufs. Ein Deploy, der nur
             "fehlgeschlagen" meldet, zwingt zur Shell — und die hat der Kunde
             nicht. -->
        <details v-if="d.last_log" class="mt-2">
          <summary class="cursor-pointer text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('deploy.log') }}
          </summary>
          <pre class="mt-1 max-h-64 overflow-auto rounded-md p-2 font-mono text-[11px]"
               :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
          >{{ d.last_log }}</pre>
        </details>

        <div v-if="offen[d.id]" class="mt-3 space-y-2">
          <div v-if="keys[d.id]">
            <p class="text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('deploy.key') }}
            </p>
            <code class="block break-all font-mono text-[11px]">{{ keys[d.id] }}</code>
          </div>

          <div v-if="releases[d.id]">
            <p class="text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('deploy.releases') }}
            </p>
            <ul class="mt-1 space-y-1">
              <li v-for="r in releases[d.id].releases" :key="r"
                  class="flex items-center gap-2 text-[11px]">
                <code class="font-mono">{{ r }}</code>
                <span v-if="r === releases[d.id].current"
                      :style="{ color: 'var(--status-good)' }">{{ t('deploy.current') }}</span>
                <button v-else class="underline" :style="{ color: 'var(--ink-secondary)' }"
                        :disabled="busy" @click="zurueck(d, r)">
                  {{ t('deploy.rollback') }}
                </button>
              </li>
            </ul>
          </div>
        </div>

        <footer class="mt-3 flex flex-wrap gap-3 text-[11px]">
          <button class="underline" :style="{ color: 'var(--ink-secondary)' }"
                  :disabled="busy || d.last_status === 'running'" @click="starten(d)">
            {{ d.last_status === 'running' ? t('deploy.running') : t('deploy.run') }}
          </button>
          <button class="underline" :style="{ color: 'var(--ink-secondary)' }"
                  @click="staendeLaden(d)">
            {{ offen[d.id] ? t('deploy.hideDetails') : t('deploy.details') }}
          </button>
          <button class="underline" :style="{ color: 'var(--status-critical)' }"
                  :disabled="busy" @click="entfernen(d)">
            {{ t('common.delete') }}
          </button>
        </footer>
      </article>
    </div>
  </div>
</template>
