// Schmaler Fetch-Wrapper. Kein axios: das Panel braucht keine 30 kB dafür.

// Das CSRF-Token steht im lesbaren Cookie und muss bei jeder schreibenden
// Anfrage als Header zurückkommen — die Gegenprobe zum Session-Cookie,
// das eine fremde Seite zwar mitschicken lassen, aber nicht auslesen kann.
function csrfToken() {
  const match = document.cookie.match(/(?:^|;\s*)volt_csrf=([^;]*)/)
  return match ? decodeURIComponent(match[1]) : ''
}

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// Das Panel kann unter einem geheimen Pfadpräfix laufen. Der Ort steht im
// <base>-Tag, das der Server einsetzt — document.baseURI löst ihn auf.
// import.meta.env.BASE_URL wäre der Wert von der Bauzeit und kennt den
// Präfix nicht, der erst bei der Installation entsteht.
const base = document.baseURI.replace(/\/$/, '')

async function request(method, path, body) {
  const headers = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken()

  const res = await fetch(`${base}/api/v1${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (res.status === 204) return null

  let payload = null
  const type = res.headers.get('content-type') || ''
  if (type.includes('application/json')) {
    payload = await res.json().catch(() => null)
  }

  if (!res.ok) {
    throw new ApiError(payload?.error || `HTTP ${res.status}`, res.status)
  }
  return payload
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body ?? {}),
  patch: (path, body) => request('PATCH', path, body ?? {}),
  del: (path) => request('DELETE', path),

  // Vollständige URL zu einem API-Pfad. Für Downloads, die der Browser selbst
  // holt — das Session-Cookie geht dabei mit.
  url: (path) => `${base}/api/v1${path}`,

  // Multipart passt nicht in den JSON-Wrapper: der Browser muss den
  // Content-Type mit seiner boundary selbst setzen.
  async upload(path, form) {
    const res = await fetch(`${base}/api/v1${path}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrfToken() },
      body: form,
    })
    const payload = (res.headers.get('content-type') || '').includes('application/json')
      ? await res.json().catch(() => null)
      : null
    if (!res.ok) throw new ApiError(payload?.error || `HTTP ${res.status}`, res.status)
    return payload
  },

  // WebSocket auf einen API-Pfad. Das Session-Cookie geht beim Upgrade
  // automatisch mit, der Server prüft zusätzlich den Origin.
  socket(path) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return new WebSocket(`${proto}//${location.host}${base}/api/v1${path}`)
  },

  metricsSocket() {
    return api.socket('/system/metrics/stream')
  },
}
