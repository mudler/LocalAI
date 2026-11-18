/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { p2pNetApi, p2pSplitApi } from '../../utils/api'
import { usePolling } from '../../hooks/usePolling'
import { useImageSelector } from '../ImageSelector'
import { clock, secondsLeft, shortPeer } from '../../utils/p2p'
import { P2P_SETTINGS_PATH } from '../p2p/P2PTabs'
import { Button, Callout, Segmented, StatusChip, useLoad } from '../ui'
import CommandBlock from './CommandBlock'
import RelayLimitsDialog from '../p2psettings/RelayLimitsDialog'
import { ENGINES, cutLimits, limitsRelay, pathOf, splitCommand, stateOf } from './splitWorkersModel'
import './split.css'

// The life of a model-split invite: long enough to set up the worker.
const INVITE_TTL = 600

// The steps of "Add a node" for a model-split worker (after "What are you
// adding?"): the engine, an invite of kind split, the command with the link,
// and a line that follows the worker once it pairs. A worker that only a relay
// reaches is not used until the administrator chooses "Add anyway".
//
//   Step      the step component of the flow (number, title, children)
//   first     the number of the first step here
export default function AddSplitWorker({ Step, first, addToast }) {
  const { t } = useTranslation('swarm')
  const status = useLoad(() => p2pNetApi.status(), [])
  const sharing = status.status === 'ready' && status.data?.mode === 'share'
  const [engine, setEngine] = useState('llama')
  const [name, setName] = useState('')
  const [invite, setInvite] = useState(null)
  const [used, setUsed] = useState(false)
  const [minting, setMinting] = useState(false)
  const [error, setError] = useState('')
  const [flavor, setFlavor] = useState('docker')
  const hardware = useImageSelector('cpu')

  const mint = async () => {
    setMinting(true)
    setError('')
    try {
      const res = await p2pNetApi.mintInvite({ kind: 'split', label: name.trim(), ttl_seconds: INVITE_TTL, labels: { engine } })
      setInvite(res)
      setUsed(false)
    } catch (err) {
      setError(err?.message || '')
    } finally {
      setMinting(false)
    }
  }
  // A new engine needs a new invite: the invite names it.
  const chooseEngine = id => { setEngine(id); setInvite(null) }

  let n = first - 1
  const next = () => { n += 1; return n }

  if (status.status === 'ready' && !sharing) {
    return (
      <Step n={next()} id="sw-split-p2p" title={t('split.add.p2pTitle')}>
        <Callout tone="warn" testId="split-needs-share" actions={<Button size="sm" to={P2P_SETTINGS_PATH}>{t('split.add.p2pOpen')}</Button>}>
          {t('split.add.p2pBody')}
        </Callout>
      </Step>
    )
  }

  const command = splitCommand({ engine, link: invite?.link, flavor, option: hardware.option, dev: hardware.dev })
  return (
    <>
      <Step n={next()} id="sw-split-engine" title={t('split.add.engineTitle')}>
        <Segmented
          mode="radio"
          label={t('split.add.engineTitle')}
          value={engine}
          onChange={chooseEngine}
          options={ENGINES.map(id => ({ id, label: t(`split.engine.${id}Long`) }))}
          itemTestId="split-engine-"
        />
        <p className="sw-note sw-note--quiet">{t(`split.add.engineSub.${engine}`)}</p>
      </Step>

      <Step n={next()} id="sw-split-invite" title={t('split.add.inviteTitle')}>
        <div className="sw-split-invite">
          {!invite && (
            <div className="sw-split-invite__row">
              <label className="sw-split-invite__field">
                <span className="dk-label">{t('split.add.name')}</span>
                <input
                  className="dk-input" value={name} maxLength={255} placeholder={t('split.add.namePlaceholder')}
                  onChange={event => setName(event.target.value)} data-testid="split-name"
                />
              </label>
              <Button variant="primary" busy={minting} onClick={mint} data-testid="split-mint">{t('split.add.mint')}</Button>
            </div>
          )}
          {invite && <InviteLine invite={invite} used={used} />}
          {error && <Callout tone="error" role="alert" testId="split-mint-error">{error}</Callout>}
          <Callout title={t('split.add.pathTitle')}>{t('split.add.pathBody')}</Callout>
        </div>
      </Step>

      <Step n={next()} id="sw-split-run" title={t('split.add.runTitle')}>
        {invite
          ? (
            <>
              {engine === 'llama' && (
                <div className="sw-add__choices">
                  {flavor === 'docker' && (
                    <div className="dk-segmented sw-hardware" role="radiogroup" aria-label={t('add.run.hardware')}>
                      {hardware.options.map(option => (
                        <button key={option.key} type="button" role="radio" aria-checked={hardware.selected === option.key} className="dk-seg" onClick={() => hardware.setSelected(option.key)}>
                          {option.label}
                        </button>
                      ))}
                    </div>
                  )}
                  <Segmented
                    mode="radio"
                    label={t('add.run.how')}
                    value={flavor}
                    onChange={setFlavor}
                    options={[{ id: 'docker', label: t('add.run.docker') }, { id: 'cli', label: t('add.run.cli') }]}
                  />
                </div>
              )}
              <CommandBlock command={command} label={t('split.add.runTitle')} addToast={addToast} />
              <p className="sw-note sw-note--quiet">{t(engine === 'mlx' ? 'split.add.runNoteMlx' : 'split.add.runNote')}</p>
            </>
          )
          : <p className="sw-note sw-note--quiet">{t('split.add.runFirst')}</p>}
      </Step>

      {invite && <WaitStep Step={Step} n={next()} invite={invite} addToast={addToast} onPaired={() => setUsed(true)} />}
    </>
  )
}

