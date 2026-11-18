/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi, p2pNetApi } from '../../utils/api'
import { useImageSelector } from '../ImageSelector'
import { DISTRIBUTED_COMMAND, joinCommand, shortPeer, workerCommand } from '../../utils/swarm'
import AddSplitWorker from './AddSplitWorker'
import CommandBlock from './CommandBlock'
import NodeState from './NodeState'
import NodeRoute from './NodeRoute'
import { Button } from '../ui'

const METHODS = ['worker', 'shard']

function Step({ n, title, children, id }) {
  return (
    <section className="sw-step" aria-labelledby={id}>
      <span className="sw-step__n" aria-hidden="true">{n}</span>
      <div className="sw-step__body">
        <h2 className="sw-step__title" id={id}>{title}</h2>
        {children}
      </div>
    </section>
  )
}

function Segmented({ label, value, onChange, options }) {
  return (
    <div className="dk-segmented" role="radiogroup" aria-label={label}>
      {options.map(([key, text]) => (
        <button key={key} type="button" role="radio" aria-checked={value === key} className="dk-seg" onClick={() => onChange(key)}>{text}</button>
      ))}
    </div>
  )
}

// The worker invite of a compute node that joins over P2P: the mode of P2P on
// this server (an invite needs "share"), the invite once made, and the call
// that makes it. The link is in the answer of the mint only, so it lives here
// and nowhere else.
function useWorkerInvite(enabled) {
  const [mode, setMode] = useState(null)
  const [minted, setMinted] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    p2pNetApi.status()
      .then(st => { if (live) setMode(st?.mode || (st?.enabled ? 'share' : 'off')) })
      .catch(() => { if (live) setMode('off') })
    return () => { live = false }
  }, [enabled])
  const mint = async (label) => {
    setBusy(true)
    setError(null)
    try {
      setMinted({ ...(await p2pNetApi.mintInvite({ kind: 'worker', label: label || undefined })), at: Date.now() })
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }
  return { mode, minted, busy, error, mint, reset: () => setMinted(null) }
}

// Seconds left until a time, as m:ss, counting down.
function Remaining({ until }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  const seconds = Math.max(0, Math.floor((new Date(until).getTime() - now) / 1000))
  return <span className="dk-mono">{Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, '0')}</span>
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

