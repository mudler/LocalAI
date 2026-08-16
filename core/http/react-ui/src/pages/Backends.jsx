import { useState, useEffect, useCallback, useRef } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi, nodesApi } from '../utils/api'
import { useDebouncedCallback } from '../hooks/useDebounce'
import React from 'react'
import { useOperations } from '../hooks/useOperations'
import { useDistributedMode } from '../hooks/useDistributedMode'
import LoadingSpinner from '../components/LoadingSpinner'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import ConfirmDialog from '../components/ConfirmDialog'
import Toggle from '../components/Toggle'
import NodeDistributionChip from '../components/NodeDistributionChip'
import NodeInstallPicker from '../components/NodeInstallPicker'
import Popover from '../components/Popover'
import SplitView from '../components/split/SplitView'
import EntityRail from '../components/split/EntityRail'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { useResources } from '../hooks/useResources'
import { ENTITY_GROUPS, groupForEntity } from '../utils/entityGroups'
import InstalledBackends from './InstalledBackends'

export default function Backends() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [searchParams, setSearchParams] = useSearchParams()
  const activeView = searchParams.get('view') === 'installed' ? 'installed' : 'catalog'
  const { operations } = useOperations()
  const { resources } = useResources()
  const { enabled: distributedEnabled, nodes: clusterNodes, refetch: refetchNodes } = useDistributedMode()
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState(() => searchParams.get('q') || '')
  const [filter, setFilter] = useState(() => {
    const state = searchParams.get('state') || ''
    return ['', 'chat', 'image', 'video', 'tts', 'transcript', 'vision'].includes(state) ? state : ''
  })
  const [sortBy, setSortBy] = useState('name')
  const [sortOrder, setSortOrder] = useState('asc')
  const [page, setPage] = useState(1)
  const [installedCount, setInstalledCount] = useState(0)
  const [showManualInstall, setShowManualInstall] = useState(false)
  const [manualUri, setManualUri] = useState('')
  const [manualName, setManualName] = useState('')
  const [manualAlias, setManualAlias] = useState('')
  // Which backend the pane is showing, or null for the host page. In the URL
  // for the same reasons as Models Explore: a backend is linkable, and Back leaves
  // the detail rather than the page.
  // True once any listing has come back. Distinguishes a cold start, which has
  // nothing to keep on screen, from a refetch, which does.
  const loadedOnce = useRef(false)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [allBackends, setAllBackends] = useState([])
  const [upgrades, setUpgrades] = useState({})
  const [upgradingAll, setUpgradingAll] = useState(false)
  const [showAllBackends, setShowAllBackends] = useState(() => searchParams.get('show_all') === '1')
  const [showDevelopment, setShowDevelopment] = useState(() => searchParams.get('development') === '1')
  const [preferDevLoaded, setPreferDevLoaded] = useState(false)
  const [pickerBackend, setPickerBackend] = useState(null)
  const [pickerInitialSelection, setPickerInitialSelection] = useState([])
  const [splitMenuOpen, setSplitMenuOpen] = useState(false)
  const [catalogErrors, setCatalogErrors] = useState({})
  const [catalogGlobalError, setCatalogGlobalError] = useState('')
  const [manualError, setManualError] = useState('')
  // Anchor for the split-button chevron. One pane, so one anchor.
  const splitMenuAnchorRef = useRef(null)

  // Target-node mode: set when navigated from /app/nodes via "+ Add backend".
  // The gallery page header banners the scope; rows collapse their split-button
  // to a single Install-on-this-node action; manual install posts to the
  // per-node endpoint.
  const selectedName = searchParams.get('backend')

  const hrefForView = (view) => {
    const next = new URLSearchParams(searchParams)
    next.set('view', view)
    return `/app/backends?${next.toString()}`
  }

  const updateUrlParam = useCallback((key, value, defaultValue = '') => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (!value || value === defaultValue) next.delete(key)
      else next.set(key, value)
      return next
    }, { replace: true })
  }, [setSearchParams])

  useEffect(() => {
    if (activeView !== 'catalog') return
    const nextSearch = searchParams.get('q') || ''
    const requestedState = searchParams.get('state') || ''
    const nextFilter = ['', 'chat', 'image', 'video', 'tts', 'transcript', 'vision'].includes(requestedState)
      ? requestedState
      : ''
    setSearch(nextSearch)
    setFilter(nextFilter)
    setShowAllBackends(searchParams.get('show_all') === '1')
    setShowDevelopment(searchParams.get('development') === '1')
  }, [activeView, searchParams])

  // Selection is a URL edit that preserves everything else in the query, so it
  // composes with the target-node scope rather than clobbering it.
  const selectBackend = useCallback((name) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (name) next.set('backend', name)
      else next.delete('backend')
      return next
    }, { replace: !name })
    setSplitMenuOpen(false)
  }, [setSearchParams])

  const selectedBackend = selectedName
    ? (allBackends.find(b => (b.name || b.id) === selectedName) || null)
    : null

  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())
  const toggleGroup = useCallback((id) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

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
      if (activeView === 'catalog' && search) params.term = search
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
      loadedOnce.current = true
      setLoading(false)
    }
  }, [activeView, search, sortBy, sortOrder, addToast])

  const debouncedFetch = useDebouncedCallback(fetchBackends)

  useEffect(() => {
    debouncedFetch()
  }, [debouncedFetch, fetchBackends])

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
  const ITEMS_PER_PAGE = 60
  const totalPages = Math.max(1, Math.ceil(filteredBackends.length / ITEMS_PER_PAGE))
  const backends = filteredBackends.slice((page - 1) * ITEMS_PER_PAGE, page * ITEMS_PER_PAGE)

  const handleSearch = (value) => {
    setSearch(value)
    updateUrlParam('q', value)
    setPage(1)
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
    setCatalogErrors(current => ({ ...current, [id]: '' }))
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
      setCatalogErrors(current => ({ ...current, [id]: `Install failed: ${err.message}` }))
    }
  }

  // Install a single gallery backend on a specific node, used in target-node
  // mode (the URL has ?target=<node-id> set from the Nodes page entry point).
  // The handler is async - we dispatch and let the global Operations panel
  // surface progress; no need to await completion here.
  const handleInstallOnTarget = async (id) => {
    if (!targetNode) return
    setCatalogErrors(current => ({ ...current, [id]: '' }))
    try {
      await nodesApi.installBackend(targetNode.id, id)
      addToast(`Installing ${id} on ${targetNode.name}...`, 'info')
      // The install runs async via the gallery job queue. Refetch shortly so
      // the Nodes column reflects "installing" state; the Operations panel
      // tracks the actual progress until completion.
      setTimeout(() => { fetchBackends(); refetchNodes() }, 1200)
    } catch (err) {
      setCatalogErrors(current => ({
        ...current,
        [id]: `Install dispatch failed on ${targetNode.name}: ${err.message}`,
      }))
    }
  }

  const openPicker = (b, initialSelection = []) => {
    setPickerBackend(b)
    setPickerInitialSelection(initialSelection)
    setSplitMenuOpen(false)
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
        setCatalogErrors(current => ({ ...current, [id]: '' }))
        try {
          await backendsApi.delete(id)
          addToast(`Deleting ${id}...`, 'info')
          setTimeout(fetchBackends, 1000)
        } catch (err) {
          setCatalogErrors(current => ({ ...current, [id]: `Delete failed: ${err.message}` }))
        }
      },
    })
  }

  const handleUpgrade = async (id) => {
    setCatalogErrors(current => ({ ...current, [id]: '' }))
    try {
      await backendsApi.upgrade(id)
      addToast(`Upgrading ${id}...`, 'info')
    } catch (err) {
      setCatalogErrors(current => ({ ...current, [id]: `Upgrade failed: ${err.message}` }))
    }
  }

  const handleUpgradeAll = async () => {
    const names = Object.keys(upgrades)
    if (names.length === 0) return
    setUpgradingAll(true)
    setCatalogGlobalError('')
    try {
      for (const name of names) {
        await backendsApi.upgrade(name)
      }
      addToast(`Upgrading ${names.length} backend${names.length > 1 ? 's' : ''}...`, 'info')
    } catch (err) {
      setCatalogGlobalError(`Upgrade failed: ${err.message}`)
    } finally {
      setUpgradingAll(false)
    }
  }

  const handleManualInstall = async (e) => {
    e.preventDefault()
    setManualError('')
    if (!manualUri.trim()) { setManualError('Please enter a URI'); return }
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
      setManualError(`Install failed: ${err.message}`)
    }
  }

  // Check if a backend has an active operation
  const getBackendOp = (backend) => {
    if (!operations.length) return null
    return operations.find(op => op.name === backend.name || op.name === backend.id) || null
  }

  const handleToggleAllBackends = () => {
    setShowAllBackends(value => {
      updateUrlParam('show_all', value ? '' : '1')
      return !value
    })
    setPage(1)
  }
  const handleToggleDev = () => {
    setShowDevelopment(value => {
      updateUrlParam('development', value ? '' : '1')
      return !value
    })
    setPage(1)
  }

  const FILTERS = [
    { key: '', label: 'All', icon: 'fa-layer-group' },
    { key: 'chat', label: 'Chat', icon: 'fa-brain' },
    { key: 'image', label: 'Image', icon: 'fa-image' },
    { key: 'video', label: 'Video', icon: 'fa-video' },
    { key: 'tts', label: 'TTS', icon: 'fa-microphone' },
    { key: 'transcript', label: 'STT', icon: 'fa-headphones' },
    { key: 'vision', label: 'Vision', icon: 'fa-eye' },
  ]

  return (
    <div className="page page--wide page--app">
      <div className="view-bar">
        <h1 className="view-bar__title">{t('backends.title')}</h1>
        {activeView === 'catalog' && (
          <span className="view-bar__count">{backends.length} of {allBackends.length}</span>
        )}
        {activeView === 'catalog' && (
          <div className="view-bar__actions">
            {Object.keys(upgrades).length > 0 && (
              <button className="btn btn-primary btn-sm" onClick={handleUpgradeAll} disabled={upgradingAll}>
                <i className={`fas ${upgradingAll ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} /> Upgrade all ({Object.keys(upgrades).length})
              </button>
            )}
            <button className="btn btn-secondary btn-sm" onClick={() => setShowManualInstall(!showManualInstall)}>
              <i className={`fas ${showManualInstall ? 'fa-chevron-up' : 'fa-plus'}`} /> Manual Install
            </button>
          </div>
        )}
      </div>

      <nav className="tabs" aria-label={t('backends.lifecycle.navigation')}>
        <Link
          className={`tab ${activeView === 'catalog' ? 'tab-active' : ''}`}
          to={hrefForView('catalog')}
          aria-current={activeView === 'catalog' ? 'page' : undefined}
        >
          <i className="fas fa-layer-group icon-before" aria-hidden="true" />
          {t('backends.lifecycle.catalog')}
        </Link>
        <Link
          className={`tab ${activeView === 'installed' ? 'tab-active' : ''}`}
          to={hrefForView('installed')}
          aria-current={activeView === 'installed' ? 'page' : undefined}
        >
          <i className="fas fa-server icon-before" aria-hidden="true" />
          {t('backends.lifecycle.installed')}
        </Link>
      </nav>

      {/* Target-node banner: when this gallery is scoped to one node via
          ?target=<id> (entered from /app/nodes), show the scope clearly and
          give a fast way to clear it. Visually a primary-tinted strip so the
          user knows they're in a special mode without it feeling alarming. */}
      {activeView === 'catalog' && targetNode && (
        <div className="card bk-notice tone-primary mb-md">
          <i className="fas fa-bullseye" style={{ color: 'var(--color-primary)' }} />
          <span className="bk-notice__text">
            Installing only on <span style={{ fontFamily: 'var(--font-mono)' }}>{targetNode.name}</span>
          </span>
          <span style={{ flex: 1 }} />
          <button className="btn btn-ghost btn-sm" type="button" onClick={clearTarget}>
            <i className="fas fa-times" /> Clear
          </button>
        </div>
      )}

      {activeView === 'installed' ? (
        <InstalledBackends
          addToast={addToast}
          catalogBackends={allBackends}
          distributedEnabled={distributedEnabled}
          operations={operations}
          upgrades={upgrades}
          selectedName={selectedName}
          onSelect={selectBackend}
        />
      ) : (
        <>
      {catalogGlobalError && (
        <div className="attention-callout attention-callout--error mb-md" role="alert">
          <i className="fas fa-circle-exclamation" aria-hidden="true" />
          <span>{catalogGlobalError}</span>
        </div>
      )}

      {/* Upgrade Banner */}
      {Object.keys(upgrades).length > 0 && (
        <div className="card bk-notice bk-notice--between tone-warning mb-md">
          <div className="hstack hstack--sm">
            <i className="fas fa-arrow-up bk-notice__icon" />
            <span className="bk-notice__text">
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

      {showManualInstall && (
        <form onSubmit={handleManualInstall} className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
          <h3 className="text-base fw-semibold mb-sm">
            <i className="fas fa-download" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
            Install External Backend
          </h3>
          <div className="bk-manual-grid">
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
          {manualError && <p className="form-error" role="alert">{manualError}</p>}
        </form>
      )}

      {/* The gallery, as a rail and a pane. Same shell as Models Explore, because it
          is the same defect: a seven-column table whose expand-row was the
          only place the repository, licence, tags and links could go. */}
      {loading && !loadedOnce.current ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
      ) : (
        <SplitView
          testId="backends"
          detail={!!selectedBackend}
          rail={
            <>
            {/* The filters narrow the rail and nothing else, so they live with it. */}
            <div className="bk-filters">
              <div className="search-bar search-grow">
                <i className="fas fa-search search-icon" />
                <input className="input" placeholder="Search backends by name, description, or type..." value={search} onChange={(e) => handleSearch(e.target.value)} />
              </div>
            </div>

            <div className="bk-filters">
              <div className="filter-bar m-0 flex-1">
                {FILTERS.map(f => (
                  <button
                    key={f.key}
                    className={`filter-btn ${filter === f.key ? 'active' : ''}`}
                    onClick={() => { setFilter(f.key); updateUrlParam('state', f.key); setPage(1) }}
                  >
                  <i className={`fas ${f.icon}`} style={{ marginRight: 4 }} />
                  {f.label}
                </button>
              ))}
              </div>

              <span className="models-filters__refine-label">Refine</span>
              <div className="bk-toggles">
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
              <EntityRail
                items={backends.map(b => railItemForBackend(b, { getBackendOp, upgrades }))}
                groups={ENTITY_GROUPS.map(g => ({ id: g.id, label: BACKEND_GROUP_LABELS[g.id], icon: g.icon }))}
                grouped={!search.trim()}
                collapsedGroups={collapsedGroups}
                onToggleGroup={toggleGroup}
                busy={loading}
                selectedId={selectedName}
                onSelect={selectBackend}
                countLabel={`${backends.length} of ${allBackends.length}`}
                ariaLabel="Backends"
                testId="backends-rail"
                actions={
                  <div className="entity-rail__sort" role="group" aria-label="Sort backends">
                    <BackendSortButton col="name" label="Name" sortBy={sortBy} sortOrder={sortOrder} onSort={handleSort} />
                    <BackendSortButton col="status" label="Status" sortBy={sortBy} sortOrder={sortOrder} onSort={handleSort} />
                  </div>
                }
              />

              {totalPages > 1 && (
                <div className="pagination split-view__pager">
                  <button className="pagination-btn" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page <= 1} aria-label="Previous page">
                    <i className="fas fa-chevron-left" />
                  </button>
                  <span className="split-view__pager-label">{page} / {totalPages}</span>
                  <button className="pagination-btn" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page >= totalPages} aria-label="Next page">
                    <i className="fas fa-chevron-right" />
                  </button>
                </div>
              )}
            </>
          }
          pane={backends.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-server" /></div>
              <h2 className="empty-state-title">No backends found</h2>
              <p className="empty-state-text">
                {search || filter ? 'Try adjusting your search or filters.' : 'No backends available in the gallery.'}
              </p>
            </div>
          ) : selectedBackend ? (() => {
            const b = selectedBackend
            const name = b.name || b.id
            const op = getBackendOp(b)
            const isProcessing = !!op && !op.error
            const upgrade = upgrades[name]

            return (
              <div className="detail-pane">
                <DetailHeader
                  testId="backends"
                  icon={groupForEntity(b).icon}
                  name={name}
                  lede={b.description ? stripMarkdown(b.description).slice(0, 220) : null}
                  ledeTitle={b.description ? stripMarkdown(b.description) : null}
                  onBack={() => selectBackend(null)}
                  backLabel="All backends"
                  actions={
                    isProcessing ? (
                      <div className="inline-install">
                        <div className="inline-install__row">
                          <div className="operation-spinner" />
                          <span className="inline-install__label">
                            {op.isDeletion ? 'Deleting...' : op.isQueued ? 'Queued' : `Installing${op.progress > 0 ? ` · ${Math.round(op.progress)}%` : '...'}`}
                          </span>
                        </div>
                        {op.progress > 0 && (
                          <div className="operation-bar-container bk-progress">
                            <div className="operation-bar" style={{ width: `${op.progress}%` }} />
                          </div>
                        )}
                      </div>
                    ) : targetNode ? (
                      // Target-node mode: one per-node action. The split button
                      // is overkill when the URL has already pinned the scope.
                      (b.nodes || []).some(n => (n.node_id ?? n.NodeID) === targetNode.id) ? (
                        <>
                          <button className="btn btn-secondary btn-sm" onClick={() => handleInstallOnTarget(name)} title={`Reinstall on ${targetNode.name}`}>
                            <i className="fas fa-rotate" /> Reinstall
                          </button>
                          <button className="btn btn-danger btn-sm" onClick={async () => {
                            setCatalogErrors(current => ({ ...current, [name]: '' }))
                            try {
                              await nodesApi.deleteBackend(targetNode.id, name)
                              addToast(`Removed ${b.name} from ${targetNode.name}`, 'success')
                              setTimeout(() => { fetchBackends(); refetchNodes() }, 600)
                            } catch (err) {
                              setCatalogErrors(current => ({ ...current, [name]: `Remove failed: ${err.message}` }))
                            }
                          }} title={`Remove from ${targetNode.name}`}>
                            <i className="fas fa-trash" /> Remove
                          </button>
                        </>
                      ) : (
                        <button className="btn btn-primary btn-sm" onClick={() => handleInstallOnTarget(name)} data-testid="backends-install">
                          <i className="fas fa-download" /> Install on {targetNode.name}
                        </button>
                      )
                    ) : b.installed ? (
                      <>
                        {upgrade && (
                          <button className="btn btn-primary btn-sm" onClick={() => handleUpgrade(name)} title={`Upgrade to ${upgrade.available_version ? 'v' + upgrade.available_version : 'latest'}`}>
                            <i className="fas fa-arrow-up" /> Upgrade
                          </button>
                        )}
                        <button className="btn btn-secondary btn-sm" onClick={() => handleInstall(name)} title="Reinstall">
                          <i className="fas fa-rotate" /> Reinstall
                        </button>
                        <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name)} title="Delete">
                          <i className="fas fa-trash" /> Delete
                        </button>
                      </>
                    ) : distributedEnabled ? (
                      // Auto-resolving (meta) entries keep fan-out as the
                      // primary; a hardware-specific build routes straight to
                      // the picker, because fanning a CPU build out to every
                      // node is the silent footgun this guard exists to stop.
                      b.isMeta ? (
                        <div className="inline-flex">
                          <button className="btn btn-primary btn-sm" onClick={() => handleInstall(name)} title="Install on all nodes" style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }} data-testid="backends-install">
                            <i className="fas fa-download" /> Install on all
                          </button>
                          <button
                            ref={splitMenuAnchorRef}
                            className="btn btn-primary btn-sm bk-split-btn"
                            onClick={() => setSplitMenuOpen(v => !v)}
                            aria-haspopup="menu"
                            aria-expanded={splitMenuOpen}
                            aria-label="More install options"
                          >
                            <i className={`fas fa-chevron-${splitMenuOpen ? 'up' : 'down'}`} style={{ fontSize: '0.6875rem' }} />
                          </button>
                        </div>
                      ) : (
                        <button className="btn btn-primary btn-sm" onClick={() => openPicker(b)} title="Choose nodes to install on" data-testid="backends-install">
                          <i className="fas fa-server" /> Choose nodes…
                        </button>
                      )
                    ) : (
                      <button className="btn btn-primary btn-sm" onClick={() => handleInstall(name)} title="Install" data-testid="backends-install">
                        <i className="fas fa-download" /> Install
                      </button>
                    )
                  }
                />

                {catalogErrors[name] && (
                  <div className="attention-callout attention-callout--error" role="alert">
                    <i className="fas fa-circle-exclamation" aria-hidden="true" />
                    <span>{catalogErrors[name]}</span>
                  </div>
                )}

                <StatGrid
                  stats={[
                    { label: 'Installed', value: b.installed ? (b.version ? `v${b.version}` : 'yes') : 'no', tone: b.installed ? 'ok' : undefined },
                    upgrade ? { label: 'Available', value: upgrade.available_version ? `v${upgrade.available_version}` : 'update', tone: 'warn' } : null,
                    { label: 'License', value: b.license || '—' },
                    { label: 'Repository', value: b.gallery ? (typeof b.gallery === 'string' ? b.gallery : b.gallery.name || '—') : '—' },
                  ]}
                />

                {/* Distribution is the one fact the pane can state that a row
                    never could: which nodes hold a copy, and which do not. */}
                {distributedEnabled && !targetNode && (
                  <div>
                    <span className="detail-pane__label">Installed on</span>
                    <div className="hstack hstack--xs">
                      <NodeDistributionChip nodes={b.nodes || []} />
                      {(() => {
                        const missing = missingNodesFor(b)
                        if (missing.length === 0 || isProcessing) return null
                        return (
                          <button
                            type="button"
                            className="btn btn-ghost btn-sm"
                            onClick={() => openPicker(b, missing)}
                            aria-label="Install on more nodes"
                          >
                            <i className="fas fa-plus" style={{ fontSize: '0.6875rem' }} /> {missing.length} more
                          </button>
                        )
                      })()}
                    </div>
                  </div>
                )}

                <BackendDetail backend={b} />
              </div>
            )
          })() : (
            <BackendHostPane
              resources={resources}
              backends={allBackends}
              installedCount={installedCount}
              upgrades={upgrades}
              onSelect={selectBackend}
              onUpgradeAll={handleUpgradeAll}
              upgradingAll={upgradingAll}
            />
          )}
        />
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

      {/* The split-button menu, anchored to the pane's own chevron. It used to
          be re-anchored per row; there is one selected backend now, so there is
          one anchor. Popover still gives us the card surface, outside-click,
          Escape and focus return. */}
      <Popover
        anchor={splitMenuAnchorRef}
        open={splitMenuOpen}
        onClose={() => setSplitMenuOpen(false)}
        ariaLabel="Install options"
      >
        <div className="action-menu">
          <button
            type="button"
            className="action-menu__item"
            onClick={() => {
              setSplitMenuOpen(false)
              if (selectedBackend) openPicker(selectedBackend)
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
        </>
      )}
    </div>
  )
}

function BackendDetailRow({ label, children }) {
  if (!children) return null
  return (
    <tr>
      <td className="bk-detail__label">
        {label}
      </td>
      <td className="bk-detail__value">{children}</td>
    </tr>
  )
}

function BackendDetail({ backend }) {
  return (
    <div className="bk-detail">
      <table className="bk-detail__table">
        <tbody>
          <BackendDetailRow label="Description">
            {backend.description && (
              <div
                className="markdown-body"
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
              <div className="hstack hstack--xs">
                {backend.tags.map(tag => (
                  <span key={tag} className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{tag}</span>
                ))}
              </div>
            )}
          </BackendDetailRow>
          <BackendDetailRow label="Links">
            {backend.urls?.length > 0 && (
              <div className="stack stack--xs">
                {backend.urls.map((url, i) => (
                  <a key={i} href={safeHref(url)} target="_blank" rel="noopener noreferrer" className="text-sm text-primary wrap-anywhere">
                    <i className="fas fa-external-link-alt icon-before text-xs" />{url}
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

// The rail line spends its one fact on the thing that decides what you do
// next: whether it is here, whether it is stale, whether it is moving.
function railItemForBackend(backend, { getBackendOp, upgrades }) {
  const name = backend.name || backend.id
  const op = getBackendOp(backend)
  const processing = !!op && !op.error
  const upgrade = upgrades[name]

  let meta = backend.version ? `v${backend.version}` : 'not installed'
  let metaTone
  if (processing) {
    meta = op.isDeletion ? 'deleting' : op.isQueued ? 'queued' : `installing${op.progress > 0 ? ` ${Math.round(op.progress)}%` : ''}`
    metaTone = 'busy'
  } else if (upgrade) {
    meta = upgrade.available_version ? `v${backend.version} → v${upgrade.available_version}` : 'update available'
    metaTone = 'warn'
  } else if (backend.installed) {
    meta = backend.version ? `v${backend.version} · installed` : 'installed'
    metaTone = 'ok'
  }

  return { id: name, name, icon: groupForEntity(backend).icon, meta, metaTone, groupId: groupForEntity(backend).id }
}

function BackendSortButton({ col, label, sortBy, sortOrder, onSort }) {
  const active = sortBy === col
  return (
    <button
      type="button"
      className={`entity-rail__sort-btn${active ? ' active' : ''}`}
      aria-pressed={active}
      onClick={() => onSort(col)}
    >
      {label}
      {active && <i className={`fas fa-arrow-${sortOrder === 'asc' ? 'up' : 'down'}`} aria-hidden="true" />}
    </button>
  )
}

// BackendHostPane is the pane with nothing selected.
//
// A backend's fitness is not free memory, it is the accelerator and platform it
// was built for, so this leads with what the host actually is. That is the
// question the table never answered: it listed 37 runtimes and left "which of
// these can even run here" entirely to the reader.
function BackendHostPane({ resources, backends, installedCount, upgrades, onSelect, onUpgradeAll, upgradingAll }) {
  const gpu = resources?.gpus?.[0]
  const accelerator = gpu ? `${gpu.name}${gpu.vendor ? ` (${gpu.vendor})` : ''}` : null
  const staleNames = Object.keys(upgrades)
  // Not installed, and near the top of whatever order the gallery returned,
  // which is the gallery's own notion of prominence rather than one invented
  // here. Anything cleverer needs a ranking rule somebody owns.
  const suggestions = backends.filter(b => !b.installed).slice(0, 3)

  return (
    <div className="zero-pane">
      <div className="zero-pane__hero">
        <span className="zero-pane__eyebrow">This host</span>
        <h2 className="zero-pane__title">
          {accelerator
            ? `${accelerator}. ${backends.length} backends in the gallery, ${installedCount} installed.`
            : `${backends.length} backends in the gallery, ${installedCount} installed.`}
        </h2>
        <p className="zero-pane__text">
          A backend is a runtime, so what decides it is your accelerator and platform rather than free memory.
          Pick one on the left for its builds, licence and repository.
        </p>
      </div>

      {staleNames.length > 0 && (
        <div className="zero-pane__alert zero-pane__alert--warn">
          <i className="fas fa-arrow-up" aria-hidden="true" />
          <span>
            {staleNames.length === 1
              ? '1 installed backend has a newer build.'
              : `${staleNames.length} installed backends have a newer build.`}
            {' '}{staleNames.slice(0, 3).join(', ')}{staleNames.length > 3 ? '…' : ''}
          </span>
          <button className="btn btn-secondary btn-sm" onClick={onUpgradeAll} disabled={upgradingAll}>
            <i className={`fas ${upgradingAll ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} /> Upgrade all
          </button>
        </div>
      )}

      {suggestions.length > 0 && (
        <div className="zero-pane__shelf">
          <div className="zero-pane__shelf-head">
            <h3 className="zero-pane__shelf-title">Not installed yet</h3>
            <span className="zero-pane__shelf-meta">{backends.length - installedCount} available</span>
          </div>
          <div className="zero-pane__tiles">
            {suggestions.map((b, i) => {
              const name = b.name || b.id
              return (
                <button
                  type="button"
                  key={name}
                  className={`zero-pane__tile${i === 0 ? ' zero-pane__tile--feat' : ''}`}
                  onClick={() => onSelect(name)}
                >
                  <span className="hstack hstack--xs">
                    <i className={`fas ${groupForEntity(b).icon}`} aria-hidden="true" />
                    <span className="zero-pane__tile-name">{name}</span>
                  </span>
                  <span className="text-sm text-muted">{stripMarkdown(b.description).slice(0, 90) || '—'}</span>
                  <span className="zero-pane__tile-foot">
                    <span className="badge badge--tiny badge--soft">{b.license || 'no licence'}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

// Backends is not translated (6 t() calls in the whole page), so the shared
// group ids get literal labels here rather than i18n keys.
const BACKEND_GROUP_LABELS = {
  text: 'Text and reasoning',
  vision: 'Vision',
  audio: 'Speech and audio',
  visual: 'Image and video',
  other: 'Everything else',
}
