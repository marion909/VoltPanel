// Anzeigeformate an einer Stelle, damit dieselbe Zahl überall gleich aussieht.

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

export function formatBytes(bytes, digits = 1) {
  const n = Math.max(Number(bytes) || 0, 0)
  if (n < 1024) return `${n} B`

  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(digits)} ${units[unit]}`
}

// daysLeft ist die Restlaufzeit eines Zertifikats bis not_after (Unix-Sekunden).
export function daysLeft(notAfter) {
  return Math.max(0, Math.ceil((notAfter * 1000 - Date.now()) / 86400000))
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

// Eine Ampel-Schwelle, an einer Stelle statt dreimal unabhängig verdrahtet
// (RingGauge, QuotaBar, Dashboard-Speicherbalken hatten bisher je ihre
// eigene Kopie derselben 75/90-Grenze) — ändert sich die Schwelle künftig,
// reicht eine Änderung hier.
const STATUS_CRITICAL_AT = 90
const STATUS_WARNING_AT = 75

export function statusForPercent(percent) {
  if (percent >= STATUS_CRITICAL_AT) return 'var(--status-critical)'
  if (percent >= STATUS_WARNING_AT) return 'var(--status-warning)'
  return 'var(--status-good)'
}

// Dieselbe Schwelle als Übersetzungsschlüssel, für Stellen wie RingGauge,
// die den Zustand zusätzlich als Wort ausgeben (Farbe steht nie allein).
export function statusKeyForPercent(percent) {
  if (percent >= STATUS_CRITICAL_AT) return 'kritisch'
  if (percent >= STATUS_WARNING_AT) return 'hoch'
  return 'normal'
}

// permsToOctal liest Gos rwx-Darstellung ("-rw-r--r--") in die gewohnte
// oktale Schreibweise ("644") um — für die Anzeige und als Vorgabewert beim
// Ändern der Rechte. setuid/setgid/sticky (s/t) zählen dabei als "x".
export function permsToOctal(mode) {
  const bits = String(mode || '').slice(-9)
  if (bits.length !== 9) return '644'
  let out = ''
  for (let i = 0; i < 9; i += 3) {
    const group = bits.slice(i, i + 3)
    const r = group[0] !== '-' ? 4 : 0
    const w = group[1] !== '-' ? 2 : 0
    const x = group[2] !== '-' && group[2] !== 'S' && group[2] !== 'T' ? 1 : 0
    out += String(r + w + x)
  }
  return out
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
