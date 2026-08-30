// Anzeigeformate an einer Stelle, damit dieselbe Zahl überall gleich aussieht.

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

export function formatBytes(bytes, digits = 1) {
  const n = Number(bytes) || 0
  if (n < 1024) return `${n} B`

  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(digits)} ${units[unit]}`
}

export function formatRate(bytesPerSecond) {
  return `${formatBytes(bytesPerSecond, 1)}/s`
}

export function formatClock(timestamp) {
  if (!timestamp) return '—'
  return new Date(timestamp * 1000).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export function formatDateTime(timestamp) {
  if (!timestamp) return '—'
  return new Date(timestamp * 1000).toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// formatUptime bricht Sekunden auf die zwei größten sinnvollen Einheiten
// herunter — "12 T 4 h" statt "1054832 s".
export function formatUptime(seconds) {
  const s = Number(seconds) || 0
  const days = Math.floor(s / 86400)
  const hours = Math.floor((s % 86400) / 3600)
  const minutes = Math.floor((s % 3600) / 60)

  if (days > 0) return `${days} T ${hours} h`
  if (hours > 0) return `${hours} h ${minutes} min`
  return `${minutes} min`
}
