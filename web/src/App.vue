<script setup>
import { computed, onMounted, ref, watch } from "vue";
import { RouterView, useRoute, useRouter } from "vue-router";
import { api } from "./api";
import { session, logout, hasRole } from "./stores/session";
import { update, checkUpdate } from "./stores/update";
import { theme, setTheme } from "./stores/theme";
import { formatUptime } from "./format";
import { t, i18n } from "./i18n";
import ConfirmDialog from "./components/ConfirmDialog.vue";

const route = useRoute();
const router = useRouter();

// Nur für den Hostname/Uptime-Chip in der Kopfzeile — dieselbe Route, die
// auch das Dashboard ruft, hier aber ohne die Zähler, die niemand außerhalb
// des Dashboards braucht.
const system = ref(null);
onMounted(async () => {
  try {
    const info = await api.get("/system/info");
    system.value = info.system;
  } catch {
    /* Kunden ohne Rechte auf Systemdaten sehen die Kopfzeile einfach ohne Chip. */
  }
});

const themeCycle = ["system", "light", "dark"];
const themeIcon = { system: "monitor", light: "sun", dark: "moon" };
function cycleTheme() {
  const i = themeCycle.indexOf(theme.mode);
  setTheme(themeCycle[(i + 1) % themeCycle.length]);
}

const avatarInitial = computed(() => {
  const name = session.user?.display_name || session.user?.email || "?";
  return name.trim().charAt(0).toUpperCase();
});

// Ein Kunde sieht keine Server-Dienste und keine Mandantenverwaltung. Das ist
// Aufräumen der Oberfläche, kein Schutz — der steht im Server.
//
// Gruppiert statt einer flachen Liste: 13 Einträge in einer Spalte sind an
// der Grenze dessen, was sich noch auf einen Blick scannen lässt — dezente
// Abschnitte machen die Struktur sichtbar, ohne die URLs anzufassen.
const navGroups = computed(() =>
  [
    {
      group: null,
      items: [{ to: "/", key: "nav.dashboard", icon: "grid" }],
    },
    {
      group: "nav.group.hosting",
      items: [
        { to: "/frontend", key: "nav.frontend", icon: "globe", active: "/frontend" },
        { to: "/domains", key: "nav.domains", icon: "shield" },
        { to: "/files", key: "nav.files", icon: "folder" },
        { to: "/databases", key: "nav.databases", icon: "database", active: "/databases" },
        { to: "/ftp", key: "nav.ftp", icon: "transfer" },
        { to: "/cronjobs", key: "nav.cron", icon: "clock" },
      ],
    },
    {
      group: "nav.group.mail",
      items: [{ to: "/mail", key: "nav.mail", icon: "mail" }],
    },
    {
      group: "nav.group.system",
      items: [
        { to: "/services", key: "nav.services", icon: "server", minRole: "admin" },
        { to: "/plugins", key: "nav.plugins", icon: "plug", minRole: "admin" },
        { to: "/backups", key: "nav.backups", icon: "archive" },
      ],
    },
    {
      group: "nav.group.admin",
      items: [
        { to: "/tenants", key: "nav.tenants", icon: "users", minRole: "admin" },
        { to: "/audit", key: "nav.audit", icon: "list" },
        { to: "/settings", key: "nav.settings", icon: "gear" },
      ],
    },
  ]
    .map((g) => ({ ...g, items: g.items.filter((item) => !item.minRole || hasRole(item.minRole)) }))
    .filter((g) => g.items.length),
);

// i18n.locale wird gelesen, damit die Beschriftungen beim Sprachwechsel neu
// gerendert werden — sonst bliebe die Navigation stehen.
const labels = computed(() => {
  void i18n.locale;
  const items = navGroups.value.flatMap((g) => g.items);
  return Object.fromEntries(items.map((n) => [n.key, t(n.key)]));
});

