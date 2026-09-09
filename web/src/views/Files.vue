<script setup>
import { ref, computed, onMounted, watch, defineAsyncComponent } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes, formatDateTime, permsToOctal } from '../format'
import { askConfirm } from '../stores/confirm'
import { isAdmin } from '../stores/session'

const SiteTerminal = defineAsyncComponent(() => import('../components/SiteTerminal.vue'))

const FAVORITES_KEY = 'volt.files.favorites'

const sites = ref([])
const loading = ref(false)
const error = ref('')
const notice = ref('')
const uploading = ref(false)
const dragOver = ref(false)
const view = ref('list') // list | grid
const search = ref('')
const sortKey = ref('name')
const sortDir = ref('asc')
const selected = ref(new Set())
const showTerminal = ref(false)

// --- Tabs --------------------------------------------------------------
// Jeder Tab bleibt an eine Site gebunden — anders als bei aaPanel gibt es
// keinen serverweiten Wurzelzugriff, nur mehrere gleichzeitig offene
// Site-Ansichten. Das wahrt die Mandantentrennung, die files.go zeilenweise
// erzwingt (jede Anfrage trägt eine site_id, nie einen absoluten Pfad).
const tabs = ref([])
const activeIndex = ref(0)
const tab = computed(() => tabs.value[activeIndex.value] || null)

function tabLabel(tb) {
  const site = sites.value.find((s) => s.id === tb.siteId)
  const domain = site?.domain || '…'
  return tb.path ? `${domain}: ${tb.path.split('/').pop()}` : domain
}

function newTab() {
  const siteId = tab.value?.siteId ?? sites.value[0]?.id ?? null
  tabs.value.push({ siteId, path: '' })
  activeIndex.value = tabs.value.length - 1
}

function closeTab(i) {
  if (tabs.value.length <= 1) return
  tabs.value.splice(i, 1)
  if (activeIndex.value >= tabs.value.length) activeIndex.value = tabs.value.length - 1
  else if (activeIndex.value > i) activeIndex.value -= 1
}

function switchTab(i) {
  activeIndex.value = i
}

function changeTabSite(newSiteId) {
  if (!tab.value) return
  tab.value.siteId = newSiteId
  tab.value.path = ''
  load()
}

// --- Favoriten (rein lokal je Browser) ----------------------------------

function loadFavorites() {
  try {
    return JSON.parse(localStorage.getItem(FAVORITES_KEY) || '[]')
  } catch {
    return []
  }
}
const favorites = ref(loadFavorites())
watch(favorites, (v) => {
  try {
    localStorage.setItem(FAVORITES_KEY, JSON.stringify(v))
  } catch {
    // Privater Modus o.ä. — Favoriten bleiben dann eben nur für diese Sitzung im Speicher.
  }
}, { deep: true })

const isFavorite = computed(() =>
  !!tab.value && favorites.value.some((f) => f.siteId === tab.value.siteId && f.path === tab.value.path),
)

function toggleFavorite() {
  if (!tab.value) return
  const idx = favorites.value.findIndex((f) => f.siteId === tab.value.siteId && f.path === tab.value.path)
  if (idx >= 0) favorites.value.splice(idx, 1)
  else favorites.value.push({ siteId: tab.value.siteId, path: tab.value.path, label: tabLabel(tab.value) })
}

function openFavorite(fav) {
  newTab()
  tab.value.siteId = fav.siteId
  tab.value.path = fav.path
}

// --- Verzeichnisinhalt ---------------------------------------------------

const entries = ref([])

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const crumbs = computed(() => {
  if (!tab.value) return []
  const parts = tab.value.path.split('/').filter(Boolean)
  const out = [{ label: t('files.root'), path: '' }]
  let acc = ''
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part
    out.push({ label: part, path: acc })
  }
  return out
})

function sortValue(entry) {
  if (sortKey.value === 'size') return entry.size
  if (sortKey.value === 'modified') return entry.mod_time
  return entry.name.toLowerCase()
}

// Verzeichnisse immer zuerst, darüber hinaus nach der gewählten Spalte.
const sorted = computed(() => {
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...entries.value].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    const av = sortValue(a)
    const bv = sortValue(b)
    if (av < bv) return -1 * dir
    if (av > bv) return 1 * dir
    return 0
  })
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? sorted.value.filter((e) => e.name.toLowerCase().includes(q)) : sorted.value
})

function setSort(key) {
  if (sortKey.value === key) sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  else {
    sortKey.value = key
    sortDir.value = 'asc'
  }
}

