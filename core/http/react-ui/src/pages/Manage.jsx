import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ResourceMonitor from '../components/ResourceMonitor'
import ConfirmDialog from '../components/ConfirmDialog'
import NodeDistributionChip from '../components/NodeDistributionChip'
import FilterBar from '../components/FilterBar'
import GalleryLoader from '../components/GalleryLoader'
import ManageSummary from '../components/ManageSummary'
import MetaBadgeRow from '../components/MetaBadgeRow'
import ActionMenu from '../components/ActionMenu'
import ResourceRow, { ChevronCell, IconCell, StopPropagationCell } from '../components/ResourceRow'
import { useModels } from '../hooks/useModels'
import { useGalleryEnrichment } from '../hooks/useGalleryEnrichment'
import { backendControlApi, modelsApi, backendsApi, systemApi, nodesApi } from '../utils/api'
import { renderMarkdown } from '../utils/markdown'
import {
  CAP_CHAT, CAP_COMPLETION, CAP_IMAGE, CAP_VIDEO, CAP_TTS,
  CAP_TRANSCRIPT, CAP_SOUND_GENERATION, CAP_FACE_RECOGNITION,
  CAP_SPEAKER_RECOGNITION, CAP_EMBEDDINGS, CAP_RERANK,
} from '../utils/capabilities'

const TABS = [
  { key: 'models', label: 'Models', icon: 'fa-brain' },
  { key: 'backends', label: 'Backends', icon: 'fa-server' },
]

// Capability → use-case badge. Entries with `route` become clickable links to
// the matching playground page; the rest render as informational badges.
// Order is the display order. CAP_CHAT covers CAP_COMPLETION too.
const USE_CASES = [
  { cap: CAP_CHAT,                label: 'Chat',       route: (id) => `/app/chat/${encodeURIComponent(id)}` },
  { cap: CAP_COMPLETION,          label: 'Completion', route: (id) => `/app/chat/${encodeURIComponent(id)}`, hideIf: CAP_CHAT },
  { cap: CAP_IMAGE,               label: 'Image',      route: (id) => `/app/image/${encodeURIComponent(id)}` },
  { cap: CAP_VIDEO,               label: 'Video',      route: (id) => `/app/video/${encodeURIComponent(id)}` },
  { cap: CAP_TTS,                 label: 'TTS',        route: (id) => `/app/tts/${encodeURIComponent(id)}` },
  { cap: CAP_TRANSCRIPT,          label: 'Transcribe', route: () => '/app/talk' },
  { cap: CAP_SOUND_GENERATION,    label: 'Sound',      route: (id) => `/app/sound/${encodeURIComponent(id)}` },
  { cap: CAP_FACE_RECOGNITION,    label: 'Face',       route: (id) => `/app/face/${encodeURIComponent(id)}` },
  { cap: CAP_SPEAKER_RECOGNITION, label: 'Voice',      route: (id) => `/app/voice/${encodeURIComponent(id)}` },
  { cap: CAP_EMBEDDINGS,          label: 'Embeddings' },
  { cap: CAP_RERANK,              label: 'Rerank' },
]

// Number of columns the expandable detail row spans, per tab. Kept as
// constants so adding/removing a column doesn't silently break the colSpan.
const MODELS_COLSPAN = 7 // chevron, icon, name, status, backend, use cases, actions

// formatInstalledAt renders an installed_at timestamp as a short relative/abs
// string suitable for dense tables. Returns the raw value if parsing fails so
// we never display "Invalid Date".
function formatInstalledAt(value) {
  if (!value) return '—'
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  const now = Date.now()
  const diffMin = Math.floor((now - d.getTime()) / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffMin < 60 * 24) return `${Math.floor(diffMin / 60)}h ago`
  if (diffMin < 60 * 24 * 30) return `${Math.floor(diffMin / (60 * 24))}d ago`
  return d.toISOString().slice(0, 10)
}

// formatInstalledAtFull returns the absolute ISO timestamp for tooltips.
function formatInstalledAtFull(value) {
  if (!value) return ''
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  return d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC'
}

