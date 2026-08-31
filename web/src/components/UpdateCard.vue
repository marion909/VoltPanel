<script setup>
import { computed, onMounted, ref } from "vue";
import { api } from "../api";
import { t } from "../i18n";
import { hasRole } from "../stores/session";
import { update, checkUpdate } from "../stores/update";

// running: das Update läuft. waiting: die Dienste starten neu, wir warten auf
// das Panel. Getrennt, weil in der zweiten Phase jeder Fehler normal ist —
// der Server ist ja gerade weg.
const running = ref(false);
const waiting = ref(false);
const reloading = ref(false);
const done = ref("");
const error = ref("");

const isAdmin = computed(() => hasRole("admin"));

onMounted(() => {
  if (!update.loaded) checkUpdate();
});

async function start() {
  if (!window.confirm(t("update.confirm"))) return;

  error.value = "";
  done.value = "";
  running.value = true;
  try {
    const res = await api.post("/system/update", {});
    running.value = false;
    if (!res.changed) {
      await checkUpdate(true);
      return;
    }
    waiting.value = true;
    await waitForPanel(res.to);
  } catch (err) {
    running.value = false;
    error.value = err.message;
  }
}

// Nach dem Tausch startet volt-web neu; die Verbindung reißt dabei ab. Das
// ist der erwartete Verlauf, kein Fehler — deshalb wird hier still weiter
// gefragt, bis das Panel wieder antwortet.
async function waitForPanel(expected) {
  const until = Date.now() + 180000;
  while (Date.now() < until) {
    await new Promise((r) => setTimeout(r, 2000));
    try {
      await checkUpdate(true);
      if (!update.available) {
        waiting.value = false;
        done.value = expected || update.current;
        reload();
        return;
      }
    } catch {
      // Panel noch nicht wieder da — weiter warten.
    }
  }
  waiting.value = false;
  error.value = t("update.waiting");
}

// Die Oberfläche steckt im selben Binary wie der Server. Nach dem Tausch
// liefert er eine neue aus — im Browser läuft aber weiter die alte, die beim
// Öffnen der Seite geladen wurde. Ohne diesen Neuladen sieht das Update
// erfolgreich aus und nichts davon ist zu sehen.
//
// Der Aufruf steht erst hier, weil das Panel an dieser Stelle nachweislich
// wieder antwortet: ein früherer Neuladen träfe einen Server, der gerade neu
// startet, und der Browser zeigte eine Fehlerseite.
function reload() {
  reloading.value = true;
  // Kurz genug, dass niemand wartet, lang genug, dass die Meldung ankommt.
  setTimeout(() => window.location.reload(), 1200);
}
</script>

<template>
  <section
    class="mb-4 rounded-lg border p-5"
    :style="{
      borderColor: 'var(--border-ring)',
      background: 'var(--surface-card)',
    }"
  >
    <div class="mb-3 flex items-center justify-between">
      <h2 class="text-[14px] font-medium">{{ t("update.title") }}</h2>
      <button
        class="rounded px-2.5 py-1 text-[12px] transition-colors disabled:opacity-50"
        :style="{ color: 'var(--ink-muted)' }"
        :disabled="update.checking || running || waiting"
        @click="checkUpdate(true)"
      >
        {{ update.checking ? "…" : t("update.check") }}
      </button>
    </div>

    <dl class="mb-3 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-[13px]">
      <dt :style="{ color: 'var(--ink-muted)' }">{{ t("update.current") }}</dt>
      <dd class="font-mono">{{ update.current || "—" }}</dd>
      <dt :style="{ color: 'var(--ink-muted)' }">{{ t("update.channel") }}</dt>
      <dd>{{ update.channel || "—" }}</dd>
    </dl>

    <!-- Reihenfolge nach Dringlichkeit: was gerade passiert, dann Fehler,
         dann der ruhende Zustand. -->
    <p
      v-if="waiting"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ t("update.waiting") }}
    </p>
    <p
      v-else-if="running"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ t("update.running") }}
    </p>
    <p
      v-else-if="done"
      class="text-[13px]"
      :style="{ color: 'var(--status-good)' }"
    >
      {{ t("update.done", { v: done }) }}
      <span v-if="reloading" :style="{ color: 'var(--ink-muted)' }">
        {{ t("update.reloading") }}
      </span>
    </p>
    <p
      v-else-if="error"
      class="text-[13px]"
      :style="{ color: 'var(--status-critical)' }"
    >
      {{ error }}
    </p>
    <p
      v-else-if="update.error"
      class="text-[13px]"
      :style="{ color: 'var(--status-warning)' }"
    >
      {{ t("update.unreachable") }}
      <span :style="{ color: 'var(--ink-muted)' }">{{ update.error }}</span>
    </p>
    <p
      v-else-if="!update.available"
      class="text-[13px]"
      :style="{ color: 'var(--ink-muted)' }"
    >
      {{ t("update.uptodate") }}
    </p>

    <template v-if="update.available && !running && !waiting && !done">
      <p class="mb-3 text-[13px] font-medium">
        {{ t("update.available", { v: update.latest }) }}
      </p>

      <div v-if="update.notes" class="mb-3">
        <h3
          class="mb-1 text-[12px] font-medium"
          :style="{ color: 'var(--ink-muted)' }"
        >
          {{ t("update.notes") }}
        </h3>
        <!-- Als Text, nicht als Markdown: die Notes kommen von aussen, und ein
             Renderer waere eine Angriffsfläche für ein bisschen Fettschrift. -->
        <pre
          class="max-h-56 overflow-auto rounded border p-3 text-[12px] leading-relaxed whitespace-pre-wrap"
          :style="{
            borderColor: 'var(--line-axis)',
            background: 'var(--surface-page)',
            color: 'var(--ink-secondary)',
          }"
          >{{ update.notes }}</pre>
      </div>

      <div class="flex items-center gap-3">
        <button
          v-if="isAdmin"
          class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white transition-opacity hover:opacity-90"
          :style="{ background: 'var(--series-1)' }"
          @click="start"
        >
          {{ t("update.start") }}
        </button>
        <span v-else class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t("update.adminonly") }}
        </span>
        <a
          v-if="update.url"
          :href="update.url"
          target="_blank"
          rel="noopener noreferrer"
          class="text-[12px] underline"
          :style="{ color: 'var(--ink-muted)' }"
          >Release-Seite</a
        >
      </div>
    </template>
  </section>
</template>
