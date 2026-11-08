/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, SkeletonRows, StatusChip } from '../components/ui'
import PageHeader from '../components/PageHeader'
import ResponsiveTable from '../components/ResponsiveTable'
import NodeUpdateDialog from '../components/updates/NodeUpdateDialog'
import { useUpdates } from '../contexts/UpdatesContext'
import { useOperations } from '../hooks/useOperations'
import { nodeView } from '../utils/nodeUpdates'
import '../components/updates/updates.css'
import '../components/updates/node-updates.css'

// One backend across the nodes of a cluster: which node has which version,
// which models run on it, and an update per node or for every node that needs
// it. Every update asks first (NodeUpdateDialog): a worker stops the running
// processes of the backend before it installs the new one.
export default function UpdateBackend() {
  const updates = useUpdates()
  if (!updates || !updates.enabled) return <Unavailable />
  return <Page updates={updates} />
}

function Unavailable() {
  const { t } = useTranslation('updates')
  return (
    <div className="page page--wide op-page nu-page" data-testid="update-backend-page">
      <PageHeader title={t('title')} supporting={t('supporting')} />
      <EmptyState variant="unavailable" title={t('adminOnly.title')} body={t('adminOnly.body')} />
    </div>
  )
}

// The dialog sits beside the body, not inside it: when the last node is
// updated the backend leaves the list and the body changes shape, and the
// dialog must keep its run and its results.
function Page({ updates }) {
  const [plan, setPlan] = useState(null)
  return (
    <>
      <Body updates={updates} onPlan={setPlan} />
      {plan && <NodeUpdateDialog entries={plan} onClose={() => setPlan(null)} />}
    </>
  )
}

function Body({ updates, onPlan }) {
  const { t } = useTranslation('updates')
  const { name = '' } = useParams()
  const { operations } = useOperations()
  const item = useMemo(
    () => (updates.snapshot && Array.isArray(updates.snapshot.items) ? updates.snapshot.items.find(i => i.kind === 'gallery' && i.name === name) : null),
    [updates.snapshot, name],
  )
  const view = useMemo(() => nodeView(item), [item])

  const crumbs = (
    <nav className="nu-crumbs" aria-label={t('node.breadcrumb')}>
      <Link to="/app/updates">{t('title')}</Link>
      <span aria-hidden="true">/</span>
      <span aria-current="page">{name}</span>
    </nav>
  )

  if (updates.loading) {
    return <div className="page page--wide op-page nu-page" data-testid="update-backend-page">{crumbs}<SkeletonRows count={3} label={t('loading')} /></div>
  }
  if (updates.loadError && !item) {
    return (
      <div className="page page--wide op-page nu-page" data-testid="update-backend-page">
        {crumbs}
        <EmptyState variant="unavailable" title={t('loadError.title')} actions={<Button onClick={() => updates.refresh()}>{t('node.reload')}</Button>} />
      </div>
    )
  }
  if (!item || view.nodes.length === 0) {
    return (
      <div className="page page--wide op-page nu-page" data-testid="update-backend-page">
        {crumbs}
        <EmptyState
          title={t('node.noneTitle', { name })}
          body={t('node.noneBody')}
          actions={<Button to="/app/updates">{t('node.back')}</Button>}
        />
      </div>
    )
  }

  const to = view.nodes[0] ? view.nodes[0].to : ''
  const supporting = item.state === 'drift'
    ? t('node.supportingDrift', { nodes: t('node.nodes', { count: view.nodes.length }), count: view.differs })
    : t('node.supporting', { version: to || '?', nodes: t('node.nodes', { count: view.nodes.length }), count: view.behind + view.differs })
  const opOf = (e) => operations.find(o => o.nodeID === e.nodeId && (o.name === e.backend || o.id === e.backend)) || null
  const busy = view.nodes.some(e => { const o = opOf(e); return o && !o.error })

  const stateOf = (e) => {
    const op = opOf(e)
    if (op && !op.error) return { chip: <StatusChip status="updating" testId="node-state" />, text: op.message || '' }
    if (op && op.error) return { chip: <StatusChip status="failed" testId="node-state" />, text: t('row.failedWhy', { message: op.error }), failed: true }
    switch (e.state) {
      case 'current': return { chip: <StatusChip tone="ok" icon="check" testId="node-state">{t('node.state.current')}</StatusChip> }
      case 'differs': return { chip: <StatusChip status="differs" testId="node-state" /> }
      case 'unreachable': return { chip: <StatusChip tone="idle" icon="circle" testId="node-state">{t('node.state.unreachable')}</StatusChip> }
      default: return { chip: <StatusChip status="update-ready" testId="node-state" /> }
    }
  }

  return (
    <div className="page page--wide op-page nu-page" data-testid="update-backend-page">
      {crumbs}
      <PageHeader
        title={name}
        supporting={supporting}
        actions={view.todo.length > 0 ? (
          <Button variant="primary" icon="arrow-up" disabled={busy} onClick={() => onPlan(view.todo)} data-testid="update-nodes">
            {t('node.updateN', { count: view.todo.length })}
          </Button>
        ) : null}
      />
      <Callout tone="warn" testId="node-warning">{t('node.warning')}</Callout>
      {item.state === 'drift' && <Callout tone="info" testId="node-drift-note">{t('node.driftNote')}</Callout>}
      <ResponsiveTable>
        <caption className="dk-sr-only">{t('node.tableLabel', { name })}</caption>
        <thead>
          <tr>
            <th scope="col">{t('node.col.node')}</th>
            <th scope="col">{t('node.col.installed')}</th>
            <th scope="col">{t('node.col.models')}</th>
            <th scope="col">{t('node.col.state')}</th>
            <th scope="col"><span className="dk-sr-only">{t('node.col.action')}</span></th>
          </tr>
        </thead>
        <tbody>
          {view.nodes.map((e) => {
            const st = stateOf(e)
            const canUpdate = (e.state === 'behind' || e.state === 'differs')
            return (
              <tr key={e.nodeId} data-testid="node-row" data-node={e.nodeId} data-state={st.failed ? 'failed' : e.state}>
                <td><strong>{e.nodeName}</strong></td>
                <td className="nu-mono">{e.from || '?'}</td>
                <td>
                  <span className="nu-models" data-known={e.models.length > 0 ? 'true' : 'false'}>
                    {e.models.length > 0 ? e.models.join(', ') : t('node.noneRecorded')}
                  </span>
                </td>
                <td>
                  {st.chip}
                  {st.text && <span className="nu-why">{st.text}</span>}
                </td>
                <td className={canUpdate ? undefined : 'nu-act--none'}>
                  <div className="nu-cell-acts">
                    {canUpdate && (
                      <Button size="sm" disabled={busy} onClick={() => onPlan([e])} aria-label={t(st.failed ? 'row.retryNamed' : 'node.updateNamed', { name: e.nodeName })} data-testid="node-update">
                        {st.failed ? t('row.retry') : t('row.update')}
                      </Button>
                    )}
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </ResponsiveTable>
    </div>
  )
}
