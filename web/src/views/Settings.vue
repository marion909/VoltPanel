<script setup>
import { ref } from "vue";
import { api } from "../api";
import { session, setUser } from "../stores/session";
import { theme, setTheme } from "../stores/theme";
import { t, i18n, setLocale } from "../i18n";
import UpdateCard from "../components/UpdateCard.vue";

const currentPassword = ref("");
const newPassword = ref("");
const pwMessage = ref("");
const pwError = ref("");

const totpSecret = ref(null);
const totpQR = ref(null);
const totpCode = ref("");
const totpError = ref("");

const inputStyle = {
  borderColor: "var(--line-axis)",
  background: "var(--surface-page)",
  color: "var(--ink-primary)",
};

async function changePassword() {
  pwError.value = "";
  pwMessage.value = "";
  try {
    const res = await api.post("/auth/password", {
      current_password: currentPassword.value,
      new_password: newPassword.value,
    });
    setUser(res.user);
    currentPassword.value = "";
    newPassword.value = "";
    pwMessage.value =
      "Passwort geändert. Alle anderen Sitzungen wurden beendet.";
  } catch (err) {
    pwError.value = err.message;
  }
}

async function setupTOTP() {
  totpError.value = "";
  try {
    const res = await api.post("/auth/2fa/setup");
    totpSecret.value = res.secret;
    totpQR.value = res.qr_code;
  } catch (err) {
    totpError.value = err.message;
  }
}

async function enableTOTP() {
  totpError.value = "";
  try {
    await api.post("/auth/2fa/enable", { code: totpCode.value });
    session.user.totp_enabled = true;
    totpSecret.value = null;
    totpQR.value = null;
    totpCode.value = "";
  } catch (err) {
    totpError.value = err.message;
  }
}

async function disableTOTP() {
  totpError.value = "";
  try {
    await api.post("/auth/2fa/disable", { code: totpCode.value });
    session.user.totp_enabled = false;
    totpCode.value = "";
  } catch (err) {
    totpError.value = err.message;
  }
}
</script>

