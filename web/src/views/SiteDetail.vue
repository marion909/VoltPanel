<script setup>
import { ref, computed, defineAsyncComponent, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes, formatDateTime } from '../format'
import { isAdmin } from '../stores/session'

const route = useRoute()
const router = useRouter()

const siteId = computed(() => Number(route.params.id))
const site = ref(null)
const settings = ref(null)
const php = ref(null)
const certs = ref([])
const logText = ref('')
const logType = ref('access')

const tab = ref('overview')
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busy = ref(false)

// Passwörter existieren nur im Formular; gespeichert wird ausschließlich der
// Hash, den der Server erzeugt.
const authUsers = ref([])
const wildcard = ref(false)
const cfToken = ref('')

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

// Erst laden, wenn der Reiter geöffnet wird: xterm ist größer als die gesamte
// übrige Oberfläche zusammen.
const SiteTerminal = defineAsyncComponent(() => import('../components/SiteTerminal.vue'))

const tabs = computed(() => {
  const list = [
    { key: 'overview', label: 'site.overview' },
    { key: 'settings', label: 'site.settings' },
    { key: 'ssl', label: 'site.ssl' },
    { key: 'logs', label: 'site.logs' },
  ]
  if (site.value?.type === 'php') list.splice(2, 0, { key: 'php', label: 'site.php' })
  if (isAdmin()) list.push({ key: 'terminal', label: 'site.terminal' })
  return list
})

