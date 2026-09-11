import { useState, useEffect, useCallback, useMemo } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import ImageSelector, { useImageSelector, dockerImage, dockerFlags } from '../components/ImageSelector'
import ClusterPulse from '../components/nodes/ClusterPulse'
import AttentionCallout from '../components/nodes/AttentionCallout'
import NodePanel from '../components/nodes/NodePanel'


function StepNumber({ n, bg, color }) {
  return (
    <span className="p2p-step" style={{ background: bg, color }}>{n}</span>
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
      <button
        onClick={copy}
        className="btn btn-sm p2p-cmd__copy"
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
    <div className="card pad-lg mb-xl">
      <h3 className="panel-title">
        <i className={`fas ${hasWorkers ? 'fa-plus-circle' : 'fa-info-circle'} text-primary`} />
        {title}
      </h3>
      <p className="text-base text-secondary mb-md">
        {isAgent
          ? 'Start agent worker nodes to execute MCP tools and agent tasks. Agent workers self-register with this frontend.'
          : 'Start worker nodes to scale inference across multiple machines. Workers self-register with this frontend.'}
      </p>

      <p className="form-label">Select your hardware</p>
      <ImageSelector selected={selected} onSelect={setSelected} dev={dev} onDevChange={setDev} />

      <div className="stack">
        <div>
          <p className="form-label">CLI</p>
          <CommandBlock
            command={`local-ai ${workerCmd} \\\n  --register-to "${frontendUrl}" \\\n  --nats-url "nats://nats:4222" \\\n  --registration-token "$LOCALAI_REGISTRATION_TOKEN"`}
            addToast={addToast}
          />
        </div>
        <div>
          <p className="form-label">Docker</p>
          <CommandBlock
            command={`docker run --net host ${flagsStr}\\\n  -e LOCALAI_REGISTER_TO="${frontendUrl}" \\\n  -e LOCALAI_NATS_URL="nats://nats:4222" \\\n  -e LOCALAI_REGISTRATION_TOKEN="$TOKEN" \\\n  ${dockerImage(option, dev)} ${workerCmd}`}
            addToast={addToast}
          />
        </div>
      </div>

      <p className="text-note mt-md">
        For full setup instructions, architecture details, and Kubernetes deployment, see the{' '}
        <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer"
          className="text-primary">Distributed Mode documentation <i className="fas fa-external-link-alt text-xs" /></a>.
      </p>
    </div>
  )
}

