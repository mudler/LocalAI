import { useState, useEffect, useCallback, Fragment } from 'react'
import { useOutletContext, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import ActionMenu from '../components/ActionMenu'
import SearchableModelSelect from '../components/SearchableModelSelect'
import ImageSelector, { useImageSelector, dockerImage, dockerFlags } from '../components/ImageSelector'
import StatCard from '../components/StatCard'

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

/**
 * Inline editor for a node's per-model replica capacity.
 *
 * UX intent: discoverable affordance (pencil icon) that opens an inline
 * input — never a modal for a single field. Source-of-truth note is shown
 * inline so operators understand a worker re-registration will overwrite
 * their override; surfacing this in a tooltip would hide too important a
 * caveat.
 *
 * `confirmShrink` is a hook the parent provides so the page can render its
 * own confirm dialog (it has access to all nodes and can phrase the message
 * with full context).
 */
function CapacityEditor({ node, loadedModelCounts, onUpdate, confirmShrink, addToast }) {
  const current = node.max_replicas_per_model || 1
  const isOverride = !!node.max_replicas_per_model_manually_set
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(String(current))
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)

  // Reset draft when current value changes (server response, etc.)
  useEffect(() => {
    if (!editing) setDraft(String(current))
  }, [current, editing])

  const cancel = useCallback(() => {
    setEditing(false)
    setDraft(String(current))
  }, [current])

  const save = useCallback(async () => {
    const value = parseInt(draft, 10)
    if (!Number.isFinite(value) || value < 1) {
      addToast('Replica capacity must be 1 or higher', 'error')
      return
    }
    if (value === current) {
      setEditing(false)
      return
    }
    // Reducing the cap below current loaded replicas: confirm so the operator
    // sees the consequence (running replicas keep going until idle eviction).
    const maxLoadedAcrossModels = Math.max(0, ...Object.values(loadedModelCounts || {}))
    if (value < maxLoadedAcrossModels) {
      const proceed = await confirmShrink({ node, newValue: value, currentLoaded: maxLoadedAcrossModels })
      if (!proceed) return
    }
    setSaving(true)
    try {
      await nodesApi.updateMaxReplicasPerModel(node.id, value)
      addToast(`Replica capacity set to ${value} on ${node.name}`, 'success')
      setEditing(false)
      onUpdate?.(value)
    } catch (err) {
      addToast(`Could not change replica capacity: ${err.message || err}`, 'error')
    } finally {
      setSaving(false)
    }
  }, [draft, current, node, loadedModelCounts, confirmShrink, onUpdate, addToast])

  const onKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); save() }
    else if (e.key === 'Escape') { e.preventDefault(); cancel() }
  }

  const reset = useCallback(async () => {
    setResetting(true)
    try {
      await nodesApi.resetMaxReplicasPerModel(node.id)
      addToast(`Override cleared on ${node.name}; worker flag will apply on next re-registration`, 'success')
      onUpdate?.(null)
    } catch (err) {
      addToast(`Could not reset override: ${err.message || err}`, 'error')
    } finally {
      setResetting(false)
    }
  }, [node, onUpdate, addToast])

  return (
    <div style={{
      display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-md)',
    }}>
      <i className="fas fa-layer-group" style={{ color: 'var(--color-text-muted)', marginTop: 3 }} aria-hidden="true" />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
          <label
            htmlFor={`capacity-${node.id}`}
            style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--color-text-primary)' }}
          >
            Max replicas per model
          </label>
          {editing ? (
            <>
              <input
                id={`capacity-${node.id}`}
                type="number"
                min={1}
                value={draft}
                disabled={saving}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onKeyDown}
                autoFocus
                aria-describedby={`capacity-hint-${node.id}`}
                style={{
                  width: 72, padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border)', background: 'var(--color-bg-primary)',
                  fontFamily: 'var(--font-mono)', fontSize: '0.8125rem',
                  color: 'var(--color-text-primary)',
                }}
              />
              <button
                className="btn btn-primary btn-sm"
                onClick={save}
                disabled={saving}
                style={{ minHeight: 32 }}
                aria-label="Save replica capacity"
              >
                {saving ? <LoadingSpinner size="xs" /> : <><i className="fas fa-check" /> Save</>}
              </button>
              <button
                className="btn btn-secondary btn-sm"
                onClick={cancel}
                disabled={saving}
                style={{ minHeight: 32 }}
                aria-label="Cancel"
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              <span
                className="cell-mono"
                style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}
              >
                {current}
              </span>
              {isOverride && (
                <span
                  title="This value was set from the UI. It will persist across worker restarts until you click Reset."
                  style={{
                    display: 'inline-block', fontSize: '0.6875rem', padding: '1px 6px',
                    borderRadius: 'var(--radius-sm)', fontWeight: 500,
                    background: 'var(--color-bg-primary)',
                    border: '1px solid var(--color-warning, #d97706)',
                    color: 'var(--color-warning, #d97706)',
                  }}
                >
                  override
                </span>
              )}
              <button
                onClick={() => setEditing(true)}
                aria-label={`Edit replica capacity (currently ${current})`}
                title="Change replica capacity for this node"
                style={{
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  minWidth: 32, minHeight: 32, padding: 4, borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border-subtle)',
                  background: 'transparent', color: 'var(--color-text-muted)', cursor: 'pointer',
                }}
              >
                <i className="fas fa-pencil-alt" />
              </button>
              {isOverride && (
                <button
                  onClick={reset}
                  disabled={resetting}
                  aria-label="Clear admin override and let the worker flag apply"
                  title="Clear override; the worker's --max-replicas-per-model flag will apply on the next re-registration"
                  className="btn btn-secondary btn-sm"
                  style={{ minHeight: 32 }}
                >
                  {resetting ? <LoadingSpinner size="xs" /> : <><i className="fas fa-undo" /> Reset</>}
                </button>
              )}
            </>
          )}
        </div>
        <div
          id={`capacity-hint-${node.id}`}
          style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 4, lineHeight: 1.4 }}
        >
          {isOverride
            ? <>Set from here. <strong>Reset</strong> to use the worker's default.</>
            : <>Saved values stick across worker restarts.</>}
        </div>
      </div>
    </div>
  )
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
  offline: { color: 'var(--color-error)', label: 'Offline' },
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
        fontSize: '0.8125rem', fontFamily: 'var(--font-mono)',
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
          borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-xs) var(--spacing-sm)', cursor: 'pointer',
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

