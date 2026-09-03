<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api";
import { t } from "../i18n";

// App-Store: ein Klick, eine fertige Website. Anders als das Formular
// daneben, das eine leere Site anlegt, entsteht hier Site plus Datenbank
// plus WordPress-Kern in einem Schritt — den letzten Teil (Titel, erstes
// Konto) übernimmt WordPress' eigener Installer im Browser, sobald die
// Domain aufgerufen wird.
const emit = defineEmits(["installed"]);

const open = ref(false);
const catalog = ref([]);
const phpVersions = ref(["8.3"]);
const busy = ref(false);
const error = ref("");
const result = ref(null);

const form = ref({ domain: "", php_version: "8.3" });

async function loadCatalog() {
  try {
    catalog.value = await api.get("/appstore");
  } catch {
    // Ohne Katalog bleibt die Klappe leer statt kaputt — dieselbe Vorsicht
    // wie beim PHP-Versionen-Laden nebenan.
  }
}

async function loadPHPVersions() {
  try {
    const info = await api.get("/system/info");
    const found = info.system?.php_versions || [];
    if (found.length) {
      phpVersions.value = found;
      form.value.php_version = found[0];
    }
  } catch {
    // s.o.
  }
}

async function installWordPress() {
  busy.value = true;
  error.value = "";
  result.value = null;
  try {
    result.value = await api.post("/appstore/wordpress", form.value);
    emit("installed");
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = false;
  }
}

function schliessen() {
  open.value = false;
  result.value = null;
  error.value = "";
  form.value = { domain: "", php_version: phpVersions.value[0] || "8.3" };
}

onMounted(() => {
  loadCatalog();
  loadPHPVersions();
});
</script>

<template>
  <div>
    <button
      type="button"
      class="rounded-md border px-3 py-1.5 text-[13px]"
      :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
      @click="open = !open"
    >
      {{ t("appstore.button") }}
    </button>

    <div
      v-if="open"
      class="mt-3 rounded-lg border p-4"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <!-- Das Ergebnis: die Site steht, und das Datenbankpasswort steht hier
           genau einmal — danach gibt das Panel es nicht mehr heraus. -->
      <div v-if="result">
        <p class="text-[13px] font-medium" :style="{ color: 'var(--status-good)' }">
          {{ t("appstore.wpDone", { domain: result.site?.domain }) }}
        </p>
        <table class="mt-2 text-[12px]">
          <tbody>
            <tr>
              <td class="pr-3" :style="{ color: 'var(--ink-muted)' }">{{ t("appstore.dbName") }}</td>
              <td class="font-mono">{{ result.database?.name }}</td>
            </tr>
            <tr>
              <td class="pr-3" :style="{ color: 'var(--ink-muted)' }">{{ t("appstore.dbPassword") }}</td>
              <td class="font-mono">{{ result.db_password }}</td>
            </tr>
          </tbody>
        </table>
        <p class="mt-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t("appstore.wpNext") }}
        </p>
        <button
          type="button"
          class="mt-3 rounded-md border px-2 py-1 text-[11px]"
          :style="{ borderColor: 'var(--border-ring)', color: 'var(--ink-secondary)' }"
          @click="schliessen"
        >
          {{ t("common.close") }}
        </button>
      </div>

      <form v-else class="grid gap-3 sm:grid-cols-3" @submit.prevent="installWordPress">
        <label class="block sm:col-span-2">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t("sites.domain") }}
          </span>
          <input
            v-model="form.domain"
            required
            placeholder="blog.example.at"
            class="w-full rounded-md border px-3 py-2 text-[13px]"
            :style="{ borderColor: 'var(--line-axis)', background: 'var(--surface-page)', color: 'var(--ink-primary)' }"
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            PHP
          </span>
          <select
            v-model="form.php_version"
            class="w-full rounded-md border px-3 py-2 text-[13px]"
            :style="{ borderColor: 'var(--line-axis)', background: 'var(--surface-page)', color: 'var(--ink-primary)' }"
          >
            <option v-for="v in phpVersions" :key="v" :value="v">{{ v }}</option>
          </select>
        </label>

        <p class="text-[11px] sm:col-span-3" :style="{ color: 'var(--ink-muted)' }">
          {{ t("appstore.wpHint") }}
        </p>

        <p v-if="error" class="text-[12px] sm:col-span-3" :style="{ color: 'var(--status-critical)' }">
          {{ error }}
        </p>

        <div class="sm:col-span-3">
          <button
            type="submit"
            :disabled="busy"
            class="rounded-md px-3 py-1.5 text-[12px] font-medium text-white disabled:opacity-60"
            :style="{ background: 'var(--series-1)' }"
          >
            {{ busy ? t("appstore.installing") : t("appstore.install") }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
