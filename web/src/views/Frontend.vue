<script setup>
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { t } from '../i18n'

const route = useRoute()

const tabs = [
  { to: '/frontend/sites', key: 'nav.sites', match: '/frontend/sites' },
  { to: '/frontend/apps', key: 'nav.apps', match: '/frontend/apps' },
  { to: '/frontend/deploys', key: 'nav.deploys', match: '/frontend/deploys' },
]

const activeTabs = computed(() =>
  Object.fromEntries(tabs.map((tab) => [tab.to, route.path.startsWith(tab.match)])),
)
</script>

<template>
  <div class="border-b px-8 pt-6" :style="{ borderColor: 'var(--border-ring)' }">
    <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-[18px] font-semibold tracking-tight">{{ t('nav.frontend') }}</h1>
      </div>
    </div>
    <nav class="flex gap-1 overflow-x-auto">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.to"
        :to="tab.to"
        class="border-b-2 px-3 py-2 text-[13px] transition-colors"
        :style="
          activeTabs[tab.to]
            ? {
                borderColor: 'var(--series-1)',
                color: 'var(--ink-primary)',
                fontWeight: 500,
              }
            : {
                borderColor: 'transparent',
                color: 'var(--ink-secondary)',
              }
        "
      >
        {{ t(tab.key) }}
      </RouterLink>
    </nav>
  </div>
  <RouterView />
</template>
