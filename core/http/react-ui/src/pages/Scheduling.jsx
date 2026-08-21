import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import ResponsiveTable from '../components/ResponsiveTable'
import SearchableModelSelect from '../components/SearchableModelSelect'
import KeyValueChips from '../components/nodes/KeyValueChips'

// Numeric input with quick-pick preset chips. Picked over a slider because
// replica counts are exact specs (operator math), not fuzzy estimates. The
// chips give one-click access to common values without the slider's
// precision/special-value problems (e.g. MaxReplicas=0 = "no limit").
function ReplicaInput({ id, label, value, onChange, presets }) {
  return (
    <div className="flex-1">
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

function selectorObject(value) {
  if (!value) return {}
  if (typeof value === 'object') return value
  try { return JSON.parse(value) } catch { return {} }
}

function configMode(config) {
  if (!config) return 'placement'
  if (config.spread_all) return 'spread'
  if (config.min_replicas > 0 || config.max_replicas > 0) return 'autoscaling'
  return 'placement'
}

function SchedulingForm({ initialConfig, onSave, onCancel }) {
  const [mode, setMode] = useState(() => configMode(initialConfig))
  const [modelName, setModelName] = useState(initialConfig?.model_name || '')
  // Selector is now a chip-builder map instead of a comma-separated string.
  // Operators were copying syntax from docs and missing commas; the chip UI
  // makes the key=value structure self-documenting.
  const [selector, setSelector] = useState(() => selectorObject(initialConfig?.node_selector))
  const [minReplicas, setMinReplicas] = useState(initialConfig?.min_replicas ?? 1)
  const [maxReplicas, setMaxReplicas] = useState(initialConfig?.max_replicas ?? 0)
  // Prefix-cache routing controls. Empty routePolicy means "inherit the
  // cluster default"; the three thresholds at 0 likewise inherit, so they
  // stay out of the POST body's effective override only when explicitly set.
  const [routePolicy, setRoutePolicy] = useState(initialConfig?.route_policy || '')
  const [balanceAbsThreshold, setBalanceAbsThreshold] = useState(initialConfig?.balance_abs_threshold ?? 0)
  const [balanceRelThreshold, setBalanceRelThreshold] = useState(initialConfig?.balance_rel_threshold ?? 0)
  const [minPrefixMatch, setMinPrefixMatch] = useState(initialConfig?.min_prefix_match ?? 0)

  const hasSelector = Object.keys(selector).length > 0

  const isValid = () => {
    if (!modelName) return false
    if (mode === 'placement') return hasSelector
    if (mode === 'spread') return true
    return minReplicas > 0 || maxReplicas > 0
  }

  const handleSubmit = () => {
    onSave({
      model_name: modelName,
      node_selector: hasSelector ? selector : undefined,
      min_replicas: mode === 'autoscaling' ? minReplicas : 0,
      max_replicas: mode === 'autoscaling' ? maxReplicas : 0,
      spread_all: mode === 'spread',
      route_policy: routePolicy,
      balance_abs_threshold: balanceAbsThreshold,
      balance_rel_threshold: balanceRelThreshold,
      min_prefix_match: minPrefixMatch,
    })
  }

  return (
    <div className="card scheduling-form">
      {/* Mode selector — uses the project's segmented control instead of two
          50%-width filled buttons that competed visually with the actual
          primary action (Save). */}
      <div role="radiogroup" aria-label="Scheduling mode" className="segmented mb-xs">
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
        <button
          type="button" role="radio" aria-checked={mode === 'spread'}
          className={`segmented__item${mode === 'spread' ? ' is-active' : ''}`}
          onClick={() => setMode('spread')}
        >
          <i className="fas fa-network-wired" aria-hidden="true" /> Spread to all
        </button>
      </div>
      <p style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', margin: '0 0 var(--spacing-lg) 0' }}>
        {mode === 'placement'
          ? 'Restrict this model to specific nodes. Loaded on demand, evictable when idle.'
          : mode === 'spread'
          ? 'Run one replica on every node matching the selector (all healthy nodes when empty). Tracks nodes joining and leaving.'
          : 'Maintain a target replica count across the cluster. Min ≥ 1 protects from eviction.'}
      </p>

      {/* Linear vertical flow — model picker is the visual focus, then the
          mode-specific fields below. No 2-column grid (the mismatched widths
          made the form look raw). */}
      <div className="stack">
        <div>
          <label className="form-label" htmlFor="sched-model">Model</label>
          {/* Searchable combobox so a long gallery doesn't force the operator
              to scroll through hundreds of entries. Free-text is allowed —
              you can pre-create a rule for a model that hasn't been
              installed yet, which is a real workflow when standing up a new
              node and pre-staging its scheduling policy. */}
          {initialConfig ? (
            <input id="sched-model" className="input scheduling-model-locked" value={modelName} readOnly />
          ) : (
            <SearchableModelSelect
              value={modelName}
              onChange={setModelName}
              placeholder="Type to search models, or paste a name..."
            />
          )}
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
          <span className="text-meta d-block mt-xs">
            {mode === 'placement'
              ? 'Models will load only on nodes that match all listed labels.'
              : (hasSelector ? 'Replicas land only on matching nodes.' : 'Empty = any healthy node.')}
          </span>
        </div>

        {mode === 'autoscaling' && (
          <div className="hstack hstack--md">
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

        {/* Per-model routing policy. Left empty/zero these inherit the
            cluster-wide defaults; set them to override how requests for this
            model are spread across replicas. */}
        <div>
          <label className="form-label" htmlFor="sched-route-policy">Routing policy</label>
          <select
            id="sched-route-policy"
            className="input"
            value={routePolicy}
            onChange={e => setRoutePolicy(e.target.value)}
          >
            <option value="">Default (cluster setting)</option>
            <option value="round_robin">Round Robin</option>
            <option value="prefix_cache">Prefix Cache</option>
          </select>
          <span className="text-meta d-block mt-xs">
            Prefix Cache routes shared-prefix requests to the same replica to reuse its KV cache, falling back to round-robin when replicas are imbalanced.
          </span>
        </div>

        {routePolicy === 'prefix_cache' && (
          <div className="hstack hstack--md">
            <div className="flex-1">
              <label className="form-label" htmlFor="sched-min-prefix-match">Min prefix match</label>
              <input
                id="sched-min-prefix-match"
                className="input"
                type="number"
                step="0.05"
                min="0"
                max="1"
                value={minPrefixMatch}
                onChange={e => setMinPrefixMatch(parseFloat(e.target.value) || 0)}
              />
              <span className="text-meta d-block mt-xs">
                Fraction of the prompt (0..1) that must match a cached prefix before affinity kicks in. 0 inherits the default.
              </span>
            </div>
            <div className="flex-1">
              <label className="form-label" htmlFor="sched-balance-abs">Balance abs threshold</label>
              <input
                id="sched-balance-abs"
                className="input"
                type="number"
                min="0"
                value={balanceAbsThreshold}
                onChange={e => setBalanceAbsThreshold(parseInt(e.target.value) || 0)}
              />
              <span className="text-meta d-block mt-xs">
                Max absolute in-flight gap allowed before falling back to round-robin. 0 inherits the default.
              </span>
            </div>
            <div className="flex-1">
              <label className="form-label" htmlFor="sched-balance-rel">Balance rel threshold</label>
              <input
                id="sched-balance-rel"
                className="input"
                type="number"
                step="0.1"
                min="0"
                value={balanceRelThreshold}
                onChange={e => setBalanceRelThreshold(parseFloat(e.target.value) || 0)}
              />
              <span className="text-meta d-block mt-xs">
                Max relative in-flight ratio (&gt;= 1) allowed before falling back to round-robin. 0 inherits the default.
              </span>
            </div>
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

const INITIAL_NODE_LIMIT = 5
const NODE_LIMIT_STEP = 20

function fuzzyIncludes(text, term) {
  if (text.includes(term)) return true
  let termIndex = 0
  for (const character of text) {
    if (character === term[termIndex]) termIndex++
    if (termIndex === term.length) return true
  }
  return false
}

function matchesNode(node, query) {
  const terms = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
  if (!terms.length) return true
  const labels = Object.entries(node.labels || {})
  const haystack = [
    node.name,
    node.id,
    ...labels.flatMap(([key, value]) => [key, String(value), `${key}=${value}`]),
  ].filter(Boolean).join(' ').toLocaleLowerCase()
  return terms.every(term => fuzzyIncludes(haystack, term))
}

function NodeLabelReference() {
  const [expanded, setExpanded] = useState(true)
  const [nodes, setNodes] = useState([])
  const [query, setQuery] = useState('')
  const [visibleLimit, setVisibleLimit] = useState(INITIAL_NODE_LIMIT)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const fetchNodes = useCallback(async () => {
    setLoading(true)
    setError(false)
    try {
      const data = await nodesApi.list()
      setNodes(Array.isArray(data) ? data : [])
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchNodes() }, [fetchNodes])

  const filtered = nodes.filter(node => matchesNode(node, query))
  const visible = filtered.slice(0, visibleLimit)
  const updateQuery = event => {
    setQuery(event.target.value)
    setVisibleLimit(INITIAL_NODE_LIMIT)
  }

  return (
    <section className="card scheduling-node-reference" data-testid="node-label-reference">
      <button
        type="button"
        className="scheduling-node-reference__toggle"
        aria-expanded={expanded}
        aria-controls="scheduling-node-label-content"
        onClick={() => setExpanded(value => !value)}
      >
        <span><i className="fas fa-tags icon-before" aria-hidden="true" />Node labels</span>
        <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} aria-hidden="true" />
      </button>
      {expanded && (
        <div id="scheduling-node-label-content" className="scheduling-node-reference__content">
          <p className="text-note">Browse labels available for node selectors without leaving this page.</p>
          {loading ? (
            <p className="scheduling-node-message" role="status">Loading node labels…</p>
          ) : error ? (
            <div className="scheduling-node-message" role="alert">
              <span>Could not load node labels.</span>
              <button type="button" className="btn btn-secondary btn-sm" aria-label="Retry loading node labels" onClick={fetchNodes}>Retry</button>
            </div>
          ) : nodes.length === 0 ? (
            <p className="scheduling-node-message">No nodes are available yet.</p>
          ) : (
            <>
              <div className="scheduling-node-toolbar">
                <input
                  type="search"
                  className="input"
                  aria-label="Search node labels"
                  placeholder="Search node, label, or key=value…"
                  value={query}
                  onChange={updateQuery}
                />
                <span className="text-meta" aria-live="polite">{Math.min(visibleLimit, filtered.length)} of {filtered.length} nodes</span>
              </div>
              {filtered.length === 0 ? (
                <p className="scheduling-node-message">No nodes match your search.</p>
              ) : (
                <div className="scheduling-node-grid">
                  {visible.map(node => {
                    const labels = Object.entries(node.labels || {})
                    return (
                      <article className="scheduling-node-card" key={node.id || node.name}>
                        <div className="scheduling-node-card__header">
                          <strong>{node.name || node.id}</strong>
                          <span className={`scheduling-node-status scheduling-node-status--${String(node.status || 'unknown').toLowerCase()}`}>{node.status || 'unknown'}</span>
                        </div>
                        {labels.length ? (
                          <div className="scheduling-node-labels">
                            {labels.map(([key, value]) => <span className="scheduling-node-label" key={key}>{key}={String(value)}</span>)}
                          </div>
                        ) : <span className="text-note">No labels</span>}
                      </article>
                    )
                  })}
                </div>
              )}
              {visibleLimit < filtered.length && (
                <button type="button" className="btn btn-secondary btn-sm scheduling-show-more" aria-label="Show 20 more nodes" onClick={() => setVisibleLimit(limit => limit + NODE_LIMIT_STEP)}>
                  Show 20 more
                </button>
              )}
            </>
          )}
        </div>
      )}
    </section>
  )
}

export default function Scheduling() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [schedulingConfigs, setSchedulingConfigs] = useState([])
  const [formState, setFormState] = useState(null)
  const [confirmDelete, setConfirmDelete] = useState(null)

  const fetchScheduling = useCallback(async () => {
    try {
      const data = await nodesApi.listScheduling()
      setSchedulingConfigs(Array.isArray(data) ? data : [])
    } catch { setSchedulingConfigs([]) }
  }, [])

  useEffect(() => { fetchScheduling() }, [fetchScheduling])

  const handleSave = async (config) => {
    try {
      await nodesApi.setScheduling(config)
      addToast('Scheduling rule saved', 'success')
      setFormState(null)
      fetchScheduling()
    } catch (err) { addToast(`Failed to save rule: ${err.message}`, 'error') }
  }

  const handleDelete = async (model) => {
    try {
      await nodesApi.deleteScheduling(model)
      addToast('Scheduling rule removed', 'success')
      setConfirmDelete(null)
      fetchScheduling()
    } catch (err) { addToast(`Failed to remove rule: ${err.message}`, 'error') }
  }

  return (
    <div className="page page--wide">
      <PageHeader
        title={<><i className="fas fa-calendar-alt" style={{ marginRight: 'var(--spacing-sm)' }} />{t('scheduling.title')}</>}
        supporting={t('scheduling.subtitle')}
      />
      <div>
        <NodeLabelReference />
        <button className="btn btn-primary btn-sm mb-md"
          onClick={() => setFormState(current => current?.kind === 'add' ? null : { kind: 'add' })}>
          <i className="fas fa-plus icon-before" />
          Add Scheduling Rule
        </button>
        {formState && (
          <SchedulingForm
            key={formState.kind === 'edit' ? formState.config.model_name : 'add'}
            initialConfig={formState.kind === 'edit' ? formState.config : undefined}
            onSave={handleSave}
            onCancel={() => setFormState(null)}
          />
        )}
        {schedulingConfigs.length === 0 && !formState ? (
          <p style={{ fontSize: '0.875rem', color: 'var(--color-text-muted)', textAlign: 'center', padding: 'var(--spacing-xl) 0' }}>
            No scheduling rules configured. Add a rule to control how models are placed on nodes.
          </p>
        ) : schedulingConfigs.length > 0 && (
          <ResponsiveTable>
              <thead><tr>
                <th>Model</th>
                <th>Mode</th>
                <th>Node Selector</th>
                <th>Min Replicas</th>
                <th>Max Replicas</th>
                <th>Routing</th>
                <th>Thresholds</th>
                <th>Status</th>
                <th className="text-right">Actions</th>
              </tr></thead>
              <tbody>
                {schedulingConfigs.map(cfg => {
                  const isSpread = !!cfg.spread_all
                  const isAutoScaling = !isSpread && (cfg.min_replicas > 0 || cfg.max_replicas > 0)
                  const hasSelector = !!cfg.node_selector
                  const modeLabel = isSpread ? 'Spread' : isAutoScaling ? 'Auto-scaling' : hasSelector ? 'Placement' : 'Inactive'
                  const modeColor = isSpread ? 'var(--color-warning)' : isAutoScaling ? 'var(--color-success)' : hasSelector ? 'var(--color-primary)' : 'var(--color-text-muted)'
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
                        } catch { return <span className="text-note">{cfg.node_selector}</span> }
                      })() : <span className="text-note">Any node</span>}
                    </td>
                    <td className="text-mono">
                      {isSpread
                        ? <span style={{
                            display: 'inline-block', fontSize: '0.75rem', padding: '2px 8px', borderRadius: "var(--radius-sm)",
                            background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-warning)',
                            color: 'var(--color-warning)', fontWeight: 600, fontFamily: 'var(--font-sans)',
                          }}>Spread: all matching nodes</span>
                        : isAutoScaling ? cfg.min_replicas : '-'}
                    </td>
                    <td className="text-mono">
                      {isSpread ? '-' : isAutoScaling ? (cfg.max_replicas || 'no limit') : '-'}
                    </td>
                    <td className="text-sm">
                      {cfg.route_policy || 'default'}
                    </td>
                    <td className="text-mono text-meta">
                      {cfg.route_policy === 'prefix_cache' ? (
                        <>
                          <div>match: {cfg.min_prefix_match ? cfg.min_prefix_match : 'inherit'}</div>
                          <div>abs: {cfg.balance_abs_threshold ? cfg.balance_abs_threshold : 'inherit'}</div>
                          <div>rel: {cfg.balance_rel_threshold ? cfg.balance_rel_threshold : 'inherit'}</div>
                        </>
                      ) : '-'}
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
                          <i className="fas fa-exclamation-triangle icon-before" />
                          Unsatisfiable until {unsatisfiableUntil.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                        </span>
                      ) : (
                        <span className="text-note">OK</span>
                      )}
                    </td>
                    <td className="text-right">
                      <div className="scheduling-rule-actions">
                        <button className="btn btn-secondary btn-sm" aria-label={`Edit ${cfg.model_name}`} onClick={() => setFormState({ kind: 'edit', config: cfg })}>
                          <i className="fas fa-edit" aria-hidden="true" />
                        </button>
                        <button className="btn btn-danger btn-sm" aria-label={`Delete ${cfg.model_name}`} onClick={() => setConfirmDelete(cfg.model_name)}>
                          <i className="fas fa-trash" aria-hidden="true" />
                        </button>
                      </div>
                    </td>
                  </tr>
                  )
                })}
              </tbody>
          </ResponsiveTable>
        )}
      </div>

      <ConfirmDialog
        open={!!confirmDelete}
        title="Remove scheduling rule"
        message={confirmDelete ? `Remove the scheduling rule for "${confirmDelete}"?` : ''}
        confirmLabel="Remove"
        danger
        onConfirm={() => confirmDelete && handleDelete(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />
    </div>
  )
}
