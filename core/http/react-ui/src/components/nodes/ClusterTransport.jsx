import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { clusterApi } from '../../utils/api'
import { busLabel, formatCountdown, replicaReady, routeChips, routeLabel, stateLabel } from '../../utils/clusterTransport'
import { Button, StatusChip } from '../ui'
import CarrierWeights from './CarrierWeights'

const POLL_IDLE_MS = 5000
const POLL_BUSY_MS = 2000

// The routes answer 404 on a build without them and 503 on a frontend that is
// not distributed. Either way there is no transport to show.
function isUnavailable(error) {
  return error?.status === 404 || error?.status === 503 || error?.status === 401 || error?.status === 403
}

function stateTone(state) {
  if (state === 'stable') return 'badge-success'
  if (state === 'draining') return 'badge-info'
  return 'badge-warning'
}

function ReplicaList({ replicas, report, label }) {
  if (!replicas || replicas.length === 0) return <p className="text-note">No live frontend replica reported.</p>
  return (
    <ul className="transport-list" aria-label={label}>
      {replicas.map(replica => {
        const ready = replicaReady(replica, report)
        return (
          <li key={replica.id} className="transport-list__row" data-testid="transport-replica">
            <span className={`transport-dot${ready ? ' transport-dot--ok' : ' transport-dot--warn'}`} aria-hidden="true" />
            <span className="transport-list__name">{replica.id}</span>
            <span className="transport-list__meta">{replica.version || 'unknown version'}</span>
            <span className="transport-list__state">{ready ? 'ready' : (replica.ready_reason || 'not ready')}</span>
          </li>
        )
      })}
    </ul>
  )
}

// The routes of one worker and whether each is live, from the route state of
// its node (GET /api/nodes). A worker whose node carries no route state shows
// the routes it reported, as before.
function WorkerRoutes({ worker, node }) {
  const { t } = useTranslation('swarm')
  const chips = routeChips(node?.route_state)
  if (chips.length === 0) {
    return (worker.attached || []).length ? worker.attached.map(routeLabel).join(' + ') : 'not reported'
  }
  return (
    <span className="transport-routes">
      {chips.map(chip => {
        const state = t(`route.state.${chip.state}`)
        return (
          <StatusChip key={chip.key} tone={chip.tone} testId="transport-route" title={chip.reason ? `${state}: ${chip.reason}` : state}>
            {t('route.chip', { route: t(`route.${chip.name}`), state })}
          </StatusChip>
        )
      })}
    </span>
  )
}

