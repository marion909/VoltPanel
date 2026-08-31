<script setup>
import { onMounted, ref } from "vue";
import { api } from "../api";
import { t } from "../i18n";

const versions = ref([]);
const version = ref("");
const extensions = ref([]);
const loading = ref(false);
const error = ref("");
const busy = ref("");
const newName = ref("");

async function loadVersions() {
  try {
    const info = await api.get("/system/info");
    versions.value = info.system?.php_versions || [];
    if (versions.value.length && !version.value) {
      version.value = versions.value[0];
      await loadExtensions();
    }
  } catch (err) {
    error.value = err.message;
  }
}

async function loadExtensions() {
  if (!version.value) return;
  loading.value = true;
  error.value = "";
  try {
    extensions.value = await api.get(`/system/php/${version.value}/extensions`);
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function toggle(ext) {
  busy.value = ext.name;
  error.value = "";
  try {
    extensions.value = await api.post(
      `/system/php/${version.value}/extensions/toggle`,
      {
        name: ext.name,
        enable: !ext.enabled,
      },
    );
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = "";
  }
}

async function install() {
  const name = newName.value.trim().toLowerCase();
  if (!name) return;
  busy.value = name;
  error.value = "";
  try {
    extensions.value = await api.post(
      `/system/php/${version.value}/extensions/install`,
      { name },
    );
    newName.value = "";
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = "";
  }
}

onMounted(loadVersions);
</script>

<template>
  <section
    class="rounded-lg border p-5"
    :style="{
      borderColor: 'var(--border-ring)',
      background: 'var(--surface-card)',
    }"
  >
    <div class="mb-1 flex items-center justify-between">
      <h2 class="text-[14px] font-medium">{{ t("php.title") }}</h2>
      <select
        v-model="version"
        class="rounded border px-2 py-1 text-[12px]"
        :style="{
          borderColor: 'var(--line-axis)',
          background: 'var(--surface-page)',
          color: 'var(--ink-primary)',
        }"
        @change="loadExtensions"
      >
        <option v-for="v in versions" :key="v" :value="v">PHP {{ v }}</option>
      </select>
    </div>
    <!-- Der Hinweis steht bewusst oben: wer ein Modul sucht, sucht es meist
         für eine bestimmte Site und soll gleich wissen, dass es alle trifft. -->
    <p class="mb-4 text-[12px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t("php.systemwide") }}
    </p>

    <p
      v-if="error"
      class="mb-3 text-[13px]"
      :style="{ color: 'var(--status-critical)' }"
    >
      {{ error }}
    </p>

    <div class="mb-4 flex gap-2">
      <input
        v-model="newName"
        :placeholder="t('php.placeholder')"
        class="w-56 rounded border px-2.5 py-1.5 text-[13px]"
        :style="{
          borderColor: 'var(--line-axis)',
          background: 'var(--surface-page)',
          color: 'var(--ink-primary)',
        }"
        @keyup.enter="install"
      />
      <button
        class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white disabled:opacity-60"
        :style="{ background: 'var(--series-1)' }"
        :disabled="!newName.trim() || busy !== ''"
        @click="install"
      >
        {{ busy === newName.trim().toLowerCase() ? "…" : t("php.install") }}
      </button>
    </div>

    <p
      v-if="loading"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      …
    </p>
    <p
      v-else-if="!extensions.length"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ t("php.none") }}
    </p>

    <div v-else class="grid grid-cols-2 gap-x-6 gap-y-1 sm:grid-cols-3">
      <label
        v-for="ext in extensions"
        :key="ext.name"
        class="flex items-center gap-2 py-0.5 text-[13px]"
        :class="ext.essential ? 'cursor-default' : 'cursor-pointer'"
        :title="ext.essential ? t('php.essential') : ''"
      >
        <input
          type="checkbox"
          :checked="ext.enabled"
          :disabled="ext.essential || busy !== ''"
          @change="toggle(ext)"
        />
        <span
          :style="{
            color: ext.enabled ? 'var(--ink-primary)' : 'var(--ink-muted)',
          }"
        >
          {{ ext.name }}
        </span>
      </label>
    </div>
  </section>
</template>
