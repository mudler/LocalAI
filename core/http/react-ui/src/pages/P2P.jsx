import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { p2pApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

function StatCard({ icon, iconColor, iconBg, title, subtitle, online, total, label }) {
  return (
    <div style={{
      background: 'var(--color-bg-secondary)',
      border: '1px solid var(--color-border-subtle)',
      borderRadius: 'var(--radius-lg)',
      padding: 'var(--spacing-md)',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <div style={{
            width: 48, height: 48, borderRadius: 'var(--radius-lg)',
            background: iconBg, display: 'flex', alignItems: 'center', justifyContent: 'center',
            marginRight: 'var(--spacing-sm)',
          }}>
            <i className={`fas ${icon}`} style={{ color: iconColor, fontSize: '1.25rem' }} />
          </div>
          <div>
            <h3 style={{ fontSize: '1.125rem', fontWeight: 700 }}>{title}</h3>
            <p style={{ color: iconColor, fontSize: '0.8125rem' }}>{subtitle}</p>
          </div>
        </div>
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>
            <span style={{ color: online > 0 ? 'var(--color-success)' : 'var(--color-error)' }}>{online}</span>
            <span style={{ color: 'var(--color-text-secondary)', fontSize: '1.25rem' }}>/{total}</span>
          </div>
          <p style={{ color: iconColor, fontSize: '0.8125rem' }}>{label}</p>
        </div>
      </div>
    </div>
  )
}

function NodeCard({ node, label, iconColor, iconBg }) {
  return (
    <div style={{
      background: 'var(--color-bg-primary)',
      border: `1px solid ${node.isOnline ? 'rgba(34,197,94,0.5)' : 'rgba(239,68,68,0.5)'}`,
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
        background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
        paddingRight: 'var(--spacing-xl)', borderRadius: 'var(--radius-md)',
        fontSize: '0.75rem', fontFamily: "'JetBrains Mono', monospace",
        whiteSpace: 'pre-wrap', wordBreak: 'break-all',
        color: 'var(--color-text-secondary)', overflow: 'auto',
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

function NetworkSection({ title, subtitle, icon, iconColor, nodes, stats, token, addToast, label, iconBg, commands }) {
  const [activeTab, setActiveTab] = useState('cli')

  return (
    <div style={{ marginBottom: 'var(--spacing-xl)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
          <i className={`fas ${icon}`} style={{ color: iconColor }} />
          <div>
            <h2 style={{ fontSize: '1.125rem', fontWeight: 600 }}>{title}</h2>
            {subtitle && <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{subtitle}</p>}
          </div>
        </div>
        <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>
          <span style={{ color: (stats?.online ?? 0) > 0 ? iconColor : 'var(--color-error)' }}>
            {stats?.online ?? 0}
          </span>
          <span style={{ color: 'var(--color-text-secondary)', fontSize: '1.125rem' }}>/{stats?.total ?? 0}</span>
        </div>
      </div>

      {/* Info banner */}
      <div style={{
        background: 'rgba(99,102,241,0.05)', border: '1px solid rgba(99,102,241,0.2)',
        borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)',
        marginBottom: 'var(--spacing-md)', fontSize: '0.8125rem', color: 'var(--color-text-secondary)',
        display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-sm)',
      }}>
        <i className="fas fa-info-circle" style={{ color: 'var(--color-primary)', marginTop: 2 }} />
        <div>
          {title === 'Federation Network' ? (
            <>In federated mode, multiple LocalAI instances share the load. Each instance can serve requests and forward them to peers when needed.</>
          ) : (
            <>Workers provide distributed compute for llama.cpp models. They connect to the network and offer their GPU/CPU resources for inference.</>
          )}
        </div>
      </div>

      {/* Nodes grid */}
      {nodes.length === 0 ? (
        <div style={{
          textAlign: 'center', padding: 'var(--spacing-xl)',
          background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-subtle)',
          borderRadius: 'var(--radius-lg)',
        }}>
          <i className="fas fa-server" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }} />
          <p style={{ color: 'var(--color-text-secondary)' }}>No {label.toLowerCase()}s connected yet</p>
          <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>Use the commands below to add {label.toLowerCase()}s</p>
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
          {nodes.map((node, i) => (
            <NodeCard key={node.id || i} node={node} label={label} iconColor={iconColor} iconBg={iconBg} />
          ))}
        </div>
      )}

      {/* CLI / Docker tabs */}
      {token && commands && (
        <div style={{
          background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border-subtle)',
          borderRadius: 'var(--radius-lg)', overflow: 'hidden', marginTop: 'var(--spacing-md)',
        }}>
          <div style={{ display: 'flex', borderBottom: '1px solid var(--color-border-subtle)' }}>
            <button
              onClick={() => setActiveTab('cli')}
              style={{
                flex: 1, padding: 'var(--spacing-sm)', background: activeTab === 'cli' ? 'var(--color-bg-primary)' : 'transparent',
                border: 'none', cursor: 'pointer', color: activeTab === 'cli' ? 'var(--color-primary)' : 'var(--color-text-secondary)',
                fontWeight: activeTab === 'cli' ? 600 : 400, fontSize: '0.8125rem',
                borderBottom: activeTab === 'cli' ? '2px solid var(--color-primary)' : '2px solid transparent',
              }}
            >
              <i className="fas fa-terminal" style={{ marginRight: 4 }} /> CLI
            </button>
            <button
              onClick={() => setActiveTab('docker')}
              style={{
                flex: 1, padding: 'var(--spacing-sm)', background: activeTab === 'docker' ? 'var(--color-bg-primary)' : 'transparent',
                border: 'none', cursor: 'pointer', color: activeTab === 'docker' ? 'var(--color-primary)' : 'var(--color-text-secondary)',
                fontWeight: activeTab === 'docker' ? 600 : 400, fontSize: '0.8125rem',
                borderBottom: activeTab === 'docker' ? '2px solid var(--color-primary)' : '2px solid transparent',
              }}
            >
              <i className="fab fa-docker" style={{ marginRight: 4 }} /> Docker
            </button>
          </div>
          <div style={{ padding: 'var(--spacing-md)' }}>
            {(activeTab === 'cli' ? commands.cli : commands.docker).map((cmd, i) => (
              <div key={i} style={{ marginBottom: i < (activeTab === 'cli' ? commands.cli : commands.docker).length - 1 ? 'var(--spacing-md)' : 0 }}>
                {cmd.label && <p style={{ fontSize: '0.75rem', fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 4 }}>{cmd.label}</p>}
                <CommandBlock command={cmd.command} addToast={addToast} />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

export default function P2P() {
  const { addToast } = useOutletContext()
  const [workers, setWorkers] = useState([])
  const [federation, setFederation] = useState([])
  const [stats, setStats] = useState({ workers: { online: 0, total: 0 }, federated: { online: 0, total: 0 } })
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(false)
  const [token, setToken] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const [wRes, fRes, sRes, tRes] = await Promise.allSettled([
        p2pApi.getWorkers(),
        p2pApi.getFederation(),
        p2pApi.getStats(),
        p2pApi.getToken(),
      ])
      if (wRes.status === 'fulfilled') {
        const data = wRes.value
        setWorkers(data?.nodes || (Array.isArray(data) ? data : []))
      }
      if (fRes.status === 'fulfilled') {
        const data = fRes.value
        setFederation(data?.nodes || (Array.isArray(data) ? data : []))
      }
      if (sRes.status === 'fulfilled') {
        setStats(sRes.value)
        setEnabled(true)
      }
      if (tRes.status === 'fulfilled') {
        const t = typeof tRes.value === 'string' ? tRes.value : (tRes.value?.token || '')
        if (t) {
          setToken(t)
          setEnabled(true)
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

  if (!enabled) {
    return (
      <div className="page">
        <div style={{ textAlign: 'center', padding: 'var(--spacing-xl) 0' }}>
          <i className="fas fa-network-wired" style={{ fontSize: '3rem', color: 'var(--color-primary)', marginBottom: 'var(--spacing-md)' }} />
          <h1 style={{ fontSize: '1.5rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            P2P Distribution Not Enabled
          </h1>
          <p style={{ color: 'var(--color-text-secondary)', maxWidth: 600, margin: '0 auto var(--spacing-xl)' }}>
            Enable P2P to connect with other LocalAI nodes and share resources across your network.
          </p>
        </div>

        {/* Setup steps */}
        <div className="card" style={{ maxWidth: 700, margin: '0 auto var(--spacing-xl)', padding: 'var(--spacing-lg)' }}>
          <h3 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
            <i className="fas fa-rocket" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Getting Started
          </h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <span style={{
                width: 28, height: 28, borderRadius: '50%', background: 'var(--color-primary)',
                color: 'white', display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: '0.8125rem', fontWeight: 700, flexShrink: 0,
              }}>1</span>
              <div>
                <p style={{ fontWeight: 500, marginBottom: 4 }}>Start LocalAI with P2P enabled</p>
                <pre style={{
                  background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
                  borderRadius: 'var(--radius-md)', fontSize: '0.75rem',
                  fontFamily: "'JetBrains Mono', monospace",
                }}>local-ai run --p2p</pre>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <span style={{
                width: 28, height: 28, borderRadius: '50%', background: 'var(--color-primary)',
                color: 'white', display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: '0.8125rem', fontWeight: 700, flexShrink: 0,
              }}>2</span>
              <div>
                <p style={{ fontWeight: 500, marginBottom: 4 }}>Or use an existing network token</p>
                <pre style={{
                  background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)',
                  borderRadius: 'var(--radius-md)', fontSize: '0.75rem',
                  fontFamily: "'JetBrains Mono', monospace", whiteSpace: 'pre-wrap',
                }}>{`export TOKEN="your-token-here"\nlocal-ai run --p2p`}</pre>
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <span style={{
                width: 28, height: 28, borderRadius: '50%', background: 'var(--color-primary)',
                color: 'white', display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: '0.8125rem', fontWeight: 700, flexShrink: 0,
              }}>3</span>
              <div>
                <p style={{ fontWeight: 500 }}>Refresh this page to access the P2P dashboard</p>
              </div>
            </div>
          </div>
        </div>

        {/* Feature cards */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-xl)' }}>
          <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
            <i className="fas fa-network-wired" style={{ fontSize: '1.5rem', color: 'var(--color-primary)', marginBottom: 'var(--spacing-sm)' }} />
            <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Instance Federation</h3>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Load balance across multiple LocalAI instances</p>
          </div>
          <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
            <i className="fas fa-share-nodes" style={{ fontSize: '1.5rem', color: 'var(--color-accent)', marginBottom: 'var(--spacing-sm)' }} />
            <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Model Sharding</h3>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Split large models across multiple workers</p>
          </div>
          <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
            <i className="fas fa-bolt" style={{ fontSize: '1.5rem', color: 'var(--color-success)', marginBottom: 'var(--spacing-sm)' }} />
            <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Resource Sharing</h3>
            <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Pool GPU/CPU resources from multiple devices</p>
          </div>
        </div>

        <div style={{ textAlign: 'center' }}>
          <a className="btn btn-primary" href="https://localai.io/features/distribute/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-book" /> Documentation
          </a>
        </div>
      </div>
    )
  }

  const federationCommands = {
    cli: [
      { label: 'Start a new federated instance (share models):', command: `export TOKEN="${token}"\nlocal-ai run --federated --p2p` },
      { label: 'Start a federated load balancer:', command: `export TOKEN="${token}"\nlocal-ai federated` },
    ],
    docker: [
      { label: 'Federated instance (port 8080):', command: `docker run -ti --net host -e TOKEN="${token}" --name local-ai -p 8080:8080 localai/localai:latest-cpu run --federated --p2p` },
      { label: 'Federated server (port 9090):', command: `docker run -ti --net host -e TOKEN="${token}" --name local-ai-fed -p 9090:8080 localai/localai:latest-cpu federated` },
    ],
  }

  const workerCommands = {
    cli: [
      { label: 'Start a worker node:', command: `export TOKEN="${token}"\nlocal-ai worker p2p-llama-cpp-rpc` },
    ],
    docker: [
      { label: 'Worker container:', command: `docker run -ti --net host -e TOKEN="${token}" --name local-ai-worker -p 8080:8080 localai/localai:latest-cpu worker p2p-llama-cpp-rpc` },
    ],
  }

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">Distributed AI Computing</h1>
        <p className="page-subtitle">P2P network status and node management</p>
      </div>

      {/* Network Status Overview */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-xl)' }}>
        <StatCard
          icon="fa-network-wired"
          iconColor="var(--color-primary)"
          iconBg="var(--color-primary-light)"
          title="Federation"
          subtitle="Instance sharing"
          online={stats.federated?.online ?? 0}
          total={stats.federated?.total ?? 0}
          label="nodes"
        />
        <StatCard
          icon="fa-server"
          iconColor="var(--color-accent)"
          iconBg="var(--color-accent-light)"
          title="Workers"
          subtitle="Compute nodes"
          online={stats.workers?.online ?? 0}
          total={stats.workers?.total ?? 0}
          label="workers"
        />

        {/* Network Token Card */}
        <div style={{
          background: 'var(--color-bg-secondary)',
          border: '1px solid var(--color-border-subtle)',
          borderRadius: 'var(--radius-lg)',
          padding: 'var(--spacing-md)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <div style={{
                width: 48, height: 48, borderRadius: 'var(--radius-lg)',
                background: 'rgba(34,197,94,0.1)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-key" style={{ color: 'var(--color-success)', fontSize: '1.25rem' }} />
              </div>
              <div>
                <h3 style={{ fontSize: '1rem', fontWeight: 700 }}>Network Token</h3>
                <p style={{ fontSize: '0.75rem', color: 'var(--color-success)' }}>Connected</p>
              </div>
            </div>
            <button
              className="btn btn-secondary btn-sm"
              onClick={copyToken}
              title="Copy token"
            >
              <i className="fas fa-copy" />
            </button>
          </div>
          <div style={{
            background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-md)',
            padding: '6px var(--spacing-sm)', fontSize: '0.6875rem',
            fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-muted)',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            cursor: 'pointer',
          }} onClick={copyToken} title="Click to copy">
            {token ? `${token.slice(0, 40)}...` : 'Loading...'}
          </div>
        </div>
      </div>

      {/* Federation Network */}
      <NetworkSection
        title="Federation Network"
        subtitle="Federated instances for load balancing"
        icon="fa-network-wired"
        iconColor="var(--color-primary)"
        iconBg="var(--color-primary-light)"
        nodes={federation}
        stats={stats.federated}
        token={token}
        addToast={addToast}
        label="Node"
        commands={federationCommands}
      />

      {/* Worker Network */}
      <NetworkSection
        title="Worker Network"
        subtitle="Distributed model computation (llama.cpp)"
        icon="fa-puzzle-piece"
        iconColor="var(--color-accent)"
        iconBg="var(--color-accent-light)"
        nodes={workers}
        stats={stats.workers}
        token={token}
        addToast={addToast}
        label="Worker"
        commands={workerCommands}
      />
    </div>
  )
}
