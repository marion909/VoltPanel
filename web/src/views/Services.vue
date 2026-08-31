<script setup>
import { ref, onMounted } from "vue";
import { api } from "../api";
import { t } from "../i18n";
import { isAdmin } from "../stores/session";
import PHPExtensions from "../components/PHPExtensions.vue";
import ProcessList from "../components/ProcessList.vue";

const services = ref([]);
const loading = ref(true);
const error = ref("");
const pending = ref("");

async function load() {
  loading.value = true;
  try {
    services.value = await api.get("/system/services");
  } catch (err) {
    error.value = err.message;
  } finally {
    loading.value = false;
  }
}

async function act(service, action) {
  pending.value = service.name;
  error.value = "";
  try {
    const updated = await api.post(
      `/system/services/${service.name}/${action}`,
    );
    // Nur die betroffene Kachel ersetzen — ein Reload aller Dienste würde die
    // Liste bei jedem Klick springen lassen.
    services.value = services.value.map((s) =>
      s.name === updated.name ? updated : s,
    );
  } catch (err) {
    error.value = err.message;
  } finally {
    pending.value = "";
  }
}

onMounted(load);
</script>

<template>
  <div class="fade-in px-8 py-6">
    <h1 class="mb-5 text-[18px] font-semibold tracking-tight">
      {{ t("services.title") }}
    </h1>

    <p
      v-if="error"
      class="mb-4 text-[13px]"
      :style="{ color: 'var(--status-critical)' }"
      role="alert"
    >
      {{ error }}
    </p>
    <p
      v-if="loading"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ t("common.loading") }}
    </p>

    <div v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="service in services"
        :key="service.name"
        class="rounded-lg border p-4"
        :style="{
          borderColor: 'var(--border-ring)',
          background: 'var(--surface-card)',
        }"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <div class="truncate text-[13px] font-medium">
              {{ service.name }}
            </div>
            <div
              class="truncate text-[11px]"
              :style="{ color: 'var(--ink-muted)' }"
            >
              {{ service.description }}
            </div>
          </div>
          <span
            class="inline-flex shrink-0 items-center gap-1.5 text-[12px]"
            :style="{
              color: service.active ? 'var(--status-good)' : 'var(--ink-muted)',
            }"
          >
            <span
              class="inline-block h-2 w-2 rounded-full"
              :style="{
                background: service.active
                  ? 'var(--status-good)'
                  : 'var(--line-axis)',
              }"
              aria-hidden="true"
            />
            {{ service.active ? t("services.running") : t("services.stopped") }}
          </span>
        </div>

        <div class="mt-3 flex items-center justify-between gap-2">
          <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ t("services.autostart") }}:
            {{ service.enabled ? t("common.yes") : t("common.no") }}
          </span>

          <div v-if="isAdmin()" class="flex gap-2">
            <button
              v-for="action in service.active ? ['restart', 'stop'] : ['start']"
              :key="action"
              :disabled="pending === service.name"
              class="rounded-md border px-2 py-1 text-[11px] disabled:opacity-50"
              :style="{
                borderColor: 'var(--line-axis)',
                color: 'var(--ink-secondary)',
              }"
              @click="act(service, action)"
            >
              {{ action }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="isAdmin()" class="mt-6">
      <PHPExtensions />
    </div>

    <div class="mt-8">
      <ProcessList />
    </div>
  </div>
</template>
