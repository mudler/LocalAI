import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi, nodesApi } from '../utils/api'
import { useDebouncedCallback } from '../hooks/useDebounce'
import React from 'react'
import { useOperations } from '../hooks/useOperations'
import { useDistributedMode } from '../hooks/useDistributedMode'
import LoadingSpinner from '../components/LoadingSpinner'
import { renderMarkdown } from '../utils/markdown'
import ConfirmDialog from '../components/ConfirmDialog'
import Toggle from '../components/Toggle'
import NodeDistributionChip from '../components/NodeDistributionChip'
import NodeInstallPicker from '../components/NodeInstallPicker'
import Popover from '../components/Popover'

export default function Backends() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('admin')
  const [searchParams, setSearchParams] = useSearchParams()
  const { operations } = useOperations()
  const { enabled: distributedEnabled, nodes: clusterNodes, refetch: refetchNodes } = useDistributedMode()
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('')
  const [sortBy, setSortBy] = useState('name')
  const [sortOrder, setSortOrder] = useState('asc')
  const [page, setPage] = useState(1)
  const [installedCount, setInstalledCount] = useState(0)
  const [showManualInstall, setShowManualInstall] = useState(false)
  const [manualUri, setManualUri] = useState('')
  const [manualName, setManualName] = useState('')
  const [manualAlias, setManualAlias] = useState('')
  const [expandedRow, setExpandedRow] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [allBackends, setAllBackends] = useState([])
  const [upgrades, setUpgrades] = useState({})
  const [upgradingAll, setUpgradingAll] = useState(false)
  const [showAllBackends, setShowAllBackends] = useState(false)
  const [showDevelopment, setShowDevelopment] = useState(false)
  const [preferDevLoaded, setPreferDevLoaded] = useState(false)
  const [pickerBackend, setPickerBackend] = useState(null)
  const [pickerInitialSelection, setPickerInitialSelection] = useState([])
  const [splitMenuFor, setSplitMenuFor] = useState(null)
  // Anchor ref for the currently-open split-button chevron. Only one row's
  // menu can be open at a time, so a single ref is enough — re-attached
  // whenever splitMenuFor changes to a different row index.
  const splitMenuAnchorRef = useRef(null)

  // Target-node mode: set when navigated from /app/nodes via "+ Add backend".
  // The gallery page header banners the scope; rows collapse their split-button
  // to a single Install-on-this-node action; manual install posts to the
  // per-node endpoint.
  const targetNodeId = searchParams.get('target') || ''
  const targetNode = targetNodeId
    ? clusterNodes.find(n => n.id === targetNodeId) || null
    : null

  const clearTarget = useCallback(() => {
    const next = new URLSearchParams(searchParams)
    next.delete('target')
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams])

  // The Popover component handles outside-click + Escape + focus return,
  // so we don't reimplement it here.

  const fetchBackends = useCallback(async () => {
    try {
      setLoading(true)
      const params = { page: 1, items: 9999, sort: sortBy, order: sortOrder }
      if (search) params.term = search
      const data = await backendsApi.list(params)
      const list = Array.isArray(data?.backends) ? data.backends : Array.isArray(data) ? data : []
      setAllBackends(list)
      setInstalledCount(list.filter(b => b.installed).length)
      // On first load, use server preference for development toggle
      if (!preferDevLoaded && data?.preferDevelopmentBackends) {
        setShowDevelopment(true)
        setPreferDevLoaded(true)
      }
    } catch (err) {
      addToast(`Failed to load backends: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [search, sortBy, sortOrder, addToast])

  useEffect(() => {
    fetchBackends()
  }, [sortBy, sortOrder])

  // Re-fetch when operations change (install/delete completion)
  useEffect(() => {
    if (!loading) fetchBackends()
  }, [operations.length])

  // Fetch available upgrades
  useEffect(() => {
    backendsApi.checkUpgrades()
      .then(data => setUpgrades(data || {}))
      .catch(() => {})
  }, [operations.length])

  // Client-side filtering by meta/development toggles and tag
  const filteredBackends = (() => {
    let result = allBackends

    // Hide concrete variants that are aliased by a meta backend unless
    // "Show all" is toggled. Standalone backends (no meta referencing them)
    // stay visible even when they don't declare capabilities themselves.
    if (!showAllBackends) {
      result = result.filter(b => b.isMeta || !b.isAlias)
    }

    // Hide development backends unless toggled on
    if (!showDevelopment) {
      result = result.filter(b => !b.isDevelopment)
    }

    // Apply tag filter
    if (filter) {
      result = result.filter(b => {
        const tags = (b.tags || []).map(t => t.toLowerCase())
        const name = (b.name || '').toLowerCase()
        const desc = (b.description || '').toLowerCase()
        const f = filter.toLowerCase()
        return tags.some(t => t.includes(f)) || name.includes(f) || desc.includes(f)
      })
    }

    return result
  })()

  // Client-side pagination
  const ITEMS_PER_PAGE = 21
  const totalPages = Math.max(1, Math.ceil(filteredBackends.length / ITEMS_PER_PAGE))
  const backends = filteredBackends.slice((page - 1) * ITEMS_PER_PAGE, page * ITEMS_PER_PAGE)

  const debouncedFetch = useDebouncedCallback(() => fetchBackends())

  const handleSearch = (value) => {
    setSearch(value)
    setPage(1)
    debouncedFetch()
  }

  const handleSort = (col) => {
    if (sortBy === col) {
      setSortOrder(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setSortBy(col)
      setSortOrder('asc')
    }
    setPage(1)
  }

  const handleInstall = async (id) => {
    try {
      await backendsApi.install(id)
    } catch (err) {
      // Distributed-mode 409 guard: surface the human message and steer the
      // user to the picker rather than failing silently. The error body has
      // a `code` field of "concrete_backend_requires_target".
      const isConcreteGuard = err?.payload?.code === 'concrete_backend_requires_target'
        || (err?.message || '').includes('hardware-specific build')
      if (isConcreteGuard && distributedEnabled) {
        const b = allBackends.find(x => x.id === id || x.name === id)
        if (b) {
          openPicker(b)
          return
        }
      }
      addToast(`Install failed: ${err.message}`, 'error')
    }
  }

  // Install a single gallery backend on a specific node, used in target-node
  // mode (the URL has ?target=<node-id> set from the Nodes page entry point).
  const handleInstallOnTarget = async (id) => {
    if (!targetNode) return
    try {
      await nodesApi.installBackend(targetNode.id, id)
      addToast(`Installing ${id} on ${targetNode.name}…`, 'info')
      // Per-node install is request-reply, not part of the global jobs feed —
      // refetch to reflect the new Nodes column state.
      setTimeout(() => { fetchBackends(); refetchNodes() }, 600)
    } catch (err) {
      addToast(`Install failed on ${targetNode.name}: ${err.message}`, 'error')
    }
  }

  const openPicker = (b, initialSelection = []) => {
    setPickerBackend(b)
    setPickerInitialSelection(initialSelection)
    setSplitMenuFor(null)
  }

  // Returns the IDs of nodes that don't yet have this backend installed.
  // Used by the Nodes column "+" affordance to pre-select missing nodes.
  const missingNodesFor = (b) => {
    const installed = new Set((b?.nodes || []).map(n => n.node_id ?? n.NodeID))
    return clusterNodes
      .filter(n => (!n.node_type || n.node_type === 'backend')
        && n.status === 'healthy'
        && !installed.has(n.id))
      .map(n => n.id)
  }

  const handleDelete = async (id) => {
    setConfirmDialog({
      title: 'Delete Backend',
      message: `Delete backend ${id}?`,
      confirmLabel: 'Delete',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await backendsApi.delete(id)
          addToast(`Deleting ${id}...`, 'info')
          setTimeout(fetchBackends, 1000)
        } catch (err) {
          addToast(`Delete failed: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleUpgrade = async (id) => {
    try {
      await backendsApi.upgrade(id)
      addToast(`Upgrading ${id}...`, 'info')
    } catch (err) {
      addToast(`Upgrade failed: ${err.message}`, 'error')
    }
  }

  const handleUpgradeAll = async () => {
    const names = Object.keys(upgrades)
    if (names.length === 0) return
    setUpgradingAll(true)
    try {
      for (const name of names) {
        await backendsApi.upgrade(name)
      }
      addToast(`Upgrading ${names.length} backend${names.length > 1 ? 's' : ''}...`, 'info')
    } catch (err) {
      addToast(`Upgrade failed: ${err.message}`, 'error')
    } finally {
      setUpgradingAll(false)
    }
  }

  const handleManualInstall = async (e) => {
    e.preventDefault()
    if (!manualUri.trim()) { addToast('Please enter a URI', 'warning'); return }
    try {
      if (targetNode) {
        // Target-node mode: route the manual install to the per-node endpoint
        // so the backend lands only on this worker, not the whole cluster.
        await nodesApi.installBackend(
          targetNode.id,
          manualName.trim() || '',
          {
            uri: manualUri.trim(),
            name: manualName.trim() || undefined,
            alias: manualAlias.trim() || undefined,
          },
        )
        addToast(`Installing on ${targetNode.name}…`, 'info')
        setTimeout(() => { fetchBackends(); refetchNodes() }, 600)
      } else {
        const body = { uri: manualUri.trim() }
        if (manualName.trim()) body.name = manualName.trim()
        if (manualAlias.trim()) body.alias = manualAlias.trim()
        await backendsApi.installExternal(body)
      }
      setManualUri('')
      setManualName('')
      setManualAlias('')
      setShowManualInstall(false)
    } catch (err) {
      addToast(`Install failed: ${err.message}`, 'error')
    }
  }

  // Check if a backend has an active operation
  const getBackendOp = (backend) => {
    if (!operations.length) return null
    return operations.find(op => op.name === backend.name || op.name === backend.id) || null
  }

  const handleToggleAllBackends = () => { setShowAllBackends(v => !v); setPage(1) }
  const handleToggleDev = () => { setShowDevelopment(v => !v); setPage(1) }

  const FILTERS = [
    { key: '', label: 'All', icon: 'fa-layer-group' },
    { key: 'llm', label: 'LLM', icon: 'fa-brain' },
    { key: 'image', label: 'Image', icon: 'fa-image' },
    { key: 'video', label: 'Video', icon: 'fa-video' },
    { key: 'tts', label: 'TTS', icon: 'fa-microphone' },
    { key: 'stt', label: 'STT', icon: 'fa-headphones' },
    { key: 'vision', label: 'Vision', icon: 'fa-eye' },
  ]

  const SortHeader = ({ col, children }) => (
    <th
      onClick={() => handleSort(col)}
      style={{ cursor: 'pointer', userSelect: 'none', whiteSpace: 'nowrap' }}
    >
      {children}
      {sortBy === col && (
        <i className={`fas fa-sort-${sortOrder === 'asc' ? 'up' : 'down'}`} style={{ marginLeft: 4, fontSize: '0.6875rem', color: 'var(--color-primary)' }} />
      )}
    </th>
  )

  return (
    <div className="page page--wide">
      {/* Target-node banner: when this gallery is scoped to one node via
          ?target=<id> (entered from /app/nodes), show the scope clearly and
          give a fast way to clear it. Visually a primary-tinted strip so the
          user knows they're in a special mode without it feeling alarming. */}
      {targetNode && (
        <div className="card" style={{
          marginBottom: 'var(--spacing-md)',
          padding: 'var(--spacing-sm) var(--spacing-md)',
          background: 'var(--color-primary-light)',
          border: '1px solid var(--color-primary-border)',
          borderRadius: 'var(--radius-md)',
          display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
          flexWrap: 'wrap',
        }}>
          <i className="fas fa-bullseye" style={{ color: 'var(--color-primary)' }} />
          <span style={{ color: 'var(--color-primary)', fontWeight: 500, fontSize: 'var(--text-sm)' }}>
            Installing only on <span style={{ fontFamily: 'var(--font-mono)' }}>{targetNode.name}</span>
          </span>
          <span style={{ flex: 1 }} />
          <button className="btn btn-ghost btn-sm" type="button" onClick={clearTarget}>
            <i className="fas fa-times" /> Clear
          </button>
        </div>
      )}

      {/* Header */}
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">{t('backends.title')}</h1>
          <p className="page-subtitle">{t('backends.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center' }}>
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.8125rem' }}>
            <div style={{ textAlign: 'center' }}>
              <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-primary)' }}>{filteredBackends.length}</div>
              <div style={{ color: 'var(--color-text-muted)' }}>Available</div>
            </div>
            <div style={{ textAlign: 'center' }}>
              <a onClick={() => navigate('/app/manage')} style={{ cursor: 'pointer' }}>
                <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-success)' }}>{installedCount}</div>
                <div style={{ color: 'var(--color-text-muted)' }}>Installed</div>
              </a>
            </div>
            {Object.keys(upgrades).length > 0 && (
              <div style={{ textAlign: 'center' }}>
                <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-warning)' }}>
                  {Object.keys(upgrades).length}
                </div>
                <div style={{ color: 'var(--color-text-muted)' }}>Updates</div>
              </div>
            )}
          </div>
          <a className="btn btn-secondary btn-sm" href="https://localai.io/docs/getting-started/manual/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-book" /> Docs
          </a>
        </div>
      </div>

      {/* Upgrade Banner */}
      {Object.keys(upgrades).length > 0 && (
        <div className="card" style={{
          marginBottom: 'var(--spacing-md)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: 'var(--spacing-sm) var(--spacing-md)',
          background: 'var(--color-warning-light)',
          border: '1px solid var(--color-warning-border)',
          borderRadius: 'var(--radius-md)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
            <i className="fas fa-arrow-up" style={{ color: 'var(--color-warning)' }} />
            <span style={{ color: 'var(--color-warning)', fontWeight: 500, fontSize: 'var(--text-sm)' }}>
              {Object.keys(upgrades).length} backend{Object.keys(upgrades).length > 1 ? 's have' : ' has'} updates available
            </span>
          </div>
          <button
            className="btn btn-primary btn-sm"
            onClick={handleUpgradeAll}
            disabled={upgradingAll}
          >
            <i className={`fas ${upgradingAll ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} style={{ marginRight: 4 }} />
            Upgrade All
          </button>
        </div>
      )}

      {/* Manual Install */}
      <div style={{ marginBottom: 'var(--spacing-md)' }}>
        <button className="btn btn-secondary btn-sm" onClick={() => setShowManualInstall(!showManualInstall)}>
          <i className={`fas ${showManualInstall ? 'fa-chevron-up' : 'fa-plus'}`} /> Manual Install
        </button>
      </div>

      {showManualInstall && (
        <form onSubmit={handleManualInstall} className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 style={{ fontSize: '0.9375rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-download" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Install External Backend
          </h3>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr auto', gap: 'var(--spacing-sm)', alignItems: 'end' }}>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">OCI Image / URL / Path *</label>
              <input className="input" value={manualUri} onChange={(e) => setManualUri(e.target.value)} placeholder="oci://quay.io/example/backend:latest" />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">Name (required for OCI)</label>
              <input className="input" value={manualName} onChange={(e) => setManualName(e.target.value)} placeholder="my-backend" />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">Alias (optional)</label>
              <input className="input" value={manualAlias} onChange={(e) => setManualAlias(e.target.value)} placeholder="alias" />
            </div>
            <button type="submit" className="btn btn-primary">
              <i className="fas fa-download" /> Install
            </button>
          </div>
        </form>
      )}

      {/* Search + Filters */}
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', flexWrap: 'wrap', alignItems: 'center' }}>
        <div className="search-bar" style={{ flex: 1, minWidth: 200 }}>
          <i className="fas fa-search search-icon" />
          <input className="input" placeholder="Search backends by name, description, or type..." value={search} onChange={(e) => handleSearch(e.target.value)} />
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)', flexWrap: 'wrap' }}>
        <div className="filter-bar" style={{ margin: 0, flex: 1 }}>
          {FILTERS.map(f => (
            <button
              key={f.key}
              className={`filter-btn ${filter === f.key ? 'active' : ''}`}
              onClick={() => { setFilter(f.key); setPage(1) }}
            >
            <i className={`fas ${f.icon}`} style={{ marginRight: 4 }} />
            {f.label}
          </button>
        ))}
        </div>

        <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center', borderLeft: '1px solid var(--color-border-subtle)', paddingLeft: 'var(--spacing-md)' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', fontSize: '0.75rem', color: 'var(--color-text-secondary)', cursor: 'pointer', userSelect: 'none', whiteSpace: 'nowrap' }}>
            <Toggle checked={showAllBackends} onChange={handleToggleAllBackends} />
            <i className="fas fa-cubes" style={{ fontSize: '0.625rem' }} />
            Show all
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', fontSize: '0.75rem', color: 'var(--color-text-secondary)', cursor: 'pointer', userSelect: 'none', whiteSpace: 'nowrap' }}>
            <Toggle checked={showDevelopment} onChange={handleToggleDev} />
            <i className="fas fa-flask" style={{ fontSize: '0.625rem' }} />
            Development
          </label>
        </div>
      </div>

      {/* Table */}
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
      ) : backends.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-server" /></div>
          <h2 className="empty-state-title">No backends found</h2>
          <p className="empty-state-text">
            {search || filter ? 'Try adjusting your search or filters.' : 'No backends available in the gallery.'}
          </p>
        </div>
      ) : (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 30 }}></th>
                <th style={{ width: 40 }}></th>
                <SortHeader col="name">Backend</SortHeader>
                <th>Description</th>
                <SortHeader col="repository">Repository</SortHeader>
                <SortHeader col="license">License</SortHeader>
                <SortHeader col="status">Status</SortHeader>
                {distributedEnabled && !targetNode && <th>Nodes</th>}
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {backends.map((b, idx) => {
                const op = getBackendOp(b)
                const isProcessing = !!op
                const isExpanded = expandedRow === idx

                return (
                  <React.Fragment key={b.name || b.id}>
                  <tr
                    onClick={() => setExpandedRow(isExpanded ? null : idx)}
                    style={{ cursor: 'pointer' }}
                  >
                    {/* Chevron */}
                    <td style={{ width: 30 }}>
                      <i className={`fas fa-chevron-${isExpanded ? 'down' : 'right'}`} style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)', transition: 'transform 150ms' }} />
                    </td>
                    {/* Icon */}
                    <td>
                      {b.icon ? (
                        <img src={b.icon} alt="" style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', objectFit: 'cover' }} />
                      ) : (
                        <div style={{
                          width: 28, height: 28, borderRadius: 'var(--radius-sm)',
                          background: 'var(--color-bg-tertiary)', display: 'flex',
                          alignItems: 'center', justifyContent: 'center',
                        }}>
                          <i className="fas fa-cog" style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }} />
                        </div>
                      )}
                    </td>

                    {/* Name */}
                    <td>
                      <span style={{ fontWeight: 500 }}>{b.name || b.id}</span>
                      {b.version && (
                        <span className="badge" style={{ fontSize: '0.625rem', marginLeft: 4, background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)' }}>
                          v{b.version}
                        </span>
                      )}
                    </td>

                    {/* Description */}
                    <td>
                      <span style={{
                        fontSize: '0.8125rem', color: 'var(--color-text-secondary)',
                        display: 'inline-block', maxWidth: 300, overflow: 'hidden',
                        textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                      }} title={b.description}>
                        {b.description || '-'}
                      </span>
                    </td>

                    {/* Repository */}
                    <td>
                      {b.gallery ? (
                        <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{typeof b.gallery === 'string' ? b.gallery : b.gallery.name || '-'}</span>
                      ) : '-'}
                    </td>

                    {/* License */}
                    <td>
                      {b.license ? (
                        <span className="badge" style={{ fontSize: '0.6875rem', background: 'var(--color-bg-tertiary)' }}>{b.license}</span>
                      ) : '-'}
                    </td>

                    {/* Status — in distributed mode the Nodes column is the
                        installed signal, so we drop the global "Installed"
                        badge here and only keep operation-progress / update
                        signals to avoid stacking 6 badges in one cell. */}
                    <td>
                      {isProcessing ? (
                        <div className="inline-install">
                          <div className="inline-install__row">
                            <div className="operation-spinner" />
                            <span className="inline-install__label">
                              {op.isDeletion ? 'Deleting...' : op.isQueued ? 'Queued' : `Installing${op.progress > 0 ? ` · ${Math.round(op.progress)}%` : '...'}`}
                            </span>
                          </div>
                          {op.progress > 0 && (
                            <div className="operation-bar-container" style={{ flex: 'none', width: '120px', marginTop: 4 }}>
                              <div className="operation-bar" style={{ width: `${op.progress}%` }} />
                            </div>
                          )}
                        </div>
                      ) : b.installed ? (
                        <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexWrap: 'wrap' }}>
                          {!distributedEnabled && (
                            <span className="badge badge-success">
                              <i className="fas fa-check" style={{ fontSize: '0.5rem', marginRight: 2 }} /> Installed
                            </span>
                          )}
                          {b.version && (
                            <span className="badge" style={{ fontSize: '0.625rem', background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)' }}>
                              v{b.version}
                            </span>
                          )}
                          {upgrades[b.name] && (
                            <span className="badge" style={{ fontSize: '0.625rem', background: 'var(--color-warning-light)', color: 'var(--color-warning)' }}>
                              <i className="fas fa-arrow-up" style={{ fontSize: '0.5rem', marginRight: 2 }} />
                              {upgrades[b.name].available_version ? `v${upgrades[b.name].available_version}` : 'Update'}
                            </span>
                          )}
                        </div>
                      ) : (
                        <span className="badge" style={{ background: 'var(--color-surface-sunken)', color: 'var(--color-text-muted)', border: '1px solid var(--color-border-default)' }}>
                          <i className="fas fa-circle" style={{ fontSize: '0.5rem', marginRight: 2 }} /> Not Installed
                        </span>
                      )}
                    </td>

                    {/* Nodes column (distributed mode only, hidden in target
                        mode since it's redundant with the banner). The chip
                        is read-only inspection; the adjacent + button is the
                        write affordance — keeping them visually separate so
                        users don't accidentally trigger the picker by clicking
                        to read distribution. */}
                    {distributedEnabled && !targetNode && (
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                          <NodeDistributionChip nodes={b.nodes || []} />
                          {(() => {
                            const missing = missingNodesFor(b)
                            if (missing.length === 0 || isProcessing) return null
                            return (
                              <button
                                type="button"
                                className="btn btn-ghost btn-sm"
                                onClick={(e) => { e.stopPropagation(); openPicker(b, missing) }}
                                title={`Install on ${missing.length} more node${missing.length === 1 ? '' : 's'}`}
                                aria-label="Install on more nodes"
                                style={{ padding: '2px 6px' }}
                              >
                                <i className="fas fa-plus" style={{ fontSize: '0.6875rem' }} />
                              </button>
                            )
                          })()}
                        </div>
                      </td>
                    )}

                    {/* Actions */}
                    <td>
                      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }} onClick={e => e.stopPropagation()}>
                        {targetNode ? (
                          // Target-node mode: collapse to a single per-node
                          // action. The split-button is overkill when scope is
                          // already pinned by the URL.
                          (b.nodes || []).some(n => (n.node_id ?? n.NodeID) === targetNode.id) ? (
                            <>
                              <button className="btn btn-secondary btn-sm" onClick={() => handleInstallOnTarget(b.name || b.id)} disabled={isProcessing}
                                title={`Reinstall on ${targetNode.name}`}>
                                <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-rotate'}`} /> Reinstall
                              </button>
                              <button className="btn btn-danger btn-sm" onClick={async () => {
                                try {
                                  await nodesApi.deleteBackend(targetNode.id, b.name || b.id)
                                  addToast(`Removed ${b.name} from ${targetNode.name}`, 'success')
                                  setTimeout(() => { fetchBackends(); refetchNodes() }, 600)
                                } catch (err) {
                                  addToast(`Remove failed: ${err.message}`, 'error')
                                }
                              }} title={`Remove from ${targetNode.name}`} disabled={isProcessing}>
                                <i className="fas fa-trash" />
                              </button>
                            </>
                          ) : (
                            <button className="btn btn-primary btn-sm" onClick={() => handleInstallOnTarget(b.name || b.id)} disabled={isProcessing}>
                              <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-download'}`} /> Install on {targetNode.name}
                            </button>
                          )
                        ) : b.installed ? (
                          <>
                            {upgrades[b.name] ? (
                              <button className="btn btn-primary btn-sm" onClick={() => handleUpgrade(b.name || b.id)} title={`Upgrade to ${upgrades[b.name]?.available_version ? 'v' + upgrades[b.name].available_version : 'latest'}`} disabled={isProcessing}>
                                <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} />
                              </button>
                            ) : (
                              <button className="btn btn-secondary btn-sm" onClick={() => handleInstall(b.name || b.id)} title="Reinstall" disabled={isProcessing}>
                                <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
                              </button>
                            )}
                            <button className="btn btn-danger btn-sm" onClick={() => handleDelete(b.name || b.id)} title="Delete" disabled={isProcessing}>
                              <i className="fas fa-trash" />
                            </button>
                          </>
                        ) : distributedEnabled ? (
                          // Split-button. Auto-resolving (meta) keeps fan-out
                          // as the primary; hardware-specific routes the
                          // primary directly to the picker — fan-out for a
                          // CPU build is the silent footgun this guard exists
                          // to prevent. Both share a chevron menu for the
                          // alternate path.
                          b.isMeta ? (
                            <div style={{ display: 'inline-flex' }}>
                              <button className="btn btn-primary btn-sm" onClick={() => handleInstall(b.name || b.id)} disabled={isProcessing} title="Install on all nodes" style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}>
                                <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-download'}`} /> Install on all
                              </button>
                              <button
                                ref={splitMenuFor === idx ? splitMenuAnchorRef : undefined}
                                className="btn btn-primary btn-sm"
                                onClick={() => setSplitMenuFor(splitMenuFor === idx ? null : idx)}
                                aria-haspopup="menu"
                                aria-expanded={splitMenuFor === idx}
                                aria-label="More install options"
                                disabled={isProcessing}
                                style={{ padding: '0 8px', borderLeft: '1px solid rgba(0,0,0,0.15)', borderTopLeftRadius: 0, borderBottomLeftRadius: 0 }}
                              >
                                <i className={`fas fa-chevron-${splitMenuFor === idx ? 'up' : 'down'}`} style={{ fontSize: '0.6875rem' }} />
                              </button>
                            </div>
                          ) : (
                            <button
                              className="btn btn-primary btn-sm"
                              onClick={() => openPicker(b)}
                              disabled={isProcessing}
                              title="Choose nodes to install on"
                            >
                              <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-server'}`} /> Choose nodes…
                            </button>
                          )
                        ) : (
                          <button className="btn btn-primary btn-sm" onClick={() => handleInstall(b.name || b.id)} title="Install" disabled={isProcessing}>
                            <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-download'}`} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                  {/* Expanded detail row */}
                  {isExpanded && (
                    <tr>
                      <td colSpan={distributedEnabled && !targetNode ? 9 : 8} style={{ padding: 0 }}>
                        <BackendDetail backend={b} />
                      </td>
                    </tr>
                  )}
                  </React.Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          gap: 'var(--spacing-sm)', marginTop: 'var(--spacing-md)',
        }}>
          <button className="btn btn-secondary btn-sm" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page <= 1}>
            <i className="fas fa-chevron-left" /> Previous
          </button>
          <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
            Page {page} of {totalPages}
          </span>
          <button className="btn btn-secondary btn-sm" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page >= totalPages}>
            Next <i className="fas fa-chevron-right" />
          </button>
        </div>
      )}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />

      {/* Single popover instance for the split-button menu, anchored to
          whichever row's chevron is currently active. Reusing the existing
          Popover gives us .card surface + outside-click + Escape + focus
          return for free. */}
      <Popover
        anchor={splitMenuAnchorRef}
        open={splitMenuFor !== null}
        onClose={() => setSplitMenuFor(null)}
        ariaLabel="Install options"
      >
        <div className="action-menu">
          <button
            type="button"
            className="action-menu__item"
            onClick={() => {
              const b = backends[splitMenuFor]
              if (b) openPicker(b)
            }}
          >
            <i className="fas fa-server action-menu__icon" />
            Install on specific nodes…
          </button>
        </div>
      </Popover>

      <NodeInstallPicker
        open={!!pickerBackend}
        onClose={() => { setPickerBackend(null); setPickerInitialSelection([]) }}
        onComplete={() => { fetchBackends(); refetchNodes() }}
        backend={pickerBackend}
        nodes={clusterNodes}
        allBackends={allBackends}
        installedNodeIds={(pickerBackend?.nodes || []).map(n => n.node_id ?? n.NodeID)}
        initialSelection={pickerInitialSelection}
        addToast={addToast}
      />
    </div>
  )
}

