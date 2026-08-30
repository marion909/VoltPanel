<script setup>
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '../api'
import { session, setUser, loadSession } from '../stores/session'
import { t } from '../i18n'

const router = useRouter()
const route = useRoute()

const email = ref('')
const password = ref('')
const totpCode = ref('')
const totpRequired = ref(false)
const error = ref('')
const busy = ref(false)

onMounted(() => {
  if (session.user) router.replace('/')
})

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const res = await api.post('/auth/login', {
      email: email.value,
      password: password.value,
      totp_code: totpCode.value,
    })

    // Der Server verlangt den zweiten Faktor, statt einen Fehler zu melden:
    // die Zugangsdaten stimmen, es fehlt nur noch der Code.
    if (res.totp_required) {
      totpRequired.value = true
      return
    }

    setUser(res.user)
    await loadSession()
    router.replace(route.query.redirect || '/')
  } catch (err) {
    error.value = err.message
    totpCode.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center px-4">
    <div class="fade-in w-full max-w-sm">
      <div class="mb-6 flex items-center justify-center gap-2">
        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M13 2L4.5 13.5H11l-1 8.5 8.5-11.5H12z" fill="var(--series-1)" />
        </svg>
        <span class="text-[19px] font-semibold tracking-tight">VoltPanel</span>
      </div>

      <form
        class="rounded-xl border p-6"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        @submit.prevent="submit"
      >
        <h1 class="mb-5 text-[15px] font-medium">{{ t('login.title') }}</h1>

        <p
          v-if="session.setupRequired"
          class="mb-4 rounded-md px-3 py-2 text-[12px]"
          :style="{
            background: 'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
            color: 'var(--ink-secondary)',
          }"
        >
          {{ t('login.setupHint') }}
        </p>

        <label class="mb-3 block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('login.email') }}
          </span>
          <input
            v-model="email"
            type="email"
            autocomplete="username"
            required
            :disabled="totpRequired"
            class="w-full rounded-md border px-3 py-2 text-[13px] outline-none focus:ring-2"
            :style="{
              borderColor: 'var(--line-axis)',
              background: 'var(--surface-page)',
              color: 'var(--ink-primary)',
            }"
          />
        </label>

        <label class="mb-3 block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('login.password') }}
          </span>
          <input
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            :disabled="totpRequired"
            class="w-full rounded-md border px-3 py-2 text-[13px] outline-none focus:ring-2"
            :style="{
              borderColor: 'var(--line-axis)',
              background: 'var(--surface-page)',
              color: 'var(--ink-primary)',
            }"
          />
        </label>

        <label v-if="totpRequired" class="mb-3 block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('login.totp') }}
          </span>
          <input
            v-model="totpCode"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            required
            autofocus
            class="tabular w-full rounded-md border px-3 py-2 text-center text-[16px] tracking-[0.3em] outline-none focus:ring-2"
            :style="{
              borderColor: 'var(--line-axis)',
              background: 'var(--surface-page)',
              color: 'var(--ink-primary)',
            }"
          />
        </label>

        <p
          v-if="error"
          class="mb-3 text-[12px]"
          :style="{ color: 'var(--status-critical)' }"
          role="alert"
        >
          {{ error }}
        </p>

        <button
          type="submit"
          :disabled="busy"
          class="w-full rounded-md px-3 py-2 text-[13px] font-medium text-white transition-opacity disabled:opacity-60"
          :style="{ background: 'var(--series-1)' }"
        >
          {{ busy ? t('common.loading') : t('login.submit') }}
        </button>
      </form>
    </div>
  </div>
</template>