export default function Nodes() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [nodesList, setNodesList] = useState([])
  const [allModels, setAllModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(true)
  const [confirmDelete, setConfirmDelete] = useState(null)
  const [showTips, setShowTips] = useState(false)
  const [activeTab, setActiveTab] = useState('all') // 'all' | 'backend' | 'agent'

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

  // Roster model fetch: drives the inline model chips on each backend panel
  // without an expand click. Grouped by node below.
  const fetchAllModels = useCallback(async () => {
    try {
      const d = await nodesApi.allModels()
      setAllModels(Array.isArray(d) ? d : [])
    } catch {
      setAllModels([])
    }
  }, [])

  useEffect(() => {
    fetchNodes()
    fetchAllModels()
    const interval = setInterval(() => {
      fetchNodes()
      fetchAllModels()
    }, 5000)
    return () => clearInterval(interval)
  }, [fetchNodes, fetchAllModels])

  const modelsByNode = useMemo(() => {
    const m = {}
    for (const x of allModels) (m[x.node_id] ||= []).push(x)
    return m
  }, [allModels])

  const handleDrain = async (nodeId) => {
    try {
      await nodesApi.drain(nodeId)
      addToast('Node set to draining', 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to drain node: ${err.message}`, 'error')
    }
  }

  const handleResume = async (nodeId) => {
    try {
      await nodesApi.resume(nodeId)
      addToast('Node resumed', 'success')
      fetchNodes()
    } catch (err) {
      addToast(`Failed to resume node: ${err.message}`, 'error')
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

  const handleDelete = async (nodeId) => {
    try {
      await nodesApi.delete(nodeId)
      addToast('Node removed', 'success')
      setConfirmDelete(null)
      fetchNodes()
    } catch (err) {
      addToast(`Failed to remove node: ${err.message}`, 'error')
      setConfirmDelete(null)
    }
  }

  if (loading) {
    return (
      <div className="page page--wide loading-center">
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  // Disabled state
  if (!enabled) {
    return (
      <div className="page page--wide">
        <div className="p2p-hero">
          <i className="fas fa-network-wired text-primary" />
          <h1 className="text-xl fw-semibold mb-sm">
            Distributed Mode Not Enabled
          </h1>
          <p className="text-secondary" style={{ maxWidth: 600, margin: '0 auto var(--spacing-xl)' }}>
            Enable distributed mode to manage backend nodes across multiple machines. Nodes self-register and are monitored for health, enabling horizontal scaling of model inference.
          </p>

          <div className="nodes-features mb-xl">
            <div className="card text-center pad-md">
              <div className="icon-chip icon-chip--centred tone-primary">
                <i className="fas fa-server" />
              </div>
              <h3 className="text-base fw-semibold mb-xs">Horizontal Scaling</h3>
              <p className="text-sub">Add backend nodes to scale inference capacity</p>
            </div>
            <div className="card text-center pad-md">
              <div className="icon-chip icon-chip--centred tone-accent">
                <i className="fas fa-route" />
              </div>
              <h3 className="text-base fw-semibold mb-xs">Smart Routing</h3>
              <p className="text-sub">Route requests to the best available node</p>
            </div>
            <div className="card text-center pad-md">
              <div className="icon-chip icon-chip--centred tone-success">
                <i className="fas fa-heart-pulse" />
              </div>
              <h3 className="text-base fw-semibold mb-xs">Health Monitoring</h3>
              <p className="text-sub">Automatic heartbeat checks and failover</p>
            </div>
          </div>
        </div>

        <div className="card p2p-enable pad-lg">
          <h3 className="panel-title">
            <i className="fas fa-rocket text-accent" />
            How to Enable Distributed Mode
          </h3>
          <div className="stack">
            <div className="hstack">
              <StepNumber n={1} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div className="flex-1">
                <p className="fw-medium mb-xs">Start LocalAI with distributed mode</p>
                <CommandBlock
                  command={`local-ai run --distributed \\\n  --distributed-db "postgres://user:pass@host/db" \\\n  --distributed-nats "nats://host:4222"`}
                  addToast={addToast}
                />
              </div>
            </div>
            <div className="hstack">
              <StepNumber n={2} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div className="flex-1">
                <p className="fw-medium mb-xs">Register backend nodes</p>
                <CommandBlock
                  command={`local-ai worker \\\n  --register-to "http://localai-host:8080" \\\n  --nats-url "nats://nats:4222" \\\n  --node-name "gpu-node-1"`}
                  addToast={addToast}
                />
              </div>
            </div>
            <div className="hstack">
              <StepNumber n={3} bg="var(--color-accent-light)" color="var(--color-accent)" />
              <div className="flex-1">
                <p className="fw-medium">Manage nodes from this dashboard</p>
                <p className="text-sub mt-xs">
                  Once enabled, refresh this page to see registered nodes and their health status.
                </p>
              </div>
            </div>
          <p className="text-note mt-md">
            For full setup instructions, architecture details, and Kubernetes deployment, see the{' '}
            <a href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer"
              className="text-primary">Distributed Mode documentation <i className="fas fa-external-link-alt text-xs" /></a>.
          </p>
          </div>
        </div>
      </div>
    )
  }

  // Split nodes by type
  const backendNodes = nodesList.filter(n => !n.node_type || n.node_type === 'backend')
  const agentNodes = nodesList.filter(n => n.node_type === 'agent')
  const filteredNodes = activeTab === 'all' ? nodesList
    : activeTab === 'agent' ? agentNodes : backendNodes

  return (
    <div className="page page--wide">
      <PageHeader
        title={
          <>
            <i className="fas fa-network-wired icon-before" />
            {t('nodes.title')}
          </>
        }
        supporting={t('nodes.subtitle')}
      />

      <ClusterPulse nodes={nodesList} />
      <AttentionCallout nodes={nodesList} onApprove={handleApprove} />

      {/* Node-type filter */}
      <div role="radiogroup" aria-label="Node type" className="segmented node-filter">
        {[['all', 'All'], ['backend', 'Backend'], ['agent', 'Agent']].map(([key, label]) => (
          <button key={key} type="button" role="radio" aria-checked={activeTab === key}
            className={`segmented__item${activeTab === key ? ' is-active' : ''}`}
            onClick={() => setActiveTab(key)}>{label}</button>
        ))}
      </div>

      {/* Worker tips */}
      {!loading && filteredNodes.length === 0 ? (
        <WorkerHintCard addToast={addToast} activeTab={activeTab} hasWorkers={false} />
      ) : (
        <>
          <button
            onClick={() => setShowTips(t => !t)}
            className="nodes-add-worker"
            aria-expanded={showTips}
          >
            <i className={`fas ${showTips ? 'fa-chevron-down' : 'fa-plus'}`} />
            {showTips ? 'Hide instructions' : 'Register a new worker'}
          </button>
          {showTips && <WorkerHintCard addToast={addToast} activeTab={activeTab} hasWorkers />}
        </>
      )}

      {filteredNodes.length > 0 && (
        <div className="node-roster">
          {filteredNodes.map(node => (
            <NodePanel key={node.id} node={node} models={modelsByNode[node.id] || []}
              onApprove={handleApprove} onDrain={handleDrain} onResume={handleResume}
              onRemove={(n) => setConfirmDelete(n)} />
          ))}
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
