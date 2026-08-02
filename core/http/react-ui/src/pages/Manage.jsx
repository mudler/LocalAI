import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate, useOutletContext, useSearchParams, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import ResourceMonitor from '../components/ResourceMonitor'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import NodeDistributionChip from '../components/NodeDistributionChip'
import FilterBar from '../components/FilterBar'
import GalleryLoader from '../components/GalleryLoader'
import ManageSummary from '../components/ManageSummary'
import MetaBadgeRow from '../components/MetaBadgeRow'
import ActionMenu from '../components/ActionMenu'
import SplitView from '../components/split/SplitView'
import EntityRail from '../components/split/EntityRail'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { useModels } from '../hooks/useModels'
import { useGalleryEnrichment } from '../hooks/useGalleryEnrichment'
import { useOperations } from '../hooks/useOperations'
import { backendControlApi, modelsApi, backendsApi, systemApi, nodesApi } from '../utils/api'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import {
  CAP_CHAT, CAP_COMPLETION, CAP_IMAGE, CAP_VIDEO, CAP_TTS,
  CAP_TRANSCRIPT, CAP_SOUND_GENERATION, CAP_FACE_RECOGNITION,
  CAP_SPEAKER_RECOGNITION, CAP_EMBEDDINGS, CAP_RERANK,
  CAP_VAD, CAP_SCORE,
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
  // Display-only badges (no playground page): infrastructure
  // capabilities the operator declares but doesn't directly drive
  // from the UI. VAD feeds the transcribe pipeline; Score feeds
  // the router classifier — both are wired through other model
  // configs and need to be visible here so operators can confirm
  // the underlying model declares the right known_usecases.
  { cap: CAP_VAD,                 label: 'VAD' },
  { cap: CAP_SCORE,               label: 'Score' },
]

// Number of columns the expandable detail row spans, per tab. Kept as
// constants so adding/removing a column doesn't silently break the colSpan.

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

// Gallery descriptions are Markdown. The row preview is a single truncated
// line, so it shows the text without the syntax; the full Markdown is rendered
// in the expanded detail panel instead.

