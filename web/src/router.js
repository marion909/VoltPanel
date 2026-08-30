import { createRouter, createWebHistory } from 'vue-router'
import { session } from './stores/session'

import Login from './views/Login.vue'
import Dashboard from './views/Dashboard.vue'
import Sites from './views/Sites.vue'
import Services from './views/Services.vue'
import Audit from './views/Audit.vue'
import Settings from './views/Settings.vue'

const routes = [
  { path: '/login', name: 'login', component: Login, meta: { public: true } },
  { path: '/', name: 'dashboard', component: Dashboard },
  { path: '/sites', name: 'sites', component: Sites },
  { path: '/services', name: 'services', component: Services },
  { path: '/audit', name: 'audit', component: Audit },
  { path: '/settings', name: 'settings', component: Settings },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// Der Router hält nur die Oberfläche zurück; die eigentliche Autorisierung
// macht der Server bei jeder Anfrage.
router.beforeEach((to) => {
  if (to.meta.public) return true
  if (!session.user) return { name: 'login', query: { redirect: to.fullPath } }
  return true
})