const isActiveNav = (item) =>
  item.active ? route.path.startsWith(item.active) : route.path === item.to;

// Einmal beim Laden fragen. Der Server hält die Antwort eine Stunde vor, ein
// zweites Fenster kostet also keinen weiteren Aufruf nach außen.
onMounted(() => {
  if (!update.loaded) checkUpdate();
});

// Off-Canvas nur unterhalb von lg: — ab dort ist die Sidebar wieder Teil des
// festen Layouts (siehe lg:-Klassen am <aside>). Ein Klick auf einen
// Navigationspunkt soll die Schublade schließen, sonst verdeckt sie den
// gerade gewählten Inhalt auf dem Handy/Tablet.
const sidebarOpen = ref(false);
watch(
  () => route.path,
  () => {
    sidebarOpen.value = false;
  },
);

async function onLogout() {
  await logout();
  router.push({ name: "login" });
}

const paths = {
  grid: "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z",
  globe:
    "M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c2.5 2.4 3.8 5.5 3.8 9s-1.3 6.6-3.8 9c-2.5-2.4-3.8-5.5-3.8-9S9.5 5.4 12 3z",
  server: "M4 5h16v5H4zM4 14h16v5H4zM7.5 7.5h.01M7.5 16.5h.01",
  list: "M4 6h16M4 12h16M4 18h10",
  box: "M21 8l-9-5-9 5v8l9 5 9-5zM3 8l9 5 9-5M12 13v10",
  branch: "M6 3v12M6 21a3 3 0 100-6 3 3 0 000 6zM6 6a3 3 0 100-6 3 3 0 000 6zM18 9a3 3 0 100-6 3 3 0 000 6zM18 6v1a4 4 0 01-4 4h-2a6 6 0 00-6 6",
  folder: "M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z",
  mail: "M3 7a2 2 0 012-2h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2zM3 8l9 6 9-6",
  database:
    "M12 3c4.4 0 8 1.3 8 3s-3.6 3-8 3-8-1.3-8-3 3.6-3 8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  clock: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 7v5l3 2",
  archive: "M3 4h18v4H3zM5 8v11a1 1 0 001 1h12a1 1 0 001-1V8M10 12h4",
  users:
    "M16 21v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2M9 11a4 4 0 100-8 4 4 0 000 8zM22 21v-2a4 4 0 00-3-3.9M16 3.1a4 4 0 010 7.8",
  gear: "M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.6 1.6 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.6 1.6 0 00-1.8-.3 1.6 1.6 0 00-1 1.5V21a2 2 0 11-4 0v-.1A1.6 1.6 0 009 19.4a1.6 1.6 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.6 1.6 0 00.3-1.8 1.6 1.6 0 00-1.5-1H3a2 2 0 110-4h.1A1.6 1.6 0 004.6 9a1.6 1.6 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.6 1.6 0 001.8.3H9a1.6 1.6 0 001-1.5V3a2 2 0 114 0v.1a1.6 1.6 0 001 1.5 1.6 1.6 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.6 1.6 0 00-.3 1.8V9a1.6 1.6 0 001.5 1H21a2 2 0 110 4h-.1a1.6 1.6 0 00-1.5 1z",
  // plug: ein Stecker — Plugins sind Zusatzdienste, die sich an den Server
  // "anschließen", nicht Bausteine der Website (dafür steht schon "box").
  plug: "M9 3v5M15 3v5M7 8h10v3a5 5 0 01-5 5 5 5 0 01-5-5V8zM12 16v5M9 21h6",
  logout: "M9 21H6a2 2 0 01-2-2V5a2 2 0 012-2h3M16 17l5-5-5-5M21 12H9",
  sun: "M12 4V2M12 22v-2M4 12H2M22 12h-2M5.6 5.6L4.2 4.2M19.8 19.8l-1.4-1.4M5.6 18.4l-1.4 1.4M19.8 4.2l-1.4 1.4M12 17a5 5 0 100-10 5 5 0 000 10z",
  moon: "M20.8 14.5A8.5 8.5 0 119.5 3.2a7 7 0 0011.3 11.3z",
  monitor: "M4 4h16v11H4zM9 20h6M12 15v5",
  download: "M12 3v12m0 0l-4.5-4.5M12 15l4.5-4.5M4 19h16",
  chevron: "M9 6l6 6-6 6",
  // transfer: zwei gegenläufige Pfeile — FTP ist Dateiübertragung, kein
  // Ordner. Teilte sich zuvor das Icon mit "Dateien", obwohl beides
  // unterschiedliche Funktionen sind.
  transfer: "M9 20V4M9 4L5 8M9 4l4 4M15 4v16M15 20l-4-4M15 20l4-4",
  menu: "M4 7h16M4 12h16M4 17h16",
  shield: "M12 3l8 4v5c0 5-3.4 8.5-8 9-4.6-.5-8-4-8-9V7z",
  close: "M6 6l12 12M18 6L6 18",
};
</script>