// "Add a node": which way the machine joins, the one command to run on it, and
// a live line that says when it has arrived. It reads the roster the page
// already polls, so "found" is a real node that was not there when the page
// opened, never a timer.
//
//   nodes    the roster (array), or null when this install is not a cluster
//   single   this install is not in distributed mode yet
//   initialMethod  'worker' | 'shard', the join method shown first
export default function AddNodeFlow({ nodes, single, addToast, onApproved, initialMethod }) {
  const { t } = useTranslation('swarm')
  const [method, setMethod] = useState(METHODS.includes(initialMethod) ? initialMethod : 'worker')
  const [flavor, setFlavor] = useState('docker')
  const [nodeType, setNodeType] = useState('backend')
  const hardware = useImageSelector('cpu')
  const [startedAt] = useState(() => Date.now())
  const [approving, setApproving] = useState(null)
  // How a compute node reaches this server: its address with the registration
  // token ('url'), or a worker invite over P2P ('p2p').
  const [connect, setConnect] = useState('url')
  const [label, setLabel] = useState('')
  const overP2P = method === 'worker' && connect === 'p2p'
  const invite = useWorkerInvite(overP2P)

  // The roster at the moment the page opened. Anything else is new.
  const baseline = useRef(null)
  if (baseline.current === null && Array.isArray(nodes)) baseline.current = new Set(nodes.map(node => node.id))

  const arrived = useMemo(() => (
    Array.isArray(nodes) && baseline.current ? nodes.filter(node => !baseline.current.has(node.id)) : []
  ), [nodes])

  const frontend = typeof window === 'undefined' ? '' : window.location.origin
  const command = overP2P
    ? joinCommand({ flavor, option: hardware.option, dev: hardware.dev, link: invite.minted?.link })
    : workerCommand({ flavor, nodeType, option: hardware.option, dev: hardware.dev, frontend })

  const approve = async (node) => {
    setApproving(node.id)
    try {
      await nodesApi.approve(node.id)
      addToast?.(t('toast.approved', { name: node.name }), 'success')
      await onApproved?.()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setApproving(null)
    }
  }

  let n = 0
  const next = () => { n += 1; return n }

  const joinStep = (
    <Step n={next()} id="sw-add-join" title={t('add.join.title')}>
      <Segmented
        label={t('add.join.title')}
        value={method}
        onChange={setMethod}
        options={METHODS.map(key => [key, t(`add.join.${key}`)])}
      />
      <p className="sw-note">{t(`add.join.${method}Sub`)}</p>
      <p className="sw-note sw-note--quiet">
        {t('add.join.instanceNote')}{' '}
        <Link className="dk-link" to="/app/p2p/federation">{t('add.join.instanceLink')}</Link>
      </p>
    </Step>
  )
  // A model-split worker pairs with an invite; its steps are its own.
  if (method === 'shard') {
    return (
      <div className="sw-add" data-testid="add-node">
        {joinStep}
        <AddSplitWorker Step={Step} first={2} addToast={addToast} />
      </div>
    )
  }

  return (
    <div className="sw-add" data-testid="add-node">
      {joinStep}

      {!single && (
        <Step n={next()} id="sw-add-connect" title={t('add.connect.title')}>
          <Segmented
            label={t('add.connect.title')}
            value={connect}
            onChange={setConnect}
            options={[['url', t('add.connect.url')], ['p2p', t('add.connect.p2p')]]}
          />
          <p className="sw-note">{t(connect === 'p2p' ? 'add.connect.p2pSub' : 'add.connect.urlSub')}</p>
          <p className="sw-note sw-note--quiet">{t('add.connect.order')}</p>
        </Step>
      )}

      {overP2P && !single && (
        <Step n={next()} id="sw-add-invite" title={t('add.invite.title')}>
          {invite.mode !== null && invite.mode !== 'share' && (
            <p className="sw-note" data-testid="invite-needs-share">
              {t('add.invite.needsShare')}{' '}
              <Link className="dk-link" to="/app/p2p/settings">{t('add.invite.settings')}</Link>
            </p>
          )}
          {invite.mode === 'share' && !invite.minted && (
            <div className="sw-invite">
              <label className="sw-invite__field">
                <span className="sw-invite__label">{t('add.invite.name')}</span>
                <input className="dk-input" value={label} maxLength={64} placeholder={t('add.invite.namePlaceholder')} onChange={event => setLabel(event.target.value)} />
              </label>
              <Button variant="primary" busy={invite.busy} onClick={() => invite.mint(label.trim())} data-testid="make-worker-invite">{t('add.invite.make')}</Button>
            </div>
          )}
          {invite.error && <p className="sw-note" data-level="error" role="alert">{invite.error}</p>}
          {invite.minted && (
            <p className="sw-invite__ready" data-testid="worker-invite">
              <span className="dk-badge dk-badge--ok">{t('add.invite.ready')}</span>
              <span>
                {t('add.invite.single')}{' · '}{t('add.invite.expires')} <Remaining until={invite.minted.expires_at} />
                {invite.minted.relay?.pass && <>{' · '}{t('add.invite.pass', { relay: invite.minted.relay.label || invite.minted.relay.id })}</>}
              </span>
              <Button variant="ghost" size="sm" onClick={invite.reset}>{t('add.invite.again')}</Button>
            </p>
          )}
          {invite.minted && invite.minted.warnings?.length > 0 && (
            <ul className="sw-invite__warn">
              {invite.minted.warnings.map(w => <li key={w}>{t(`add.invite.warn.${w}`, { defaultValue: w })}</li>)}
            </ul>
          )}
        </Step>
      )}

      {single && (
        <Step n={next()} id="sw-add-distributed" title={t('add.distributed.title')}>
          <p className="sw-note">{t('add.distributed.sub')}</p>
          <CommandBlock command={DISTRIBUTED_COMMAND} label={t('add.distributed.title')} addToast={addToast} />
          <p className="sw-note sw-note--quiet">
            {t('add.distributed.docs')}{' '}
            <a className="dk-link" href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer">{t('add.distributed.docsLink')}</a>
          </p>
        </Step>
      )}

      <Step n={next()} id="sw-add-run" title={t('add.run.title')}>
        <div className="sw-add__choices">
          {flavor === 'docker' && (
            <div className="dk-segmented sw-hardware" role="radiogroup" aria-label={t('add.run.hardware')}>
              {hardware.options.map(option => (
                <button
                  key={option.key}
                  type="button"
                  role="radio"
                  aria-checked={hardware.selected === option.key}
                  className="dk-seg"
                  onClick={() => hardware.setSelected(option.key)}
                >
                  {option.label}
                </button>
              ))}
            </div>
          )}
          <Segmented label={t('add.run.how')} value={flavor} onChange={setFlavor} options={[['docker', t('add.run.docker')], ['cli', t('add.run.cli')]]} />
          {!overP2P && (
            <Segmented label={t('add.run.kind')} value={nodeType} onChange={setNodeType} options={[['backend', t('add.run.backend')], ['agent', t('add.run.agent')]]} />
          )}
          {flavor === 'docker' && (
            <button type="button" className="dk-chip" aria-pressed={hardware.dev} onClick={() => hardware.setDev(!hardware.dev)} title={t('add.run.devTitle')}>{t('add.run.dev')}</button>
          )}
        </div>
        {(!overP2P || invite.minted) && <CommandBlock command={command} label={t('add.run.title')} addToast={addToast} />}
        {overP2P && !invite.minted && <p className="sw-note">{t('add.run.inviteFirst')}</p>}
        <p className="sw-note sw-note--quiet">
          {overP2P ? t('add.run.joinNote') : t('add.run.tokenNote')}
        </p>
      </Step>

      <Step n={next()} id="sw-add-wait" title={arrived.length > 0 ? t('add.wait.foundTitle') : t('add.wait.title')}>
        {arrived.length === 0 && (
          <p className="sw-wait" role="status">
            <span className="sw-wait__dot" aria-hidden="true" />
            <span>{t('add.wait.listening')} <Elapsed since={startedAt} /></span>
          </p>
        )}
        {arrived.map(node => (
          <div key={node.id} className="sw-found" data-testid="found-node">
            <div className="sw-found__text">
              <Link className="dk-table-name dk-mono sw-name" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{node.name}</Link>
              <span className="dk-mono sw-found__addr" title={node.peer_id || undefined}>{node.peer_id ? shortPeer(node.peer_id) : node.address}</span>
              <NodeRoute node={node} />
              <NodeState node={node} />
            </div>
            <div className="sw-found__acts">
              {node.status === 'pending' && (
                <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={approving === node.id} onClick={() => approve(node)}>{t('actions.approve')}</button>
              )}
              <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{t('add.wait.open')}</Link>
            </div>
          </div>
        ))}
        {arrived.some(node => node.status === 'pending' && node.peer_id) && (
          <p className="sw-note sw-note--quiet">{t('add.wait.checkPeer')}</p>
        )}
        {arrived.length === 0 && <p className="sw-note sw-note--quiet">{t(overP2P ? 'add.wait.joinStuck' : 'add.wait.stuck')}</p>}
      </Step>
    </div>
  )
}