// Numeric input with quick-pick preset chips. Picked over a slider because
// replica counts are exact specs (operator math), not fuzzy estimates. The
// chips give one-click access to common values without the slider's
// precision/special-value problems (e.g. MaxReplicas=0 = "no limit").
function ReplicaInput({ id, label, value, onChange, presets }) {
  return (
    <div style={{ flex: 1 }}>
      <label className="form-label" htmlFor={id}>{label}</label>
      <input
        id={id}
        className="input"
        type="number"
        min={0}
        value={value}
        onChange={e => onChange(parseInt(e.target.value) || 0)}
      />
      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginTop: 6 }}>
        {presets.map(({ v, l }) => {
          const active = value === v
          return (
            <button
              key={v}
              type="button"
              onClick={() => onChange(v)}
              aria-pressed={active}
              className="cell-mono"
              style={{
                padding: '2px 8px',
                borderRadius: 'var(--radius-sm)',
                fontSize: '0.6875rem',
                fontWeight: 500,
                cursor: 'pointer',
                background: active ? 'var(--color-primary-light)' : 'transparent',
                border: `1px solid ${active ? 'var(--color-primary-border)' : 'var(--color-border-subtle)'}`,
                color: active ? 'var(--color-primary)' : 'var(--color-text-muted)',
              }}
            >{l || v}</button>
          )
        })}
      </div>
    </div>
  )
}

/**
 * Controlled chip-builder for { key: value } maps. Replaces the prior
 * comma-separated-string Node Selector input AND the bespoke Labels editor
 * in the node drawer — both were rendering the same chip pattern with
 * subtly different markup.
 *
 * Fully controlled: parent owns the map and decides what onAdd/onRemove
 * does (form state for the scheduling form; API calls for the live
 * labels editor). The component just renders chips and a key/value input
 * row.
 *
 * Props:
 *   pairs       — current map of key → value
 *   onAdd(k,v)  — called when the user adds a pair (parent handles dedup
 *                 and persistence side effects)
 *   onRemove(k) — called when a chip's × is clicked
 *   placeholderKey, placeholderValue — input hints
 *   ariaLabel   — accessible name for the section
 */
function KeyValueChips({ pairs, onAdd, onRemove, placeholderKey = 'key', placeholderValue = 'value', ariaLabel }) {
  const [k, setK] = useState('')
  const [v, setV] = useState('')

  const add = () => {
    const key = k.trim()
    if (!key) return
    onAdd(key, v.trim())
    setK(''); setV('')
  }
  const onKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); add() }
  }

  const entries = pairs ? Object.entries(pairs) : []
  return (
    <div aria-label={ariaLabel}>
      {entries.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 'var(--spacing-xs)' }}>
          {entries.map(([key, val]) => (
            <span key={key} style={{
              display: 'inline-flex', alignItems: 'center', gap: 4,
              fontSize: '0.75rem', padding: '2px 8px',
              borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg-tertiary)',
              border: '1px solid var(--color-border-subtle)',
              fontFamily: 'var(--font-mono)',
            }}>
              {key}={val}
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onRemove(key) }}
                aria-label={`Remove ${key}`}
                title="Remove"
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: 'var(--color-text-muted)', fontSize: '0.625rem', padding: 0,
                }}
              >
                <i className="fas fa-times" />
              </button>
            </span>
          ))}
        </div>
      )}
      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', alignItems: 'stretch' }}>
        <input
          className="input"
          type="text"
          placeholder={placeholderKey}
          value={k}
          onChange={e => setK(e.target.value)}
          onKeyDown={onKeyDown}
          style={{ flex: 1 }}
        />
        <input
          className="input"
          type="text"
          placeholder={placeholderValue}
          value={v}
          onChange={e => setV(e.target.value)}
          onKeyDown={onKeyDown}
          style={{ flex: 1 }}
        />
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={add}
          disabled={!k.trim()}
          style={{ minHeight: 36 }}
        >
          <i className="fas fa-plus" /> Add
        </button>
      </div>
    </div>
  )
}

