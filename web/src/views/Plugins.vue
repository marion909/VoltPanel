<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api";
import { t } from "../i18n";

// Plugins: server-weite Fähigkeiten aus dem festen Katalog
// (internal/core/plugins.go). Diese Ansicht selbst ist Administratoren
// vorbehalten — der Router lässt niemand anderen herein, und der Server
// prüft die Rolle bei jeder Anfrage ohnehin noch einmal.
const plugins = ref([]);
const loading = ref(true);
const error = ref("");
// Name des Plugins, für das gerade eine Aktion läuft — sperrt nur dessen
// eigene Knöpfe, nicht die ganze Liste.
const busy = ref("");

// Webmail: eine einzige, server-weite Roundcube-Installation
// (internal/core/webmail.go) — kein Eintrag aus dem apt-Katalog oben, ein
// aus dem Internet geholtes PHP-Programm mit eigener Datenbank. Eigener
// Abschnitt statt eine Karte in der Liste, damit der Unterschied auch in der
// Oberfläche sichtbar bleibt: Installieren braucht hier eine PHP-Fassung,
// nicht nur einen Klick.
const webmail = ref(null);
const webmailPHPVersions = ref(["8.3"]);
const webmailForm = ref({ php_version: "8.3" });
const webmailBusy = ref(false);
const webmailError = ref("");

async function loadWebmail() {
  try {
    webmail.value = await api.get("/webmail");
  } catch (err) {
    webmailError.value = err.message;
  }
}

async function loadWebmailPHPVersions() {
  try {
    const info = await api.get("/system/info");
    const found = info.system?.php_versions || [];
    if (found.length) {
      webmailPHPVersions.value = found;
      webmailForm.value.php_version = found[0];
    }
  } catch {
    // Ohne Auskunft bleibt die Vorauswahl stehen — dieselbe Vorsicht wie im
    // App-Store-Dialog.
  }
}

async function installWebmail() {
  webmailBusy.value = true;
  webmailError.value = "";
  try {
    await api.post("/webmail", webmailForm.value);
    await loadWebmail();
  } catch (err) {
    webmailError.value = err.message;
  } finally {
    webmailBusy.value = false;
  }
}

