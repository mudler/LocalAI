/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { groupNodes, nodeLifecycleAction } from '../../utils/nodeFleet'
import { isDown } from '../../utils/swarm'
import { timeAgo } from '../nodes/nodeStatus'
import MemoryMeter from './MemoryMeter'
import NodeState from './NodeState'
import Icon from '../Icon'

function SortHead({ col, label, sort, onSort, className }) {
  const active = sort.key === col
  return (
    <th scope="col" className={className} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => onSort({ key: col, direction: active && sort.direction === 'asc' ? 'desc' : 'asc' })}>
        {label}
        <Icon name="arrow-up" className="dk-icon" />
      </button>
    </th>
  )
}

// The sortable table of nodes: one row each, the state in words, memory as a
// bar, and the node's page one click away. Selection and the density switch
// belong to the page; the table only draws them.
export default function NodesTable({
  nodes, reasons, selectedIds, onSelectionChange, sort, onSortChange, groupBy, onApprove, compact, busyId,
}) {
  const { t } = useTranslation('swarm')
  const navigate = useNavigate()
  const groups = groupNodes(nodes, groupBy)
  const visibleIds = nodes.map(node => node.id)
  const selectedVisible = visibleIds.filter(id => selectedIds.has(id)).length
  const setMany = (ids, selected) => {
    const next = new Set(selectedIds)
    ids.forEach(id => (selected ? next.add(id) : next.delete(id)))
    onSelectionChange(next)
  }
  const cols = 9

  const open = (event, node) => {
    if (event.target.closest('a, button, input, label')) return
    navigate(`/app/nodes/${encodeURIComponent(node.id)}`)
  }

  return (
    <div className="dk-table-wrap sw-wrap" data-testid="nodes-table">
      <table className={`dk-table sw-table sw-table--nodes${compact ? ' dk-table--compact' : ''}`}>
        <caption className="dk-sr-only">{t('table.caption')}</caption>
        <thead>
          <tr>
            <th scope="col" className="dk-table-select">
              <input
                className="dk-check"
                type="checkbox"
                aria-label={t('table.selectPage')}
                title={t('table.selectPageTitle')}
                checked={visibleIds.length > 0 && selectedVisible === visibleIds.length}
                ref={input => { if (input) input.indeterminate = selectedVisible > 0 && selectedVisible < visibleIds.length }}
                onChange={event => setMany(visibleIds, event.target.checked)}
              />
            </th>
            <SortHead col="name" label={t('table.node')} sort={sort} onSort={onSortChange} />
            <SortHead col="node_type" label={t('table.role')} sort={sort} onSort={onSortChange} className="dk-hide-phone" />
            <SortHead col="status" label={t('table.state')} sort={sort} onSort={onSortChange} />
            <SortHead col="_memory" label={t('table.memory')} sort={sort} onSort={onSortChange} className="dk-hide-phone" />
            <SortHead col="model_count" label={t('table.models')} sort={sort} onSort={onSortChange} className="dk-hide-phone dk-num" />
            <SortHead col="_seen" label={t('table.lastSeen')} sort={sort} onSort={onSortChange} className="dk-hide-phone" />
            <SortHead col="version" label={t('table.version')} sort={sort} onSort={onSortChange} className="dk-hide-phone" />
            <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('table.open')}</span></th>
          </tr>
        </thead>
        <tbody>
          {nodes.length === 0 && (
            <tr className="dk-table-empty"><td colSpan={cols}><div className="dk-empty"><p className="dk-empty-text">{t('table.noMatch')}</p></div></td></tr>
          )}
          {groups.flatMap(group => {
            const groupIds = group.nodes.map(node => node.id)
            const groupSelected = groupIds.filter(id => selectedIds.has(id)).length
            const rows = group.nodes.map(node => {
              const selected = selectedIds.has(node.id)
              const down = isDown(node)
              const why = reasons.get(node.id) || []
              return (
                <tr
                  key={node.id}
                  data-row
                  data-clickable
                  data-entity={node.name}
                  data-selected={selected ? 'true' : undefined}
                  data-down={down ? '' : undefined}
                  onClick={event => open(event, node)}
                >
                  <td className="dk-table-select">
                    <input
                      className="dk-check"
                      type="checkbox"
                      aria-label={t('table.select', { name: node.name })}
                      checked={selected}
                      onChange={event => setMany([node.id], event.target.checked)}
                    />
                  </td>
                  <td>
                    <Link className="dk-table-name dk-mono sw-name" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{node.name}</Link>
                    <span className="dk-table-sub dk-mono">{node.address || t('table.noAddress')}</span>
                  </td>
                  <td className="dk-hide-phone"><span className="dk-chip dk-chip--sm sw-role">{node.node_type || 'backend'}</span></td>
                  <td>
                    <NodeState node={node} />
                    {nodeLifecycleAction(node.status) === 'approve' && (
                      <button
                        type="button"
                        className="dk-btn dk-btn--secondary dk-btn--sm sw-approve"
                        aria-label={t('table.approve', { name: node.name })}
                        disabled={busyId === node.id}
                        onClick={() => onApprove(node.id)}
                      >
                        {t('actions.approve')}
                      </button>
                    )}
                    {why.filter(r => r.startsWith('low')).map(reason => (
                      <span key={reason} className="sw-reason">{t(`attention.${reason}`)}</span>
                    ))}
                  </td>
                  <td className="dk-hide-phone"><MemoryMeter node={node} /></td>
                  <td className="dk-hide-phone dk-num dk-mono">{node.node_type === 'agent' ? '—' : (node.model_count ?? 0)}</td>
                  <td className="dk-hide-phone dk-mono sw-seen" data-late={down ? '' : undefined}>{node.last_heartbeat ? timeAgo(node.last_heartbeat) : '—'}</td>
                  <td className="dk-hide-phone dk-mono">{node.version || '—'}</td>
                  <td className="dk-table-actions"><Icon name="chevron-right" className="sw-chev" /></td>
                </tr>
              )
            })
            if (groupBy === 'none') return rows
            return [
              <tr key={`group:${group.key}`} className="sw-group">
                <th colSpan={cols} scope="colgroup">
                  <label>
                    <input
                      className="dk-check"
                      type="checkbox"
                      aria-label={t('table.selectGroup', { name: group.label })}
                      checked={groupIds.length > 0 && groupSelected === groupIds.length}
                      ref={input => { if (input) input.indeterminate = groupSelected > 0 && groupSelected < groupIds.length }}
                      onChange={event => setMany(groupIds, event.target.checked)}
                    />
                    {group.label}
                  </label>
                  <span className="sw-group__count">{t('table.groupCount', { count: group.nodes.length, selected: groupSelected })}</span>
                </th>
              </tr>,
              ...rows,
            ]
          })}
        </tbody>
      </table>
    </div>
  )
}