function BackendDetailRow({ label, children }) {
  if (!children) return null
  return (
    <tr>
      <td style={{ fontWeight: 500, fontSize: '0.8125rem', color: 'var(--color-text-secondary)', whiteSpace: 'nowrap', verticalAlign: 'top', padding: '6px 12px 6px 0' }}>
        {label}
      </td>
      <td style={{ fontSize: '0.8125rem', padding: '6px 0' }}>{children}</td>
    </tr>
  )
}

function BackendDetail({ backend }) {
  return (
    <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)', background: 'var(--color-bg-primary)', borderTop: '1px solid var(--color-border-subtle)' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          <BackendDetailRow label="Description">
            {backend.description && (
              <div
                style={{ color: 'var(--color-text-secondary)', lineHeight: 1.6 }}
                dangerouslySetInnerHTML={{ __html: renderMarkdown(backend.description) }}
              />
            )}
          </BackendDetailRow>
          <BackendDetailRow label="Repository">
            {backend.gallery && (
              <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                {typeof backend.gallery === 'string' ? backend.gallery : backend.gallery.name || '-'}
              </span>
            )}
          </BackendDetailRow>
          <BackendDetailRow label="License">
            {backend.license && <span>{backend.license}</span>}
          </BackendDetailRow>
          <BackendDetailRow label="Tags">
            {backend.tags?.length > 0 && (
              <div style={{ display: 'flex', gap: 'var(--spacing-xs)', flexWrap: 'wrap' }}>
                {backend.tags.map(tag => (
                  <span key={tag} className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{tag}</span>
                ))}
              </div>
            )}
          </BackendDetailRow>
          <BackendDetailRow label="Links">
            {backend.urls?.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                {backend.urls.map((url, i) => (
                  <a key={i} href={url} target="_blank" rel="noopener noreferrer" style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', wordBreak: 'break-all' }}>
                    <i className="fas fa-external-link-alt" style={{ marginRight: 4, fontSize: '0.6875rem' }} />{url}
                  </a>
                ))}
              </div>
            )}
          </BackendDetailRow>
        </tbody>
      </table>
    </div>
  )
}
