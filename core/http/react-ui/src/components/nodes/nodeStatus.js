export const statusConfig = {
  healthy: { color: 'var(--color-success)', label: 'Healthy' },
  unhealthy: { color: 'var(--color-error)', label: 'Unhealthy' },
  offline: { color: 'var(--color-error)', label: 'Offline' },
  registering: { color: 'var(--color-primary)', label: 'Registering' },
  draining: { color: 'var(--color-warning)', label: 'Draining' },
  pending: { color: 'var(--color-warning)', label: 'Pending Approval' },
}

export const modelStateConfig = {
  loaded: { bg: 'var(--color-success-light)', color: 'var(--color-success)', border: 'var(--color-success-border)' },
  staging: { bg: 'var(--color-primary-light)', color: 'var(--color-primary)', border: 'var(--color-primary-border)' },
  loading: { bg: 'var(--color-primary-light)', color: 'var(--color-primary)', border: 'var(--color-primary-border)' },
  unloading: { bg: 'var(--color-warning-light)', color: 'var(--color-warning)', border: 'var(--color-warning-border)' },
  idle: { bg: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)', border: 'var(--color-border-subtle)' },
}

export function formatVRAM(bytes) {
  if (!bytes || bytes === 0) return null
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

export function formatBytes(bytes) {
  if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes < 0) return 'No data'
  if (bytes < 1024) return `${Math.round(bytes)} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes / 1024
  let unit = units[0]
  for (let index = 1; index < units.length && value >= 1024; index += 1) {
    value /= 1024
    unit = units[index]
  }
  const precision = value >= 10 ? 0 : 1
  return `${value.toFixed(precision).replace(/\.0$/, '')} ${unit}`
}

export function formatCapacity(used, total) {
  if (!(total > 0)) return 'No data'
  return `${formatBytes(used)} / ${formatBytes(total)}`
}

export function timeAgo(dateString) {
  if (!dateString) return 'never'
  const seconds = Math.floor((Date.now() - new Date(dateString).getTime()) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}