function SchedulingForm({ onSave, onCancel }) {
  const [mode, setMode] = useState('placement')
  const [modelName, setModelName] = useState('')
  // Selector is now a chip-builder map instead of a comma-separated string.
  // Operators were copying syntax from docs and missing commas; the chip UI
  // makes the key=value structure self-documenting.
  const [selector, setSelector] = useState({})
  const [minReplicas, setMinReplicas] = useState(1)
  const [maxReplicas, setMaxReplicas] = useState(0)

  const hasSelector = Object.keys(selector).length > 0

  const isValid = () => {
    if (!modelName) return false
    if (mode === 'placement') return hasSelector
    return minReplicas > 0 || maxReplicas > 0
  }

  const handleSubmit = () => {
    onSave({
      model_name: modelName,
      node_selector: hasSelector ? selector : undefined,
      min_replicas: mode === 'placement' ? 0 : minReplicas,
      max_replicas: mode === 'placement' ? 0 : maxReplicas,
    })
  }

  return (
    <div className="card" style={{ padding: 'var(--spacing-lg)', marginBottom: 'var(--spacing-md)' }}>
      {/* Mode selector \u2014 uses the project's segmented control instead of two
          50%-width filled buttons that competed visually with the actual
          primary action (Save). */}
      <div role="radiogroup" aria-label="Scheduling mode" className="segmented" style={{ marginBottom: 'var(--spacing-xs)' }}>
        <button
          type="button" role="radio" aria-checked={mode === 'placement'}
          className={`segmented__item${mode === 'placement' ? ' is-active' : ''}`}
          onClick={() => setMode('placement')}
        >
          <i className="fas fa-thumbtack" aria-hidden="true" /> Pin to nodes
        </button>
        <button
          type="button" role="radio" aria-checked={mode === 'autoscaling'}
          className={`segmented__item${mode === 'autoscaling' ? ' is-active' : ''}`}
          onClick={() => setMode('autoscaling')}
        >
          <i className="fas fa-arrows-up-down" aria-hidden="true" /> Auto-scale
        </button>
      </div>
      <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: '0 0 var(--spacing-lg) 0' }}>
        {mode === 'placement'
          ? 'Restrict this model to specific nodes. Loaded on demand, evictable when idle.'
          : 'Maintain a target replica count across the cluster. Min \u2265 1 protects from eviction.'}
      </p>

      {/* Linear vertical flow \u2014 model picker is the visual focus, then the
          mode-specific fields below. No 2-column grid (the mismatched widths
          made the form look raw). */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
        <div>
          <label className="form-label" htmlFor="sched-model">Model</label>
          {/* Searchable combobox so a long gallery doesn't force the operator
              to scroll through hundreds of entries. Free-text is allowed —
              you can pre-create a rule for a model that hasn't been
              installed yet, which is a real workflow when standing up a new
              node and pre-staging its scheduling policy. */}
          <SearchableModelSelect
            value={modelName}
            onChange={setModelName}
            placeholder="Type to search models, or paste a name..."
          />
        </div>

        <div>
          <label className="form-label">
            Node selector{mode === 'placement' ? '' : ' (optional)'}
          </label>
          <KeyValueChips
            pairs={selector}
            onAdd={(k, v) => setSelector(prev => ({ ...prev, [k]: v }))}
            onRemove={(k) => setSelector(prev => { const n = { ...prev }; delete n[k]; return n })}
            placeholderKey="key (e.g. gpu.vendor)"
            placeholderValue="value (e.g. nvidia)"
            ariaLabel="Node selector"
          />
          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', display: 'block', marginTop: 6 }}>
            {mode === 'placement'
              ? 'Models will load only on nodes that match all listed labels.'
              : (hasSelector ? 'Replicas land only on matching nodes.' : 'Empty = any healthy node.')}
          </span>
        </div>

        {mode === 'autoscaling' && (
          <div style={{ display: 'flex', gap: 'var(--spacing-md)' }}>
            <ReplicaInput
              id="sched-min"
              label="Min replicas"
              value={minReplicas}
              onChange={setMinReplicas}
              presets={[{ v: 1 }, { v: 2 }, { v: 3 }, { v: 4 }]}
            />
            <ReplicaInput
              id="sched-max"
              label="Max replicas"
              value={maxReplicas}
              onChange={setMaxReplicas}
              presets={[{ v: 0, l: 'no limit' }, { v: 2 }, { v: 4 }, { v: 8 }]}
            />
          </div>
        )}
      </div>

      {/* Hairline divider above the actions, matching the project's form pattern. */}
      <div style={{
        display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end',
        marginTop: 'var(--spacing-lg)', paddingTop: 'var(--spacing-md)',
        borderTop: '1px solid var(--color-border-subtle)',
      }}>
        <button className="btn btn-secondary btn-sm" onClick={onCancel}>Cancel</button>
        <button className="btn btn-primary btn-sm" onClick={handleSubmit} disabled={!isValid()}>Save rule</button>
      </div>
    </div>
  )
}

