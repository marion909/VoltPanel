<script setup>
import { ref, computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'
import { askConfirm } from '../stores/confirm'
import { hasRole, session, loadSession } from '../stores/session'
import { formatDateTime, daysLeft } from '../format'
import SkeletonRows from '../components/SkeletonRows.vue'

const tab = ref('domains') // domains | certs
const error = ref('')
const notice = ref('')
const busy = ref(false)

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

// --- Zonen & Einträge -------------------------------------------------------

const zones = ref([])
const loadingZones = ref(true)
const selectedZoneKey = ref('') // "<provider>:<id>", weil IDs zwischen Providern kollidieren können
const records = ref([])
const loadingRecords = ref(false)

const selectedZone = computed(() =>
  zones.value.find((z) => `${z.Provider}:${z.ID}` === selectedZoneKey.value) || null,
)

const hasAnyProvider = computed(
  () => !!(session.tenant?.has_cloudflare_token || session.tenant?.has_hetzner_token),
)

async function loadZones() {
  loadingZones.value = true
  error.value = ''
  try {
    zones.value = await api.get('/dns/zones')
    if (zones.value.length && !selectedZone.value) {
      selectedZoneKey.value = `${zones.value[0].Provider}:${zones.value[0].ID}`
      await loadRecords()
    }
  } catch (err) {
    error.value = err.message
  } finally {
    loadingZones.value = false
  }
}

async function loadRecords() {
  if (!selectedZone.value) {
    records.value = []
    return
  }
  loadingRecords.value = true
  error.value = ''
  try {
    records.value = await api.get(
      `/dns/zones/${selectedZone.value.ID}/records?provider=${selectedZone.value.Provider}`,
    )
  } catch (err) {
    error.value = err.message
  } finally {
    loadingRecords.value = false
  }
}

function onZoneChange() {
  loadRecords()
}

const recordTypes = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'SRV', 'CAA', 'NS']

const showRecordForm = ref(false)
const editingOldValue = ref(null) // null = neu, sonst der bisherige Wert des bearbeiteten Eintrags
const recordForm = ref({ type: 'A', name: '', value: '', ttl: 3600, priority: null })

function startAddRecord() {
  editingOldValue.value = null
  recordForm.value = { type: 'A', name: '', value: '', ttl: 3600, priority: null }
  showRecordForm.value = true
}

function startEditRecord(rec) {
  editingOldValue.value = rec.Value
  recordForm.value = {
    type: rec.Type, name: rec.Name, value: rec.Value,
    ttl: rec.TTL || 3600, priority: rec.Priority ?? null,
  }
  showRecordForm.value = true
}

