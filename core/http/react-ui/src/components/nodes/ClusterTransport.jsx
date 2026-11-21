import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { clusterApi } from '../../utils/api'
import { attachedRoutes, busLabel, busNote, routeChips, routeLabel } from '../../utils/clusterTransport'
import { Button, StatusChip } from '../ui'
import CarrierWeights from './CarrierWeights'

const POLL_MS = 5000

// The routes answer 404 on a build without them and 503 on a frontend that is
// not distributed. Either way there is no transport to show.
function isUnavailable(error) {
  return error?.status === 404 || error?.status === 503 || error?.status === 401 || error?.status === 403
}

function ReplicaList({ replicas, label }) {
  if (!replicas || replicas.length === 0) return <p className="text-note">No live frontend replica reported.</p>
  return (
    <ul className="transport-list" aria-label={label}>
      {replicas.map(replica => (
        <li key={replica.id} className="transport-list__row" data-testid="transport-replica">
          <span className="transport-dot transport-dot--ok" aria-hidden="true" />
          <span className="transport-list__name">{replica.id}</span>
          <span className="transport-list__meta">{replica.version || 'unknown version'}</span>
        </li>
      ))}
    </ul>
  )
}

// The routes of one worker and whether each is live, from the route state of
// its node (GET /api/nodes). A node that carries no route state shows the
// routes it reported.
function WorkerRoutes({ node }) {
  const { t } = useTranslation('swarm')
  const chips = routeChips(node?.route_state)
  if (chips.length === 0) {
    const attached = attachedRoutes(node)
    return attached.length ? attached.map(routeLabel).join(' + ') : 'not reported'
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

function WorkerTable({ nodes }) {
  if (!nodes || nodes.length === 0) return <p className="text-note">No worker is registered.</p>
  return (
    <div className="transport-table-wrap">
      <table className="transport-table" aria-label="Workers by route">
        <thead><tr><th>Worker</th><th>Attached over</th></tr></thead>
        <tbody>
          {nodes.map(node => (
            <tr key={node.id} data-testid="transport-worker">
              <td>{node.name || node.id}</td>
              <td><WorkerRoutes node={node} /></td>
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
          ? 'Saved. This frontend reaches the NATS server. Restart the frontends to use the new address.'
          : 'Saved. Restart the frontends to use the new address.',
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
      <p className="text-note">Saving checks that this frontend reaches the NATS server. The frontends use a new address after a restart.</p>
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

// ClusterTransport shows the broadcast bus of this frontend, the frontends and
// the workers of the cluster with the routes each worker holds, the NATS
// address settings and the carrier weights. It renders nothing when the
// cluster routes are not available. `nodes` is the node list of the page; it
// gives the workers and the live state of each route of a worker.
export default function ClusterTransport({ nodes }) {
  const [view, setView] = useState(null)
  const [available, setAvailable] = useState(true)
  const [loadError, setLoadError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await clusterApi.settings()
      setView(data)
      setLoadError('')
      setAvailable(true)
    } catch (error) {
      if (isUnavailable(error)) setAvailable(false)
      else setLoadError(error.message || 'Unable to read the cluster transport')
    }
  }, [])

  useEffect(() => {
    load()
    const interval = setInterval(load, POLL_MS)
    return () => clearInterval(interval)
  }, [load])

  if (!available) return null
  if (!view) {
    return loadError ? <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport"><p className="transport-error" role="alert">{loadError}</p></section> : null
  }

  const note = busNote(view.bus)

  return (
    <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport" data-testid="cluster-transport">
      <div className="transport-head">
        <div>
          <span className="fleet-kicker">Broadcast bus between frontends</span>
          <div className="transport-head__line">
            <strong className="transport-head__bus" data-testid="transport-bus">{busLabel(view.bus)}</strong>
          </div>
          <p className="text-note">Each worker attaches over NATS, WebSocket or P2P. Frontends broadcast over NATS while they reach it, and over PostgreSQL otherwise. Workers never connect to PostgreSQL.</p>
          {note && <p className="text-note" role="status" data-testid="transport-bus-note">{note}</p>}
          {loadError && <p className="transport-error" role="alert">{loadError}</p>}
        </div>
      </div>

      <div className="transport-grid">
        <div>
          <p className="form-label">Frontend replicas</p>
          <ReplicaList replicas={view.replicas} label="Replicas" />
        </div>
        <div>
          <p className="form-label">Workers</p>
          <WorkerTable nodes={nodes} />
        </div>
      </div>

      <NatsSettings settings={view} onSaved={load} />
      <CarrierWeights settings={view} onSaved={load} />
    </section>
  )
}
