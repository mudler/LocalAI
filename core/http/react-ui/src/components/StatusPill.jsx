// Single source for status visuals. Maps a semantic status to a token-driven
// dot + label. Replaces per-page hex status maps over time.
const STATUS = {
  healthy: 'success',
  online: 'success',
  warning: 'warning',
  draining: 'warning',
  error: 'error',
  unhealthy: 'error',
  loading: 'info',
  idle: 'muted',
}
export default function StatusPill({ status, label, className = '' }) {
  const tone = STATUS[status] || 'muted'
  return (
    <span className={`status-pill status-pill--${tone} ${className}`.trim()}>
      <span className="status-pill__dot" aria-hidden="true" />
      {label != null ? label : status}
    </span>
  )
}
