import ActionMenu from '../ActionMenu'
import { SortButton } from './ModelFleetTable'
import { formatBytes } from './nodeStatus'
import { uptime } from '../../utils/localHost'

// The single-node counterpart of ModelFleetTable. A cluster row is about
// placement (replicas, nodes, in-flight work); a row here is about one
// process on this host, so the columns are what that process is costing.

function UsageCell({ percent, label, tone, title, emptyLabel = 'No data' }) {
  if (percent == null) return <span className="fleet-table__unknown" aria-label={title} title={title}>{emptyLabel}</span>
  const clamped = Math.min(100, Math.max(0, percent))
  return (
    <div className={`fleet-table__resource fleet-table__resource--${tone}`} aria-label={title}>
      <progress className="fleet-table__resource-track" max="100" value={clamped} aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}

export default function LocalModelTable({ models, onViewLogs, onStop, stoppingName, sort, onSortChange, now = Date.now() }) {
  return (
    <div className="fleet-table-wrap model-fleet-table-wrap">
      <table className="fleet-table model-fleet-table local-model-table" aria-label="Models running on this machine">
        <thead><tr>
          <th><SortButton column="model_name" label="Model" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="backend" label="Backend" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="rss_bytes" label="Memory" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="cpu_percent" label="CPU" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="started_at" label="Up for" sort={sort} onSortChange={onSortChange} /></th>
          <th className="model-fleet-table__actions"><span className="sr-only">Actions</span></th>
        </tr></thead>
        <tbody>{models.map(model => {
          const up = uptime(model.started_at, now)
          const stopping = stoppingName === model.model_name
          return (
            <tr key={model.model_name} className={`fleet-table__row${stopping ? ' is-stopping' : ''}`} data-testid="local-model-row">
              <td>
                <span className="fleet-table__node">{model.model_name}</span>
                {model.pid != null && <span className="fleet-table__subvalue">PID {model.pid}</span>}
              </td>
              <td><div className="model-backend-list">{model.backend ? <span>{model.backend}</span> : <span className="fleet-table__unknown">Unknown</span>}</div></td>
              <td>
                <UsageCell percent={model.memory_percent} tone="ram"
                  label={model.rss_bytes != null ? formatBytes(model.rss_bytes) : null}
                  title={model.rss_bytes != null ? `${formatBytes(model.rss_bytes)} resident, ${model.memory_percent?.toFixed(1)}% of host RAM` : 'Memory not reported'} />
              </td>
              <td>
                {/* CPU is a delta between two server readings, so a process seen
                    for the first time has none yet; that is not "no data". */}
                <UsageCell percent={model.cpu_percent} tone="cpu"
                  label={model.cpu_percent != null ? `${model.cpu_percent.toFixed(1)}%` : null}
                  emptyLabel={model.pid != null ? 'Measuring…' : 'No data'}
                  title={model.cpu_percent != null ? `${model.cpu_percent.toFixed(1)}% of host CPU` : model.pid != null ? 'CPU not measured yet' : 'CPU not reported'} />
              </td>
              <td>{up ?? <span className="fleet-table__unknown">Unknown</span>}</td>
              <td className="model-fleet-table__actions">
                <ActionMenu
                  compact
                  ariaLabel={`${model.model_name} actions`}
                  triggerLabel={`Actions for ${model.model_name}`}
                  items={[{
                    key: 'logs',
                    icon: 'fa-terminal',
                    label: 'View logs',
                    onClick: () => onViewLogs(model),
                  }, {
                    divider: true,
                  }, {
                    key: 'stop',
                    icon: 'fa-stop',
                    label: stopping ? 'Stopping…' : 'Stop model…',
                    danger: true,
                    disabled: !!stoppingName,
                    onClick: invoker => onStop(model, invoker),
                  }]}
                />
              </td>
            </tr>
          )
        })}</tbody>
      </table>
    </div>
  )
}
