<script setup>
import { computed } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { session, logout } from './stores/session'
import { t, i18n } from './i18n'

const route = useRoute()
const router = useRouter()

const nav = computed(() => [
  { to: '/', key: 'nav.dashboard', icon: 'grid' },
  { to: '/sites', key: 'nav.sites', icon: 'globe' },
  { to: '/services', key: 'nav.services', icon: 'server' },
  { to: '/audit', key: 'nav.audit', icon: 'list' },
  { to: '/settings', key: 'nav.settings', icon: 'gear' },
])

// i18n.locale wird gelesen, damit die Beschriftungen beim Sprachwechsel neu
// gerendert werden — sonst bliebe die Navigation stehen.
const labels = computed(() => {
  void i18n.locale
  return Object.fromEntries(nav.value.map((n) => [n.key, t(n.key)]))
})

async function onLogout() {
  await logout()
  router.push({ name: 'login' })
}

const paths = {
  grid: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
  globe: 'M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c2.5 2.4 3.8 5.5 3.8 9s-1.3 6.6-3.8 9c-2.5-2.4-3.8-5.5-3.8-9S9.5 5.4 12 3z',
  server: 'M4 5h16v5H4zM4 14h16v5H4zM7.5 7.5h.01M7.5 16.5h.01',
  list: 'M4 6h16M4 12h16M4 18h10',
  gear: 'M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.6 1.6 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.6 1.6 0 00-1.8-.3 1.6 1.6 0 00-1 1.5V21a2 2 0 11-4 0v-.1A1.6 1.6 0 009 19.4a1.6 1.6 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.6 1.6 0 00.3-1.8 1.6 1.6 0 00-1.5-1H3a2 2 0 110-4h.1A1.6 1.6 0 004.6 9a1.6 1.6 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.6 1.6 0 001.8.3H9a1.6 1.6 0 001-1.5V3a2 2 0 114 0v.1a1.6 1.6 0 001 1.5 1.6 1.6 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.6 1.6 0 00-.3 1.8V9a1.6 1.6 0 001.5 1H21a2 2 0 110 4h-.1a1.6 1.6 0 00-1.5 1z',
}
</script>

<template>
  <!-- Die Login-Ansicht bringt ihr eigenes Layout mit. -->
  <RouterView v-if="!session.user" />

  <div v-else class="flex min-h-screen">
    <aside
      class="flex w-56 shrink-0 flex-col border-r"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
    >
      <div class="flex items-center gap-2 px-5 py-5">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M13 2L4.5 13.5H11l-1 8.5 8.5-11.5H12z" fill="var(--series-1)" />
        </svg>
        <span class="text-[15px] font-semibold tracking-tight">VoltPanel</span>
      </div>

      <nav class="flex flex-1 flex-col gap-0.5 px-3">
        <RouterLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-[13px] transition-colors"
          :style="
            route.path === item.to
              ? { background: 'var(--surface-sunken)', color: 'var(--ink-primary)', fontWeight: 500 }
              : { color: 'var(--ink-secondary)' }
          "
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="paths[item.icon]" />
          </svg>
          {{ labels[item.key] }}
        </RouterLink>
      </nav>

      <div class="border-t px-3 py-3" :style="{ borderColor: 'var(--border-ring)' }">
        <div class="px-2.5 pb-2">
          <div class="truncate text-[13px]">{{ session.user.display_name || session.user.email }}</div>
          <div class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
            {{ session.user.role }} · {{ session.tenant?.name }}
          </div>
        </div>
        <button
          class="w-full rounded-md px-2.5 py-2 text-left text-[13px] transition-colors hover:opacity-80"
          :style="{ color: 'var(--ink-secondary)' }"
          @click="onLogout"
        >
          {{ t('nav.logout') }}
        </button>
        <div class="px-2.5 pt-2 text-[11px]" :style="{ color: 'var(--ink-muted)' }">
          {{ session.version?.version }}
        </div>
      </div>
    </aside>

    <main class="min-w-0 flex-1 overflow-x-hidden">
      <div
        v-if="session.user.must_change_pw"
        class="border-b px-8 py-2.5 text-[13px]"
        :style="{
          borderColor: 'var(--border-ring)',
          background: 'color-mix(in srgb, var(--status-warning) 14%, var(--surface-card))',
        }"
      >
        {{ t('common.mustChangePassword') }}
        <RouterLink to="/settings" class="ml-1 underline">{{ t('settings.password') }}</RouterLink>
      </div>
      <RouterView />
    </main>
  </div>
</template>
