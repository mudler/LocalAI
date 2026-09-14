import StatusPill from './StatusPill'
import { formatBytes, timeAgo } from './nodeStatus'
import { groupNodes } from '../../utils/nodeFleet'

function MetricCell({ total, available, tone }) {
  if (!(total > 0)) return <span className="fleet-table__unknown">No data</span>
  const used = Math.max(0, Math.min(total, total - (Number.isFinite(available) ? available : 0)))
  const percent = Math.round(used / total * 100)
  return (
    <div className={`fleet-table__resource fleet-table__resource--${tone}`} aria-label={`${formatBytes(used)} of ${formatBytes(total)} used`}>
      <progress className="fleet-table__resource-track" max="100" value={percent} aria-hidden="true" />
      <span>{formatBytes(used)} / {formatBytes(total)}</span>
    </div>
  )
}

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

export default function NodeFleetTable({ nodes, selectedIds, onSelectionChange, onInspect, sort, onSortChange, groupBy, onApprove }) {
  const groups = groupNodes(nodes, groupBy)
  const visibleIds = nodes.map(node => node.id)
  const selectedVisible = visibleIds.filter(id => selectedIds.has(id)).length
  const setMany = (ids, selected) => {
    const next = new Set(selectedIds)
    ids.forEach(id => selected ? next.add(id) : next.delete(id))
    onSelectionChange(next)
  }

  return (
    <div className="fleet-table-wrap">
      <table className="fleet-table" aria-label="Fleet nodes">
        <thead>
          <tr>
            <th className="fleet-table__check"><input type="checkbox" aria-label="Select visible nodes" checked={visibleIds.length > 0 && selectedVisible === visibleIds.length}
              ref={input => { if (input) input.indeterminate = selectedVisible > 0 && selectedVisible < visibleIds.length }}
              onChange={event => setMany(visibleIds, event.target.checked)} /></th>
            <th><SortButton column="name" label="Node" sort={sort} onSortChange={onSortChange} /></th>
            <th><SortButton column="status" label="Status" sort={sort} onSortChange={onSortChange} /></th>
            <th><SortButton column="node_type" label="Type" sort={sort} onSortChange={onSortChange} /></th>
            <th>VRAM</th><th>RAM</th><th>CPU</th>
            <th><SortButton column="model_count" label="Models" sort={sort} onSortChange={onSortChange} /></th>
            <th>Heartbeat</th><th><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {groups.flatMap(group => {
            const groupIds = group.nodes.map(node => node.id)
            const groupSelected = groupIds.filter(id => selectedIds.has(id)).length
            const rows = group.nodes.map(node => (
              <tr key={node.id} className={`fleet-table__row${selectedIds.has(node.id) ? ' is-selected' : ''}`} tabIndex="0" onClick={event => onInspect(node, event.currentTarget)}
                onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onInspect(node, event.currentTarget) } }}>
                <td onClick={event => event.stopPropagation()}><input type="checkbox" aria-label={`Select ${node.name}`} checked={selectedIds.has(node.id)}
                  onChange={event => setMany([node.id], event.target.checked)} /></td>
                <td><button type="button" className="fleet-table__node" aria-label={`Inspect ${node.name}`} onClick={event => { event.stopPropagation(); onInspect(node, event.currentTarget) }}>{node.name}</button><span>{node.address || 'No address'}</span></td>
                <td><StatusPill status={node.status} /></td>
                <td>{node.node_type || 'backend'}</td>
                <td><MetricCell total={node.total_vram} available={node.available_vram} tone="vram" /></td>
                <td><MetricCell total={node.total_ram} available={node.available_ram} tone="ram" /></td>
                <td>{node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) ? `${node.cpu_usage_percent.toFixed(0)}% · ${node.cpu_logical_cores}c` : <span className="fleet-table__unknown">No data</span>}</td>
                <td>{node.model_count ?? 0}<span className="fleet-table__subvalue">{node.in_flight_count ?? 0} in flight</span></td>
                <td>{timeAgo(node.last_heartbeat)}</td>
                <td onClick={event => event.stopPropagation()}>{node.status === 'pending' && <button type="button" className="btn btn-primary btn-sm" aria-label={`Approve ${node.name}`} onClick={() => onApprove(node.id)}>Approve</button>}</td>
              </tr>
            ))
            if (groupBy === 'none') return rows
            return [
              <tr key={`group:${group.key}`} className="fleet-table__group">
                <th colSpan="10"><label><input type="checkbox" aria-label={`Select ${group.label} group`} checked={groupIds.length > 0 && groupSelected === groupIds.length}
                  ref={input => { if (input) input.indeterminate = groupSelected > 0 && groupSelected < groupIds.length }}
                  onChange={event => setMany(groupIds, event.target.checked)} /> {group.label}</label><span>{group.nodes.length} nodes · {groupSelected} selected</span></th>
              </tr>,
              ...rows,
            ]
          })}
        </tbody>
      </table>
      {nodes.length === 0 && <div className="fleet-table__empty">No nodes match the current view.</div>}
    </div>
  )
}