async function loadSites() {
  try {
    sites.value = await api.get('/sites')
    if (sites.value.length && !tabs.value.length) {
      tabs.value.push({ siteId: sites.value[0].id, path: '' })
    }
  } catch (err) {
    error.value = err.message
  }
}

let loadController = null

async function load() {
  if (!tab.value?.siteId) return
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loading.value = true
  error.value = ''
  try {
    const res = await api.get(
      `/sites/${tab.value.siteId}/files?path=${encodeURIComponent(tab.value.path)}`,
      { signal: controller.signal },
    )
    entries.value = res.entries
    selected.value = new Set()
  } catch (err) {
    if (err.name === 'AbortError') return
    error.value = err.message
    entries.value = []
  } finally {
    if (loadController === controller) loading.value = false
  }
}

function open(entry) {
  if (entry.is_dir) {
    tab.value.path = entry.path
    return
  }
  edit(entry)
}

// --- Editor ---------------------------------------------------------------

const editor = ref(null)
const EDIT_MAX_BYTES = 2 * 1024 * 1024

async function edit(entry) {
  error.value = ''
  if (entry.size > EDIT_MAX_BYTES) {
    error.value = t('files.tooLargeToEdit')
    return
  }
  try {
    const res = await api.get(
      `/sites/${tab.value.siteId}/files/read?path=${encodeURIComponent(entry.path)}`,
    )
    editor.value = { path: entry.path, name: entry.name, content: res.content }
  } catch (err) {
    error.value = err.message
  }
}

