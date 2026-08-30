import { reactive } from 'vue'

// 'system' bedeutet: kein data-theme setzen und prefers-color-scheme wirken lassen.
export const theme = reactive({
  mode: localStorage.getItem('volt.theme') || 'system',
})

export function setTheme(mode) {
  theme.mode = mode
  localStorage.setItem('volt.theme', mode)
  applyTheme()
}

export function applyTheme() {
  const root = document.documentElement
  if (theme.mode === 'system') {
    root.removeAttribute('data-theme')
  } else {
    root.setAttribute('data-theme', theme.mode)
  }
}
