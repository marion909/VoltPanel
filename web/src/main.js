import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { loadSession } from './stores/session'
import { applyTheme } from './stores/theme'
import { i18n } from './i18n'
import './style.css'

applyTheme()
document.documentElement.lang = i18n.locale

// Erst die Sitzung klären, dann mounten — sonst blitzt bei jedem Reload
// kurz die Login-Maske auf, obwohl man angemeldet ist.
loadSession().finally(() => {
  createApp(App).use(router).mount('#app')
})
