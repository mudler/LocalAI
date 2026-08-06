import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { p2pApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import ImageSelector, { useImageSelector, dockerImage, dockerFlags } from '../components/ImageSelector'

function NodeCard({ node, label, tone }) {
  return (
    <div className={`p2p-node ${tone}${node.isOnline ? ' p2p-node--online' : ''}`}>
      <div className="hstack hstack--between hstack--nowrap">
        <div className="hstack hstack--nowrap">
          <span className="icon-chip"><i className="fas fa-server" /></span>
          <div className="flex-1-min">
            <h4 className="p2p-node__name">{label}</h4>
            <p className="p2p-node__id">{node.id}</p>
          </div>
        </div>
        <span className="p2p-node__state">
          <i className="fas fa-circle" />
          {node.isOnline ? 'Online' : 'Offline'}
        </span>
      </div>
      <div className="p2p-node__foot">
        <i className="fas fa-clock" />
        <span>Updated: {new Date().toLocaleTimeString()}</span>
      </div>
    </div>
  )
}

function CommandBlock({ command, addToast }) {
  const copy = () => {
    navigator.clipboard.writeText(command)
    addToast('Copied to clipboard', 'success', 2000)
  }
  return (
    <div className="p2p-cmd">
      <pre>{command}</pre>
      <button className="btn btn-sm p2p-cmd__copy" onClick={copy} title="Copy" aria-label="Copy command">
        <i className="fas fa-copy" />
      </button>
    </div>
  )
}

function StepNumber({ n, tone }) {
  return <span className={`p2p-step ${tone}`}>{n}</span>
}

/* Live "online / total" readout, repeated once per architecture. */
function NodeCount({ online, total }) {
  return (
    <div className="p2p-count">
      <span className={`p2p-count__online${online > 0 ? ' p2p-count__online--up' : ''}`}>{online}</span>
      <span className="p2p-count__total">/{total}</span>
    </div>
  )
}

function NoNodes({ icon, title, hint }) {
  return (
    <div className="p2p-empty">
      <i className={icon} />
      <p>{title}</p>
      <p>{hint}</p>
    </div>
  )
}

export default function P2P() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [workers, setWorkers] = useState([])
  const [mlxWorkers, setMlxWorkers] = useState([])
  const [federation, setFederation] = useState([])
  const [stats, setStats] = useState({ llama_cpp_workers: { online: 0, total: 0 }, federated: { online: 0, total: 0 }, mlx_workers: { online: 0, total: 0 } })
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(false)
  const [token, setToken] = useState('')
  const [activeTab, setActiveTab] = useState('federation')
  const imgSelector = useImageSelector('cpu')

  const fetchData = useCallback(async () => {
    try {
      const [wRes, fRes, sRes, tRes] = await Promise.allSettled([
        p2pApi.getWorkers(),
        p2pApi.getFederation(),
        p2pApi.getStats(),
        p2pApi.getToken(),
      ])

      let p2pToken = ''
      if (tRes.status === 'fulfilled') {
        p2pToken = (typeof tRes.value === 'string' ? tRes.value : (tRes.value?.token || '')).trim()
      }
      setToken(p2pToken)
      setEnabled(!!p2pToken)

      if (p2pToken) {
        if (wRes.status === 'fulfilled') {
          const data = wRes.value
          // Handle both old format ({nodes: [...]}) and new grouped format ({llama_cpp: {nodes: [...]}, mlx: {nodes: [...]}})
          if (data?.llama_cpp) {
            setWorkers(data.llama_cpp.nodes || [])
            setMlxWorkers(data.mlx?.nodes || [])
          } else {
            setWorkers(data?.nodes || (Array.isArray(data) ? data : []))
          }
        }
        if (fRes.status === 'fulfilled') {
          const data = fRes.value
          setFederation(data?.nodes || (Array.isArray(data) ? data : []))
        }
        if (sRes.status === 'fulfilled') {
          setStats(sRes.value)
        }
      }
    } catch {
      setEnabled(false)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 3000)
    return () => clearInterval(interval)
  }, [fetchData])

  const copyToken = () => {
    if (token) {
      navigator.clipboard.writeText(token)
      addToast('Token copied to clipboard', 'success', 2000)
    }
  }

  const runCmd = (suffix, name = 'local-ai') =>
    `docker run -ti --net host ${dockerFlags(imgSelector.option) ? dockerFlags(imgSelector.option) + ' ' : ''}\\\n  --name ${name} \\\n  ${dockerImage(imgSelector.option, imgSelector.dev)} ${suffix}`

  const tokenCmd = (suffix, name) =>
    `docker run -ti --net host ${dockerFlags(imgSelector.option) ? dockerFlags(imgSelector.option) + ' ' : ''}\\\n  -e TOKEN="${token}" \\\n  --name ${name} \\\n  ${dockerImage(imgSelector.option, imgSelector.dev)} ${suffix}`

  if (loading) {
    return (
      <div className="page page--narrow loading-center">
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  // ── P2P Disabled ──
  if (!enabled) {
    return (
      <div className="page page--narrow">
        <div className="p2p-hero">
          <i className="fas fa-network-wired" />
          <h1>P2P distribution not enabled</h1>
          <p>
            Enable peer-to-peer distribution to scale your AI workloads across multiple devices. Share instances,
            shard models, and pool computational resources across your network.
          </p>

          <div className="p2p-features">
            <div className="card p2p-feature tone-primary">
              <span className="icon-chip"><i className="fas fa-network-wired" /></span>
              <h3>Instance federation</h3>
              <p>Load balance across multiple instances</p>
            </div>
            <div className="card p2p-feature tone-accent">
              <span className="icon-chip"><i className="fas fa-puzzle-piece" /></span>
              <h3>Model sharding</h3>
              <p>Split large models across workers</p>
            </div>
            <div className="card p2p-feature tone-success">
              <span className="icon-chip"><i className="fas fa-share-alt" /></span>
              <h3>Resource sharing</h3>
              <p>Pool resources from multiple devices</p>
            </div>
          </div>
        </div>

        <div className="card p2p-enable">
          <h3 className="hstack tone-accent">
            <i className="fas fa-rocket tone-icon" />
            How to enable P2P
          </h3>
          <div>
            <p className="form-label">Select your hardware</p>
            <ImageSelector selected={imgSelector.selected} onSelect={imgSelector.setSelected} dev={imgSelector.dev} onDevChange={imgSelector.setDev} />
          </div>

          <div className="stack">
            <div className="hstack hstack--top hstack--nowrap tone-accent">
              <StepNumber n={1} tone="tone-accent" />
              <div className="flex-1 stack stack--sm">
                <p className="form-label">Start LocalAI with P2P enabled</p>
                <CommandBlock command={runCmd('run --p2p')} addToast={addToast} />
                <p className="text-note">This will automatically generate a network token for you.</p>
              </div>
            </div>
            <div className="hstack hstack--top hstack--nowrap tone-accent">
              <StepNumber n={2} tone="tone-accent" />
              <div className="flex-1 stack stack--sm">
                <p className="form-label">Or use an existing token</p>
                <CommandBlock
                  command={`docker run -ti --net host ${dockerFlags(imgSelector.option) ? dockerFlags(imgSelector.option) + ' ' : ''}\\\n  -e TOKEN="your-token-here" \\\n  --name local-ai \\\n  ${dockerImage(imgSelector.option, imgSelector.dev)} run --p2p`}
                  addToast={addToast}
                />
                <p className="text-note">If you already have a token from another instance, you can reuse it.</p>
              </div>
            </div>
            <div className="hstack hstack--top hstack--nowrap tone-accent">
              <StepNumber n={3} tone="tone-accent" />
              <div className="flex-1 stack stack--sm">
                <p className="form-label">Access the P2P dashboard</p>
                <p className="text-note">Once enabled, refresh this page to see your network token and start connecting nodes.</p>
              </div>
            </div>
          </div>
        </div>

        <div className="hstack hstack--md hstack--center">
          <a className="btn btn-primary" href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-book" /> Documentation <i className="fas fa-external-link-alt text-meta" />
          </a>
          <a className="btn btn-secondary" href="https://localai.io/basics/getting_started/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-graduation-cap" /> Getting started <i className="fas fa-external-link-alt text-meta" />
          </a>
        </div>
      </div>
    )
  }

  // ── P2P Enabled ──
  const fedOnline = stats.federated?.online ?? 0
  const fedTotal = stats.federated?.total ?? 0
  const llamaOnline = stats.llama_cpp_workers?.online ?? 0
  const llamaTotal = stats.llama_cpp_workers?.total ?? 0
  const mlxOnline = stats.mlx_workers?.online ?? 0
  const mlxTotal = stats.mlx_workers?.total ?? 0

  return (
    <div className="page page--narrow">
      <PageHeader
        title={<><i className="fas fa-circle-nodes fa-circle-info" /> {t('p2p.title')}</>}
        supporting={
          <>
            {t('p2p.subtitle')}
            {' '}
            <a href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-up-right-from-square" aria-hidden="true" />
            </a>
          </>
        }
      />

      <section className="p2p-token">
        <div className="hstack hstack--nowrap tone-warning">
          <i className="fas fa-key tone-icon" />
          <h3 className="flex-1-min">Network token</h3>
          <button className="btn btn-secondary btn-sm" onClick={copyToken} title="Copy token" aria-label="Copy token">
            <i className="fas fa-copy" />
          </button>
        </div>
        <pre className="p2p-token__value" onClick={copyToken}>{token || 'Loading...'}</pre>
        <p className="text-note">
          All nodes (federated servers, instances, and workers) use the same token to join the network.
          Pass it via the <code>TOKEN</code> environment variable.
        </p>
      </section>

      <div className="p2p-tabs" role="tablist">
        <button
          role="tab"
          aria-selected={activeTab === 'federation'}
          onClick={() => setActiveTab('federation')}
          className={`p2p-tab tone-primary${activeTab === 'federation' ? ' p2p-tab--on' : ''}`}
        >
          <span className="icon-chip"><i className="fas fa-circle-nodes" /></span>
          <span className="p2p-tab__text">
            <span className="p2p-tab__label">Federation</span>
            <span className="p2p-tab__count">{fedOnline}/{fedTotal} instances</span>
          </span>
        </button>
        <button
          role="tab"
          aria-selected={activeTab === 'sharding'}
          onClick={() => setActiveTab('sharding')}
          className={`p2p-tab tone-accent${activeTab === 'sharding' ? ' p2p-tab--on' : ''}`}
        >
          <span className="icon-chip"><i className="fas fa-puzzle-piece" /></span>
          <span className="p2p-tab__text">
            <span className="p2p-tab__label">Model sharding</span>
            <span className="p2p-tab__count">{llamaOnline + mlxOnline}/{llamaTotal + mlxTotal} workers</span>
          </span>
        </button>
      </div>

      {/* ── Federation Tab ── */}
      {activeTab === 'federation' && (
        <div className="p2p-panel">
          <div className="p2p-section">
            <div className="p2p-diagram">
              <div className="p2p-diagram__row">
                <div className="p2p-diagram__item tone-warning">
                  <span className="icon-chip icon-chip--lg"><i className="fas fa-user" /></span>
                  <span className="p2p-diagram__label">API client</span>
                </div>
                <i className="fas fa-arrow-right p2p-diagram__arrow" />
                <div className="p2p-diagram__item tone-success">
                  <span className="icon-chip icon-chip--lg icon-chip--ring"><i className="fas fa-scale-balanced" /></span>
                  <span className="p2p-diagram__label">Federated server</span>
                  <span className="p2p-diagram__sub">Load balancer</span>
                </div>
                <i className="fas fa-arrow-right p2p-diagram__arrow" />
                <div className="p2p-diagram__item tone-primary">
                  <span className="p2p-diagram__group">
                    {[1, 2, 3].map(n => (
                      <span key={n} className="icon-chip icon-chip--sm"><i className="fas fa-server" /></span>
                    ))}
                  </span>
                  <span className="p2p-diagram__label">Federated instances</span>
                  <span className="p2p-diagram__sub">Workers</span>
                </div>
              </div>
              <p className="p2p-diagram__note">
                The <strong>federated server</strong> acts as a load balancer: it receives API requests and
                distributes them across <strong>federated instances</strong> (workers running your models).
              </p>
            </div>

            <div className="hstack hstack--between">
              <h3>Connected instances</h3>
              <NodeCount online={fedOnline} total={fedTotal} />
            </div>

            {federation.length === 0 ? (
              <NoNodes icon="fas fa-server" title="No federated instances connected" hint="Follow the setup steps below" />
            ) : (
              <div className="p2p-nodes">
                {federation.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label="Instance" tone="tone-primary" />
                ))}
              </div>
            )}
          </div>

          <div className="p2p-section">
            <h3 className="hstack tone-primary">
              <i className="fas fa-book tone-icon" />
              Setup guide
            </h3>

            <div className="p2p-setup">
              <p className="form-label">Select your hardware</p>
              <ImageSelector selected={imgSelector.selected} onSelect={imgSelector.setSelected} dev={imgSelector.dev} onDevChange={imgSelector.setDev} />

              <div className="hstack tone-success">
                <StepNumber n={1} tone="tone-success" />
                <h4 className="p2p-setup__title">
                  Start the federated server <span className="text-note">(load balancer)</span>
                </h4>
              </div>
              <p className="text-note">
                This is the entry point for your API clients. It receives requests and distributes them to
                federated instances.
              </p>
              <CommandBlock command={tokenCmd('federated', 'local-ai-federated')} addToast={addToast} />
              <p className="text-meta">
                Listens on port <code>8080</code> by default. To change it, add <code>-e ADDRESS=:9090</code>.
              </p>

              <div className="hstack tone-primary push-lg">
                <StepNumber n={2} tone="tone-primary" />
                <h4 className="p2p-setup__title">
                  Start federated instances <span className="text-note">(workers)</span>
                </h4>
              </div>
              <p className="text-note">
                Run this on each machine you want to add as a worker. Each instance runs your models and receives
                tasks from the federated server.
              </p>
              <CommandBlock command={tokenCmd('run --federated --p2p', 'local-ai')} addToast={addToast} />
              <p className="text-meta">
                Listens on port <code>8080</code> by default. To change it, add <code>-e ADDRESS=:9090</code>.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* ── Model Sharding Tab ── */}
      {activeTab === 'sharding' && (
        <div className="p2p-panel">
          <div className="p2p-section">
            <p className="p2p-note">
              <i className="fas fa-info-circle" />
              <strong>Different from federation:</strong> federation distributes whole requests across instances.
              Model sharding splits a single model across machines for joint inference.
            </p>

            <h3 className="hstack tone-accent">
              <i className="fas fa-microchip tone-icon" />
              llama.cpp RPC workers
            </h3>

            <div className="p2p-diagram">
              <div className="p2p-diagram__row">
                <div className="p2p-diagram__item tone-primary">
                  <span className="icon-chip icon-chip--lg icon-chip--ring"><i className="fas fa-server" /></span>
                  <span className="p2p-diagram__label">LocalAI instance</span>
                  <span className="p2p-diagram__sub">Orchestrator</span>
                </div>
                <span className="p2p-diagram__arrow">
                  <i className="fas fa-arrow-right" />
                  <span>RPC</span>
                </span>
                <div className="p2p-diagram__item tone-accent">
                  <span className="p2p-diagram__group">
                    {['Layer 1-10', 'Layer 11-20', 'Layer 21-30'].map(label => (
                      <span key={label}>
                        <span className="p2p-diagram__shard"><i className="fas fa-microchip" /></span>
                        <span className="p2p-diagram__shard-label">{label}</span>
                      </span>
                    ))}
                  </span>
                  <span className="p2p-diagram__label">RPC workers</span>
                  <span className="p2p-diagram__sub">Distributed memory</span>
                </div>
              </div>
              <p className="p2p-diagram__note">
                Model weights are <strong>split across RPC workers</strong>. Each worker holds a portion of the
                model layers in its memory (GPU or CPU).
              </p>
            </div>

            <div className="hstack hstack--between">
              <h4>Connected workers</h4>
              <NodeCount online={llamaOnline} total={llamaTotal} />
            </div>

            {workers.length === 0 ? (
              <NoNodes icon="fas fa-puzzle-piece" title="No llama.cpp workers connected" hint="Start workers to see them here" />
            ) : (
              <div className="p2p-nodes">
                {workers.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label="Worker" tone="tone-accent" />
                ))}
              </div>
            )}
          </div>

          <div className="p2p-section">
            <h3 className="hstack tone-warning">
              <i className="fas fa-apple-whole tone-icon" />
              MLX distributed workers
            </h3>

            <div className="p2p-diagram">
              <div className="p2p-diagram__row">
                <div className="p2p-diagram__item tone-primary">
                  <span className="icon-chip icon-chip--lg icon-chip--ring"><i className="fas fa-server" /></span>
                  <span className="p2p-diagram__label">LocalAI</span>
                  <span className="p2p-diagram__sub">Rank 0</span>
                </div>
                <span className="p2p-diagram__arrow">
                  <i className="fas fa-arrows-left-right" />
                  <span>Ring / JACCL</span>
                </span>
                <div className="p2p-diagram__item tone-warning">
                  <span className="p2p-diagram__group">
                    {['Layers 1-16', 'Layers 17-32'].map(label => (
                      <span key={label}>
                        <span className="p2p-diagram__shard p2p-diagram__shard--wide"><i className="fas fa-microchip" /></span>
                        <span className="p2p-diagram__shard-label">{label}</span>
                      </span>
                    ))}
                  </span>
                  <span className="p2p-diagram__label">MLX workers</span>
                  <span className="p2p-diagram__sub">Pipeline parallel</span>
                </div>
              </div>
              <p className="p2p-diagram__note">
                MLX distributed uses <strong>native Apple Silicon communication</strong>. All nodes execute model
                code simultaneously via pipeline or tensor parallelism.
              </p>
            </div>

            <div className="hstack hstack--between">
              <h4>Connected MLX workers</h4>
              <NodeCount online={mlxOnline} total={mlxTotal} />
            </div>

            {mlxWorkers.length === 0 ? (
              <NoNodes icon="fas fa-apple-whole" title="No MLX workers connected" hint="Start MLX workers on Apple Silicon Macs" />
            ) : (
              <div className="p2p-nodes">
                {mlxWorkers.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label={`MLX rank ${i + 1}`} tone="tone-warning" />
                ))}
              </div>
            )}
          </div>

          <div className="p2p-section">
            <h3 className="hstack tone-accent">
              <i className="fas fa-book tone-icon" />
              Setup workers
            </h3>

            <div className="p2p-setup">
              <h4 className="p2p-setup__title">llama.cpp RPC worker</h4>
              <p className="text-note">
                Each worker exposes its GPU/CPU memory as a shard for distributed model inference.
              </p>
              <p className="form-label">Select your hardware</p>
              <ImageSelector selected={imgSelector.selected} onSelect={imgSelector.setSelected} dev={imgSelector.dev} onDevChange={imgSelector.setDev} />
              <CommandBlock command={tokenCmd('worker p2p-llama-cpp-rpc', 'local-ai-worker')} addToast={addToast} />
            </div>

            <div className="p2p-setup p2p-setup--warning">
              <h4 className="p2p-setup__title">MLX distributed worker</h4>
              <p className="text-note">
                Run on Apple Silicon Macs to participate in distributed MLX inference via pipeline parallelism.
              </p>
              <CommandBlock
                command={`docker run -ti --net host \\\n  -e TOKEN="${token}" \\\n  --name local-ai-mlx-worker \\\n  localai/localai:latest-metal-darwin-arm64 worker p2p-mlx`}
                addToast={addToast}
              />
              <p className="text-meta">
                For more information, see the{' '}
                <a href="https://localai.io/features/mlx-distributed/" target="_blank" rel="noopener noreferrer">
                  MLX distributed
                </a> docs.
              </p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
