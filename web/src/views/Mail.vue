<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { isAdmin } from '../stores/session'
import { askConfirm } from '../stores/confirm'
import { formatBytes } from '../format'
import InstallHint from '../components/InstallHint.vue'
import SkeletonCards from '../components/SkeletonCards.vue'

// Mail: Domänen, Postfächer, Weiterleitungen.
//
// Eine Maildomäne gehört einem Mandanten — anders als Firewall oder Dienste.
// Deshalb steht diese Ansicht jedem Angemeldeten offen; was er sieht,
// entscheidet der Scope auf dem Server, nicht diese Datei.
const status = ref(null)
const domains = ref([])
const boxes = ref([])
const aliases = ref([])
const webmail = ref(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const tab = ref('domains') // domains | mailboxes | settings

// Ein frisch gesetztes Passwort steht genau einmal hier.
const credentials = ref(null)

const offen = ref({})
const domainForm = ref('')
const boxForm = ref({ domain_id: null, local_part: '', password: '', quota_mb: 0 })
const aliasForm = ref({ domain_id: null, source: '', destination: '' })
const dkim = ref({})
const check = ref(null)
const dnsErgebnis = ref({})
const autoconfigErgebnis = ref({})
const settings = ref(null)
const checkBusy = ref(false)
const blacklistBusy = ref({})
const spam = ref(null)
const copied = ref('')

// Neues Postfach aus dem Mailboxen-Reiter — dort ist die Domäne ein Feld im
// Formular, nicht schon durch die Karte gegeben wie im Domänen-Reiter.
const newBoxForm = ref({ domain_id: null, local_part: '', password: '', quota_mb: 0 })
const mailboxFilter = ref('')
const mailboxSearch = ref('')

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const bereit = computed(() => status.value?.postfix_installed && status.value?.configured)

const boxenVon = (id) => boxes.value.filter((b) => b.domain_id === id)
const aliaseVon = (id) => aliases.value.filter((a) => a.domain_id === id)

const domainByID = computed(() => Object.fromEntries(domains.value.map((d) => [d.id, d])))

const filteredBoxes = computed(() => {
  const suche = mailboxSearch.value.trim().toLowerCase()
  return boxes.value.filter((b) => {
    if (mailboxFilter.value && String(b.domain_id) !== String(mailboxFilter.value)) return false
    if (suche && !b.address.toLowerCase().includes(suche)) return false
    return true
  })
})

// Verbrauch/Kontingent einer Domäne insgesamt — die Summe ihrer Postfächer.
// 0 als Grenze eines Postfachs heißt "unbegrenzt"; sobald eines dabei ist,
// ist die Summe als Ganzes unbegrenzt, auch wenn andere eine Zahl haben.
function domainQuota(domainID) {
  const eigene = boxenVon(domainID)
  const used = eigene.reduce((sum, b) => sum + (b.used_bytes || 0), 0)
  const unbegrenzt = eigene.some((b) => !b.quota_mb)
  const limitMB = eigene.reduce((sum, b) => sum + (b.quota_mb || 0), 0)
  return { used, unbegrenzt, limitBytes: limitMB * 1024 * 1024 }
}

// Die Zustellbarkeitsprüfung nennt TLS als einen von mehreren Befunden, für
// den ganzen Server statt je Domäne (Postfix hat ein Zertifikat oder keins).
// Ohne dass "Prüfen" schon einmal lief, ist der Stand einfach unbekannt.
const mailTLS = computed(() => {
  const b = (check.value?.befunde || []).find((x) => x.was === 'TLS')
  if (!b) return null
  return b.stufe === 'gut'
})

async function load() {
  loading.value = true
  try {
    const [s, d, b, a, cfg, sp, wm] = await Promise.all([
      // Der Zustandsbericht ist Administratoren vorbehalten — für alle
      // anderen bleibt er leer, und die Listen stehen trotzdem.
      api.get('/mail/status').catch(() => null),
      api.get('/mail/domains'),
      api.get('/mail/mailboxes'),
      api.get('/mail/aliases'),
      api.get('/mail/settings').catch(() => null),
      api.get('/mail/spamstats').catch(() => null),
      api.get('/webmail').catch(() => null),
    ])
    status.value = s
    domains.value = d
    boxes.value = b
    aliases.value = a
    settings.value = cfg
    spam.value = sp
    webmail.value = wm
    error.value = ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function fuehreAus(fn) {
  busy.value = true
  error.value = ''
  try {
    await fn()
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

const setup = () => fuehreAus(() => api.post('/mail/setup'))

// Die Zustellbarkeitsprüfung. Sie fragt DNS und dauert deshalb einen Moment —
// darum auf Knopfdruck und nicht bei jedem Aufruf der Seite.
async function pruefen() {
  checkBusy.value = true
  error.value = ''
  try {
    check.value = await api.get('/mail/check')
  } catch (err) {
    error.value = err.message
  } finally {
    checkBusy.value = false
  }
}

// Die Domain-Blacklist-Abfrage (dbl.spamhaus.org) — je Domäne einzeln, weil
// sie sich unabhängig voneinander ändern kann und eine Anfrage nach außen
// ist, die nicht bei jedem Laden der Liste erneut laufen soll.
async function blacklistPruefen(d) {
  blacklistBusy.value = { ...blacklistBusy.value, [d.id]: true }
  error.value = ''
  try {
    await api.post(`/mail/domains/${d.id}/blacklist-check`)
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    blacklistBusy.value = { ...blacklistBusy.value, [d.id]: false }
  }
}

const stufenFarbe = {
  gut: 'var(--status-good)',
  warnung: 'var(--status-warning)',
  kritisch: 'var(--status-critical)',
}

const domainAnlegen = () =>
  fuehreAus(async () => {
    await api.post('/mail/domains', { domain: domainForm.value.trim() })
    domainForm.value = ''
  })

const domainUmschalten = (d) =>
  fuehreAus(() => api.patch(`/mail/domains/${d.id}`, { active: !d.active }))

const catchAllSetzen = (d, wert) =>
  fuehreAus(() => api.patch(`/mail/domains/${d.id}`, { catch_all: wert.trim() }))

const vorgabeQuotaSetzen = (d, wert) =>
  fuehreAus(() => api.patch(`/mail/domains/${d.id}`, { default_quota_mb: Number(wert) || 0 }))

async function domainEntfernen(d) {
  if (!(await askConfirm(t('mail.confirmDeleteDomain', { domain: d.domain })))) return
  fuehreAus(async () => {
    const res = await api.del(`/mail/domains/${d.id}`)
    if (res?.hinweis) alert(res.hinweis)
  })
}

const postfachAnlegen = (d) =>
  fuehreAus(async () => {
    await api.post('/mail/mailboxes', {
      domain_id: d.id,
      local_part: boxForm.value.local_part.trim(),
      password: boxForm.value.password,
      quota_mb: Number(boxForm.value.quota_mb) || 0,
    })
    boxForm.value = { domain_id: null, local_part: '', password: '', quota_mb: 0 }
  })

// Dieselbe Aktion wie postfachAnlegen, nur mit der Domäne aus dem Formular
// des Mailboxen-Reiters statt aus der Karte, in der er steht.
const postfachAnlegenGlobal = () =>
  fuehreAus(async () => {
    await api.post('/mail/mailboxes', {
      domain_id: Number(newBoxForm.value.domain_id),
      local_part: newBoxForm.value.local_part.trim(),
      password: newBoxForm.value.password,
      quota_mb: Number(newBoxForm.value.quota_mb) || 0,
    })
    newBoxForm.value = { domain_id: null, local_part: '', password: '', quota_mb: 0 }
  })

async function passwortZeigen(box) {
  try {
    const res = await api.get(`/mail/mailboxes/${box.id}/password`)
    credentials.value = { address: box.address, password: res.password }
  } catch (err) {
    error.value = err.message
  }
}

const postfachUmschalten = (box) =>
  fuehreAus(() => api.patch(`/mail/mailboxes/${box.id}`, { active: !box.active }))

async function postfachEntfernen(box) {
  if (!(await askConfirm(t('mail.confirmDeleteBox', { address: box.address })))) return
  fuehreAus(async () => {
    const res = await api.del(`/mail/mailboxes/${box.id}`)
    if (res?.hinweis) alert(res.hinweis)
  })
}

const aliasAnlegen = (d) =>
  fuehreAus(async () => {
    await api.post('/mail/aliases', {
      domain_id: d.id,
      source: aliasForm.value.source.trim(),
      destination: aliasForm.value.destination.trim(),
    })
    aliasForm.value = { domain_id: null, source: '', destination: '' }
  })

const aliasEntfernen = (a) => fuehreAus(() => api.del(`/mail/aliases/${a.id}`))

// DKIM. Der Schlüssel entsteht im Panel; hier kommt nur der DNS-Eintrag an —
// der private Teil verlässt den Server nie über HTTP.
async function dkimZeigen(d) {
  try {
    dkim.value = { ...dkim.value, [d.id]: await api.get(`/mail/domains/${d.id}/dkim`) }
  } catch {
    dkim.value = { ...dkim.value, [d.id]: null }
  }
}

// Beim Aufklappen den vorhandenen DKIM-Eintrag nachladen. Er steht nicht in
// der Domänenliste: dort wäre er Ballast in jeder Zeile, und gebraucht wird er
// genau einmal — beim Eintragen im DNS.
function umschalten(d) {
  offen.value = { ...offen.value, [d.id]: !offen.value[d.id] }
  if (offen.value[d.id] && dkim.value[d.id] === undefined) dkimZeigen(d)
}

// Die Einträge gleich setzen, statt sie abzuschreiben. Ein DKIM-Wert mit einem
// falschen Zeichen prüft sich nicht mehr — und die Mail wird dann abgewertet
// statt gar nicht unterschrieben.
const dnsSetzen = (d) =>
  fuehreAus(async () => {
    dnsErgebnis.value = { ...dnsErgebnis.value, [d.id]: await api.post(`/mail/domains/${d.id}/dns`) }
  })

const autoconfigSetzen = (d) =>
  fuehreAus(async () => {
    autoconfigErgebnis.value = {
      ...autoconfigErgebnis.value,
      [d.id]: await api.post(`/mail/domains/${d.id}/autoconfig`),
    }
  })

const dkimAnlegen = (d) =>
  fuehreAus(async () => {
    dkim.value = { ...dkim.value, [d.id]: await api.post(`/mail/domains/${d.id}/dkim`) }
  })

// Kopiert reine Werte (Webmail-Adresse, Domain-Referenz) — nie ein Passwort:
// das bleibt auf "Zeigen" beschränkt, damit sein Abruf im Audit-Log steht.
async function kopieren(text, id) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = id
    setTimeout(() => {
      if (copied.value === id) copied.value = ''
    }, 1500)
  } catch {
    /* Zwischenablage ohne sicheren Kontext nicht verfügbar — kein Absturz wert. */
  }
}

const webmailURL = computed(() => (webmail.value ? `https://${webmail.value.hostname}/` : ''))

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <div>
        <h1 class="text-[18px] font-semibold tracking-tight">{{ t('mail.title') }}</h1>
        <p class="mt-0.5 text-[12px] text-ink-secondary">
          {{ t('mail.subtitle') }}
        </p>
      </div>
    </header>

    <p v-if="error" class="mb-4 text-[13px] text-status-critical" role="alert">
      {{ error }}
    </p>

    <!-- Nicht eingerichtet: ohne Mailspeicher hilft der Rest der Seite niemandem. -->
    <div v-if="status && !bereit" class="panel-card mb-5 p-5">
      <h2 class="mb-1 text-[14px] font-medium">{{ t('mail.setupTitle') }}</h2>
      <p class="mb-3 whitespace-pre-line text-[12px] text-ink-secondary">
        {{ t('mail.setupHint') }}
      </p>
      <InstallHint
        v-if="status && !status.postfix_installed"
        feature="postfix"
        :text="t('mail.needPostfix')"
        @installed="load"
      />
      <InstallHint
        v-if="status && !status.dovecot_installed"
        feature="dovecot"
        :text="t('mail.needDovecot')"
        @installed="load"
      />
      <InstallHint
        v-if="status && !status.opendkim_installed"
        feature="opendkim"
        :text="t('mail.needOpendkim')"
        @installed="load"
      />
      <button
        v-if="isAdmin() && status.postfix_installed"
        :disabled="busy"
        class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent"
        @click="setup"
      >
        {{ busy ? t('mail.settingUp') : t('mail.setup') }}
      </button>
    </div>

    <!-- Ein Passwort steht genau einmal da. Danach nur noch auf Abruf, und
         der Abruf steht im Audit-Log. -->
    <div
      v-if="credentials"
      class="mb-5 rounded-lg border p-4"
      :style="{
        borderColor: 'var(--status-good)',
        background: 'color-mix(in srgb, var(--status-good) 8%, var(--surface-card))',
      }"
    >
      <div class="flex items-center justify-between gap-3">
        <div class="font-mono text-[13px]">
          {{ credentials.address }} · {{ credentials.password }}
        </div>
        <button class="text-[12px] underline text-ink-secondary" @click="credentials = null">
          {{ t('common.close') }}
        </button>
      </div>
      <p class="mt-1 text-[11px] text-ink-muted">
        {{ t('mail.passwordNote') }}
      </p>
    </div>

    <SkeletonCards v-if="loading" :count="3" cols="" height="h-16" />

    <template v-else-if="bereit || !status">
      <!-- Reiter -->
      <nav class="mb-5 flex gap-1 overflow-x-auto border-b border-line-hairline">
        <button
          v-for="item in [
            { key: 'domains', label: t('mail.tabDomains') },
            { key: 'mailboxes', label: t('mail.tabMailboxes') },
            { key: 'settings', label: t('mail.tabSettings') },
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

      <!-- ================= Domänen ================= -->
      <template v-if="tab === 'domains'">
        <div class="panel-card mb-5 flex flex-wrap items-end gap-2 p-4">
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">
              {{ t('mail.newDomain') }}
            </span>
            <input
              v-model="domainForm"
              placeholder="example.at"
              class="w-64 rounded-md border px-3 py-2 text-[13px]"
              :style="inputStyle"
            />
          </label>
          <button
            class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent"
            :disabled="busy || !domainForm.trim()"
            @click="domainAnlegen"
          >
            {{ t('common.create') }}
          </button>
          <p class="w-full text-[11px] text-ink-muted">
            {{ t('mail.dnsHint') }}
          </p>
        </div>

        <p
          v-if="!domains.length"
          class="rounded-lg border p-6 text-center text-[13px]"
          :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
        >
          {{ t('mail.empty') }}
        </p>

        <div v-else class="panel-card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="w-full text-left text-[12px]">
              <thead class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colDomain') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colBlacklist') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colQuota') }}</th>
                  <th class="px-4 py-2.5 text-right font-normal">{{ t('mail.colMailboxCount') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colDefaultQuota') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.catchAll') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colTLS') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colWebmail') }}</th>
                  <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="d in domains" :key="d.id">
                  <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                    <td class="px-4 py-3">
                      <div class="flex items-center gap-2">
                        <span
                          class="h-1.5 w-1.5 shrink-0 rounded-full"
                          :style="{ background: d.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
                          :title="d.active ? t('mail.active') : t('mail.inactive')"
                        ></span>
                        <span class="font-medium">{{ d.domain }}</span>
                      </div>
                    </td>
                    <td class="px-4 py-3">
                      <div v-if="d.blacklist_checked_at" class="flex items-center gap-1.5">
                        <span
                          class="inline-flex items-center gap-1"
                          :style="{
                            color: d.blacklist_status === 'gelistet' ? 'var(--status-critical)' : 'var(--status-good)',
                          }"
                        >
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                            <path v-if="d.blacklist_status === 'gelistet'" d="M18 6L6 18M6 6l12 12" stroke-linecap="round" />
                            <path v-else d="M20 6L9 17l-5-5" stroke-linecap="round" stroke-linejoin="round" />
                          </svg>
                          {{ d.blacklist_status === 'gelistet' ? t('mail.blacklisted') : t('mail.notBlacklisted') }}
                        </span>
                        <button
                          class="text-ink-muted hover:opacity-70"
                          :disabled="blacklistBusy[d.id]"
                          :title="t('mail.blacklistRefresh')"
                          @click="blacklistPruefen(d)"
                        >
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                            <path d="M3 12a9 9 0 0115.4-6.4M21 12a9 9 0 01-15.4 6.4M21 5v5h-5M3 19v-5h5" />
                          </svg>
                        </button>
                      </div>
                      <button
                        v-else
                        class="underline text-ink-secondary disabled:opacity-60"
                        :disabled="blacklistBusy[d.id]"
                        @click="blacklistPruefen(d)"
                      >
                        {{ blacklistBusy[d.id] ? t('mail.checking') : t('mail.blacklistCheck') }}
                      </button>
                      <div v-if="d.blacklist_checked_at" class="mt-0.5 text-[10px] text-ink-muted">
                        {{ new Date(d.blacklist_checked_at * 1000).toLocaleString() }}
                      </div>
                    </td>
                    <td class="tabular px-4 py-3 text-ink-secondary">
                      {{ formatBytes(domainQuota(d.id).used) }} /
                      {{ domainQuota(d.id).unbegrenzt ? t('mail.noQuota') : formatBytes(domainQuota(d.id).limitBytes) }}
                    </td>
                    <td class="tabular px-4 py-3 text-right">{{ boxenVon(d.id).length }}</td>
                    <td class="px-4 py-3">
                      <input
                        :value="d.default_quota_mb || ''"
                        type="number"
                        min="0"
                        :placeholder="t('mail.noQuota')"
                        class="w-20 rounded-md border px-2 py-1 text-[12px]"
                        :style="inputStyle"
                        @change="vorgabeQuotaSetzen(d, $event.target.value)"
                      />
                      <span class="ml-1 text-[10px] text-ink-muted">MB</span>
                    </td>
                    <td class="px-4 py-3">
                      <input
                        :value="d.catch_all"
                        :placeholder="t('mail.none')"
                        class="w-40 rounded-md border px-2 py-1 font-mono text-[11px]"
                        :style="inputStyle"
                        @change="catchAllSetzen(d, $event.target.value)"
                      />
                    </td>
                    <td class="px-4 py-3 whitespace-nowrap">
                      <span
                        class="inline-flex items-center gap-1"
                        :style="{ color: mailTLS === null ? 'var(--ink-muted)' : mailTLS ? 'var(--status-good)' : 'var(--status-critical)' }"
                      >
                        {{ mailTLS === null ? t('mail.unknown') : mailTLS ? t('common.yes') : t('common.no') }}
                      </span>
                    </td>
                    <td class="px-4 py-3 whitespace-nowrap">
                      <span :style="{ color: webmail ? 'var(--status-good)' : 'var(--ink-muted)' }">
                        {{ webmail ? t('mail.installed') : t('mail.notInstalled') }}
                      </span>
                    </td>
                    <td class="px-4 py-3 text-right whitespace-nowrap">
                      <button class="underline text-ink-secondary" @click="umschalten(d)">
                        {{ offen[d.id] ? t('common.close') : t('mail.manage') }}
                      </button>
                      <button class="ml-3 underline text-ink-secondary" :disabled="busy" @click="domainUmschalten(d)">
                        {{ d.active ? t('mail.disable') : t('mail.enable') }}
                      </button>
                      <button class="ml-3 underline text-status-critical" :disabled="busy" @click="domainEntfernen(d)">
                        {{ t('common.delete') }}
                      </button>
                    </td>
                  </tr>

                  <!-- Verwalten: DKIM, DNS, Autoconfig, Weiterleitungen — je Domäne genug
                       Inhalt für eine eigene Zeile, die sich über die ganze Breite spannt. -->
                  <tr v-if="offen[d.id]" class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                    <td colspan="9" class="px-4 py-4" :style="{ background: 'var(--surface-sunken)' }">
                      <div class="grid gap-4 lg:grid-cols-2">
                        <!-- Postfächer dieser Domäne -->
                        <section>
                          <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.mailboxes') }}</h3>
                          <ul class="space-y-1">
                            <li
                              v-for="b in boxenVon(d.id)"
                              :key="b.id"
                              class="flex flex-wrap items-center gap-3 text-[12px]"
                            >
                              <span
                                class="h-1.5 w-1.5 shrink-0 rounded-full"
                                :style="{ background: b.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
                              ></span>
                              <code class="font-mono">{{ b.address }}</code>
                              <span class="text-ink-muted">
                                {{ b.usage_known ? formatBytes(b.used_bytes) + ' / ' : '' }}{{ b.quota_mb ? b.quota_mb + ' MB' : t('mail.noQuota') }}
                              </span>
                              <button class="underline text-ink-secondary" @click="passwortZeigen(b)">
                                {{ t('mail.showPassword') }}
                              </button>
                              <button class="underline text-ink-secondary" :disabled="busy" @click="postfachUmschalten(b)">
                                {{ b.active ? t('mail.disable') : t('mail.enable') }}
                              </button>
                              <button class="underline text-status-critical" :disabled="busy" @click="postfachEntfernen(b)">
                                {{ t('common.delete') }}
                              </button>
                            </li>
                          </ul>

                          <div class="mt-2 flex flex-wrap items-end gap-2">
                            <input
                              v-model="boxForm.local_part"
                              :placeholder="t('mail.localPart')"
                              class="w-32 rounded-md border px-2 py-1 text-[12px]"
                              :style="inputStyle"
                            />
                            <input
                              v-model="boxForm.password"
                              type="password"
                              minlength="10"
                              required
                              :placeholder="t('mail.password')"
                              class="w-44 rounded-md border px-2 py-1 text-[12px]"
                              :style="inputStyle"
                            />
                            <input
                              v-model.number="boxForm.quota_mb"
                              type="number"
                              min="0"
                              :placeholder="t('mail.quota')"
                              class="w-24 rounded-md border px-2 py-1 text-[12px]"
                              :style="inputStyle"
                            />
                            <button
                              class="rounded-md border px-2 py-1 text-[12px]"
                              :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                              :disabled="busy || !boxForm.local_part.trim() || boxForm.password.length < 10"
                              @click="postfachAnlegen(d)"
                            >
                              {{ t('mail.addMailbox') }}
                            </button>
                          </div>
                        </section>

                        <!-- Weiterleitungen -->
                        <section>
                          <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.aliases') }}</h3>
                          <ul class="space-y-1">
                            <li
                              v-for="a in aliaseVon(d.id)"
                              :key="a.id"
                              class="flex flex-wrap items-center gap-3 text-[12px]"
                            >
                              <code class="font-mono">{{ a.source }} → {{ a.destination }}</code>
                              <button class="underline text-status-critical" :disabled="busy" @click="aliasEntfernen(a)">
                                {{ t('common.delete') }}
                              </button>
                            </li>
                          </ul>

                          <div class="mt-2 flex flex-wrap items-end gap-2">
                            <input
                              v-model="aliasForm.source"
                              :placeholder="'info@' + d.domain"
                              class="w-40 rounded-md border px-2 py-1 font-mono text-[12px]"
                              :style="inputStyle"
                            />
                            <span class="text-ink-muted">→</span>
                            <input
                              v-model="aliasForm.destination"
                              :placeholder="'post@' + d.domain"
                              class="w-40 rounded-md border px-2 py-1 font-mono text-[12px]"
                              :style="inputStyle"
                            />
                            <button
                              class="rounded-md border px-2 py-1 text-[12px]"
                              :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                              :disabled="busy || !aliasForm.source.trim() || !aliasForm.destination.trim()"
                              @click="aliasAnlegen(d)"
                            >
                              {{ t('mail.addAlias') }}
                            </button>
                          </div>
                        </section>

                        <!-- DKIM -->
                        <section class="lg:col-span-2">
                          <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.dkim') }}</h3>
                          <template v-if="dkim[d.id]">
                            <p class="mb-1 text-[11px] text-ink-secondary">{{ t('mail.dkimRecord') }}</p>
                            <div class="rounded-md p-2 font-mono text-[11px]" :style="{ background: 'var(--surface-card)', color: 'var(--ink-secondary)' }">
                              <div>TXT &nbsp;{{ dkim[d.id].name }}</div>
                              <div class="mt-1 break-all">{{ dkim[d.id].value }}</div>
                            </div>
                            <p class="mt-1 text-[11px] text-ink-muted">{{ t('mail.dkimHint') }}</p>
                            <div class="mt-2 flex flex-wrap items-center gap-2">
                              <button
                                class="rounded-md border px-2 py-1 text-[12px]"
                                :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                                :disabled="busy"
                                @click="dnsSetzen(d)"
                              >
                                {{ t('mail.dnsPublish') }}
                              </button>
                              <span class="text-[11px] text-ink-muted">{{ t('mail.dnsPublishHint') }}</span>
                            </div>
                            <ul v-if="dnsErgebnis[d.id]" class="mt-2 space-y-0.5 text-[11px]">
                              <li v-for="(e, i) in dnsErgebnis[d.id]" :key="i" class="flex items-center gap-2">
                                <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: stufenFarbe[e.status] }"></span>
                                <span class="font-medium">{{ e.name }}</span>
                                <span class="text-ink-secondary">{{ e.text }}</span>
                              </li>
                            </ul>
                            <div class="mt-3 flex flex-wrap items-center gap-2">
                              <button
                                class="rounded-md border px-2 py-1 text-[12px]"
                                :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                                :disabled="busy"
                                @click="autoconfigSetzen(d)"
                              >
                                {{ t('mail.autoconfigPublish') }}
                              </button>
                              <span class="text-[11px] text-ink-muted">{{ t('mail.autoconfigPublishHint') }}</span>
                            </div>
                            <ul v-if="autoconfigErgebnis[d.id]" class="mt-2 space-y-0.5 text-[11px]">
                              <li v-for="(e, i) in autoconfigErgebnis[d.id]" :key="i" class="flex items-center gap-2">
                                <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: stufenFarbe[e.status] }"></span>
                                <span class="font-medium">{{ e.name }}</span>
                                <span class="text-ink-secondary">{{ e.text }}</span>
                              </li>
                            </ul>
                          </template>
                          <template v-else>
                            <p class="mb-2 text-[11px] text-ink-muted">{{ t('mail.dkimNone') }}</p>
                            <button
                              class="rounded-md border px-2 py-1 text-[12px]"
                              :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                              :disabled="busy"
                              @click="dkimAnlegen(d)"
                            >
                              {{ t('mail.dkimCreate') }}
                            </button>
                          </template>
                        </section>
                      </div>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
        </div>
      </template>

      <!-- ================= Postfächer ================= -->
      <template v-else-if="tab === 'mailboxes'">
        <div class="panel-card mb-5 flex flex-wrap items-end gap-2 p-4">
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('mail.newDomain') }}</span>
            <select v-model="newBoxForm.domain_id" class="w-48 rounded-md border px-2 py-2 text-[12px]" :style="inputStyle">
              <option :value="null" disabled>{{ t('mail.chooseDomain') }}</option>
              <option v-for="d in domains" :key="d.id" :value="d.id">{{ d.domain }}</option>
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('mail.localPart') }}</span>
            <input v-model="newBoxForm.local_part" class="w-32 rounded-md border px-2 py-2 text-[12px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('mail.password') }}</span>
            <input v-model="newBoxForm.password" type="password" minlength="10" class="w-44 rounded-md border px-2 py-2 text-[12px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[12px] text-ink-secondary">{{ t('mail.quota') }}</span>
            <input v-model.number="newBoxForm.quota_mb" type="number" min="0" class="w-20 rounded-md border px-2 py-2 text-[12px]" :style="inputStyle" />
          </label>
          <button
            class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent"
            :disabled="busy || !newBoxForm.domain_id || !newBoxForm.local_part.trim() || newBoxForm.password.length < 10"
            @click="postfachAnlegenGlobal"
          >
            {{ t('mail.addMailbox') }}
          </button>
        </div>

        <div class="mb-3 flex flex-wrap items-center gap-2">
          <select v-model="mailboxFilter" class="rounded-md border px-2 py-1.5 text-[12px]" :style="inputStyle">
            <option value="">{{ t('mail.allDomains') }}</option>
            <option v-for="d in domains" :key="d.id" :value="d.id">{{ d.domain }}</option>
          </select>
          <input
            v-model="mailboxSearch"
            :placeholder="t('mail.searchAddress')"
            class="w-56 rounded-md border px-2 py-1.5 text-[12px]"
            :style="inputStyle"
          />
        </div>

        <p
          v-if="!filteredBoxes.length"
          class="rounded-lg border p-6 text-center text-[13px]"
          :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
        >
          {{ t('mail.mailboxesEmpty') }}
        </p>

        <div v-else class="panel-card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="w-full text-left text-[12px]">
              <thead class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colUsername') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colPassword') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colLoginInfo') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('mail.colUsage') }}</th>
                  <th class="px-4 py-2.5 font-normal">{{ t('common.status') }}</th>
                  <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="b in filteredBoxes" :key="b.id" class="border-b last:border-0" :style="{ borderColor: 'var(--line-hairline)' }">
                  <td class="px-4 py-3">
                    <div class="font-mono font-medium">{{ b.address }}</div>
                    <div class="text-[10px] text-ink-muted">{{ domainByID[b.domain_id]?.domain }}</div>
                  </td>
                  <td class="px-4 py-3">
                    <button
                      class="inline-flex items-center gap-1.5 underline text-ink-secondary"
                      @click="passwortZeigen(b)"
                    >
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                        <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7z" />
                        <circle cx="12" cy="12" r="3" />
                      </svg>
                      {{ t('mail.showPassword') }}
                    </button>
                  </td>
                  <td class="px-4 py-3">
                    <button
                      v-if="webmailURL"
                      class="underline text-ink-secondary"
                      @click="kopieren(webmailURL, 'wm' + b.id)"
                    >
                      {{ copied === 'wm' + b.id ? t('mail.copied') : t('mail.copyLoginLink') }}
                    </button>
                    <span v-else class="text-ink-muted">—</span>
                  </td>
                  <td class="tabular px-4 py-3 text-ink-secondary">
                    {{ b.usage_known ? formatBytes(b.used_bytes) : '—' }} /
                    {{ b.quota_mb ? formatBytes(b.quota_mb * 1024 * 1024) : t('mail.noQuota') }}
                  </td>
                  <td class="px-4 py-3">
                    <span class="inline-flex items-center gap-1.5" :style="{ color: b.active ? 'var(--status-good)' : 'var(--ink-muted)' }">
                      <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: b.active ? 'var(--status-good)' : 'var(--line-axis)' }"></span>
                      {{ b.active ? t('mail.active') : t('mail.inactive') }}
                    </span>
                  </td>
                  <td class="px-4 py-3 text-right whitespace-nowrap">
                    <a v-if="webmailURL" :href="webmailURL" target="_blank" rel="noopener" class="underline text-ink-secondary">
                      {{ t('mail.webmail') }}
                    </a>
                    <button class="ml-3 underline text-ink-secondary" :disabled="busy" @click="postfachUmschalten(b)">
                      {{ b.active ? t('mail.disable') : t('mail.enable') }}
                    </button>
                    <button class="ml-3 underline text-status-critical" :disabled="busy" @click="postfachEntfernen(b)">
                      {{ t('common.delete') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>

      <!-- ================= Weitere Einstellungen ================= -->
      <template v-else>
        <div class="grid gap-4 lg:grid-cols-2">
          <!-- Was in ein Mailprogramm gehört. -->
          <section v-if="settings" class="panel-card p-4">
            <h2 class="mb-3 text-[14px] font-medium">{{ t('mail.clientTitle') }}</h2>
            <table class="text-[12px]">
              <tbody>
                <tr>
                  <td class="py-0.5 pr-4 text-ink-muted">IMAP</td>
                  <td class="py-0.5 font-mono">{{ settings.host }}:{{ settings.imap_port }} · {{ settings.imap_encryption }}</td>
                </tr>
                <tr>
                  <td class="py-0.5 pr-4 text-ink-muted">SMTP</td>
                  <td class="py-0.5 font-mono">{{ settings.host }}:{{ settings.smtp_port }} · {{ settings.smtp_encryption }}</td>
                </tr>
                <tr>
                  <td class="py-0.5 pr-4 text-ink-muted">{{ t('mail.clientUser') }}</td>
                  <td class="py-0.5">{{ t('mail.clientUserValue') }}</td>
                </tr>
                <tr v-if="webmail">
                  <td class="py-0.5 pr-4 text-ink-muted">Webmail</td>
                  <td class="py-0.5">
                    <a :href="webmailURL" target="_blank" rel="noopener" class="underline">{{ webmailURL }}</a>
                  </td>
                </tr>
              </tbody>
            </table>
          </section>

          <!-- Was Rspamd tatsächlich aussortiert. -->
          <section v-if="spam && spam.installed" class="panel-card p-4">
            <h2 class="mb-2 text-[14px] font-medium">{{ t('mail.spamTitle') }}</h2>
            <p v-if="!spam.reachable" class="text-[12px] text-ink-muted">
              {{ spam.hinweis || t('mail.spamUnreachable') }}
            </p>
            <template v-else>
              <div class="flex flex-wrap gap-x-6 gap-y-1 text-[12px]">
                <span>{{ t('mail.spamScanned') }}: <strong>{{ spam.scanned }}</strong></span>
                <span :style="{ color: 'var(--status-critical)' }">
                  {{ t('mail.spamCount') }}: <strong>{{ spam.spam_count }}</strong>
                </span>
                <span :style="{ color: 'var(--status-good)' }">
                  {{ t('mail.hamCount') }}: <strong>{{ spam.ham_count }}</strong>
                </span>
              </div>
              <div v-if="spam.actions && Object.keys(spam.actions).length" class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-ink-muted">
                <span v-for="(n, name) in spam.actions" :key="name">{{ name }}: {{ n }}</span>
              </div>
            </template>
          </section>

          <!-- Zustellbarkeit -->
          <section class="panel-card p-4 lg:col-span-2">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 class="text-[14px] font-medium">{{ t('mail.checkTitle') }}</h2>
                <p class="mt-0.5 text-[11px] text-ink-muted">{{ t('mail.checkHint') }}</p>
              </div>
              <button
                class="rounded-md border px-3 py-1.5 text-[12px] disabled:opacity-60"
                :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                :disabled="checkBusy"
                @click="pruefen"
              >
                {{ checkBusy ? t('mail.checking') : t('mail.check') }}
              </button>
            </div>

            <table v-if="check" class="mt-3 w-full text-[12px]">
              <tbody>
                <tr v-for="(b, i) in check.befunde" :key="i" class="align-top">
                  <td class="w-24 py-1 pr-2">
                    <span class="inline-flex items-center gap-1.5">
                      <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: stufenFarbe[b.stufe] }"></span>
                      <span class="text-ink-muted">{{ t('mail.level.' + b.stufe) }}</span>
                    </span>
                  </td>
                  <td class="w-28 py-1 pr-2 font-medium">
                    {{ b.was }}
                    <div v-if="b.domain" class="font-normal text-ink-muted">{{ b.domain }}</div>
                  </td>
                  <td class="py-1">
                    <div>{{ b.text }}</div>
                    <div v-if="b.rat" class="mt-0.5 text-[11px] text-ink-muted">{{ b.rat }}</div>
                  </td>
                </tr>
              </tbody>
            </table>
          </section>
        </div>
      </template>
    </template>
  </div>
</template>
