/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { groupNodes, nodeLifecycleAction } from '../../utils/nodeFleet'
import { isDown, memoryOf, nodeState } from '../../utils/swarm'
import { isSplit, nodeConnections, splitConnections } from '../../utils/workerRoster'
import { formatBytes, timeAgo } from '../nodes/nodeStatus'
import { cssVars } from '../../utils/modelLedger'
import { Button, StatusChip } from '../ui'
import ConnectionChips from './ConnectionChips'

const TONE = { ok: 'ok', warn: 'warn', error: 'error', idle: 'idle' }

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
  if (down) return <span className="wk-row__none">{t('row.noHeartbeat')}</span>
  const memory = memoryOf(node)
  if (!memory) return <span className="wk-row__none">{t('table.noReading')}</span>
  const percent = Math.round(memory.usagePercent)
  const level = percent >= 90 ? ' dk-meter-seg--error' : percent >= 75 ? ' dk-meter-seg--warn' : ''
  return (
    <>
      <span className="wk-row__fig">{t(memory.kind === 'gpu' ? 'row.vram' : 'row.ram', { percent })}</span>
      <span className="dk-meter wk-row__bar" role="img" aria-label={t('table.memoryAria', { used: formatBytes(memory.used), total: formatBytes(memory.total), percent })}>
        <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${percent}%` })} />
      </span>
    </>
  )
}

// The roster: every machine of the cluster in one list, whatever way it
// connects. A row says who the worker is, its state, the connections it holds
// (each with its own state), how full its memory is and when it last answered.
// A model-split worker is a row too, marked as such, with its one P2P
// connection. Choosing a row opens it in the inspector; the checkbox is for
// bulk actions on nodes. The page owns selection, sorting and what the
// inspector shows.
export default function WorkerRoster({
  rows, reasons, selectedIds, onSelectionChange, groupBy, onApprove, busyId, activeId, onInspect, modelsByNode,
}) {
  const { t } = useTranslation('swarm')
  const { t: tc } = useTranslation('cluster')
  const nodeRows = rows.filter(row => !isSplit(row))
  const splitRows = rows.filter(isSplit)
  const groups = groupNodes(nodeRows, groupBy)
  if (splitRows.length > 0) {
    // With no grouping the model-split workers follow the nodes in the one list;
    // with a grouping they are a group of their own.
    if (groupBy === 'none' || !groupBy) groups[0].nodes = [...groups[0].nodes, ...splitRows]
    else groups.push({ key: 'model-split', label: tc('roster.splitGroup'), nodes: splitRows })
  }
  const selectable = nodeRows.map(node => node.id)
  const selectedVisible = selectable.filter(id => selectedIds.has(id)).length
  const setMany = (ids, selected) => {
    const next = new Set(selectedIds)
    ids.forEach(id => (selected ? next.add(id) : next.delete(id)))
    onSelectionChange(next)
  }

  const nodeRow = node => {
    const state = nodeState(node)
    const down = isDown(node)
    const why = reasons.get(node.id) || []
    const models = modelsByNode ? modelsByNode.get(node.id) || [] : null
    const sub = describe(node, models, t)
    const active = activeId === node.id
    return (
      <li
        key={node.id} className="wk-row" data-row data-entity={node.name}
        data-selected={selectedIds.has(node.id) ? 'true' : undefined} data-active={active ? 'true' : undefined} data-down={down ? '' : undefined}
      >
        <input
          className="dk-check wk-row__check" type="checkbox" aria-label={t('table.select', { name: node.name })}
          checked={selectedIds.has(node.id)} onChange={event => setMany([node.id], event.target.checked)}
        />
        <button type="button" className="wk-row__main" aria-current={active ? 'true' : undefined} onClick={() => onInspect(node.id)}>
          <b className="dk-mono wk-row__name">{node.name}</b>
          <span className="wk-row__sub" title={sub}>{sub}</span>
        </button>
        <div className="wk-row__state">
          <StatusChip tone={TONE[state.level]} dot title={t(`state.${state.key}`)}>{t(`state.${state.key}`)}</StatusChip>
          {why.filter(reason => reason.startsWith('low')).map(reason => (
            <StatusChip key={reason} tone="warn">{t(`attention.${reason}`)}</StatusChip>
          ))}
        </div>
        <div className="wk-row__conns"><ConnectionChips connections={nodeConnections(node)} label={tc('conn.of', { name: node.name })} /></div>
        <div className="wk-row__use"><Usage node={node} down={down} t={t} /></div>
        <div className="wk-row__seen" data-late={down ? '' : undefined}>
          {nodeLifecycleAction(node.status) === 'approve'
            ? (
              <Button size="sm" aria-label={t('table.approve', { name: node.name })} disabled={busyId === node.id} onClick={() => onApprove(node.id)}>{t('actions.approve')}</Button>
            )
            : node.last_heartbeat ? timeAgo(node.last_heartbeat) : '—'}
        </div>
      </li>
    )
  }

  const splitRow = row => {
    const worker = row._split
    const state = row._splitState
    const active = activeId === row.id
    const sub = [
      t(`split.engine.${worker.engine === 'mlx' ? 'mlx' : 'llama'}`),
      worker.memory_bytes > 0 ? formatBytes(worker.memory_bytes) : null,
      row.attached_to ? tc('roster.attachedTo', { node: row.attached_to }) : null,
    ].filter(Boolean).join(' · ')
    return (
      <li
        key={row.id} className="wk-row" data-row data-split data-entity={row.name} data-testid={`split-worker-${worker.id}`}
        data-active={active ? 'true' : undefined} data-down={state.key === 'offline' ? '' : undefined}
      >
        <span className="wk-row__check" aria-hidden="true" />
        <button type="button" className="wk-row__main" aria-current={active ? 'true' : undefined} onClick={() => onInspect(row.id)}>
          <span className="wk-row__title">
            <b className="dk-mono wk-row__name">{row.name}</b>
            <StatusChip tone="accent" testId="split-mark">{tc('roster.splitMark')}</StatusChip>
          </span>
          <span className="wk-row__sub" title={sub}>{sub}</span>
        </button>
        <div className="wk-row__state"><StatusChip tone={state.tone} dot>{t(`split.state.${state.key}`)}</StatusChip></div>
        <div className="wk-row__conns"><ConnectionChips connections={splitConnections(worker)} label={tc('conn.of', { name: row.name })} /></div>
        <div className="wk-row__use">
          <span className="wk-row__none">{worker.memory_bytes > 0 ? tc('roster.lends', { memory: formatBytes(worker.memory_bytes) }) : t('table.noReading')}</span>
        </div>
        <div className="wk-row__seen">—</div>
      </li>
    )
  }

  return (
    <div className="wk-list" data-testid="nodes-table" role="group" aria-label={t('table.caption')}>
      <div className="wk-list__head">
        <label>
          <input
            className="dk-check" type="checkbox" aria-label={t('table.selectPage')} title={t('table.selectPageTitle')}
            checked={selectable.length > 0 && selectedVisible === selectable.length}
            ref={input => { if (input) input.indeterminate = selectedVisible > 0 && selectedVisible < selectable.length }}
            onChange={event => setMany(selectable, event.target.checked)}
          />
          <span className="wk-head__all">{t('row.selectAll')}</span>
        </label>
        <span className="wk-head__main">{tc('roster.worker')}</span>
        <span className="wk-head__state">{tc('roster.state')}</span>
        <span className="wk-head__conns">{tc('roster.connections')}</span>
        <span className="wk-head__use">{tc('roster.load')}</span>
        <span className="wk-head__seen">{tc('roster.seen')}</span>
      </div>
      {rows.length === 0 && <p className="dk-empty-text wk-list__empty">{t('table.noMatch')}</p>}
      {groups.filter(group => group.nodes.length > 0).map(group => {
        const groupIds = group.nodes.filter(node => !isSplit(node)).map(node => node.id)
        const groupSelected = groupIds.filter(id => selectedIds.has(id)).length
        const attention = group.nodes.filter(node => reasons.has(node.id)).length
        return (
          <section key={group.key} className="wk-group" aria-label={groupBy === 'none' ? undefined : group.label}>
            {groupBy !== 'none' && (
              <div className="wk-group__head">
                <label>
                  <input
                    className="dk-check" type="checkbox" aria-label={t('table.selectGroup', { name: group.label })} disabled={groupIds.length === 0}
                    checked={groupIds.length > 0 && groupSelected === groupIds.length}
                    ref={input => { if (input) input.indeterminate = groupSelected > 0 && groupSelected < groupIds.length }}
                    onChange={event => setMany(groupIds, event.target.checked)}
                  />
                  <h3>{group.label}</h3>
                </label>
                <span className="wk-group__meta" data-warn={attention ? '' : undefined}>
                  {t('row.groupNodes', { count: group.nodes.length })}
                  {attention > 0 && ` · ${t('row.groupAttention', { count: attention })}`}
                  {groupSelected > 0 && ` · ${t('row.groupSelected', { count: groupSelected })}`}
                </span>
                <span className="wk-group__rule" aria-hidden="true" />
              </div>
            )}
            <ul className="wk-list__rows">{group.nodes.map(row => (isSplit(row) ? splitRow(row) : nodeRow(row)))}</ul>
          </section>
        )
      })}
    </div>
  )
}
