import StatusPill from './StatusPill'
import { formatBytes, timeAgo } from './nodeStatus'
import { capacityReading, groupNodes, nodeLifecycleAction } from '../../utils/nodeFleet'

function MetricCell({ total, available, tone }) {
  const reading = capacityReading(total, available)
  if (!reading) return <span className="fleet-table__unknown">No data</span>
  const percent = Math.round(reading.usagePercent)
  return (
    <div className={`fleet-table__resource fleet-table__resource--${tone}`} aria-label={`${formatBytes(reading.used)} of ${formatBytes(reading.total)} used`}>
      <progress className="fleet-table__resource-track" max="100" value={percent} aria-hidden="true" />
      <span>{formatBytes(reading.used)} / {formatBytes(reading.total)}</span>
    </div>
  )
}

function CapacityCell({ node }) {
  return <div className="fleet-table__capacity">
    <div className="fleet-table__capacity-row"><b>VRAM</b><MetricCell total={node.total_vram} available={node.available_vram} tone="vram" /></div>
    <div className="fleet-table__capacity-row"><b>RAM</b><MetricCell total={node.total_ram} available={node.available_ram} tone="ram" /></div>
  </div>
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
            <th className="fleet-table__check"><input className="fleet-checkbox" type="checkbox" aria-label="Select page" title="Select nodes on this page" checked={visibleIds.length > 0 && selectedVisible === visibleIds.length}
              ref={input => { if (input) input.indeterminate = selectedVisible > 0 && selectedVisible < visibleIds.length }}
              onChange={event => setMany(visibleIds, event.target.checked)} /></th>
            <th><SortButton column="name" label="Node" sort={sort} onSortChange={onSortChange} /></th>
            <th><SortButton column="status" label="Status" sort={sort} onSortChange={onSortChange} /></th>
            <th>Capacity</th><th>CPU</th>
            <th><SortButton column="model_count" label="Workload" sort={sort} onSortChange={onSortChange} /></th>
            <th>Heartbeat</th>
          </tr>
        </thead>
        <tbody>
          {groups.flatMap(group => {
            const groupIds = group.nodes.map(node => node.id)
            const groupSelected = groupIds.filter(id => selectedIds.has(id)).length
            const rows = group.nodes.map(node => (
              <tr key={node.id} className={`fleet-table__row${selectedIds.has(node.id) ? ' is-selected' : ''}`} tabIndex="0" onClick={event => onInspect(node, event.currentTarget)}
                onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onInspect(node, event.currentTarget) } }}>
                <td className="fleet-table__check" onClick={event => event.stopPropagation()}><input className="fleet-checkbox" type="checkbox" aria-label={`Select ${node.name}`} checked={selectedIds.has(node.id)}
                  onChange={event => setMany([node.id], event.target.checked)} /></td>
                <td><button type="button" className="fleet-table__node" aria-label={`Inspect ${node.name}`} onClick={event => { event.stopPropagation(); onInspect(node, event.currentTarget) }}>{node.name}</button><span>{node.node_type || 'backend'} · {node.address || 'No address'}</span></td>
                <td><StatusPill status={node.status} />{nodeLifecycleAction(node.status) === 'approve' && <button type="button" className="fleet-table__approve" aria-label={`Approve ${node.name}`} onClick={event => { event.stopPropagation(); onApprove(node.id) }}>Approve</button>}</td>
                <td><CapacityCell node={node} /></td>
                <td>{node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) ? `${node.cpu_usage_percent.toFixed(0)}% · ${node.cpu_logical_cores}c` : <span className="fleet-table__unknown">No data</span>}</td>
                <td><strong>{node.model_count ?? 0} models</strong><span className="fleet-table__subvalue">{node.in_flight_count ?? 0} in flight</span></td>
                <td>{timeAgo(node.last_heartbeat)}</td>
              </tr>
            ))
            if (groupBy === 'none') return rows
            return [
              <tr key={`group:${group.key}`} className="fleet-table__group">
                <th colSpan="7"><label><input className="fleet-checkbox" type="checkbox" aria-label={`Select ${group.label} group`} checked={groupIds.length > 0 && groupSelected === groupIds.length}
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
