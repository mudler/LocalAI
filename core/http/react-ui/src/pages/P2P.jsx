/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { p2pApi } from '../utils/api'
import { usePolling } from '../hooks/usePolling'
import { copyToClipboard } from '../utils/clipboard'
import { useImageSelector } from '../components/ImageSelector'
import { p2pCommand } from '../utils/swarm'
import CommandBlock from '../components/swarm/CommandBlock'
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
import './swarm.css'

const EMPTY_STATS = { llama_cpp_workers: { online: 0, total: 0 }, federated: { online: 0, total: 0 }, mlx_workers: { online: 0, total: 0 } }

// A list of peers, one row each with the word Online or Offline.
function PeerTable({ rows, label, empty }) {
  const { t } = useTranslation('swarm')
  if (rows.length === 0) return <p className="sw-empty" data-testid="peer-empty">{empty}</p>
  return (
    <div className="dk-table-wrap sw-wrap">
      <table className="dk-table dk-table--compact sw-table">
        <caption className="dk-sr-only">{label}</caption>
        <thead>
          <tr>
            <th scope="col">{t('p2p.peer')}</th>
            <th scope="col">{t('table.state')}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((peer, index) => (
            <tr key={peer.id || index} data-row>
              <td><span className="dk-table-name dk-mono">{peer.id || t('p2p.unnamed', { n: index + 1 })}</span></td>
              <td>
                <span className="sw-state" data-level={peer.isOnline ? 'ok' : 'idle'}>
                  <Icon name={peer.isOnline ? 'check-circle' : 'circle'} />
                  {peer.isOnline ? t('p2p.online') : t('p2p.offline')}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Hardware({ hardware }) {
  const { t } = useTranslation('swarm')
  return (
    <div className="dk-segmented sw-hardware" role="radiogroup" aria-label={t('add.run.hardware')}>
      {hardware.options.map(option => (
        <button key={option.key} type="button" role="radio" aria-checked={hardware.selected === option.key} className="dk-seg" onClick={() => hardware.setSelected(option.key)}>{option.label}</button>
      ))}
    </div>
  )
}

// Peer-to-peer networking: other LocalAI instances and machines join this one
// over a shared network token, with no message bus or database. Two things
// share it: federation (whole requests balanced across instances) and model
// sharding (one model split across machines). This page shows the token and
// who is online; adding a machine is the Add a node flow.
export default function P2P() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const [workers, setWorkers] = useState([])
  const [mlxWorkers, setMlxWorkers] = useState([])
  const [federation, setFederation] = useState([])
  const [stats, setStats] = useState(EMPTY_STATS)
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(false)
  const [token, setToken] = useState('')
  const [tab, setTab] = useState('federation')
  const hardware = useImageSelector('cpu')

  const read = useCallback(async () => {
    try {
      const [workersResult, federationResult, statsResult, tokenResult] = await Promise.allSettled([
        p2pApi.getWorkers(), p2pApi.getFederation(), p2pApi.getStats(), p2pApi.getToken(),
      ])
      let value = ''
      if (tokenResult.status === 'fulfilled') {
        value = (typeof tokenResult.value === 'string' ? tokenResult.value : (tokenResult.value?.token || '')).trim()
      }
      setToken(value)
      setEnabled(!!value)
      if (!value) return
      if (workersResult.status === 'fulfilled') {
        const data = workersResult.value
        // The grouped answer carries llama.cpp and MLX apart; the older one is a flat list.
        if (data?.llama_cpp) {
          setWorkers(data.llama_cpp.nodes || [])
          setMlxWorkers(data.mlx?.nodes || [])
        } else {
          setWorkers(data?.nodes || (Array.isArray(data) ? data : []))
        }
      }
      if (federationResult.status === 'fulfilled') {
        const data = federationResult.value
        setFederation(data?.nodes || (Array.isArray(data) ? data : []))
      }
      if (statsResult.status === 'fulfilled') setStats(statsResult.value)
    } catch {
      setEnabled(false)
    } finally {
      setLoading(false)
    }
  }, [])

  usePolling(read, 3000)

  const copyToken = async () => {
    if (!token) return
    const ok = await copyToClipboard(token)
    addToast(ok ? t('p2p.tokenCopied') : t('command.copyFailed'), ok ? 'success' : 'error', 2000)
  }

  if (loading) return <div className="page page--wide sw-loading"><LoadingSpinner size="lg" /></div>

  if (!enabled) {
    return (
      <div className="page page--wide sw-page sw-page--narrow" data-testid="p2p-off">
        <header className="sw-head">
          <h1 className="sw-title">{t('p2p.offTitle')}</h1>
          <p className="sw-note">{t('p2p.offSub')}</p>
        </header>
        <dl className="sw-policy">
          {['federation', 'sharding', 'sharing'].map(key => (
            <div key={key} className="sw-policy__row">
              <dt><h2 className="sw-dt">{t(`p2p.features.${key}.title`)}</h2></dt>
              <dd>{t(`p2p.features.${key}.text`)}</dd>
            </div>
          ))}
        </dl>
        <section className="sw-section" aria-labelledby="sw-p2p-enable">
          <h2 className="sw-h2" id="sw-p2p-enable">{t('p2p.enableTitle')}</h2>
          <Hardware hardware={hardware} />
          <p className="sw-note">{t('p2p.enableStart')}</p>
          <CommandBlock
            command={`docker run -ti --net host${hardware.option.dockerFlags ? ` ${hardware.option.dockerFlags}` : ''} \\\n  --name local-ai \\\n  localai/localai:${hardware.dev ? hardware.option.devTag : hardware.option.tag} run --p2p`}
            label={t('p2p.enableTitle')}
            addToast={addToast}
          />
          <p className="sw-note sw-note--quiet">{t('p2p.enableToken')}</p>
          <CommandBlock
            command={`docker run -ti --net host${hardware.option.dockerFlags ? ` ${hardware.option.dockerFlags}` : ''} \\\n  -e TOKEN="your-token-here" \\\n  --name local-ai \\\n  localai/localai:${hardware.dev ? hardware.option.devTag : hardware.option.tag} run --p2p`}
            label={t('p2p.enableToken')}
            addToast={addToast}
          />
          <p className="sw-note sw-note--quiet">{t('p2p.enableThen')}</p>
          <p className="sw-note sw-note--quiet">
            <a className="dk-link" href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer">{t('p2p.docs')}</a>
          </p>
        </section>
      </div>
    )
  }

  const fed = stats.federated || { online: 0, total: 0 }
  const llama = stats.llama_cpp_workers || { online: 0, total: 0 }
  const mlx = stats.mlx_workers || { online: 0, total: 0 }

  return (
    <div className="page page--wide sw-page" data-testid="p2p-on">
      <h1 className="dk-sr-only">{t('p2p.title')}</h1>
      <div className="sw-bar sw-bar--lede">
        <p className="sw-note">{t('p2p.lede')}</p>
        <div className="sw-bar__acts">
          <Link className="dk-btn dk-btn--primary" to={`/app/nodes/add?join=${tab === 'federation' ? 'peer' : 'shard'}`}><Icon name="plus" /> {tab === 'federation' ? t('p2p.addPeer') : t('p2p.addShard')}</Link>
        </div>
      </div>

      <section className="sw-section" aria-labelledby="sw-p2p-token">
        <h2 className="sw-h2" id="sw-p2p-token">{t('p2p.token')}</h2>
        <div className="sw-cmd" data-testid="p2p-token">
          <pre className="sw-cmd__text" tabIndex={0} aria-label={t('p2p.token')}><code>{token}</code></pre>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm sw-cmd__copy" onClick={copyToken} aria-label={t('p2p.copyToken')}><Icon name="copy" /> {t('command.copy')}</button>
        </div>
        <p className="sw-note sw-note--quiet">{t('p2p.tokenNote')}</p>
      </section>

      <div className="dk-tabs sw-tabs" role="tablist" aria-label={t('p2p.sections')}>
        <button type="button" role="tab" id="sw-p2p-tab-federation" aria-controls="sw-p2p-federation" aria-selected={tab === 'federation'} tabIndex={tab === 'federation' ? 0 : -1} className="dk-tab" onClick={() => setTab('federation')}>
          {t('p2p.federation')} <span className="sw-tab__n">{t('p2p.count', { online: fed.online, total: fed.total })}</span>
        </button>
        <button type="button" role="tab" id="sw-p2p-tab-sharding" aria-controls="sw-p2p-sharding" aria-selected={tab === 'sharding'} tabIndex={tab === 'sharding' ? 0 : -1} className="dk-tab" onClick={() => setTab('sharding')}>
          {t('p2p.sharding')} <span className="sw-tab__n">{t('p2p.count', { online: llama.online + mlx.online, total: llama.total + mlx.total })}</span>
        </button>
      </div>

      {tab === 'federation' && (
        <div className="dk-tabpanel sw-panel" role="tabpanel" id="sw-p2p-federation" aria-labelledby="sw-p2p-tab-federation" tabIndex={0}>
          <p className="sw-note">{t('p2p.federationSub')}</p>
          <h2 className="sw-h2">{t('p2p.instances')} <span className="sw-count dk-mono">{t('p2p.count', { online: fed.online, total: fed.total })}</span></h2>
          <PeerTable rows={federation} label={t('p2p.instances')} empty={t('p2p.noInstances')} />
          <h2 className="sw-h2">{t('p2p.serverTitle')}</h2>
          <p className="sw-note">{t('p2p.serverSub')}</p>
          <Hardware hardware={hardware} />
          <CommandBlock command={p2pCommand({ method: 'server', option: hardware.option, dev: hardware.dev, token, flavor: 'docker' })} label={t('p2p.serverTitle')} addToast={addToast} />
          <p className="sw-note sw-note--quiet">{t('p2p.port')}</p>
        </div>
      )}

      {tab === 'sharding' && (
        <div className="dk-tabpanel sw-panel" role="tabpanel" id="sw-p2p-sharding" aria-labelledby="sw-p2p-tab-sharding" tabIndex={0}>
          <p className="sw-note">{t('p2p.shardingSub')}</p>
          <h2 className="sw-h2">{t('p2p.llama')} <span className="sw-count dk-mono">{t('p2p.count', { online: llama.online, total: llama.total })}</span></h2>
          <p className="sw-note sw-note--quiet">{t('p2p.llamaSub')}</p>
          <PeerTable rows={workers} label={t('p2p.llama')} empty={t('p2p.noLlama')} />
          <h2 className="sw-h2">{t('p2p.mlx')} <span className="sw-count dk-mono">{t('p2p.count', { online: mlx.online, total: mlx.total })}</span></h2>
          <p className="sw-note sw-note--quiet">{t('p2p.mlxSub')} <a className="dk-link" href="https://localai.io/features/mlx-distributed/" target="_blank" rel="noopener noreferrer">{t('p2p.mlxDocs')}</a></p>
          <PeerTable rows={mlxWorkers} label={t('p2p.mlx')} empty={t('p2p.noMlx')} />
          <p className="sw-note">{t('p2p.shardCommands')} <Link className="dk-link" to="/app/nodes/add?join=shard">{t('p2p.addShard')}</Link></p>
        </div>
      )}
    </div>
  )
}
