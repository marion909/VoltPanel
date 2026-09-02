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

onMounted(load);
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
  </div>
</template>
