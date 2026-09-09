import { reactive } from 'vue'

// Ein einzelner geteilter Zustand statt einer Instanz pro Aufrufstelle: die
// Modal-Komponente wird genau einmal in App.vue gerendert, jede Ansicht
// ruft nur askConfirm() und wartet auf das Promise — dasselbe Muster wie
// session/theme/update als schlanke reaktive Stores statt Pinia.
export const confirmState = reactive({
  open: false,
  title: '',
  message: '',
  details: [],
  confirmLabel: '',
  cancelLabel: '',
  danger: true,
  resolve: null,
})

// Ersetzt window.confirm(): löst mit true/false auf, statt den Thread zu
// blockieren — Aufrufstellen wechseln von `if (!confirm(text))` zu
// `if (!(await askConfirm(text)))`, der Rest bleibt unverändert.
export function askConfirm(message, opts = {}) {
  return new Promise((resolve) => {
    if (confirmState.resolve) {
      // Ein zweiter Dialog vor der Antwort auf den ersten wäre ein UI-Bug,
      // kein Nutzerfehler — der alte wird als abgebrochen aufgelöst, statt
      // sein Promise für immer offen zu lassen.
      confirmState.resolve(false)
    }
    confirmState.open = true
    confirmState.title = opts.title || ''
    confirmState.message = message
    confirmState.details = opts.details || []
    confirmState.confirmLabel = opts.confirmLabel || ''
    confirmState.cancelLabel = opts.cancelLabel || ''
    confirmState.danger = opts.danger !== false
    confirmState.resolve = resolve
  })
}

export function resolveConfirm(value) {
  const resolve = confirmState.resolve
  confirmState.open = false
  confirmState.resolve = null
  if (resolve) resolve(value)
}
