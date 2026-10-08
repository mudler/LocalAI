/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { capacityReading, nodeLifecycleAction } from '../utils/nodeFleet'
import { isDown, leavePreview } from '../utils/swarm'
import { cssVars } from '../utils/modelLedger'
import { usePolling } from '../hooks/usePolling'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import ActionMenu from '../components/ActionMenu'
import CapacityEditor from '../components/nodes/CapacityEditor'
import KeyValueChips from '../components/nodes/KeyValueChips'
import { formatVRAM, timeAgo } from '../components/nodes/nodeStatus'
import NodeState from '../components/swarm/NodeState'
import SwarmDialog from '../components/swarm/SwarmDialog'
import LeaveList from '../components/swarm/LeaveList'
import Icon from '../components/Icon'
import './swarm.css'

const TABS = ['models', 'backends', 'logs', 'config']

function Vital({ label, value, note, reading, danger }) {
  const percent = reading ? Math.round(reading.usagePercent) : 0
  const level = danger || percent >= 90 ? ' dk-meter-seg--error' : percent >= 75 ? ' dk-meter-seg--warn' : ''
  return (
    <div className="sw-vital">
      <span className="sw-vital__label">{label}</span>
      <span className="sw-vital__value dk-mono">{value}</span>
      {note && <span className="sw-vital__note">{note}</span>}
      {reading && (
        <span className="dk-meter sw-vital__bar" role="img" aria-label={`${label}: ${percent}%`}>
          <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${percent}%` })} />
        </span>
      )}
    </div>
  )
}

function stateLevel(state) {
  if (state === 'loaded') return 'ok'
  if (state === 'unloading') return 'warn'
  return 'idle'
}

// One node, as a page: whether it is well, what is on it, and the things you
// can do to it. Draining shows what it would change before it does anything,
// and removing asks for the node's name.
export default function NodeDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const [params, setParams] = useSearchParams()
  const [node, setNode] = useState(null)
  const [models, setModels] = useState([])
  const [backends, setBackends] = useState({ list: [], error: '' })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [dialog, setDialog] = useState(null)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmUnload, setConfirmUnload] = useState(null)
  const [confirmDeleteBackend, setConfirmDeleteBackend] = useState(null)
  // CapacityEditor awaits this: the page owns the dialog so it can phrase the
  // warning with the node's own numbers.
  const [confirmShrinkState, setConfirmShrinkState] = useState(null)
  const [cluster, setCluster] = useState({ nodes: null, rows: null, rules: null })
  const [openLeave, setOpenLeave] = useState(true)

  const tab = TABS.includes(params.get('tab')) ? params.get('tab') : 'models'
  const setTab = value => setParams(current => {
    const next = new URLSearchParams(current)
    if (value === 'models') next.delete('tab')
    else next.set('tab', value)
    return next
  }, { replace: true })

  const refresh = useCallback(async ({ quiet = false } = {}) => {
    if (!quiet) { setLoading(true); setLoadError('') }
    try {
      const n = await nodesApi.get(id)
      const [m, b] = await Promise.allSettled([nodesApi.getModels(id), nodesApi.getBackends(id)])
      setNode(n)
      setLoadError('')
      setModels(m.status === 'fulfilled' && Array.isArray(m.value) ? m.value : [])
      setBackends(b.status === 'fulfilled'
        ? { list: Array.isArray(b.value) ? b.value : [], error: '' }
        : { list: [], error: b.reason?.message || 'failed' })
    } catch (err) {
      if (!quiet) {
        setNode(null)
        setLoadError(err.message || 'Unable to load node')
        addToast(t('toast.loadFailed', { message: err.message }), 'error')
      }
    } finally {
      if (!quiet) setLoading(false)
    }
  }, [id, addToast, t])

  useEffect(() => { refresh() }, [refresh])
  usePolling(() => refresh({ quiet: true }), 10_000, { immediate: false, enabled: !!node })

  // What a drain or a loss would change needs the other nodes, every loaded
  // replica and the rules. They are read once the node is known; if any read
  // fails the section says so rather than guessing.
  const nodeId = node?.id
  useEffect(() => {
    if (!nodeId) return undefined
    let cancelled = false
    ;(async () => {
      const [list, rows, rules] = await Promise.allSettled([nodesApi.list(), nodesApi.allModels(), nodesApi.listScheduling()])
      if (cancelled) return
      setCluster({
        nodes: list.status === 'fulfilled' && Array.isArray(list.value) ? list.value : null,
        rows: rows.status === 'fulfilled' && Array.isArray(rows.value) ? rows.value : null,
        rules: rules.status === 'fulfilled' && Array.isArray(rules.value) ? rules.value : [],
      })
    })()
    return () => { cancelled = true }
  }, [nodeId, models.length])

  const confirmShrink = useCallback(ctx => new Promise(resolve => setConfirmShrinkState({ ...ctx, resolve })), [])

  const preview = useMemo(() => {
    if (!node || !cluster.nodes || !cluster.rows) return null
    return leavePreview({ node, rows: cluster.rows, nodes: cluster.nodes, rules: cluster.rules })
  }, [node, cluster])

  if (loading) return <div className="page page--wide loading-center"><LoadingSpinner size="lg" /></div>
  if (!node && loadError) {
    const notFound = loadError.includes('404') || loadError.toLowerCase().includes('not found')
    return (
      <div className="page page--wide sw-page" data-testid="node-error">
        <Link className="sw-back" to="/app/nodes"><Icon name="arrow-left" /> {t('detail.back')}</Link>
        <header className="sw-head">
          <h1 className="sw-title">{notFound ? t('detail.notFound') : t('detail.loadFailed')}</h1>
          <p className="sw-note">{notFound ? t('detail.notFoundSub') : loadError}</p>
        </header>
        {!notFound && (
          <div className="sw-error" role="alert">
            <Icon name="warning" />
            <span><strong>{t('detail.loadFailed')}</strong> {t('detail.loadFailedSub')}</span>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => void refresh()}>{t('detail.retry')}</button>
          </div>
        )}
      </div>
    )
  }
  if (!node) return <div className="page page--wide"><h1 className="sw-title">{t('detail.notFound')}</h1></div>

  const run = async (action, success) => {
    try {
      await action()
      addToast(success, 'success')
      return true
    } catch (e) {
      addToast(e.message, 'error')
      return false
    }
  }
  const approve = async () => { if (await run(() => nodesApi.approve(id), t('toast.approvedOne'))) refresh({ quiet: true }) }
  const resume = async () => { if (await run(() => nodesApi.resume(id), t('toast.resumed'))) refresh({ quiet: true }) }
  const drain = async () => {
    setBusy(true)
    const ok = await run(() => nodesApi.drain(id), t('toast.draining'))
    setBusy(false)
    setDialog(null)
    if (ok) refresh({ quiet: true })
  }
  const remove = async () => {
    setBusy(true)
    const ok = await run(() => nodesApi.delete(id), t('toast.removed'))
    setBusy(false)
    if (ok) navigate('/app/nodes')
  }
  const unload = async name => { if (await run(() => nodesApi.unloadModel(id, name), t('toast.unloaded', { name }))) refresh({ quiet: true }) }
  // The upgrade runs through the gallery job queue (202 + jobID); the global
  // Operations panel tracks it, so the toast only reports the dispatch.
  const upgradeBackend = async name => {
    try {
      await nodesApi.upgradeBackend(id, name)
      addToast(t('toast.upgrading', { name }), 'info')
      setTimeout(() => refresh({ quiet: true }), 1200)
    } catch (e) { addToast(e.message, 'error') }
  }
  const deleteBackend = async name => { if (await run(() => nodesApi.deleteBackend(id, name), t('toast.backendDeleted', { name }))) refresh({ quiet: true }) }
  const addLabel = async (k, v) => { try { await nodesApi.mergeLabels(id, { [k]: v }); refresh({ quiet: true }) } catch (e) { addToast(e.message, 'error') } }
  const delLabel = async k => { try { await nodesApi.deleteLabel(id, k); refresh({ quiet: true }) } catch (e) { addToast(e.message, 'error') } }

  const vram = capacityReading(node.total_vram, node.available_vram)
  const ram = capacityReading(node.total_ram, node.available_ram)
  const disk = capacityReading(node.total_disk, node.available_disk)
  const cpuPercent = node.cpu_logical_cores > 0 && Number.isFinite(node.cpu_usage_percent) ? node.cpu_usage_percent : null
  const action = nodeLifecycleAction(node.status)
  const down = isDown(node)
  const pending = node.status === 'pending'
  const draining = node.status === 'draining'
  const isAgent = node.node_type === 'agent'
  const loadedCounts = {}
  models.forEach(m => { if (m.state === 'loaded') loadedCounts[m.model_name] = (loadedCounts[m.model_name] || 0) + 1 })
  const replicaCounts = {}
  models.forEach(m => { replicaCounts[m.model_name] = (replicaCounts[m.model_name] || 0) + 1 })
  const logTarget = (m) => `/app/node-backend-logs/${encodeURIComponent(id)}/${encodeURIComponent(`${m.model_name}#${m.replica_index ?? 0}`)}`
  const hasPreview = !isAgent && !pending
  const seen = node.last_heartbeat ? timeAgo(node.last_heartbeat) : t('detail.never')

  const fmt = reading => (reading ? `${formatVRAM(reading.used) || '0'} / ${formatVRAM(reading.total)}` : t('detail.noData'))

  return (
    <div className="page page--wide sw-page" data-testid="node-detail">
      <Link className="sw-back" to="/app/nodes"><Icon name="arrow-left" /> {t('detail.back')}</Link>

      <header className="sw-node-head">
        <div className="sw-node-head__lead">
          <h1 className="sw-title dk-mono">{node.name}</h1>
          <p className="sw-identity">
            <NodeState node={node} />
            <span className="dk-mono">{node.address || node.id}</span>
            <span>{t('detail.kind', { type: node.node_type || 'backend' })}</span>
            {node.version && <span className="dk-mono">{node.version}</span>}
            <span>{t('detail.seen', { when: seen })}</span>
            {node.gpu_vendor && <span className="dk-chip dk-chip--sm">{node.gpu_vendor}</span>}
          </p>
        </div>
        <div className="sw-node-head__acts">
          {action === 'approve' && <button type="button" className="dk-btn dk-btn--primary" onClick={approve}><Icon name="check" /> {t('actions.approve')}</button>}
          {action === 'drain' && <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setDialog('drain')}><Icon name="pause" /> {t('actions.drain')}</button>}
          {action === 'resume' && <button type="button" className="dk-btn dk-btn--secondary" onClick={resume}><Icon name="play" /> {t('actions.resume')}</button>}
          <button type="button" className="dk-btn dk-btn--ghost sw-remove" onClick={() => { setTyped(''); setDialog('remove') }}><Icon name="trash" /> {t('actions.remove')}</button>
        </div>
      </header>

      {down && (
        <section className="sw-banner" data-level="error" role="status" data-testid="down-banner">
          <p><strong>{t('detail.down', { when: seen })}</strong> {t('detail.downSub')}</p>
          <p className="sw-banner__more"><Link className="dk-link" to="/app/failover">{t('detail.seeFailover')}</Link></p>
        </section>
      )}
      {pending && (
        <section className="sw-banner" data-level="warn" role="status" data-testid="pending-banner">
          <p><strong>{t('detail.pending')}</strong> {t('detail.pendingSub')}</p>
        </section>
      )}
      {draining && (
        <section className="sw-banner" data-level="warn" role="status" data-testid="draining-banner">
          <p><strong>{t('detail.draining')}</strong> {t('detail.drainingSub')}</p>
        </section>
      )}

      <section className="sw-vitals" aria-label={t('detail.resources')}>
        <Vital label="VRAM" value={fmt(vram)} reading={vram} />
        <Vital label="RAM" value={fmt(ram)} reading={ram} />
        {/* Free space on the worker's MODELS filesystem. A node can look
            healthy on VRAM while having nowhere to put the weights, which is
            why this sits beside VRAM and not in a diagnostics panel. */}
        <Vital
          label={t('detail.disk')}
          value={disk ? `${formatVRAM(disk.available) || '0'} / ${formatVRAM(disk.total)}` : t('detail.noData')}
          reading={disk}
        />
        {cpuPercent !== null && (
          <Vital
            label="CPU"
            value={`${cpuPercent.toFixed(1)}% of ${node.cpu_logical_cores} cores`}
            note={Number.isFinite(node.cpu_load_1) ? t('detail.load', { load: node.cpu_load_1.toFixed(2) }) : null}
            reading={{ usagePercent: Math.min(100, Math.max(0, cpuPercent)) }}
          />
        )}
        <Vital label={t('detail.inFlight')} value={String(node.in_flight_count || 0)} />
      </section>

      {hasPreview && (
        <section className="sw-disclose" aria-label={t(down ? 'leave.titleLost' : draining ? 'leave.titleDraining' : 'leave.title')}>
          <button type="button" className="sw-disclose__head" aria-expanded={openLeave} onClick={() => setOpenLeave(v => !v)}>
            <span>{t(down ? 'leave.titleLost' : draining ? 'leave.titleDraining' : 'leave.title')}</span>
            <span className="sw-badge-preview">{t('preview.label')}</span>
            <Icon name="chevron-down" className="sw-disclose__chev" />
          </button>
          {openLeave && (
            <div className="sw-disclose__body">
              {preview === null
                ? <p className="sw-note">{t('leave.unavailable')}</p>
                : <LeaveList items={preview} lost={down} />}
              <p className="sw-note sw-note--quiet">{t('leave.how')}</p>
            </div>
          )}
        </section>
      )}

      <div className="dk-tabs sw-tabs" role="tablist" aria-label={t('detail.tabs')}>
        {TABS.filter(key => !(isAgent && key === 'backends')).map(key => (
          <button
            key={key}
            type="button"
            role="tab"
            id={`sw-tab-${key}`}
            aria-controls={`sw-panel-${key}`}
            aria-selected={tab === key}
            tabIndex={tab === key ? 0 : -1}
            className="dk-tab"
            onClick={() => setTab(key)}
            onKeyDown={e => {
              const keys = TABS.filter(k => !(isAgent && k === 'backends'))
              const at = keys.indexOf(key)
              const go = e.key === 'ArrowRight' ? keys[(at + 1) % keys.length] : e.key === 'ArrowLeft' ? keys[(at + keys.length - 1) % keys.length] : null
              if (go) { e.preventDefault(); setTab(go); requestAnimationFrame(() => document.getElementById(`sw-tab-${go}`)?.focus()) }
            }}
          >
            {t(`detail.tab.${key}`)}
            {key === 'models' && <span className="sw-tab__n"> {models.length}</span>}
            {key === 'backends' && !backends.error && <span className="sw-tab__n"> {backends.list.length}</span>}
          </button>
        ))}
      </div>

      {tab === 'models' && (
        <div className="dk-tabpanel" role="tabpanel" id="sw-panel-models" aria-labelledby="sw-tab-models" tabIndex={0}>
          <section aria-label={t('detail.runningModels')} className="sw-section">
            {models.length === 0 ? (
              <p className="sw-empty"><Icon name="cube" /> {t('detail.noModels')}</p>
            ) : (
              <div className="dk-table-wrap sw-wrap">
                <table className="dk-table sw-table">
                  <caption className="dk-sr-only">{t('detail.runningModels')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('detail.model')}</th>
                      <th scope="col">{t('detail.state')}</th>
                      <th scope="col" className="dk-num">{t('detail.inFlight')}</th>
                      <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('detail.actions')}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {models.map(model => {
                      const n = (model.replica_index ?? 0) + 1
                      const showReplica = replicaCounts[model.model_name] > 1
                      const processKey = `${model.model_name}#${model.replica_index ?? 0}`
                      return (
                        <tr key={model.id || processKey} data-row>
                          <td>
                            <span className="dk-table-name dk-mono">{model.model_name}{showReplica && <span className="dk-chip dk-chip--sm sw-rep" aria-label={`replica ${n}`} title={t('detail.replicaTitle', { n })}>rep {n}</span>}</span>
                            <span className="dk-table-sub">{model.backend_type || ' '}</span>
                          </td>
                          <td><span className="sw-state" data-level={stateLevel(model.state)}>{t(`modelState.${model.state}`, model.state)}</span></td>
                          <td className="dk-num dk-mono">{model.in_flight ?? 0}</td>
                          <td className="dk-table-actions">
                            <ActionMenu compact ariaLabel={`${model.model_name} replica ${n} actions`} triggerLabel={`Actions for ${model.model_name} replica ${n}`} items={[
                              { key: 'logs', icon: 'terminal', label: t('detail.viewLogs'), onClick: () => navigate(logTarget(model)) },
                              { divider: true },
                              { key: 'unload', icon: 'stop', label: t('detail.unload'), danger: true, onClick: () => setConfirmUnload({ modelName: model.model_name, inFlight: model.in_flight ?? 0 }) },
                            ]} />
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
            <p className="sw-note sw-note--quiet">{t('detail.placementNote')} <Link className="dk-link" to="/app/scheduling">{t('detail.placementLink')}</Link></p>
          </section>
        </div>
      )}

      {tab === 'backends' && !isAgent && (
        <div className="dk-tabpanel" role="tabpanel" id="sw-panel-backends" aria-labelledby="sw-tab-backends" tabIndex={0}>
          <section aria-label={t('detail.installedBackends')} className="sw-section">
            <div className="sw-section__head">
              <p className="sw-note">{t('detail.backendsSub')}</p>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate(`/app/backends?target=${encodeURIComponent(id)}`)}><Icon name="plus" /> {t('detail.addBackend')}</button>
            </div>
            {backends.error ? (
              <div className="sw-error" role="alert"><Icon name="alert-circle" /><span>{t('detail.backendsFailed')}</span></div>
            ) : backends.list.length === 0 ? (
              <p className="sw-empty">{t('detail.noBackends')} <button type="button" className="dk-link sw-linkbtn" onClick={() => navigate(`/app/backends?target=${encodeURIComponent(id)}`)}>{t('detail.installOne')}</button></p>
            ) : (
              <div className="dk-table-wrap sw-wrap">
                <table className="dk-table sw-table">
                  <caption className="dk-sr-only">{t('detail.installedBackends')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('detail.name')}</th>
                      <th scope="col">{t('detail.source')}</th>
                      <th scope="col" className="dk-hide-phone">{t('detail.installed')}</th>
                      <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('detail.actions')}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {backends.list.map(backend => (
                      <tr key={backend.name} data-row>
                        <td><span className="dk-table-name dk-mono">{backend.name}</span></td>
                        <td><span className="dk-chip dk-chip--sm">{backend.is_system ? t('detail.system') : t('detail.gallery')}</span></td>
                        <td className="dk-hide-phone dk-mono">{backend.installed_at ? timeAgo(backend.installed_at) : '—'}</td>
                        <td className="dk-table-actions">
                          {!backend.is_system && (
                            <ActionMenu compact ariaLabel={`${backend.name} backend actions`} triggerLabel={`Actions for backend ${backend.name}`} items={[
                              { key: 'upgrade', icon: 'arrow-up', label: t('detail.upgradeBackend'), onClick: () => upgradeBackend(backend.name) },
                              { divider: true },
                              { key: 'delete', icon: 'trash', label: t('detail.deleteBackend'), danger: true, onClick: () => setConfirmDeleteBackend({ backend: backend.name }) },
                            ]} />
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      )}

      {tab === 'logs' && (
        <div className="dk-tabpanel" role="tabpanel" id="sw-panel-logs" aria-labelledby="sw-tab-logs" tabIndex={0}>
          <section aria-label={t('detail.tab.logs')} className="sw-section">
            <p className="sw-note">{t('detail.logsSub')}</p>
            {models.length === 0 ? (
              <p className="sw-empty">{t('detail.noLogs')}</p>
            ) : (
              <ul className="sw-loglinks">
                {models.map(model => {
                  const n = (model.replica_index ?? 0) + 1
                  return (
                    <li key={model.id || `${model.model_name}#${model.replica_index}`}>
                      <Link className="dk-link dk-mono" to={logTarget(model)}>
                        {model.model_name}{replicaCounts[model.model_name] > 1 ? ` · ${t('detail.replicaShort', { n })}` : ''}
                      </Link>
                      <span className="sw-count">{t(`modelState.${model.state}`, model.state)}</span>
                    </li>
                  )
                })}
              </ul>
            )}
          </section>
        </div>
      )}

      {tab === 'config' && (
        <div className="dk-tabpanel" role="tabpanel" id="sw-panel-config" aria-labelledby="sw-tab-config" tabIndex={0}>
          <aside className="sw-config" aria-label={t('detail.config')}>
            {!isAgent && (
              <section>
                <h2 className="sw-h2">{t('detail.replicaCap')}</h2>
                <p className="sw-note">{t('detail.replicaCapSub')}</p>
                <CapacityEditor node={node} loadedModelCounts={loadedCounts} confirmShrink={confirmShrink} addToast={addToast} onUpdate={() => refresh({ quiet: true })} />
              </section>
            )}
            <section>
              <h2 className="sw-h2">{t('detail.labels')}</h2>
              <p className="sw-note">{t('detail.labelsSub')}</p>
              <KeyValueChips
                pairs={Object.fromEntries(Object.entries(node.labels || {}).filter(([key]) => key !== 'node.replica-slots'))}
                onAdd={addLabel}
                onRemove={delLabel}
                placeholderKey="key"
                placeholderValue="value"
                ariaLabel="Node labels"
              />
            </section>
          </aside>
        </div>
      )}

      {dialog === 'drain' && (
        <SwarmDialog
          testId="drain-dialog"
          title={t('drain.title', { name: node.name })}
          desc={t('drain.desc')}
          pending={busy}
          onClose={() => setDialog(null)}
          foot={<>
            <button type="button" className="dk-btn dk-btn--secondary" disabled={busy} onClick={() => setDialog(null)}>{t('actions.cancel')}</button>
            <button type="button" className="dk-btn dk-btn--primary sw-dialog__primary" disabled={busy} onClick={drain}>{busy ? t('drain.pending') : t('drain.confirm')}</button>
          </>}
        >
          <p className="sw-badge-preview sw-badge-preview--block">{t('preview.label')}</p>
          {preview === null ? <p className="sw-note">{t('leave.unavailable')}</p> : <LeaveList items={preview} />}
          <p className="sw-note sw-note--quiet">{t('leave.how')}</p>
          <p className="sw-note sw-note--quiet">{t('drain.undo')}</p>
        </SwarmDialog>
      )}

      {dialog === 'remove' && (
        <SwarmDialog
          testId="remove-dialog"
          role="alertdialog"
          title={t('remove.title', { name: node.name })}
          desc={t('remove.desc')}
          pending={busy}
          onClose={() => setDialog(null)}
          foot={<>
            <button type="button" className="dk-btn dk-btn--secondary" disabled={busy} onClick={() => setDialog(null)}>{t('actions.cancel')}</button>
            <button type="button" className="dk-btn dk-btn--danger" disabled={busy || typed !== node.name} onClick={remove}>{busy ? t('remove.pending') : t('remove.confirm')}</button>
          </>}
        >
          {models.length > 0 && <p className="sw-note">{t('remove.impact', { count: models.length })}</p>}
          <div className="dk-field">
            <label className="dk-label" htmlFor="sw-remove-name">{t('remove.type', { name: node.name })}</label>
            <input
              id="sw-remove-name"
              className="dk-input dk-input--mono"
              autoComplete="off"
              value={typed}
              onChange={e => setTyped(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && typed === node.name && !busy) { e.preventDefault(); remove() } }}
            />
          </div>
        </SwarmDialog>
      )}

      <ConfirmDialog
        open={!!confirmUnload}
        title={t('unload.title')}
        message={
          confirmUnload
            ? confirmUnload.inFlight > 0
              ? t('unload.busy', { name: confirmUnload.modelName, count: confirmUnload.inFlight })
              : t('unload.message', { name: confirmUnload.modelName, node: node.name })
            : ''
        }
        confirmLabel={t('unload.confirm')}
        danger={confirmUnload?.inFlight > 0}
        onConfirm={() => { if (confirmUnload) unload(confirmUnload.modelName); setConfirmUnload(null) }}
        onCancel={() => setConfirmUnload(null)}
      />

      <ConfirmDialog
        open={!!confirmDeleteBackend}
        title={t('backendDelete.title')}
        message={confirmDeleteBackend ? t('backendDelete.message', { backend: confirmDeleteBackend.backend, node: node.name }) : ''}
        confirmLabel={t('backendDelete.confirm')}
        danger
        onConfirm={() => { if (confirmDeleteBackend) deleteBackend(confirmDeleteBackend.backend); setConfirmDeleteBackend(null) }}
        onCancel={() => setConfirmDeleteBackend(null)}
      />

      <ConfirmDialog
        open={!!confirmShrinkState}
        title={t('shrink.title')}
        message={confirmShrinkState ? t('shrink.message', { name: node.name, loaded: confirmShrinkState.currentLoaded, value: confirmShrinkState.newValue }) : ''}
        confirmLabel={t('shrink.confirm')}
        onConfirm={() => { confirmShrinkState?.resolve(true); setConfirmShrinkState(null) }}
        onCancel={() => { confirmShrinkState?.resolve(false); setConfirmShrinkState(null) }}
      />
    </div>
  )
}
