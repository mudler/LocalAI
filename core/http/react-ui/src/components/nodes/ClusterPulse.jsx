import { formatVRAM } from './nodeStatus'

export default function ClusterPulse({ nodes }) {
  const total = nodes.length
  const healthy = nodes.filter(n => n.status === 'healthy').length
  const draining = nodes.filter(n => n.status === 'draining').length
  const usedVRAM = nodes.reduce((s, n) =>
    (n.total_vram && n.available_vram != null) ? s + (n.total_vram - n.available_vram) : s, 0)
  const vramStr = formatVRAM(usedVRAM)
  return (
    <p className="cluster-pulse">
      <span className="cluster-pulse__strong">{total} {total === 1 ? 'node' : 'nodes'}</span>
      {' · '}<span style={{ color: 'var(--color-success)' }}>{healthy} healthy</span>
      {draining > 0 && <>{' · '}<span style={{ color: 'var(--color-warning)' }}>{draining} draining</span></>}
      {vramStr && <>{' · '}{vramStr} VRAM in use</>}
    </p>
  )
}