function flash(message) {
  notice.value = message
  setTimeout(() => (notice.value = ''), 3000)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    site.value = await api.get(`/sites/${siteId.value}`)
    settings.value = await api.get(`/sites/${siteId.value}/settings`)

    // Vorhandene Zugänge ohne Passwort anzeigen: ein leeres Feld heißt
    // "unverändert lassen" ist nicht möglich, weil nur der Hash gespeichert
    // ist — beim Speichern müssen alle Passwörter neu gesetzt werden.
    authUsers.value = (settings.value.basic_auth?.users || []).map((username) => ({
      username,
      password: '',
    }))

    if (site.value.type === 'php') {
      php.value = await api.get(`/sites/${siteId.value}/php`)
    }
    certs.value = (await api.get('/certs')).filter((c) => c.site_id === siteId.value)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

// --- Einstellungen ---

function addRedirect() {
  settings.value.redirects = [...(settings.value.redirects || []), { from: '/', to: '/', code: 301 }]
}
function removeRedirect(i) {
  settings.value.redirects.splice(i, 1)
}

async function saveSettings() {
  busy.value = true
  error.value = ''
  try {
    const payload = {
      redirects: settings.value.redirects || [],
      deny_ips: splitLines(settings.value._denyText),
      allow_ips: splitLines(settings.value._allowText),
      extra_lines: splitLines(settings.value._extraText),
      max_body_size: settings.value.max_body_size || '',
      fastcgi_timeout: Number(settings.value.fastcgi_timeout) || 0,
    }
    // Nur mitschicken, wenn der Abschnitt angefasst wurde — sonst würde jedes
    // Speichern alle Passwörter neu verlangen.
    if (authUsers.value.some((u) => u.password) || authUsers.value.length === 0) {
      payload.basic_auth_users = authUsers.value.filter((u) => u.username && u.password)
    }

    settings.value = { ...(await api.patch(`/sites/${siteId.value}/settings`, payload)) }
    hydrateTextareas()
    flash(t('site.saved'))
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

function splitLines(text) {
  return (text || '')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

// Die Listen werden als mehrzeiliger Text bearbeitet — bequemer als eine
// Reihe einzelner Eingabefelder.
function hydrateTextareas() {
  settings.value._denyText = (settings.value.deny_ips || []).join('\n')
  settings.value._allowText = (settings.value.allow_ips || []).join('\n')
  settings.value._extraText = (settings.value.extra_lines || []).join('\n')
}

// --- PHP ---

async function savePHP() {
  busy.value = true
  error.value = ''
  try {
    const p = php.value.pool
    const payload = {
      php_version: p.php_version,
      pm: p.pm,
      max_children: Number(p.max_children),
      memory_limit: p.memory_limit,
      max_execution_time: Number(p.max_execution_time),
      upload_max_filesize: p.upload_max_filesize,
    }
    if (isAdmin()) {
      payload.disable_functions = p.disable_functions
      payload.extra_ini = p.extra_ini
    }
    php.value.pool = await api.patch(`/sites/${siteId.value}/php`, payload)
    await load()
    flash(t('site.saved'))
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

// --- SSL ---

// HSTS gilt für ein Jahr und lässt sich nicht zurückrufen: der Browser merkt
// sich die Anweisung. Ohne Zertifikat lehnt der Server sie deshalb ab.
async function saveHTTPS() {
  busy.value = true
  error.value = ''
  try {
    site.value = await api.patch(`/sites/${siteId.value}`, {
      force_https: site.value.force_https,
      hsts: site.value.hsts,
    })
    flash(t('site.saved'))
  } catch (err) {
    error.value = err.message
    await load()
  } finally {
    busy.value = false
  }
}

async function issueCert() {
  busy.value = true
  error.value = ''
  try {
    await api.post(`/sites/${siteId.value}/certificate`, {
      wildcard: wildcard.value,
      cloudflare_token: cfToken.value,
    })
    cfToken.value = ''
    await load()
    flash(t('site.certIssued'))
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

// --- Logs ---

async function loadLog() {
  try {
    const res = await api.get(`/sites/${siteId.value}/logs?type=${logType.value}&lines=300`)
    logText.value = res.content || t('site.noLog')
  } catch (err) {
    error.value = err.message
  }
}

async function rebuild() {
  try {
    await api.post(`/sites/${siteId.value}/rebuild`)
    flash(t('site.rebuilt'))
  } catch (err) {
    error.value = err.message
  }
}

onMounted(async () => {
  await load()
  hydrateTextareas()
})

watch(tab, (value) => {
  if (value === 'logs') loadLog()
})
watch(logType, loadLog)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5">
      <button class="mb-2 text-[12px] underline" :style="{ color: 'var(--ink-muted)' }"
              @click="router.push('/sites')">
        ← {{ t('sites.title') }}
      </button>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-[18px] font-semibold tracking-tight">{{ site?.domain }}</h1>
          <p v-if="site" class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
            {{ site.type }}<template v-if="site.php_version"> · PHP {{ site.php_version }}</template>
            · {{ site.system_user }} · {{ formatBytes(site.disk_bytes) }}
          </p>
        </div>
        <button class="rounded-md border px-3 py-1.5 text-[12px]"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                @click="rebuild">
          {{ t('sites.rebuild') }}
        </button>
      </div>
    </header>

    <nav class="mb-4 flex flex-wrap gap-1 border-b" :style="{ borderColor: 'var(--line-hairline)' }">
      <button
        v-for="item in tabs"
        :key="item.key"
        class="-mb-px border-b-2 px-3 py-2 text-[13px]"
        :style="{
          borderColor: tab === item.key ? 'var(--series-1)' : 'transparent',
          color: tab === item.key ? 'var(--ink-primary)' : 'var(--ink-secondary)',
        }"
        @click="tab = item.key"
      >
        {{ t(item.label) }}
      </button>
    </nav>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>
    <p v-if="notice" class="mb-4 text-[13px]" :style="{ color: 'var(--status-good)' }">{{ notice }}</p>
    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <!-- Übersicht -->
    <section
      v-else-if="tab === 'overview' && site"
      class="max-w-2xl rounded-lg border p-5"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-[13px]">
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('sites.domain') }}</dt>
        <dd>{{ site.domain }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.aliases') }}</dt>
        <dd>{{ site.aliases?.length ? site.aliases.join(', ') : '—' }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.root') }}</dt>
        <dd class="tabular font-mono text-[12px]">{{ site.root_path }}/{{ site.document_root }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.user') }}</dt>
        <dd class="tabular">{{ site.system_user }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('files.size') }}</dt>
        <dd class="tabular">
          {{ formatBytes(site.disk_bytes) }}
          <span v-if="site.disk_measured_at" :style="{ color: 'var(--ink-muted)' }">
            ({{ formatDateTime(site.disk_measured_at) }})
          </span>
        </dd>
        <dt :style="{ color: 'var(--ink-muted)' }">{{ t('sites.ssl') }}</dt>
        <dd>{{ site.ssl_enabled ? t('common.yes') : t('common.no') }}</dd>
      </dl>
    </section>

    <!-- Einstellungen -->
    <section v-else-if="tab === 'settings' && settings" class="max-w-3xl space-y-4">
      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-3 text-[14px] font-medium">{{ t('site.redirects') }}</h2>

        <div v-for="(r, i) in settings.redirects || []" :key="i" class="mb-2 flex flex-wrap gap-2">
          <input v-model="r.from" placeholder="/alt"
                 class="w-40 rounded-md border px-3 py-1.5 font-mono text-[12px]" :style="inputStyle" />
          <input v-model="r.to" placeholder="/neu oder https://…"
                 class="min-w-48 flex-1 rounded-md border px-3 py-1.5 font-mono text-[12px]" :style="inputStyle" />
          <select v-model.number="r.code" class="rounded-md border px-2 py-1.5 text-[12px]" :style="inputStyle">
            <option :value="301">301</option>
            <option :value="302">302</option>
            <option :value="307">307</option>
            <option :value="308">308</option>
          </select>
          <button class="px-2 text-[12px]" :style="{ color: 'var(--status-critical)' }"
                  @click="removeRedirect(i)">×</button>
        </div>

        <button class="text-[12px] underline" :style="{ color: 'var(--series-1)' }" @click="addRedirect">
          + {{ t('site.addRedirect') }}
        </button>
      </div>

      <div class="grid gap-4 sm:grid-cols-2">
        <div class="rounded-lg border p-5"
             :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
          <h2 class="mb-1 text-[14px] font-medium">{{ t('site.denyIPs') }}</h2>
          <p class="mb-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('site.ipHint') }}</p>
          <textarea v-model="settings._denyText" rows="4" spellcheck="false"
                    class="tabular w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                    :style="inputStyle" placeholder="203.0.113.5&#10;198.51.100.0/24"></textarea>
        </div>

        <div class="rounded-lg border p-5"
             :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
          <h2 class="mb-1 text-[14px] font-medium">{{ t('site.allowIPs') }}</h2>
          <p class="mb-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('site.allowHint') }}</p>
          <textarea v-model="settings._allowText" rows="4" spellcheck="false"
                    class="tabular w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                    :style="inputStyle" placeholder="10.0.0.0/8"></textarea>
        </div>
      </div>

      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-1 text-[14px] font-medium">{{ t('site.basicAuth') }}</h2>
        <p class="mb-3 text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('site.authHint') }}</p>

        <div v-for="(u, i) in authUsers" :key="i" class="mb-2 flex flex-wrap gap-2">
          <input v-model="u.username" placeholder="benutzer"
                 class="w-40 rounded-md border px-3 py-1.5 text-[12px]" :style="inputStyle" />
          <input v-model="u.password" type="password" :placeholder="t('site.authPassword')"
                 class="min-w-48 flex-1 rounded-md border px-3 py-1.5 text-[12px]" :style="inputStyle" />
          <button class="px-2 text-[12px]" :style="{ color: 'var(--status-critical)' }"
                  @click="authUsers.splice(i, 1)">×</button>
        </div>

        <button class="text-[12px] underline" :style="{ color: 'var(--series-1)' }"
                @click="authUsers.push({ username: '', password: '' })">
          + {{ t('site.addAuthUser') }}
        </button>
      </div>

      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-1 text-[14px] font-medium">{{ t('site.extraLines') }}</h2>
        <p class="mb-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('site.extraHint') }}</p>
        <textarea v-model="settings._extraText" rows="5" spellcheck="false"
                  class="tabular w-full rounded-md border px-3 py-2 font-mono text-[12px]"
                  :style="inputStyle" placeholder="add_header X-Robots-Tag noindex;"></textarea>

        <div class="mt-4 grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('site.maxBody') }}
            </span>
            <input v-model="settings.max_body_size" placeholder="64M"
                   class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('site.fastcgiTimeout') }}
            </span>
            <input v-model.number="settings.fastcgi_timeout" type="number" min="0" max="3600"
                   class="tabular w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
        </div>
      </div>

      <button :disabled="busy"
              class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
              :style="{ background: 'var(--series-1)' }" @click="saveSettings">
        {{ busy ? t('common.loading') : t('common.save') }}
      </button>
    </section>

    <!-- PHP -->
    <section v-else-if="tab === 'php' && php" class="max-w-2xl">
      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('sites.php') }}
            </span>
            <select v-model="php.pool.php_version" class="w-full rounded-md border px-3 py-2 text-[13px]"
                    :style="inputStyle">
              <option v-for="v in php.available_versions.length ? php.available_versions
                        : [php.pool.php_version]" :key="v" :value="v">{{ v }}</option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('site.processManager') }}
            </span>
            <select v-model="php.pool.pm" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
              <option value="ondemand">ondemand</option>
              <option value="dynamic">dynamic</option>
              <option value="static">static</option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('site.maxChildren') }}
            </span>
            <input v-model.number="php.pool.max_children" type="number" min="1" max="500"
                   class="tabular w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>

          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              memory_limit
            </span>
            <input v-model="php.pool.memory_limit" placeholder="256M"
                   class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>

          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              max_execution_time
            </span>
            <input v-model.number="php.pool.max_execution_time" type="number" min="1" max="3600"
                   class="tabular w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>

          <label class="block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              upload_max_filesize
            </span>
            <input v-model="php.pool.upload_max_filesize" placeholder="64M"
                   class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
        </div>

        <!-- Diese beiden Felder heben die Isolation der Site auf, wenn man sie
             leert — deshalb nur für Administratoren. -->
        <template v-if="isAdmin()">
          <label class="mt-4 block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              disable_functions
            </span>
            <textarea v-model="php.pool.disable_functions" rows="3" spellcheck="false"
                      class="w-full rounded-md border px-3 py-2 font-mono text-[11px]"
                      :style="inputStyle"></textarea>
            <span class="mt-1 block text-[11px]" :style="{ color: 'var(--status-warning)' }">
              {{ t('site.disableFunctionsHint') }}
            </span>
          </label>

          <label class="mt-3 block">
            <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('site.extraINI') }}
            </span>
            <textarea v-model="php.pool.extra_ini" rows="3" spellcheck="false"
                      class="w-full rounded-md border px-3 py-2 font-mono text-[11px]"
                      :style="inputStyle"></textarea>
          </label>
        </template>

        <button :disabled="busy"
                class="mt-4 rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }" @click="savePHP">
          {{ busy ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </section>

    <!-- SSL -->
    <section v-else-if="tab === 'ssl'" class="max-w-2xl space-y-4">
      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-3 text-[14px] font-medium">{{ t('site.httpsBehaviour') }}</h2>

        <label class="mb-2 flex items-start gap-2 text-[13px]">
          <input v-model="site.force_https" type="checkbox" class="mt-0.5" />
          <span>
            {{ t('site.forceHTTPS') }}
            <span class="block text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ t('site.forceHTTPSHint') }}
            </span>
          </span>
        </label>

        <label class="mb-3 flex items-start gap-2 text-[13px]">
          <input v-model="site.hsts" type="checkbox" class="mt-0.5"
                 :disabled="!site.ssl_enabled" />
          <span>
            {{ t('site.hsts') }}
            <span class="block text-[11px]"
                  :style="{ color: site.ssl_enabled ? 'var(--ink-muted)' : 'var(--status-warning)' }">
              {{ site.ssl_enabled ? t('site.hstsHint') : t('site.hstsNeedsCert') }}
            </span>
          </span>
        </label>

        <button :disabled="busy"
                class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }" @click="saveHTTPS">
          {{ t('common.save') }}
        </button>
      </div>

      <div v-if="certs.length" class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-3 text-[14px] font-medium">{{ t('site.currentCert') }}</h2>
        <dl v-for="cert in certs" :key="cert.id"
            class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-[13px]">
          <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.domains') }}</dt>
          <dd>{{ cert.domains.join(', ') }}</dd>
          <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.challenge') }}</dt>
          <dd>{{ cert.challenge }}</dd>
          <dt :style="{ color: 'var(--ink-muted)' }">{{ t('site.expires') }}</dt>
          <dd class="tabular">
            {{ cert.not_after ? formatDateTime(cert.not_after) : '—' }}
          </dd>
        </dl>
      </div>

      <div class="rounded-lg border p-5"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <h2 class="mb-1 text-[14px] font-medium">{{ t('site.issueCert') }}</h2>
        <p class="mb-3 text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('site.certHint') }}</p>

        <label class="mb-3 flex items-center gap-2 text-[13px]">
          <input v-model="wildcard" type="checkbox" />
          {{ t('site.wildcard') }}
        </label>

        <label v-if="wildcard" class="mb-3 block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('site.cfToken') }}
          </span>
          <input v-model="cfToken" type="password" :placeholder="t('site.cfTokenHint')"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <button :disabled="busy"
                class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }" @click="issueCert">
          {{ busy ? t('site.issuing') : t('site.issueCert') }}
        </button>
      </div>
    </section>

    <section v-else-if="tab === 'terminal' && site" class="max-w-4xl">
      <SiteTerminal :site-id="site.id" />
    </section>

    <!-- Logs -->
    <section v-else-if="tab === 'logs'">
      <div class="mb-3 flex gap-2">
        <button
          v-for="type in ['access', 'error']"
          :key="type"
          class="rounded-md border px-3 py-1.5 text-[12px]"
          :style="{
            borderColor: logType === type ? 'var(--series-1)' : 'var(--line-axis)',
            color: logType === type ? 'var(--series-1)' : 'var(--ink-secondary)',
          }"
          @click="logType = type"
        >
          {{ type }}
        </button>
      </div>

      <pre
        class="max-h-[32rem] overflow-auto rounded-lg border p-4 font-mono text-[11px] leading-relaxed"
        :style="{
          borderColor: 'var(--border-ring)',
          background: 'var(--surface-card)',
          color: 'var(--ink-secondary)',
        }"
      >{{ logText }}</pre>
    </section>
  </div>
</template>