async function uninstallWebmail() {
  if (!confirm(t("plugins.webmailConfirmUninstall"))) return;
  webmailBusy.value = true;
  webmailError.value = "";
  try {
    await api.del("/webmail");
    await loadWebmail();
  } catch (err) {
    webmailError.value = err.message;
  } finally {
    webmailBusy.value = false;
  }
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    plugins.value = await api.get("/plugins");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function installieren(p) {
  busy.value = p.id;
  error.value = "";
  try {
    await api.post(`/plugins/${p.id}/install`);
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = "";
  }
}

async function entfernen(p) {
  if (!confirm(t("plugins.confirmUninstall", { name: p.name }))) return;
  busy.value = p.id;
  error.value = "";
  try {
    await api.post(`/plugins/${p.id}/uninstall`);
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = "";
  }
}

async function umschalten(p) {
  busy.value = p.id;
  error.value = "";
  try {
    await api.post(`/plugins/${p.id}/set`, { enabled: !p.enabled });
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = "";
  }
}

onMounted(() => {
  load();
  loadWebmail();
  loadWebmailPHPVersions();
});
</script>

<template>
  <div class="fade-in px-8 py-6">
    <h1 class="text-[18px] font-semibold tracking-tight">
      {{ t("plugins.title") }}
    </h1>
    <p class="mt-1 max-w-2xl text-[13px]" :style="{ color: 'var(--ink-secondary)' }">
      {{ t("plugins.subtitle") }}
    </p>

    <p
      v-if="error"
      class="mt-4 text-[13px]"
      :style="{ color: 'var(--status-critical)' }"
      role="alert"
    >
      {{ error }}
    </p>
    <p v-if="loading" class="mt-4 text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t("common.loading") }}
    </p>

    <div v-else class="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="p in plugins"
        :key="p.id"
        class="rounded-lg border p-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <div class="text-[13px] font-medium">{{ p.name }}</div>
            <div class="mt-0.5 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ p.description }}
            </div>
          </div>
          <span
            v-if="p.installed && p.service"
            class="inline-flex shrink-0 items-center gap-1.5 text-[12px]"
            :style="{ color: p.active ? 'var(--status-good)' : 'var(--ink-muted)' }"
          >
            <span
              class="inline-block h-2 w-2 rounded-full"
              :style="{ background: p.active ? 'var(--status-good)' : 'var(--line-axis)' }"
              aria-hidden="true"
            />
            {{ p.active ? t("plugins.active") : t("plugins.inactive") }}
          </span>
        </div>

        <div class="mt-3 flex items-center justify-between gap-2">
          <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ p.installed ? t("plugins.installed") : t("plugins.notInstalled") }}
          </span>

          <div class="flex gap-2">
            <template v-if="!p.installed">
              <button
                :disabled="busy === p.id"
                class="rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                @click="installieren(p)"
              >
                {{ busy === p.id ? t("plugins.installing") : t("plugins.install") }}
              </button>
            </template>
            <template v-else>
              <button
                v-if="p.service"
                :disabled="busy === p.id"
                class="rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
                @click="umschalten(p)"
              >
                {{ busy === p.id ? t("plugins.settingUp") : p.enabled ? t("plugins.disable") : t("plugins.enable") }}
              </button>
              <button
                :disabled="busy === p.id"
                class="rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
                :style="{ borderColor: 'var(--line-axis)', color: 'var(--status-critical)' }"
                @click="entfernen(p)"
              >
                {{ busy === p.id ? t("plugins.uninstalling") : t("plugins.uninstall") }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </div>

    <section class="mt-8">
      <h2 class="text-[15px] font-semibold tracking-tight">{{ t("plugins.webmailTitle") }}</h2>
      <p class="mt-1 max-w-2xl text-[13px]" :style="{ color: 'var(--ink-secondary)' }">
        {{ t("plugins.webmailSubtitle") }}
      </p>

      <p
        v-if="webmailError"
        class="mt-3 text-[13px]"
        :style="{ color: 'var(--status-critical)' }"
        role="alert"
      >
        {{ webmailError }}
      </p>

      <div
        v-if="webmail"
        class="mt-3 max-w-md rounded-lg border p-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <template v-if="webmail.installed">
          <div class="text-[13px] font-medium">
            <a :href="'https://' + webmail.hostname" target="_blank" rel="noopener">{{ webmail.hostname }}</a>
          </div>
          <div class="mt-0.5 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            PHP {{ webmail.php_version }}
          </div>
          <button
            :disabled="webmailBusy"
            class="mt-3 rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
            :style="{ borderColor: 'var(--line-axis)', color: 'var(--status-critical)' }"
            @click="uninstallWebmail"
          >
            {{ webmailBusy ? t("plugins.webmailUninstalling") : t("plugins.webmailUninstall") }}
          </button>
        </template>
        <template v-else>
          <div class="flex flex-wrap items-end gap-2">
            <label class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ t("plugins.webmailPhpVersion") }}
              <select
                v-model="webmailForm.php_version"
                class="mt-1 block rounded-md border px-2 py-1 text-[12px]"
                :style="{ borderColor: 'var(--line-axis)', background: 'var(--surface-page)', color: 'var(--ink-primary)' }"
              >
                <option v-for="v in webmailPHPVersions" :key="v" :value="v">{{ v }}</option>
              </select>
            </label>
            <button
              :disabled="webmailBusy"
              class="rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
              :style="{ borderColor: 'var(--line-axis)', color: 'var(--ink-secondary)' }"
              @click="installWebmail"
            >
              {{ webmailBusy ? t("plugins.webmailInstalling") : t("plugins.webmailInstall") }}
            </button>
          </div>
          <p class="mt-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ t("plugins.webmailHostnameHint") }}
          </p>
        </template>
      </div>
    </section>
  </div>
</template>