async function saveRecord() {
  if (!selectedZone.value) return
  busy.value = true
  error.value = ''
  try {
    const body = {
      provider: selectedZone.value.Provider,
      type: recordForm.value.type,
      name: recordForm.value.name.trim(),
      value: recordForm.value.value.trim(),
      ttl: recordForm.value.ttl || 0,
      priority: ['MX', 'SRV'].includes(recordForm.value.type) ? recordForm.value.priority : null,
    }
    if (editingOldValue.value !== null) {
      await api.put(`/dns/zones/${selectedZone.value.ID}/records`, { ...body, old_value: editingOldValue.value })
    } else {
      await api.post(`/dns/zones/${selectedZone.value.ID}/records`, body)
    }
    showRecordForm.value = false
    await loadRecords()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function deleteRecord(rec) {
  if (!selectedZone.value) return
  if (!(await askConfirm(t('domains.confirmDeleteRecord', { name: rec.Name })))) return
  error.value = ''
  try {
    const q = new URLSearchParams({
      provider: selectedZone.value.Provider, type: rec.Type, name: rec.Name, value: rec.Value,
    })
    await api.del(`/dns/zones/${selectedZone.value.ID}/records?${q}`)
    await loadRecords()
  } catch (err) {
    error.value = err.message
  }
}

// --- API-Zugänge -------------------------------------------------------------

const cfTokenInput = ref('')
const hetznerTokenInput = ref('')
const savingToken = ref('')

async function saveCloudflareToken() {
  if (!session.tenant) return
  savingToken.value = 'cloudflare'
  error.value = ''
  try {
    await api.put(`/tenants/${session.tenant.id}/cloudflare`, { token: cfTokenInput.value })
    cfTokenInput.value = ''
    await loadSession()
    notice.value = t('domains.tokenSaved')
    await loadZones()
  } catch (err) {
    error.value = err.message
  } finally {
    savingToken.value = ''
  }
}

async function saveHetznerToken() {
  if (!session.tenant) return
  savingToken.value = 'hetzner'
  error.value = ''
  try {
    await api.put(`/tenants/${session.tenant.id}/hetzner-dns`, { token: hetznerTokenInput.value })
    hetznerTokenInput.value = ''
    await loadSession()
    notice.value = t('domains.tokenSaved')
    await loadZones()
  } catch (err) {
    error.value = err.message
  } finally {
    savingToken.value = ''
  }
}

// --- Zertifikate -------------------------------------------------------------

const certs = ref([])
const sites = ref([])
const loadingCerts = ref(true)
const renewBusy = ref({})

const siteById = computed(() => {
  const out = {}
  for (const s of sites.value) out[s.id] = s
  return out
})

async function loadCerts() {
  loadingCerts.value = true
  error.value = ''
  try {
    const [c, s] = await Promise.all([api.get('/certs'), api.get('/sites').catch(() => [])])
    certs.value = c
    sites.value = s
  } catch (err) {
    error.value = err.message
  } finally {
    loadingCerts.value = false
  }
}

function certColor(cert) {
  if (!cert.not_after) return 'var(--ink-muted)'
  const n = daysLeft(cert.not_after)
  return n <= 14 ? 'var(--status-critical)' : n <= 30 ? 'var(--status-warning)' : 'var(--status-good)'
}

const showCertForm = ref(false)
const certForm = ref({ domains: '', wildcard: false, cloudflare_token: '' })

async function issueCert() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/certs', {
      domains: certForm.value.domains.split(',').map((d) => d.trim()).filter(Boolean),
      wildcard: certForm.value.wildcard,
      cloudflare_token: certForm.value.cloudflare_token,
    })
    showCertForm.value = false
    certForm.value = { domains: '', wildcard: false, cloudflare_token: '' }
    await loadCerts()
    notice.value = t('domains.certIssued')
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function toggleAutoRenew(cert) {
  error.value = ''
  try {
    await api.patch(`/certs/${cert.id}`, { auto_renew: !cert.auto_renew })
    await loadCerts()
  } catch (err) {
    error.value = err.message
  }
}

async function renewCert(cert) {
  renewBusy.value = { ...renewBusy.value, [cert.id]: true }
  error.value = ''
  try {
    await api.post(`/certs/${cert.id}/renew`)
    await loadCerts()
    notice.value = t('domains.certRenewed')
  } catch (err) {
    error.value = err.message
  } finally {
    renewBusy.value = { ...renewBusy.value, [cert.id]: false }
  }
}

async function deleteCert(cert) {
  if (!(await askConfirm(t('domains.confirmDeleteCert', { domains: cert.domains.join(', ') })))) return
  error.value = ''
  try {
    await api.del(`/certs/${cert.id}`)
    await loadCerts()
  } catch (err) {
    error.value = err.message
  }
}

