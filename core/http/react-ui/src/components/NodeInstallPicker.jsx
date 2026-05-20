import { useState, useMemo, useEffect, useRef } from 'react'
import Modal from './Modal'
import SearchableSelect from './SearchableSelect'
import { nodesApi } from '../utils/api'

// NodeInstallPicker is the single multi-node install surface used both from
// the Backends gallery split-button and from the "Install on more nodes" `+`
// affordance in the Nodes column. Submit fires N parallel per-node install
// calls; rows transition inline so the user sees per-node success/failure
// without leaving the modal.
//
// Props:
//   open               — controls visibility
//   onClose            — close handler (header X / Cancel / Esc / backdrop)
//   onComplete         — fired after at least one node install succeeded;
//                        gallery uses this to refetch and update the Nodes
//                        column without a manual reload
//   backend            — { name, isMeta, capabilities, metaBackendFor }
//   nodes              — BackendNode[] from /api/nodes
//   installedNodeIds   — Set/array of node IDs that already have this backend
//   initialSelection   — optional pre-selected node IDs (e.g. "missing nodes"
//                        when opened from the Nodes column `+` affordance)

const STATUS_LABELS = { healthy: 'Healthy', draining: 'Draining', unhealthy: 'Unhealthy', offline: 'Offline' }

function formatVRAM(bytes) {
  if (!bytes || bytes === 0) return null
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

function gpuVendorLabel(vendor) {
  const labels = { nvidia: 'NVIDIA', amd: 'AMD', intel: 'Intel', vulkan: 'Vulkan' }
  return labels[vendor] || null
}

// hardwareTargetOf parses the capability key that points to a concrete
// variant in the parent meta's CapabilitiesMap. e.g. cpu-llama-cpp comes
// from {"cpu": "cpu-llama-cpp"} → "cpu". Falls back to "" when the parent
// is unknown (the gallery list payload still gives us metaBackendFor).
function hardwareTargetOf(backend, allBackends) {
  if (!backend || !backend.name || backend.isMeta) return ''
  const parentName = backend.metaBackendFor
  if (!parentName) return ''
  const parent = (allBackends || []).find(b => b.name === parentName || b.id === parentName)
  if (!parent || !parent.capabilities) return ''
  for (const [cap, concreteName] of Object.entries(parent.capabilities)) {
    if (concreteName === backend.name) return cap
  }
  return ''
}

// humanTargetLabel turns a capability key into a user-facing phrase used in
// the picker header note: "CPU build", "CUDA 12 build", etc. Keep it
// concrete and product-recognisable, not the raw token from the gallery.
function humanTargetLabel(target) {
  if (!target) return 'hardware-specific build'
  const t = target.toLowerCase()
  if (t.startsWith('cpu') || t === 'default') return 'CPU build'
  if (t.includes('cuda-13') || t.includes('cuda13')) return 'CUDA 13 build'
  if (t.includes('cuda-12') || t.includes('cuda12')) return 'CUDA 12 build'
  if (t.includes('cuda')) return 'NVIDIA CUDA build'
  if (t.includes('l4t')) return 'NVIDIA Jetson (L4T) build'
  if (t.includes('nvidia')) return 'NVIDIA build'
  if (t.includes('rocm') || t.includes('amd')) return 'AMD ROCm build'
  if (t.includes('metal')) return 'Apple Metal build'
  if (t.includes('sycl') || t.includes('intel')) return 'Intel SYCL build'
  if (t.includes('vulkan')) return 'Vulkan build'
  if (t.includes('darwin-x86')) return 'macOS x86 build'
  return 'hardware-specific build'
}

// suitabilityFor returns the picker's per-row suitability state for the
// requested backend. Already-installed wins over compatible/override so
// the user sees a single signal per row.
function suitabilityFor({ node, backend, hardwareTarget, alreadyInstalled }) {
  if (alreadyInstalled) return 'installed'
  // backend can be null on the first render before pickerBackend is set —
  // this function is invoked from useMemo, which runs regardless of the
  // outer open guard. Treat missing data as "compatible" so the placeholder
  // render doesn't blow up; the picker won't actually paint anything until
  // the early-return below the hooks fires.
  if (!backend || backend.isMeta || !hardwareTarget) return 'compatible'
  const vendor = (node.gpu_vendor || '').toLowerCase()
  const t = hardwareTarget.toLowerCase()
  if (t.startsWith('cpu') || t === 'default') {
    // CPU builds always run; they're never marked Override (running CPU on a
    // GPU node is the headline use case the user is choosing intentionally).
    return 'compatible'
  }
  if (t.includes('nvidia') || t.includes('cuda') || t.includes('l4t')) {
    return vendor === 'nvidia' ? 'compatible' : 'override'
  }
  if (t.includes('amd') || t.includes('rocm') || t.includes('hip')) {
    return vendor === 'amd' ? 'compatible' : 'override'
  }
  if (t.includes('intel') || t.includes('sycl')) {
    return vendor === 'intel' ? 'compatible' : 'override'
  }
  if (t.includes('metal') || t.includes('darwin')) {
    // No vendor reporting for Metal; trust the user.
    return 'compatible'
  }
  return 'compatible'
}

export default function NodeInstallPicker({
  open, onClose, onComplete,
  backend,
  nodes = [],
  allBackends = [],
  installedNodeIds = [],
  initialSelection,
  addToast,
}) {
  const [search, setSearch] = useState('')
  const [showHealthy, setShowHealthy] = useState(true)
  const [showDraining, setShowDraining] = useState(false)
  const [selected, setSelected] = useState(() => new Set())
  const [overrideVariant, setOverrideVariant] = useState('') // chosen concrete name
  const [overrideExpanded, setOverrideExpanded] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [showMismatchConfirm, setShowMismatchConfirm] = useState(false)
  // Per-node submission state: { [nodeId]: { status: 'pending'|'installing'|'done'|'error', error? , version? } }
  const [perNode, setPerNode] = useState({})
  const headerInputRef = useRef(null)

  // Backend-derived metadata used throughout the picker.
  const hardwareTarget = useMemo(() => hardwareTargetOf(backend, allBackends), [backend, allBackends])
  const targetLabel = humanTargetLabel(hardwareTarget)
  const concreteVariants = useMemo(() => {
    if (!backend?.isMeta || !backend.capabilities) return []
    return Object.entries(backend.capabilities).map(([cap, concrete]) => ({
      value: concrete,
      label: `${concrete}  ·  ${cap}`,
    }))
  }, [backend])

  // Pending nodes are surgically removed from the list — they can't accept
  // installs until approved. Surface the count instead of dead-disabled rows.
  const pendingCount = nodes.filter(n => n.status === 'pending').length
  const backendNodes = nodes.filter(n =>
    (!n.node_type || n.node_type === 'backend') && n.status !== 'pending'
  )

  const installedSet = useMemo(() => {
    const s = new Set()
    if (Array.isArray(installedNodeIds)) installedNodeIds.forEach(id => s.add(id))
    else if (installedNodeIds && typeof installedNodeIds.has === 'function') {
      installedNodeIds.forEach(id => s.add(id))
    }
    return s
  }, [installedNodeIds])

  const filteredNodes = useMemo(() => {
    let list = backendNodes
    if (!showHealthy) list = list.filter(n => n.status !== 'healthy')
    if (!showDraining) list = list.filter(n => n.status !== 'draining')
    if (search.trim()) {
      const q = search.toLowerCase()
      list = list.filter(n =>
        (n.name || '').toLowerCase().includes(q) ||
        Object.entries(n.labels || {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q))
      )
    }
    return list
  }, [backendNodes, showHealthy, showDraining, search])

  // Pre-seed selection on open. Reset all transient state so reopening
  // doesn't surface ghost progress from the prior submit.
  useEffect(() => {
    if (!open) return
    const initial = new Set()
    if (Array.isArray(initialSelection)) initialSelection.forEach(id => initial.add(id))
    setSelected(initial)
    setSearch('')
    setOverrideVariant('')
    setOverrideExpanded(false)
    setPerNode({})
    setSubmitting(false)
    setShowMismatchConfirm(false)
  }, [open, initialSelection])

  // Auto-expand the variant override disclosure when at least one selected
  // node lacks a working GPU. This is the headline use case the feature
  // exists for; surfacing it instead of hiding behind a click.
  useEffect(() => {
    if (!backend?.isMeta) return
    const someGPUMissing = Array.from(selected).some(id => {
      const n = backendNodes.find(x => x.id === id)
      return n && (!n.gpu_vendor || n.gpu_vendor === '' || n.gpu_vendor === 'unknown')
    })
    if (someGPUMissing && !overrideExpanded) setOverrideExpanded(true)
  }, [selected, backend, backendNodes]) // eslint-disable-line react-hooks/exhaustive-deps

  // The effective backend that gets installed on each node. For
  // hardware-specific backends this is just backend.name. For meta backends
  // with no override, the worker picks per-node — we pass backend.name and
  // the worker resolves. With an override set, the picker installs that
  // exact concrete variant on every selected node.
  const effectiveBackendName = overrideVariant || backend?.name

  const counts = useMemo(() => {
    let already = 0, overrides = 0
    selected.forEach(id => {
      const n = backendNodes.find(x => x.id === id)
      if (!n) return
      if (installedSet.has(id)) { already++; return }
      const eff = overrideVariant
        ? { name: overrideVariant, isMeta: false, metaBackendFor: backend?.name }
        : backend
      const target = overrideVariant ? hardwareTargetOf(eff, allBackends) : hardwareTarget
      const s = suitabilityFor({ node: n, backend: eff, hardwareTarget: target, alreadyInstalled: false })
      if (s === 'override') overrides++
    })
    return { already, overrides, selected: selected.size }
  }, [selected, backendNodes, installedSet, overrideVariant, backend, hardwareTarget, allBackends])

  const toggle = (nodeId) => {
    setSelected(prev => {
      const next = new Set(prev)
      next.has(nodeId) ? next.delete(nodeId) : next.add(nodeId)
      return next
    })
  }

  const selectAllHealthy = () => {
    setSelected(new Set(filteredNodes.filter(n => n.status === 'healthy').map(n => n.id)))
  }
  const selectCompatible = () => {
    const eff = overrideVariant
      ? { name: overrideVariant, isMeta: false, metaBackendFor: backend?.name }
      : backend
    const target = overrideVariant ? hardwareTargetOf(eff, allBackends) : hardwareTarget
    setSelected(new Set(
      filteredNodes
        .filter(n => suitabilityFor({ node: n, backend: eff, hardwareTarget: target, alreadyInstalled: false }) === 'compatible')
        .map(n => n.id)
    ))
  }
  const clearSelection = () => setSelected(new Set())

  const submit = async () => {
    if (selected.size === 0 || submitting) return
    if (counts.overrides > 0 && !showMismatchConfirm) {
      setShowMismatchConfirm(true)
      return
    }
    setShowMismatchConfirm(false)
    setSubmitting(true)
    const ids = Array.from(selected)
    setPerNode(prev => {
      const next = { ...prev }
      ids.forEach(id => { next[id] = { status: 'installing' } })
      return next
    })

    const results = await Promise.allSettled(ids.map(id =>
      nodesApi.installBackend(id, effectiveBackendName)
        .then(r => ({ id, ok: true, message: r?.message }))
        .catch(err => ({ id, ok: false, error: err?.message || 'install failed' }))
    ))

    let successCount = 0, failCount = 0
    setPerNode(prev => {
      const next = { ...prev }
      for (const r of results) {
        if (r.status !== 'fulfilled') continue
        const v = r.value
        if (v.ok) {
          next[v.id] = { status: 'done' }
          successCount++
        } else {
          next[v.id] = { status: 'error', error: v.error }
          failCount++
        }
      }
      return next
    })
    setSubmitting(false)

    if (successCount > 0 && onComplete) onComplete()

    if (failCount === 0) {
      addToast?.(`Installed on ${successCount} node${successCount === 1 ? '' : 's'}`, 'success')
      setTimeout(() => onClose?.(), 800)
    } else if (successCount === 0) {
      addToast?.(`Install failed on all ${failCount} node${failCount === 1 ? '' : 's'}`, 'error')
    } else {
      addToast?.(`Installed on ${successCount}, failed on ${failCount}`, 'warning')
    }
  }

  const retryFailed = async () => {
    const failedIds = Object.entries(perNode)
      .filter(([, v]) => v.status === 'error')
      .map(([id]) => id)
    if (failedIds.length === 0) return
    setSelected(new Set(failedIds))
    // Replace state for failed rows so they show "installing" again, not stale errors.
    setPerNode(prev => {
      const next = { ...prev }
      failedIds.forEach(id => { next[id] = { status: 'installing' } })
      return next
    })
    setSubmitting(true)
    const results = await Promise.allSettled(failedIds.map(id =>
      nodesApi.installBackend(id, effectiveBackendName)
        .then(r => ({ id, ok: true, message: r?.message }))
        .catch(err => ({ id, ok: false, error: err?.message || 'install failed' }))
    ))
    let successCount = 0, failCount = 0
    setPerNode(prev => {
      const next = { ...prev }
      for (const r of results) {
        if (r.status !== 'fulfilled') continue
        const v = r.value
        if (v.ok) { next[v.id] = { status: 'done' }; successCount++ }
        else { next[v.id] = { status: 'error', error: v.error }; failCount++ }
      }
      return next
    })
    setSubmitting(false)
    if (successCount > 0 && onComplete) onComplete()
    if (failCount === 0) {
      addToast?.(`Installed on ${successCount} node${successCount === 1 ? '' : 's'}`, 'success')
      setTimeout(() => onClose?.(), 800)
    }
  }

  const doneCount = Object.values(perNode).filter(v => v.status === 'done').length
  const errorCount = Object.values(perNode).filter(v => v.status === 'error').length
  const totalAttempted = Object.keys(perNode).length

  if (!open || !backend) return null

  const noNodes = backendNodes.length === 0

  return (
    <Modal onClose={onClose} maxWidth="780px">
      <div style={{
        padding: 'var(--spacing-md) var(--spacing-lg)',
        borderBottom: '1px solid var(--color-border-subtle)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--spacing-sm)',
      }}>
        <h2 style={{ margin: 0, fontSize: '1rem', display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
          <i className="fas fa-cog" style={{ color: 'var(--color-primary)' }} />
          Install <span style={{ fontFamily: 'var(--font-mono)' }}>{backend.name}</span>
          {backend.isMeta ? (
            <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>Auto-resolving</span>
          ) : (
            <span className="badge badge-warning" style={{ fontSize: '0.6875rem' }}>Hardware-specific</span>
          )}
        </h2>
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={onClose}
          aria-label="Close"
          style={{ fontSize: '1.125rem', lineHeight: 1, padding: '4px 10px' }}
        >×</button>
      </div>

      <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)' }}>
        {!backend.isMeta && (
          <div className="card" style={{
            marginBottom: 'var(--spacing-md)',
            padding: 'var(--spacing-sm) var(--spacing-md)',
            background: 'var(--color-warning-light)',
            border: '1px solid var(--color-warning-border)',
            borderRadius: 'var(--radius-md)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--spacing-sm)',
          }}>
            <i className="fas fa-microchip" style={{ color: 'var(--color-warning)' }} />
            <span style={{ color: 'var(--color-warning)', fontSize: '0.8125rem' }}>
              {targetLabel}. Install only on nodes where you want this build to run.
              {hardwareTarget && ` Targets: ${humanTargetLabel(hardwareTarget).replace(' build', '')}.`}
            </span>
          </div>
        )}

        {noNodes ? (
          <div className="empty-state" style={{ padding: 'var(--spacing-xl) 0' }}>
            <div className="empty-state-icon"><i className="fas fa-server" /></div>
            <h3 className="empty-state-title">No backend nodes available</h3>
            <p className="empty-state-text">
              Approve pending workers or register new ones.
              {pendingCount > 0 && ` (${pendingCount} awaiting approval.)`}
            </p>
            <a className="btn btn-secondary btn-sm" href="/app/nodes">
              <i className="fas fa-network-wired" /> Manage nodes
            </a>
          </div>
        ) : (
          <>
            {/* Filter row */}
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center', marginBottom: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
              <div className="search-bar" style={{ flex: 1, minWidth: 180 }}>
                <i className="fas fa-search search-icon" />
                <input
                  ref={headerInputRef}
                  className="input"
                  placeholder="Filter nodes by name or label..."
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                />
              </div>
              <button className="btn btn-secondary btn-sm" onClick={selectAllHealthy} type="button">
                Select all healthy
              </button>
              <button className="btn btn-secondary btn-sm" onClick={selectCompatible} type="button">
                Select compatible nodes
              </button>
              {selected.size > 0 && (
                <button className="btn btn-ghost btn-sm" onClick={clearSelection} type="button">
                  Clear
                </button>
              )}
            </div>

            {/* Variant override (auto-resolving only) */}
            {backend.isMeta && concreteVariants.length > 0 && (
              <div style={{ marginBottom: 'var(--spacing-sm)' }}>
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  onClick={() => setOverrideExpanded(v => !v)}
                  aria-expanded={overrideExpanded}
                  style={{ padding: '4px 8px' }}
                >
                  <i className={`fas fa-chevron-${overrideExpanded ? 'down' : 'right'}`} style={{ marginRight: 4, fontSize: '0.625rem' }} />
                  Override variant for selected nodes…
                </button>
                {overrideExpanded && (
                  <div className="card" style={{ marginTop: 4, padding: 'var(--spacing-sm) var(--spacing-md)' }}>
                    <p style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', marginTop: 0, marginBottom: 'var(--spacing-xs)' }}>
                      By default each node picks its own variant. Override to install one specific variant on every selected node — useful when GPU detection fails on a node and you want the CPU build there instead.
                    </p>
                    <SearchableSelect
                      value={overrideVariant}
                      onChange={setOverrideVariant}
                      options={concreteVariants}
                      placeholder="Per-node auto-resolve (default)"
                      allOption={{ value: '', label: 'Per-node auto-resolve (default)' }}
                    />
                  </div>
                )}
              </div>
            )}

            {/* Node table */}
            <div className="table-container" style={{ marginBottom: 'var(--spacing-sm)', maxHeight: '40vh', overflowY: 'auto' }}>
              <table className="table" style={{ margin: 0 }}>
                <thead>
                  <tr>
                    <th style={{ width: 28 }}>
                      <input
                        type="checkbox"
                        aria-label="Select all visible"
                        checked={filteredNodes.length > 0 && filteredNodes.every(n => selected.has(n.id))}
                        onChange={(e) => {
                          setSelected(prev => {
                            const next = new Set(prev)
                            if (e.target.checked) filteredNodes.forEach(n => next.add(n.id))
                            else filteredNodes.forEach(n => next.delete(n.id))
                            return next
                          })
                        }}
                      />
                    </th>
                    <th>Node</th>
                    <th>Status</th>
                    <th>Hardware</th>
                    <th>Suitability</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredNodes.map(node => {
                    const installed = installedSet.has(node.id)
                    const eff = overrideVariant
                      ? { name: overrideVariant, isMeta: false, metaBackendFor: backend.name }
                      : backend
                    const target = overrideVariant ? hardwareTargetOf(eff, allBackends) : hardwareTarget
                    const suit = suitabilityFor({ node, backend: eff, hardwareTarget: target, alreadyInstalled: installed })
                    const isSel = selected.has(node.id)
                    const rowState = perNode[node.id]
                    const vendor = gpuVendorLabel(node.gpu_vendor)
                    const totalVRAM = formatVRAM(node.total_vram)
                    const totalRAM = formatVRAM(node.total_ram)
                    return (
                      <tr key={node.id}>
                        <td>
                          <input
                            type="checkbox"
                            aria-label={`Select ${node.name}`}
                            aria-disabled={rowState?.status === 'installing'}
                            checked={isSel}
                            onChange={() => toggle(node.id)}
                          />
                        </td>
                        <td>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                            <span style={{ fontWeight: 500, fontSize: '0.875rem' }}>{node.name}</span>
                            {node.labels && Object.keys(node.labels).length > 0 && (
                              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
                                {Object.entries(node.labels).slice(0, 3).map(([k, v]) => (
                                  <span key={k} className="cell-mono" style={{
                                    padding: '1px 5px', borderRadius: 'var(--radius-sm)', fontSize: '0.6875rem',
                                    background: 'var(--color-bg-tertiary)', border: '1px solid var(--color-border-subtle)',
                                  }}>{k}={v}</span>
                                ))}
                                {Object.keys(node.labels).length > 3 && (
                                  <span className="cell-muted" style={{ fontSize: '0.6875rem' }}>
                                    +{Object.keys(node.labels).length - 3}
                                  </span>
                                )}
                              </div>
                            )}
                          </div>
                        </td>
                        <td>
                          <span style={{ fontSize: '0.8125rem' }}>
                            {STATUS_LABELS[node.status] || node.status}
                          </span>
                        </td>
                        <td style={{ fontSize: '0.8125rem', fontFamily: 'var(--font-mono)', color: 'var(--color-text-secondary)' }}>
                          {totalVRAM ? (
                            <>{vendor && <span style={{ marginRight: 4 }}>{vendor}</span>}{totalVRAM}</>
                          ) : totalRAM ? (
                            <span>CPU · {totalRAM}</span>
                          ) : <span className="cell-muted">—</span>}
                        </td>
                        <td>
                          {rowState?.status === 'installing' ? (
                            <span className="badge badge-info">
                              <i className="fas fa-spinner fa-spin" style={{ marginRight: 4 }} />Installing
                            </span>
                          ) : rowState?.status === 'done' ? (
                            <span className="badge badge-success">
                              <i className="fas fa-check" style={{ marginRight: 4 }} />Installed
                            </span>
                          ) : rowState?.status === 'error' ? (
                            <button
                              type="button"
                              className="badge badge-error"
                              title={rowState.error}
                              aria-describedby={`err-${node.id}`}
                              style={{ border: 'none', cursor: 'help' }}
                            >
                              <i className="fas fa-exclamation-triangle" style={{ marginRight: 4 }} />Failed
                              <span id={`err-${node.id}`} style={{ position: 'absolute', left: -9999 }}>{rowState.error}</span>
                            </button>
                          ) : suit === 'installed' ? (
                            <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                              Installed
                            </span>
                          ) : suit === 'override' ? (
                            <span className="badge badge-warning">
                              <i className="fas fa-exclamation-circle" style={{ marginRight: 4 }} />Override
                            </span>
                          ) : (
                            <span className="badge badge-success" style={{ background: 'var(--color-success-light)', color: 'var(--color-success)' }}>
                              Compatible
                            </span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                  {filteredNodes.length === 0 && (
                    <tr>
                      <td colSpan={5} style={{ textAlign: 'center', padding: 'var(--spacing-md)', color: 'var(--color-text-muted)' }}>
                        No nodes match the current filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>

            {pendingCount > 0 && (
              <p style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 0, marginBottom: 'var(--spacing-sm)' }}>
                +{pendingCount} awaiting approval — <a href="/app/nodes" style={{ color: 'var(--color-primary)' }}>approve from Nodes</a>.
              </p>
            )}

            {/* Mismatch confirm */}
            {showMismatchConfirm && (
              <div className="card" style={{
                marginBottom: 'var(--spacing-sm)',
                padding: 'var(--spacing-md)',
                background: 'var(--color-warning-light)',
                border: '1px solid var(--color-warning-border)',
                borderRadius: 'var(--radius-md)',
              }}>
                <p style={{ marginTop: 0, marginBottom: 'var(--spacing-sm)', color: 'var(--color-warning)', fontSize: '0.875rem' }}>
                  Installing {targetLabel.toLowerCase()} on {counts.overrides} node{counts.overrides === 1 ? '' : 's'} that don't match. Those nodes will run inference on the chosen build, not their native GPU. Continue?
                </p>
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end' }}>
                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => setShowMismatchConfirm(false)}>
                    Cancel
                  </button>
                  <button className="btn btn-primary btn-sm" type="button" onClick={submit}
                    style={{ background: 'var(--color-warning)', borderColor: 'var(--color-warning)' }}>
                    Install on {targetLabel.replace(' build', '')}
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {!noNodes && (
        <div style={{
          padding: 'var(--spacing-md) var(--spacing-lg)',
          borderTop: '1px solid var(--color-border-subtle)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--spacing-sm)',
          flexWrap: 'wrap',
        }}>
          <div style={{ flex: 1, fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
            {totalAttempted > 0 ? (
              <>
                {doneCount} of {totalAttempted} done
                {errorCount > 0 && (
                  <> · <span className="badge badge-error" style={{ fontSize: '0.6875rem' }}>{errorCount} failed</span></>
                )}
              </>
            ) : (
              <>
                {counts.selected} {counts.selected === 1 ? 'node' : 'nodes'} selected
                {counts.already > 0 && <> · {counts.already} already installed</>}
                {counts.overrides > 0 && <> · {counts.overrides} override{counts.overrides === 1 ? '' : 's'}</>}
              </>
            )}
          </div>
          {errorCount > 0 && !submitting && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={retryFailed}>
              <i className="fas fa-redo" /> Retry failed nodes
            </button>
          )}
          <button className="btn btn-secondary btn-sm" type="button" onClick={onClose} disabled={submitting}>
            {totalAttempted > 0 && doneCount > 0 ? 'Close' : 'Cancel'}
          </button>
          <button
            className="btn btn-primary btn-sm"
            type="button"
            onClick={submit}
            disabled={submitting || counts.selected === 0 || showMismatchConfirm}
          >
            {submitting ? (
              <><i className="fas fa-spinner fa-spin" /> Installing…</>
            ) : (
              <>Install on {counts.selected} {counts.selected === 1 ? 'node' : 'nodes'}</>
            )}
          </button>
        </div>
      )}
    </Modal>
  )
}
