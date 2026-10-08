import { useCallback, useEffect, useRef, useState } from 'react'
import { clusterApi } from '../../utils/api'
import {
  CARRIERS, PROFILE_LABELS, canForce, carrierLabel, formatCountdown, otherCarrier,
  replicaReady, stateLabel, workerProfile,
} from '../../utils/clusterTransport'
import ConfirmDialog from '../ConfirmDialog'
import Modal from '../Modal'

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

function WorkerTable({ workers }) {
  if (!workers || workers.length === 0) return <p className="text-note">No worker is registered.</p>
  return (
    <div className="transport-table-wrap">
      <table className="transport-table" aria-label="Workers by carrier">
        <thead><tr><th>Worker</th><th>Profile</th><th>Attached to</th><th>Follow status</th></tr></thead>
        <tbody>
          {workers.map(worker => (
            <tr key={worker.id} data-testid="transport-worker">
              <td>{worker.name || worker.id}</td>
              <td>{PROFILE_LABELS[workerProfile(worker)]}</td>
              <td>{(worker.attached || []).length ? worker.attached.map(carrierLabel).join(' + ') : 'not reported'}</td>
              <td>{worker.follow_error
                ? <span className="transport-error" role="status">{worker.follow_error}</span>
                : <span className="text-note">ok</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function BlockerList({ blockers }) {
  return (
    <ul className="transport-blockers" aria-label="Blockers">
      {blockers.map((blocker, index) => (
        <li key={`${blocker.kind}-${blocker.id || index}`} data-testid="transport-blocker">
          <strong>{blocker.kind}{blocker.id ? ` ${blocker.id}` : ''}</strong>
          <span>{blocker.reason}</span>
          {blocker.forceable && <span className="badge badge-warning">can be forced</span>}
        </li>
      ))}
    </ul>
  )
}

function SwitchDialog({ report, onClose, onStarted, addToast }) {
  const [target, setTarget] = useState(otherCarrier(report.active))
  const [dry, setDry] = useState(null)
  const [dryError, setDryError] = useState('')
  const [running, setRunning] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [force, setForce] = useState(false)
  const [confirmForce, setConfirmForce] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')

  const resetResult = () => { setDry(null); setDryError(''); setConfirmed(false); setForce(false); setSubmitError('') }
  const pickTarget = value => { setTarget(value); resetResult() }

  const runDry = async () => {
    setRunning(true)
    resetResult()
    try {
      setDry(await clusterApi.dryRun(target))
    } catch (error) {
      setDryError(error.message || 'The dry run failed')
    } finally {
      setRunning(false)
    }
  }

  const blockers = dry?.blockers || []
  const blocked = blockers.length > 0
  const forceable = canForce(dry)
  const sameCarrier = target === report.active
  const mayStart = !!dry && confirmed && !sameCarrier && (!blocked || (force && forceable))

  const start = async () => {
    setSubmitting(true)
    setSubmitError('')
    try {
      const answer = await clusterApi.switchCarrier(target, force)
      addToast(`Change to ${carrierLabel(target)} started (epoch ${answer.epoch}).`, 'success')
      onStarted()
      onClose()
    } catch (error) {
      const refused = error?.body?.blockers
      setSubmitError(Array.isArray(refused) && refused.length
        ? `${error.message}: ${refused.map(b => b.reason).join('; ')}`
        : (error.message || 'The request failed'))
    } finally {
      setSubmitting(false)
      setConfirmForce(false)
    }
  }

  const onPrimary = () => { if (force) setConfirmForce(true); else start() }
  const forcedNodes = blockers.filter(b => b.kind === 'worker').length

  return (
    <>
      <Modal onClose={submitting ? () => {} : onClose} maxWidth="680px" ariaLabel="Switch cluster transport">
        <div className="transport-dialog">
          <h3 className="panel-title"><i className="fas fa-right-left" aria-hidden="true" />Switch cluster transport</h3>
          <p className="text-base text-secondary">Run a dry run first. It changes nothing and lists what would block the change.</p>

          <div role="radiogroup" aria-label="Target carrier" className="segmented transport-dialog__targets">
            {CARRIERS.map(value => (
              <button key={value} type="button" role="radio" aria-checked={target === value}
                className={`segmented__item${target === value ? ' is-active' : ''}`}
                disabled={running || submitting} onClick={() => pickTarget(value)}>
                {carrierLabel(value)}{value === report.active ? ' (active)' : ''}
              </button>
            ))}
          </div>
          {sameCarrier && <p className="text-note" role="status">{carrierLabel(target)} is the active carrier already.</p>}

          <div className="transport-dialog__actions">
            <button type="button" className="btn btn-secondary btn-sm" onClick={runDry} disabled={running || submitting || sameCarrier}>
              {running ? 'Checking…' : 'Dry run'}
            </button>
          </div>
          {dryError && <p className="transport-error" role="alert">{dryError}</p>}

          {dry && (
            <div className="transport-dialog__result" aria-live="polite">
              <p className={dry.ok ? 'transport-ok' : 'transport-error'} data-testid="transport-dry-verdict">
                {dry.ok ? `Nothing blocks a change to ${carrierLabel(target)}.` : `${blockers.length} blocker${blockers.length === 1 ? '' : 's'} for a change to ${carrierLabel(target)}.`}
              </p>
              {blocked && <BlockerList blockers={blockers} />}
              {(dry.warnings || []).map(w => <p key={w} className="text-note">{w}</p>)}
              {dry.in_flight && (
                <p className="text-note">In flight: {dry.in_flight.loads} loading model(s), {dry.in_flight.jobs} job(s), {dry.in_flight.pending_claims + dry.in_flight.claimed_claims} claim(s). They finish where they started.</p>
              )}
              <p className="form-label">Live frontend replicas</p>
              <ReplicaList replicas={dry.replicas} report={report} label="Live replicas" />
              <label className="checkbox-row">
                <input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />
                I confirm these are all the frontends and all are upgraded
              </label>
              {blocked && (
                forceable
                  ? <label className="checkbox-row">
                      <input type="checkbox" checked={force} onChange={event => setForce(event.target.checked)} />
                      Force the change past the blockers above
                    </label>
                  : <p className="text-note">At least one blocker cannot be forced.</p>
              )}
            </div>
          )}
          {submitError && <p className="transport-error" role="alert">{submitError}</p>}

          <div className="transport-dialog__footer">
            <button type="button" className="btn btn-secondary btn-sm" onClick={onClose} disabled={submitting}>Cancel</button>
            <button type="button" className={`btn btn-sm ${force ? 'btn-danger' : 'btn-primary'}`} disabled={!mayStart || submitting} onClick={onPrimary}>
              {submitting ? 'Starting…' : force ? `Force switch to ${carrierLabel(target)}` : `Switch to ${carrierLabel(target)}`}
            </button>
          </div>
        </div>
      </Modal>
      <ConfirmDialog open={confirmForce} danger title="Force the change?"
        message={`A forced change goes ahead without the replicas and workers that are listed as blockers.${forcedNodes ? ` ${forcedNodes} worker(s) cannot follow and become unroutable until they are restarted or fixed. They are not removed.` : ''}`}
        confirmLabel="Force the change" pendingLabel="Starting…" pending={submitting}
        onConfirm={start} onCancel={() => setConfirmForce(false)} />
    </>
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
          ? 'Saved. This frontend reaches the NATS server. The active carrier did not change.'
          : 'Saved. The active carrier did not change.',
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
      <p className="text-note">Saving makes NATS available and checks that this frontend reaches it. It does not switch the cluster.</p>
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
        <button type="submit" className="btn btn-secondary btn-sm" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
      </div>
      {result && <p className={result.ok ? 'transport-ok' : 'transport-error'} role="status" data-testid="transport-settings-result">{result.text}</p>}
    </form>
  )
}

// ClusterTransport shows which carrier the cluster uses and lets an admin
// change it. It renders nothing when the cluster routes are not available.
export default function ClusterTransport({ addToast }) {
  const [report, setReport] = useState(null)
  const [available, setAvailable] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [showSwitch, setShowSwitch] = useState(false)
  const [confirmAbort, setConfirmAbort] = useState(false)
  const [aborting, setAborting] = useState(false)
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

  const abort = async () => {
    setAborting(true)
    try {
      await clusterApi.abort()
      addToast('The change was aborted. The previous carrier stays active.', 'success')
      await load()
    } catch (error) {
      addToast(`Could not abort: ${error.message}`, 'error')
    } finally {
      setAborting(false)
      setConfirmAbort(false)
    }
  }

  if (!available) return null
  if (!report) {
    return loadError ? <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport"><p className="transport-error" role="alert">{loadError}</p></section> : null
  }

  const label = stateLabel(report)
  const remainingNs = draining ? Math.max(0, Number(report.drain_remaining_ns) - (now - fetchedAt) * 1e6) : 0
  const other = otherCarrier(report.active)
  const target = report.row?.target

  return (
    <section className="card pad-lg mb-lg transport-panel" aria-label="Cluster transport" data-testid="cluster-transport">
      <div className="transport-head">
        <div>
          <span className="fleet-kicker">Cluster transport</span>
          <div className="transport-head__line">
            <strong className="transport-head__carrier" data-testid="transport-active">{carrierLabel(report.active)}</strong>
            <span className={`badge ${stateTone(label)}`} data-testid="transport-state">{label}</span>
            <span className="text-note" data-testid="transport-epoch">epoch {report.epoch}</span>
          </div>
          {busy && target && <p className="text-note" role="status">Changing to {carrierLabel(target)}: {report.state === 'prepare' ? 'every frontend builds the new carrier and reports ready' : 'the new carrier is active, frontends are confirming'}.</p>}
          {draining && <p className="text-note" role="status" data-testid="transport-drain">Draining {carrierLabel(report.row?.draining || other)}: {formatCountdown(remainingNs)} left. Work that started there finishes there; what is left is cut afterwards.</p>}
          {loadError && <p className="transport-error" role="alert">{loadError}</p>}
        </div>
        <div className="transport-head__actions">
          {report.state === 'prepare' && <button type="button" className="btn btn-danger btn-sm" onClick={() => setConfirmAbort(true)} disabled={aborting}>Abort change</button>}
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setShowSwitch(true)} disabled={busy}>Switch to {carrierLabel(other)}…</button>
        </div>
      </div>

      <div className="transport-grid">
        <div>
          <p className="form-label">Frontend replicas</p>
          <ReplicaList replicas={report.replicas} report={report} label="Replicas" />
        </div>
        <div>
          <p className="form-label">Workers</p>
          <WorkerTable workers={report.workers} />
        </div>
      </div>

      <NatsSettings settings={report.settings} onSaved={load} />

      {showSwitch && <SwitchDialog report={report} onClose={() => setShowSwitch(false)} onStarted={load} addToast={addToast} />}
      <ConfirmDialog open={confirmAbort} danger title="Abort the change?"
        message={`The cluster stays on ${carrierLabel(report.active)}. Nothing moves.`}
        confirmLabel="Abort the change" pendingLabel="Aborting…" pending={aborting}
        onConfirm={abort} onCancel={() => setConfirmAbort(false)} />
    </section>
  )
}
