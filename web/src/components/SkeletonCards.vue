<script setup>
// Dieselbe Idee wie SkeletonRows, für Ansichten, die als Kartenraster statt
// als Tabelle laden (Tenants, Apps, Plugins, Dienste, Mail-Domänen …).
defineProps({
  count: { type: Number, default: 4 },
  cols: { type: String, default: 'sm:grid-cols-2 lg:grid-cols-3' },
  height: { type: String, default: 'h-24' },
})
</script>

<template>
  <div class="grid gap-3" :class="cols" aria-hidden="true">
    <div
      v-for="i in count"
      :key="i"
      class="skeleton-bar panel-card bg-surface-sunken"
      :class="height"
    />
  </div>
</template>
