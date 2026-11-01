import StatusChip from './ui/StatusChip'
import { resolveStatus, legacyPillTone } from './ui/status'

// The old name of StatusChip, kept for its callers (failover, the add-on
// SDK's Badge). It draws a StatusChip and keeps the old classes on the root
// (status-pill, status-pill--success|warning|error|info|muted), so selectors
// that read them still match. New code uses StatusChip.
export default function StatusPill({ status, label, className = '' }) {
  const { tone } = resolveStatus({ status })
  return (
    <StatusChip
      status={status}
      label={label != null ? label : status}
      className={`status-pill status-pill--${legacyPillTone(tone)} ${className}`.trim()}
    />
  )
}
