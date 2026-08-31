<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes, formatDateTime } from '../format'

const sites = ref([])
const siteId = ref(null)
const path = ref('')
const entries = ref([])
const loading = ref(false)
const error = ref('')
const notice = ref('')
const uploading = ref(false)

// Der Editor hält Pfad und Inhalt getrennt, damit beim Speichern nicht
// versehentlich der inzwischen gewechselte Pfad benutzt wird.
const editor = ref(null)
const fileInput = ref(null)

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

// Brotkrumen aus dem aktuellen Pfad; jeder Teil ist anklickbar.
const crumbs = computed(() => {
  const parts = path.value.split('/').filter(Boolean)
  const out = [{ label: t('files.root'), path: '' }]
  let acc = ''
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part
    out.push({ label: part, path: acc })
  }
  return out
})

// Verzeichnisse zuerst, dann alphabetisch — die übliche Erwartung.
const sorted = computed(() =>
  [...entries.value].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    return a.name.localeCompare(b.name)
  }),
)

async function loadSites() {
  try {
    sites.value = await api.get('/sites')
    if (sites.value.length && siteId.value === null) siteId.value = sites.value[0].id
  } catch (err) {
    error.value = err.message
  }
}

async function load() {
  if (!siteId.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await api.get(`/sites/${siteId.value}/files?path=${encodeURIComponent(path.value)}`)
    entries.value = res.entries
  } catch (err) {
    error.value = err.message
    entries.value = []
  } finally {
    loading.value = false
  }
}

function open(entry) {
  if (entry.is_dir) {
    path.value = entry.path
    return
  }
  edit(entry)
}

async function edit(entry) {
  error.value = ''
  try {
    const res = await api.get(
      `/sites/${siteId.value}/files/read?path=${encodeURIComponent(entry.path)}`,
    )
    editor.value = { path: entry.path, name: entry.name, content: res.content }
  } catch (err) {
    error.value = err.message
  }
}

async function save() {
  try {
    await api.post(`/sites/${siteId.value}/files/write`, {
      path: editor.value.path,
      content: editor.value.content,
    })
    notice.value = t('files.saved')
    setTimeout(() => (notice.value = ''), 2500)
    editor.value = null
    await load()
  } catch (err) {
    error.value = err.message
  }
}

function join(name) {
  return path.value ? `${path.value}/${name}` : name
}

