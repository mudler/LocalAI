import { useResources } from '../hooks/useResources'
import { formatBytes, percentColor, vendorColor } from '../utils/format'

export default function ResourceMonitor() {
  const { resources, loading, error } = useResources()

  return (
    <ResourceMonitorView
      resources={resources}
      loading={loading}
      unavailable={Boolean(error)}
    />
  )
}

export function ResourceMonitorView({
  resources,
  loading = false,
  unavailable = false,
  title = 'System Resources',
  loadingText = 'Loading resources...',
  unavailableText = 'Resource data unavailable',
  emptyText = 'No resource data reported',
  copy = {},
  testId,
}) {
  if (loading) {
    return <div className="resource-monitor text-note" data-testid={testId}>{loadingText}</div>
  }

  if (unavailable || !resources) {
    return <div className="resource-monitor text-note" data-testid={testId}>{unavailableText}</div>
  }

  const gpus = resources.gpus || []
  const aggregate = resources.aggregate || {}
  const ram = resources.ram || aggregate.ram || {}
  const isGpu = resources.type === 'gpu' && gpus.length > 0
  const hasRam = ram.total != null || ram.total_bytes != null || ram.used != null || ram.used_bytes != null || ram.usage_percent != null
  const hasStorage = resources.storage_size != null
  const labels = {
    gpuCount: count => `${count} GPUs`,
    reclaimer: 'Reclaimer Active',
    used: 'Used',
    total: 'Total',
    systemRam: 'System RAM',
    memory: 'Memory',
    totalVram: 'Total VRAM',
    storage: 'Models storage',
    ...copy,
  }

  if (!isGpu && !hasRam && !hasStorage) {
    return <div className="resource-monitor text-note" data-testid={testId}>{emptyText}</div>
  }

  return (
    <div className="resource-monitor" data-testid={testId}>
      <div className="hstack hstack--between mb-sm">
        <h3 className="resource-monitor-title m-0">
          <i className="fas fa-chart-bar" aria-hidden="true" /> {title}
        </h3>
        <div className="resource-monitor-badges">
          {isGpu && gpus.length > 1 && (
            <span className="badge badge-info">{labels.gpuCount(gpus.length)}</span>
          )}
          {resources.reclaimer_enabled && (
            <span className="badge badge-success">{labels.reclaimer}</span>
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
                  <span className="resource-gpu-name resource-gpu-name--truncate">
                    {gpu.name || `GPU ${i}`}
                  </span>
                  {gpu.vendor && (
                    <span className="resource-gpu-vendor resource-gpu-vendor--dynamic" style={{ '--resource-vendor-color': vColor }}>
                      {gpu.vendor}
                    </span>
                  )}
                </div>
                <div className="resource-meter">
                  <div className="resource-bar-container flex-1">
                    <div className="resource-bar" style={{ '--resource-width': `${pct}%`, '--resource-color': color }} />
                  </div>
                  <span className="resource-meter__value" style={{ '--resource-color': color }}>
                    {pct.toFixed(0)}%
                  </span>
                </div>
                <div className="resource-gpu-stats">
                  <span>{labels.used}: {formatBytes(gpu.used_vram)}</span>
                  <span>{labels.total}: {formatBytes(gpu.total_vram)}</span>
                </div>
              </div>
            )
          })}
        </div>
      ) : hasRam ? (
        /* RAM display */
        <div className="resource-gpu-card">
          <div className="resource-gpu-header">
            <span className="resource-gpu-name">{labels.systemRam}</span>
            <span className="resource-gpu-vendor resource-gpu-vendor--memory">
              {labels.memory}
            </span>
          </div>
          <div className="resource-meter">
            <div className="resource-bar-container flex-1">
              <div className="resource-bar" style={{ '--resource-width': `${ram.usage_percent || 0}%`, '--resource-color': percentColor(ram.usage_percent || 0) }} />
            </div>
            <span className="resource-meter__value" style={{ '--resource-color': percentColor(ram.usage_percent || 0) }}>
              {(ram.usage_percent || 0).toFixed(0)}%
            </span>
          </div>
          <div className="resource-gpu-stats">
            <span>{labels.used}: {formatBytes(ram.used ?? ram.used_bytes ?? 0)}</span>
            <span>{labels.total}: {formatBytes(ram.total ?? ram.total_bytes ?? 0)}</span>
          </div>
        </div>
      ) : null}

      {/* Aggregate for multi-GPU */}
      {isGpu && aggregate.gpu_count > 1 && (
        <div className="resource-summary-row">
          <span>{labels.totalVram}</span>
          <span className="text-mono">
            {formatBytes(aggregate.used_memory)} / {formatBytes(aggregate.total_memory)} ({aggregate.usage_percent?.toFixed(1)}%)
          </span>
        </div>
      )}

      {/* Storage */}
      {resources.storage_size != null && (
        <div className="resource-summary-row">
          <span>{labels.storage}</span>
          <span className="resource-summary-row__value">
            {formatBytes(resources.storage_size)}
          </span>
        </div>
      )}
    </div>
  )
}