export default function Manage() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('admin')
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') || localStorage.getItem('manage-tab') || 'models'
  const [activeTab, setActiveTab] = useState(TABS.some(tab => tab.key === initialTab) ? initialTab : 'models')
  const { models, loading: modelsLoading, refetch: refetchModels } = useModels()
  const { enrichModel, enrichBackend } = useGalleryEnrichment()
  const { operations } = useOperations()
  const [loadedModelIds, setLoadedModelIds] = useState(new Set())
  // Map of alias name -> target. The capabilities endpoint that feeds the row
  // list doesn't carry the alias field, so we fetch it once and look rows up by
  // name to render the read-only "alias -> target" badge.
  const [aliasTargets, setAliasTargets] = useState({})
  const [backends, setBackends] = useState([])
  const [backendsLoading, setBackendsLoading] = useState(true)
  // See Models.jsx: a cold start has nothing to keep, a refetch does.
  const modelsLoadedOnce = useRef(false)
  const backendsLoadedOnce = useRef(false)
  const [reloading, setReloading] = useState(false)
  const [reinstallingBackends, setReinstallingBackends] = useState(new Set())
  const [upgrades, setUpgrades] = useState({})
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [distributedMode, setDistributedMode] = useState(false)
  const [togglingModels, setTogglingModels] = useState(new Set())
  const [pinningModels, setPinningModels] = useState(new Set())
  const [loadingModels, setLoadingModels] = useState(new Set())
  // Which entity the pane is showing, or null for the status page. The tab
  // already disambiguates models from backends, so the id alone is enough.
  // In the URL for the same reasons as the two galleries: a model is linkable
  // and Back leaves the detail rather than the page.
  const selectedId = searchParams.get('sel')
  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())
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
    selectEntity(null)
    localStorage.setItem('manage-tab', tab)
    setSearchParams({ tab })
  }

  // Switch tabs and pre-set a filter — wired into the StatCards so cards
  // double as shortcuts to a filtered slice instead of being purely visual.
  const handleSummaryClick = (tab, filter) => {
    setActiveTab(tab)
    selectEntity(null)
    localStorage.setItem('manage-tab', tab)
    if (tab === 'models') setModelsFilter(filter)
    if (tab === 'backends') setBackendsFilter(filter)
    const p = new URLSearchParams(searchParams)
    p.set('tab', tab)
    setSearchParams(p, { replace: true })
  }

  const selectEntity = useCallback((id) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (id) next.set('sel', id)
      else next.delete('sel')
      return next
    }, { replace: !id })
  }, [setSearchParams])

  const toggleGroup = useCallback((id) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

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
      backendsLoadedOnce.current = true
      setBackendsLoading(false)
    }
  }, [])

  const fetchAliases = useCallback(async () => {
    try {
      const data = await modelsApi.listAliases()
      const map = {}
      for (const a of Array.isArray(data) ? data : []) map[a.name] = a.target
      setAliasTargets(map)
    } catch {
      setAliasTargets({})
    }
  }, [])

  useEffect(() => {
    fetchLoadedModels()
    fetchBackends()
    fetchAliases()
    // Detect distributed mode (nodes API returns 503 when not enabled)
    nodesApi.list().then(() => setDistributedMode(true)).catch(() => {})
  }, [fetchLoadedModels, fetchBackends, fetchAliases])

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

  // Refresh installed backends + available upgrades when the Backends tab opens
  // AND whenever a backend operation settles (operations.length changes as a
  // reinstall/upgrade completes and drops off the list). Without the op-settle
  // refresh the installed-version cell and the "update available" badge stay
  // stale after an upgrade until the user switches tabs - the op looks like it
  // "did nothing". Mirrors the operations.length watch Backends.jsx uses.
  useEffect(() => {
    if (activeTab !== 'backends') return
    fetchBackends()
    backendsApi.checkUpgrades()
      .then(data => setUpgrades(data || {}))
      .catch(() => {})
  }, [operations.length, activeTab, fetchBackends])

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

  // Pre-load a model (or all of a realtime pipeline's sub-models) into memory.
  // The /backend/load call blocks until loading finishes, so the menu item shows
  // a loading state while in flight and reports the outcome on completion.
  const handleLoadModel = async (modelName) => {
    setLoadingModels(prev => new Set(prev).add(modelName))
    try {
      await backendControlApi.load({ model: modelName })
      addToast(`Loaded ${modelName}`, 'success')
      setTimeout(fetchLoadedModels, 500)
    } catch (err) {
      addToast(`Failed to load: ${err.message}`, 'error')
    } finally {
      setLoadingModels(prev => {
        const next = new Set(prev)
        next.delete(modelName)
        return next
      })
    }
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
  useEffect(() => {
    if (!modelsLoading) modelsLoadedOnce.current = true
  }, [modelsLoading])

  const runningCount = models.filter(m =>
    !m.disabled && (loadedModelIds.has(m.id) || (Array.isArray(m.loaded_on) && m.loaded_on.length > 0))
  ).length
  const updatesCount = Object.keys(upgrades).length

  // A backend is mid-flight if an operation names it, or if a reinstall was
  // just fired from this page and the operation has not landed yet.
  const isBackendProcessing = useCallback((backend) => {
    const name = backend?.Name
    if (!name) return false
    if (reinstallingBackends.has(name)) return true
    return operations.some(op => op.name === name && !op.completed && !op.error)
  }, [reinstallingBackends, operations])

  const selectedModel = selectedId && activeTab === 'models'
    ? (models.find(m => m.id === selectedId) || null)
    : null
  const selectedBackend = selectedId && activeTab === 'backends'
    ? (backends.find(b => b.Name === selectedId) || null)
    : null

  return (
    <div className="page page--wide page--app">
      <div className="view-bar">
        <h1 className="view-bar__title">{t('manage.title')}</h1>
        <span className="view-bar__count">
          {modelsLoading ? '—' : models.length} models · {backendsLoading ? '—' : backends.length} backends
        </span>
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
      <div className="tabs mb-md">
        {TABS.map(t => {
          const upgradeCount = t.key === 'backends' ? Object.keys(upgrades).length : 0
          return (
            <button
              key={t.key}
              className={`tab ${activeTab === t.key ? 'tab-active' : ''}`}
              onClick={() => handleTabChange(t.key)}
            >
              <i className={`fas ${t.icon} icon-before`} />
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
              {/* A status line, not a control. It had picked up btn classes and
                  two copies of `fas`, so it rendered as a button you cannot
                  press next to a button that looked like text. */}
              {distributedMode && (
                <span
                  className="cell-muted text-xs nowrap"
                  title="Auto-refreshes every 10s in distributed mode so ghost models clear promptly"
                >
                  <i className={`fas ${reloading ? 'fa-spinner fa-spin' : 'fa-rotate'} icon-before`} aria-hidden="true" />
                  Last synced {lastSyncedAgo}
                </span>
              )}
              <button className="btn btn-secondary btn-sm" onClick={handleReload} disabled={reloading}>
                <i className={`fas ${reloading ? 'fa-spinner fa-spin' : 'fa-rotate'}`} aria-hidden="true" />
                {reloading ? 'Updating…' : 'Update'}
              </button>
            </>
          )}
        />

        {modelsLoading && !modelsLoadedOnce.current ? (
          <GalleryLoader />
        ) : models.length === 0 ? (
          <div className="empty-state empty-state--page">
            <div className="empty-state-icon"><i className="fas fa-brain" /></div>
            <h2 className="empty-state-title">No models installed yet</h2>
            <p className="empty-state-text">
              Install a model from the gallery to get started, or import one you already have on disk.
            </p>
            <div className="empty-state__actions">
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
          <SplitView
            testId="host"
            detail={!!selectedModel}
            rail={
              <EntityRail
                items={visibleModels.map(m => railItemForManagedModel(m, { loadedModelIds, enrichModel, loadingModels }))}
                groups={MODEL_STATE_GROUPS}
                grouped={!modelsSearch.trim()}
                collapsedGroups={collapsedGroups}
                onToggleGroup={toggleGroup}
                busy={modelsLoading}
                selectedId={selectedId}
                onSelect={selectEntity}
                countLabel={`${visibleModels.length} of ${models.length}`}
                ariaLabel="Installed models"
                testId="host-rail"
              />
            }
            pane={selectedModel ? (() => {
              const enriched = enrichModel(selectedModel.id)
              const caps = Array.isArray(selectedModel.capabilities) ? selectedModel.capabilities : []
              const matchedCaps = USE_CASES.filter(uc => caps.includes(uc.cap) && !(uc.hideIf && caps.includes(uc.hideIf)))
              const isRunning = loadedModelIds.has(selectedModel.id) || (Array.isArray(selectedModel.loaded_on) && selectedModel.loaded_on.length > 0)
              return (
                <div className="detail-pane">
                  <DetailHeader
                    testId="host"
                    icon="fa-brain"
                    name={selectedModel.id}
                    lede={enriched?.description ? stripMarkdown(enriched.description).slice(0, 220) : null}
                    ledeTitle={enriched?.description ? stripMarkdown(enriched.description) : null}
                    onBack={() => selectEntity(null)}
                    backLabel="All models"
                    actions={
                      <>
                        {!selectedModel.disabled && !isRunning && (
                          <button className="btn btn-primary btn-sm" onClick={() => handleLoadModel(selectedModel.id)} disabled={loadingModels.has(selectedModel.id)}>
                            <i className="fas fa-bolt" /> {loadingModels.has(selectedModel.id) ? 'Loading…' : 'Load'}
                          </button>
                        )}
                        {isRunning && (
                          <button className="btn btn-secondary btn-sm" onClick={() => handleStopModel(selectedModel.id)}>
                            <i className="fas fa-stop" /> Stop
                          </button>
                        )}
                        {/* The rest stays behind a menu. Load/Stop is what an
                            operator came for; everything else is occasional and
                            would only dilute it. */}
                        <ActionMenu
                          ariaLabel={`Actions for ${selectedModel.id}`}
                          triggerLabel={`Actions for ${selectedModel.id}`}
                          items={[
                            { key: 'toggle', icon: selectedModel.disabled ? 'fa-toggle-on' : 'fa-toggle-off',
                              label: selectedModel.disabled ? 'Enable model' : 'Disable model',
                              onClick: () => handleToggleModel(selectedModel.id, selectedModel.disabled),
                              disabled: togglingModels.has(selectedModel.id) },
                            { key: 'pin', icon: 'fa-thumbtack',
                              label: selectedModel.pinned ? 'Unpin (allow idle unload)' : 'Pin (prevent idle unload)',
                              onClick: () => handleTogglePinned(selectedModel.id, selectedModel.pinned),
                              disabled: pinningModels.has(selectedModel.id) || !!selectedModel.disabled },
                            { key: 'edit', icon: 'fa-pen-to-square', label: 'Edit configuration',
                              onClick: () => navigate(`/app/model-editor/${encodeURIComponent(selectedModel.id)}`, { state: fromState(location, t('manage.title')) }) },
                            { key: 'logs', icon: 'fa-terminal', label: 'Backend logs',
                              onClick: () => navigate(`/app/backend-logs/${encodeURIComponent(selectedModel.id)}`) },
                            { divider: true },
                            { key: 'delete', icon: 'fa-trash', label: 'Delete model', danger: true,
                              onClick: () => handleDeleteModel(selectedModel.id) },
                          ]}
                        />
                      </>
                    }
                  />

                  <StatGrid
                    stats={[
                      { label: 'State',
                        value: selectedModel.disabled ? 'Disabled' : isRunning ? 'Running' : 'Idle',
                        tone: selectedModel.disabled ? undefined : isRunning ? 'ok' : undefined },
                      { label: 'Backend', value: selectedModel.backend || 'Auto' },
                      enriched?.estimated_vram_display && enriched.estimated_vram_display !== '0 B'
                        ? { label: 'VRAM', value: enriched.estimated_vram_display } : null,
                      selectedModel.pinned ? { label: 'Pinned', value: 'yes', tone: 'warn' } : null,
                    ]}
                  />

                  {/* Adopted, pinned and alias are row badges that lost their
                      cell. They are facts about the model, not about its state,
                      so they sit under the numbers rather than in the rail. */}
                  {(aliasTargets[selectedModel.id] || selectedModel.source === 'registry-only') && (
                    <div className="badge-row">
                      {selectedModel.source === 'registry-only' && (
                        <span className="badge badge-warning" title="Discovered on a worker but not configured locally. Persist the config to make it permanent.">
                          <i className="fas fa-ghost" /> Adopted
                        </span>
                      )}
                      {aliasTargets[selectedModel.id] && (
                        <span className="badge badge-info" title={`Alias -> ${aliasTargets[selectedModel.id]}`}>
                          <i className="fas fa-arrow-right-arrow-left" /> alias -&gt; {aliasTargets[selectedModel.id]}
                        </span>
                      )}
                    </div>
                  )}

                  {matchedCaps.length > 0 && (
                    <div>
                      <span className="detail-pane__label">Use cases</span>
                      <div className="badge-row">
                        {matchedCaps.map(uc => uc.route ? (
                          <a
                            key={uc.cap}
                            href="#"
                            onClick={(e) => { e.preventDefault(); navigate(uc.route(selectedModel.id)) }}
                            className="badge badge-info badge-link"
                          >{uc.label}</a>
                        ) : (
                          <span key={uc.cap} className="badge">{uc.label}</span>
                        ))}
                      </div>
                    </div>
                  )}

                  <ModelDetail
                    model={selectedModel}
                    enriched={enriched}
                    matchedCaps={matchedCaps}
                    distributedMode={distributedMode}
                    onNavigate={navigate}
                  />
                </div>
              )
            })() : (
              <HostStatusPane
                models={models}
                backends={backends}
                loadedModelIds={loadedModelIds}
                upgrades={upgrades}
                operations={operations}
                enrichModel={enrichModel}
                onJump={handleSummaryClick}
              />
            )}
          />

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

        {backendsLoading && !backendsLoadedOnce.current ? (
          <GalleryLoader />
        ) : backends.length === 0 ? (
          <div className="empty-state empty-state--page">
            <div className="empty-state-icon"><i className="fas fa-server" /></div>
            <h2 className="empty-state-title">No backends installed yet</h2>
            <p className="empty-state-text">
              A backend is the runtime that actually runs a model. Install one from the gallery to give this host something to run with.
            </p>
            <div className="empty-state__actions">
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
            <SplitView
              testId="host"
              detail={!!selectedBackend}
              rail={
                <EntityRail
                  items={visibleBackends.map(b => railItemForManagedBackend(b, { upgrades, isBackendProcessing }))}
                  groups={BACKEND_STATE_GROUPS}
                  grouped={!backendsSearch.trim()}
                  collapsedGroups={collapsedGroups}
                  onToggleGroup={toggleGroup}
                  busy={backendsLoading}
                  selectedId={selectedId}
                  onSelect={selectEntity}
                  countLabel={`${visibleBackends.length} of ${backends.length}`}
                  ariaLabel="Installed backends"
                  testId="host-rail"
                />
              }
              pane={selectedBackend ? (
                <ManagedBackendPane
                  backend={selectedBackend}
                  enriched={enrichBackend(selectedBackend.Name)}
                  upgradeInfo={upgrades[selectedBackend.Name]}
                  processing={isBackendProcessing(selectedBackend)}
                  distributedMode={distributedMode}
                  onBack={() => selectEntity(null)}
                  onUpgrade={handleUpgradeBackend}
                  onReinstall={handleReinstallBackend}
                  onDelete={handleDeleteBackend}
                />
              ) : (
                <HostStatusPane
                  models={models}
                  backends={backends}
                  loadedModelIds={loadedModelIds}
                  upgrades={upgrades}
                  operations={operations}
                  enrichModel={enrichModel}
                  onJump={handleSummaryClick}
                />
              )}
            />

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
              className="resource-row__detail-md markdown-body"
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
                  className="badge badge-info badge-link"
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
            <div className="stack stack--xs">
              {urls.map((url, i) => (
                <a key={i} href={safeHref(url)} target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)', wordBreak: 'break-all', fontSize: 'var(--text-xs)' }}>
                  <i className="fas fa-external-link-alt icon-before text-xs" />{url}
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
              className="resource-row__detail-md markdown-body"
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
            <div className="stack stack--xs">
              {urls.map((url, i) => (
                <a key={i} href={safeHref(url)} target="_blank" rel="noopener noreferrer"
                  style={{ color: 'var(--color-primary)', wordBreak: 'break-all', fontSize: 'var(--text-xs)' }}>
                  <i className="fas fa-external-link-alt icon-before text-xs" />{url}
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

// State groups. An inventory is read by condition before it is read by name, so
// the rail is bucketed by what a thing is doing rather than by what it is for.
// That is the opposite of the galleries, and deliberately so: nobody opens Host
// wondering which of their models does vision.
const MODEL_STATE_GROUPS = [
  { id: 'running', label: 'Running', icon: 'fa-circle-play' },
  { id: 'idle', label: 'Idle', icon: 'fa-pause' },
  { id: 'disabled', label: 'Disabled', icon: 'fa-ban' },
]

const BACKEND_STATE_GROUPS = [
  { id: 'update', label: 'Update available', icon: 'fa-arrow-up' },
  { id: 'installed', label: 'Installed', icon: 'fa-check' },
]

function railItemForManagedModel(model, { loadedModelIds, enrichModel, loadingModels }) {
  const running = loadedModelIds.has(model.id) || (Array.isArray(model.loaded_on) && model.loaded_on.length > 0)
  const enriched = enrichModel(model.id)
  const vram = enriched?.estimated_vram_display
  const hasVram = vram && vram !== '0 B'

  let groupId = 'idle'
  let stripe = 'idle'
  let meta = hasVram ? `idle · ${vram}` : 'idle'
  let metaTone

  if (model.disabled) {
    groupId = 'disabled'
    stripe = 'off'
    meta = 'disabled'
  } else if (loadingModels.has(model.id)) {
    groupId = 'idle'
    stripe = 'idle'
    meta = 'loading…'
    metaTone = 'busy'
  } else if (running) {
    groupId = 'running'
    stripe = 'run'
    meta = hasVram ? `running · ${vram}` : 'running'
    metaTone = 'ok'
  }

  return { id: model.id, name: model.id, icon: 'fa-brain', meta, metaTone, stripe, groupId }
}

function railItemForManagedBackend(backend, { upgrades, isBackendProcessing }) {
  const name = backend.Name
  const upgrade = upgrades[name]
  const version = backend.Metadata?.version || backend.Version

  let groupId = 'installed'
  let stripe = 'idle'
  let meta = version ? `v${version}` : 'installed'
  let metaTone

  if (isBackendProcessing(backend)) {
    meta = 'working…'
    metaTone = 'busy'
  } else if (upgrade) {
    groupId = 'update'
    stripe = 'err'
    meta = upgrade.available_version ? `v${version} → v${upgrade.available_version}` : 'update available'
    metaTone = 'warn'
  }

  return { id: name, name, icon: 'fa-server', meta, metaTone, stripe, groupId }
}

// ManagedBackendPane is the detail for one installed backend. System backends
// keep their protection: they are managed outside the gallery, so the pane
// states that rather than offering actions that would fail.
function ManagedBackendPane({ backend, enriched, upgradeInfo, processing, distributedMode, onBack, onUpgrade, onReinstall, onDelete }) {
  const name = backend.Name
  const version = backend.Metadata?.version || backend.Version
  return (
    <div className="detail-pane">
      <DetailHeader
        testId="host"
        icon="fa-server"
        name={name}
        lede={enriched?.description ? stripMarkdown(enriched.description).slice(0, 220) : null}
        ledeTitle={enriched?.description ? stripMarkdown(enriched.description) : null}
        onBack={onBack}
        backLabel="All backends"
        actions={
          backend.IsSystem ? (
            <span className="badge" title="System backends are managed outside the gallery">
              <i className="fas fa-lock" /> Protected
            </span>
          ) : (
            <>
              {upgradeInfo && (
                <button className="btn btn-primary btn-sm" onClick={() => onUpgrade(name)} disabled={processing}>
                  <i className="fas fa-arrow-up" /> {upgradeInfo.available_version ? `Upgrade to v${upgradeInfo.available_version}` : 'Upgrade'}
                </button>
              )}
              <ActionMenu
                ariaLabel={`Actions for ${name}`}
                triggerLabel={`Actions for ${name}`}
                items={[
                  { key: 'reinstall', icon: 'fa-rotate', label: 'Reinstall backend',
                    onClick: () => onReinstall(name), disabled: processing },
                  { divider: true },
                  { key: 'delete', icon: 'fa-trash', label: 'Delete backend', danger: true,
                    onClick: () => onDelete(name) },
                ]}
              />
            </>
          )
        }
      />

      <StatGrid
        stats={[
          { label: 'Version', value: version ? `v${version}` : '—' },
          upgradeInfo ? { label: 'Available', value: upgradeInfo.available_version ? `v${upgradeInfo.available_version}` : 'update', tone: 'warn' } : null,
          { label: 'Managed', value: backend.IsSystem ? 'system' : 'gallery' },
        ]}
      />

      <BackendDetail
        backend={backend}
        enriched={enriched}
        upgradeInfo={upgradeInfo}
        nodes={backend.nodes}
        distributedMode={distributedMode}
      />
    </div>
  )
}

// HostStatusPane is the pane with nothing selected.
//
// Nobody opens Host to discover anything, so the zero state is not a catalog
// front page: it is the answer to the question people actually arrive with.
// What is loaded, what is stale, and what fell over. Every number here already
// existed on the page; none of them had been assembled into one statement.
function HostStatusPane({ models, backends, loadedModelIds, upgrades, operations, enrichModel, onJump }) {
  const running = models.filter(m => !m.disabled && (loadedModelIds.has(m.id) || (Array.isArray(m.loaded_on) && m.loaded_on.length > 0)))
  const disabled = models.filter(m => m.disabled)
  const idle = models.length - running.length - disabled.length
  const staleNames = Object.keys(upgrades)
  // Failures are kept in the operations list on purpose so they can be seen and
  // dismissed; unseen is exactly what a red badge in a scrolled-off row was.
  const failures = operations.filter(op => op.error)

  return (
    <div className="zero-pane">
      <div className="zero-pane__hero">
        <span className="zero-pane__eyebrow">Right now</span>
        <h2 className="zero-pane__title">
          {running.length === 0
            ? `Nothing loaded. ${models.length} models and ${backends.length} backends installed.`
            : `${running.length} of ${models.length} models loaded, ${backends.length} backends installed.`}
        </h2>
        <p className="zero-pane__text">Pick anything on the left to load it, stop it, or see its configuration.</p>
      </div>

      {failures.length > 0 && (
        <div className="zero-pane__alert zero-pane__alert--bad" role="status">
          <i className="fas fa-circle-exclamation" aria-hidden="true" />
          <span>
            {failures.length === 1 ? '1 operation failed' : `${failures.length} operations failed`}
            {': '}{failures.slice(0, 2).map(op => op.name).join(', ')}{failures.length > 2 ? '…' : ''}
          </span>
        </div>
      )}

      {staleNames.length > 0 && (
        <div className="zero-pane__alert zero-pane__alert--warn">
          <i className="fas fa-arrow-up" aria-hidden="true" />
          <span>
            {staleNames.length === 1 ? '1 backend has an update' : `${staleNames.length} backends have updates`}
            {': '}{staleNames.slice(0, 3).join(', ')}{staleNames.length > 3 ? '…' : ''}
          </span>
          <button className="btn btn-secondary btn-sm" onClick={() => onJump('backends', 'updates')}>
            Review
          </button>
        </div>
      )}

      <StatGrid
        stats={[
          { label: 'Loaded', value: running.length, tone: running.length > 0 ? 'ok' : undefined },
          { label: 'Idle', value: idle },
          { label: 'Disabled', value: disabled.length },
          { label: 'Updates', value: staleNames.length, tone: staleNames.length > 0 ? 'warn' : undefined },
        ]}
      />

      {running.length > 0 && (
        <div className="zero-pane__shelf">
          <div className="zero-pane__shelf-head">
            <h3 className="zero-pane__shelf-title">Loaded now</h3>
            <span className="zero-pane__shelf-meta">estimated VRAM</span>
          </div>
          <div className="rowlist">
            {running.slice(0, 6).map(m => {
              const vram = enrichModel(m.id)?.estimated_vram_display
              return (
                <div className="rowline" key={m.id}>
                  <span className="badge badge-success"><i className="fas fa-circle icon-tiny" /> running</span>
                  <span>{m.id}</span>
                  <span className="cell-mono cell-muted rowline__num">{vram && vram !== '0 B' ? vram : '—'}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