async function newFolder() {
  const name = prompt(t('files.folderName'))
  if (!name) return
  try {
    await api.post(`/sites/${siteId.value}/files/mkdir`, { path: join(name) })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function newFile() {
  const name = prompt(t('files.fileName'))
  if (!name) return
  try {
    await api.post(`/sites/${siteId.value}/files/write`, { path: join(name), content: '' })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function rename(entry) {
  const name = prompt(t('files.newName'), entry.name)
  if (!name || name === entry.name) return
  try {
    await api.post(`/sites/${siteId.value}/files/move`, { from: entry.path, to: join(name) })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function remove(entry) {
  const question = entry.is_dir
    ? t('files.confirmDeleteDir', { name: entry.name })
    : t('files.confirmDelete', { name: entry.name })
  if (!confirm(question)) return
  try {
    await api.post(`/sites/${siteId.value}/files/delete`, {
      path: entry.path,
      recursive: entry.is_dir,
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function extract(entry) {
  try {
    await api.post(`/sites/${siteId.value}/files/extract`, {
      archive: entry.path,
      dest: path.value,
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function archive(entry) {
  try {
    await api.post(`/sites/${siteId.value}/files/archive`, {
      sources: [entry.path],
      dest: join(`${entry.name}.tar.gz`),
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

function download(entry) {
  // Der Browser lädt selbst; das Session-Cookie geht dabei mit.
  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  window.location.href =
    `${base}/api/v1/sites/${siteId.value}/files/download?path=${encodeURIComponent(entry.path)}`
}

async function upload(event) {
  const files = Array.from(event.target.files || [])
  if (!files.length) return

  uploading.value = true
  error.value = ''
  try {
    for (const file of files) {
      const body = new FormData()
      body.append('file', file)
      body.append('path', path.value)

      // FormData geht nicht durch den JSON-Wrapper, deshalb hier direkt.
      const base = import.meta.env.BASE_URL.replace(/\/$/, '')
      const csrf = document.cookie.match(/(?:^|;\s*)volt_csrf=([^;]*)/)
      const res = await fetch(`${base}/api/v1/sites/${siteId.value}/files/upload`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-CSRF-Token': csrf ? decodeURIComponent(csrf[1]) : '' },
        body,
      })
      if (!res.ok) {
        const payload = await res.json().catch(() => null)
        throw new Error(payload?.error || `HTTP ${res.status}`)
      }
    }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    uploading.value = false
    event.target.value = ''
  }
}

function isArchive(name) {
  return /\.(tar\.gz|tgz|zip)$/i.test(name)
}

onMounted(async () => {
  await loadSites()
  await load()
})

// Beim Wechsel der Site zurück zur Wurzel — der alte Pfad gilt dort nicht.
watch(siteId, () => {
  path.value = ''
  editor.value = null
  load()
})
watch(path, load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('files.title') }}</h1>

      <div class="flex flex-wrap items-center gap-2">
        <select v-model="siteId" class="rounded-md border px-3 py-1.5 text-[13px]" :style="inputStyle">
          <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.domain }}</option>
        </select>

        <button class="rounded-md border px-3 py-1.5 text-[12px]"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                @click="newFolder">
          {{ t('files.newFolder') }}
        </button>
        <button class="rounded-md border px-3 py-1.5 text-[12px]"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                @click="newFile">
          {{ t('files.newFile') }}
        </button>

        <input ref="fileInput" type="file" multiple class="hidden" @change="upload" />
        <button class="rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }"
                :disabled="uploading || !siteId"
                @click="fileInput.click()">
          {{ uploading ? t('files.uploading') : t('files.upload') }}
        </button>
      </div>
    </header>

    <nav class="mb-3 flex flex-wrap items-center gap-1 text-[12px]">
      <template v-for="(crumb, i) in crumbs" :key="crumb.path">
        <span v-if="i > 0" :style="{ color: 'var(--ink-muted)' }">/</span>
        <button
          class="rounded px-1 py-0.5 hover:underline"
          :style="{ color: i === crumbs.length - 1 ? 'var(--ink-primary)' : 'var(--ink-secondary)' }"
          @click="path = crumb.path"
        >
          {{ crumb.label }}
        </button>
      </template>
    </nav>

    <p v-if="error" class="mb-3 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>
    <p v-if="notice" class="mb-3 text-[13px]" :style="{ color: 'var(--status-good)' }">{{ notice }}</p>
    <p v-if="!sites.length && !loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('files.chooseSite') }}
    </p>

    <!-- Editor -->
    <section
      v-if="editor"
      class="mb-4 rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <header class="flex items-center justify-between gap-3 border-b px-4 py-2.5"
              :style="{ borderColor: 'var(--line-hairline)' }">
        <span class="tabular text-[13px] font-medium">{{ editor.path }}</span>
        <div class="flex gap-2">
          <button class="rounded-md px-3 py-1.5 text-[12px] font-medium text-white"
                  :style="{ background: 'var(--series-1)' }" @click="save">
            {{ t('files.save') }}
          </button>
          <button class="rounded-md border px-3 py-1.5 text-[12px]"
                  :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                  @click="editor = null">
            {{ t('common.cancel') }}
          </button>
        </div>
      </header>
      <textarea
        v-model="editor.content"
        spellcheck="false"
        class="tabular block w-full resize-y bg-transparent p-4 font-mono text-[12px] leading-relaxed outline-none"
        rows="20"
      ></textarea>
    </section>

    <div
      v-if="sorted.length"
      class="overflow-hidden rounded-lg border"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <table class="w-full text-left text-[13px]">
        <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
            <th class="px-4 py-2.5 font-normal">{{ t('db.name') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('files.size') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('files.permissions') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('files.owner') }}</th>
            <th class="px-4 py-2.5 font-normal">{{ t('files.modified') }}</th>
            <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="entry in sorted"
            :key="entry.path"
            class="border-b last:border-0"
            :style="{ borderColor: 'var(--line-hairline)' }"
          >
            <td class="px-4 py-2">
              <button class="flex items-center gap-2 text-left hover:underline" @click="open(entry)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                     stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"
                     :style="{ color: entry.is_dir ? 'var(--series-1)' : 'var(--ink-muted)' }"
                     aria-hidden="true">
                  <path v-if="entry.is_dir"
                        d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
                  <path v-else d="M14 3v5h5M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V8z" />
                </svg>
                {{ entry.name }}
              </button>
            </td>
            <td class="tabular px-4 py-2 text-right" :style="{ color: 'var(--ink-secondary)' }">
              {{ entry.is_dir ? '—' : formatBytes(entry.size) }}
            </td>
            <td class="tabular px-4 py-2 text-[12px]" :style="{ color: 'var(--ink-muted)' }">
              {{ entry.mode }}
            </td>
            <td class="px-4 py-2 text-[12px]" :style="{ color: 'var(--ink-muted)' }">
              {{ entry.owner }}
            </td>
            <td class="tabular px-4 py-2 text-[12px]" :style="{ color: 'var(--ink-muted)' }">
              {{ formatDateTime(entry.mod_time) }}
            </td>
            <td class="px-4 py-2 text-right whitespace-nowrap text-[12px]">
              <button v-if="!entry.is_dir" class="underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="download(entry)">
                {{ t('files.download') }}
              </button>
              <button v-if="isArchive(entry.name)" class="ml-3 underline"
                      :style="{ color: 'var(--ink-secondary)' }" @click="extract(entry)">
                {{ t('files.extract') }}
              </button>
              <button v-if="entry.is_dir" class="ml-3 underline"
                      :style="{ color: 'var(--ink-secondary)' }" @click="archive(entry)">
                {{ t('files.archive') }}
              </button>
              <button class="ml-3 underline" :style="{ color: 'var(--ink-secondary)' }"
                      @click="rename(entry)">
                {{ t('files.rename') }}
              </button>
              <button class="ml-3 underline" :style="{ color: 'var(--status-critical)' }"
                      @click="remove(entry)">
                {{ t('files.delete') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p v-else-if="!loading && siteId" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('files.empty') }}
    </p>
  </div>
</template>