async function save() {
  try {
    await api.post(`/sites/${tab.value.siteId}/files/write`, {
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
  return tab.value.path ? `${tab.value.path}/${name}` : name
}

// --- Einzelaktionen ---------------------------------------------------------

async function newFolder() {
  const name = prompt(t('files.folderName'))
  if (!name) return
  try {
    await api.post(`/sites/${tab.value.siteId}/files/mkdir`, { path: join(name) })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function newFile() {
  const name = prompt(t('files.fileName'))
  if (!name) return
  try {
    await api.post(`/sites/${tab.value.siteId}/files/write`, { path: join(name), content: '' })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function rename(entry) {
  const name = prompt(t('files.newName'), entry.name)
  if (!name || name === entry.name) return
  try {
    await api.post(`/sites/${tab.value.siteId}/files/move`, { from: entry.path, to: join(name) })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function copyEntry(entry) {
  const dest = prompt(t('files.copyTo'), join(`${entry.name}-Kopie`))
  if (!dest) return
  try {
    await api.post(`/sites/${tab.value.siteId}/files/copy`, { from: entry.path, to: dest })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function chmodEntry(entry) {
  const mode = prompt(t('files.chmodPrompt'), permsToOctal(entry.mode))
  if (!mode) return
  let recursive = false
  if (entry.is_dir) recursive = await askConfirm(t('files.chmodRecursive'))
  try {
    await api.post(`/sites/${tab.value.siteId}/files/chmod`, { path: entry.path, mode, recursive })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function remove(entry) {
  const question = entry.is_dir
    ? t('files.confirmDeleteDir', { name: entry.name })
    : t('files.confirmDelete', { name: entry.name })
  if (!(await askConfirm(question))) return
  try {
    await api.post(`/sites/${tab.value.siteId}/files/delete`, {
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
    await api.post(`/sites/${tab.value.siteId}/files/extract`, {
      archive: entry.path,
      dest: tab.value.path,
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function archive(entry) {
  try {
    await api.post(`/sites/${tab.value.siteId}/files/archive`, {
      sources: [entry.path],
      dest: join(`${entry.name}.zip`),
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

function download(entry) {
  window.location.href =
    api.url(`/sites/${tab.value.siteId}/files/download?path=${encodeURIComponent(entry.path)}`)
}

function isArchive(name) {
  return /\.(tar\.gz|tgz|zip)$/i.test(name)
}

// --- Mehrfachauswahl ---------------------------------------------------

function toggleSelect(entry) {
  const next = new Set(selected.value)
  if (next.has(entry.path)) next.delete(entry.path)
  else next.add(entry.path)
  selected.value = next
}

const allSelected = computed(
  () => filtered.value.length > 0 && filtered.value.every((e) => selected.value.has(e.path)),
)

function toggleSelectAll() {
  selected.value = allSelected.value ? new Set() : new Set(filtered.value.map((e) => e.path))
}

const selectedEntries = computed(() => entries.value.filter((e) => selected.value.has(e.path)))

async function batchDelete() {
  const list = selectedEntries.value
  if (!list.length) return
  if (!(await askConfirm(t('files.confirmBatchDelete', { n: list.length })))) return
  error.value = ''
  try {
    for (const entry of list) {
      await api.post(`/sites/${tab.value.siteId}/files/delete`, {
        path: entry.path, recursive: entry.is_dir,
      })
    }
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function batchMove() {
  const list = selectedEntries.value
  if (!list.length) return
  const destDir = prompt(t('files.moveTo'), tab.value.path)
  if (destDir === null) return
  error.value = ''
  try {
    for (const entry of list) {
      const to = destDir ? `${destDir}/${entry.name}` : entry.name
      await api.post(`/sites/${tab.value.siteId}/files/move`, { from: entry.path, to })
    }
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function batchArchive() {
  const list = selectedEntries.value
  if (!list.length) return
  const name = prompt(t('files.archiveName'), `archiv-${Date.now()}.zip`)
  if (!name) return
  error.value = ''
  try {
    await api.post(`/sites/${tab.value.siteId}/files/archive`, {
      sources: list.map((e) => e.path),
      dest: join(name),
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

// --- Upload (Knopf + Drag&Drop) -----------------------------------------

const fileInput = ref(null)

async function uploadFiles(files) {
  if (!files.length || !tab.value?.siteId) return
  uploading.value = true
  error.value = ''
  try {
    for (const file of files) {
      const body = new FormData()
      body.append('file', file)
      body.append('path', tab.value.path)
      await api.upload(`/sites/${tab.value.siteId}/files/upload`, body)
    }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    uploading.value = false
  }
}

function onFileInput(event) {
  uploadFiles(Array.from(event.target.files || []))
  event.target.value = ''
}

function onDrop(event) {
  dragOver.value = false
  uploadFiles(Array.from(event.dataTransfer?.files || []))
}

onMounted(async () => {
  await loadSites()
  await load()
})

watch(activeIndex, () => {
  editor.value = null
  load()
})
watch(() => tab.value?.path, () => {
  editor.value = null
  load()
})
</script>

<template>
  <div class="fade-in px-8 py-6" @dragover.prevent="dragOver = true" @dragleave.prevent="dragOver = false" @drop.prevent="onDrop">
    <header class="mb-3 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('files.title') }}</h1>
    </header>

    <!-- Tabs -->
    <nav class="mb-3 flex items-center gap-1 overflow-x-auto">
      <div
        v-for="(tb, i) in tabs" :key="i"
        class="flex shrink-0 items-center gap-1.5 rounded-t-md border-b-2 px-3 py-1.5 text-[12px] cursor-pointer"
        :style="i === activeIndex
          ? { borderColor: 'var(--accent)', color: 'var(--ink-primary)', background: 'var(--surface-card)', fontWeight: 500 }
          : { borderColor: 'transparent', color: 'var(--ink-secondary)' }"
        @click="switchTab(i)"
      >
        {{ tabLabel(tb) }}
        <button v-if="tabs.length > 1" class="text-ink-muted hover:opacity-70" :title="t('files.closeTab')" @click.stop="closeTab(i)">
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
      <button class="shrink-0 rounded-md px-2 py-1.5 text-[13px] text-ink-secondary hover:opacity-70" :title="t('files.newTab')" @click="newTab">
        +
      </button>
    </nav>

    <div v-if="tab" class="panel-card mb-3 flex flex-wrap items-center gap-2 p-3">
      <select
        :value="tab.siteId"
        class="rounded-md border px-2 py-1.5 text-[12px]" :style="inputStyle"
        @change="changeTabSite(Number($event.target.value))"
      >
        <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.domain }}</option>
      </select>

      <input ref="fileInput" type="file" multiple class="hidden" @change="onFileInput" />
      <button class="rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60 bg-accent"
              :disabled="uploading || !tab.siteId" @click="fileInput.click()">
        {{ uploading ? t('files.uploading') : t('files.upload') }}
      </button>
      <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="newFolder">
        {{ t('files.newFolder') }}
      </button>
      <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="newFile">
        {{ t('files.newFile') }}
      </button>
      <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="tab.path = ''">
        {{ t('files.rootDir') }}
      </button>
      <button v-if="isAdmin()" class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="showTerminal = !showTerminal">
        {{ t('site.terminal') }}
      </button>
      <button
        class="rounded-md border px-2 py-1.5 text-[12px]"
        :style="{ borderColor: 'var(--line-axis)', color: isFavorite ? 'var(--status-warning)' : 'var(--ink-secondary)' }"
        :title="t('files.favorite')"
        @click="toggleFavorite"
      >
        <svg width="13" height="13" viewBox="0 0 24 24" :fill="isFavorite ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 3l2.7 5.5 6 .9-4.4 4.2 1 6-5.3-2.8-5.3 2.8 1-6-4.4-4.2 6-.9z" />
        </svg>
      </button>

      <span v-if="favorites.length" class="relative">
        <select class="rounded-md border px-2 py-1.5 text-[12px]" :style="inputStyle" @change="openFavorite(favorites[$event.target.value]); $event.target.value = ''">
          <option value="" selected disabled>{{ t('files.favorites') }}</option>
          <option v-for="(f, i) in favorites" :key="i" :value="i">{{ f.label }}</option>
        </select>
      </span>

      <div class="ml-auto flex items-center gap-2">
        <input v-model="search" :placeholder="t('files.search')" class="w-40 rounded-md border px-2 py-1.5 text-[12px]" :style="inputStyle" />
        <button class="rounded-md border p-1.5" :style="{ borderColor: 'var(--line-axis)', color: view === 'list' ? 'var(--accent)' : 'var(--ink-muted)' }" :title="t('files.listView')" @click="view = 'list'">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" /></svg>
        </button>
        <button class="rounded-md border p-1.5" :style="{ borderColor: 'var(--line-axis)', color: view === 'grid' ? 'var(--accent)' : 'var(--ink-muted)' }" :title="t('files.gridView')" @click="view = 'grid'">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" aria-hidden="true"><rect x="4" y="4" width="7" height="7" /><rect x="13" y="4" width="7" height="7" /><rect x="4" y="13" width="7" height="7" /><rect x="13" y="13" width="7" height="7" /></svg>
        </button>
      </div>
    </div>

    <section v-if="showTerminal && tab" class="panel-card mb-3">
      <SiteTerminal :site-id="tab.siteId" />
    </section>

    <nav v-if="tab" class="mb-3 flex flex-wrap items-center gap-1 text-[12px]">
      <template v-for="(crumb, i) in crumbs" :key="crumb.path">
        <span v-if="i > 0" :style="{ color: 'var(--ink-muted)' }">/</span>
        <button
          class="rounded px-1 py-0.5 hover:underline"
          :style="{ color: i === crumbs.length - 1 ? 'var(--ink-primary)' : 'var(--ink-secondary)' }"
          @click="tab.path = crumb.path"
        >
          {{ crumb.label }}
        </button>
      </template>
    </nav>

    <p v-if="error" class="mb-3 text-[13px] text-status-critical" role="alert">{{ error }}</p>
    <p v-if="notice" class="mb-3 text-[13px] text-status-good">{{ notice }}</p>
    <p v-if="!sites.length && !loading" class="text-[13px] text-ink-muted">{{ t('files.chooseSite') }}</p>

    <!-- Editor -->
    <section v-if="editor" class="panel-card mb-4">
      <header class="flex items-center justify-between gap-3 border-b px-4 py-2.5 border-line-hairline">
        <span class="tabular text-[13px] font-medium">{{ editor.path }}</span>
        <div class="flex gap-2">
          <button class="rounded-md px-3 py-1.5 text-[12px] font-medium text-white bg-accent" @click="save">
            {{ t('files.save') }}
          </button>
          <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="editor = null">
            {{ t('common.cancel') }}
          </button>
        </div>
      </header>
      <textarea
        v-model="editor.content" spellcheck="false"
        class="tabular block w-full resize-y bg-transparent p-4 font-mono text-[12px] leading-relaxed outline-none"
        rows="20"
      ></textarea>
    </section>

    <!-- Mehrfachauswahl-Aktionen -->
    <div v-if="selected.size" class="panel-card mb-3 flex flex-wrap items-center gap-2 p-3">
      <span class="text-[12px] text-ink-secondary">{{ t('files.selectedCount', { n: selected.size }) }}</span>
      <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="batchArchive">
        {{ t('files.batchArchive') }}
      </button>
      <button class="rounded-md border px-3 py-1.5 text-[12px]" :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }" @click="batchMove">
        {{ t('files.batchMove') }}
      </button>
      <button class="rounded-md border px-3 py-1.5 text-[12px] text-status-critical" :style="{ borderColor: 'var(--line-axis)' }" @click="batchDelete">
        {{ t('files.batchDelete') }}
      </button>
    </div>

    <!-- Listenansicht -->
    <div v-if="view === 'list' && filtered.length" class="panel-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-[13px]">
          <thead class="text-[12px] text-ink-muted">
            <tr class="border-b border-line-hairline">
              <th class="w-8 px-4 py-2.5">
                <input type="checkbox" :checked="allSelected" @change="toggleSelectAll" />
              </th>
              <th class="cursor-pointer px-4 py-2.5 font-normal" @click="setSort('name')">{{ t('db.name') }}</th>
              <th class="cursor-pointer px-4 py-2.5 text-right font-normal" @click="setSort('size')">{{ t('files.size') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('files.permsOwner') }}</th>
              <th class="cursor-pointer px-4 py-2.5 font-normal" @click="setSort('modified')">{{ t('files.modified') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in filtered" :key="entry.path" class="border-b last:border-0 border-line-hairline">
              <td class="px-4 py-2">
                <input type="checkbox" :checked="selected.has(entry.path)" @change="toggleSelect(entry)" />
              </td>
              <td class="px-4 py-2">
                <button class="flex items-center gap-2 text-left hover:underline" @click="open(entry)">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"
                       :style="{ color: entry.is_dir ? 'var(--series-1)' : 'var(--ink-muted)' }" aria-hidden="true">
                    <path v-if="entry.is_dir" d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
                    <path v-else d="M14 3v5h5M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V8z" />
                  </svg>
                  {{ entry.name }}
                </button>
              </td>
              <td class="tabular px-4 py-2 text-right text-ink-secondary">
                {{ entry.is_dir ? '—' : formatBytes(entry.size) }}
              </td>
              <td class="tabular px-4 py-2 text-[12px] text-ink-muted">
                {{ permsToOctal(entry.mode) }} / {{ entry.owner }}
              </td>
              <td class="tabular px-4 py-2 text-[12px] text-ink-muted">
                {{ formatDateTime(entry.mod_time) }}
              </td>
              <td class="px-4 py-2 text-right whitespace-nowrap text-[12px]">
                <button v-if="!entry.is_dir" class="underline text-ink-secondary" @click="download(entry)">{{ t('files.download') }}</button>
                <button v-if="isArchive(entry.name)" class="ml-3 underline text-ink-secondary" @click="extract(entry)">{{ t('files.extract') }}</button>
                <button v-if="entry.is_dir" class="ml-3 underline text-ink-secondary" @click="archive(entry)">{{ t('files.archive') }}</button>
                <button class="ml-3 underline text-ink-secondary" @click="copyEntry(entry)">{{ t('files.copy') }}</button>
                <button class="ml-3 underline text-ink-secondary" @click="chmodEntry(entry)">{{ t('files.permissions') }}</button>
                <button class="ml-3 underline text-ink-secondary" @click="rename(entry)">{{ t('files.rename') }}</button>
                <button class="ml-3 underline text-status-critical" @click="remove(entry)">{{ t('files.delete') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Rasteransicht -->
    <div v-else-if="view === 'grid' && filtered.length" class="panel-card grid grid-cols-3 gap-2 p-3 sm:grid-cols-4 lg:grid-cols-6">
      <button
        v-for="entry in filtered" :key="entry.path"
        class="flex flex-col items-center gap-1.5 rounded-md p-3 text-center hover:bg-surface-sunken"
        @click="open(entry)"
      >
        <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"
             :style="{ color: entry.is_dir ? 'var(--series-1)' : 'var(--ink-muted)' }" aria-hidden="true">
          <path v-if="entry.is_dir" d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
          <path v-else d="M14 3v5h5M14 3H6a2 2 0 00-2 2v14a2 2 0 002 2h12a2 2 0 002-2V8z" />
        </svg>
        <span class="w-full truncate text-[12px]">{{ entry.name }}</span>
      </button>
    </div>

    <p v-else-if="!loading && tab" class="text-[13px] text-ink-muted">{{ t('files.empty') }}</p>

    <div
      v-if="dragOver"
      class="fixed inset-0 z-50 flex items-center justify-center"
      :style="{ background: 'color-mix(in srgb, var(--surface-page) 70%, transparent)' }"
    >
      <div class="panel-card p-8 text-[14px] font-medium">{{ t('files.dropHint') }}</div>
    </div>
  </div>
</template>
