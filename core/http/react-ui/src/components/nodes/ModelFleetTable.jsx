import { timeAgo } from './nodeStatus'
import ActionMenu from '../ActionMenu'

function SortButton({ column, label, sort, onSortChange }) {
  const active = sort.key === column
  const nextDirection = active && sort.direction === 'asc' ? 'desc' : 'asc'
  return (
    <button type="button" className="fleet-table__sort" onClick={() => onSortChange({ key: column, direction: nextDirection })}
      aria-label={`Sort by ${label.toLowerCase()}${active ? `, ${sort.direction}ending` : ''}`}>
      {label} {active && <i className={`fas fa-arrow-${sort.direction === 'asc' ? 'up' : 'down'}`} aria-hidden="true" />}
    </button>
  )
}

export default function ModelFleetTable({ models, selectedName, inspectorOpen, onInspect, onViewLogs, onStop, stoppingName, sort, onSortChange }) {
  return (
    <div className="fleet-table-wrap model-fleet-table-wrap">
      <table className="fleet-table model-fleet-table" aria-label="Running models">
        <thead><tr>
          <th><SortButton column="model_name" label="Model" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="replica_count" label="Replicas" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="node_count" label="Nodes" sort={sort} onSortChange={onSortChange} /></th>
          <th><SortButton column="in_flight" label="In flight" sort={sort} onSortChange={onSortChange} /></th>
          <th>Backends</th>
          <th><SortButton column="last_used" label="Last used" sort={sort} onSortChange={onSortChange} /></th>
          <th className="model-fleet-table__actions"><span className="sr-only">Actions</span></th>
        </tr></thead>
        <tbody>{models.map(model => {
          const selected = selectedName === model.model_name
          const expanded = selected && inspectorOpen
          return (
          <tr key={model.model_name} className={`fleet-table__row${selected ? ' is-selected' : ''}`}>
            <td><button type="button" className="fleet-table__node" aria-label={`Inspect ${model.model_name}`}
              aria-pressed={selected} aria-expanded={expanded} aria-current={selected ? 'true' : undefined}
              aria-controls={expanded ? 'model-inspector' : undefined}
              onClick={event => onInspect(model, event.currentTarget)}>{model.model_name}</button></td>
            <td>{model.replica_count}</td>
            <td>{model.node_count}</td>
            <td>{model.in_flight}</td>
            <td><div className="model-backend-list">{model.backend_types.length ? model.backend_types.map(backend => <span key={backend}>{backend}</span>) : <span className="fleet-table__unknown">Unknown</span>}</div></td>
            <td>{model.last_used ? timeAgo(model.last_used) : <span className="fleet-table__unknown">Never</span>}</td>
            <td className="model-fleet-table__actions">
              <ActionMenu
                compact
                ariaLabel={`${model.model_name} actions`}
                triggerLabel={`Actions for ${model.model_name}`}
                items={[{
                  key: 'logs',
                  icon: 'fa-terminal',
                  label: 'View logs…',
                  onClick: invoker => onViewLogs(model, invoker),
                }, {
                  divider: true,
                }, {
                  key: 'stop',
                  icon: 'fa-stop',
                  label: stoppingName === model.model_name ? 'Stopping…' : 'Stop model…',
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
