export function formatBytes(bytes) {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

export function percentColor(pct) {
  if (pct > 90) return 'var(--color-error)'
  if (pct > 70) return 'var(--color-warning)'
  return 'var(--color-success)'
}

// normalizeTimestampMs converts a timestamp emitted by the backend into JS
// milliseconds, regardless of its encoding. The agent SSE bridge emits the
// json_message timestamp in three different shapes depending on deploy mode:
// an RFC3339 string (standalone agent pool), Unix milliseconds (local
// dispatcher), or Unix nanoseconds (older NATS path). A numeric value is
// classified by magnitude (s / ms / us / ns) so any of them yields a sane
// epoch. Falls back to Date.now() for null/empty/unparseable input.
export function normalizeTimestampMs(ts) {
  if (ts === null || ts === undefined || ts === '') return Date.now()
  if (typeof ts === 'string') {
    const parsed = Date.parse(ts)
    return Number.isNaN(parsed) ? Date.now() : parsed
  }
  if (typeof ts !== 'number' || !Number.isFinite(ts)) return Date.now()
  if (ts > 1e17) return Math.floor(ts / 1e6) // nanoseconds
  if (ts > 1e14) return Math.floor(ts / 1e3) // microseconds
  if (ts > 1e11) return ts                    // milliseconds
  return ts * 1000                            // seconds
}

export function formatTimestamp(ts) {
  if (!ts) return '-'
  const d = new Date(ts)
  return d.toLocaleTimeString() + '.' + String(d.getMilliseconds()).padStart(3, '0')
}

export function relativeTime(ts) {
  if (!ts) return ''
  const diff = Date.now() - ts
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return 'Just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return new Date(ts).toLocaleDateString()
}

export function generateId() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2)
}

export function vendorColor(vendor) {
  if (!vendor) return 'var(--color-accent)'
  const v = vendor.toLowerCase()
  if (v.includes('nvidia')) return '#76b900'
  if (v.includes('amd')) return '#ed1c24'
  if (v.includes('intel')) return '#0071c5'
  if (v.includes('apple')) return '#a2aaad'
  return 'var(--color-accent)'
}
