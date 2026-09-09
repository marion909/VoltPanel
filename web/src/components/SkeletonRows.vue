<script setup>
// Platzhalter für eine Tabellen-Karte, solange die echten Daten noch unterwegs
// sind — ersetzt die reine "Wird geladen …"-Zeile, die beim Eintreffen der
// Daten einen Layoutsprung verursacht (Text weg, Tabelle mit Kopf- und
// mehreren Datenzeilen da). Absichtlich kein Text/keine Rolle: der Rahmen
// selbst trägt schon "Wird geladen" über den umgebenden Ladezustand, ein
// zweites "lädt" hier wäre für Screenreader nur Wiederholung.
defineProps({
  cols: { type: Number, default: 4 },
  rows: { type: Number, default: 3 },
})
</script>

<template>
  <div class="panel-card overflow-hidden" aria-hidden="true">
    <div
      class="flex gap-4 border-b px-4 py-3 border-line-hairline"
    >
      <div
        v-for="c in cols"
        :key="c"
        class="skeleton-bar h-2.5 flex-1 rounded-full"
        :style="{ background: 'var(--surface-sunken)', maxWidth: c === 1 ? '40%' : '100%' }"
      />
    </div>
    <div
      v-for="r in rows"
      :key="r"
      class="flex items-center gap-4 border-b px-4 py-4 last:border-0 border-line-hairline"
    >
      <div
        v-for="c in cols"
        :key="c"
        class="skeleton-bar h-3 flex-1 rounded-full"
        :style="{ background: 'var(--surface-sunken)', maxWidth: c === 1 ? '40%' : '100%' }"
      />
    </div>
  </div>
</template>
