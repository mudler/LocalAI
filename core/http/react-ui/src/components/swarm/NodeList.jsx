/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { groupNodes, nodeLifecycleAction } from '../../utils/nodeFleet'
import { isDown, memoryOf, nodeState } from '../../utils/swarm'
import { formatBytes, timeAgo } from '../nodes/nodeStatus'
import { cssVars } from '../../utils/modelLedger'
import NodeSpark from './NodeSpark'

function describe(node, models, t) {
  const gpu = node.node_type === 'agent'
    ? t('row.agent')
    : node.total_vram > 0
      ? [node.gpu_vendor, formatBytes(node.total_vram)].filter(Boolean).join(' ')
      : t('row.cpuNode', { ram: node.total_ram > 0 ? formatBytes(node.total_ram) : '?' })
  const loaded = node.node_type === 'agent' || !models ? null : models.length ? models.join(', ') : t('row.idle')
  return [gpu, loaded].filter(Boolean).join(' · ')
}

function Usage({ node, down, t }) {
  if (down) return <span className="sw-nrow__none">{t('row.noHeartbeat')}</span>
  const memory = memoryOf(node)
  if (!memory) return <span className="sw-nrow__none">{t('table.noReading')}</span>
  const percent = Math.round(memory.usagePercent)
  const level = percent >= 90 ? ' dk-meter-seg--error' : percent >= 75 ? ' dk-meter-seg--warn' : ''
  return (
    <>
      <span className="sw-nrow__fig">{t(memory.kind === 'gpu' ? 'row.vram' : 'row.ram', { percent })}</span>
      <span
        className="dk-meter sw-nrow__bar"
        role="img"
        aria-label={t('table.memoryAria', { used: formatBytes(memory.used), total: formatBytes(memory.total), percent })}
      >
        <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${percent}%` })} />
      </span>
    </>
  )
}

// The roster: one row per node under thin group headers. A row says who the
// node is, what it runs, how full its memory is, its recent load and when it
// last answered. Choosing a row opens it in the inspector; the checkbox is for
// bulk actions. The page owns selection, sorting and what the inspector shows.
export default function NodeList({
  nodes, reasons, selectedIds, onSelectionChange, groupBy, onApprove, busyId, activeId, onInspect, modelsByNode,
}) {
  const { t } = useTranslation('swarm')
  const groups = groupNodes(nodes, groupBy)
  const visibleIds = nodes.map(node => node.id)
  const selectedVisible = visibleIds.filter(id => selectedIds.has(id)).length
  const setMany = (ids, selected) => {
    const next = new Set(selectedIds)
    ids.forEach(id => (selected ? next.add(id) : next.delete(id)))
    onSelectionChange(next)
  }

  const row = node => {
    const state = nodeState(node)
    const down = isDown(node)
    const why = reasons.get(node.id) || []
    const models = modelsByNode ? modelsByNode.get(node.id) || [] : null
    const sub = describe(node, models, t)
    const active = activeId === node.id
    return (
      <li
        key={node.id}
        className="sw-nrow"
        data-row
        data-entity={node.name}
        data-selected={selectedIds.has(node.id) ? 'true' : undefined}
        data-active={active ? 'true' : undefined}
        data-down={down ? '' : undefined}
      >
        <input
          className="dk-check sw-nrow__check"
          type="checkbox"
          aria-label={t('table.select', { name: node.name })}
          checked={selectedIds.has(node.id)}
          onChange={event => setMany([node.id], event.target.checked)}
        />
        <span className="sw-dot" data-level={state.level} data-state={state.key} aria-hidden="true" />
        <button type="button" className="sw-nrow__main" aria-current={active ? 'true' : undefined} onClick={() => onInspect(node.id)}>
          <span className="sw-nrow__name">
            <b className="dk-mono">{node.name}</b>
            <span className={state.key === 'healthy' ? 'dk-sr-only' : 'sw-nrow__state'} data-level={state.level}>{t(`state.${state.key}`)}</span>
            {why.filter(reason => reason.startsWith('low')).map(reason => (
              <span key={reason} className="sw-nrow__state" data-level="warn">{t(`attention.${reason}`)}</span>
            ))}
          </span>
          <span className="sw-nrow__sub" title={sub}>{sub}</span>
        </button>
        <div className="sw-nrow__use"><Usage node={node} down={down} t={t} /></div>
        <div className="sw-nrow__spark">{!down && <NodeSpark node={node} />}</div>
        <div className="sw-nrow__beat" data-late={down ? '' : undefined}>
          {nodeLifecycleAction(node.status) === 'approve'
            ? (
              <button
                type="button"
                className="dk-btn dk-btn--secondary dk-btn--sm"
                aria-label={t('table.approve', { name: node.name })}
                disabled={busyId === node.id}
                onClick={() => onApprove(node.id)}
              >
                {t('actions.approve')}
              </button>
            )
            : node.last_heartbeat ? timeAgo(node.last_heartbeat) : '—'}
        </div>
      </li>
    )
  }

  return (
    <div className="sw-nlist" data-testid="nodes-table" role="group" aria-label={t('table.caption')}>
      <div className="sw-nlist__all">
        <label>
          <input
            className="dk-check"
            type="checkbox"
            aria-label={t('table.selectPage')}
            title={t('table.selectPageTitle')}
            checked={visibleIds.length > 0 && selectedVisible === visibleIds.length}
            ref={input => { if (input) input.indeterminate = selectedVisible > 0 && selectedVisible < visibleIds.length }}
            onChange={event => setMany(visibleIds, event.target.checked)}
          />
          <span>{t('row.selectAll')}</span>
        </label>
      </div>
      {nodes.length === 0 && <p className="dk-empty-text sw-nlist__empty">{t('table.noMatch')}</p>}
      {groups.map(group => {
        const groupIds = group.nodes.map(node => node.id)
        const groupSelected = groupIds.filter(id => selectedIds.has(id)).length
        const attention = group.nodes.filter(node => reasons.has(node.id)).length
        return (
          <section key={group.key} className="sw-ngroup" aria-label={groupBy === 'none' ? undefined : group.label}>
            {groupBy !== 'none' && (
              <div className="sw-ngroup__head">
                <label>
                  <input
                    className="dk-check"
                    type="checkbox"
                    aria-label={t('table.selectGroup', { name: group.label })}
                    checked={groupIds.length > 0 && groupSelected === groupIds.length}
                    ref={input => { if (input) input.indeterminate = groupSelected > 0 && groupSelected < groupIds.length }}
                    onChange={event => setMany(groupIds, event.target.checked)}
                  />
                  <h3>{group.label}</h3>
                </label>
                <span className="sw-ngroup__meta" data-warn={attention ? '' : undefined}>
                  {t('row.groupNodes', { count: group.nodes.length })}
                  {attention > 0 && ` · ${t('row.groupAttention', { count: attention })}`}
                  {groupSelected > 0 && ` · ${t('row.groupSelected', { count: groupSelected })}`}
                </span>
                <span className="sw-ngroup__rule" aria-hidden="true" />
              </div>
            )}
            <ul className="sw-nlist__rows">{group.nodes.map(row)}</ul>
          </section>
        )
      })}
    </div>
  )
}
