<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes } from '../format'
import { isAdmin } from '../stores/session'

const archives = ref([])
const entries = ref([])
const targets = ref([])
const loading = ref(true)
const busy = ref('')
const error = ref('')
const notice = ref('')

const showForm = ref(false)
const editing = ref(null)

const leer = () => ({
  name: '',
  kind: 's3',
  endpoint: '',
  region: '',
  bucket: '',
  secret: '',
  username: '',
  base_path: '',
  host: '',
  port: 21,
  use_tls: true,
  skip_verify: false,
  path_style: false,
  enabled: true,
})
const form = ref(leer())

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const istS3 = computed(() => form.value.kind === 's3' || form.value.kind === 'b2')

async function load() {
  loading.value = true
  try {
    const [backups, list] = await Promise.all([
      api.get('/backups'),
      api.get('/backup-targets'),
    ])
    archives.value = backups.archives || []
    entries.value = backups.entries || []
    targets.value = list
    error.value = ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

function startNew() {
  editing.value = null
  form.value = leer()
  showForm.value = true
}

// Beim Bearbeiten bleibt das Geheimnis leer. Es wird nie ausgeliefert, und ein
// leeres Feld heisst hier "unverändert" — der Platzhalter sagt das.
function startEdit(target) {
  editing.value = target.id
  form.value = { ...leer(), ...target, secret: '' }
  showForm.value = true
}

async function save() {
  busy.value = 'save'
  error.value = ''
  try {
    const body = { ...form.value, port: Number(form.value.port) || 21 }
    if (editing.value) await api.patch(`/backup-targets/${editing.value}`, body)
    else await api.post('/backup-targets', body)
    showForm.value = false
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
  }
}

async function test(target) {
  busy.value = `test-${target.id}`
  error.value = ''
  notice.value = ''
  try {
    await api.post(`/backup-targets/${target.id}/test`)
    notice.value = t('backup.testOk', { name: target.name })
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
    await load()
  }
}

async function remove(target) {
  if (!confirm(t('backup.confirmDelete', { name: target.name }))) return
  try {
    await api.del(`/backup-targets/${target.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function create() {
  busy.value = 'create'
  error.value = ''
  notice.value = ''
  try {
    const res = await api.post('/backups', { include_config: true })
    notice.value = t('backup.created', { size: formatBytes(res.size_bytes) })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
  }
}

async function upload(archive, targetId) {
  if (!targetId) return
  busy.value = `upload-${archive.name}`
  error.value = ''
  notice.value = ''
  try {
    const res = await api.post(`/backup-targets/${targetId}/upload`, { filename: archive.name })
    notice.value = t('backup.uploaded', { name: res.target, path: res.remote_path })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
  }
}

const aktiveZiele = computed(() => targets.value.filter((t) => t.enabled))
const zeit = (unix) => (unix ? new Date(unix * 1000).toLocaleString() : '—')

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('backup.title') }}</h1>
      <div class="flex gap-2">
        <button
          v-if="isAdmin()"
          :disabled="busy === 'create'"
          class="rounded-md border px-3 py-1.5 text-[13px] disabled:opacity-60"
          :style="{ borderColor: 'var(--line-axis)' }"
          @click="create"
        >
          {{ busy === 'create' ? t('backup.creating') : t('backup.create') }}
        </button>
        <button
          class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
          :style="{ background: 'var(--series-1)' }"
          @click="showForm ? (showForm = false) : startNew()"
        >
          {{ showForm ? t('common.cancel') : t('backup.newTarget') }}
        </button>
      </div>
    </header>

    <p v-if="error" class="mb-4 text-[13px] whitespace-pre-line"
       :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>
    <p v-if="notice" class="mb-4 text-[13px]" :style="{ color: 'var(--status-good)' }">
      {{ notice }}
    </p>

    <form
      v-if="showForm"
      class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-2 lg:grid-cols-3"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      @submit.prevent="save"
    >
      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('backup.name') }}
        </span>
        <input v-model="form.name" required
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('backup.kind') }}
        </span>
        <select v-model="form.kind"
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option value="s3">S3 (AWS, Hetzner, MinIO, Wasabi)</option>
          <option value="b2">Backblaze B2</option>
          <option value="ftp">FTP</option>
        </select>
      </label>

      <template v-if="istS3">
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('backup.region') }}
          </span>
          <input v-model="form.region" required placeholder="eu-central-1"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('backup.endpoint') }}
          </span>
          <input v-model="form.endpoint"
                 :placeholder="form.kind === 'b2' ? t('backup.endpointAuto') : 's3.eu-central-1.amazonaws.com'"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('backup.bucket') }}
          </span>
          <input v-model="form.bucket" required
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
      </template>

      <template v-else>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('backup.host') }}
          </span>
          <input v-model="form.host" required placeholder="sicherung.example.at"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('backup.port') }}
          </span>
          <input v-model="form.port" type="number" min="1" max="65535"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
      </template>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ istS3 ? t('backup.accessKey') : t('ftp.username') }}
        </span>
        <input v-model="form.username"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ istS3 ? t('backup.secretKey') : t('db.password') }}
        </span>
        <input v-model="form.secret" type="password" autocomplete="new-password"
               :placeholder="editing ? t('backup.secretUnchanged') : ''"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('backup.basePath') }}
        </span>
        <input v-model="form.base_path" placeholder="volt"
               class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
      </label>

      <div class="flex flex-wrap items-center gap-4 text-[12px] sm:col-span-2 lg:col-span-3">
        <label v-if="istS3" class="flex items-center gap-2">
          <input v-model="form.path_style" type="checkbox" />
          {{ t('backup.pathStyle') }}
        </label>
        <template v-else>
          <label class="flex items-center gap-2">
            <input v-model="form.use_tls" type="checkbox" />
            {{ t('backup.useTls') }}
          </label>
          <label v-if="form.use_tls" class="flex items-center gap-2">
            <input v-model="form.skip_verify" type="checkbox" />
            {{ t('backup.skipVerify') }}
          </label>
        </template>
        <label class="flex items-center gap-2">
          <input v-model="form.enabled" type="checkbox" />
          {{ t('backup.enabled') }}
        </label>

        <button type="submit" :disabled="busy === 'save'"
                class="ml-auto rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                :style="{ background: 'var(--series-1)' }">
          {{ t('common.save') }}
        </button>
      </div>

      <p v-if="!istS3 && !form.use_tls" class="text-[12px] sm:col-span-2 lg:col-span-3"
         :style="{ color: 'var(--status-warning)' }">
        {{ t('backup.noTlsWarning') }}
      </p>
    </form>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <template v-else>
      <!-- Ziele -->
      <h2 class="mb-2 text-[14px] font-medium">{{ t('backup.targets') }}</h2>
      <div v-if="targets.length" class="mb-6 overflow-hidden rounded-lg border"
           :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
        <table class="w-full text-left text-[13px]">
          <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
            <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
              <th class="px-4 py-2.5 font-normal">{{ t('backup.name') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('backup.kind') }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t('backup.lastRun') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="target in targets" :key="target.id"
                class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
              <td class="px-4 py-2.5">
                <span class="font-medium">{{ target.name }}</span>
                <span v-if="!target.enabled" class="ml-2 text-[11px]"
                      :style="{ color: 'var(--ink-muted)' }">
                  {{ t('ftp.status_disabled') }}
                </span>
              </td>
              <td class="px-4 py-2.5 font-mono text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
                {{ target.kind === 'ftp'
                    ? `${target.host}:${target.port}`
                    : `${target.bucket}.${target.endpoint}` }}
              </td>
              <td class="px-4 py-2.5 text-[12px]">
                <!-- Ein Ziel, das seit Wochen still scheitert, muss sich von
                     einem funktionierenden unterscheiden. -->
                <span v-if="target.last_error" :style="{ color: 'var(--status-critical)' }"
                      :title="target.last_error">
                  {{ zeit(target.last_used_at) }} — {{ t('backup.failed') }}
                </span>
                <span v-else-if="target.last_ok_at" :style="{ color: 'var(--status-good)' }">
                  {{ zeit(target.last_ok_at) }}
                </span>
                <span v-else :style="{ color: 'var(--ink-muted)' }">{{ t('backup.never') }}</span>
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap">
                <button class="text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                        :disabled="busy === `test-${target.id}`" @click="test(target)">
                  {{ busy === `test-${target.id}` ? t('backup.testing') : t('backup.test') }}
                </button>
                <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--ink-secondary)' }"
                        @click="startEdit(target)">
                  {{ t('common.edit') }}
                </button>
                <button class="ml-3 text-[12px] underline" :style="{ color: 'var(--status-critical)' }"
                        @click="remove(target)">
                  {{ t('sites.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else class="mb-6 text-[13px]" :style="{ color: 'var(--ink-muted)' }">
        {{ t('backup.noTargets') }}
      </p>

      <!-- Lokale Archive -->
      <template v-if="isAdmin()">
        <h2 class="mb-2 text-[14px] font-medium">{{ t('backup.archives') }}</h2>
        <div v-if="archives.length" class="overflow-hidden rounded-lg border"
             :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }">
          <table class="w-full text-left text-[13px]">
            <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
              <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                <th class="px-4 py-2.5 font-normal">{{ t('backup.archive') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('backup.size') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('backup.date') }}</th>
                <th class="px-4 py-2.5 text-right font-normal">{{ t('backup.uploadTo') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="archive in archives" :key="archive.name"
                  class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
                <td class="px-4 py-2.5 font-mono text-[12px]">{{ archive.name }}</td>
                <td class="tabular px-4 py-2.5">{{ formatBytes(archive.size_bytes) }}</td>
                <td class="px-4 py-2.5 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
                  {{ zeit(archive.mod_time) }}
                </td>
                <td class="px-4 py-2.5 text-right">
                  <span v-if="busy === `upload-${archive.name}`" class="text-[12px]"
                        :style="{ color: 'var(--ink-muted)' }">
                    {{ t('backup.uploading') }}
                  </span>
                  <select
                    v-else-if="aktiveZiele.length"
                    class="rounded-md border px-2 py-1 text-[12px]"
                    :style="inputStyle"
                    :value="''"
                    @change="upload(archive, Number($event.target.value)); $event.target.value = ''"
                  >
                    <option value="">—</option>
                    <option v-for="target in aktiveZiele" :key="target.id" :value="target.id">
                      {{ target.name }}
                    </option>
                  </select>
                  <span v-else class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
                    {{ t('backup.noTargets') }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('backup.noArchives') }}
        </p>
      </template>

      <!-- Verlauf -->
      <h2 v-if="entries.length" class="mt-6 mb-2 text-[14px] font-medium">
        {{ t('backup.history') }}
      </h2>
      <ul v-if="entries.length" class="space-y-1 text-[12px]">
        <li v-for="entry in entries.slice(0, 20)" :key="entry.id"
            :style="{ color: 'var(--ink-secondary)' }">
          <span class="tabular">{{ zeit(entry.created_at) }}</span>
          · {{ entry.destination }}
          · {{ formatBytes(entry.size_bytes) }}
          <span v-if="entry.remote_path" class="font-mono">{{ entry.remote_path }}</span>
        </li>
      </ul>
    </template>
  </div>
</template>
