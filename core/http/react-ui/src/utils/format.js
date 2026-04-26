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
