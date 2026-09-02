<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { isAdmin } from '../stores/session'
import InstallHint from '../components/InstallHint.vue'

// Mail: Domänen, Postfächer, Weiterleitungen.
//
// Eine Maildomäne gehört einem Mandanten — anders als Firewall oder Dienste.
// Deshalb steht diese Ansicht jedem Angemeldeten offen; was er sieht,
// entscheidet der Scope auf dem Server, nicht diese Datei.
const status = ref(null)
const domains = ref([])
const boxes = ref([])
const aliases = ref([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

// Ein frisch gesetztes Passwort steht genau einmal hier.
const credentials = ref(null)

const offen = ref({})
const domainForm = ref('')
const boxForm = ref({ domain_id: null, local_part: '', password: '', quota_mb: 0 })
const aliasForm = ref({ domain_id: null, source: '', destination: '' })
const dkim = ref({})
const check = ref(null)
const dnsErgebnis = ref({})
const settings = ref(null)
const checkBusy = ref(false)

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const bereit = computed(() => status.value?.postfix_installed && status.value?.configured)

const boxenVon = (id) => boxes.value.filter((b) => b.domain_id === id)
const aliaseVon = (id) => aliases.value.filter((a) => a.domain_id === id)

async function load() {
  loading.value = true
  try {
    const [s, d, b, a, cfg] = await Promise.all([
      // Der Zustandsbericht ist Administratoren vorbehalten — für alle
      // anderen bleibt er leer, und die Listen stehen trotzdem.
      api.get('/mail/status').catch(() => null),
      api.get('/mail/domains'),
      api.get('/mail/mailboxes'),
      api.get('/mail/aliases'),
      api.get('/mail/settings').catch(() => null),
    ])
    status.value = s
    domains.value = d
    boxes.value = b
    aliases.value = a
    settings.value = cfg
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

function domainEntfernen(d) {
  if (!confirm(t('mail.confirmDeleteDomain', { domain: d.domain }))) return
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

function postfachEntfernen(box) {
  if (!confirm(t('mail.confirmDeleteBox', { address: box.address }))) return
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

const dkimAnlegen = (d) =>
  fuehreAus(async () => {
    dkim.value = { ...dkim.value, [d.id]: await api.post(`/mail/domains/${d.id}/dkim`) }
  })

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <div>
        <h1 class="text-[18px] font-semibold tracking-tight">{{ t('mail.title') }}</h1>
        <p class="mt-0.5 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('mail.subtitle') }}
        </p>
      </div>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <!--
      Der schwierige Teil eines Mailservers ist nicht der Code, sondern die
      Frage, ob eine Mail im Posteingang landet. Daran hängt ein Dutzend
      Kleinigkeiten, die alle woanders stehen — hier stehen sie zusammen.
    -->
    <div
      v-if="domains.length"
      class="mb-5 rounded-lg border p-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="text-[14px] font-medium">{{ t('mail.checkTitle') }}</h2>
          <p class="mt-0.5 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ t('mail.checkHint') }}
          </p>
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
                <span
                  class="h-1.5 w-1.5 shrink-0 rounded-full"
                  :style="{ background: stufenFarbe[b.stufe] }"
                ></span>
                <!-- Die Stufe steht als Wort da, nicht nur als Farbe. -->
                <span :style="{ color: 'var(--ink-muted)' }">{{ t('mail.level.' + b.stufe) }}</span>
              </span>
            </td>
            <td class="w-28 py-1 pr-2 font-medium">
              {{ b.was }}
              <div v-if="b.domain" class="font-normal" :style="{ color: 'var(--ink-muted)' }">
                {{ b.domain }}
              </div>
            </td>
            <td class="py-1">
              <div>{{ b.text }}</div>
              <div v-if="b.rat" class="mt-0.5 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                {{ b.rat }}
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Nicht eingerichtet: ohne Mailspeicher hilft eine Liste niemandem. -->
    <div
      v-if="status && !bereit"
      class="mb-5 rounded-lg border p-5"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <h2 class="mb-1 text-[14px] font-medium">{{ t('mail.setupTitle') }}</h2>
      <p class="mb-3 whitespace-pre-line text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
        {{ t('mail.setupHint') }}
      </p>
      <!-- Fehlt ein Dienst, steht der Knopf daneben. Ein Hinweis ohne Weg
           weiter schickt den Betreiber auf die Shell. -->
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
        class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
        :style="{ background: 'var(--series-1)' }"
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
        <button
          class="text-[12px] underline"
          :style="{ color: 'var(--ink-secondary)' }"
          @click="credentials = null"
        >
          {{ t('common.close') }}
        </button>
      </div>
      <p class="mt-1 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
        {{ t('mail.passwordNote') }}
      </p>
    </div>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-secondary)' }">
      {{ t('common.loading') }}
    </p>

    <template v-else>
      <!-- Neue Domäne -->
      <div
        class="mb-5 flex flex-wrap items-end gap-2 rounded-lg border p-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
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
          class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
          :style="{ background: 'var(--series-1)' }"
          :disabled="busy || !domainForm.trim()"
          @click="domainAnlegen"
        >
          {{ t('common.create') }}
        </button>
        <p class="w-full text-[11px]" :style="{ color: 'var(--ink-muted)' }">
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

      <div v-else class="space-y-3">
        <article
          v-for="d in domains"
          :key="d.id"
          class="rounded-lg border p-4"
          :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        >
          <header class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex items-center gap-2">
              <span
                class="h-1.5 w-1.5 shrink-0 rounded-full"
                :style="{ background: d.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
                :title="d.active ? t('mail.active') : t('mail.inactive')"
              ></span>
              <span class="text-[14px] font-medium">{{ d.domain }}</span>
              <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                {{ t('mail.counts', { boxes: boxenVon(d.id).length, aliases: aliaseVon(d.id).length }) }}
              </span>
            </div>
            <div class="flex gap-3 text-[11px]">
              <button
                class="underline"
                :style="{ color: 'var(--ink-secondary)' }"
                @click="umschalten(d)"
              >
                {{ offen[d.id] ? t('common.close') : t('mail.manage') }}
              </button>
              <button
                class="underline"
                :style="{ color: 'var(--ink-secondary)' }"
                :disabled="busy"
                @click="domainUmschalten(d)"
              >
                {{ d.active ? t('mail.disable') : t('mail.enable') }}
              </button>
              <button
                class="underline"
                :style="{ color: 'var(--status-critical)' }"
                :disabled="busy"
                @click="domainEntfernen(d)"
              >
                {{ t('common.delete') }}
              </button>
            </div>
          </header>

          <div v-if="offen[d.id]" class="mt-4 space-y-4">
                  <!-- Was in ein Mailprogramm gehört. Ohne diese Angaben steht ein
                 Kunde vor einem angelegten Postfach und weiß nicht, wohin
                 damit — der häufigste Grund für eine Rückfrage, die es nicht
                 bräuchte. -->
            <section v-if="settings">
              <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.clientTitle') }}</h3>
              <table class="text-[12px]">
                <tbody>
                  <tr>
                    <td class="py-0.5 pr-4" :style="{ color: 'var(--ink-muted)' }">IMAP</td>
                    <td class="py-0.5 font-mono">
                      {{ settings.host }}:{{ settings.imap_port }} · {{ settings.imap_encryption }}
                    </td>
                  </tr>
                  <tr>
                    <td class="py-0.5 pr-4" :style="{ color: 'var(--ink-muted)' }">SMTP</td>
                    <td class="py-0.5 font-mono">
                      {{ settings.host }}:{{ settings.smtp_port }} · {{ settings.smtp_encryption }}
                    </td>
                  </tr>
                  <tr>
                    <td class="py-0.5 pr-4" :style="{ color: 'var(--ink-muted)' }">
                      {{ t('mail.clientUser') }}
                    </td>
                    <td class="py-0.5">{{ t('mail.clientUserValue') }}</td>
                  </tr>
                </tbody>
              </table>
            </section>

      <!-- Postfächer -->
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
                  <span :style="{ color: 'var(--ink-muted)' }">
                    {{ b.quota_mb ? b.quota_mb + ' MB' : t('mail.noQuota') }}
                  </span>
                  <button
                    class="underline"
                    :style="{ color: 'var(--ink-secondary)' }"
                    @click="passwortZeigen(b)"
                  >
                    {{ t('mail.showPassword') }}
                  </button>
                  <button
                    class="underline"
                    :style="{ color: 'var(--ink-secondary)' }"
                    :disabled="busy"
                    @click="postfachUmschalten(b)"
                  >
                    {{ b.active ? t('mail.disable') : t('mail.enable') }}
                  </button>
                  <button
                    class="underline"
                    :style="{ color: 'var(--status-critical)' }"
                    :disabled="busy"
                    @click="postfachEntfernen(b)"
                  >
                    {{ t('common.delete') }}
                  </button>
                </li>
              </ul>

              <div class="mt-2 flex flex-wrap items-end gap-2">
                <input
                  v-model="boxForm.local_part"
                  :placeholder="t('mail.localPart')"
                  class="w-40 rounded-md border px-2 py-1 text-[12px]"
                  :style="inputStyle"
                />
                <input
                  v-model="boxForm.password"
                  type="password"
                  :placeholder="t('mail.password')"
                  class="w-52 rounded-md border px-2 py-1 text-[12px]"
                  :style="inputStyle"
                />
                <input
                  v-model.number="boxForm.quota_mb"
                  type="number"
                  min="0"
                  :placeholder="t('mail.quota')"
                  class="w-28 rounded-md border px-2 py-1 text-[12px]"
                  :style="inputStyle"
                />
                <button
                  class="rounded-md border px-2 py-1 text-[12px]"
                  :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                  :disabled="busy || !boxForm.local_part.trim()"
                  @click="postfachAnlegen(d)"
                >
                  {{ t('mail.addMailbox') }}
                </button>
              </div>
            </section>

            <!-- Catch-All -->
            <section>
              <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.catchAll') }}</h3>
              <div class="flex flex-wrap items-center gap-2">
                <input
                  :value="d.catch_all"
                  :placeholder="t('mail.catchAllNone')"
                  class="w-64 rounded-md border px-2 py-1 font-mono text-[12px]"
                  :style="inputStyle"
                  @change="catchAllSetzen(d, $event.target.value)"
                />
                <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                  {{ t('mail.catchAllHint') }}
                </span>
              </div>
            </section>

            <!-- DKIM -->
            <section>
              <h3 class="mb-2 text-[12px] font-medium">{{ t('mail.dkim') }}</h3>
              <template v-if="dkim[d.id]">
                <p class="mb-1 text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
                  {{ t('mail.dkimRecord') }}
                </p>
                <div
                  class="rounded-md p-2 font-mono text-[11px]"
                  :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
                >
                  <div>TXT &nbsp;{{ dkim[d.id].name }}</div>
                  <div class="mt-1 break-all">{{ dkim[d.id].value }}</div>
                </div>
                <p class="mt-1 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                  {{ t('mail.dkimHint') }}
                </p>
                <div class="mt-2 flex flex-wrap items-center gap-2">
                  <button
                    class="rounded-md border px-2 py-1 text-[12px]"
                    :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
                    :disabled="busy"
                    @click="dnsSetzen(d)"
                  >
                    {{ t('mail.dnsPublish') }}
                  </button>
                  <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                    {{ t('mail.dnsPublishHint') }}
                  </span>
                </div>
                <ul v-if="dnsErgebnis[d.id]" class="mt-2 space-y-0.5 text-[11px]">
                  <li
                    v-for="(e, i) in dnsErgebnis[d.id]"
                    :key="i"
                    class="flex items-center gap-2"
                  >
                    <span
                      class="h-1.5 w-1.5 shrink-0 rounded-full"
                      :style="{ background: stufenFarbe[e.status] }"
                    ></span>
                    <span class="font-medium">{{ e.name }}</span>
                    <span :style="{ color: 'var(--ink-secondary)' }">{{ e.text }}</span>
                  </li>
                </ul>
              </template>
              <template v-else>
                <p class="mb-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                  {{ t('mail.dkimNone') }}
                </p>
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
                  <button
                    class="underline"
                    :style="{ color: 'var(--status-critical)' }"
                    :disabled="busy"
                    @click="aliasEntfernen(a)"
                  >
                    {{ t('common.delete') }}
                  </button>
                </li>
              </ul>

              <div class="mt-2 flex flex-wrap items-end gap-2">
                <input
                  v-model="aliasForm.source"
                  :placeholder="'info@' + d.domain"
                  class="w-56 rounded-md border px-2 py-1 font-mono text-[12px]"
                  :style="inputStyle"
                />
                <span :style="{ color: 'var(--ink-muted)' }">→</span>
                <input
                  v-model="aliasForm.destination"
                  :placeholder="'post@' + d.domain"
                  class="w-56 rounded-md border px-2 py-1 font-mono text-[12px]"
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
          </div>
        </article>
      </div>
    </template>
  </div>
</template>
