import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { p2pApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

function NodeCard({ node, label, iconColor, iconBg }) {
  return (
    <div style={{
      background: 'var(--color-bg-primary)',
      border: `1px solid ${node.isOnline ? 'var(--color-success-border)' : 'var(--color-error-border)'}`,
      borderRadius: 'var(--radius-md)',
      padding: 'var(--spacing-md)',
      transition: 'border-color 200ms',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <div style={{
            width: 40, height: 40, borderRadius: 'var(--radius-md)',
            background: iconBg, display: 'flex', alignItems: 'center', justifyContent: 'center',
            marginRight: 'var(--spacing-sm)',
          }}>
            <i className="fas fa-server" style={{ color: iconColor, fontSize: '1rem' }} />
          </div>
          <div>
            <h4 style={{ fontSize: '0.875rem', fontWeight: 600 }}>{label}</h4>
            <p style={{ fontSize: '0.75rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-secondary)', wordBreak: 'break-all' }}>
              {node.id}
            </p>
          </div>
        </div>
        <div style={{
          display: 'flex', alignItems: 'center', gap: '6px',
          background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)',
          padding: '4px 10px', border: '1px solid var(--color-border-subtle)',
        }}>
          <i className="fas fa-circle" style={{
            fontSize: '0.5rem',
            color: node.isOnline ? 'var(--color-success)' : 'var(--color-error)',
          }} />
          <span style={{
            fontSize: '0.75rem', fontWeight: 500,
            color: node.isOnline ? 'var(--color-success)' : 'var(--color-error)',
          }}>
            {node.isOnline ? 'Online' : 'Offline'}
          </span>
        </div>
      </div>
      <div style={{
        fontSize: '0.75rem', color: 'var(--color-text-muted)',
        paddingTop: 'var(--spacing-sm)', borderTop: '1px solid var(--color-border-subtle)',
        display: 'flex', alignItems: 'center', gap: '6px',
      }}>
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
    <div style={{ position: 'relative' }}>
      <pre style={{
        background: 'var(--color-bg-primary)', padding: 'var(--spacing-md)',
        paddingRight: 'var(--spacing-xl)', borderRadius: 'var(--radius-md)',
        fontSize: '0.8125rem', fontFamily: "'JetBrains Mono', monospace",
        whiteSpace: 'pre-wrap', wordBreak: 'break-all',
        color: 'var(--color-warning)', overflow: 'auto',
        border: '1px solid var(--color-border-subtle)',
      }}>
        {command}
      </pre>
      <button
        onClick={copy}
        style={{
          position: 'absolute', top: 8, right: 8,
          background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border-subtle)',
          borderRadius: 'var(--radius-sm)', padding: '4px 8px', cursor: 'pointer',
          color: 'var(--color-text-secondary)', fontSize: '0.75rem',
        }}
        title="Copy"
      >
        <i className="fas fa-copy" />
      </button>
    </div>
  )
}

function StepNumber({ n, bg, color }) {
  return (
    <span style={{
      width: 28, height: 28, borderRadius: '50%', background: bg,
      color, display: 'flex', alignItems: 'center', justifyContent: 'center',
      fontSize: '0.8125rem', fontWeight: 700, flexShrink: 0,
    }}>{n}</span>
  )
}

export default function P2P() {
  const { addToast } = useOutletContext()
  const [workers, setWorkers] = useState([])
  const [mlxWorkers, setMlxWorkers] = useState([])
  const [federation, setFederation] = useState([])
  const [stats, setStats] = useState({ llama_cpp_workers: { online: 0, total: 0 }, federated: { online: 0, total: 0 }, mlx_workers: { online: 0, total: 0 } })
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(false)
  const [token, setToken] = useState('')
  const [activeTab, setActiveTab] = useState('federation')

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

  if (loading) {
    return (
      <div className="page" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  // ── P2P Disabled ──
  if (!enabled) {
    return (
      <div className="page">
        <div style={{ textAlign: 'center', padding: 'var(--spacing-xl) 0' }}>
          <i className="fas fa-network-wired" style={{ fontSize: '3rem', color: 'var(--color-primary)', marginBottom: 'var(--spacing-md)' }} />
          <h1 style={{ fontSize: '1.5rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            P2P Distribution Not Enabled
          </h1>
          <p style={{ color: 'var(--color-text-secondary)', maxWidth: 600, margin: '0 auto var(--spacing-xl)' }}>
            Enable peer-to-peer distribution to scale your AI workloads across multiple devices. Share instances, shard models, and pool computational resources across your network.
          </p>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-xl)' }}>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-network-wired" style={{ color: 'var(--color-primary)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Instance Federation</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Load balance across multiple instances</p>
            </div>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-accent-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-puzzle-piece" style={{ color: 'var(--color-accent)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Model Sharding</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Split large models across workers</p>
            </div>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-success-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-share-alt" style={{ color: 'var(--color-success)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Resource Sharing</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Pool resources from multiple devices</p>
            </div>
          </div>
        </div>

        {/* How to Enable */}
        <div className="card" style={{ maxWidth: 700, margin: '0 auto var(--spacing-xl)', padding: 'var(--spacing-lg)', textAlign: 'left' }}>
          <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center' }}>
            <i className="fas fa-rocket" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-sm)' }} />
            How to Enable P2P
          </h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={1} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500, marginBottom: 'var(--spacing-xs)' }}>Start LocalAI with P2P enabled</p>
                <CommandBlock
                  command={`docker run -ti --net host --name local-ai \\\n  localai/localai:latest-cpu run --p2p`}
                  addToast={addToast}
                />
                <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', marginTop: 'var(--spacing-xs)' }}>
                  This will automatically generate a network token for you.
                </p>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={2} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500, marginBottom: 'var(--spacing-xs)' }}>Or use an existing token</p>
                <CommandBlock
                  command={`docker run -ti --net host \\\n  -e TOKEN="your-token-here" \\\n  --name local-ai \\\n  localai/localai:latest-cpu run --p2p`}
                  addToast={addToast}
                />
                <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', marginTop: 'var(--spacing-xs)' }}>
                  If you already have a token from another instance, you can reuse it.
                </p>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={3} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500 }}>Access the P2P dashboard</p>
                <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', marginTop: 'var(--spacing-xs)' }}>
                  Once enabled, refresh this page to see your network token and start connecting nodes.
                </p>
              </div>
            </div>
          </div>
        </div>

        <div style={{ textAlign: 'center', display: 'flex', gap: 'var(--spacing-md)', justifyContent: 'center', flexWrap: 'wrap' }}>
          <a className="btn btn-primary" href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-book" /> Documentation <i className="fas fa-external-link-alt" style={{ fontSize: '0.75rem', marginLeft: 4 }} />
          </a>
          <a className="btn btn-secondary" href="https://localai.io/basics/getting_started/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-graduation-cap" /> Getting Started <i className="fas fa-external-link-alt" style={{ fontSize: '0.75rem', marginLeft: 4 }} />
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
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">
          <i className="fas fa-circle-nodes" style={{ marginRight: 'var(--spacing-sm)' }} />
          Distributed AI Computing
        </h1>
        <p className="page-subtitle">
          Scale your AI workloads across multiple devices with peer-to-peer distribution
          {' '}
          <a href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer"
            style={{ color: 'var(--color-primary)' }}>
            <i className="fas fa-circle-info" />
          </a>
        </p>
      </div>

      {/* Network Token */}
      <div style={{
        background: 'var(--color-bg-secondary)', border: '1px solid var(--color-accent-border)',
        borderRadius: 'var(--radius-lg)', padding: 'var(--spacing-lg)', marginBottom: 'var(--spacing-xl)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', marginBottom: 'var(--spacing-md)' }}>
          <i className="fas fa-key" style={{ color: 'var(--color-warning)', fontSize: '1.25rem', marginRight: 'var(--spacing-sm)' }} />
          <h3 style={{ fontSize: '1.125rem', fontWeight: 700, flex: 1 }}>Network Token</h3>
          <button className="btn btn-secondary btn-sm" onClick={copyToken} title="Copy token">
            <i className="fas fa-copy" />
          </button>
        </div>
        <pre
          onClick={copyToken}
          style={{
            background: 'var(--color-bg-primary)', color: 'var(--color-warning)',
            padding: 'var(--spacing-md)', borderRadius: 'var(--radius-md)',
            wordBreak: 'break-all', whiteSpace: 'pre-wrap',
            border: '1px solid var(--color-border-subtle)', cursor: 'pointer',
            fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem',
          }}
        >
          {token || 'Loading...'}
        </pre>
        <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', marginTop: 'var(--spacing-sm)' }}>
          All nodes (federated servers, instances, and workers) use the same token to join the network. Pass it via the <code>TOKEN</code> environment variable.
        </p>
      </div>

      {/* Tab bar */}
      <div style={{
        display: 'flex', borderBottom: '2px solid var(--color-border-subtle)',
        marginBottom: 'var(--spacing-xl)', gap: '2px',
      }}>
        <button
          onClick={() => setActiveTab('federation')}
          style={{
            flex: 1, padding: 'var(--spacing-md)',
            background: activeTab === 'federation' ? 'var(--color-bg-secondary)' : 'transparent',
            border: 'none', cursor: 'pointer',
            borderBottom: activeTab === 'federation' ? '2px solid var(--color-primary)' : '2px solid transparent',
            marginBottom: '-2px',
            borderRadius: 'var(--radius-md) var(--radius-md) 0 0',
            transition: 'all 150ms',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--spacing-sm)' }}>
            <div style={{
              width: 36, height: 36, borderRadius: 'var(--radius-md)',
              background: activeTab === 'federation' ? 'var(--color-primary-light)' : 'var(--color-bg-tertiary)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              <i className="fas fa-circle-nodes" style={{
                color: activeTab === 'federation' ? 'var(--color-primary)' : 'var(--color-text-muted)',
                fontSize: '1rem',
              }} />
            </div>
            <div style={{ textAlign: 'left' }}>
              <div style={{
                fontSize: '0.9375rem', fontWeight: 600,
                color: activeTab === 'federation' ? 'var(--color-text-primary)' : 'var(--color-text-secondary)',
              }}>
                Federation
              </div>
              <div style={{
                fontSize: '0.75rem',
                color: activeTab === 'federation' ? 'var(--color-primary)' : 'var(--color-text-muted)',
              }}>
                {fedOnline}/{fedTotal} instances
              </div>
            </div>
          </div>
        </button>
        <button
          onClick={() => setActiveTab('sharding')}
          style={{
            flex: 1, padding: 'var(--spacing-md)',
            background: activeTab === 'sharding' ? 'var(--color-bg-secondary)' : 'transparent',
            border: 'none', cursor: 'pointer',
            borderBottom: activeTab === 'sharding' ? '2px solid var(--color-accent)' : '2px solid transparent',
            marginBottom: '-2px',
            borderRadius: 'var(--radius-md) var(--radius-md) 0 0',
            transition: 'all 150ms',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--spacing-sm)' }}>
            <div style={{
              width: 36, height: 36, borderRadius: 'var(--radius-md)',
              background: activeTab === 'sharding' ? 'var(--color-accent-light)' : 'var(--color-bg-tertiary)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              <i className="fas fa-puzzle-piece" style={{
                color: activeTab === 'sharding' ? 'var(--color-accent)' : 'var(--color-text-muted)',
                fontSize: '1rem',
              }} />
            </div>
            <div style={{ textAlign: 'left' }}>
              <div style={{
                fontSize: '0.9375rem', fontWeight: 600,
                color: activeTab === 'sharding' ? 'var(--color-text-primary)' : 'var(--color-text-secondary)',
              }}>
                Model Sharding
              </div>
              <div style={{
                fontSize: '0.75rem',
                color: activeTab === 'sharding' ? 'var(--color-accent)' : 'var(--color-text-muted)',
              }}>
                {llamaOnline + mlxOnline}/{llamaTotal + mlxTotal} workers
              </div>
            </div>
          </div>
        </button>
      </div>

      {/* ── Federation Tab ── */}
      {activeTab === 'federation' && (
        <div style={{
          background: 'var(--color-bg-secondary)', border: '1px solid var(--color-accent-border)',
          borderRadius: 'var(--radius-lg)', overflow: 'hidden',
        }}>
          <div style={{ padding: 'var(--spacing-lg)', borderBottom: '1px solid var(--color-border-subtle)' }}>
            {/* Architecture diagram */}
            <div style={{
              background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
              borderRadius: 'var(--radius-lg)', padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--spacing-md)', flexWrap: 'wrap' }}>
                <div style={{ textAlign: 'center' }}>
                  <div style={{
                    width: 48, height: 48, borderRadius: 'var(--radius-md)',
                    background: 'var(--color-warning-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                    margin: '0 auto var(--spacing-xs)',
                  }}>
                    <i className="fas fa-user" style={{ color: 'var(--color-warning)', fontSize: '1rem' }} />
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>API Client</div>
                </div>
                <i className="fas fa-arrow-right" style={{ color: 'var(--color-text-muted)', fontSize: '1rem' }} />
                <div style={{ textAlign: 'center' }}>
                  <div style={{
                    width: 48, height: 48, borderRadius: 'var(--radius-md)',
                    background: 'var(--color-success-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                    margin: '0 auto var(--spacing-xs)', border: '2px solid var(--color-success)',
                  }}>
                    <i className="fas fa-scale-balanced" style={{ color: 'var(--color-success)', fontSize: '1rem' }} />
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>Federated Server</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Load balancer</div>
                </div>
                <i className="fas fa-arrow-right" style={{ color: 'var(--color-text-muted)', fontSize: '1rem' }} />
                <div style={{ textAlign: 'center' }}>
                  <div style={{ display: 'flex', gap: '4px', marginBottom: 'var(--spacing-xs)' }}>
                    {[1, 2, 3].map(n => (
                      <div key={n} style={{
                        width: 36, height: 36, borderRadius: 'var(--radius-sm)',
                        background: 'var(--color-primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                      }}>
                        <i className="fas fa-server" style={{ color: 'var(--color-primary)', fontSize: '0.75rem' }} />
                      </div>
                    ))}
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>Federated Instances</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Workers</div>
                </div>
              </div>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', textAlign: 'center', marginTop: 'var(--spacing-sm)', lineHeight: 1.5 }}>
                The <strong>Federated Server</strong> acts as a load balancer — it receives API requests and distributes them across <strong>Federated Instances</strong> (workers running your models).
              </p>
            </div>

            {/* Status + nodes */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
              <h3 style={{ fontSize: '1rem', fontWeight: 700 }}>Connected Instances</h3>
              <div style={{ fontSize: '1.25rem', fontWeight: 700 }}>
                <span style={{ color: fedOnline > 0 ? 'var(--color-success)' : 'var(--color-error)' }}>{fedOnline}</span>
                <span style={{ color: 'var(--color-text-secondary)', fontSize: '1rem' }}>/{fedTotal}</span>
              </div>
            </div>

            {federation.length === 0 ? (
              <div style={{
                textAlign: 'center', padding: 'var(--spacing-lg)',
                background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
                borderRadius: 'var(--radius-lg)',
              }}>
                <i className="fas fa-server" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }} />
                <p style={{ fontWeight: 500, color: 'var(--color-text-secondary)' }}>No federated instances connected</p>
                <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>Follow the setup steps below</p>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 'var(--spacing-md)' }}>
                {federation.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label="Instance" iconColor="var(--color-primary)" iconBg="var(--color-primary-light)" />
                ))}
              </div>
            )}
          </div>

          {/* Setup Guide */}
          <div style={{ padding: 'var(--spacing-lg)' }}>
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-book" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-sm)' }} />
              Setup Guide
            </h3>

            <div style={{
              background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-lg)',
              border: '1px solid var(--color-border-subtle)', padding: 'var(--spacing-lg)',
            }}>
              {/* Step 1 */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)' }}>
                <StepNumber n={1} bg="var(--color-success-light)" color="var(--color-success)" />
                <h4 style={{ fontSize: '1rem', fontWeight: 700 }}>
                  Start the Federated Server <span style={{ fontSize: '0.8125rem', fontWeight: 400, color: 'var(--color-text-muted)' }}>(load balancer)</span>
                </h4>
              </div>
              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-sm)' }}>
                This is the entry point for your API clients. It receives requests and distributes them to federated instances.
              </p>
              <CommandBlock
                command={`docker run -ti --net host \\\n  -e TOKEN="${token}" \\\n  --name local-ai-federated \\\n  localai/localai:latest-cpu federated`}
                addToast={addToast}
              />
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem', marginTop: 'var(--spacing-sm)' }}>
                Listens on port <code>8080</code> by default. To change it, add <code>-e ADDRESS=:9090</code>.
              </p>

              {/* Step 2 */}
              <div style={{
                display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
                marginTop: 'var(--spacing-xl)', marginBottom: 'var(--spacing-sm)',
              }}>
                <StepNumber n={2} bg="var(--color-primary-light)" color="var(--color-primary)" />
                <h4 style={{ fontSize: '1rem', fontWeight: 700 }}>
                  Start Federated Instances <span style={{ fontSize: '0.8125rem', fontWeight: 400, color: 'var(--color-text-muted)' }}>(workers)</span>
                </h4>
              </div>
              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-sm)' }}>
                Run this on each machine you want to add as a worker. Each instance runs your models and receives tasks from the federated server.
              </p>
              <CommandBlock
                command={`docker run -ti --net host \\\n  -e TOKEN="${token}" \\\n  --name local-ai \\\n  localai/localai:latest-cpu run --federated --p2p`}
                addToast={addToast}
              />
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem', marginTop: 'var(--spacing-sm)' }}>
                Listens on port <code>8080</code> by default. To change it, add <code>-e ADDRESS=:9090</code>.
              </p>

              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginTop: 'var(--spacing-lg)' }}>
                For GPU images and all available options, see the{' '}
                <a href="https://localai.io/basics/container/" target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)' }}>Container images</a>
                {' '}and{' '}
                <a href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)' }}>Distribution</a> docs.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* ── Model Sharding Tab ── */}
      {activeTab === 'sharding' && (
        <div style={{
          background: 'var(--color-bg-secondary)', border: '1px solid var(--color-accent-border)',
          borderRadius: 'var(--radius-lg)', overflow: 'hidden',
        }}>
          <div style={{ padding: 'var(--spacing-lg)', borderBottom: '1px solid var(--color-border-subtle)' }}>
            <div style={{
              background: 'var(--color-accent-light)', border: '1px solid var(--color-accent-border)',
              borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm) var(--spacing-md)',
              fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)',
            }}>
              <i className="fas fa-info-circle" style={{ color: 'var(--color-accent)', marginRight: 6 }} />
              <strong>Different from federation:</strong> Federation distributes whole requests across instances. Model sharding splits a single model across machines for joint inference.
            </div>

            {/* ── llama.cpp RPC Workers Section ── */}
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-sm)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <i className="fas fa-microchip" style={{ color: 'var(--color-accent)' }} />
              llama.cpp RPC Workers
            </h3>

            {/* Architecture diagram */}
            <div style={{
              background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
              borderRadius: 'var(--radius-lg)', padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--spacing-md)', flexWrap: 'wrap' }}>
                <div style={{ textAlign: 'center' }}>
                  <div style={{
                    width: 48, height: 48, borderRadius: 'var(--radius-md)',
                    background: 'var(--color-primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                    margin: '0 auto var(--spacing-xs)', border: '2px solid var(--color-primary)',
                  }}>
                    <i className="fas fa-server" style={{ color: 'var(--color-primary)', fontSize: '1rem' }} />
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>LocalAI Instance</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Orchestrator</div>
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
                  <i className="fas fa-arrow-right" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }} />
                  <span style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>RPC</span>
                </div>
                <div style={{ textAlign: 'center' }}>
                  <div style={{ display: 'flex', gap: '4px', marginBottom: 'var(--spacing-xs)' }}>
                    {['Layer 1-10', 'Layer 11-20', 'Layer 21-30'].map((label, i) => (
                      <div key={i} style={{ textAlign: 'center' }}>
                        <div style={{
                          width: 56, height: 36, borderRadius: 'var(--radius-sm)',
                          background: 'var(--color-accent-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                          border: '1px solid var(--color-accent-border)',
                        }}>
                          <i className="fas fa-microchip" style={{ color: 'var(--color-accent)', fontSize: '0.75rem' }} />
                        </div>
                        <div style={{ fontSize: '0.5625rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{label}</div>
                      </div>
                    ))}
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>RPC Workers</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Distributed memory</div>
                </div>
              </div>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', textAlign: 'center', marginTop: 'var(--spacing-sm)', lineHeight: 1.5 }}>
                Model weights are <strong>split across RPC workers</strong>. Each worker holds a portion of the model layers in its memory (GPU or CPU).
              </p>
            </div>

            {/* llama.cpp Status + nodes */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
              <h4 style={{ fontSize: '1rem', fontWeight: 600 }}>Connected Workers</h4>
              <div style={{ fontSize: '1.25rem', fontWeight: 700 }}>
                <span style={{ color: llamaOnline > 0 ? 'var(--color-success)' : 'var(--color-error)' }}>{llamaOnline}</span>
                <span style={{ color: 'var(--color-text-secondary)', fontSize: '1rem' }}>/{llamaTotal}</span>
              </div>
            </div>

            {workers.length === 0 ? (
              <div style={{
                textAlign: 'center', padding: 'var(--spacing-lg)',
                background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
                borderRadius: 'var(--radius-lg)',
              }}>
                <i className="fas fa-puzzle-piece" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }} />
                <p style={{ fontWeight: 500, color: 'var(--color-text-secondary)' }}>No llama.cpp workers connected</p>
                <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>Start workers to see them here</p>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 'var(--spacing-md)' }}>
                {workers.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label="Worker" iconColor="var(--color-accent)" iconBg="var(--color-accent-light)" />
                ))}
              </div>
            )}
          </div>

          {/* ── MLX Distributed Workers Section ── */}
          <div style={{ padding: 'var(--spacing-lg)', borderBottom: '1px solid var(--color-border-subtle)' }}>
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-sm)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <i className="fas fa-apple-whole" style={{ color: 'var(--color-warning)' }} />
              MLX Distributed Workers
            </h3>

            {/* MLX Architecture diagram */}
            <div style={{
              background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
              borderRadius: 'var(--radius-lg)', padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--spacing-md)', flexWrap: 'wrap' }}>
                <div style={{ textAlign: 'center' }}>
                  <div style={{
                    width: 48, height: 48, borderRadius: 'var(--radius-md)',
                    background: 'var(--color-primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                    margin: '0 auto var(--spacing-xs)', border: '2px solid var(--color-primary)',
                  }}>
                    <i className="fas fa-server" style={{ color: 'var(--color-primary)', fontSize: '1rem' }} />
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>LocalAI</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Rank 0</div>
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
                  <i className="fas fa-arrows-left-right" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }} />
                  <span style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Ring / JACCL</span>
                </div>
                <div style={{ textAlign: 'center' }}>
                  <div style={{ display: 'flex', gap: '4px', marginBottom: 'var(--spacing-xs)' }}>
                    {['Layers 1-16', 'Layers 17-32'].map((label, i) => (
                      <div key={i} style={{ textAlign: 'center' }}>
                        <div style={{
                          width: 64, height: 36, borderRadius: 'var(--radius-sm)',
                          background: 'var(--color-warning-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
                          border: '1px solid var(--color-warning-border)',
                        }}>
                          <i className="fas fa-microchip" style={{ color: 'var(--color-warning)', fontSize: '0.75rem' }} />
                        </div>
                        <div style={{ fontSize: '0.5625rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{label}</div>
                      </div>
                    ))}
                  </div>
                  <div style={{ fontSize: '0.75rem', fontWeight: 600 }}>MLX Workers</div>
                  <div style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>Pipeline parallel</div>
                </div>
              </div>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', textAlign: 'center', marginTop: 'var(--spacing-sm)', lineHeight: 1.5 }}>
                MLX distributed uses <strong>native Apple Silicon communication</strong>. All nodes execute model code simultaneously via pipeline or tensor parallelism.
              </p>
            </div>

            {/* MLX Status + nodes */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
              <h4 style={{ fontSize: '1rem', fontWeight: 600 }}>Connected MLX Workers</h4>
              <div style={{ fontSize: '1.25rem', fontWeight: 700 }}>
                <span style={{ color: mlxOnline > 0 ? 'var(--color-success)' : 'var(--color-error)' }}>{mlxOnline}</span>
                <span style={{ color: 'var(--color-text-secondary)', fontSize: '1rem' }}>/{mlxTotal}</span>
              </div>
            </div>

            {mlxWorkers.length === 0 ? (
              <div style={{
                textAlign: 'center', padding: 'var(--spacing-lg)',
                background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
                borderRadius: 'var(--radius-lg)',
              }}>
                <i className="fas fa-apple-whole" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }} />
                <p style={{ fontWeight: 500, color: 'var(--color-text-secondary)' }}>No MLX workers connected</p>
                <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-xs)' }}>Start MLX workers on Apple Silicon Macs</p>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 'var(--spacing-md)' }}>
                {mlxWorkers.map((node, i) => (
                  <NodeCard key={node.id || i} node={node} label={`MLX Rank ${i + 1}`} iconColor="var(--color-warning)" iconBg="var(--color-warning-light)" />
                ))}
              </div>
            )}
          </div>

          {/* Setup Guides */}
          <div style={{ padding: 'var(--spacing-lg)' }}>
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-book" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-sm)' }} />
              Setup Workers
            </h3>

            <div style={{
              background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-lg)',
              border: '1px solid var(--color-border-subtle)', padding: 'var(--spacing-lg)',
              marginBottom: 'var(--spacing-md)',
            }}>
              <h4 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>llama.cpp RPC Worker</h4>
              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-sm)' }}>
                Each worker exposes its GPU/CPU memory as a shard for distributed model inference.
              </p>
              <CommandBlock
                command={`docker run -ti --net host \\\n  -e TOKEN="${token}" \\\n  --name local-ai-worker \\\n  localai/localai:latest-cpu worker p2p-llama-cpp-rpc`}
                addToast={addToast}
              />
            </div>

            <div style={{
              background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-lg)',
              border: '1px solid var(--color-warning-border)', padding: 'var(--spacing-lg)',
            }}>
              <h4 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>MLX Distributed Worker</h4>
              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-sm)' }}>
                Run on Apple Silicon Macs to participate in distributed MLX inference via pipeline parallelism.
              </p>
              <CommandBlock
                command={`docker run -ti --net host \\\n  -e TOKEN="${token}" \\\n  --name local-ai-mlx-worker \\\n  localai/localai:latest-metal-darwin-arm64 worker p2p-mlx`}
                addToast={addToast}
              />
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem', marginTop: 'var(--spacing-sm)' }}>
                For more information, see the{' '}
                <a href="https://localai.io/features/mlx-distributed/" target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-warning)' }}>MLX Distributed</a> docs.
              </p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