// formatBackendVersion derives a single short identifier suitable for a dense
// "Version" cell. The runtime API doesn't carry a semver for OCI installs —
// it has digest, uri, or gallery_url instead — so showing "—" for everything
// imported via OCI was misleading. Order of preference: explicit version →
// short digest → OCI tag (the part after the last colon) → ocifile basename.
//
// Returns { label, full } where `full` is the unabridged value to expose via
// title attr / detail panel.
function formatBackendVersion(metadata) {
  if (!metadata) return { label: '—', full: '' }
  if (metadata.version) {
    return { label: `v${metadata.version}`, full: `version v${metadata.version}` }
  }
  if (metadata.digest) {
    // sha256:7b2a044a… — show the short hex form devs are used to.
    const m = /^(sha\d+:)?([a-f0-9]+)$/i.exec(metadata.digest)
    if (m) {
      const hex = m[2]
      return { label: hex.slice(0, 12), full: metadata.digest }
    }
    return { label: metadata.digest.slice(0, 12), full: metadata.digest }
  }
  const uri = metadata.uri || ''
  if (uri.startsWith('ocifile://')) {
    // Local OCI tarball — show the basename, not the full path.
    const path = uri.replace(/^ocifile:\/\//, '')
    const base = path.split('/').pop() || path
    return { label: base, full: uri }
  }
  if (uri) {
    // Registry ref like quay.io/foo/bar:tag → show the tag, full ref on hover.
    const tag = uri.includes(':') ? uri.slice(uri.lastIndexOf(':') + 1) : uri
    return { label: tag, full: uri }
  }
  return { label: '—', full: '' }
}

export default function Manage() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('admin')
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') || localStorage.getItem('manage-tab') || 'models'
  const [activeTab, setActiveTab] = useState(TABS.some(tab => tab.key === initialTab) ? initialTab : 'models')
  const { models, loading: modelsLoading, refetch: refetchModels } = useModels()
  const { enrichModel, enrichBackend } = useGalleryEnrichment()
  const [loadedModelIds, setLoadedModelIds] = useState(new Set())
  const [backends, setBackends] = useState([])
  const [backendsLoading, setBackendsLoading] = useState(true)
  const [reloading, setReloading] = useState(false)
  const [reinstallingBackends, setReinstallingBackends] = useState(new Set())
  const [upgrades, setUpgrades] = useState({})
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [distributedMode, setDistributedMode] = useState(false)
  const [togglingModels, setTogglingModels] = useState(new Set())
  const [pinningModels, setPinningModels] = useState(new Set())
  // Expanded row state — keyed by `${tab}:${id}` so switching tabs doesn't
  // collide and a single row is open at a time per tab.
  const [expandedKey, setExpandedKey] = useState(null)
  // Filter state per tab. Persisted in the URL query so switching tabs
  // doesn't lose the filter the operator just set.
  const [modelsSearch, setModelsSearch] = useState(() => searchParams.get('mq') || '')
  const [modelsFilter, setModelsFilter] = useState(() => searchParams.get('mf') || 'all')
  const [backendsSearch, setBackendsSearch] = useState(() => searchParams.get('bq') || '')
  const [backendsFilter, setBackendsFilter] = useState(() => searchParams.get('bf') || 'all')
  // Two independent toggles. Meta backends are always visible — they're the
  // entries operators configure against. `bv` controls platform-specific
  // concrete variants (e.g. llama-cpp-cuda12-12.4) that a meta backend
  // aliases on the host. `bd` controls pre-release `-development` builds.
  // The legacy `bm` flag (when both were bundled) maps onto both so old
  // deep-links land on the same view they used to.
  const [showVariants, setShowVariants] = useState(() => {
    const p = searchParams
    return p.get('bv') === '1' || p.get('bm') === '1'
  })
  const [showDevelopment, setShowDevelopment] = useState(() => {
    const p = searchParams
    return p.get('bd') === '1' || p.get('bm') === '1'
  })

  // Sync filter state into the URL so deep-links + tab switches survive.
  useEffect(() => {
    const p = new URLSearchParams(searchParams)
    const setOrDelete = (k, v) => { if (v && v !== 'all') p.set(k, v); else p.delete(k) }
    setOrDelete('mq', modelsSearch)
    setOrDelete('mf', modelsFilter)
    setOrDelete('bq', backendsSearch)
    setOrDelete('bf', backendsFilter)
    if (showVariants) p.set('bv', '1'); else p.delete('bv')
    if (showDevelopment) p.set('bd', '1'); else p.delete('bd')
    p.delete('bm')
    setSearchParams(p, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelsSearch, modelsFilter, backendsSearch, backendsFilter, showVariants, showDevelopment])

  const handleTabChange = (tab) => {
    setActiveTab(tab)
    setExpandedKey(null)
    localStorage.setItem('manage-tab', tab)
    setSearchParams({ tab })
  }

  // Switch tabs and pre-set a filter — wired into the StatCards so cards
  // double as shortcuts to a filtered slice instead of being purely visual.
  const handleSummaryClick = (tab, filter) => {
    setActiveTab(tab)
    setExpandedKey(null)
    localStorage.setItem('manage-tab', tab)
    if (tab === 'models') setModelsFilter(filter)
    if (tab === 'backends') setBackendsFilter(filter)
    const p = new URLSearchParams(searchParams)
    p.set('tab', tab)
    setSearchParams(p, { replace: true })
  }

  const toggleExpanded = (tab, id) => {
    const key = `${tab}:${id}`
    setExpandedKey(prev => (prev === key ? null : key))
  }

  const fetchLoadedModels = useCallback(async () => {
    try {
      const info = await systemApi.info()
      const loaded = Array.isArray(info?.loaded_models) ? info.loaded_models : []
      setLoadedModelIds(new Set(loaded.map(m => m.id)))
    } catch {
      setLoadedModelIds(new Set())
    }
  }, [])

  const fetchBackends = useCallback(async () => {
    try {
      setBackendsLoading(true)
      const data = await backendsApi.listInstalled()
      setBackends(Array.isArray(data) ? data : [])
    } catch {
      setBackends([])
    } finally {
      setBackendsLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchLoadedModels()
    fetchBackends()
    // Detect distributed mode (nodes API returns 503 when not enabled)
    nodesApi.list().then(() => setDistributedMode(true)).catch(() => {})
  }, [fetchLoadedModels, fetchBackends])

  // Auto-refresh the Models tab every 10s in distributed mode so ghost models
  // (loaded on a worker but absent from this frontend's in-memory cache)
  // clear on their own without the user clicking Update.
  const [lastSyncedAt, setLastSyncedAt] = useState(() => Date.now())
  const [nowTick, setNowTick] = useState(() => Date.now())
  useEffect(() => {
    if (!distributedMode || activeTab !== 'models') return
    const interval = setInterval(() => {
      refetchModels()
      fetchLoadedModels()
      setLastSyncedAt(Date.now())
    }, 10000)
    return () => clearInterval(interval)
  }, [distributedMode, activeTab, refetchModels, fetchLoadedModels])

  // Drive the "last synced Ns ago" label without over-rendering the table.
  useEffect(() => {
    if (!distributedMode) return
    const interval = setInterval(() => setNowTick(Date.now()), 1000)
    return () => clearInterval(interval)
  }, [distributedMode])
  const lastSyncedAgo = (() => {
    const s = Math.max(0, Math.floor((nowTick - lastSyncedAt) / 1000))
    if (s < 5) return 'just now'
    if (s < 60) return `${s}s ago`
    const m = Math.floor(s / 60)
    return `${m}m ago`
  })()

  // Fetch available backend upgrades
  useEffect(() => {
    if (activeTab === 'backends') {
      backendsApi.checkUpgrades()
        .then(data => setUpgrades(data || {}))
        .catch(() => {})
    }
  }, [activeTab])

  const handleStopModel = (modelName) => {
    setConfirmDialog({
      title: 'Stop Model',
      message: `Stop model ${modelName}?`,
      confirmLabel: 'Stop',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await backendControlApi.shutdown({ model: modelName })
          addToast(`Stopped ${modelName}`, 'success')
          setTimeout(fetchLoadedModels, 500)
        } catch (err) {
          addToast(`Failed to stop: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleDeleteModel = (modelName) => {
    setConfirmDialog({
      title: 'Delete Model',
      message: `Delete model ${modelName}? This cannot be undone.`,
      confirmLabel: 'Delete',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await modelsApi.deleteByName(modelName)
          addToast(`Deleted ${modelName}`, 'success')
          refetchModels()
          fetchLoadedModels()
        } catch (err) {
          addToast(`Failed to delete: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleToggleModel = async (modelId, currentlyDisabled) => {
    const action = currentlyDisabled ? 'enable' : 'disable'
    setTogglingModels(prev => new Set(prev).add(modelId))
    try {
      await modelsApi.toggleState(modelId, action)
      addToast(`Model ${modelId} ${action}d`, 'success')
      refetchModels()
      if (!currentlyDisabled) {
        // Model was just disabled, refresh loaded models since it may have been shut down
        setTimeout(fetchLoadedModels, 500)
      }
    } catch (err) {
      addToast(`Failed to ${action} model: ${err.message}`, 'error')
    } finally {
      setTogglingModels(prev => {
        const next = new Set(prev)
        next.delete(modelId)
        return next
      })
    }
  }

  const handleTogglePinned = async (modelId, currentlyPinned) => {
    const action = currentlyPinned ? 'unpin' : 'pin'
    setPinningModels(prev => new Set(prev).add(modelId))
    try {
      await modelsApi.togglePinned(modelId, action)
      addToast(`Model ${modelId} ${action}ned`, 'success')
      refetchModels()
    } catch (err) {
      addToast(`Failed to ${action} model: ${err.message}`, 'error')
    } finally {
      setPinningModels(prev => {
        const next = new Set(prev)
        next.delete(modelId)
        return next
      })
    }
  }

  const handleReload = async () => {
    setReloading(true)
    try {
      await modelsApi.reload()
      addToast('Models reloaded', 'success')
      setTimeout(() => { refetchModels(); fetchLoadedModels(); setReloading(false) }, 1000)
    } catch (err) {
      addToast(`Reload failed: ${err.message}`, 'error')
      setReloading(false)
    }
  }

  const handleReinstallBackend = async (name) => {
    try {
      setReinstallingBackends(prev => new Set(prev).add(name))
      await backendsApi.install(name)
      addToast(`Reinstalling ${name}...`, 'info')
    } catch (err) {
      addToast(`Failed to reinstall: ${err.message}`, 'error')
    } finally {
      setReinstallingBackends(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  const handleUpgradeBackend = async (name) => {
    try {
      setReinstallingBackends(prev => new Set(prev).add(name))
      await backendsApi.upgrade(name)
      addToast(`Upgrading ${name}...`, 'info')
    } catch (err) {
      addToast(`Failed to upgrade: ${err.message}`, 'error')
    } finally {
      setReinstallingBackends(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  const [upgradingAll, setUpgradingAll] = useState(false)
  const handleUpgradeAll = async () => {
    const names = Object.keys(upgrades)
    if (names.length === 0) return
    setUpgradingAll(true)
    try {
      // Serial upgrade — matches the gallery's Upgrade All behavior.
      // Each backend upgrade is itself a cluster-wide fan-out, so parallel
      // calls would multiply load on every worker.
      for (const name of names) {
        try {
          await backendsApi.upgrade(name)
        } catch (err) {
          addToast(`Upgrade failed for ${name}: ${err.message}`, 'error')
        }
      }
      addToast(`Upgrade started for ${names.length} backend${names.length === 1 ? '' : 's'}`, 'info')
    } finally {
      setUpgradingAll(false)
    }
  }

  const handleDeleteBackend = (name) => {
    setConfirmDialog({
      title: 'Delete Backend',
      message: `Delete backend ${name}?`,
      confirmLabel: 'Delete',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await backendsApi.deleteInstalled(name)
          addToast(`Deleted backend ${name}`, 'success')
          fetchBackends()
        } catch (err) {
          addToast(`Failed to delete backend: ${err.message}`, 'error')
        }
      },
    })
  }

  // Counts for the summary header — derived in-memory; no extra API calls.
  const runningCount = models.filter(m =>
    !m.disabled && (loadedModelIds.has(m.id) || (Array.isArray(m.loaded_on) && m.loaded_on.length > 0))
  ).length
  const updatesCount = Object.keys(upgrades).length

  return (
    <div className="page page--wide">
      <div className="page-header">
        <h1 className="page-title">{t('manage.title')}</h1>
        <p className="page-subtitle">{t('manage.subtitle')}</p>
      </div>

      {/* Resource Monitor */}
      <ResourceMonitor />

      {/* Summary */}
      <ManageSummary
        modelsCount={modelsLoading ? '—' : models.length}
        backendsCount={backendsLoading ? '—' : backends.length}
        runningCount={runningCount}
        updatesCount={updatesCount}
        onCardClick={handleSummaryClick}
      />

      {/* Tabs */}
      <div className="tabs" style={{ marginBottom: 'var(--spacing-md)' }}>
        {TABS.map(t => {
          const upgradeCount = t.key === 'backends' ? Object.keys(upgrades).length : 0
          return (
            <button
              key={t.key}
              className={`tab ${activeTab === t.key ? 'tab-active' : ''}`}
              onClick={() => handleTabChange(t.key)}
            >
              <i className={`fas ${t.icon}`} style={{ marginRight: 6 }} />
              {t.label}
              {t.key === 'models' && !modelsLoading && ` (${models.length})`}
              {t.key === 'backends' && !backendsLoading && ` (${backends.length})`}
              {upgradeCount > 0 && (
                <span className="tab-pill tab-pill--warning" title={`${upgradeCount} update${upgradeCount === 1 ? '' : 's'} available`}>
                  <i className="fas fa-arrow-up" /> {upgradeCount}
                </span>
              )}
            </button>
          )
        })}
      </div>

      {/* Models Tab */}
      {activeTab === 'models' && (() => {
        const MODEL_FILTERS = [
          { key: 'all',      label: 'All',      icon: 'fa-layer-group' },
          { key: 'running',  label: 'Running',  icon: 'fa-circle-play' },
          { key: 'idle',     label: 'Idle',     icon: 'fa-pause' },
          { key: 'disabled', label: 'Disabled', icon: 'fa-ban' },
          { key: 'pinned',   label: 'Pinned',   icon: 'fa-thumbtack' },
          ...(distributedMode ? [{ key: 'distributed', label: 'Distributed', icon: 'fa-server' }] : []),
        ]
        const passesFilter = (m) => {
          if (modelsFilter === 'running') return !m.disabled && (loadedModelIds.has(m.id) || (m.loaded_on && m.loaded_on.length > 0))
          if (modelsFilter === 'idle')    return !m.disabled && !loadedModelIds.has(m.id) && !(m.loaded_on && m.loaded_on.length > 0)
          if (modelsFilter === 'disabled') return !!m.disabled
          if (modelsFilter === 'pinned')   return !!m.pinned
          if (modelsFilter === 'distributed') return Array.isArray(m.loaded_on) && m.loaded_on.length > 0
          return true
        }
        const q = modelsSearch.trim().toLowerCase()
        const passesSearch = (m) => !q || (m.id || '').toLowerCase().includes(q) || (m.backend || '').toLowerCase().includes(q)
        const visibleModels = models.filter(m => passesFilter(m) && passesSearch(m))
        return (
      <div>
        <FilterBar
          search={modelsSearch}
          onSearchChange={setModelsSearch}
          searchPlaceholder="Search models by name or backend..."
          filters={MODEL_FILTERS}
          activeFilter={modelsFilter}
          onFilterChange={setModelsFilter}
          rightSlot={(
            <>
              {distributedMode && (
                <span className="cell-muted" title="Auto-refreshes every 10s in distributed mode so ghost models clear promptly">
                  <i className="fas fa-rotate" /> Last synced {lastSyncedAgo}
                </span>
              )}
              <button className="btn btn-secondary btn-sm" onClick={handleReload} disabled={reloading}>
                <i className={`fas ${reloading ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
                {reloading ? ' Updating...' : ' Update'}
              </button>
            </>
          )}
        />

        {modelsLoading ? (
          <GalleryLoader />
        ) : models.length === 0 ? (
          <div className="card" style={{ padding: 'var(--spacing-xl)', textAlign: 'center' }}>
            <i className="fas fa-exclamation-triangle" style={{ fontSize: '2rem', color: 'var(--color-warning)', marginBottom: 'var(--spacing-md)' }} />
            <h3 style={{ marginBottom: 'var(--spacing-sm)' }}>No models installed yet</h3>
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)' }}>
              Install a model from the gallery to get started.
            </p>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
              <button className="btn btn-primary btn-sm" onClick={() => navigate('/app/models')}>
                <i className="fas fa-store" /> Browse Gallery
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
                <i className="fas fa-upload" /> Import Model
              </button>
              <a className="btn btn-secondary btn-sm" href="https://localai.io" target="_blank" rel="noopener noreferrer">
                <i className="fas fa-book" /> Documentation
              </a>
            </div>
          </div>
        ) : visibleModels.length === 0 ? (
          <div className="empty-state">
            <i className="fas fa-filter" />
            <p>No models match the current filter.</p>
            <button className="btn btn-ghost btn-sm" onClick={() => { setModelsSearch(''); setModelsFilter('all') }}>Clear filters</button>
          </div>
        ) : (
          <div className="table-container">
            <table className="table">
              <thead>
                <tr>
                  <th style={{ width: 30 }}></th>
                  <th style={{ width: 64 }}></th>
                  <th>Model</th>
                  <th>Status</th>
                  <th>Backend</th>
                  <th>Use cases</th>
                  <th style={{ width: 40 }}></th>
                </tr>
              </thead>
              <tbody>
                {visibleModels.map(model => {
                  const enriched = enrichModel(model.id)
                  const isExpanded = expandedKey === `models:${model.id}`
                  const isRunning = loadedModelIds.has(model.id) || (Array.isArray(model.loaded_on) && model.loaded_on.length > 0)
                  const caps = Array.isArray(model.capabilities) ? model.capabilities : []
                  const matchedCaps = USE_CASES.filter(uc => caps.includes(uc.cap) && !(uc.hideIf && caps.includes(uc.hideIf)))
                  return (
                    <ResourceRow
                      key={model.id}
                      expanded={isExpanded}
                      onToggleExpand={() => toggleExpanded('models', model.id)}
                      colSpan={MODELS_COLSPAN}
                      dimmed={!!model.disabled}
                      detail={(
                        <ModelDetail
                          model={model}
                          enriched={enriched}
                          matchedCaps={matchedCaps}
                          distributedMode={distributedMode}
                          onNavigate={navigate}
                        />
                      )}
                    >
                      <ChevronCell expanded={isExpanded} />
                      <IconCell icon={enriched?.icon} fallback="fa-brain" alt="" />
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
                          <span style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>{model.id}</span>
                          {enriched?.description && (
                            <span className="resource-row__desc" title={enriched.description}>
                              {enriched.description}
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        <div className="cell-stack">
                          {model.disabled ? (
                            <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                              <i className="fas fa-ban" /> Disabled
                            </span>
                          ) : Array.isArray(model.loaded_on) && model.loaded_on.length > 0 ? (
                            <NodeDistributionChip nodes={model.loaded_on} context="models" />
                          ) : loadedModelIds.has(model.id) ? (
                            <span className="badge badge-success">
                              <i className="fas fa-circle" style={{ fontSize: '6px' }} /> Running
                            </span>
                          ) : (
                            <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                              <i className="fas fa-circle" style={{ fontSize: '6px' }} /> Idle
                            </span>
                          )}
                          {model.source === 'registry-only' && (
                            <span className="badge badge-warning" title="Discovered on a worker but not configured locally. Persist the config to make it permanent.">
                              <i className="fas fa-ghost" /> Adopted
                            </span>
                          )}
                          {model.pinned && (
                            <span className="badge badge-warning" title="Pinned — won't be idle-unloaded">
                              <i className="fas fa-thumbtack" /> Pinned
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        <span className="badge badge-info">{model.backend || 'Auto'}</span>
                      </td>
                      <td>
                        <div className="badge-row">
                          {matchedCaps.length === 0 ? (
                            <span className="cell-muted">—</span>
                          ) : matchedCaps.map(uc => uc.route ? (
                            <a
                              key={uc.cap}
                              href="#"
                              onClick={(e) => { e.preventDefault(); e.stopPropagation(); navigate(uc.route(model.id)) }}
                              className="badge badge-info"
                              style={{ textDecoration: 'none', cursor: 'pointer' }}
                            >{uc.label}</a>
                          ) : (
                            <span key={uc.cap} className="badge">{uc.label}</span>
                          ))}
                        </div>
                      </td>
                      <StopPropagationCell style={{ textAlign: 'right' }}>
                        <ActionMenu
                          ariaLabel={`Actions for ${model.id}`}
                          triggerLabel={`Actions for ${model.id}`}
                          items={[
                            { key: 'toggle', icon: model.disabled ? 'fa-toggle-on' : 'fa-toggle-off',
                              label: model.disabled ? 'Enable model' : 'Disable model',
                              onClick: () => handleToggleModel(model.id, model.disabled),
                              disabled: togglingModels.has(model.id) },
                            { key: 'stop', icon: 'fa-stop', label: 'Stop model',
                              onClick: () => handleStopModel(model.id), hidden: !isRunning },
                            { key: 'pin', icon: 'fa-thumbtack',
                              label: model.pinned ? 'Unpin (allow idle unload)' : 'Pin (prevent idle unload)',
                              onClick: () => handleTogglePinned(model.id, model.pinned),
                              disabled: pinningModels.has(model.id) || !!model.disabled },
                            { key: 'edit', icon: 'fa-pen-to-square', label: 'Edit configuration',
                              onClick: () => navigate(`/app/model-editor/${encodeURIComponent(model.id)}`) },
                            { key: 'logs', icon: 'fa-terminal', label: 'Backend logs',
                              onClick: () => navigate(`/app/backend-logs/${encodeURIComponent(model.id)}`),
                              hidden: distributedMode },
                            { divider: true },
                            { key: 'delete', icon: 'fa-trash', label: 'Delete model', danger: true,
                              onClick: () => handleDeleteModel(model.id) },
                          ]}
                        />
                      </StopPropagationCell>
                    </ResourceRow>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
        )
      })()}

      {/* Backends Tab */}
      {activeTab === 'backends' && (
      <div>
        {/* Upgrade banner — mirrors the gallery so operators can't miss updates */}
        {!backendsLoading && Object.keys(upgrades).length > 0 && (
          <div className="upgrade-banner">
            <div className="upgrade-banner__text">
              <i className="fas fa-arrow-up" />
              <span>
                {Object.keys(upgrades).length} backend{Object.keys(upgrades).length === 1 ? ' has' : 's have'} updates available
              </span>
            </div>
            <div className="upgrade-banner__actions">
              <button
                className="btn btn-primary btn-sm"
                onClick={handleUpgradeAll}
                disabled={upgradingAll}
              >
                <i className={`fas ${upgradingAll ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} />
                {upgradingAll ? ' Upgrading...' : ' Upgrade all'}
              </button>
            </div>
          </div>
        )}

        {backendsLoading ? (
          <GalleryLoader />
        ) : backends.length === 0 ? (
          <div className="card" style={{ padding: 'var(--spacing-xl)', textAlign: 'center' }}>
            <i className="fas fa-server" style={{ fontSize: '2rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-md)' }} />
            <h3 style={{ marginBottom: 'var(--spacing-sm)' }}>No backends installed yet</h3>
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)' }}>
              Install backends from the gallery to extend functionality.
            </p>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
              <button className="btn btn-primary btn-sm" onClick={() => navigate('/app/backends')}>
                <i className="fas fa-server" /> Browse Backend Gallery
              </button>
              <a className="btn btn-secondary btn-sm" href="https://localai.io/backends/" target="_blank" rel="noopener noreferrer">
                <i className="fas fa-book" /> Documentation
              </a>
            </div>
          </div>
        ) : (() => {
          // Production meta backends (e.g. "llama-cpp") are the surface
          // operators actually configure against — gallery enrichment marks
          // them isAlias=false/isDevelopment=false, so they pass both toggles.
          // Meta-dev entries (e.g. "llama-cpp-development") still carry
          // isDevelopment=true in the gallery and must be hidden by the
          // Development toggle just like concrete dev variants — don't
          // short-circuit on IsMeta or they leak through.
          const flagsFor = (b) => {
            const g = enrichBackend(b.Name)
            if (!g) return { variant: false, dev: false }
            return { variant: !!g.isAlias, dev: !!g.isDevelopment }
          }
          const isHidden = (b) => {
            const f = flagsFor(b)
            if (f.variant && !showVariants) return true
            if (f.dev && !showDevelopment) return true
            return false
          }
          const visibleBase = backends.filter(b => !isHidden(b))

          // Counts AFTER the meta/dev filter so the chip numbers reflect what
          // the user is actually about to filter into.
          const upgradableCount = visibleBase.filter(b => upgrades[b.Name]).length
          const userCount       = visibleBase.filter(b => !b.IsSystem).length
          const systemCount     = visibleBase.filter(b => b.IsSystem).length
          const offlineCount    = visibleBase.filter(b => {
            const n = b.Nodes || b.nodes || []
            return n.some(x => {
              const s = x.node_status || x.NodeStatus
              return s && s !== 'healthy' && s !== 'draining'
            })
          }).length
          // Per-toggle counts: how many items in this category are currently
          // hidden because of THIS toggle. A dev-variant counts in both —
          // that's fine, it tells the operator the category is non-empty.
          const hiddenVariantCount = showVariants ? 0 : backends.filter(b => flagsFor(b).variant).length
          const hiddenDevCount     = showDevelopment ? 0 : backends.filter(b => flagsFor(b).dev).length
          const hiddenTotal        = backends.length - visibleBase.length

          const BACKEND_FILTERS = [
            { key: 'all',        label: 'All',        icon: 'fa-layer-group',       count: visibleBase.length },
            { key: 'user',       label: 'User',       icon: 'fa-download',          count: userCount },
            { key: 'system',     label: 'System',     icon: 'fa-shield-alt',        count: systemCount },
            ...(upgradableCount > 0 ? [{ key: 'upgradable', label: 'Updates', icon: 'fa-arrow-up', count: upgradableCount }] : []),
            ...(distributedMode && offlineCount > 0 ? [{ key: 'offline', label: 'Offline nodes', icon: 'fa-exclamation-circle', count: offlineCount }] : []),
          ]
          const q = backendsSearch.trim().toLowerCase()
          const passesSearch = (b) => !q
            || (b.Name || '').toLowerCase().includes(q)
            || (b.Metadata?.alias || '').toLowerCase().includes(q)
            || (b.Metadata?.meta_backend_for || '').toLowerCase().includes(q)
          const passesFilter = (b) => {
            switch (backendsFilter) {
              case 'user':       return !b.IsSystem
              case 'system':     return !!b.IsSystem
              case 'upgradable': return !!upgrades[b.Name]
              case 'offline': {
                const n = b.Nodes || b.nodes || []
                return n.some(x => {
                  const s = x.node_status || x.NodeStatus
                  return s && s !== 'healthy' && s !== 'draining'
                })
              }
              default: return true
            }
          }
          const visibleBackends = visibleBase.filter(b => passesFilter(b) && passesSearch(b))
          // Polished column count: chevron, icon, name+badges, version,
          // installed, actions (+ optional nodes when distributed).
          const colSpan = distributedMode ? 7 : 6

          const filterBar = (
            <FilterBar
              search={backendsSearch}
              onSearchChange={setBackendsSearch}
              searchPlaceholder="Search backends by name or alias..."
              filters={BACKEND_FILTERS}
              activeFilter={backendsFilter}
              onFilterChange={setBackendsFilter}
              toggles={[
                {
                  key: 'variants',
                  label: hiddenVariantCount > 0 ? `Variants (${hiddenVariantCount})` : 'Variants',
                  icon: 'fa-cubes',
                  checked: showVariants,
                  onChange: () => setShowVariants(v => !v),
                },
                {
                  key: 'development',
                  label: hiddenDevCount > 0 ? `Development (${hiddenDevCount})` : 'Development',
                  icon: 'fa-flask',
                  checked: showDevelopment,
                  onChange: () => setShowDevelopment(v => !v),
                },
              ]}
            />
          )

          if (visibleBackends.length === 0) {
            return (
              <>
                {filterBar}
                <div className="empty-state">
                  <i className="fas fa-filter" />
                  <p>
                    No backends match the current filter.
                    {hiddenTotal > 0 && (
                      <> {hiddenTotal} backend{hiddenTotal === 1 ? ' is' : 's are'} hidden by the Variants/Development toggles — flip them on to reveal {hiddenTotal === 1 ? 'it' : 'them'}.</>
                    )}
                  </p>
                  <button className="btn btn-ghost btn-sm" onClick={() => { setBackendsSearch(''); setBackendsFilter('all') }}>Clear filters</button>
                </div>
              </>
            )
          }
          return (
          <>
            {filterBar}
            <div className="table-container">
            <table className="table">
              <thead>
                <tr>
                  <th style={{ width: 30 }}></th>
                  <th style={{ width: 64 }}></th>
                  <th>Backend</th>
                  <th>Version</th>
                  {distributedMode && <th>Nodes</th>}
                  <th>Installed</th>
                  <th style={{ width: 40 }}></th>
                </tr>
              </thead>
              <tbody>
                {visibleBackends.map((backend) => {
                  const upgradeInfo = upgrades[backend.Name]
                  const hasDrift = upgradeInfo?.node_drift?.length > 0
                  const nodes = backend.Nodes || backend.nodes || []
                  const enriched = enrichBackend(backend.Name)
                  const isExpanded = expandedKey === `backends:${backend.Name}`
                  const isDevelopment = !!(enriched?.isDevelopment)
                  const isProcessing = reinstallingBackends.has(backend.Name)
                  return (
                    <ResourceRow
                      key={backend.Name}
                      expanded={isExpanded}
                      onToggleExpand={() => toggleExpanded('backends', backend.Name)}
                      colSpan={colSpan}
                      detail={(
                        <BackendDetail
                          backend={backend}
                          enriched={enriched}
                          upgradeInfo={upgradeInfo}
                          nodes={nodes}
                          distributedMode={distributedMode}
                        />
                      )}
                    >
                      <ChevronCell expanded={isExpanded} />
                      <IconCell icon={enriched?.icon} fallback="fa-cogs" alt="" />
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', flexWrap: 'wrap' }}>
                            <span style={{ fontWeight: 600, fontSize: 'var(--text-sm)' }}>{backend.Name}</span>
                            <MetaBadgeRow
                              isSystem={!!backend.IsSystem}
                              isMeta={!!backend.IsMeta}
                              isDevelopment={isDevelopment}
                            />
                            {backend.Metadata?.alias && backend.Metadata.alias !== backend.Name && (
                              <span className="cell-subtle" style={{ marginLeft: 0 }}>· alias {backend.Metadata.alias}</span>
                            )}
                            {backend.Metadata?.meta_backend_for && (
                              <span className="cell-subtle" style={{ marginLeft: 0 }}>· for {backend.Metadata.meta_backend_for}</span>
                            )}
                          </div>
                          {(enriched?.description) && (
                            <span className="resource-row__desc" title={enriched.description}>
                              {enriched.description}
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        {(() => {
                          const v = formatBackendVersion(backend.Metadata)
                          return (
                            <div className="cell-stack">
                              <span className="cell-mono" title={v.full || undefined}>{v.label}</span>
                              {upgradeInfo && (
                                <span className="badge badge-warning" title={upgradeInfo.available_version ? `Upgrade to v${upgradeInfo.available_version}` : 'Update available'}>
                                  <i className="fas fa-arrow-up" />
                                  {upgradeInfo.available_version ? ` v${upgradeInfo.available_version}` : ' Update'}
                                </span>
                              )}
                              {hasDrift && (
                                <span
                                  className="badge badge-warning"
                                  title={`Drift: ${upgradeInfo.node_drift.map(d => `${d.node_name}${d.version ? ' v' + d.version : ''}`).join(', ')}`}
                                >
                                  <i className="fas fa-code-branch" />
                                  {' '}Drift: {upgradeInfo.node_drift.length} node{upgradeInfo.node_drift.length === 1 ? '' : 's'}
                                </span>
                              )}
                            </div>
                          )
                        })()}
                      </td>
                      {distributedMode && (
                        <td>
                          <NodeDistributionChip nodes={nodes} context="backends" />
                        </td>
                      )}
                      <td>
                        <span
                          className="cell-muted cell-mono"
                          title={backend.Metadata?.installed_at ? formatInstalledAtFull(backend.Metadata.installed_at) : undefined}
                        >
                          {backend.Metadata?.installed_at ? formatInstalledAt(backend.Metadata.installed_at) : '—'}
                        </span>
                      </td>
                      <StopPropagationCell style={{ textAlign: 'right' }}>
                        {backend.IsSystem ? (
                          <span className="badge" title="System backends are managed outside the gallery">
                            <i className="fas fa-lock" /> Protected
                          </span>
                        ) : (
                          <ActionMenu
                            ariaLabel={`Actions for ${backend.Name}`}
                            triggerLabel={`Actions for ${backend.Name}`}
                            items={[
                              { key: 'upgrade', icon: 'fa-arrow-up',
                                label: upgradeInfo?.available_version ? `Upgrade to v${upgradeInfo.available_version}` : 'Upgrade',
                                onClick: () => handleUpgradeBackend(backend.Name),
                                disabled: isProcessing,
                                hidden: !upgradeInfo },
                              { key: 'reinstall', icon: 'fa-rotate', label: 'Reinstall backend',
                                onClick: () => handleReinstallBackend(backend.Name),
                                disabled: isProcessing },
                              { divider: true },
                              { key: 'delete', icon: 'fa-trash',
                                label: 'Delete backend',
                                danger: true,
                                onClick: () => handleDeleteBackend(backend.Name) },
                            ]}
                          />
                        )}
                      </StopPropagationCell>
                    </ResourceRow>
                  )
                })}
              </tbody>
            </table>
            </div>
          </>
          )
        })()}
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
    </div>
  )
}

// ModelDetail renders the expanded panel for a Models row. It pulls richer
// fields (description, license, tags, links, files) from the gallery cache
// when available, and falls back gracefully for items not in the gallery.
function ModelDetail({ model, enriched, matchedCaps, distributedMode, onNavigate }) {
  const description = enriched?.description
  const license = enriched?.license
  const tags = Array.isArray(enriched?.tags) ? enriched.tags : []
  const urls = Array.isArray(enriched?.urls) ? enriched.urls : []
  const files = Array.isArray(enriched?.additionalFiles) ? enriched.additionalFiles
              : Array.isArray(enriched?.files) ? enriched.files
              : []
  const sizeDisplay = enriched?.estimated_size_display && enriched.estimated_size_display !== '0 B' ? enriched.estimated_size_display : null
  const vramDisplay = enriched?.estimated_vram_display && enriched.estimated_vram_display !== '0 B' ? enriched.estimated_vram_display : null

  return (
    <div className="resource-row__detail">
      <h4><i className="fas fa-circle-info" /> Details</h4>
      <dl className="resource-row__detail-grid">
        <dt>Description</dt>
        <dd>
          {description ? (
            <div
              className="resource-row__detail-md"
              dangerouslySetInnerHTML={{ __html: renderMarkdown(description) }}
            />
          ) : (
            <span className="cell-muted">No description available — this model isn't in the gallery.</span>
          )}
        </dd>

        <dt>Backend</dt>
        <dd>
          <span className="badge badge-info">{model.backend || 'Auto'}</span>
        </dd>

        {matchedCaps.length > 0 && (<>
          <dt>Capabilities</dt>
          <dd>
            <div className="badge-row">
              {matchedCaps.map(uc => uc.route ? (
                <a
                  key={uc.cap}
                  href="#"
                  onClick={(e) => { e.preventDefault(); onNavigate(uc.route(model.id)) }}
                  className="badge badge-info"
                  style={{ textDecoration: 'none', cursor: 'pointer' }}
                >{uc.label}</a>
              ) : (
                <span key={uc.cap} className="badge">{uc.label}</span>
              ))}
            </div>
          </dd>
        </>)}

        {(sizeDisplay || vramDisplay) && (<>
          <dt>Size / VRAM</dt>
          <dd>
            {sizeDisplay && <span style={{ marginRight: 'var(--spacing-md)' }}>Size: {sizeDisplay}</span>}
            {vramDisplay && <span>VRAM: {vramDisplay}</span>}
          </dd>
        </>)}

        {license && (<>
          <dt>License</dt>
          <dd>{license}</dd>
        </>)}

        {tags.length > 0 && (<>
          <dt>Tags</dt>
          <dd>
            <div className="badge-row">
              {tags.map(t => <span key={t} className="badge badge-info">{t}</span>)}
            </div>
          </dd>
        </>)}

        {urls.length > 0 && (<>
          <dt>Links</dt>
          <dd>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {urls.map((url, i) => (
                <a key={i} href={url} target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)', wordBreak: 'break-all', fontSize: 'var(--text-xs)' }}>
                  <i className="fas fa-external-link-alt" style={{ marginRight: 4, fontSize: '0.625rem' }} />{url}
                </a>
              ))}
            </div>
          </dd>
        </>)}

        {distributedMode && Array.isArray(model.loaded_on) && model.loaded_on.length > 0 && (<>
          <dt>Distributed</dt>
          <dd>
            <NodeDistributionChip nodes={model.loaded_on} context="models" compactThreshold={20} />
          </dd>
        </>)}

        {model.source && (<>
          <dt>Source</dt>
          <dd className="cell-muted">{model.source}</dd>
        </>)}

        {files.length > 0 && (<>
          <dt>Files</dt>
          <dd>
            <span className="cell-muted">{files.length} file{files.length === 1 ? '' : 's'}</span>
          </dd>
        </>)}
      </dl>
    </div>
  )
}

// BackendDetail renders the expanded panel for a Backends row. Gallery metadata
// (description, license, tags, repository, URLs) is layered on top of the
// runtime state from the installed list (version, drift, per-node info).
function BackendDetail({ backend, enriched, upgradeInfo, nodes, distributedMode }) {
  const description = enriched?.description
  const license = enriched?.license
  const tags = Array.isArray(enriched?.tags) ? enriched.tags : []
  const urls = Array.isArray(enriched?.urls) ? enriched.urls : []
  const repository = typeof enriched?.gallery === 'string'
    ? enriched.gallery
    : enriched?.gallery?.name

  return (
    <div className="resource-row__detail">
      <h4><i className="fas fa-circle-info" /> Details</h4>
      <dl className="resource-row__detail-grid">
        <dt>Description</dt>
        <dd>
          {description ? (
            <div
              className="resource-row__detail-md"
              dangerouslySetInnerHTML={{ __html: renderMarkdown(description) }}
            />
          ) : (
            <span className="cell-muted">No description available — this backend isn't in the gallery.</span>
          )}
        </dd>

        {repository && (<>
          <dt>Repository</dt>
          <dd>
            <span className="badge badge-info">{repository}</span>
          </dd>
        </>)}

        {license && (<>
          <dt>License</dt>
          <dd>{license}</dd>
        </>)}

        {tags.length > 0 && (<>
          <dt>Tags</dt>
          <dd>
            <div className="badge-row">
              {tags.map(t => <span key={t} className="badge badge-info">{t}</span>)}
            </div>
          </dd>
        </>)}

        {urls.length > 0 && (<>
          <dt>Links</dt>
          <dd>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {urls.map((url, i) => (
                <a key={i} href={url} target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)', wordBreak: 'break-all', fontSize: 'var(--text-xs)' }}>
                  <i className="fas fa-external-link-alt" style={{ marginRight: 4, fontSize: '0.625rem' }} />{url}
                </a>
              ))}
            </div>
          </dd>
        </>)}

        {backend.Metadata?.uri && (<>
          <dt>Source</dt>
          <dd>
            <span className="cell-mono" style={{ wordBreak: 'break-all' }}>{backend.Metadata.uri}</span>
          </dd>
        </>)}

        {backend.Metadata?.digest && (<>
          <dt>Digest</dt>
          <dd>
            <span className="cell-mono" style={{ wordBreak: 'break-all' }}>{backend.Metadata.digest}</span>
          </dd>
        </>)}

        {backend.Metadata?.installed_at && (<>
          <dt>Installed</dt>
          <dd>
            <span className="cell-mono">{formatInstalledAt(backend.Metadata.installed_at)}</span>
            <span className="cell-muted" style={{ marginLeft: 'var(--spacing-sm)' }}>
              ({formatInstalledAtFull(backend.Metadata.installed_at)})
            </span>
          </dd>
        </>)}

        {backend.Metadata?.alias && (<>
          <dt>Alias</dt>
          <dd className="cell-mono">{backend.Metadata.alias}</dd>
        </>)}

        {backend.Metadata?.meta_backend_for && (<>
          <dt>Meta for</dt>
          <dd className="cell-mono">{backend.Metadata.meta_backend_for}</dd>
        </>)}

        {distributedMode && nodes.length > 0 && (<>
          <dt>Nodes</dt>
          <dd>
            <NodeDistributionChip nodes={nodes} context="backends" compactThreshold={20} />
          </dd>
        </>)}

        {upgradeInfo?.node_drift?.length > 0 && (<>
          <dt>Drift</dt>
          <dd>
            <table className="table" style={{ margin: 0, fontSize: 'var(--text-xs)' }}>
              <thead>
                <tr><th>Node</th><th>Version</th></tr>
              </thead>
              <tbody>
                {upgradeInfo.node_drift.map((d, i) => (
                  <tr key={i}>
                    <td className="cell-mono">{d.node_name}</td>
                    <td className="cell-mono">{d.version ? `v${d.version}` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </dd>
        </>)}
      </dl>
    </div>
  )
}
