import { reactive } from 'vue'
import { api } from '../api'

// Ein schlanker reaktiver Store statt Pinia — die App hat genau einen
// globalen Zustand, und der passt hierher.
export const session = reactive({
  user: null,
  tenant: null,
  version: null,
  setupRequired: false,
  ready: false,
})

export async function loadSession() {
  try {
    const me = await api.get('/auth/me')
    session.user = me.user
    session.tenant = me.tenant
    session.version = me.version
  } catch {
    // 401 ist hier der Normalfall: niemand angemeldet.
    session.user = null
    try {
      const state = await api.get('/auth/state')
      session.setupRequired = state.setup_required
      session.version = state.version
    } catch {
      /* Panel nicht erreichbar — die Login-Ansicht meldet das selbst. */
    }
  } finally {
    session.ready = true
  }
}

export function setUser(user) {
  session.user = user
}

export async function logout() {
  try {
    await api.post('/auth/logout')
  } finally {
    session.user = null
    session.tenant = null
  }
}

// Dieselbe Rangordnung wie im Server. Sie steuert nur, was die Oberfläche
// zeigt — durchgesetzt wird sie serverseitig bei jeder Anfrage.
const roleRank = { owner: 4, admin: 3, reseller: 2, customer: 1 }

export function hasRole(minimum) {
  return (roleRank[session.user?.role] ?? 0) >= (roleRank[minimum] ?? 99)
}

export function isAdmin() {
  return hasRole('admin')
}
