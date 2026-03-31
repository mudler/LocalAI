import { useState, useEffect, useCallback, Fragment } from 'react'
import { useOutletContext, useNavigate } from 'react-router-dom'
import { nodesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import ImageSelector, { useImageSelector, dockerImage, dockerFlags } from '../components/ImageSelector'

function timeAgo(dateString) {
  if (!dateString) return 'never'
  const seconds = Math.floor((Date.now() - new Date(dateString).getTime()) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

function formatVRAM(bytes) {
  if (!bytes || bytes === 0) return null
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

function gpuVendorLabel(vendor) {
  const labels = {
    nvidia: 'NVIDIA',
    amd: 'AMD',
    intel: 'Intel',
    vulkan: 'Vulkan',
  }
  return labels[vendor] || null
}

const statusConfig = {
  healthy: { color: 'var(--color-success)', label: 'Healthy' },
  unhealthy: { color: 'var(--color-error)', label: 'Unhealthy' },
  registering: { color: 'var(--color-primary)', label: 'Registering' },
  draining: { color: 'var(--color-warning)', label: 'Draining' },
  pending: { color: 'var(--color-warning)', label: 'Pending Approval' },
}

const modelStateConfig = {
  loaded: { bg: 'var(--color-success-light)', color: 'var(--color-success)', border: 'var(--color-success-border)' },
  loading: { bg: 'var(--color-primary-light)', color: 'var(--color-primary)', border: 'var(--color-primary-border)' },
  unloading: { bg: 'var(--color-warning-light)', color: 'var(--color-warning)', border: 'var(--color-warning-border)' },
  idle: { bg: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)', border: 'var(--color-border-subtle)' },
}

function StatCard({ icon, label, value, color }) {
  return (
    <div className="card" style={{ padding: 'var(--spacing-sm) var(--spacing-md)', flex: '1 1 0', minWidth: 120 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
        <i className={icon} style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
        <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontWeight: 500, textTransform: 'uppercase', letterSpacing: '0.03em' }}>{label}</span>
      </div>
      <div style={{ fontSize: '1.375rem', fontWeight: 700, fontFamily: 'JetBrains Mono, monospace', color: color || 'var(--color-text-primary)' }}>
        {value}
      </div>
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

function WorkerHintCard({ addToast, activeTab, hasWorkers }) {
  const frontendUrl = window.location.origin
  const { selected, setSelected, option, dev, setDev } = useImageSelector('cpu')
  const isAgent = activeTab === 'agent'
  const workerCmd = isAgent ? 'agent-worker' : 'worker'
  const flags = dockerFlags(option)
  const flagsStr = flags ? `${flags} \\\n  ` : ''

  const title = hasWorkers
    ? (isAgent ? 'Add more agent workers' : 'Add more workers')
    : (isAgent ? 'No agent workers registered yet' : 'No workers registered yet')

  return (
    <div className="card" style={{ padding: 'var(--spacing-lg)', marginBottom: 'var(--spacing-xl)' }}>
      <h3 style={{ fontSize: '1rem', fontWeight: 700, marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center' }}>
        <i className={`fas ${hasWorkers ? 'fa-plus-circle' : 'fa-info-circle'}`} style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-sm)' }} />
        {title}
      </h3>
      <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)' }}>
        {isAgent
          ? 'Start agent worker nodes to execute MCP tools and agent tasks. Agent workers self-register with this frontend.'
          : 'Start worker nodes to scale inference across multiple machines. Workers self-register with this frontend.'}
      </p>

      <p style={{ fontWeight: 600, fontSize: '0.8125rem', marginBottom: 'var(--spacing-xs)' }}>Select your hardware</p>
      <ImageSelector selected={selected} onSelect={setSelected} dev={dev} onDevChange={setDev} />

      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
        <div>
          <p style={{ fontWeight: 600, fontSize: '0.8125rem', marginBottom: 'var(--spacing-xs)' }}>CLI</p>
          <CommandBlock
            command={`local-ai ${workerCmd} \\\n  --register-to "${frontendUrl}" \\\n  --nats-url "nats://nats:4222" \\\n  --registration-token "$LOCALAI_REGISTRATION_TOKEN"`}
            addToast={addToast}
          />
        </div>
        <div>
          <p style={{ fontWeight: 600, fontSize: '0.8125rem', marginBottom: 'var(--spacing-xs)' }}>Docker</p>
          <CommandBlock
            command={`docker run --net host ${flagsStr}\\\n  -e LOCALAI_REGISTER_TO="${frontendUrl}" \\\n  -e LOCALAI_NATS_URL="nats://nats:4222" \\\n  -e LOCALAI_REGISTRATION_TOKEN="$TOKEN" \\\n  ${dockerImage(option, dev)} ${workerCmd}`}
            addToast={addToast}
          />
        </div>
      </div>

      <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-md)' }}>
        For full setup instructions, architecture details, and Kubernetes deployment, see the{' '}
        <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer"
          style={{ color: 'var(--color-primary)' }}>Distributed Mode documentation <i className="fas fa-external-link-alt" style={{ fontSize: '0.625rem' }} /></a>.
      </p>
    </div>
  )
}

function SchedulingForm({ onSave, onCancel }) {
  const [modelName, setModelName] = useState('')
  const [selectorText, setSelectorText] = useState('')
  const [minReplicas, setMinReplicas] = useState(0)
  const [maxReplicas, setMaxReplicas] = useState(0)

  const handleSubmit = () => {
    let nodeSelector = null
    if (selectorText.trim()) {
      const pairs = {}
      selectorText.split(',').forEach(p => {
        const [k, v] = p.split('=').map(s => s.trim())
        if (k) pairs[k] = v || ''
      })
      nodeSelector = pairs
    }
    onSave({
      model_name: modelName,
      node_selector: nodeSelector ? JSON.stringify(nodeSelector) : '',
      min_replicas: minReplicas,
      max_replicas: maxReplicas,
    })
  }

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-sm)' }}>
        <div>
          <label style={{ fontSize: '0.75rem', fontWeight: 500 }}>Model Name</label>
          <input type="text" value={modelName} onChange={e => setModelName(e.target.value)}
            placeholder="e.g. llama3" style={{ width: '100%' }} />
        </div>
        <div>
          <label style={{ fontSize: '0.75rem', fontWeight: 500 }}>Node Selector (key=value, comma-separated)</label>
          <input type="text" value={selectorText} onChange={e => setSelectorText(e.target.value)}
            placeholder="e.g. gpu.vendor=nvidia,tier=fast" style={{ width: '100%' }} />
        </div>
        <div>
          <label style={{ fontSize: '0.75rem', fontWeight: 500 }}>Min Replicas (0 = no minimum)</label>
          <input type="number" min={0} value={minReplicas} onChange={e => setMinReplicas(parseInt(e.target.value) || 0)}
            style={{ width: '100%' }} />
        </div>
        <div>
          <label style={{ fontSize: '0.75rem', fontWeight: 500 }}>Max Replicas (0 = unlimited)</label>
          <input type="number" min={0} value={maxReplicas} onChange={e => setMaxReplicas(parseInt(e.target.value) || 0)}
            style={{ width: '100%' }} />
        </div>
      </div>
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginTop: 'var(--spacing-sm)', justifyContent: 'flex-end' }}>
        <button className="btn btn-secondary btn-sm" onClick={onCancel}>Cancel</button>
        <button className="btn btn-primary btn-sm" onClick={handleSubmit} disabled={!modelName}>Save</button>
      </div>
    </div>
  )
}

export default function Nodes() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [nodesList, setNodesList] = useState([])
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(true)
  const [expandedNodeId, setExpandedNodeId] = useState(null)
  const [nodeModels, setNodeModels] = useState({})
  const [nodeBackends, setNodeBackends] = useState({})
  const [confirmDelete, setConfirmDelete] = useState(null)
  const [showTips, setShowTips] = useState(false)
  const [activeTab, setActiveTab] = useState('backend') // 'backend', 'agent', or 'scheduling'
  const [schedulingConfigs, setSchedulingConfigs] = useState([])
  const [showSchedulingForm, setShowSchedulingForm] = useState(false)

  const fetchNodes = useCallback(async () => {
    try {
      const data = await nodesApi.list()
      setNodesList(Array.isArray(data) ? data : [])
      setEnabled(true)
    } catch (err) {
      if (err.message?.includes('503') || err.message?.includes('Service Unavailable')) {
        setEnabled(false)
      }
    } finally {
      setLoading(false)
    }
  }, [])

  const fetchScheduling = useCallback(async () => {
    try {
      const data = await nodesApi.listScheduling()
      setSchedulingConfigs(Array.isArray(data) ? data : [])
    } catch { setSchedulingConfigs([]) }
  }, [])

  useEffect(() => {
    fetchNodes()
    fetchScheduling()
    const interval = setInterval(fetchNodes, 5000)
    return () => clearInterval(interval)
  }, [fetchNodes, fetchScheduling])

  const fetchModels = useCallback(async (nodeId) => {
    try {
      const data = await nodesApi.getModels(nodeId)
      setNodeModels(prev => ({ ...prev, [nodeId]: Array.isArray(data) ? data : [] }))
    } catch {
      setNodeModels(prev => ({ ...prev, [nodeId]: [] }))
    }
  }, [])

  const fetchBackends = useCallback(async (nodeId) => {
    try {
      const data = await nodesApi.getBackends(nodeId)
      setNodeBackends(prev => ({ ...prev, [nodeId]: Array.isArray(data) ? data : [] }))
    } catch {
      setNodeBackends(prev => ({ ...prev, [nodeId]: [] }))
    }
  }, [])

  const toggleExpand = (nodeId) => {
    if (expandedNodeId === nodeId) {
      setExpandedNodeId(null)
    } else {
      setExpandedNodeId(nodeId)
      if (!nodeModels[nodeId]) {
        fetchModels(nodeId)
      }
      if (!nodeBackends[nodeId]) {
        fetchBackends(nodeId)
      }
    }
  }

  const handleReinstallBackend = async (nodeId, backendName) => {
    try {
      await nodesApi.installBackend(nodeId, backendName)
      addToast(`Backend "${backendName}" reinstalled`, 'success')
      fetchBackends(nodeId)
    } catch (err) {
      addToast(`Failed to reinstall backend: ${err.message}`, 'error')
    }
  }

  const handleDrain = async (nodeId) => {
    try {
      await nodesApi.drain(nodeId)
      addToast('Node set to draining', 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to drain node: ${err.message}`, 'error')
    }
  }

  const handleApprove = async (nodeId) => {
    try {
      await nodesApi.approve(nodeId)
      addToast('Node approved', 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to approve node: ${err.message}`, 'error')
    }
  }

  const handleUnloadModel = async (nodeId, modelName) => {
    try {
      await nodesApi.unloadModel(nodeId, modelName)
      addToast(`Model "${modelName}" unloaded`, 'success')
      fetchModels(nodeId)
    } catch (err) {
      addToast(`Failed to unload model: ${err.message}`, 'error')
    }
  }

  const handleAddLabel = async (nodeId, key, value) => {
    try {
      await nodesApi.mergeLabels(nodeId, { [key]: value })
      addToast(`Label "${key}=${value}" added`, 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to add label: ${err.message}`, 'error')
    }
  }

  const handleDeleteLabel = async (nodeId, key) => {
    try {
      await nodesApi.deleteLabel(nodeId, key)
      addToast(`Label "${key}" removed`, 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to remove label: ${err.message}`, 'error')
    }
  }

  const handleDelete = async (nodeId) => {
    try {
      await nodesApi.delete(nodeId)
      addToast('Node removed', 'success')
      setConfirmDelete(null)
      if (expandedNodeId === nodeId) setExpandedNodeId(null)
      fetchNodes()
    } catch (err) {
      addToast(`Failed to remove node: ${err.message}`, 'error')
      setConfirmDelete(null)
    }
  }

  if (loading) {
    return (
      <div className="page" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  // Disabled state
  if (!enabled) {
    return (
      <div className="page">
        <div style={{ textAlign: 'center', padding: 'var(--spacing-xl) 0' }}>
          <i className="fas fa-network-wired" style={{ fontSize: '3rem', color: 'var(--color-primary)', marginBottom: 'var(--spacing-md)' }} />
          <h1 style={{ fontSize: '1.5rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            Distributed Mode Not Enabled
          </h1>
          <p style={{ color: 'var(--color-text-secondary)', maxWidth: 600, margin: '0 auto var(--spacing-xl)' }}>
            Enable distributed mode to manage backend nodes across multiple machines. Nodes self-register and are monitored for health, enabling horizontal scaling of model inference.
          </p>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-xl)' }}>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-server" style={{ color: 'var(--color-primary)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Horizontal Scaling</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Add backend nodes to scale inference capacity</p>
            </div>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-accent-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-route" style={{ color: 'var(--color-accent)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Smart Routing</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Route requests to the best available node</p>
            </div>
            <div className="card" style={{ textAlign: 'center', padding: 'var(--spacing-md)' }}>
              <div style={{
                width: 40, height: 40, borderRadius: 'var(--radius-md)', margin: '0 auto var(--spacing-sm)',
                background: 'var(--color-success-light)', display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <i className="fas fa-heart-pulse" style={{ color: 'var(--color-success)', fontSize: '1.25rem' }} />
              </div>
              <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Health Monitoring</h3>
              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>Automatic heartbeat checks and failover</p>
            </div>
          </div>
        </div>

        <div className="card" style={{ maxWidth: 700, margin: '0 auto var(--spacing-xl)', padding: 'var(--spacing-lg)', textAlign: 'left' }}>
          <h3 style={{ fontSize: '1.125rem', fontWeight: 700, marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center' }}>
            <i className="fas fa-rocket" style={{ color: 'var(--color-accent)', marginRight: 'var(--spacing-sm)' }} />
            How to Enable Distributed Mode
          </h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={1} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500, marginBottom: 'var(--spacing-xs)' }}>Start LocalAI with distributed mode</p>
                <CommandBlock
                  command={`local-ai run --distributed \\\n  --distributed-db "postgres://user:pass@host/db" \\\n  --distributed-nats "nats://host:4222"`}
                  addToast={addToast}
                />
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={2} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500, marginBottom: 'var(--spacing-xs)' }}>Register backend nodes</p>
                <CommandBlock
                  command={`local-ai worker \\\n  --register-to "http://localai-host:8080" \\\n  --nats-url "nats://nats:4222" \\\n  --node-name "gpu-node-1"`}
                  addToast={addToast}
                />
              </div>
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              <StepNumber n={3} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div style={{ flex: 1 }}>
                <p style={{ fontWeight: 500 }}>Manage nodes from this dashboard</p>
                <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', marginTop: 'var(--spacing-xs)' }}>
                  Once enabled, refresh this page to see registered nodes and their health status.
                </p>
              </div>
            </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginTop: 'var(--spacing-md)' }}>
            For full setup instructions, architecture details, and Kubernetes deployment, see the{' '}
            <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer"
              style={{ color: 'var(--color-primary)' }}>Distributed Mode documentation <i className="fas fa-external-link-alt" style={{ fontSize: '0.625rem' }} /></a>.
          </p>
          </div>
        </div>
      </div>
    )
  }

  // Split nodes by type
  const backendNodes = nodesList.filter(n => !n.node_type || n.node_type === 'backend')
  const agentNodes = nodesList.filter(n => n.node_type === 'agent')
  const filteredNodes = activeTab === 'agent' ? agentNodes : backendNodes

  // Compute stats for current tab
  const total = filteredNodes.length
  const healthy = filteredNodes.filter(n => n.status === 'healthy').length
  const unhealthy = filteredNodes.filter(n => n.status === 'unhealthy').length
  const draining = filteredNodes.filter(n => n.status === 'draining').length
  const pending = filteredNodes.filter(n => n.status === 'pending').length

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">
          <i className="fas fa-network-wired" style={{ marginRight: 'var(--spacing-sm)' }} />
          Distributed Nodes
        </h1>
        <p className="page-subtitle">
          Manage backend and agent worker nodes
        </p>
      </div>

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-lg)', borderBottom: '2px solid var(--color-border)' }}>
        <button
          onClick={() => setActiveTab('backend')}
          style={{
            padding: 'var(--spacing-sm) var(--spacing-lg)',
            border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: '0.875rem',
            background: 'none',
            color: activeTab === 'backend' ? 'var(--color-primary)' : 'var(--color-text-muted)',
            borderBottom: activeTab === 'backend' ? '2px solid var(--color-primary)' : '2px solid transparent',
            marginBottom: '-2px',
          }}
        >
          <i className="fas fa-server" style={{ marginRight: 6 }} />
          Backend Workers ({backendNodes.length})
        </button>
        <button
          onClick={() => setActiveTab('agent')}
          style={{
            padding: 'var(--spacing-sm) var(--spacing-lg)',
            border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: '0.875rem',
            background: 'none',
            color: activeTab === 'agent' ? 'var(--color-primary)' : 'var(--color-text-muted)',
            borderBottom: activeTab === 'agent' ? '2px solid var(--color-primary)' : '2px solid transparent',
            marginBottom: '-2px',
          }}
        >
          <i className="fas fa-robot" style={{ marginRight: 6 }} />
          Agent Workers ({agentNodes.length})
        </button>
        <button
          onClick={() => setActiveTab('scheduling')}
          style={{
            padding: 'var(--spacing-sm) var(--spacing-lg)',
            border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: '0.875rem',
            background: 'none',
            color: activeTab === 'scheduling' ? 'var(--color-primary)' : 'var(--color-text-muted)',
            borderBottom: activeTab === 'scheduling' ? '2px solid var(--color-primary)' : '2px solid transparent',
            marginBottom: '-2px',
          }}
        >
          <i className="fas fa-calendar-alt" style={{ marginRight: 6 }} />
          Scheduling ({schedulingConfigs.length})
        </button>
      </div>

      {activeTab !== 'scheduling' && <>
      {/* Stat cards */}
      <div style={{ display: 'flex', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-xl)', flexWrap: 'wrap' }}>
        <StatCard icon={activeTab === 'agent' ? 'fas fa-robot' : 'fas fa-server'} label={`Total ${activeTab === 'agent' ? 'Agent' : 'Backend'} Workers`} value={total} />
        <StatCard icon="fas fa-check-circle" label="Healthy" value={healthy} color="var(--color-success)" />
        <StatCard icon="fas fa-exclamation-circle" label="Unhealthy" value={unhealthy} color={unhealthy > 0 ? 'var(--color-error)' : undefined} />
        <StatCard icon="fas fa-hourglass-half" label="Draining" value={draining} color={draining > 0 ? 'var(--color-warning)' : undefined} />
        {pending > 0 && (
          <StatCard icon="fas fa-clock" label="Pending" value={pending} color="var(--color-warning)" />
        )}
        {activeTab === 'backend' && (() => {
          const clusterTotalVRAM = backendNodes.reduce((sum, n) => sum + (n.total_vram || 0), 0)
          const clusterUsedVRAM = backendNodes.reduce((sum, n) => {
            if (n.total_vram && n.available_vram != null) return sum + (n.total_vram - n.available_vram)
            return sum
          }, 0)
          const totalModelsLoaded = backendNodes.reduce((sum, n) => sum + (n.model_count || 0), 0)
          return (
            <>
              {clusterTotalVRAM > 0 && (
                <StatCard icon="fas fa-microchip" label="Cluster VRAM"
                  value={`${formatVRAM(clusterUsedVRAM) || '0'} / ${formatVRAM(clusterTotalVRAM)}`} />
              )}
              <StatCard icon="fas fa-cube" label="Models Loaded" value={totalModelsLoaded} />
            </>
          )
        })()}
      </div>

      {/* Worker tips */}
      {!loading && filteredNodes.length === 0 ? (
        <WorkerHintCard addToast={addToast} activeTab={activeTab} hasWorkers={false} />
      ) : (
        <>
          <button
            onClick={() => setShowTips(t => !t)}
            style={{
              background: 'none', border: 'none', cursor: 'pointer',
              color: 'var(--color-primary)', fontSize: '0.8125rem', fontWeight: 500,
              display: 'flex', alignItems: 'center', gap: 6,
              padding: 0, marginBottom: 'var(--spacing-md)',
            }}
          >
            <i className={`fas fa-chevron-${showTips ? 'down' : 'right'}`} style={{ fontSize: '0.625rem' }} />
            Add more workers
          </button>
          {showTips && <WorkerHintCard addToast={addToast} activeTab={activeTab} hasWorkers />}
        </>
      )}

      {/* Node table */}
      {filteredNodes.length > 0 && (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Status</th>
                <th>GPU / VRAM</th>
                <th>Last Heartbeat</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredNodes.map(node => {
                const status = statusConfig[node.status] || statusConfig.unhealthy
                const isExpanded = expandedNodeId === node.id
                const models = nodeModels[node.id]
                const backends = nodeBackends[node.id]
                const vendorLabel = gpuVendorLabel(node.gpu_vendor)
                const totalVRAMStr = formatVRAM(node.total_vram)
                const availVRAMStr = formatVRAM(node.available_vram)
                const usedVRAM = node.total_vram && node.available_vram != null
                  ? node.total_vram - node.available_vram
                  : null
                const usedVRAMStr = usedVRAM != null ? formatVRAM(usedVRAM) : null

                // RAM fallback for CPU-only workers
                const hasGPU = node.total_vram > 0
                const totalRAMStr = formatVRAM(node.total_ram)
                const usedRAM = node.total_ram && node.available_ram != null
                  ? node.total_ram - node.available_ram
                  : null
                const usedRAMStr = usedRAM != null ? formatVRAM(usedRAM) : null

                const canExpand = activeTab !== 'agent'
                return (
                  <Fragment key={node.id}>
                    <tr
                      onClick={canExpand ? () => toggleExpand(node.id) : undefined}
                      style={{ cursor: canExpand ? 'pointer' : 'default' }}
                    >
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                          <i className="fas fa-server" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }} />
                          <div>
                            <div style={{ fontWeight: 600, fontSize: '0.875rem' }}>{node.name}</div>
                            <div style={{ fontSize: '0.75rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-muted)' }}>
                              {node.address}
                            </div>
                            {node.labels && Object.keys(node.labels).length > 0 && (
                              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3, marginTop: 3 }}>
                                {Object.entries(node.labels).slice(0, 5).map(([k, v]) => (
                                  <span key={k} style={{
                                    fontSize: '0.625rem', padding: '1px 5px', borderRadius: 3,
                                    background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)',
                                    fontFamily: "'JetBrains Mono', monospace", border: '1px solid var(--color-border-subtle)',
                                  }}>{k}={v}</span>
                                ))}
                                {Object.keys(node.labels).length > 5 && (
                                  <span style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>
                                    +{Object.keys(node.labels).length - 5} more
                                  </span>
                                )}
                              </div>
                            )}
                          </div>
                        </div>
                      </td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          <i className="fas fa-circle" style={{ fontSize: '0.5rem', color: status.color }} />
                          <span style={{ fontSize: '0.8125rem', color: status.color, fontWeight: 500 }}>
                            {status.label}
                          </span>
                        </div>
                      </td>
                      <td>
                        {hasGPU && totalVRAMStr ? (
                          <div style={{ fontSize: '0.8125rem', fontFamily: "'JetBrains Mono', monospace" }}>
                            {vendorLabel && (
                              <span style={{ color: 'var(--color-text-secondary)', marginRight: 4 }}>{vendorLabel}</span>
                            )}
                            <span style={{ color: 'var(--color-text-muted)' }}>
                              {usedVRAMStr || '0'} / {totalVRAMStr}
                            </span>
                          </div>
                        ) : totalRAMStr ? (
                          <div style={{ fontSize: '0.8125rem', fontFamily: "'JetBrains Mono', monospace" }}>
                            <span style={{ color: 'var(--color-text-secondary)', marginRight: 4 }}>CPU</span>
                            <span style={{ color: 'var(--color-text-muted)' }}>
                              {usedRAMStr || '0'} / {totalRAMStr} RAM
                            </span>
                          </div>
                        ) : (
                          <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>-</span>
                        )}
                      </td>
                      <td>
                        <span style={{ fontSize: '0.8125rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--color-text-secondary)' }}>
                          {timeAgo(node.last_heartbeat)}
                        </span>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }} onClick={e => e.stopPropagation()}>
                          {node.status === 'pending' && (
                            <button
                              className="btn btn-primary btn-sm"
                              onClick={() => handleApprove(node.id)}
                              title="Approve node"
                            >
                              <i className="fas fa-check" />
                            </button>
                          )}
                          {node.status !== 'draining' && node.status !== 'pending' && (
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleDrain(node.id)}
                              title="Drain node"
                            >
                              <i className="fas fa-pause" />
                            </button>
                          )}
                          <button
                            className="btn btn-danger btn-sm"
                            onClick={() => setConfirmDelete(node)}
                            title="Remove node"
                          >
                            <i className="fas fa-trash" />
                          </button>
                        </div>
                      </td>
                    </tr>
                    {isExpanded && canExpand && (
                      <tr>
                        <td colSpan={5} style={{ padding: 0, background: 'var(--color-bg-secondary)' }}>
                          <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}>
                            <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)', color: 'var(--color-text-secondary)' }}>
                              <i className="fas fa-cube" style={{ marginRight: 6 }} />
                              Loaded Models
                            </h4>
                            {!models ? (
                              <LoadingSpinner size="sm" />
                            ) : models.length === 0 ? (
                              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>No models loaded on this node</p>
                            ) : (
                              <table className="table" style={{ margin: 0 }}>
                                <thead>
                                  <tr>
                                    <th>Model</th>
                                    <th>State</th>
                                    <th>In-Flight</th>
                                    <th style={{ width: 40 }}>Logs</th>
                                    <th style={{ textAlign: 'right' }}>Actions</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {models.map(m => {
                                    const stCfg = modelStateConfig[m.state] || modelStateConfig.idle
                                    return (
                                      <tr key={m.id || m.model_name}>
                                        <td style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}>
                                          {m.model_name}
                                        </td>
                                        <td>
                                          <span style={{
                                            display: 'inline-block', padding: '2px 8px', borderRadius: 'var(--radius-sm)',
                                            fontSize: '0.75rem', fontWeight: 500,
                                            background: stCfg.bg, color: stCfg.color, border: `1px solid ${stCfg.border}`,
                                          }}>
                                            {m.state}
                                          </span>
                                        </td>
                                        <td style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}>
                                          {m.in_flight ?? 0}
                                        </td>
                                        <td>
                                          <a
                                            href="#"
                                            onClick={(e) => {
                                              e.preventDefault()
                                              navigate(`/app/node-backend-logs/${node.id}/${encodeURIComponent(m.model_name)}`)
                                            }}
                                            style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}
                                            title="View backend logs"
                                          >
                                            <i className="fas fa-terminal" />
                                          </a>
                                        </td>
                                        <td style={{ textAlign: 'right' }}>
                                          <button
                                            className="btn btn-danger btn-sm"
                                            disabled={m.in_flight > 0}
                                            title={m.in_flight > 0 ? 'Cannot unload while serving requests' : 'Unload model'}
                                            onClick={(e) => {
                                              e.stopPropagation()
                                              if (confirm(`Unload "${m.model_name}" from ${node.name}?`)) {
                                                handleUnloadModel(node.id, m.model_name)
                                              }
                                            }}
                                          >
                                            <i className="fas fa-stop" />
                                          </button>
                                        </td>
                                      </tr>
                                    )
                                  })}
                                </tbody>
                              </table>
                            )}

                            <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginTop: 'var(--spacing-md)', marginBottom: 'var(--spacing-sm)', color: 'var(--color-text-secondary)' }}>
                              <i className="fas fa-cogs" style={{ marginRight: 6 }} />
                              Installed Backends
                            </h4>
                            {!backends ? (
                              <LoadingSpinner size="sm" />
                            ) : backends.length === 0 ? (
                              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>No backends installed on this node</p>
                            ) : (
                              <table className="table" style={{ margin: 0 }}>
                                <thead>
                                  <tr>
                                    <th>Name</th>
                                    <th>Type</th>
                                    <th>Installed At</th>
                                    <th style={{ textAlign: 'right' }}>Actions</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {backends.map(b => (
                                    <tr key={b.name}>
                                      <td style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}>
                                        {b.name}
                                      </td>
                                      <td>
                                        <span style={{
                                          display: 'inline-block', padding: '2px 8px', borderRadius: 'var(--radius-sm)',
                                          fontSize: '0.75rem', fontWeight: 500,
                                          background: b.is_system ? 'var(--color-bg-tertiary)' : 'var(--color-primary-light)',
                                          color: b.is_system ? 'var(--color-text-muted)' : 'var(--color-primary)',
                                          border: `1px solid ${b.is_system ? 'var(--color-border-subtle)' : 'var(--color-primary-border)'}`,
                                        }}>
                                          {b.is_system ? 'system' : 'gallery'}
                                        </span>
                                      </td>
                                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
                                        {b.installed_at ? timeAgo(b.installed_at) : '-'}
                                      </td>
                                      <td style={{ textAlign: 'right' }}>
                                        {!b.is_system && (
                                          <button
                                            className="btn btn-secondary btn-sm"
                                            onClick={() => handleReinstallBackend(node.id, b.name)}
                                            title="Reinstall backend"
                                          >
                                            <i className="fas fa-sync-alt" />
                                          </button>
                                        )}
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            )}

                            {/* Labels */}
                            <div style={{ marginTop: 'var(--spacing-md)' }}>
                              <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)', color: 'var(--color-text-secondary)' }}>
                                <i className="fas fa-tags" style={{ marginRight: 6 }} />
                                Labels
                              </h4>
                              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-sm)' }}>
                                {node.labels && Object.entries(node.labels).map(([k, v]) => (
                                  <span key={k} style={{
                                    display: 'inline-flex', alignItems: 'center', gap: 4,
                                    fontSize: '0.75rem', padding: '2px 8px', borderRadius: 4,
                                    background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-border-subtle)',
                                    fontFamily: "'JetBrains Mono', monospace",
                                  }}>
                                    {k}={v}
                                    <button
                                      onClick={(e) => { e.stopPropagation(); handleDeleteLabel(node.id, k) }}
                                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--color-text-muted)', fontSize: '0.625rem', padding: 0 }}
                                      title="Remove label"
                                    >
                                      <i className="fas fa-times" />
                                    </button>
                                  </span>
                                ))}
                              </div>
                              {/* Add label form */}
                              <div style={{ display: 'flex', gap: 'var(--spacing-xs)', alignItems: 'center' }}>
                                <input
                                  type="text" placeholder="key" style={{ width: 100, fontSize: '0.75rem' }}
                                  id={`label-key-${node.id}`}
                                />
                                <input
                                  type="text" placeholder="value" style={{ width: 100, fontSize: '0.75rem' }}
                                  id={`label-value-${node.id}`}
                                />
                                <button className="btn btn-secondary btn-sm" onClick={(e) => {
                                  e.stopPropagation()
                                  const key = document.getElementById(`label-key-${node.id}`).value.trim()
                                  const val = document.getElementById(`label-value-${node.id}`).value.trim()
                                  if (key) handleAddLabel(node.id, key, val)
                                }}>Add</button>
                              </div>
                            </div>
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      </>}

      {activeTab === 'scheduling' && (
        <div>
          <button className="btn btn-primary btn-sm" style={{ marginBottom: 'var(--spacing-md)' }}
            onClick={() => setShowSchedulingForm(f => !f)}>
            <i className="fas fa-plus" style={{ marginRight: 6 }} />
            Add Scheduling Rule
          </button>
          {showSchedulingForm && <SchedulingForm onSave={async (config) => {
            try {
              await nodesApi.setScheduling(config)
              fetchScheduling()
              setShowSchedulingForm(false)
              addToast('Scheduling rule saved', 'success')
            } catch (err) {
              addToast(`Failed to save rule: ${err.message}`, 'error')
            }
          }} onCancel={() => setShowSchedulingForm(false)} />}
          {schedulingConfigs.length === 0 && !showSchedulingForm ? (
            <p style={{ fontSize: '0.875rem', color: 'var(--color-text-muted)', textAlign: 'center', padding: 'var(--spacing-xl) 0' }}>
              No scheduling rules configured. Add a rule to control how models are placed on nodes.
            </p>
          ) : schedulingConfigs.length > 0 && (
            <div className="table-container">
              <table className="table">
                <thead><tr>
                  <th>Model</th>
                  <th>Node Selector</th>
                  <th>Min Replicas</th>
                  <th>Max Replicas</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr></thead>
                <tbody>
                  {schedulingConfigs.map(cfg => (
                    <tr key={cfg.id || cfg.model_name}>
                      <td style={{ fontWeight: 600, fontSize: '0.875rem' }}>{cfg.model_name}</td>
                      <td>
                        {cfg.node_selector ? (() => {
                          try {
                            const sel = typeof cfg.node_selector === 'string' ? JSON.parse(cfg.node_selector) : cfg.node_selector
                            return Object.entries(sel).map(([k,v]) => (
                              <span key={k} style={{
                                display: 'inline-block', fontSize: '0.75rem', padding: '2px 6px', borderRadius: 3,
                                background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-border-subtle)',
                                fontFamily: "'JetBrains Mono', monospace", marginRight: 4,
                              }}>{k}={v}</span>
                            ))
                          } catch { return <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>{cfg.node_selector}</span> }
                        })() : <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>Any node</span>}
                      </td>
                      <td style={{ fontFamily: "'JetBrains Mono', monospace" }}>{cfg.min_replicas || '-'}</td>
                      <td style={{ fontFamily: "'JetBrains Mono', monospace" }}>{cfg.max_replicas || 'unlimited'}</td>
                      <td style={{ textAlign: 'right' }}>
                        <button className="btn btn-danger btn-sm" onClick={async () => {
                          try {
                            await nodesApi.deleteScheduling(cfg.model_name)
                            fetchScheduling()
                            addToast('Rule deleted', 'success')
                          } catch (err) {
                            addToast(`Failed to delete rule: ${err.message}`, 'error')
                          }
                        }}><i className="fas fa-trash" /></button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={!!confirmDelete}
        title="Remove Node"
        message={confirmDelete ? `Are you sure you want to remove node "${confirmDelete.name}"? This will deregister it from the cluster.` : ''}
        confirmLabel="Remove"
        danger
        onConfirm={() => confirmDelete && handleDelete(confirmDelete.id)}
        onCancel={() => setConfirmDelete(null)}
      />
    </div>
  )
}
