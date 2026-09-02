<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

// Firewall und Fail2ban. Beides betrifft den ganzen Server, nicht eine Site —
// der Endpunkt lehnt jeden ab, der kein Administrator ist, und diese Komponente
// wird auch nur dort eingebunden.
const fw = ref(null)
const f2b = ref(null)
const error = ref('')
const busy = ref(false)
const form = ref({ action: 'allow', port: null, port_to: null, proto: 'tcp' })

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

async function load() {
  error.value = ''
  const [a, b] = await Promise.all([
    api.get('/system/firewall').catch((e) => ({ hinweis: e.message })),
    api.get('/system/fail2ban').catch((e) => ({ hinweis: e.message })),
  ])
  fw.value = a
  f2b.value = b
}

async function regelSetzen(remove) {
  busy.value = true
  error.value = ''
  try {
    await api.post('/system/firewall', {
      action: form.value.action,
      port: Number(form.value.port) || 0,
      port_to: Number(form.value.port_to) || 0,
      proto: form.value.proto,
      remove,
    })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function entsperren(jail, ip) {
  busy.value = true
  error.value = ''
  try {
    await api.post('/system/fail2ban/unban', { jail, ip })
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <section>
    <h2 class="mb-3 text-[14px] font-medium">{{ t('fw.title') }}</h2>

    <p
      v-if="error"
      class="mb-3 text-[12px]"
      :style="{ color: 'var(--status-critical)' }"
      role="alert"
    >
      {{ error }}
    </p>

    <div
      class="rounded-lg border p-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <div v-if="fw" class="text-[12px]">
        <div class="mb-2 flex items-center gap-2">
          <span
            class="h-1.5 w-1.5 rounded-full"
            :style="{ background: fw.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
          ></span>
          <span :style="{ color: 'var(--ink-primary)' }">
            {{ fw.backend || t('fw.none') }}
            <template v-if="fw.backend">
              — {{ fw.active ? t('fw.on') : t('fw.off') }}
            </template>
          </span>
        </div>

        <p v-if="fw.hinweis" class="mb-3 text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
          {{ fw.hinweis }}
        </p>

        <ul v-if="fw.rules && fw.rules.length" class="mb-3 space-y-1">
          <li
            v-for="(r, i) in fw.rules"
            :key="i"
            class="font-mono text-[11px]"
            :style="{ color: 'var(--ink-secondary)' }"
          >
            {{ r.raw }}
          </li>
        </ul>

        <!--
          Die Regel geht in Teilen über die Leitung, nicht als Text. Ein
          Textfeld hieße, ufws eigene Sprache durchzureichen — und in der ist
          allerlei gültig, was hier niemand gemeint hat.
        -->
        <div v-if="fw.writable" class="flex flex-wrap items-end gap-2">
          <label class="block">
            <span class="mb-1 block text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('fw.action') }}
            </span>
            <select v-model="form.action" class="rounded-md border px-2 py-1 text-[12px]"
                    :style="inputStyle">
              <option value="allow">{{ t('fw.allow') }}</option>
              <option value="deny">{{ t('fw.deny') }}</option>
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('fw.port') }}
            </span>
            <input v-model.number="form.port" type="number" min="1" max="65535"
                   class="w-24 rounded-md border px-2 py-1 text-[12px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('fw.portTo') }}
            </span>
            <input v-model.number="form.port_to" type="number" min="0" max="65535"
                   class="w-24 rounded-md border px-2 py-1 text-[12px]" :style="inputStyle" />
          </label>
          <label class="block">
            <span class="mb-1 block text-[11px]" :style="{ color: 'var(--ink-secondary)' }">
              {{ t('fw.proto') }}
            </span>
            <select v-model="form.proto" class="rounded-md border px-2 py-1 text-[12px]"
                    :style="inputStyle">
              <option value="tcp">tcp</option>
              <option value="udp">udp</option>
            </select>
          </label>
          <button
            class="rounded-md border px-2 py-1 text-[12px]"
            :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
            :disabled="busy || !form.port"
            @click="regelSetzen(false)"
          >
            {{ t('fw.set') }}
          </button>
          <button
            class="rounded-md border px-2 py-1 text-[12px]"
            :style="{ borderColor: 'var(--border-ring)', color: 'var(--status-critical)' }"
            :disabled="busy || !form.port"
            @click="regelSetzen(true)"
          >
            {{ t('fw.remove') }}
          </button>
        </div>
      </div>
    </div>

    <h2 class="mb-3 mt-6 text-[14px] font-medium">{{ t('fw.f2bTitle') }}</h2>

    <div
      class="rounded-lg border p-4 text-[12px]"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <p v-if="f2b && f2b.hinweis" :style="{ color: 'var(--ink-secondary)' }">
        {{ f2b.hinweis }}
      </p>

      <p
        v-else-if="f2b && !f2b.jails.length"
        :style="{ color: 'var(--ink-secondary)' }"
      >
        {{ t('fw.noJails') }}
      </p>

      <div v-else-if="f2b" class="space-y-3">
        <div v-for="jail in f2b.jails" :key="jail.name">
          <div class="flex items-baseline gap-2">
            <code class="font-mono">{{ jail.name }}</code>
            <span :style="{ color: 'var(--ink-muted)' }">
              {{ t('fw.banned', { n: jail.currently, total: jail.total }) }}
            </span>
          </div>
          <ul v-if="jail.banned.length" class="mt-1 space-y-0.5">
            <li v-for="ip in jail.banned" :key="ip" class="flex items-center gap-2 text-[11px]">
              <code class="font-mono">{{ ip }}</code>
              <button
                class="underline"
                :style="{ color: 'var(--ink-secondary)' }"
                :disabled="busy"
                @click="entsperren(jail.name, ip)"
              >
                {{ t('fw.unban') }}
              </button>
            </li>
          </ul>
        </div>
      </div>
    </div>
  </section>
</template>
