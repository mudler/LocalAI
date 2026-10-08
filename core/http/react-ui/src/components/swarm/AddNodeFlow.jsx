/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi, p2pApi } from '../../utils/api'
import { usePolling } from '../../hooks/usePolling'
import { useImageSelector } from '../ImageSelector'
import { DISTRIBUTED_COMMAND, p2pCommand, workerCommand } from '../../utils/swarm'
import CommandBlock from './CommandBlock'
import NodeState from './NodeState'
import Icon from '../Icon'

const METHODS = ['worker', 'peer', 'shard']

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

// Reads the P2P token and the number of peers online, for the two methods that
// join over P2P. `enabled` is false for a registered worker, which uses neither.
function useP2P(enabled) {
  const [token, setToken] = useState(null)
  const [stats, setStats] = useState(null)
  const read = async () => {
    const [tokenResult, statsResult] = await Promise.allSettled([p2pApi.getToken(), p2pApi.getStats()])
    if (tokenResult.status === 'fulfilled') {
      const value = tokenResult.value
      setToken((typeof value === 'string' ? value : value?.token || '').trim())
    } else {
      setToken('')
    }
    if (statsResult.status === 'fulfilled') setStats(statsResult.value)
  }
  usePolling(read, 3000, { enabled })
  const online = stats ? (stats.federated?.online ?? 0) + (stats.llama_cpp_workers?.online ?? 0) + (stats.mlx_workers?.online ?? 0) : null
  return { token, online, loaded: token !== null }
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
//   initialMethod  'worker' | 'peer' | 'shard', the join method shown first
export default function AddNodeFlow({ nodes, single, addToast, onApproved, initialMethod }) {
  const { t } = useTranslation('swarm')
  const [method, setMethod] = useState(METHODS.includes(initialMethod) ? initialMethod : 'worker')
  const [shard, setShard] = useState('llama')
  const [flavor, setFlavor] = useState('docker')
  const [nodeType, setNodeType] = useState('backend')
  const hardware = useImageSelector('cpu')
  const [startedAt] = useState(() => Date.now())
  const p2p = useP2P(method !== 'worker')
  const [approving, setApproving] = useState(null)

  // The roster at the moment the page opened. Anything else is new.
  const baseline = useRef(null)
  if (baseline.current === null && Array.isArray(nodes)) baseline.current = new Set(nodes.map(node => node.id))
  const p2pBaseline = useRef(null)
  if (p2pBaseline.current === null && p2p.online !== null) p2pBaseline.current = p2p.online

  const arrived = useMemo(() => (
    Array.isArray(nodes) && baseline.current ? nodes.filter(node => !baseline.current.has(node.id)) : []
  ), [nodes])

  const frontend = typeof window === 'undefined' ? '' : window.location.origin
  const command = method === 'worker'
    ? workerCommand({ flavor, nodeType, option: hardware.option, dev: hardware.dev, frontend })
    : p2pCommand({ method: method === 'peer' ? 'peer' : shard === 'mlx' ? 'mlx' : 'shard', option: hardware.option, dev: hardware.dev, token: p2p.token, flavor })

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

  const needsDistributed = single && method === 'worker'
  const p2pOff = method !== 'worker' && p2p.loaded && !p2p.token
  let n = 0
  const next = () => { n += 1; return n }
  const p2pFound = method !== 'worker' && p2p.online !== null && p2pBaseline.current !== null && p2p.online > p2pBaseline.current

  return (
    <div className="sw-add" data-testid="add-node">
      <Step n={next()} id="sw-add-join" title={t('add.join.title')}>
        <Segmented
          label={t('add.join.title')}
          value={method}
          onChange={setMethod}
          options={METHODS.map(key => [key, t(`add.join.${key}`)])}
        />
        <p className="sw-note">{t(`add.join.${method}Sub`)}</p>
        {method === 'shard' && (
          <Segmented
            label={t('add.shard.label')}
            value={shard}
            onChange={setShard}
            options={[['llama', t('add.shard.llama')], ['mlx', t('add.shard.mlx')]]}
          />
        )}
      </Step>

      {needsDistributed && (
        <Step n={next()} id="sw-add-distributed" title={t('add.distributed.title')}>
          <p className="sw-note">{t('add.distributed.sub')}</p>
          <CommandBlock command={DISTRIBUTED_COMMAND} label={t('add.distributed.title')} addToast={addToast} />
          <p className="sw-note sw-note--quiet">
            {t('add.distributed.docs')}{' '}
            <a className="dk-link" href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer">{t('add.distributed.docsLink')}</a>
          </p>
        </Step>
      )}

      {p2pOff && (
        <Step n={next()} id="sw-add-p2p" title={t('add.p2pOff.title')}>
          <p className="sw-note">{t('add.p2pOff.sub')}</p>
          <CommandBlock
            command={flavor === 'cli' ? 'local-ai run --p2p' : `docker run -ti --net host${hardware.option.dockerFlags ? ` ${hardware.option.dockerFlags}` : ''} \\\n  --name local-ai \\\n  localai/localai:${hardware.dev ? hardware.option.devTag : hardware.option.tag} run --p2p`}
            label={t('add.p2pOff.title')}
            addToast={addToast}
          />
        </Step>
      )}

      <Step n={next()} id="sw-add-run" title={t('add.run.title')}>
        <div className="sw-add__choices">
          {flavor === 'docker' && !(method === 'shard' && shard === 'mlx') && (
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
          {method === 'worker' && (
            <Segmented label={t('add.run.kind')} value={nodeType} onChange={setNodeType} options={[['backend', t('add.run.backend')], ['agent', t('add.run.agent')]]} />
          )}
          {flavor === 'docker' && (
            <button type="button" className="dk-chip" aria-pressed={hardware.dev} onClick={() => hardware.setDev(!hardware.dev)} title={t('add.run.devTitle')}>{t('add.run.dev')}</button>
          )}
        </div>
        <CommandBlock command={command} label={t('add.run.title')} addToast={addToast} />
        <p className="sw-note sw-note--quiet">
          {method === 'worker' ? t('add.run.tokenNote') : t('add.run.p2pNote')}
        </p>
      </Step>

      <Step n={next()} id="sw-add-wait" title={arrived.length > 0 || p2pFound ? t('add.wait.foundTitle') : t('add.wait.title')}>
        {method === 'worker' && arrived.length === 0 && (
          <p className="sw-wait" role="status">
            <span className="sw-wait__dot" aria-hidden="true" />
            <span>{t('add.wait.listening')} <Elapsed since={startedAt} /></span>
          </p>
        )}
        {method === 'worker' && arrived.map(node => (
          <div key={node.id} className="sw-found" data-testid="found-node">
            <div className="sw-found__text">
              <Link className="dk-table-name dk-mono sw-name" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{node.name}</Link>
              <span className="dk-mono sw-found__addr">{node.address}</span>
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
        {method !== 'worker' && !p2pFound && (
          <p className="sw-wait" role="status">
            <span className="sw-wait__dot" aria-hidden="true" />
            <span>
              {p2p.online === null ? t('add.wait.p2pListening') : t('add.wait.p2pCount', { count: p2p.online })} <Elapsed since={startedAt} />
            </span>
          </p>
        )}
        {method !== 'worker' && p2pFound && (
          <p className="sw-found sw-found--p2p" role="status" data-testid="found-peer">
            <Icon name="check-circle" /> {t('add.wait.p2pFound', { count: p2p.online })}
            <Link className="dk-link" to="/app/p2p">{t('add.wait.p2pOpen')}</Link>
          </p>
        )}
        {arrived.length === 0 && !p2pFound && <p className="sw-note sw-note--quiet">{t(method === 'worker' ? 'add.wait.stuck' : 'add.wait.p2pStuck')}</p>}
      </Step>
    </div>
  )
}