export default function Nodes() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('admin')
  const [nodesList, setNodesList] = useState([])
  const [loading, setLoading] = useState(true)
  const [enabled, setEnabled] = useState(true)
  const [expandedNodeId, setExpandedNodeId] = useState(null)
  const [nodeModels, setNodeModels] = useState({})
  const [nodeBackends, setNodeBackends] = useState({})
  const [confirmDelete, setConfirmDelete] = useState(null)
  const [confirmUnload, setConfirmUnload] = useState(null)
  const [confirmDeleteBackend, setConfirmDeleteBackend] = useState(null)
  // Capacity-shrink confirm uses a Promise resolver so the editor can `await`
  // the user's choice. Pattern matches the rest of the page where confirms
  // open a ConfirmDialog and the action proceeds in onConfirm.
  const [confirmShrinkState, setConfirmShrinkState] = useState(null)
  const confirmShrink = useCallback(({ node, newValue, currentLoaded }) => {
    return new Promise((resolve) => {
      setConfirmShrinkState({ node, newValue, currentLoaded, resolve })
    })
  }, [])
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

  const handleUpgradeBackend = async (nodeId, backendName) => {
    try {
      await nodesApi.installBackend(nodeId, backendName)
      addToast(`Backend "${backendName}" upgraded`, 'success')
      fetchBackends(nodeId)
    } catch (err) {
      addToast(`Failed to upgrade backend: ${err.message}`, 'error')
    }
  }

  const handleDeleteBackendOnNode = async (nodeId, backendName) => {
    try {
      await nodesApi.deleteBackend(nodeId, backendName)
      addToast(`Backend "${backendName}" deleted`, 'success')
      fetchBackends(nodeId)
    } catch (err) {
      addToast(`Failed to delete backend: ${err.message}`, 'error')
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
      <div className="page page--wide" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  // Disabled state
  if (!enabled) {
    return (
      <div className="page page--wide">
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
  const unhealthy = filteredNodes.filter(n => n.status === 'unhealthy' || n.status === 'offline').length
  const draining = filteredNodes.filter(n => n.status === 'draining').length
  const pending = filteredNodes.filter(n => n.status === 'pending').length

  return (
    <div className="page page--wide">
      <div className="page-header">
        <h1 className="page-title">
          <i className="fas fa-network-wired" style={{ marginRight: 'var(--spacing-sm)' }} />
          {t('nodes.title')}
        </h1>
        <p className="page-subtitle">
          {t('nodes.subtitle')}
        </p>
      </div>

      {/* Tabs */}
      <div className="tabs" style={{ marginBottom: 'var(--spacing-lg)' }}>
        <button
          onClick={() => setActiveTab('backend')}
          className={`tab ${activeTab === 'backend' ? 'tab-active' : ''}`}
        >
          <i className="fas fa-server" style={{ marginRight: 6 }} />
          Backend Workers ({backendNodes.length})
        </button>
        <button
          onClick={() => setActiveTab('agent')}
          className={`tab ${activeTab === 'agent' ? 'tab-active' : ''}`}
        >
          <i className="fas fa-robot" style={{ marginRight: 6 }} />
          Agent Workers ({agentNodes.length})
        </button>
        <button
          onClick={() => setActiveTab('scheduling')}
          className={`tab ${activeTab === 'scheduling' ? 'tab-active' : ''}`}
        >
          <i className="fas fa-calendar-alt" style={{ marginRight: 6 }} />
          Scheduling ({schedulingConfigs.length})
        </button>
      </div>

      {activeTab !== 'scheduling' && <>
      {/* Stat cards */}
      <div className="stat-grid">
        <StatCard icon={activeTab === 'agent' ? 'fas fa-robot' : 'fas fa-server'}
          label={`Total ${activeTab === 'agent' ? 'Agent' : 'Backend'} Workers`} value={total} />
        <StatCard icon="fas fa-check-circle" label="Healthy" value={healthy}
          accentVar={healthy > 0 ? '--color-success' : undefined} />
        <StatCard icon="fas fa-exclamation-circle" label="Unhealthy" value={unhealthy}
          accentVar={unhealthy > 0 ? '--color-error' : undefined} />
        <StatCard icon="fas fa-hourglass-half" label="Draining" value={draining}
          accentVar={draining > 0 ? '--color-warning' : undefined} />
        {pending > 0 && (
          <StatCard icon="fas fa-clock" label="Pending" value={pending} accentVar="--color-warning" />
        )}
        {activeTab === 'backend' && (() => {
          const clusterTotalVRAM = backendNodes.reduce((sum, n) => sum + (n.total_vram || 0), 0)
          const clusterUsedVRAM = backendNodes.reduce((sum, n) => {
            if (n.total_vram && n.available_vram != null) return sum + (n.total_vram - n.available_vram)
            return sum
          }, 0)
          const totalModelsLoaded = backendNodes.reduce((sum, n) => sum + (n.model_count || 0), 0)
          const totalInFlight = backendNodes.reduce((sum, n) => sum + (n.in_flight_count || 0), 0)
          return (
            <>
              {clusterTotalVRAM > 0 && (
                <StatCard icon="fas fa-microchip" label="Cluster VRAM"
                  value={`${formatVRAM(clusterUsedVRAM) || '0'} / ${formatVRAM(clusterTotalVRAM)}`} />
              )}
              <StatCard icon="fas fa-cube" label="Models Loaded" value={totalModelsLoaded} />
              <StatCard icon="fas fa-exchange-alt" label="In-Flight Requests" value={totalInFlight}
                accentVar={totalInFlight > 0 ? '--color-primary' : undefined} />
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
            className="nodes-add-worker"
            aria-expanded={showTips}
          >
            <i className={`fas ${showTips ? 'fa-chevron-down' : 'fa-plus'}`} />
            {showTips ? 'Hide instructions' : 'Register a new worker'}
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
                          {canExpand && (
                            <span className={`row-chevron${isExpanded ? ' is-expanded' : ''}`} aria-hidden="true">
                              <i className="fas fa-chevron-right" />
                            </span>
                          )}
                          <i className="fas fa-server" style={{ color: 'var(--color-text-muted)', fontSize: 'var(--text-sm)' }} />
                          <div>
                            <div style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>
                              {node.name}
                              {node.node_type !== 'agent' && (() => {
                                // Slot count only applies to backend workers — agents don't
                                // load models. Always render for backend nodes so operators
                                // discover the field; muted (border-only) at the default of 1,
                                // accented when > 1 so fat nodes stand out at a glance.
                                const slots = node.max_replicas_per_model || 1
                                const isMulti = slots > 1
                                return (
                                  <span
                                    className="cell-mono"
                                    title={isMulti
                                      ? `Up to ${slots} replicas of any one model can run on this node`
                                      : 'Single replica per model (default). Click the row to expand and change.'}
                                    style={{
                                      marginLeft: 8, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                                      background: isMulti ? 'var(--color-bg-tertiary)' : 'transparent',
                                      border: `1px solid ${isMulti ? 'var(--color-border)' : 'var(--color-border-subtle)'}`,
                                      fontSize: '0.6875rem', fontWeight: 500,
                                      color: isMulti ? 'var(--color-text-secondary)' : 'var(--color-text-muted)',
                                    }}
                                  >
                                    <i className="fas fa-layer-group" style={{ marginRight: 4 }} />
                                    {slots}× slots
                                  </span>
                                )
                              })()}
                            </div>
                            <div className="cell-mono cell-muted">
                              {node.address}
                            </div>
                            {node.labels && Object.keys(node.labels).length > 0 && (() => {
                              // node.replica-slots is already shown structurally by the
                              // slot badge above; surfacing it again as a label is noise.
                              const visible = Object.entries(node.labels).filter(([k]) => k !== 'node.replica-slots')
                              if (visible.length === 0) return null
                              return (
                              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3, marginTop: 3 }}>
                                {visible.slice(0, 5).map(([k, v]) => (
                                  <span key={k} className="cell-mono" style={{
                                    padding: '1px 5px', borderRadius: "var(--radius-sm)",
                                    background: 'var(--color-bg-tertiary)',
                                    border: '1px solid var(--color-border-subtle)',
                                  }}>{k}={v}</span>
                                ))}
                                {visible.length > 5 && (
                                  <span className="cell-muted">
                                    +{visible.length - 5} more
                                  </span>
                                )}
                              </div>
                              )
                            })()}
                          </div>
                        </div>
                      </td>
                      <td>
                        <span className="node-status" style={{ color: status.color }}>
                          <span className="node-status__dot" style={{ background: status.color }} />
                          {status.label}
                        </span>
                      </td>
                      <td>
                        {hasGPU && totalVRAMStr ? (
                          <div style={{ fontSize: '0.8125rem', fontFamily: 'var(--font-mono)' }}>
                            {vendorLabel && (
                              <span style={{ color: 'var(--color-text-secondary)', marginRight: 4 }}>{vendorLabel}</span>
                            )}
                            <span style={{ color: 'var(--color-text-muted)' }}>
                              {usedVRAMStr || '0'} / {totalVRAMStr}
                            </span>
                            {/* In-tick soft reservation: deducted at scheduling time, reset by the worker's next heartbeat. */}
                            {node.reserved_vram > 0 && (
                              <span
                                title={`${formatVRAM(node.reserved_vram)} reserved by in-flight scheduling decisions; resets on next heartbeat`}
                                style={{ color: 'var(--color-warning, #d97706)', marginLeft: 6 }}
                              >
                                +{formatVRAM(node.reserved_vram)} reserved
                              </span>
                            )}
                          </div>
                        ) : totalRAMStr ? (
                          <div style={{ fontSize: '0.8125rem', fontFamily: 'var(--font-mono)' }}>
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
                        <span style={{ fontSize: '0.8125rem', fontFamily: 'var(--font-mono)', color: 'var(--color-text-secondary)' }}>
                          {timeAgo(node.last_heartbeat)}
                        </span>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <div className="row-actions" onClick={e => e.stopPropagation()}>
                          {/* Approve stays as a prominent primary button — it's
                              a stateful admission gate, not a routine action,
                              and matches how /manage surfaces install-time
                              decisions outside the kebab menu. */}
                          {node.status === 'pending' && (
                            <button
                              className="btn btn-primary btn-sm"
                              onClick={() => handleApprove(node.id)}
                            >
                              <i className="fas fa-check" /> Approve
                            </button>
                          )}
                          <ActionMenu
                            ariaLabel={`Actions for ${node.name}`}
                            triggerLabel={`Actions for ${node.name}`}
                            items={[
                              { key: 'resume', icon: 'fa-play', label: 'Resume',
                                onClick: () => handleResume(node.id),
                                hidden: node.status !== 'draining' },
                              { key: 'drain', icon: 'fa-pause', label: 'Drain',
                                onClick: () => handleDrain(node.id),
                                hidden: node.status === 'draining' || node.status === 'pending' },
                              { divider: true, hidden: node.status === 'pending' },
                              { key: 'remove', icon: 'fa-trash', label: 'Remove from cluster',
                                onClick: () => setConfirmDelete(node), danger: true },
                            ]}
                          />
                        </div>
                      </td>
                    </tr>
                    {isExpanded && canExpand && (
                      <tr>
                        <td colSpan={5} style={{ padding: 0, background: 'var(--color-bg-secondary)' }}>
                          <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}>
                            {/* The at-a-glance: what's running here? Empty
                                state is a single thin line so an empty node
                                doesn't render a giant placeholder box; the
                                row's slot badge already conveys the
                                node-level state. */}
                            {!models ? (
                              <LoadingSpinner size="sm" />
                            ) : models.length === 0 ? (
                              <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: '0 0 var(--spacing-md) 0' }}>
                                <i className="fas fa-cube" style={{ marginRight: 6, opacity: 0.6 }} aria-hidden="true" />
                                No models loaded yet — they'll appear here when scheduled to this node.
                              </p>
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
                                  {(() => {
                                    // Pre-compute per-model replica counts so the disambiguation
                                    // pill only renders when this node actually hosts >1 replica
                                    // of the same model. Single-replica deployments stay clean.
                                    const replicaCounts = {}
                                    models.forEach(m => { replicaCounts[m.model_name] = (replicaCounts[m.model_name] || 0) + 1 })
                                    return models.map(m => {
                                      const stCfg = modelStateConfig[m.state] || modelStateConfig.idle
                                      const showReplica = (replicaCounts[m.model_name] || 0) > 1
                                      // Per-replica process key — what the worker stores logs under and what the
                                      // store's GetLines/Subscribe match on for replica-scoped filtering.
                                      const processKey = `${m.model_name}#${m.replica_index ?? 0}`
                                      return (
                                      <tr key={m.id || `${m.model_name}#${m.replica_index ?? 0}`}>
                                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                                          {m.model_name}
                                          {showReplica && (
                                            <span
                                              className="cell-mono"
                                              aria-label={`replica ${m.replica_index ?? 0}`}
                                              title={`Replica ${m.replica_index ?? 0} on this node`}
                                              style={{
                                                marginLeft: 8, padding: '1px 6px', borderRadius: 'var(--radius-sm)',
                                                background: 'var(--color-bg-tertiary)',
                                                border: '1px solid var(--color-border-subtle)',
                                                fontSize: '0.6875rem', fontWeight: 500,
                                                color: 'var(--color-text-secondary)',
                                              }}
                                            >
                                              rep {m.replica_index ?? 0}
                                            </span>
                                          )}
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
                                        <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
                                          {m.in_flight ?? 0}
                                        </td>
                                        <td>
                                          <a
                                            href="#"
                                            onClick={(e) => {
                                              e.preventDefault()
                                              // Send the replica-scoped process key (modelName#replicaIndex).
                                              // The worker's BackendLogStore returns only this replica's lines
                                              // when given the full key; a future "merged" toggle in the logs
                                              // page can navigate to the bare modelName URL to use aggregation.
                                              navigate(`/app/node-backend-logs/${node.id}/${encodeURIComponent(processKey)}`)
                                            }}
                                            style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}
                                            title={showReplica ? `View backend logs for replica ${m.replica_index ?? 0}` : 'View backend logs'}
                                          >
                                            <i className="fas fa-terminal" />
                                          </a>
                                        </td>
                                        <td style={{ textAlign: 'right' }}>
                                          <button
                                            className="btn btn-danger btn-sm"
                                            title={m.in_flight > 0 ? 'Unload model (has in-flight requests)' : 'Unload model'}
                                            onClick={(e) => {
                                              e.stopPropagation()
                                              setConfirmUnload({
                                                nodeId: node.id,
                                                nodeName: node.name,
                                                modelName: m.model_name,
                                                inFlight: m.in_flight ?? 0,
                                              })
                                            }}
                                          >
                                            <i className="fas fa-stop" />
                                          </button>
                                        </td>
                                      </tr>
                                    )
                                  })
                                  })()}
                                </tbody>
                              </table>
                            )}

                            {/* Manage drawer: collapses three rarely-touched
                                config zones (capacity, backends, labels)
                                behind one disclosure so routine inspections
                                stay focused on what's loaded above. Each
                                zone gets a small eyebrow label instead of an
                                h4 to avoid creating parallel hierarchies
                                inside the disclosed area. */}
                            <details className="node-manage" style={{ marginTop: 'var(--spacing-md)' }} onClick={e => e.stopPropagation()}>
                              <summary style={{
                                cursor: 'pointer', listStyle: 'none',
                                fontSize: '0.8125rem', fontWeight: 600,
                                color: 'var(--color-text-secondary)',
                                padding: 'var(--spacing-xs) 0',
                                display: 'inline-flex', alignItems: 'center', gap: 'var(--spacing-xs)',
                              }}>
                                <i className="fas fa-chevron-right node-manage__chevron" aria-hidden="true" />
                                <i className="fas fa-sliders" aria-hidden="true" />
                                Manage
                              </summary>
                              <div style={{ paddingTop: 'var(--spacing-md)', display: 'flex', flexDirection: 'column', gap: 'var(--spacing-lg)' }}>
                                {/* Capacity */}
                                <div>
                                  <div className="drawer-eyebrow">Capacity</div>
                                  <CapacityEditor
                                    node={node}
                                    loadedModelCounts={(() => {
                                      // {modelName: replicaCount} so confirm-shrink
                                      // can warn if reducing the cap below the actual
                                      // count of any single model on this node.
                                      const counts = {}
                                      ;(models || []).forEach(m => {
                                        if (m.state === 'loaded') counts[m.model_name] = (counts[m.model_name] || 0) + 1
                                      })
                                      return counts
                                    })()}
                                    confirmShrink={confirmShrink}
                                    addToast={addToast}
                                    onUpdate={() => fetchNodes()}
                                  />
                                </div>

                                {/* Backends */}
                                <div>
                                  <div style={{
                                    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                                    marginBottom: 'var(--spacing-sm)',
                                  }}>
                                    <div className="drawer-eyebrow" style={{ margin: 0 }}>Backends</div>
                                    <button
                                      type="button"
                                      className="btn btn-secondary btn-sm"
                                      onClick={(e) => {
                                        e.stopPropagation()
                                        // Hand off to the gallery in target-node mode.
                                        // The Backends page reads ?target=<id> and
                                        // scopes its install action to this node —
                                        // one gallery, two scopes, no duplicate UI.
                                        navigate(`/app/backends?target=${encodeURIComponent(node.id)}`)
                                      }}
                                      title={`Install a backend on ${node.name}`}
                                    >
                                      <i className="fas fa-plus" /> Add backend
                                    </button>
                                  </div>
                                  {!backends ? (
                                    <LoadingSpinner size="sm" />
                                  ) : backends.length === 0 ? (
                                    <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: 0 }}>
                                      None installed. <a href="#" style={{ color: 'var(--color-primary)' }} onClick={(e) => { e.preventDefault(); e.stopPropagation(); navigate(`/app/backends?target=${encodeURIComponent(node.id)}`) }}>Install one from the gallery</a> to schedule models here.
                                    </p>
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
                                            <td style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8125rem' }}>
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
                                                <div style={{ display: 'inline-flex', gap: 'var(--spacing-xs)' }}>
                                                  <button
                                                    className="btn btn-secondary btn-sm"
                                                    onClick={() => handleUpgradeBackend(node.id, b.name)}
                                                    title="Upgrade backend on this node"
                                                  >
                                                    <i className="fas fa-arrow-up" />
                                                  </button>
                                                  <button
                                                    className="btn btn-danger-ghost btn-sm"
                                                    onClick={() => setConfirmDeleteBackend({ nodeId: node.id, nodeName: node.name, backend: b.name })}
                                                    title="Delete backend from this node"
                                                  >
                                                    <i className="fas fa-trash" />
                                                  </button>
                                                </div>
                                              )}
                                            </td>
                                          </tr>
                                        ))}
                                      </tbody>
                                    </table>
                                  )}
                                </div>

                                {/* Labels — same chip-builder as the scheduling
                                    form, but onAdd/onRemove fire API calls
                                    instead of mutating form state. node.replica-slots
                                    is filtered out so the Capacity editor stays
                                    the single source of truth for that label. */}
                                <div>
                                  <div className="drawer-eyebrow">Labels</div>
                                  <KeyValueChips
                                    pairs={node.labels ? Object.fromEntries(Object.entries(node.labels).filter(([k]) => k !== 'node.replica-slots')) : {}}
                                    onAdd={(k, v) => handleAddLabel(node.id, k, v)}
                                    onRemove={(k) => handleDeleteLabel(node.id, k)}
                                    placeholderKey="key"
                                    placeholderValue="value"
                                    ariaLabel={`Labels for ${node.name}`}
                                  />
                                </div>
                              </div>
                            </details>
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
                  <th>Mode</th>
                  <th>Node Selector</th>
                  <th>Min Replicas</th>
                  <th>Max Replicas</th>
                  <th>Status</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr></thead>
                <tbody>
                  {schedulingConfigs.map(cfg => {
                    const isAutoScaling = cfg.min_replicas > 0 || cfg.max_replicas > 0
                    const hasSelector = !!cfg.node_selector
                    const modeLabel = isAutoScaling ? 'Auto-scaling' : hasSelector ? 'Placement' : 'Inactive'
                    const modeColor = isAutoScaling ? 'var(--color-success)' : hasSelector ? 'var(--color-primary)' : 'var(--color-text-muted)'
                    // Cooldown: reconciler tripped the circuit breaker because cluster
                    // capacity is exhausted. Surface so the operator sees it instead
                    // of the model silently failing to scale.
                    const unsatisfiableUntil = cfg.unsatisfiable_until ? new Date(cfg.unsatisfiable_until) : null
                    const isUnsatisfiable = unsatisfiableUntil && unsatisfiableUntil.getTime() > Date.now()
                    return (
                    <tr key={cfg.id || cfg.model_name}>
                      <td style={{ fontWeight: 600, fontSize: '0.875rem' }}>{cfg.model_name}</td>
                      <td>
                        <span style={{
                          display: 'inline-block', fontSize: '0.75rem', padding: '2px 8px', borderRadius: "var(--radius-sm)",
                          background: 'var(--color-bg-tertiary)', border: `1px solid ${modeColor}`,
                          color: modeColor, fontWeight: 600,
                        }}>{modeLabel}</span>
                      </td>
                      <td>
                        {cfg.node_selector ? (() => {
                          try {
                            const sel = typeof cfg.node_selector === 'string' ? JSON.parse(cfg.node_selector) : cfg.node_selector
                            return Object.entries(sel).map(([k,v]) => (
                              <span key={k} style={{
                                display: 'inline-block', fontSize: '0.75rem', padding: '2px 6px', borderRadius: "var(--radius-sm)",
                                background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-border-subtle)',
                                fontFamily: 'var(--font-mono)', marginRight: 4,
                              }}>{k}={v}</span>
                            ))
                          } catch { return <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>{cfg.node_selector}</span> }
                        })() : <span style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>Any node</span>}
                      </td>
                      <td style={{ fontFamily: 'var(--font-mono)' }}>
                        {isAutoScaling ? cfg.min_replicas : '-'}
                      </td>
                      <td style={{ fontFamily: 'var(--font-mono)' }}>
                        {isAutoScaling ? (cfg.max_replicas || 'no limit') : '-'}
                      </td>
                      <td>
                        {isUnsatisfiable ? (
                          <span
                            title={`Reconciler couldn't satisfy this rule (capacity exhausted). Will retry by ${unsatisfiableUntil.toLocaleString()}, or sooner on a node lifecycle change.`}
                            style={{
                              display: 'inline-block', fontSize: '0.75rem', padding: '2px 8px',
                              borderRadius: 'var(--radius-sm)', fontWeight: 600,
                              background: 'var(--color-bg-tertiary)',
                              border: '1px solid var(--color-warning, #d97706)',
                              color: 'var(--color-warning, #d97706)',
                            }}
                          >
                            <i className="fas fa-exclamation-triangle" style={{ marginRight: 4 }} />
                            Unsatisfiable until {unsatisfiableUntil.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                          </span>
                        ) : (
                          <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>OK</span>
                        )}
                      </td>
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
                    )
                  })}
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

      <ConfirmDialog
        open={!!confirmDeleteBackend}
        title="Delete Backend"
        message={confirmDeleteBackend ? `Delete "${confirmDeleteBackend.backend}" from ${confirmDeleteBackend.nodeName}? This removes the backend files from this node only.` : ''}
        confirmLabel="Delete"
        danger
        onConfirm={() => {
          if (confirmDeleteBackend) {
            handleDeleteBackendOnNode(confirmDeleteBackend.nodeId, confirmDeleteBackend.backend)
          }
          setConfirmDeleteBackend(null)
        }}
        onCancel={() => setConfirmDeleteBackend(null)}
      />

      <ConfirmDialog
        open={!!confirmUnload}
        title="Unload Model"
        message={
          confirmUnload
            ? confirmUnload.inFlight > 0
              ? `"${confirmUnload.modelName}" on ${confirmUnload.nodeName} currently has ${confirmUnload.inFlight} in-flight request(s). Unloading will interrupt them. Continue?`
              : `Unload "${confirmUnload.modelName}" from ${confirmUnload.nodeName}?`
            : ''
        }
        confirmLabel="Unload"
        danger={confirmUnload?.inFlight > 0}
        onConfirm={() => {
          if (confirmUnload) {
            handleUnloadModel(confirmUnload.nodeId, confirmUnload.modelName)
          }
          setConfirmUnload(null)
        }}
        onCancel={() => setConfirmUnload(null)}
      />

      <ConfirmDialog
        open={!!confirmShrinkState}
        title="Reduce replica capacity"
        message={
          confirmShrinkState
            ? `${confirmShrinkState.node.name} currently has ${confirmShrinkState.currentLoaded} replica(s) of at least one model loaded. Reducing the cap to ${confirmShrinkState.newValue} won't evict anything immediately — running replicas keep going, but the reconciler will trim down on the next idle window. Continue?`
            : ''
        }
        confirmLabel="Reduce"
        onConfirm={() => {
          confirmShrinkState?.resolve(true)
          setConfirmShrinkState(null)
        }}
        onCancel={() => {
          confirmShrinkState?.resolve(false)
          setConfirmShrinkState(null)
        }}
      />
    </div>
  )
}
