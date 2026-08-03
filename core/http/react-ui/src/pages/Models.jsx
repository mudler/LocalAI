import { useState, useCallback, useEffect, useRef } from 'react'
import { useNavigate, useOutletContext, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import { modelsApi } from '../utils/api'
import { safeHref } from '../utils/url'
import { useDebouncedCallback } from '../hooks/useDebounce'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import SearchableSelect from '../components/SearchableSelect'
import PageHeader from '../components/PageHeader'
import ConfirmDialog from '../components/ConfirmDialog'
import GalleryLoader from '../components/GalleryLoader'
import Toggle from '../components/Toggle'
import RecommendedModels from '../components/RecommendedModels'
import SplitView from '../components/split/SplitView'
import EntityRail from '../components/split/EntityRail'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { formatBytes } from '../utils/format'
import { ENTITY_GROUPS, groupForEntity } from '../utils/entityGroups'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import React from 'react'


// The rail groups what it has, so it needs enough rows for the groups to mean
// something. At nine a page rarely held more than one bucket, so turning one
// rebuilt the rail's whole structure; at thirty the sections are stable enough
// to read as structure rather than noise, and there are five times fewer pages.
const RAIL_PAGE_SIZE = 30

// How many estimates to have in flight at once. See the fetch effect: this
// exists to leave connections free for whatever the user clicks next.
const ESTIMATE_CONCURRENCY = 4

const CHART_HEIGHT = 96
const CHART_LABEL_ROOM = 15

const CONTEXT_SIZES = [8192, 16384, 32768, 65536, 131072, 262144]
const CONTEXT_LABELS = ['8K', '16K', '32K', '64K', '128K', '256K']
const FITS_FILTER_STORAGE_KEY = 'localai-models-fits-filter'
const COLLAPSE_VARIANTS_STORAGE_KEY = 'localai-models-collapse-variants-filter'
// The deduplicated gallery is what a user asking "what can I install" wants, so
// that is the default. The control exists for the other job: browsing every
// build the gallery holds, which the collapsed view makes impossible however
// many pages you turn.
const COLLAPSE_VARIANTS_DEFAULT = true

// How many listing rows to ask for when resolving one variant's gallery entry
// by exact name. The term is the full name, so the entry is always in the
// match set; the page size only has to be wide enough that the fuzzy matches
// sharing that name's prefix cannot push it past the first page.
const VARIANT_DETAIL_SEARCH_ITEMS = 100

// Only 'on'/'off' counts as a choice. An earlier build wrote '1'/'0' from an
// effect that ran on mount, so those values record that the page was opened
// rather than that anyone picked a view, and honouring them would pin a
// visitor to a default they never chose.
const readCollapseVariantsPreference = () => {
  try {
    const stored = localStorage.getItem(COLLAPSE_VARIANTS_STORAGE_KEY)
    if (stored === 'on') return true
    if (stored === 'off') return false
    return COLLAPSE_VARIANTS_DEFAULT
  } catch {
    return COLLAPSE_VARIANTS_DEFAULT
  }
}

const FILTERS = [
  { key: '', labelKey: 'filters.all', icon: 'fa-layer-group' },
  { key: 'chat', labelKey: 'filters.llm', icon: 'fa-brain' },
  { key: 'image', labelKey: 'filters.image', icon: 'fa-image' },
  { key: 'video', labelKey: 'filters.video', icon: 'fa-video' },
  { key: '3d', labelKey: 'filters.threed', icon: 'fa-cube' },
  { key: 'multimodal', labelKey: 'filters.multimodal', icon: 'fa-shapes' },
  { key: 'vision', labelKey: 'filters.vision', icon: 'fa-eye' },
  { key: 'tts', labelKey: 'filters.tts', icon: 'fa-microphone' },
  { key: 'transcript', labelKey: 'filters.stt', icon: 'fa-headphones' },
  { key: 'diarization', labelKey: 'filters.diarization', icon: 'fa-users' },
  { key: 'sound_classification', labelKey: 'filters.soundClassification', icon: 'fa-ear-listen' },
  { key: 'sound_generation', labelKey: 'filters.soundGen', icon: 'fa-music' },
  { key: 'audio_transform', labelKey: 'filters.audioTransform', icon: 'fa-sliders' },
  { key: 'realtime_audio', labelKey: 'filters.realtimeAudio', icon: 'fa-tower-broadcast' },
  { key: 'embeddings', labelKey: 'filters.embedding', icon: 'fa-vector-square' },
  { key: 'rerank', labelKey: 'filters.rerank', icon: 'fa-sort' },
  { key: 'detection', labelKey: 'filters.detection', icon: 'fa-bullseye' },
  { key: 'vad', labelKey: 'filters.vad', icon: 'fa-wave-square' },
  { key: 'token_classify', labelKey: 'filters.ner', icon: 'fa-tags' },
]

// The chips grouped, using the families the rest of the UI already speaks. The
// unlabelled first section holds "All" on its own, because it is a reset rather
// than a use case and grouping it under a heading would imply otherwise.
const FILTER_SECTIONS = [
  { id: 'all', labelKey: null, keys: [''] },
  { id: 'text', labelKey: 'groups.text', icon: 'fa-brain', pick: 'chat',
    blurbKey: 'shelves.pickText',
    keys: ['chat', 'embeddings', 'rerank', 'token_classify'] },
  { id: 'vision', labelKey: 'groups.vision', icon: 'fa-eye', pick: 'vision',
    blurbKey: 'shelves.pickVision',
    keys: ['vision', 'multimodal', 'detection'] },
  { id: 'audio', labelKey: 'groups.audio', icon: 'fa-wave-square', pick: 'tts',
    blurbKey: 'shelves.pickAudio',
    keys: ['tts', 'transcript', 'diarization', 'sound_classification',
      'sound_generation', 'audio_transform', 'realtime_audio', 'vad'] },
  { id: 'visual', labelKey: 'groups.visual', icon: 'fa-image', pick: 'image',
    blurbKey: 'shelves.pickVisual',
    keys: ['image', 'video', '3d'] },
]

export default function Models() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('models')
  const { operations } = useOperations()
  const { resources } = useResources()
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const [totalPages, setTotalPages] = useState(1)
  const [search, setSearch] = useState('')
  const [filters, setFilters] = useState([])
  const [sort, setSort] = useState('')
  const [order, setOrder] = useState('asc')
  const [installing, setInstalling] = useState(new Map())
  const [expandedFiles, setExpandedFiles] = useState(false)
  // Which model the pane is showing, or null for the discovery shelves. It
  // lives in the URL so a model is linkable and so Back steps out of the detail
  // rather than off the page, which is the one thing the expanded row could
  // never do.
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedName = searchParams.get('model')
  const [stats, setStats] = useState({ total: 0, installed: 0, repositories: 0 })
  // Distinguishes "nothing installed" from "not asked yet". The recommendations
  // panel defaults off the installed count, so it must not read the initial 0.
  const [statsLoaded, setStatsLoaded] = useState(false)
  const [backendFilter, setBackendFilter] = useState('')
  const [allBackends, setAllBackends] = useState([])
  const [backendUsecases, setBackendUsecases] = useState({})
  const [estimates, setEstimates] = useState({})
  // Models whose estimate is in flight, so a row can say it is still working
  // rather than silently showing nothing where a size will appear.
  const [pendingEstimates, setPendingEstimates] = useState(() => new Set())
  const [contextSize, setContextSize] = useState(CONTEXT_SIZES[0])
  // True once any listing has come back. Distinguishes a cold start, which has
  // nothing to keep on screen, from a refetch, which does.
  const loadedOnce = useRef(false)
  const [confirmDialog, setConfirmDialog] = useState(null)
  // Variant descriptions, keyed by model name. The listing only tells us
  // whether an entry declares any; describing them costs the server a network
  // probe per variant, so we ask for one entry at a time and keep the answer
  // for the rest of the page session.
  const [variantData, setVariantData] = useState({})
  // Gallery entries behind individual variants, keyed by variant name. The
  // variant description carries only what ranking needs, and a variant the
  // collapse hides has no listing row of its own, so this is the only place
  // its description, licence, tags, links and files become reachable.
  const [variantDetails, setVariantDetails] = useState({})
  const [fitsFilter, setFitsFilter] = useState(() => {
    try {
      return localStorage.getItem(FITS_FILTER_STORAGE_KEY) === '1'
    } catch {
      return false
    }
  })
  // Collapses the listing to one row per model by hiding the individual builds
  // another entry already offers as variants. Server-side, unlike fitsFilter,
  // because the listing paginates and a client-side narrowing would leave the
  // page count describing the unfiltered set.
  const [collapseVariants, setCollapseVariants] = useState(readCollapseVariantsPreference)
  // The use-case chips do not fit beside a 320px rail, so they live in a
  // popover that states the current selection rather than spelling out
  // nineteen options nobody is reading.
  const [useCaseOpen, setUseCaseOpen] = useState(false)
  // Rail groups the user has folded away.
  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())
  // Total GPU memory for "fits" check
  const totalGpuMemory = resources?.aggregate?.total_memory || 0
  // gpu_count is 0 and gpus is null on a CPU-only host, where total_memory is
  // system RAM. The fits check has always used it either way; only the copy
  // has to stop calling it VRAM.
  const hasGpu = (resources?.aggregate?.gpu_count || 0) > 0 || (resources?.gpus?.length || 0) > 0

  const fetchModels = useCallback(async (params = {}) => {
    try {
      setLoading(true)
      const searchVal = params.search !== undefined ? params.search : search
      const filtersVal = params.filters !== undefined ? params.filters : filters
      const sortVal = params.sort !== undefined ? params.sort : sort
      const backendVal = params.backendFilter !== undefined ? params.backendFilter : backendFilter
      const collapseVal = params.collapseVariants !== undefined ? params.collapseVariants : collapseVariants
      const queryParams = {
        page: params.page || page,
        items: RAIL_PAGE_SIZE,
      }
      // Omitted entirely when off rather than sent as false, so opting out asks
      // for exactly the listing every other API client gets.
      //
      // Sent alongside the term rather than instead of it. The handler matches
      // the term against every build the gallery holds either way; the collapse
      // only decides how a match is reported, and grouped, a match on a build
      // another entry offers comes back as that entry. So a search never dead
      // ends, and what "collapsed" means stays decided in one place.
      if (collapseVal) queryParams.collapse_variants = 'true'
      if (filtersVal.length > 0) queryParams.tag = filtersVal.join(',')
      if (searchVal) queryParams.term = searchVal
      if (backendVal) queryParams.backend = backendVal
      if (sortVal) {
        queryParams.sort = sortVal
        queryParams.order = params.order || order
      }
      const data = await modelsApi.list(queryParams)
      setModels(data?.models || [])
      setTotalPages(data?.totalPages || data?.total_pages || 1)
      setStats({
        total: data?.availableModels || 0,
        installed: data?.installedModels || 0,
      })
      setStatsLoaded(true)
      setAllBackends(data?.allBackends || [])
    } catch (err) {
      addToast(t('errors.loadFailed', { message: err.message }), 'error')
    } finally {
      loadedOnce.current = true
      setLoading(false)
    }
  }, [page, search, filters, sort, order, backendFilter, collapseVariants, addToast, t])

  useEffect(() => {
    fetchModels()
  }, [page, filters, sort, order, backendFilter, collapseVariants])

  // Fetch backend→usecase mapping once on mount
  useEffect(() => {
    modelsApi.backendUsecases().then(setBackendUsecases).catch(() => {})
  }, [])

  // When backend changes, remove selected filters that aren't available
  useEffect(() => {
    if (backendFilter && backendUsecases[backendFilter]) {
      setFilters(prev => {
        const possible = backendUsecases[backendFilter]
        const filtered = prev.filter(k => k === 'multimodal' || possible.includes(k))
        return filtered.length !== prev.length ? filtered : prev
      })
    }
  }, [backendFilter, backendUsecases])

  // Re-fetch when operations change (install/delete completion)
  useEffect(() => {
    if (!loading) fetchModels()
  }, [operations.length])

  const debouncedFetch = useDebouncedCallback((value) => {
    setPage(1)
    fetchModels({ search: value, page: 1 })
  })

  // Fetch VRAM/size estimates for the loaded page, a few at a time.
  //
  // A browser allows around six connections per host, and an estimate against
  // a cold server cache takes seconds. Firing one per row took every slot, so
  // the request behind a click - the variant list, an install - waited behind a
  // queue of work the user never asked for, and the page felt frozen while the
  // list was in fact already usable. Four leaves room for the interactive
  // request to overtake.
  useEffect(() => {
    if (models.length === 0) return
    const queue = models
      .map(m => m.name || m.id)
      .filter(id => !estimates[id])
    if (queue.length === 0) return

    let cancelled = false
    setPendingEstimates(prev => {
      const next = new Set(prev)
      queue.forEach(id => next.add(id))
      return next
    })

    const settle = (id) => setPendingEstimates(prev => {
      if (!prev.has(id)) return prev
      const next = new Set(prev)
      next.delete(id)
      return next
    })

    let cursor = 0
    const worker = async () => {
      while (!cancelled && cursor < queue.length) {
        const id = queue[cursor++]
        try {
          const est = await modelsApi.estimate(id, CONTEXT_SIZES)
          if (!cancelled && est && (est.sizeBytes || est.estimates)) {
            setEstimates(prev => ({ ...prev, [id]: est }))
          }
        } catch {
          // An estimate is a nicety. The row names the model and installs it
          // either way, so a failure must not stop the queue behind it.
        }
        if (!cancelled) settle(id)
      }
    }
    Promise.all(Array.from({ length: Math.min(ESTIMATE_CONCURRENCY, queue.length) }, worker))

    return () => {
      cancelled = true
      setPendingEstimates(prev => {
        if (prev.size === 0) return prev
        const next = new Set(prev)
        queue.forEach(id => next.delete(id))
        return next
      })
    }
  }, [models])

  const handleSearch = (value) => {
    setSearch(value)
    debouncedFetch(value)
  }

  const toggleFilter = (key) => {
    if (key === '') { setFilters([]); setPage(1); return }
    setFilters(prev =>
      prev.includes(key) ? prev.filter(k => k !== key) : [...prev, key]
    )
    setPage(1)
  }

  const isFilterAvailable = (key) => {
    if (!backendFilter || key === '' || key === 'multimodal') return true
    const possible = backendUsecases[backendFilter]
    return !possible || possible.includes(key)
  }

  const handleSort = (col) => {
    if (sort === col) {
      setOrder(o => o === 'asc' ? 'desc' : 'asc')
    } else {
      setSort(col)
      setOrder('asc')
    }
  }

  // Fetches an entry's variant description once. Called from the two points
  // where a user actually asks to see variants: opening the split-button menu
  // and expanding the detail row. An entry that declares none never gets here,
  // so it issues no request at all.
  const loadVariants = useCallback((id) => {
    if (!id) return
    setVariantData(prev => {
      if (prev[id]) return prev
      modelsApi.variants(id)
        .then(data => setVariantData(p => ({ ...p, [id]: { loading: false, ...data } })))
        .catch(() => setVariantData(p => ({ ...p, [id]: { loading: false, variants: [] } })))
      return { ...prev, [id]: { loading: true, variants: [] } }
    })
  }, [])

  // Resolves one variant's full gallery entry, once, and only when the user
  // asks to see it.
  //
  // The listing already returns every field the detail view renders, so this
  // keeps the fields off both the listing and DescribeVariants: an expand costs
  // nothing, and a variant nobody opens costs nothing.
  //
  // The query deliberately omits collapse_variants, which is what makes it
  // reach the build itself. Grouped, the same term would answer with the entry
  // that offers this build, and the panel is being asked about the build.
  //
  // A name the listing does not return is a real outcome, not a bug to hide:
  // the gallery can be reloaded between describing the variants and asking
  // about one of them. It is recorded as an error so the panel can say so.
  const loadVariantDetail = useCallback((variantName) => {
    if (!variantName) return
    setVariantDetails(prev => {
      if (prev[variantName]) return prev
      modelsApi.list({ term: variantName, items: VARIANT_DETAIL_SEARCH_ITEMS })
        .then(data => {
          const entry = (data?.models || []).find(m => (m.name || m.id) === variantName)
          setVariantDetails(p => ({ ...p, [variantName]: entry ? { entry } : { error: true } }))
        })
        .catch(() => setVariantDetails(p => ({ ...p, [variantName]: { error: true } })))
      return { ...prev, [variantName]: { loading: true } }
    })
  }, [])

  const handleInstall = async (modelId, variant) => {
    try {
      setInstalling(prev => new Map(prev).set(modelId, Date.now()))
      await modelsApi.install(modelId, variant)
    } catch (err) {
      addToast(t('errors.installFailed', { message: err.message }), 'error')
    }
  }

  const handleDelete = (modelId) => {
    setConfirmDialog({
      title: t('deleteDialog.title'),
      message: t('deleteDialog.message', { model: modelId }),
      confirmLabel: t('deleteDialog.confirm', { model: modelId }),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await modelsApi.delete(modelId)
          addToast(t('deleteDialog.deletingToast', { model: modelId }), 'info')
          fetchModels()
        } catch (err) {
          addToast(t('errors.deleteFailed', { message: err.message }), 'error')
        }
      },
    })
    return
  }

  // Clear local installing flags when operations finish (success or error)
  useEffect(() => {
    if (installing.size === 0) return
    setInstalling(prev => {
      const next = new Map(prev)
      let changed = false
      for (const [modelId, timestamp] of prev) {
        const hasActiveOp = operations.some(op =>
          op.name === modelId && !op.completed && !op.error
        )
        const hasCompletedOp = operations.some(op =>
          op.name === modelId && (op.completed || op.error)
        )
        const elapsed = Date.now() - timestamp
        // Remove if operation completed, or if >5s passed with no operation ever appearing
        if (hasCompletedOp || (!hasActiveOp && elapsed > 5000)) {
          next.delete(modelId)
          changed = true
        }
      }
      return changed ? next : prev
    })
  }, [operations, installing.size])

  const isInstalling = (modelId) => {
    return installing.has(modelId) || operations.some(op =>
      op.name === modelId && !op.completed && !op.error
    )
  }

  const getOperationProgress = (modelId) => {
    const op = operations.find(o => o.name === modelId && !o.completed && !o.error)
    return op?.progress ?? 0
  }

  const fitsGpu = (vramBytes) => {
    if (!vramBytes || !totalGpuMemory) return null
    return vramBytes <= totalGpuMemory * 0.95
  }

  useEffect(() => {
    try {
      localStorage.setItem(FITS_FILTER_STORAGE_KEY, fitsFilter ? '1' : '0')
    } catch {
      // Ignore storage errors (e.g., private browsing restrictions).
    }
  }, [fitsFilter])

  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_VARIANTS_STORAGE_KEY, collapseVariants ? 'on' : 'off')
    } catch {
      // Ignore storage errors (e.g., private browsing restrictions).
    }
  }, [collapseVariants])

  const useCaseLabel = filters.length === 0
    ? t('filters.all')
    : filters.length === 1
      ? t(FILTERS.find(f => f.key === filters[0])?.labelKey || 'filters.all')
      : t('filters.someSelected', { count: filters.length })

  const visibleModels = models.filter((model) => {
    if (!fitsFilter) return true
    const name = model.name || model.id
    const vramBytes = estimates[name]?.estimates?.[String(contextSize)]?.vramBytes
    const fit = fitsGpu(vramBytes)
    // Keep models visible while estimate is still loading; hide only explicit non-fits.
    return fit !== false
  })

  const selectedModel = selectedName
    ? visibleModels.find(m => (m.name || m.id) === selectedName) || null
    : null

  const toggleGroup = useCallback((id) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const selectModel = useCallback((name) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (name) next.set('model', name)
      else next.delete('model')
      return next
    // Returning to the shelves replaces the entry rather than pushing one, so
    // Back leaves the gallery instead of bouncing between the two pane states.
    }, { replace: !name })
    setExpandedFiles(false)
  }, [setSearchParams])

  // The detail pane lists variants, so opening a model is the ask that pays for
  // the describe call. loadVariants is idempotent per name.
  useEffect(() => {
    if (selectedModel?.has_variants) loadVariants(selectedName)
  }, [selectedName, selectedModel, loadVariants])

  return (
    <div className="page page--wide page--app">
      {/* Title only. The two counts used to live here as well, which meant the
          screen stated "1,247 available" three times: once in this header, once
          as the rail's "9 of 1,247", and once in the pane's own headline. The
          rail and the pane are describing what you are looking at; the header
          was just repeating them from a distance. */}
      <div className="view-bar">
        <h1 className="view-bar__title">{t('title')}</h1>
        <span className="view-bar__count">{t('rail.showingCount', { shown: visibleModels.length, total: stats.total })}</span>
        <div className="view-bar__actions">
          <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/model-editor', { state: fromState(location, t('models')) })}>
            <i className="fas fa-plus" /> {t('actions.addModel')}
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
            <i className="fas fa-upload" /> {t('actions.importModel')}
          </button>
        </div>
      </div>

      {/* Filters, in three deliberate bands.
          1. Query scope: free-text search plus the backend select. The backend
             select leads the taxonomy row rather than trailing it because
             picking a backend disables the use-cases that backend cannot serve
             (see isFilterAvailable), so it reads as the gate on what follows.
          2. Taxonomy: the use-case chips, which wrap freely.
          3. Refinements: one row per model, fits-in-GPU and context size.
             All three narrow a listing the user is already reading rather than
             naming what to look at, which is what separates them from the
             query scope above. Fits-in-GPU and context size are additionally
             one control group - the context size is the length the VRAM
             estimate is computed at, and that estimate is exactly what the
             fits filter tests against.
          Each band owns its container, so how many chips happen to wrap at a
          given width can no longer decide where the other controls land. */}

      {/* The gallery, as a rail to scan and a pane that answers.
          The pane has two jobs and no third: with nothing selected it is the
          discovery page, and with a model selected it is that model's detail.
          This is what replaces the click-to-expand row, which existed only
          because variants, files and a VRAM estimate never fitted inside a
          <tr> in the first place. */}
      {loading && !loadedOnce.current ? (
        <GalleryLoader />
      ) : (
        <SplitView
          testId="discover"
          detail={!!selectedModel}
          rail={
            <>
              <div className="filter-bar-group models-filters">
                <div className="filter-bar-group__row models-filters__query">
                  <div className="search-bar filter-bar-group__search">
                    <i className="fas fa-search search-icon" aria-hidden="true" />
                    <input
                      className="input"
                      type="text"
                      placeholder={t('search.placeholder')}
                      aria-label={t('search.placeholder')}
                      value={search}
                      onChange={(e) => handleSearch(e.target.value)}
                    />
                  </div>
                  {allBackends.length > 0 && (
                    <div className="models-filters__backend">
                      <SearchableSelect
                        value={backendFilter}
                        onChange={(v) => { setBackendFilter(v); setPage(1) }}
                        options={allBackends}
                        placeholder={t('filters.allBackends')}
                        allOption={t('filters.allBackends')}
                        searchPlaceholder={t('filters.searchBackends')}
                      />
                    </div>
                  )}
                </div>

                <button
          type="button"
          className="models-filters__usecase-trigger"
          aria-haspopup="dialog"
          aria-expanded={useCaseOpen}
          onClick={() => setUseCaseOpen(v => !v)}
        >
          <i className="fas fa-layer-group" aria-hidden="true" />
          <span>{useCaseLabel}</span>
          <i className={`fas fa-chevron-${useCaseOpen ? 'up' : 'down'} models-filters__usecase-caret`} aria-hidden="true" />
        </button>

        {/* An inline disclosure rather than a popover. Picking use cases is
            multi-select and interleaves with the backend select and the
            refinements below, and a popover dismisses itself the moment you
            touch either of those, which turns one decision into three. */}
        {useCaseOpen && (
        <div className="models-filters__usecases" role="group" aria-label={t('filters.useCaseLabel')}>
                  {FILTER_SECTIONS.map(section => {
            const inSection = FILTERS.filter(f => section.keys.includes(f.key))
            if (inSection.length === 0) return null
            return (
              <div className="models-filters__usecase-group" key={section.id}>
                {section.labelKey && (
                  <span className="models-filters__usecase-label">{t(section.labelKey)}</span>
                )}
                <div className="filter-bar">
                  {inSection.map(f => {
                    const isAll = f.key === ''
                    const active = isAll ? filters.length === 0 : filters.includes(f.key)
                    const available = isFilterAvailable(f.key)
                    return (
                      <button
                        key={f.key}
                        type="button"
                        className={`filter-btn ${active ? 'active' : ''}`}
                        disabled={!available}
                        aria-pressed={active}
                        title={!available ? t('filters.unavailableForBackend') : undefined}
                        onClick={() => toggleFilter(f.key)}
                      >
                        <i className={`fas ${f.icon}`} aria-hidden="true" />
                        {t(f.labelKey)}
                      </button>
                    )
                  })}
                </div>
              </div>
            )
          })}
                </div>
                )}

                <div className="models-filters__refine" data-testid="models-filters-refine">
                  <span className="models-filters__refine-label">{t('filters.refineLabel')}</span>
                  {/* Leads the band because it decides how many rows the other two
                      refine over, and because unlike fits-in-GPU it is always present:
                      a host with no GPU still browses builds. Turning it off is the
                      only way to page through every build the gallery holds; searching
                      reaches a specific one but cannot enumerate them. */}
                  <label className="filter-bar-group__toggle" data-testid="models-collapse-variants">
                    <Toggle
                      checked={collapseVariants}
                      onChange={(v) => { setCollapseVariants(v); setPage(1) }}
                    />
                    <i className="fas fa-layer-group" aria-hidden="true" />
                    <span>{t('filters.collapseVariants')}</span>
                  </label>
                  {totalGpuMemory > 0 && (
                    <label className="filter-bar-group__toggle">
                      <Toggle checked={fitsFilter} onChange={setFitsFilter} />
                      <i className="fas fa-microchip" aria-hidden="true" />
                      <span>{t('filters.fitsGpu')}</span>
                    </label>
                  )}
                  <div className="models-filters__context">
                    <label htmlFor="models-context-size">
                      <i className="fas fa-memory" aria-hidden="true" />
                      {t('filters.contextSize')}
                    </label>
                    <input
                      id="models-context-size"
                      type="range"
                      min={0}
                      max={CONTEXT_SIZES.length - 1}
                      value={CONTEXT_SIZES.indexOf(contextSize)}
                      // The slider steps over an index, so the raw value ("2") is
                      // meaningless to a screen reader; announce the size instead.
                      aria-valuetext={CONTEXT_LABELS[CONTEXT_SIZES.indexOf(contextSize)]}
                      onChange={(e) => setContextSize(CONTEXT_SIZES[e.target.value])}
                    />
                    <span className="models-filters__context-value">
                      {CONTEXT_LABELS[CONTEXT_SIZES.indexOf(contextSize)]}
                    </span>
                  </div>
                </div>
              </div>
              {/* Grouped while browsing, flat while searching: once a term is
                  typed the buckets stand between the reader and the answer. */}
              <EntityRail
                items={visibleModels.map(m => railItemFor(m, { estimates, pendingEstimates, contextSize, fitsGpu, isInstalling, getOperationProgress, t }))}
                groups={ENTITY_GROUPS.map(g => ({ id: g.id, label: t(g.labelKey), icon: g.icon }))}
                grouped={!search.trim()}
                collapsedGroups={collapsedGroups}
                onToggleGroup={toggleGroup}
                busy={loading}
                selectedId={selectedName}
                onSelect={selectModel}
                countLabel={t('rail.showingCount', { shown: visibleModels.length, total: stats.total })}
                ariaLabel={t('title')}
                testId="discover-rail"
                actions={
                  <div className="entity-rail__sort" role="group" aria-label={t('rail.sortLabel')}>
                    <SortButton col="name" label={t('table.modelName')} sort={sort} order={order} onSort={handleSort} />
                    <SortButton col="status" label={t('table.status')} sort={sort} order={order} onSort={handleSort} />
                  </div>
                }
              />

              {totalPages > 1 && (
                <div className="pagination split-view__pager">
                  <button className="pagination-btn" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} aria-label={t('rail.previousPage')}>
                    <i className="fas fa-chevron-left" />
                  </button>
                  <span className="split-view__pager-label">{page} / {totalPages}</span>
                  <button className="pagination-btn" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page === totalPages} aria-label={t('rail.nextPage')}>
                    <i className="fas fa-chevron-right" />
                  </button>
                </div>
              )}
            </>
          }
          pane={
            visibleModels.length === 0 ? (
    <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-search" /></div>
              <h2 className="empty-state-title">{t('empty.title')}</h2>
              <p className="empty-state-text">
                {search || filters.length > 0 || backendFilter || fitsFilter || !collapseVariants ? t('empty.withFilters') : t('empty.noFilters')}
              </p>
              {/* Only the fits filter can leave the collapse to blame. The term,
                  the chips and the backend are applied server-side over every build
                  the gallery holds, and a match there is always reported as some
                  row, so those three can no longer come back empty on account of
                  the collapse. Fits runs here in the browser, after the server
                  substituted a matching build for the entry that offers it, and
                  judges that entry's own size: the build that fits can still be
                  filtered out along with a parent that does not. */}
              {collapseVariants && fitsFilter && (
                <p className="empty-state-hint">{t('empty.collapsedVariantsHint')}</p>
              )}
              {(search || filters.length > 0 || backendFilter || fitsFilter || !collapseVariants) && (
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => { handleSearch(''); setFilters([]); setBackendFilter(''); setFitsFilter(false); setCollapseVariants(COLLAPSE_VARIANTS_DEFAULT); setPage(1) }}
                >
                  <i className="fas fa-times" /> {t('search.clearFilters')}
                </button>
              )}
            </div>
            ) : selectedModel ? (
              <DiscoverDetail
                model={selectedModel}
                estimate={estimates[selectedName]}
                contextSize={contextSize}
                onPickContext={setContextSize}
                totalGpuMemory={totalGpuMemory}
                fitsGpu={fitsGpu}
                installing={isInstalling(selectedName)}
                progress={getOperationProgress(selectedName)}
                onInstall={handleInstall}
                onDelete={handleDelete}
                onBack={() => selectModel(null)}
                expandedFiles={expandedFiles}
                setExpandedFiles={setExpandedFiles}
                variantData={selectedModel.has_variants ? variantData[selectedName] : null}
                variantDetails={variantDetails}
                onLoadVariantDetail={loadVariantDetail}
                t={t}
              />
            ) : (
              <div className="zero-pane">
                <div className="zero-pane__hero">
                  <span className="zero-pane__eyebrow">{t('shelves.hostLabel')}</span>
                  <h2 className="zero-pane__title">
                    {/* The resources endpoint reports system RAM when there is
                        no accelerator, so calling it "GPU memory" was a claim
                        the data did not support. */}
                    {totalGpuMemory <= 0
                      ? t('shelves.heroNoGpu', { count: stats.total })
                      : hasGpu
                        ? t('shelves.heroWithGpu', { vram: formatBytes(totalGpuMemory), count: stats.total })
                        : t('shelves.heroWithRam', { ram: formatBytes(totalGpuMemory), count: stats.total })}
                  </h2>
                  <p className="zero-pane__text">{t('shelves.heroHint')}</p>
                </div>

                {/* The hardware-fit strip is the curation, and here it finally
                    gets the width to argue for a model rather than list one.
                    It keeps its own dismissal and collapse state, so someone
                    who closed it still lands on the pane below. */}
                <RecommendedModels addToast={addToast} />

                {/* Somewhere to start when the recommendations are not it.
                    These set the use-case filter rather than fetching a second
                    list, so a shelf costs nothing and cannot go stale. */}
                <div className="zero-pane__shelf">
                  <div className="zero-pane__shelf-head">
                    <h3 className="zero-pane__shelf-title">{t('shelves.byUseCase')}</h3>
                  </div>
                  {/* Lanes, not tiles. These are a list of ways in, read in
                      order — a grid of equal cards asks the reader to compare
                      them, which is not the choice being offered. */}
                  <ul className="lanes lanes--usecase">
                    {FILTER_SECTIONS.filter(sec => sec.pick).map(sec => (
                      <li key={sec.id}>
                        <button
                          type="button"
                          className="lane"
                          onClick={() => { setFilters([sec.pick]); setPage(1); setUseCaseOpen(false) }}
                        >
                          <span className="lane__tag">
                            <i className={`fas ${sec.icon}`} aria-hidden="true" /> {t(sec.labelKey)}
                          </span>
                          <span className="lane__desc">{t(sec.blurbKey)}</span>
                          <span className="lane__go" aria-hidden="true">→</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            )
          }
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
    </div>
  )
}

// variantSizeLabel renders a variant footprint. memory_bytes is omitempty on
// the wire, so an absent key means the probe could not determine a size; it
// must never render as "0 B", which would read as "needs nothing".
function variantSizeLabel(variant, t) {
  return variant?.memory_bytes ? formatBytes(variant.memory_bytes) : t('variants.unknownSize')
}

// variantFeatureLabel spells out a serving feature.
//
// The vocabulary is short and curated server-side, so each token has a real
// translated name. An unrecognised one still renders as its uppercased token
// rather than being dropped: the server's list can grow ahead of the locale
// files, and a missing string is a worse outcome than an untranslated one when
// the alternative is silently hiding a genuine reason to pick a build.
function variantFeatureLabel(feature, t) {
  return t(`variants.features.${feature}`, { defaultValue: feature.toUpperCase() })
}

function DetailRow({ label, children }) {
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

// VariantDetailPanel is the same detail view a top-level row gets, rendered for
// one variant.
//
// It reuses ModelDetail rather than restating what an entry looks like, so a
// field added to the detail view appears here too. variantData is deliberately
// withheld: a variant's own entry may declare variants of its own, and
// recursing would nest a picker inside a picker two levels deep already. The
// file disclosure gets its own state here because each panel opens and closes
// independently of the parent's.
function VariantDetailPanel({ model, t }) {
  const [expandedFiles, setExpandedFiles] = useState(false)
  return (
    <ModelDetail
      model={model}
      nested
      expandedFiles={expandedFiles}
      setExpandedFiles={setExpandedFiles}
      variantData={null}
      t={t}
    />
  )
}

function ModelDetail({ model, fit, sizeDisplay, vramDisplay, expandedFiles, setExpandedFiles, variantData, variantDetails, onLoadVariantDetail, installing, onInstall, nested, t }) {
  const files = model.additionalFiles || model.files || []
  const name = model.name || model.id
  // Which variant has its details revealed, or null. One at a time: the list is
  // a comparison, and two open panels push the rows being compared apart.
  const [openVariant, setOpenVariant] = useState(null)
  // Escape returns focus to the control that opened the panel, so dismissing by
  // keyboard does not drop the user back at the top of the document.
  const infoRefs = useRef({})
  return (
    <div style={{
      padding: 'var(--spacing-md) var(--spacing-lg)',
      background: nested ? 'transparent' : 'var(--color-bg-primary)',
      borderTop: nested ? 'none' : '1px solid var(--color-border-subtle)',
    }}>
      {model.description && (
        // Prose sits outside the label/value table: an eight-line value cell
        // in a grid of one-line ones breaks the rhythm exactly where the eye
        // enters, and the full pane width is roughly double a readable measure.
        <div className="detail-prose">
          <div className="detail-prose__label">{t('detail.description')}</div>
          <div
            className="markdown-body detail-prose__body"
            dangerouslySetInnerHTML={{ __html: renderMarkdown(model.description) }}
          />
        </div>
      )}
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          <DetailRow label={t('detail.gallery')}>
            {model.gallery && (
              <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                {typeof model.gallery === 'string' ? model.gallery : model.gallery.name || '—'}
              </span>
            )}
          </DetailRow>
          <DetailRow label={t('detail.backend')}>
            {model.backend && (
              <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                {model.backend}
              </span>
            )}
          </DetailRow>
          <DetailRow label={t('detail.size')}>
            {sizeDisplay && sizeDisplay !== '0 B' ? sizeDisplay : null}
          </DetailRow>
          <DetailRow label={t('detail.vram')}>
            {vramDisplay && vramDisplay !== '0 B' ? (
              <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                {vramDisplay}
                {fit !== null && (
                  <span style={{ fontSize: '0.75rem', color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
                    <i className="fas fa-microchip" /> {fit ? t('detail.fitsGpu') : t('detail.mayNotFitGpu')}
                  </span>
                )}
              </span>
            ) : null}
          </DetailRow>
          {variantData?.loading && (
            <DetailRow label={t('variants.title')}>
              <span style={{ color: 'var(--color-text-muted)' }}>
                <i className="fas fa-spinner fa-spin" style={{ marginRight: 6 }} />{t('variants.loading')}
              </span>
            </DetailRow>
          )}
          {variantData?.variants?.length > 0 && (
            <DetailRow label={t('variants.title')}>
              <div className="variant-list">
                {variantData.variants.map(v => {
                  const isAuto = v.model === variantData.auto_selected
                  const detail = variantDetails?.[v.model]
                  const detailOpen = openVariant === v.model
                  const panelId = `variant-detail-${v.model}`
                  return (
                    <div
                      key={v.model}
                      className="variant-entry"
                      onKeyDown={(e) => {
                        if (e.key !== 'Escape' || !detailOpen) return
                        // Stops the row's own expansion, and any dialog above
                        // it, from also closing on the same keystroke.
                        e.stopPropagation()
                        setOpenVariant(null)
                        infoRefs.current[v.model]?.focus()
                      }}
                    >
                    {/* A separate control, not a region of the install button:
                        nesting it would be invalid markup and, worse, would
                        make "tell me more" a click on "install this". It leads
                        the row because it acts on the name that follows it. */}
                    <button
                      type="button"
                      ref={(el) => { infoRefs.current[v.model] = el }}
                      className="variant-row__info"
                      aria-expanded={detailOpen}
                      aria-controls={detailOpen ? panelId : undefined}
                      // Named after the build it describes: a column of
                      // identical "Details" buttons tells a screen reader
                      // user nothing about which row they are on.
                      aria-label={detailOpen
                        ? t('variants.hideDetails', { variant: v.model })
                        : t('variants.showDetails', { variant: v.model })}
                      onClick={(e) => {
                        e.stopPropagation()
                        if (detailOpen) { setOpenVariant(null); return }
                        setOpenVariant(v.model)
                        onLoadVariantDetail?.(v.model)
                      }}
                    >
                      <i className="fas fa-circle-info" aria-hidden="true" />
                    </button>
                    {/* Listing the alternatives without offering them made the
                        detail view read as a menu that could not be ordered
                        from; installing one is the same call the split-button
                        chevron already makes. */}
                    <button
                      type="button"
                      className={`variant-row${v.fits ? '' : ' variant-row--unfit'}`}
                      disabled={installing}
                      aria-label={t('variants.installVariant', { variant: v.model })}
                      onClick={(e) => { e.stopPropagation(); onInstall(name, v.model) }}
                    >
                      <span className="variant-row__name">{v.model}</span>
                      <span className="variant-row__backend">{v.backend || t('variants.unknownBackend')}</span>
                      {/* Its own column rather than appended to the backend
                          cell, so precision lines up down the list and two
                          builds can be compared by scanning rather than by
                          reading. An entry naming no weight format says so:
                          an empty cell in an aligned column reads as a
                          rendering fault. */}
                      <span
                        className={`variant-row__quant${v.quantization ? '' : ' variant-row__quant--unknown'}`}
                        title={t('variants.quantizationTitle')}
                      >
                        {v.quantization || t('variants.unknownQuantization')}
                      </span>
                      <span className="variant-row__size">{variantSizeLabel(v, t)}</span>
                      <span className="variant-row__status">
                        {isAuto && (
                          <span className="badge badge-success">
                            <i className="fas fa-circle-check" /> {t('variants.autoSelected')}
                          </span>
                        )}
                        {!v.fits && <span className="badge badge-warning">{t('variants.doesNotFit')}</span>}
                        {v.is_base && !isAuto && <span className="badge badge-info">{t('variants.base')}</span>}
                        {/* The room the detail row has over the dropdown is
                            spent here: "DFLASH" names nothing to a user who
                            has not met it, whereas the spelled-out feature
                            says why this build is worth choosing. */}
                        {(v.features || []).map(f => (
                          <span key={f} className="badge badge-info">
                            <i className="fas fa-bolt" aria-hidden="true" /> {variantFeatureLabel(f, t)}
                          </span>
                        ))}
                      </span>
                      <i className="fas fa-download variant-row__action" aria-hidden="true" />
                    </button>
                    {detailOpen && (
                      // An inline disclosure rather than a modal. This is
                      // already inside an expanded row, and a dialog opened
                      // from there stacks a dismissal on top of a dismissal
                      // for what is a few more lines of the same entry. The
                      // rule and inset carry the third level instead.
                      <div className="variant-detail" id={panelId}>
                        {(!detail || detail.loading) && (
                          <div className="variant-detail__state">
                            <i className="fas fa-spinner fa-spin" aria-hidden="true" />
                            <span>{t('variants.detailsLoading')}</span>
                          </div>
                        )}
                        {detail?.error && (
                          // Stated, not blank: an empty panel reads as a
                          // rendering fault rather than as a lookup that
                          // failed.
                          <div className="variant-detail__state variant-detail__state--error" role="status">
                            <i className="fas fa-triangle-exclamation" aria-hidden="true" />
                            <span>{t('variants.detailsUnavailable', { variant: v.model })}</span>
                          </div>
                        )}
                        {detail?.entry && <VariantDetailPanel model={detail.entry} t={t} />}
                      </div>
                    )}
                    </div>
                  )
                })}
              </div>
            </DetailRow>
          )}
          <DetailRow label={t('detail.license')}>
            {model.license && <span>{model.license}</span>}
          </DetailRow>
          <DetailRow label={t('detail.tags')}>
            {model.tags?.length > 0 && (
              <div style={{ display: 'flex', gap: 'var(--spacing-xs)', flexWrap: 'wrap' }}>
                {model.tags.map(tag => (
                  <span key={tag} className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{tag}</span>
                ))}
              </div>
            )}
          </DetailRow>
          <DetailRow label={t('detail.links')}>
            {model.urls?.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                {model.urls.map((url, i) => (
                  <a key={i} href={safeHref(url)} target="_blank" rel="noopener noreferrer" style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', wordBreak: 'break-all' }}>
                    <i className="fas fa-external-link-alt" style={{ marginRight: 4, fontSize: '0.6875rem' }} />{url}
                  </a>
                ))}
              </div>
            )}
          </DetailRow>
          {model.trustRemoteCode && (
            <DetailRow label={t('detail.warning')}>
              <span className="badge badge-error" style={{ fontSize: '0.6875rem' }}>
                <i className="fas fa-circle-exclamation" /> {t('detail.requiresTrustRemoteCode')}
              </span>
            </DetailRow>
          )}
          {files.length > 0 && (
            <DetailRow label={t('detail.files')}>
              <div>
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={(e) => { e.stopPropagation(); setExpandedFiles(!expandedFiles) }}
                  style={{ marginBottom: expandedFiles ? 'var(--spacing-sm)' : 0 }}
                >
                  <i className={`fas fa-chevron-${expandedFiles ? 'down' : 'right'}`} style={{ fontSize: '0.5rem', marginRight: 4 }} />
                  {t('detail.fileCount', { count: files.length })}
                </button>
                {expandedFiles && (
                  <div style={{ border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', overflow: 'hidden' }}>
                    <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.75rem' }}>
                      <thead>
                        <tr style={{ background: 'var(--color-bg-tertiary)' }}>
                          <th style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', textAlign: 'left', fontWeight: 500 }}>{t('detail.filename')}</th>
                          <th style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', textAlign: 'left', fontWeight: 500 }}>{t('detail.uri')}</th>
                          <th style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', textAlign: 'left', fontWeight: 500 }}>{t('detail.sha256')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {files.map((f, i) => (
                          <tr key={i} style={{ borderTop: '1px solid var(--color-border-subtle)' }}>
                            <td style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontFamily: 'var(--font-mono)' }}>{f.filename || '—'}</td>
                            <td style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', wordBreak: 'break-all', maxWidth: 300 }}>{f.uri || '—'}</td>
                            <td style={{ padding: 'var(--spacing-xs) var(--spacing-sm)', fontFamily: 'var(--font-mono)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                              {f.sha256 ? f.sha256.substring(0, 16) + '...' : '—'}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </DetailRow>
          )}
        </tbody>
      </table>
    </div>
  )
}

// railItemFor maps a gallery entry onto the shape EntityRail speaks. Keeping
// the vocabulary translation here, rather than teaching the rail about models,
// is what lets Backends and Host reuse the same component without three
// slightly different rails growing out of it.
//
// The rail line gets exactly one fact beyond the name, and it is spent on
// whether the thing will run here. Descriptions belong in the pane; two lines
// is the budget and the second one is worth more as an answer than as prose.
function railItemFor(model, { estimates, pendingEstimates, contextSize, fitsGpu, isInstalling, getOperationProgress, t }) {
  const name = model.name || model.id
  const est = estimates[name]
  const sizeDisplay = est?.sizeDisplay
  const vramBytes = est?.estimates?.[String(contextSize)]?.vramBytes
  const fit = fitsGpu(vramBytes)
  const hasSize = sizeDisplay && sizeDisplay !== '0 B'
  const installing = isInstalling(name)
  const progress = getOperationProgress(name)

  let meta = model.backend || ''
  let metaTone
  if (installing) {
    meta = progress > 0 ? t('rail.downloadingPct', { percent: Math.round(progress) }) : t('table.installing')
    metaTone = 'busy'
  } else if (model.installed) {
    meta = t('table.installed')
    metaTone = 'ok'
  } else if (hasSize && fit === false) {
    meta = t('rail.tooLarge', { size: sizeDisplay })
    metaTone = 'bad'
  } else if (hasSize && fit === true) {
    meta = t('rail.fitsSize', { size: sizeDisplay })
  } else if (hasSize) {
    meta = sizeDisplay
  } else if (pendingEstimates?.has(name)) {
    // Say the size is coming rather than leaving the line to fill in silently.
    // The row is usable now; only the "will it fit" answer is still on its way.
    meta = t('rail.sizing')
    metaTone = 'pending'
  }

  return { id: name, name, icon: groupForEntity(model).icon, meta, metaTone, groupId: groupForEntity(model).id }
}

// SortButton is the home sorting found after the column headers went. It sits
// in the rail rather than the filter band above, because it orders this list
// and nothing else on the page.
function SortButton({ col, label, sort, order, onSort }) {
  const active = sort === col
  return (
    <button
      type="button"
      className={`entity-rail__sort-btn${active ? ' active' : ''}`}
      aria-pressed={active}
      onClick={() => onSort(col)}
    >
      {label}
      {active && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} aria-hidden="true" />}
    </button>
  )
}

// VramByContext plots the estimate the fits filter actually tests against, at
// every context length the page asks the server for.
//
// It exists because a single number answers the wrong question. "Needs 6.2 GB"
// invites "so will it run?", and the honest answer is usually "yes, up to a
// 32k context" - which is a shape, not a number. The limit line is what makes
// the bars mean anything, so a host with no GPU gets no chart at all rather
// than a chart with nothing to compare against.
function VramByContext({ estimate, contextSize, onPickContext, totalGpuMemory, t }) {
  if (!(totalGpuMemory > 0)) return null
  const points = CONTEXT_SIZES
    .map((ctx, i) => ({ ctx, label: CONTEXT_LABELS[i], bytes: estimate?.estimates?.[String(ctx)]?.vramBytes || 0 }))
    .filter(p => p.bytes > 0)
  // One bar compares with nothing; the stat grid already states that number.
  if (points.length < 2) return null

  const limit = totalGpuMemory * 0.95
  const max = Math.max(limit, ...points.map(p => p.bytes)) * 1.12
  const over = points.filter(p => p.bytes > limit).length
  const lastFitting = points.reduce((acc, p) => (p.bytes <= limit ? p : acc), null)

  let verdictClass = 'ok'
  let verdict = t('chart.fitsEverywhere')
  if (over === points.length) {
    verdictClass = 'bad'
    verdict = t('chart.fitsNowhere')
  } else if (over > 0) {
    // Warn, however many sizes are over. A model that fits at 8k but not 32k is
    // a trade-off, not a fault, and it stays installable — reserving the error
    // tone for "fits nowhere" keeps that distinction legible.
    verdictClass = 'warn'
    verdict = t('chart.fitsUpTo', { context: lastFitting.label })
  }

  // Heights are resolved in pixels against a known plot height rather than as
  // percentages. A percentage would resolve against the column box, which also
  // holds the value label, so the tallest bars would overflow it.
  const track = CHART_HEIGHT - CHART_LABEL_ROOM

  return (
    <div className="discover__chart">
      <span className="discover__chart-title">{t('chart.title')}</span>
      <div className="discover__chart-plot">
        <div
          className="discover__chart-limit"
          style={{ '--discover-limit': `${(limit / max) * track}px` }}
        >
          <span className="discover__chart-limit-label">{t('chart.available', { vram: formatBytes(totalGpuMemory) })}</span>
        </div>
        {points.map(p => {
          const unfit = p.bytes > limit
          return (
            <button
              type="button"
              key={p.ctx}
              className={`discover__chart-col${p.ctx === contextSize ? ' discover__chart-col--on' : ''}`}
              aria-pressed={p.ctx === contextSize}
              // The bars read the context size out and set it: the slider in
              // the filter band writes the same value, and picking the length
              // you care about here is the same gesture as reading its bar.
              onClick={() => onPickContext(p.ctx)}
              title={t('chart.barTitle', { context: p.label, vram: formatBytes(p.bytes) })}
            >
              <span className="discover__chart-value">{formatBytes(p.bytes)}</span>
              <span
                className={`discover__chart-bar${unfit ? ' discover__chart-bar--over' : ''}`}
                style={{ '--discover-bar': `${(p.bytes / max) * track}px` }}
              />
            </button>
          )
        })}
      </div>
      {/* The axis is its own row so every column shares one baseline, which a
          label inside each column cannot guarantee once the values above them
          wrap differently. */}
      <div className="discover__chart-axis" aria-hidden="true">
        {points.map(p => (
          <span key={p.ctx} className={p.ctx === contextSize ? 'discover__chart-axis-on' : undefined}>{p.label}</span>
        ))}
      </div>
      <p className={`discover__chart-verdict discover__chart-verdict--${verdictClass}`}>
        <i className="fas fa-microchip" aria-hidden="true" /> {verdict}
      </p>
    </div>
  )
}

// DiscoverDetail is the pane with a model selected. It owns the part a table
// row could not hold - the headline numbers, the VRAM curve and the actions -
// and hands the rest to ModelDetail, which already knows how to render an
// entry's fields and is shared with the per-variant panel.
function DiscoverDetail({
  model, estimate, contextSize, onPickContext, totalGpuMemory, fitsGpu,
  installing, progress, onInstall, onDelete, onBack,
  expandedFiles, setExpandedFiles, variantData, variantDetails, onLoadVariantDetail, t,
}) {
  const name = model.name || model.id
  const sizeDisplay = estimate?.sizeDisplay
  const vramBytes = estimate?.estimates?.[String(contextSize)]?.vramBytes
  const fit = fitsGpu(vramBytes)
  const contextLabel = CONTEXT_LABELS[CONTEXT_SIZES.indexOf(contextSize)]
  const headroom = totalGpuMemory > 0 && vramBytes ? totalGpuMemory * 0.95 - vramBytes : null

  return (
    <div className="detail-pane">
      <DetailHeader
        testId="discover"
        icon={groupForEntity(model).icon}
        name={name}
        lede={model.description ? stripMarkdown(model.description).slice(0, 220) : null}
        ledeTitle={model.description ? stripMarkdown(model.description) : null}
        onBack={onBack}
        backLabel={t('detail.backToAll')}
        warning={model.trustRemoteCode ? t('detail.requiresTrustRemoteCode') : null}
        actions={
          installing ? (
            <div className="inline-install">
              <div className="inline-install__row">
                <div className="operation-spinner" />
                <span className="inline-install__label">
                  {progress > 0 ? t('table.installingPct', { percent: Math.round(progress) }) : `${t('table.installing')}...`}
                </span>
              </div>
              {progress > 0 && (
                <div className="operation-bar-container discover__progress">
                  <div className="operation-bar" style={{ width: `${progress}%` }} />
                </div>
              )}
            </div>
          ) : model.installed ? (
            <>
              <button className="btn btn-secondary btn-sm" onClick={() => onInstall(name)}>
                <i className="fas fa-rotate" /> {t('actions.reinstall')}
              </button>
              <button className="btn btn-danger btn-sm" onClick={() => onDelete(name)}>
                <i className="fas fa-trash" /> {t('actions.delete')}
              </button>
            </>
          ) : (
            <button className="btn btn-primary btn-sm" onClick={() => onInstall(name)} data-testid="discover-install">
              <i className="fas fa-download" /> {t('actions.install')}
            </button>
          )
        }
      />

      <StatGrid
        stats={[
          { label: t('detail.size'), value: sizeDisplay && sizeDisplay !== '0 B' ? sizeDisplay : '—' },
          { label: t('detail.vramAt', { context: contextLabel }), value: vramBytes ? formatBytes(vramBytes) : '—' },
          {
            label: t('detail.headroom'),
            value: headroom === null ? '—' : (headroom < 0 ? '−' : '') + formatBytes(Math.abs(headroom)),
            tone: headroom === null ? undefined : headroom < 0 ? 'bad' : 'ok',
          },
        ]}
      />

      <VramByContext
        estimate={estimate}
        contextSize={contextSize}
        onPickContext={onPickContext}
        totalGpuMemory={totalGpuMemory}
        t={t}
      />

      {/* sizeDisplay and vramDisplay are withheld: the stat grid above already
          states both, and ModelDetail drops a row whose value is empty. */}
      <ModelDetail
        model={model}
        fit={fit}
        expandedFiles={expandedFiles}
        setExpandedFiles={setExpandedFiles}
        variantData={variantData}
        variantDetails={variantDetails}
        onLoadVariantDetail={onLoadVariantDetail}
        installing={installing}
        onInstall={onInstall}
        nested
        t={t}
      />
    </div>
  )
}