// The state of the invite: single use, the time left and the server it names.
function InviteLine({ invite, used }) {
  const { t } = useTranslation('swarm')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  const left = secondsLeft(invite.expires_at, now)
  if (used) {
    return (
      <p className="sw-split-invite__meta" data-testid="split-invite-used">
        <StatusChip tone="idle">{t('split.add.inviteUsed')}</StatusChip>
      </p>
    )
  }
  return (
    <p className="sw-split-invite__meta" data-testid="split-invite-ready">
      <StatusChip tone={left > 0 ? 'ok' : 'idle'}>{t(left > 0 ? 'split.add.inviteReady' : 'split.add.inviteExpired')}</StatusChip>
      <span>{t('split.add.inviteMeta', { time: clock(left) })}</span>
      {invite.relay && !invite.relay.pass && invite.relay.reason === 'relay_unreachable' && <span>{t('split.add.noPass')}</span>}
    </p>
  )
}

// Follows the invite until a worker redeemed it, then the worker until it is
// in use. A worker that only a relay reaches waits for "Add anyway".
function WaitStep({ Step, n, invite, addToast, onPaired }) {
  const { t } = useTranslation('swarm')
  const [deviceId, setDeviceId] = useState('')
  const [worker, setWorker] = useState(null)
  const [busy, setBusy] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const [limitsOf, setLimitsOf] = useState(null)
  const [startedAt] = useState(() => Date.now())

  const read = useCallback(async () => {
    let id = deviceId
    if (!id) {
      const list = await p2pNetApi.invites().catch(() => null)
      const mine = list?.invites?.find(i => i.id === invite.id)
      if (mine?.device_id) {
        id = mine.device_id
        setDeviceId(id)
        onPaired?.()
      }
    }
    if (!id) return
    const split = await p2pSplitApi.list().catch(() => null)
    setWorker(split?.workers?.find(w => w.id === id) || null)
  }, [deviceId, invite.id, onPaired])
  usePolling(read, 3000)

  const useAnyway = async () => {
    setBusy(true)
    try {
      setWorker(await p2pSplitApi.setSlowPath(worker.id, true))
      addToast?.(t('split.toast.used', { name: worker.label || shortPeer(worker.peer_id) }), 'success')
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const state = worker ? stateOf(worker).key : null
  const title = !deviceId ? t('split.add.waitTitle')
    : state === 'slow' || state === 'limited' ? t('split.add.relayTitle')
      : state === 'online' ? t('split.add.foundTitle')
        : t('split.add.pairedTitle')
  return (
    <Step n={n} id="sw-split-wait" title={title}>
      {!deviceId && (
        <p className="sw-wait" role="status">
          <span className="sw-wait__dot" aria-hidden="true" />
          <span>{t('split.add.waiting')} <Elapsed since={startedAt} /></span>
        </p>
      )}
      {deviceId && !worker && <p className="sw-note" role="status">{t('split.add.pairedWaiting')}</p>}
      {worker && <WorkerLine worker={worker} />}
      {state === 'slow' && !dismissed && (
        <Callout
          tone="warn" title={t('split.add.slowTitle')} testId="split-add-slow"
          actions={(
            <>
              <Button size="sm" variant="primary" busy={busy} onClick={useAnyway} data-testid="split-add-anyway">{t('split.add.addAnyway')}</Button>
              {limitsRelay(worker) && (
                <Button size="sm" variant="ghost" icon="sliders" onClick={() => setLimitsOf(limitsRelay(worker))} data-testid="split-add-limits">{t('split.relayLimits')}</Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setDismissed(true)}>{t('split.add.notNow')}</Button>
            </>
          )}
        >
          {t('split.add.slowBody', { name: worker.label || shortPeer(worker.peer_id) })}
          {cutLimits(worker).length > 0 && <> {t('split.add.slowCut', { limits: cutLimits(worker).join(', ') })}</>}
        </Callout>
      )}
      {limitsOf && <RelayLimitsDialog relay={limitsOf} addToast={addToast} onSaved={read} onClose={() => setLimitsOf(null)} />}
      {state === 'limited' && <Callout tone="error" title={t('split.limitedTitle')} testId="split-add-limited">{t('split.limitedBody')}</Callout>}
      {state === 'refused' && <Callout tone="error" role="alert" testId="split-add-refused">{worker.error}</Callout>}
      {state === 'online' && (
        <p className="sw-note">{t('split.add.foundBody')} <Link className="dk-link" to="/app/nodes">{t('split.add.openNodes')}</Link></p>
      )}
    </Step>
  )
}

function WorkerLine({ worker }) {
  const { t } = useTranslation('swarm')
  const path = pathOf(worker)
  const state = stateOf(worker)
  return (
    <div className="sw-split-found" data-testid="split-found">
      <span className="dk-mono">{worker.label || worker.name || shortPeer(worker.peer_id)}</span>
      <span className="dk-mono sw-count">{shortPeer(worker.peer_id)}</span>
      {path.key !== 'none' && <StatusChip tone={worker.path?.kind === 'relayed' ? 'warn' : 'ok'} dot>{t(`split.path.${path.key}`, { relay: path.relay })}</StatusChip>}
      {path.rtt && <span className="sw-count">{t('split.path.rtt', { ms: path.rtt })}</span>}
      <StatusChip tone={state.tone} dot>{t(`split.state.${state.key}`)}</StatusChip>
    </div>
  )
}

function Elapsed({ since }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  const seconds = Math.max(0, Math.floor((now - since) / 1000))
  return <span className="dk-mono">{Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, '0')}</span>
}