<template>
  <!-- Die Login-Ansicht bringt ihr eigenes Layout mit. -->
  <RouterView v-if="!session.user" />

  <div v-else class="flex min-h-screen" :style="{ background: 'var(--surface-page)' }">
    <!-- Nur unterhalb von lg: sichtbar — dort ist die Sidebar ein Off-Canvas-
         Panel, kein fester Teil des Layouts mehr, deshalb braucht es ein
         Abblenden dahinter, das sie beim Antippen wieder schließt. -->
    <div
      v-if="sidebarOpen"
      class="fixed inset-0 z-30 bg-black/40 lg:hidden"
      @click="sidebarOpen = false"
    />

    <aside
      class="fixed inset-y-0 left-0 z-40 flex w-64 shrink-0 -translate-x-full flex-col transition-transform duration-200 lg:static lg:w-60 lg:translate-x-0"
      :class="{ 'translate-x-0': sidebarOpen }"
      :style="{
        borderRight: '1px solid var(--line-hairline)',
        background: 'var(--surface-card)',
      }"
    >
      <div class="flex items-center gap-2.5 px-5 py-5">
        <svg
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M13 2L4.5 13.5H11l-1 8.5 8.5-11.5H12z"
            fill="var(--accent)"
          />
        </svg>
        <span class="text-[16px] font-semibold tracking-tight">VoltPanel</span>
        <button
          class="ml-auto flex h-8 w-8 items-center justify-center rounded-full lg:hidden"
          :style="{ color: 'var(--ink-secondary)' }"
          :aria-label="t('nav.close')"
          @click="sidebarOpen = false"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="paths.close" />
          </svg>
        </button>
      </div>

      <nav class="flex flex-1 flex-col gap-0.5 overflow-y-auto px-3 pb-3">
        <template v-for="g in navGroups" :key="g.group || '_root'">
          <div
            v-if="g.group"
            class="mt-3 mb-1 px-3 text-[11px] font-medium tracking-wide uppercase first:mt-0"
            :style="{ color: 'var(--ink-muted)' }"
          >
            {{ t(g.group) }}
          </div>
          <RouterLink
            v-for="item in g.items"
            :key="item.to"
            :to="item.to"
            class="group flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-[13px] transition-colors"
            :style="
              isActiveNav(item)
                ? {
                    background: 'var(--accent)',
                    color: 'var(--accent-contrast)',
                    fontWeight: 600,
                  }
                : { color: 'var(--ink-secondary)' }
            "
          >
            <svg
              width="17"
              height="17"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
              class="shrink-0"
            >
              <path :d="paths[item.icon]" />
            </svg>
            {{ labels[item.key] }}
            <!-- Ein Punkt, keine Zahl: es gibt genau ein Update oder keines.
                 Der Weg dorthin führt über die Einstellungen, wo auch steht,
                 was sich ändert. -->
            <span
              v-if="item.key === 'nav.settings' && update.available"
              class="ml-auto h-1.5 w-1.5 rounded-full"
              :style="{ background: isActiveNav(item) ? 'var(--accent-contrast)' : 'var(--series-2)' }"
              :title="t('update.available', { v: update.latest })"
            />
          </RouterLink>
        </template>

        <div class="my-2 border-t" :style="{ borderColor: 'var(--line-hairline)' }" />

        <button
          class="flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-left text-[13px] transition-colors hover:opacity-80"
          :style="{ color: 'var(--ink-secondary)' }"
          @click="onLogout"
        >
          <svg
            width="17"
            height="17"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
            class="shrink-0"
          >
            <path :d="paths.logout" />
          </svg>
          {{ t("nav.logout") }}
        </button>
      </nav>

      <div class="px-5 py-3 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
        VoltPanel {{ session.version?.version }}
      </div>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col overflow-x-hidden">
      <header
        class="flex items-center justify-between gap-3 px-6 py-3"
        :style="{ borderBottom: '1px solid var(--line-hairline)', background: 'var(--surface-card)' }"
      >
        <div class="flex min-w-0 items-center gap-3">
          <button
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full lg:hidden"
            :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
            :aria-label="t('nav.open')"
            @click="sidebarOpen = true"
          >
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path :d="paths.menu" />
            </svg>
          </button>

          <div
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-[13px] font-semibold"
            :style="{ background: 'var(--accent-soft)', color: 'var(--accent)' }"
            aria-hidden="true"
          >
            {{ avatarInitial }}
          </div>
          <div class="min-w-0 leading-tight">
            <div class="truncate text-[13px] font-medium">
              {{ session.user.display_name || session.user.email }}
            </div>
            <div class="truncate text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ session.user.role }} · {{ session.tenant?.name }}
            </div>
          </div>
          <div
            v-if="system"
            class="ml-2 hidden shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-[11px] sm:flex"
            :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
          >
            <span
              class="inline-block h-1.5 w-1.5 rounded-full"
              :style="{ background: 'var(--status-good)' }"
              aria-hidden="true"
            />
            {{ system.platform || system.os }} · {{ t('dash.uptime') }} {{ formatUptime(system.uptime) }}
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <RouterLink
            v-if="update.available && hasRole('admin')"
            to="/settings"
            class="hidden items-center gap-1.5 rounded-full px-3 py-1.5 text-[11px] font-medium sm:flex"
            :style="{ background: 'var(--accent-soft)', color: 'var(--accent)' }"
            :title="t('update.available', { v: update.latest })"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path :d="paths.download" />
            </svg>
            {{ t('update.available', { v: update.latest }) }}
          </RouterLink>

          <button
            class="flex h-8 w-8 items-center justify-center rounded-full transition-colors hover:opacity-80"
            :style="{ background: 'var(--surface-sunken)', color: 'var(--ink-secondary)' }"
            :title="t(`settings.theme${theme.mode.charAt(0).toUpperCase()}${theme.mode.slice(1)}`)"
            @click="cycleTheme"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path :d="paths[themeIcon[theme.mode]]" />
            </svg>
          </button>
        </div>
      </header>

      <div
        v-if="session.user.must_change_pw"
        class="px-8 py-2.5 text-[13px]"
        :style="{
          borderBottom: '1px solid var(--line-hairline)',
          background:
            'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
        }"
      >
        {{ t("common.mustChangePassword") }}
        <RouterLink to="/settings" class="ml-1 underline">{{
          t("settings.password")
        }}</RouterLink>
      </div>
      <main class="min-w-0 flex-1">
        <RouterView />
      </main>
    </div>
  </div>

  <!-- Ein einziger Dialog für die ganze App statt einer Instanz je
       Aufrufstelle — Ansichten rufen nur askConfirm() aus stores/confirm.js. -->
  <ConfirmDialog />
</template>
