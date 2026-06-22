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
  loading: { bg: 'var(--color-primary-light)', color: 'var(--color-primary)', border: 'var(--color-primary-border)' },
  unloading: { bg: 'var(--color-warning-light)', color: 'var(--color-warning)', border: 'var(--color-warning-border)' },
  idle: { bg: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)', border: 'var(--color-border-subtle)' },
}

export function formatVRAM(bytes) {
  if (!bytes || bytes === 0) return null
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}
