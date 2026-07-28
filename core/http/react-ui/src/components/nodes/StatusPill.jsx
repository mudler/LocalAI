import { statusConfig } from './nodeStatus'

export default function StatusPill({ status }) {
  const cfg = statusConfig[status] || statusConfig.unhealthy
  return (
    <span className="node-status" style={{ color: cfg.color }}>
      <span className="node-status__dot" style={{ background: cfg.color }} />
      {cfg.label}
    </span>
  )
}
