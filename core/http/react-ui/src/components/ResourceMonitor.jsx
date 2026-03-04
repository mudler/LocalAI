import { useResources } from '../hooks/useResources'
import { formatBytes, percentColor, vendorColor } from '../utils/format'

export default function ResourceMonitor() {
  const { resources, loading } = useResources()

  if (loading || !resources) {
    return <div className="resource-monitor" style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>Loading resources...</div>
  }

  const gpus = resources.gpus || []
  const ram = resources.ram || {}
  const aggregate = resources.aggregate || {}
  const isGpu = resources.type === 'gpu' && gpus.length > 0

  return (
    <div className="resource-monitor">
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
        <h3 className="resource-monitor-title" style={{ margin: 0 }}>
          <i className="fas fa-chart-bar" /> System Resources
        </h3>
        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', alignItems: 'center' }}>
          {isGpu && gpus.length > 1 && (
            <span className="badge badge-info">{gpus.length} GPUs</span>
          )}
          {resources.reclaimer_enabled && (
            <span className="badge badge-success">Reclaimer Active</span>
          )}
        </div>
      </div>

      {isGpu ? (
        <div className="resource-gpu-list">
          {gpus.map((gpu, i) => {
            const pct = gpu.usage_percent || 0
            const color = percentColor(pct)
            const vColor = vendorColor(gpu.vendor)
            return (
              <div key={i} className="resource-gpu-card">
                <div className="resource-gpu-header">
                  <span className="resource-gpu-name" style={{ maxWidth: '200px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {gpu.name || `GPU ${i}`}
                  </span>
                  {gpu.vendor && (
                    <span className="resource-gpu-vendor" style={{ background: `${vColor}20`, color: vColor }}>
                      {gpu.vendor}
                    </span>
                  )}
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xs)' }}>
                  <div className="resource-bar-container" style={{ flex: 1 }}>
                    <div className="resource-bar" style={{ width: `${pct}%`, background: color }} />
                  </div>
                  <span style={{ fontSize: '0.8125rem', fontWeight: 600, fontFamily: "'JetBrains Mono', monospace", color, minWidth: '3em', textAlign: 'right' }}>
                    {pct.toFixed(0)}%
                  </span>
                </div>
                <div className="resource-gpu-stats">
                  <span>Used: {formatBytes(gpu.used_vram)}</span>
                  <span>Total: {formatBytes(gpu.total_vram)}</span>
                </div>
              </div>
            )
          })}
        </div>
      ) : (
        /* RAM display */
        <div className="resource-gpu-card">
          <div className="resource-gpu-header">
            <span className="resource-gpu-name">System RAM</span>
            <span className="resource-gpu-vendor" style={{ background: 'var(--color-accent-light)', color: 'var(--color-accent)' }}>
              Memory
            </span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-xs)' }}>
            <div className="resource-bar-container" style={{ flex: 1 }}>
              <div className="resource-bar" style={{ width: `${ram.usage_percent || 0}%`, background: percentColor(ram.usage_percent || 0) }} />
            </div>
            <span style={{ fontSize: '0.8125rem', fontWeight: 600, fontFamily: "'JetBrains Mono', monospace", color: percentColor(ram.usage_percent || 0), minWidth: '3em', textAlign: 'right' }}>
              {(ram.usage_percent || 0).toFixed(0)}%
            </span>
          </div>
          <div className="resource-gpu-stats">
            <span>Used: {formatBytes(ram.used || 0)}</span>
            <span>Total: {formatBytes(ram.total || 0)}</span>
          </div>
        </div>
      )}

      {/* Aggregate for multi-GPU */}
      {isGpu && aggregate.gpu_count > 1 && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', marginTop: 'var(--spacing-sm)', display: 'flex', justifyContent: 'space-between' }}>
          <span>Total VRAM</span>
          <span style={{ fontFamily: "'JetBrains Mono', monospace" }}>
            {formatBytes(aggregate.used_memory)} / {formatBytes(aggregate.total_memory)} ({aggregate.usage_percent?.toFixed(1)}%)
          </span>
        </div>
      )}

      {/* Storage */}
      {resources.storage_size != null && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', marginTop: 'var(--spacing-sm)', display: 'flex', justifyContent: 'space-between' }}>
          <span>Models storage</span>
          <span style={{ fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-primary)' }}>
            {formatBytes(resources.storage_size)}
          </span>
        </div>
      )}
    </div>
  )
}