onMounted(() => {
  loadZones()
  loadCerts()
})
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('domains.title') }}</h1>
    </header>

    <p v-if="error" class="mb-4 text-[13px] text-status-critical" role="alert">{{ error }}</p>
    <p v-if="notice" class="mb-4 text-[13px] text-status-good">{{ notice }}</p>

    <nav class="mb-5 flex gap-1 overflow-x-auto border-b border-line-hairline">
      <button
        v-for="item in [
          { key: 'domains', label: t('domains.tabDomains') },
          { key: 'certs', label: t('domains.tabCerts') },
        ]"
        :key="item.key"
        class="-mb-px border-b-2 px-3 py-2 text-[13px] transition-colors"
        :style="
          tab === item.key
            ? { borderColor: 'var(--accent)', color: 'var(--ink-primary)', fontWeight: 500 }
            : { borderColor: 'transparent', color: 'var(--ink-secondary)' }
        "
        @click="tab = item.key"
      >
        {{ item.label }}
      </button>
    </nav>

    <!-- ================= Domains ================= -->
    <template v-if="tab === 'domains'">
      <p
        v-if="!hasAnyProvider"
        class="panel-card mb-5 p-4 text-[13px]"
        :style="{ color: 'var(--ink-secondary)' }"
      >
        {{ t('domains.noProvider') }}
      </p>

      <SkeletonRows v-if="loadingZones" :cols="4" />

      <template v-else-if="zones.length">
        <div class="panel-card mb-5 flex flex-wrap items-end gap-2 p-4">
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.zone') }}</span>
            <select
              v-model="selectedZoneKey"
              class="w-64 rounded-md border px-3 py-2 text-[13px]"
              :style="inputStyle"
              @change="onZoneChange"
            >
              <option v-for="z in zones" :key="`${z.Provider}:${z.ID}`" :value="`${z.Provider}:${z.ID}`">
                {{ z.Name }} ({{ z.Provider }})
              </option>
            </select>
          </label>
          <button
            v-if="selectedZone"
            class="rounded-md px-3 py-2 text-[13px] font-medium text-white bg-accent"
            @click="startAddRecord"
          >
            {{ t('domains.addRecord') }}
          </button>
        </div>

        <form
          v-if="showRecordForm"
          class="panel-card mb-5 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-5"
          @submit.prevent="saveRecord"
        >
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.colType') }}</span>
            <select v-model="recordForm.type" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
              <option v-for="ty in recordTypes" :key="ty" :value="ty">{{ ty }}</option>
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.colName') }}</span>
            <input v-model="recordForm.name" required placeholder="www" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.colValue') }}</span>
            <input v-model="recordForm.value" required class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.colTTL') }}</span>
            <input v-model.number="recordForm.ttl" type="number" min="0" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
          <label v-if="['MX', 'SRV'].includes(recordForm.type)" class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.colPriority') }}</span>
            <input v-model.number="recordForm.priority" type="number" min="0" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
          </label>
          <div class="flex items-end gap-2">
            <button type="submit" :disabled="busy" class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent">
              {{ busy ? t('common.loading') : t('common.save') }}
            </button>
            <button type="button" class="rounded-md px-3 py-2 text-[13px]" :style="inputStyle" @click="showRecordForm = false">
              {{ t('common.cancel') }}
            </button>
          </div>
        </form>

        <SkeletonRows v-if="loadingRecords" :cols="5" />
        <p
          v-else-if="!records.length"
          class="rounded-lg border p-6 text-center text-[13px]"
          :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
        >
          {{ t('domains.recordsEmpty') }}
        </p>
        <div v-else class="panel-card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="w-full text-left text-[13px]">
              <thead class="text-[12px] text-ink-muted">
                <tr class="border-b border-line-hairline">
                  <th class="px-4 py-2.5 font-normal">{{ t('domains.colName') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('domains.colType') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('domains.colValue') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('domains.colTTL') }}</th>
                  <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(rec, i) in records" :key="i" class="border-b last:border-0 border-line-hairline">
                  <td class="max-w-[160px] truncate px-4 py-2.5 font-medium" :title="rec.Name">{{ rec.Name }}</td>
                  <td class="px-4 py-2.5 text-ink-secondary">{{ rec.Type }}</td>
                  <td class="max-w-[320px] truncate px-4 py-2.5 font-mono text-[12px] text-ink-secondary" :title="rec.Value">{{ rec.Value }}</td>
                  <td class="tabular px-4 py-2.5 text-ink-secondary">{{ rec.TTL || '—' }}</td>
                  <td class="px-4 py-2.5 text-right whitespace-nowrap">
                    <button class="text-[12px] underline text-ink-secondary" @click="startEditRecord(rec)">
                      {{ t('common.edit') }}
                    </button>
                    <button class="ml-3 text-[12px] underline text-status-critical" @click="deleteRecord(rec)">
                      {{ t('common.delete') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>

      <p v-else class="text-[13px] text-ink-muted">{{ t('domains.empty') }}</p>

      <section v-if="hasRole('reseller')" class="panel-card mt-6 p-4">
        <h2 class="mb-3 text-[13px] font-semibold">{{ t('domains.apiTokens') }}</h2>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="block">
              <span class="mb-1 block text-[12px] text-ink-secondary">
                {{ t('domains.cloudflareToken') }} —
                {{ session.tenant?.has_cloudflare_token ? t('domains.hasToken') : t('domains.noToken') }}
              </span>
              <input
                v-model="cfTokenInput" type="password" :placeholder="t('domains.tokenPlaceholder')"
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle"
              />
            </label>
            <button
              class="mt-2 rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60 bg-accent"
              :disabled="savingToken === 'cloudflare'"
              @click="saveCloudflareToken"
            >
              {{ t('common.save') }}
            </button>
          </div>
          <div>
            <label class="block">
              <span class="mb-1 block text-[12px] text-ink-secondary">
                {{ t('domains.hetznerToken') }} —
                {{ session.tenant?.has_hetzner_token ? t('domains.hasToken') : t('domains.noToken') }}
              </span>
              <input
                v-model="hetznerTokenInput" type="password" :placeholder="t('domains.tokenPlaceholder')"
                class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle"
              />
            </label>
            <button
              class="mt-2 rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60 bg-accent"
              :disabled="savingToken === 'hetzner'"
              @click="saveHetznerToken"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </div>
      </section>
    </template>

    <!-- ================= SSL-Zertifikate ================= -->
    <template v-else-if="tab === 'certs'">
      <div class="mb-5 flex justify-end">
        <button class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white bg-accent" @click="showCertForm = !showCertForm">
          {{ showCertForm ? t('common.cancel') : t('domains.certNew') }}
        </button>
      </div>

      <form v-if="showCertForm" class="panel-card mb-5 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4" @submit.prevent="issueCert">
        <label class="block lg:col-span-2">
          <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.certDomains') }}</span>
          <input
            v-model="certForm.domains" required :placeholder="t('domains.certDomainsPlaceholder')"
            class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle"
          />
        </label>
        <label class="flex items-end gap-2 pb-2.5">
          <input v-model="certForm.wildcard" type="checkbox" />
          <span class="text-[13px] text-ink-secondary">{{ t('domains.certWildcard') }}</span>
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('domains.cloudflareToken') }}</span>
          <input
            v-model="certForm.cloudflare_token" type="password"
            class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle"
          />
        </label>
        <div class="flex items-end">
          <button type="submit" :disabled="busy" class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent">
            {{ busy ? t('common.loading') : t('common.create') }}
          </button>
        </div>
      </form>

      <SkeletonRows v-if="loadingCerts" :cols="7" />
      <p
        v-else-if="!certs.length"
        class="rounded-lg border p-6 text-center text-[13px]"
        :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
      >
        {{ t('domains.certEmpty') }}
      </p>
      <div v-else class="panel-card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full text-left text-[13px]">
            <thead class="text-[12px] text-ink-muted">
              <tr class="border-b border-line-hairline">
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certBrand') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certDomains') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certDaysLeft') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certAutoRenew') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certLocation') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certLastIssued') }}</th>
                <th class="px-4 py-2.5 font-normal">{{ t('domains.certMethod') }}</th>
                <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="cert in certs" :key="cert.id" class="border-b last:border-0 border-line-hairline">
                <td class="px-4 py-2.5 text-ink-secondary">Let's Encrypt</td>
                <td class="px-4 py-2.5 font-mono text-[12px]">{{ cert.domains.join(', ') }}</td>
                <td class="tabular px-4 py-2.5" :style="{ color: certColor(cert) }">
                  {{ cert.not_after ? t('sites.daysLeft', { n: daysLeft(cert.not_after) }) : '—' }}
                </td>
                <td class="px-4 py-2.5">
                  <button
                    class="inline-flex items-center gap-1.5"
                    :style="{ color: cert.auto_renew ? 'var(--status-good)' : 'var(--ink-muted)' }"
                    @click="toggleAutoRenew(cert)"
                  >
                    <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: cert.auto_renew ? 'var(--status-good)' : 'var(--line-axis)' }"></span>
                    {{ cert.auto_renew ? t('common.yes') : t('common.no') }}
                  </button>
                </td>
                <td class="px-4 py-2.5 text-ink-secondary">
                  <RouterLink v-if="cert.site_id && siteById[cert.site_id]" :to="`/frontend/sites/${cert.site_id}`" class="underline">
                    {{ siteById[cert.site_id].domain }}
                  </RouterLink>
                  <span v-else>—</span>
                </td>
                <td class="px-4 py-2.5 text-ink-secondary">{{ formatDateTime(cert.last_renewal_at) }}</td>
                <td class="px-4 py-2.5 text-ink-secondary">{{ cert.challenge }}</td>
                <td class="px-4 py-2.5 text-right whitespace-nowrap">
                  <button
                    class="text-[12px] underline text-ink-secondary disabled:opacity-60"
                    :disabled="renewBusy[cert.id]"
                    @click="renewCert(cert)"
                  >
                    {{ renewBusy[cert.id] ? t('common.loading') : t('domains.certRenew') }}
                  </button>
                  <button class="ml-3 text-[12px] underline text-status-critical" @click="deleteCert(cert)">
                    {{ t('common.delete') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>
