import { createRouter, createWebHistory } from 'vue-router'
import { session, hasRole } from './stores/session'

import Login from './views/Login.vue'
import Dashboard from './views/Dashboard.vue'
import Sites from './views/Sites.vue'
import Apps from './views/Apps.vue'
import Mail from './views/Mail.vue'
import Deploys from './views/Deploys.vue'
import SiteDetail from './views/SiteDetail.vue'
import Databases from './views/Databases.vue'
import SQL from './views/SQL.vue'
import Backups from './views/Backups.vue'
import Files from './views/Files.vue'
import Cronjobs from './views/Cronjobs.vue'
import FTP from './views/FTP.vue'
import Tenants from './views/Tenants.vue'
import Services from './views/Services.vue'
import Audit from './views/Audit.vue'
import Settings from './views/Settings.vue'

const routes = [
  { path: '/login', name: 'login', component: Login, meta: { public: true } },
  { path: '/', name: 'dashboard', component: Dashboard },
  { path: '/sites', name: 'sites', component: Sites },
  { path: '/sites/:id', name: 'site-detail', component: SiteDetail },
  { path: '/apps', name: 'apps', component: Apps },
  { path: '/deploys', name: 'deploys', component: Deploys },
  { path: '/databases', name: 'databases', component: Databases },
  { path: '/sql', name: 'sql', component: SQL },
  { path: '/files', name: 'files', component: Files },
  { path: '/ftp', name: 'ftp', component: FTP },
  { path: '/mail', name: 'mail', component: Mail },
  { path: '/cronjobs', name: 'cronjobs', component: Cronjobs },
  { path: '/backups', name: 'backups', component: Backups },
  { path: '/services', name: 'services', component: Services },
  { path: '/tenants', name: 'tenants', component: Tenants, meta: { minRole: 'admin' } },
  { path: '/audit', name: 'audit', component: Audit },
  { path: '/settings', name: 'settings', component: Settings },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  // Ohne Argument liest vue-router das <base>-Tag, das der Server mit dem
  // tatsaechlichen Pfadpraefix einsetzt. import.meta.env.BASE_URL waere der
  // Wert von der Bauzeit und damit falsch.
  history: createWebHistory(),
  routes,
})

// Der Router hält nur die Oberfläche zurück; die eigentliche Autorisierung
// macht der Server bei jeder Anfrage. Eine Route auszublenden ist Bequemlichkeit,
// kein Schutz.
router.beforeEach((to) => {
  if (to.meta.public) return true
  if (!session.user) return { name: 'login', query: { redirect: to.fullPath } }
  if (to.meta.minRole && !hasRole(to.meta.minRole)) return { name: 'dashboard' }
  return true
})