<template>
  <div class="fade-in max-w-2xl px-8 py-6">
    <h1 class="mb-5 text-[18px] font-semibold tracking-tight">
      {{ t("settings.title") }}
    </h1>

    <UpdateCard />

    <section
      class="mb-4 rounded-lg border p-5"
      :style="{
        borderColor: 'var(--border-ring)',
        background: 'var(--surface-card)',
      }"
    >
      <h2 class="mb-3 text-[14px] font-medium">{{ t("settings.account") }}</h2>
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-[13px]">
        <dt :style="{ color: 'var(--ink-muted)' }">E-Mail</dt>
        <dd>{{ session.user.email }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">Rolle</dt>
        <dd>{{ session.user.role }}</dd>
        <dt :style="{ color: 'var(--ink-muted)' }">Tenant</dt>
        <dd>{{ session.tenant?.name }}</dd>
      </dl>
    </section>

    <section
      class="mb-4 rounded-lg border p-5"
      :style="{
        borderColor: 'var(--border-ring)',
        background: 'var(--surface-card)',
      }"
    >
      <h2 class="mb-3 text-[14px] font-medium">{{ t("settings.password") }}</h2>
      <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="changePassword">
        <label class="block">
          <span
            class="mb-1 block text-[12px]"
            :style="{ color: 'var(--ink-secondary)' }"
          >
            {{ t("settings.currentPassword") }}
          </span>
          <input
            v-model="currentPassword"
            type="password"
            autocomplete="current-password"
            required
            class="w-full rounded-md border px-3 py-2 text-[13px]"
            :style="inputStyle"
          />
        </label>
        <label class="block">
          <span
            class="mb-1 block text-[12px]"
            :style="{ color: 'var(--ink-secondary)' }"
          >
            {{ t("settings.newPassword") }}
          </span>
          <input
            v-model="newPassword"
            type="password"
            autocomplete="new-password"
            required
            class="w-full rounded-md border px-3 py-2 text-[13px]"
            :style="inputStyle"
          />
        </label>
        <div class="sm:col-span-2">
          <button
            type="submit"
            class="rounded-md px-3 py-2 text-[13px] font-medium text-white"
            :style="{ background: 'var(--series-1)' }"
          >
            {{ t("common.save") }}
          </button>
          <span
            v-if="pwMessage"
            class="ml-3 text-[12px]"
            :style="{ color: 'var(--status-good)' }"
          >
            {{ pwMessage }}
          </span>
          <span
            v-if="pwError"
            class="ml-3 text-[12px]"
            :style="{ color: 'var(--status-critical)' }"
          >
            {{ pwError }}
          </span>
        </div>
      </form>
    </section>

    <section
      class="mb-4 rounded-lg border p-5"
      :style="{
        borderColor: 'var(--border-ring)',
        background: 'var(--surface-card)',
      }"
    >
      <h2 class="mb-1 text-[14px] font-medium">{{ t("settings.twofa") }}</h2>
      <p class="mb-3 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
        {{
          session.user.totp_enabled
            ? t("settings.twofaOn")
            : t("settings.twofaOff")
        }}
      </p>

      <div v-if="!session.user.totp_enabled && !totpSecret">
        <button
          class="rounded-md border px-3 py-2 text-[13px]"
          :style="{ borderColor: 'var(--line-axis)' }"
          @click="setupTOTP"
        >
          {{ t("settings.twofaSetup") }}
        </button>
      </div>

      <div v-if="totpSecret" class="flex flex-wrap items-start gap-5">
        <img
          :src="totpQR"
          alt="QR-Code für die Authenticator-App"
          width="160"
          height="160"
          class="rounded-md"
        />
        <div>
          <p
            class="mb-2 max-w-xs text-[12px]"
            :style="{ color: 'var(--ink-secondary)' }"
          >
            {{ t("settings.twofaScan") }}
          </p>
          <code
            class="tabular mb-3 block text-[11px] break-all"
            :style="{ color: 'var(--ink-muted)' }"
          >
            {{ totpSecret }}
          </code>
          <div class="flex gap-2">
            <input
              v-model="totpCode"
              inputmode="numeric"
              maxlength="6"
              placeholder="000000"
              class="tabular w-28 rounded-md border px-3 py-2 text-center text-[13px]"
              :style="inputStyle"
            />
            <button
              class="rounded-md px-3 py-2 text-[13px] font-medium text-white"
              :style="{ background: 'var(--series-1)' }"
              @click="enableTOTP"
            >
              {{ t("settings.twofaEnable") }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="session.user.totp_enabled" class="flex gap-2">
        <input
          v-model="totpCode"
          inputmode="numeric"
          maxlength="6"
          placeholder="000000"
          class="tabular w-28 rounded-md border px-3 py-2 text-center text-[13px]"
          :style="inputStyle"
        />
        <button
          class="rounded-md border px-3 py-2 text-[13px]"
          :style="{
            borderColor: 'var(--line-axis)',
            color: 'var(--status-critical)',
          }"
          @click="disableTOTP"
        >
          {{ t("settings.twofaDisable") }}
        </button>
      </div>

      <p
        v-if="totpError"
        class="mt-2 text-[12px]"
        :style="{ color: 'var(--status-critical)' }"
      >
        {{ totpError }}
      </p>
    </section>

    <section
      class="rounded-lg border p-5"
      :style="{
        borderColor: 'var(--border-ring)',
        background: 'var(--surface-card)',
      }"
    >
      <h2 class="mb-3 text-[14px] font-medium">{{ t("settings.theme") }}</h2>
      <div class="mb-4 flex gap-2">
        <button
          v-for="mode in ['system', 'light', 'dark']"
          :key="mode"
          class="rounded-md border px-3 py-1.5 text-[12px]"
          :style="{
            borderColor:
              theme.mode === mode ? 'var(--series-1)' : 'var(--line-axis)',
            color:
              theme.mode === mode ? 'var(--series-1)' : 'var(--ink-secondary)',
          }"
          @click="setTheme(mode)"
        >
          {{
            t(`settings.theme${mode.charAt(0).toUpperCase()}${mode.slice(1)}`)
          }}
        </button>
      </div>

      <h2 class="mb-3 text-[14px] font-medium">{{ t("settings.language") }}</h2>
      <div class="flex gap-2">
        <button
          v-for="locale in ['de', 'en']"
          :key="locale"
          class="rounded-md border px-3 py-1.5 text-[12px] uppercase"
          :style="{
            borderColor:
              i18n.locale === locale ? 'var(--series-1)' : 'var(--line-axis)',
            color:
              i18n.locale === locale
                ? 'var(--series-1)'
                : 'var(--ink-secondary)',
          }"
          @click="setLocale(locale)"
        >
          {{ locale }}
        </button>
      </div>
    </section>
  </div>
</template>