function WorkerTable({ workers, nodes }) {
  if (!workers || workers.length === 0) return <p className="text-note">No worker is registered.</p>
  const byId = new Map((nodes || []).map(node => [node.id, node]))
  return (
    <div className="transport-table-wrap">
      <table className="transport-table" aria-label="Workers by route">
        <thead><tr><th>Worker</th><th>Attached over</th></tr></thead>
        <tbody>
          {workers.map(worker => (
            <tr key={worker.id} data-testid="transport-worker">
              <td>{worker.name || worker.id}</td>
              <td><WorkerRoutes worker={worker} node={byId.get(worker.id)} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function NatsSettings({ settings, onSaved }) {
  const [natsUrl, setNatsUrl] = useState(settings?.nats_url || '')
  const [workerUrl, setWorkerUrl] = useState(settings?.nats_worker_url || '')
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState(null)
  const touched = useRef(false)

  // Follow the stored value until the admin starts typing.
  useEffect(() => {
    if (touched.current) return
    setNatsUrl(settings?.nats_url || '')
    setWorkerUrl(settings?.nats_worker_url || '')
  }, [settings?.nats_url, settings?.nats_worker_url])

  const save = async event => {
    event.preventDefault()
    setSaving(true)
    setResult(null)
    try {
      const answer = await clusterApi.saveSettings({ nats_url: natsUrl.trim(), nats_worker_url: workerUrl.trim() })
      touched.current = false
      setResult({
        ok: true,
        text: answer?.nats?.reachable
          ? 'Saved. This frontend reaches the NATS server. The carrier setting did not change; restart the frontends to broadcast over the new address.'
          : 'Saved. The carrier setting did not change.',
      })
      onSaved()
    } catch (error) {
      setResult({ ok: false, text: error.message || 'The settings were not saved' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="transport-settings" onSubmit={save} aria-label="NATS settings">
      <p className="form-label">NATS settings</p>
      <p className="text-note">Saving makes NATS available and checks that this frontend reaches it. It does not change the carrier setting.</p>
      <label className="transport-field">
        <span>NATS URL</span>
        <input className="input input-mono" aria-label="NATS URL" placeholder="nats://nats:4222" value={natsUrl}
          onChange={event => { touched.current = true; setNatsUrl(event.target.value) }} />
      </label>
      <label className="transport-field">
        <span>Worker NATS URL (what workers dial; default is the NATS URL)</span>
        <input className="input input-mono" aria-label="Worker NATS URL" placeholder="nats://nats.internal:4222" value={workerUrl}
          onChange={event => { touched.current = true; setWorkerUrl(event.target.value) }} />
      </label>
      <div className="transport-dialog__actions">
        <Button type="submit" size="sm" disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
      </div>
      {result && <p className={result.ok ? 'transport-ok' : 'transport-error'} role="status" data-testid="transport-settings-result">{result.text}</p>}
    </form>
  )
}

// ClusterTransport shows the carrier setting of the cluster, the frontends
// and workers behind it, and the NATS address settings. It is read-only apart
// from the NATS settings: changing the bus is an API and CLI operation. It
// renders nothing when the cluster routes are not available. `nodes` is the node
// list of the page; it gives the live state of each route of a worker.
export default function ClusterTransport({ nodes }) {
  const [report, setReport] = useState(null)
  const [available, setAvailable] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [fetchedAt, setFetchedAt] = useState(0)
  const [now, setNow] = useState(Date.now())

  const load = useCallback(async () => {
    try {
      const data = await clusterApi.carrier()
      setReport(data)
      setFetchedAt(Date.now())
      setLoadError('')
      setAvailable(true)
    } catch (error) {
      if (isUnavailable(error)) setAvailable(false)
      else setLoadError(error.message || 'Unable to read the cluster transport')
    }
  }, [])

  const busy = !!report && report.state !== 'stable'
  const draining = !!report && Number(report.drain_remaining_ns) > 0
  useEffect(() => {
    load()
    const interval = setInterval(load, busy || draining ? POLL_BUSY_MS : POLL_IDLE_MS)
    return () => clearInterval(interval)
  }, [load, busy, draining])

  useEffect(() => {
    if (!draining) return undefined
    const tick = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(tick)
  }, [draining])

  if (!available) return null
  if (!report) {
    return loadError ? <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport"><p className="transport-error" role="alert">{loadError}</p></section> : null
  }

  const label = stateLabel(report)
  const remainingNs = draining ? Math.max(0, Number(report.drain_remaining_ns) - (now - fetchedAt) * 1e6) : 0
  const target = report.row?.target

  return (
    <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport" data-testid="cluster-transport">
      <div className="transport-head">
        <div>
          <span className="fleet-kicker">Carrier setting of the cluster</span>
          <div className="transport-head__line">
            <strong className="transport-head__carrier" data-testid="transport-active">{busLabel(report.active)}</strong>
            <span className={`badge ${stateTone(label)}`} data-testid="transport-state">{label}</span>
            <span className="text-note" data-testid="transport-epoch">epoch {report.epoch}</span>
          </div>
          <p className="text-note">Each worker attaches over NATS, WebSocket or P2P. Frontends broadcast over NATS while they reach it, and over PostgreSQL otherwise. Workers never connect to PostgreSQL.</p>
          {busy && target && <p className="text-note" role="status">A change to {busLabel(target)} is in progress: {report.state === 'prepare' ? 'every frontend builds the new bus and reports ready' : 'the new bus is active, frontends are confirming'}.</p>}
          {draining && <p className="text-note" role="status" data-testid="transport-drain">Draining {busLabel(report.row?.draining)}: {formatCountdown(remainingNs)} left. Work that started there finishes there; what is left is cut afterwards.</p>}
          {loadError && <p className="transport-error" role="alert">{loadError}</p>}
        </div>
      </div>

      <div className="transport-grid">
        <div>
          <p className="form-label">Frontend replicas</p>
          <ReplicaList replicas={report.replicas} report={report} label="Replicas" />
        </div>
        <div>
          <p className="form-label">Workers</p>
          <WorkerTable workers={report.workers} nodes={nodes} />
        </div>
      </div>

      <NatsSettings settings={report.settings} onSaved={load} />
      <CarrierWeights settings={report.settings} onSaved={load} />
    </section>
  )
}
