<script setup>
import { ref, computed, onMounted } from "vue";
import { RouterLink } from "vue-router";
import { api } from "../api";
import { t } from "../i18n";
import { askConfirm } from "../stores/confirm";
import { isAdmin } from "../stores/session";
import { formatBytes, daysLeft } from "../format";
import AppStoreDialog from "../components/AppStoreDialog.vue";
import SkeletonRows from "../components/SkeletonRows.vue";

const sites = ref([]);
const certs = ref([]);
const loading = ref(true);
const error = ref("");
const notice = ref("");
const showForm = ref(false);
const busy = ref(false);
const backupBusy = ref({});

const form = ref({
  domain: "",
  type: "static",
  php_version: "8.3",
  proxy_target: "",
  document_root: "public",
});

// Die Liste kommt vom Server: fest verdrahtet bot sie Versionen an, die dort
// gar nicht installiert sind, und die Site scheiterte erst beim Anlegen.
const phpVersions = ref(["8.3"]);

async function loadPHPVersions() {
  try {
    const info = await api.get("/system/info");
    const found = info.system?.php_versions || [];
    if (found.length) {
      phpVersions.value = found;
      if (!found.includes(form.value.php_version))
        form.value.php_version = found[0];
    }
  } catch {
    // Ohne Antwort bleibt die Vorgabe stehen — das Formular soll benutzbar
    // bleiben, auch wenn der Agent gerade nicht erreichbar ist.
  }
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const [s, c] = await Promise.all([api.get("/sites"), api.get("/certs").catch(() => [])]);
    sites.value = s;
    certs.value = c;
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

// Je Site das Zertifikat mit der kürzesten Restlaufzeit — das ist das, das
// zuerst zum Problem wird, wenn eine Site aus irgendeinem Grund mehrere hat.
const certBySite = computed(() => {
  const out = {};
  for (const cert of certs.value) {
    if (!cert.site_id || !cert.not_after) continue;
    if (!out[cert.site_id] || cert.not_after < out[cert.site_id].not_after) {
      out[cert.site_id] = cert;
    }
  }
  return out;
});

// Reiter nach Site-Typ — reine Anzeigefilterung über den vorhandenen
// site.type, kein zusätzlicher Serveraufruf.
const typeTabs = [
  { key: "all", label: "sites.tabAll" },
  { key: "static", label: "sites.tabStatic" },
  { key: "php", label: "sites.tabPHP" },
  { key: "proxy", label: "sites.tabProxy" },
];
const activeType = ref("all");
const filteredSites = computed(() =>
  activeType.value === "all" ? sites.value : sites.value.filter((s) => s.type === activeType.value),
);

async function create() {
  busy.value = true;
  error.value = "";
  try {
    const payload = { ...form.value };
    // Nur mitschicken, was zum Typ gehört — der Server lehnt eine PHP-Version
    // an einer Proxy-Site sonst zu Recht ab.
    if (payload.type !== "php") payload.php_version = "";
    if (payload.type !== "proxy") payload.proxy_target = "";

    await api.post("/sites", payload);
    showForm.value = false;
    form.value = {
      domain: "",
      type: "static",
      php_version: "8.3",
      proxy_target: "",
      document_root: "public",
    };
    await load();
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = false;
  }
}

async function rebuild(site) {
  try {
    await api.post(`/sites/${site.id}/rebuild`);
  } catch (err) {
    error.value = err.message;
  }
}

async function remove(site) {
  if (!(await askConfirm(t("sites.confirmDelete", { domain: site.domain })))) return;
  try {
    await api.del(`/sites/${site.id}`);
    await load();
  } catch (err) {
    error.value = err.message;
  }
}

// Ein Archiv "nur für diese Site" gibt es nicht wirklich — jedes Archiv
// enthält immer die ganze Panel-Datenbank (backup.go), site_domains
// entscheidet nur, wessen Dateien zusätzlich mitkommen. Der Hinweis dazu
// steht im Titel des Knopfes, nicht nur in der Doku.
async function backupSite(site) {
  backupBusy.value = { ...backupBusy.value, [site.id]: true };
  error.value = "";
  notice.value = "";
  try {
    const res = await api.post("/backups", { include_config: true, site_domains: [site.domain] });
    notice.value = t("sites.backupDone", { domain: site.domain, size: formatBytes(res.size_bytes) });
  } catch (err) {
    error.value = err.message;
  } finally {
    backupBusy.value = { ...backupBusy.value, [site.id]: false };
  }
}

onMounted(() => {
  load();
  loadPHPVersions();
});

const inputStyle = {
  borderColor: "var(--line-axis)",
  background: "var(--surface-page)",
  color: "var(--ink-primary)",
};
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">
        {{ t("sites.title") }}
      </h1>
      <div class="flex gap-2">
        <button
          class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white bg-accent"
          @click="showForm = !showForm"
        >
          {{ showForm ? t("common.cancel") : t("sites.new") }}
        </button>
      </div>
    </header>

    <AppStoreDialog class="mb-5" @installed="load" />

    <p v-if="error" class="mb-4 text-[13px] text-status-critical" role="alert">
      {{ error }}
    </p>
    <p v-if="notice" class="mb-4 text-[13px] text-status-good">
      {{ notice }}
    </p>

    <form
      v-if="showForm"
      class="panel-card mb-5 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4"
      @submit.prevent="create"
    >
      <label class="block">
        <span class="mb-1 block text-[12px] text-ink-secondary">
          {{ t("sites.domain") }}
        </span>
        <input
          v-model="form.domain"
          required
          placeholder="example.at"
          class="w-full rounded-md border px-3 py-2 text-[13px]"
          :style="inputStyle"
        />
      </label>

      <label class="block">
        <span class="mb-1 block text-[12px] text-ink-secondary">
          {{ t("sites.type") }}
        </span>
        <select v-model="form.type" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option value="static">static</option>
          <option value="php">php</option>
          <option value="proxy">proxy</option>
        </select>
      </label>

      <label v-if="form.type === 'php'" class="block">
        <span class="mb-1 block text-[12px] text-ink-secondary">
          {{ t("sites.php") }}
        </span>
        <select v-model="form.php_version" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
          <option v-for="v in phpVersions" :key="v" :value="v">{{ v }}</option>
        </select>
      </label>

      <label v-if="form.type === 'proxy'" class="block">
        <span class="mb-1 block text-[12px] text-ink-secondary">{{ t("sites.proxyTarget") }}</span>
        <input
          v-model="form.proxy_target"
          required
          placeholder="http://127.0.0.1:3000"
          class="w-full rounded-md border px-3 py-2 text-[13px]"
          :style="inputStyle"
        />
      </label>

      <div class="flex items-end">
        <button
          type="submit"
          :disabled="busy"
          class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60 bg-accent"
        >
          {{ busy ? t("common.loading") : t("sites.create") }}
        </button>
      </div>
    </form>

    <!-- Reiter nach Typ -->
    <nav class="mb-4 flex gap-1 overflow-x-auto border-b border-line-hairline">
      <button
        v-for="item in typeTabs"
        :key="item.key"
        class="-mb-px border-b-2 px-3 py-2 text-[13px] transition-colors"
        :style="
          activeType === item.key
            ? { borderColor: 'var(--accent)', color: 'var(--ink-primary)', fontWeight: 500 }
            : { borderColor: 'transparent', color: 'var(--ink-secondary)' }
        "
        @click="activeType = item.key"
      >
        {{ t(item.label) }}
      </button>
    </nav>

    <SkeletonRows v-if="loading" :cols="6" />

    <div v-else-if="filteredSites.length" class="panel-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-[13px]">
          <thead class="text-[12px] text-ink-muted">
            <tr class="border-b border-line-hairline">
              <th class="px-4 py-2.5 font-normal">{{ t("sites.domain") }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t("sites.type") }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t("sites.php") }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t("sites.ssl") }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t("sites.requests") }}</th>
              <th class="px-4 py-2.5 font-normal">{{ t("sites.status") }}</th>
              <th class="px-4 py-2.5 text-right font-normal">
                {{ t("common.actions") }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="site in filteredSites" :key="site.id" class="border-b last:border-0 border-line-hairline">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-1.5">
                  <RouterLink :to="`/frontend/sites/${site.id}`" class="font-medium hover:underline">
                    {{ site.domain }}
                  </RouterLink>
                  <a
                    :href="`https://${site.domain}`"
                    target="_blank"
                    rel="noopener"
                    class="text-ink-muted hover:opacity-70"
                    :title="t('sites.openSite')"
                  >
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                      <path d="M14 3h7v7M21 3l-9 9M5 5h6v2H7v10h10v-4h2v6H5z" />
                    </svg>
                  </a>
                </div>
                <div class="text-[11px] text-ink-muted">{{ site.system_user }}</div>
              </td>
              <td class="px-4 py-2.5 text-ink-secondary">
                {{ site.type }}
              </td>
              <td class="px-4 py-2.5 tabular text-ink-secondary">
                {{ site.php_version || "—" }}
              </td>
              <td class="px-4 py-2.5 whitespace-nowrap">
                <span
                  v-if="certBySite[site.id]"
                  class="inline-flex items-center gap-1.5"
                  :style="{
                    color:
                      daysLeft(certBySite[site.id].not_after) <= 14
                        ? 'var(--status-critical)'
                        : daysLeft(certBySite[site.id].not_after) <= 30
                          ? 'var(--status-warning)'
                          : 'var(--status-good)',
                  }"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M20 6L9 17l-5-5" />
                  </svg>
                  {{ t("sites.daysLeft", { n: daysLeft(certBySite[site.id].not_after) }) }}
                </span>
                <span v-else class="inline-flex items-center gap-1.5 text-ink-muted">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true">
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                  {{ t("common.no") }}
                </span>
              </td>
              <td class="tabular px-4 py-2.5 text-right text-ink-secondary">
                {{ (site.traffic_requests || 0).toLocaleString() }}
              </td>
              <td class="px-4 py-2.5 text-ink-secondary">
                {{ site.status }}
              </td>
              <td class="px-4 py-2.5 text-right whitespace-nowrap">
                <RouterLink :to="`/frontend/sites/${site.id}`" class="text-[12px] underline text-ink-secondary">
                  {{ t("site.settings") }}
                </RouterLink>
                <button
                  v-if="isAdmin()"
                  class="ml-3 text-[12px] underline text-ink-secondary disabled:opacity-60"
                  :disabled="backupBusy[site.id]"
                  :title="t('sites.backupHint')"
                  @click="backupSite(site)"
                >
                  {{ backupBusy[site.id] ? t("sites.backingUp") : t("sites.backup") }}
                </button>
                <button class="ml-3 text-[12px] underline text-ink-secondary" @click="rebuild(site)">
                  {{ t("sites.rebuild") }}
                </button>
                <button class="ml-3 text-[12px] underline text-status-critical" @click="remove(site)">
                  {{ t("sites.delete") }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <p v-else class="text-[13px] text-ink-muted">
      {{ t("sites.empty") }}
    </p>
  </div>
</template>
