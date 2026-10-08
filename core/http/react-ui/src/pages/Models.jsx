// eslint-disable-next-line no-unused-vars
import { useState, useCallback, useEffect, useLayoutEffect, useRef, Suspense } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, Outlet, useMatch, useNavigate, useOutletContext, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import { modelPath } from '../utils/modelWalk'
import { modelsApi, systemApi } from '../utils/api'
import { safeHref } from '../utils/url'
import { useDebouncedCallback } from '../hooks/useDebounce'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import { useFacetCounts } from '../hooks/useFacetCounts'
import { useModelRemoval } from '../hooks/useModelRemoval'
import { rememberSize } from '../hooks/useModelSizes'
import { modelBudget } from '../utils/modelBudget'
import { UNDO_MS } from '../utils/cleanupPlan'
import { diskState, fitFor, fitStyle, leavesFree, gbLabel, gbNumber } from '../utils/modelLedger'
import SearchableSelect from '../components/SearchableSelect'
import Toggle from '../components/Toggle'
import RecommendedModels from '../components/RecommendedModels'
// eslint-disable-next-line no-unused-vars
import ExploreTable from '../components/models/ExploreTable'
import FacetBar from '../components/models/FacetBar'
import DiskStrip from '../components/models/DiskStrip'
// eslint-disable-next-line no-unused-vars
import CleanupSheet from '../components/models/CleanupSheet'
import { useLedgerKeys } from '../components/models/rowKeys'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
import InstalledModels, { ModelLifecycleDetailShell, modelUseCases } from './InstalledModels'
import { formatBytes } from '../utils/format'
import { groupForEntity } from '../utils/entityGroups'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import React from 'react'
import Icon from '../components/Icon'
import './models-ledger.css'


// The ledger pages through the gallery thirty rows at a time: enough for the
// groups to mean something, few enough that a page of estimates arrives fast.
const PAGE_SIZE = 30

// How many estimates to have in flight at once. See the fetch effect: this
// exists to leave connections free for whatever the user clicks next.
const ESTIMATE_CONCURRENCY = 4

const CHART_HEIGHT = 96
const CHART_LABEL_ROOM = 15

const CONTEXT_SIZES = [8192, 16384, 32768, 65536, 131072, 262144]
const CONTEXT_LABELS = ['8K', '16K', '32K', '64K', '128K', '256K']
const FITS_FILTER_STORAGE_KEY = 'localai-models-fits-filter'
const COLLAPSE_VARIANTS_STORAGE_KEY = 'localai-models-collapse-variants-filter'
const DENSITY_STORAGE_KEY = 'localai-models-density'
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

const readDensity = () => {
  try {
    return localStorage.getItem(DENSITY_STORAGE_KEY) === 'compact' ? 'compact' : 'comfortable'
  } catch {
    return 'comfortable'
  }
}

const FILTERS = [
  { key: '', labelKey: 'filters.all', icon: 'layers' },
  { key: 'chat', labelKey: 'filters.llm', icon: 'brain' },
  { key: 'image', labelKey: 'filters.image', icon: 'image' },
  { key: 'video', labelKey: 'filters.video', icon: 'video' },
  { key: '3d', labelKey: 'filters.threed', icon: 'cube' },
  { key: '3d_animation', labelKey: 'filters.threedAnimation', icon: 'walk' },
  { key: 'multimodal', labelKey: 'filters.multimodal', icon: 'shapes' },
  { key: 'vision', labelKey: 'filters.vision', icon: 'eye' },
  { key: 'tts', labelKey: 'filters.tts', icon: 'mic' },
  { key: 'transcript', labelKey: 'filters.stt', icon: 'headphones' },
  { key: 'diarization', labelKey: 'filters.diarization', icon: 'users' },
  { key: 'sound_classification', labelKey: 'filters.soundClassification', icon: 'ear' },
  { key: 'sound_generation', labelKey: 'filters.soundGen', icon: 'music' },
  { key: 'audio_transform', labelKey: 'filters.audioTransform', icon: 'sliders' },
  { key: 'realtime_audio', labelKey: 'filters.realtimeAudio', icon: 'broadcast' },
  { key: 'embeddings', labelKey: 'filters.embedding', icon: 'bounding-box' },
  { key: 'rerank', labelKey: 'filters.rerank', icon: 'sort' },
  { key: 'detection', labelKey: 'filters.detection', icon: 'target' },
  { key: 'vad', labelKey: 'filters.vad', icon: 'waveform' },
  { key: 'token_classify', labelKey: 'filters.ner', icon: 'tag' },
]
const FACET_KEYS = FILTERS.map(f => f.key)

// The families the discovery pane offers as ways in. Each picks one facet.
const USE_CASE_LANES = [
  { id: 'text', labelKey: 'groups.text', icon: 'brain', pick: 'chat', blurbKey: 'shelves.pickText' },
  { id: 'vision', labelKey: 'groups.vision', icon: 'eye', pick: 'vision', blurbKey: 'shelves.pickVision' },
  { id: 'audio', labelKey: 'groups.audio', icon: 'waveform', pick: 'tts', blurbKey: 'shelves.pickAudio' },
  { id: 'visual', labelKey: 'groups.visual', icon: 'image', pick: 'image', blurbKey: 'shelves.pickVisual' },
]

// CSS custom property for the context slider's filled part.
function rangeStyle(index) {
  return { '--dk-fill': `${(index / (CONTEXT_SIZES.length - 1)) * 100}%` }
}

function ModelsTabs({ activeView, searchParams, installedCount, t }) {
  const hrefFor = view => {
    const next = new URLSearchParams(searchParams)
    if (view === 'installed') next.set('view', 'installed')
    else next.delete('view')
    const query = next.toString()
    return `/app/models${query ? `?${query}` : ''}`
  }

  return (
    <nav className="dk-hubtabs models-tabs" aria-label={t('lifecycle.navLabel')}>
      <Link
        className="dk-hubtab"
        to={hrefFor('explore')}
        aria-current={activeView === 'explore' ? 'page' : undefined}
      >
        <Icon name="compass" /> <span>{t('lifecycle.views.explore')}</span>
      </Link>
      <Link
        className="dk-hubtab"
        to={hrefFor('installed')}
        aria-current={activeView === 'installed' ? 'page' : undefined}
      >
        <Icon name="hard-drive" /> <span>{t('lifecycle.views.installed')}</span>
        {installedCount > 0 && <span className="dk-hubtab-count">{installedCount}</span>}
      </Link>
    </nav>
  )
}

export default function Models() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  // A model's page is a child route: this component stays mounted, hidden, so
  // the list is exactly as it was when the page closes.
  const detailOpen = !!useMatch('/app/models/:id')
  const { t } = useTranslation('models')
  const { operations, dismissFailedOp } = useOperations()
  const { resources } = useResources()
  const [liveParams, setSearchParams] = useSearchParams()
  // The list's own state lives in its address (view, search, state, selection).
  // While a model's page is open the address is that page's, so the list keeps
  // reading the one it had, and is exactly as it was when the page closes.
  const frozenParams = useRef(liveParams)
  if (!detailOpen) frozenParams.current = liveParams
  const searchParams = frozenParams.current
  const activeView = searchParams.get('view') === 'installed' ? 'installed' : 'explore'
  const installedState = ['running', 'idle', 'disabled', 'pinned', 'distributed'].includes(searchParams.get('state'))
    ? searchParams.get('state')
    : 'all'
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [page, setPage] = useState(1)
  const [totalPages, setTotalPages] = useState(1)
  const [search, setSearch] = useState(() => searchParams.get('q') || '')
  const [filters, setFilters] = useState([])
  const [sort, setSort] = useState('')
  const [order, setOrder] = useState('asc')
  const [installing, setInstalling] = useState(new Map())
  const [installedProfiles, setInstalledProfiles] = useState({})
  const [expandedFiles, setExpandedFiles] = useState(false)
  // Which model the inspector is showing, or null for the discovery shelves. It
  // lives in the URL so a model is linkable and so Back steps out of the detail
  // rather than off the page.
  const selectedName = searchParams.get('model')
  const urlSearch = searchParams.get('q') || ''
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
  const [density, setDensity] = useState(readDensity)
  const [sheetOpen, setSheetOpen] = useState(false)
  // Bumped when a removal finishes, so the Installed table and the cleanup
  // sheet read the list again.
  const [refreshToken, setRefreshToken] = useState(0)
  const searchRef = useRef(null)
  // True once any listing has come back. Distinguishes a cold start, which has
  // nothing to keep on screen, from a refetch, which does.
  const loadedOnce = useRef(false)
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
  // Rail groups the user has folded away.
  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())
  // What every "will it fit" verdict on this page is measured against. In
  // distributed mode that is the cluster's largest node rather than the
  // controller serving the page, which is usually a GPU-less pod (see
  // modelBudget).
  const budget = modelBudget(resources)
  const totalGpuMemory = budget.totalMemory
  const hasGpu = budget.hasGpu
  // RAM that could take what the GPU cannot. Absent in a cluster reading.
  const ramAvailable = resources?.ram?.available ?? resources?.ram?.free ?? null
  const disk = diskState(resources)

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
        items: PAGE_SIZE,
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
      setLoadError('')
    } catch (err) {
      // Shown inline, as a banner that stays until a load works, so no toast.
      setLoadError(err.message || String(err))
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

  // Gallery entries only say whether a model is installed. The capabilities
  // endpoint is authoritative about what that installation can open, so the
  // Explore detail uses it for a useful primary action without duplicating
  // destructive lifecycle controls from Installed.
  useEffect(() => {
    if (activeView !== 'explore') return undefined
    let cancelled = false
    modelsApi.listCapabilities()
      .then(data => {
        if (cancelled) return
        setInstalledProfiles(Object.fromEntries(
          (data?.data || []).map(profile => [profile.id, profile])
        ))
      })
      // A background refresh should not remove an action that was already
      // resolved successfully earlier in this page session.
      .catch(() => {})
    return () => { cancelled = true }
  }, [activeView, operations.length, refreshToken])

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
    // A page open over the list has no use for the rows' figures; they are
    // filled in when the list comes back.
    if (models.length === 0 || detailOpen) return
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
            rememberSize(id, est.sizeBytes)
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
  }, [models, detailOpen])

  const handleSearch = (value) => {
    setSearch(value)
    setSearchParams(previous => {
      const next = new URLSearchParams(previous)
      if (value) next.set('q', value)
      else next.delete('q')
      return next
    }, { replace: true })
    debouncedFetch(value)
  }

  // Search is URL-owned. Popstate changes therefore update the controlled
  // field and refetch the gallery instead of leaving the previous term on
  // screen after Back or Forward.
  useEffect(() => {
    if (urlSearch === search) return
    setSearch(urlSearch)
    setPage(1)
    debouncedFetch(urlSearch)
    // debouncedFetch intentionally follows the URL value only. Depending on
    // the callback itself would restart this effect whenever fetch state moves.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlSearch])

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

  // A failed install leaves its operation in the list with the error on it.
  // Retrying dismisses that record first, so the row does not show the old
  // failure beside the new attempt.
  const failedOp = (modelId) => operations.find(op => op.name === modelId && op.error) || null
  const handleRetry = async (modelId, op) => {
    if (op?.jobID) await dismissFailedOp(op.jobID)
    handleInstall(modelId)
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

  useEffect(() => {
    try {
      localStorage.setItem(DENSITY_STORAGE_KEY, density)
    } catch {
      // Ignore storage errors.
    }
  }, [density])

  const { counts: facetCounts, stale: facetsStale } = useFacetCounts({
    keys: FACET_KEYS,
    term: search.trim(),
    backend: backendFilter,
    collapse: collapseVariants,
    ready: activeView === 'explore' && statsLoaded,
  })

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

  const selectModel = useCallback((name, { replace } = {}) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (name) next.set('model', name)
      else next.delete('model')
      return next
    // Returning to the shelves replaces the entry rather than pushing one, so
    // Back leaves the gallery instead of bouncing between the two pane states.
    // So does moving with the arrow keys: a held key must not fill the history.
    }, { replace: !name || !!replace })
    setExpandedFiles(false)
  }, [setSearchParams])

  const setInstalledQuery = useCallback(value => {
    setSearchParams(previous => {
      const next = new URLSearchParams(previous)
      // `all` is a valid search term. Only an empty string clears q.
      if (value) next.set('q', value)
      else next.delete('q')
      return next
    }, { replace: true })
  }, [setSearchParams])

  const setInstalledState = useCallback(value => {
    setSearchParams(previous => {
      const next = new URLSearchParams(previous)
      // State alone reserves `all` as its default sentinel. q and model may
      // both name a literal installed model called "all".
      if (value && value !== 'all') next.set('state', value)
      else next.delete('state')
      return next
    })
  }, [setSearchParams])

  // One update for both, not two in a row: each call reads the URL as it was
  // when the page rendered, so the second would put back what the first removed.
  const clearInstalledFilters = useCallback(() => {
    setSearchParams(previous => {
      const next = new URLSearchParams(previous)
      next.delete('q')
      next.delete('state')
      return next
    })
  }, [setSearchParams])

  // The detail pane lists variants, so opening a model is the ask that pays for
  // the describe call. loadVariants is idempotent per name.
  useEffect(() => {
    if (selectedModel?.has_variants) loadVariants(selectedName)
  }, [selectedName, selectedModel, loadVariants])

  // Removal from the cleanup sheet. The delete itself is the call the row menu
  // uses; this hook only adds the wait before it (see useModelRemoval).
  const removal = useModelRemoval({
    deleteModel: id => modelsApi.deleteByName(id),
    loadRunning: async () => {
      const ids = new Set()
      try {
        const info = await systemApi.info()
        for (const m of Array.isArray(info?.loaded_models) ? info.loaded_models : []) ids.add(m.id)
      } catch { /* see useModelRemoval */ }
      try {
        const caps = await modelsApi.listCapabilities()
        for (const m of caps?.data || []) if (!m.disabled && m.loaded_on?.length > 0) ids.add(m.id)
      } catch { /* see useModelRemoval */ }
      return ids
    },
    onSettled: ({ removed, failed, skipped }) => {
      setRefreshToken(n => n + 1)
      if (removed.length > 0) addToast(t('cleanup.toasts.removed', { count: removed.length }), 'success')
      for (const f of failed) addToast(t('cleanup.toasts.failed', { model: f.id, message: f.message }), 'error')
      if (skipped.length > 0) addToast(t('cleanup.toasts.skipped', { models: skipped.join(', ') }), 'warning')
    },
    onAbandoned: ids => addToast(t('cleanup.toasts.abandoned', { count: ids.length }), 'info'),
  })
  const hiddenIds = new Set(removal.pending?.ids || [])
  const pendingFree = removal.pending ? removal.pending.items.reduce((sum, item) => sum + (item.size || 0), 0) : 0

  // --- a model's own page ----------------------------------------------------
  const hostRef = useRef(null)
  const savedScroll = useRef(null)
  const lastOpened = useRef(null)

  // The scroll offsets of the list and its table, taken before the page hides
  // them: a hidden element forgets where it was scrolled to.
  const rememberScroll = useCallback(() => {
    const root = hostRef.current
    if (!root) return
    savedScroll.current = Array.from(root.querySelectorAll('.models-page, .ledger-wrap')).map(el => [el, el.scrollTop])
  }, [])

  const openPage = useCallback((name) => {
    if (!name) return
    rememberScroll()
    lastOpened.current = name
    navigate(modelPath(name), { state: { from: location.pathname + location.search } })
  }, [navigate, location.pathname, location.search, rememberScroll])

  // On a phone there is no inspector beside the table, so a tap on a row goes
  // straight to the page. Arrow keys still move the selection.
  const rowSelect = useCallback((name, options) => {
    const phone = typeof window !== 'undefined' && window.matchMedia?.('(max-width: 640px)').matches
    if (name && phone && !options?.replace) openPage(name)
    else selectModel(name, options)
  }, [openPage, selectModel])

  useLayoutEffect(() => {
    if (detailOpen) return
    if (savedScroll.current) {
      for (const [el, top] of savedScroll.current) el.scrollTop = top
      savedScroll.current = null
    }
    const name = lastOpened.current
    if (!name) return
    lastOpened.current = null
    const frame = window.requestAnimationFrame(() => {
      hostRef.current?.querySelector(`[data-entity="${CSS.escape(name)}"] [data-row-open]`)?.focus({ preventScroll: true })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [detailOpen])

  useLedgerKeys({
    enabled: !detailOpen,
    searchRef,
    onToggleDensity: () => setDensity(d => (d === 'compact' ? 'comfortable' : 'compact')),
    hasSelection: !!selectedName,
    onClose: () => selectModel(null),
    onOpen: () => openPage(selectedName),
  })

  // The list, and over it, when one is open, a model's page.
  const withDetail = (list) => (
    <>
      <div className="models-host" hidden={detailOpen} ref={hostRef}>{list}</div>
      {detailOpen && (
        <Suspense fallback={null}>
          <Outlet context={{ addToast }} />
        </Suspense>
      )}
    </>
  )

  const headerActions = (
    <div className="view-bar__actions models-bar__actions">
      <DiskStrip disk={disk} open={sheetOpen} onOpen={() => setSheetOpen(true)} />
      <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate('/app/model-editor', { state: fromState(location, t('models')) })}>
        <Icon name="plus" /> {t('actions.addModel')}
      </button>
      <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate('/app/import-model')}>
        <Icon name="upload" /> {t('actions.importModel')}
      </button>
    </div>
  )

  const overlays = (
    <>
      <CleanupSheet
        open={sheetOpen}
        onClose={() => setSheetOpen(false)}
        disk={disk}
        hiddenIds={hiddenIds}
        removalPending={!!removal.pending}
        refreshToken={refreshToken}
        onRemove={items => removal.start(items)}
      />
      {removal.pending && (
        <HomeUndoToast
          key={removal.pending.ids.join('|')}
          testId="removal-undo-toast"
          message={removal.pending.phase === 'removing'
            ? t('cleanup.toasts.removing', { count: removal.pending.ids.length })
            : t('cleanup.toasts.undoMessage', {
              count: removal.pending.ids.length,
              amount: pendingFree > 0 ? gbLabel(pendingFree) : '',
              seconds: Math.round(UNDO_MS / 1000),
            })}
          undoLabel={t('cleanup.undo')}
          dismissible={false}
          duration={UNDO_MS}
          onUndo={removal.undo}
          onExpire={removal.commit}
        />
      )}
    </>
  )

  if (activeView === 'installed') {
    return withDetail(
      <div className="page page--wide page--app models-page">
        <div className="view-bar models-bar">
          <h1 className="view-bar__title">{t('lifecycle.title')}</h1>
          <ModelsTabs activeView={activeView} searchParams={searchParams} installedCount={statsLoaded ? stats.installed : 0} t={t} />
          {headerActions}
        </div>
        <InstalledModels
          addToast={addToast}
          query={urlSearch}
          state={installedState}
          selectedName={selectedName}
          onQueryChange={setInstalledQuery}
          onStateChange={setInstalledState}
          onClearFilters={clearInstalledFilters}
          onSelect={rowSelect}
          onOpen={openPage}
          hiddenIds={hiddenIds}
          refreshToken={refreshToken}
          density={density}
          onDensity={setDensity}
          searchRef={searchRef}
          onOpenCleanup={() => setSheetOpen(true)}
          disk={disk}
        />
        {overlays}
      </div>,
    )
  }

  const contextIndex = CONTEXT_SIZES.indexOf(contextSize)
  const contextLabel = CONTEXT_LABELS[contextIndex]
  const filtersActive = !!(search || filters.length > 0 || backendFilter || fitsFilter || !collapseVariants)
  const firstLoad = loading && !loadedOnce.current
  const galleryEmpty = statsLoaded && stats.total === 0 && !filtersActive && !loadError
  const basis = totalGpuMemory <= 0
    ? null
    : budget.scope === 'cluster'
      ? t('ledger.basis.cluster', { memory: formatBytes(totalGpuMemory), node: budget.nodeName })
      : hasGpu
        ? t('ledger.basis.gpu', { memory: formatBytes(totalGpuMemory) })
        : t('ledger.basis.ram', { memory: formatBytes(totalGpuMemory) })

  return withDetail(
    <div className="page page--wide page--app models-page">
      <div className="view-bar models-bar">
        <h1 className="view-bar__title">{t('lifecycle.title')}</h1>
        <ModelsTabs activeView={activeView} searchParams={searchParams} installedCount={statsLoaded ? stats.installed : 0} t={t} />
        {statsLoaded && (
          <span className="view-bar__count">{t('rail.showingCount', { shown: visibleModels.length, total: stats.total })}</span>
        )}
        {headerActions}
      </div>

      {/* Filters, in two bands.
          1. Query scope: free-text search, the backend select, and the
             refinements that narrow a listing the user is already reading
             (one row per model, fits in GPU). The backend select comes before
             the facets because picking a backend disables the facets that
             backend cannot serve (see isFilterAvailable).
          2. Facets: the capability chips with counts, and the context length
             the VRAM estimate is computed at. The estimate is exactly what the
             fits filter tests against, so the two controls sit together. */}
      <div className="ledger-controls models-filters">
        <div className="ledger-controls__row">
          <div className="dk-input-icon ledger-search" data-testid="models-search-wrap">
            <Icon name="search" className="dk-icon" />
            <input
              ref={searchRef}
              className="dk-input"
              data-testid="models-search"
              type="text"
              placeholder={t('search.placeholder')}
              aria-label={t('search.placeholder')}
              aria-keyshortcuts="/"
              value={search}
              onChange={(e) => handleSearch(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Escape') e.currentTarget.blur() }}
            />
            <kbd className="dk-kbd ledger-search__key" aria-hidden="true">/</kbd>
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
          <div className="models-filters__refine ledger-refine" data-testid="models-filters-refine">
            {/* Leads the band because it decides how many rows the other one
                refines over, and because unlike fits-in-GPU it is always
                present: a host with no GPU still browses builds. Turning it
                off is the only way to page through every build the gallery
                holds; searching reaches a specific one but cannot enumerate
                them. */}
            <label className="filter-bar-group__toggle" data-testid="models-collapse-variants">
              <Toggle
                checked={collapseVariants}
                onChange={(v) => { setCollapseVariants(v); setPage(1) }}
              />
              <span>{t('filters.collapseVariants')}</span>
            </label>
            {totalGpuMemory > 0 && (
              <label className="filter-bar-group__toggle">
                <Toggle checked={fitsFilter} onChange={setFitsFilter} />
                <span>{hasGpu ? t('filters.fitsGpu') : t('filters.fitsMemory')}</span>
              </label>
            )}
          </div>
          <div className="dk-segmented ledger-density" role="group" aria-label={t('ledger.density.label')}>
            <button
              type="button"
              className="dk-seg"
              aria-pressed={density === 'comfortable'}
              aria-label={t('ledger.density.comfortable')}
              title={t('ledger.density.comfortable')}
              data-testid="density-comfortable"
              onClick={() => setDensity('comfortable')}
            >
              <Icon name="list" />
            </button>
            <button
              type="button"
              className="dk-seg"
              aria-pressed={density === 'compact'}
              aria-label={t('ledger.density.compact')}
              title={t('ledger.density.compact')}
              data-testid="density-compact"
              onClick={() => setDensity('compact')}
            >
              <Icon name="equals" />
            </button>
          </div>
        </div>
        <div className="ledger-controls__row ledger-controls__row--facets">
          <FacetBar
            facets={FILTERS}
            active={filters}
            counts={facetCounts}
            stale={facetsStale}
            isAvailable={isFilterAvailable}
            onToggle={toggleFilter}
            t={t}
            ariaLabel={t('filters.useCaseLabel')}
          />
        </div>
      </div>

      {loadError && (
        <div className="ledger-banner ledger-banner--error" role="alert" data-testid="gallery-error">
          <Icon name="alert-circle" />
          <span>
            <strong>{t('ledger.offline.title')}</strong>{' '}
            {models.length > 0 ? t('ledger.offline.stale', { message: loadError }) : t('ledger.offline.empty', { message: loadError })}
          </span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => fetchModels()}>
            <Icon name="refresh" /> {t('ledger.retry')}
          </button>
        </div>
      )}

      {/* The gallery, as a table to scan and an inspector that answers.
          The inspector has two jobs and no third: with nothing selected it is
          the discovery page, and with a model selected it is that model's
          quick look. Below the breakpoint the two cannot both survive, so a
          selected model means the inspector is the page. */}
      <div className={`ledger${selectedModel ? ' ledger--detail' : ''}`} data-testid="discover">
        <div className="ledger__table-col">
          <div className="dk-table-bar ledger-bar">
            {basis && <span className="ledger-bar__basis" data-testid="fit-basis">{basis}</span>}
            <div className="models-filters__context">
              <label htmlFor="models-context-size">{t('filters.contextSize')}</label>
              <div className="dk-range-wrap">
                <input
                  id="models-context-size"
                  className="dk-range"
                  type="range"
                  min={0}
                  max={CONTEXT_SIZES.length - 1}
                  value={contextIndex}
                  style={rangeStyle(contextIndex)}
                  // The slider steps over an index, so the raw value ("2") is
                  // meaningless to a screen reader; announce the size instead.
                  aria-valuetext={contextLabel}
                  onChange={(e) => setContextSize(CONTEXT_SIZES[e.target.value])}
                />
                <output className="dk-range-value models-filters__context-value" htmlFor="models-context-size">{contextLabel}</output>
              </div>
            </div>
          </div>
          {!firstLoad && visibleModels.length === 0 ? (
            loadError ? (
              // The list could not be fetched, which is not the same as an empty
              // gallery. The banner above holds the reason and the way out.
              <div className="dk-empty ledger-empty" data-testid="gallery-unavailable">
                <div className="dk-empty-icon"><Icon name="cloud" /></div>
                <h2 className="dk-empty-title">{t('ledger.offline.emptyTitle')}</h2>
                <p className="dk-empty-text">{t('ledger.offline.emptyText')}</p>
              </div>
            ) : (
            <div className="dk-empty ledger-empty" data-testid="gallery-empty">
              <div className="dk-empty-icon"><Icon name="search" /></div>
              <h2 className="dk-empty-title">{t('empty.title')}</h2>
              <p className="dk-empty-text">
                {filtersActive ? t('empty.withFilters') : t('empty.noFilters')}
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
                <p className="empty-state-hint dk-empty-text">{t('empty.collapsedVariantsHint')}</p>
              )}
              {filtersActive && (
                <button
                  className="dk-btn dk-btn--secondary dk-btn--sm"
                  onClick={() => { handleSearch(''); setFilters([]); setBackendFilter(''); setFitsFilter(false); setCollapseVariants(COLLAPSE_VARIANTS_DEFAULT); setPage(1) }}
                >
                  <Icon name="close" /> {t('search.clearFilters')}
                </button>
              )}
            </div>
            )
          ) : (
            <ExploreTable
              models={visibleModels}
              loading={firstLoad}
              // Grouped while browsing, flat while searching: once a term is
              // typed the buckets stand between the reader and the answer.
              grouped={!search.trim()}
              collapsedGroups={collapsedGroups}
              onToggleGroup={toggleGroup}
              selectedName={selectedName}
              onSelect={rowSelect}
              onOpen={openPage}
              onInstall={handleInstall}
              onRetry={handleRetry}
              estimates={estimates}
              pendingEstimates={pendingEstimates}
              contextSize={contextSize}
              contextLabel={contextLabel}
              budget={budget}
              ramAvailable={ramAvailable}
              isInstalling={isInstalling}
              progressOf={getOperationProgress}
              failedOp={failedOp}
              sort={sort}
              order={order}
              onSort={handleSort}
              density={density}
              t={t}
            />
          )}

          {totalPages > 1 && (
            <div className="pagination split-view__pager ledger-pager">
              <button className="pagination-btn" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} aria-label={t('rail.previousPage')}>
                <Icon name="chevron-left" />
              </button>
              <span className="split-view__pager-label">{page} / {totalPages}</span>
              <button className="pagination-btn" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page === totalPages} aria-label={t('rail.nextPage')}>
                <Icon name="chevron-right" />
              </button>
            </div>
          )}
          <p className="ledger-keys dk-hide-phone" aria-hidden="true">
            <span><kbd className="dk-kbd">/</kbd> {t('ledger.keys.search')}</span>
            <span><kbd className="dk-kbd">&uarr;</kbd><kbd className="dk-kbd">&darr;</kbd> {t('ledger.keys.move')}</span>
            <span><kbd className="dk-kbd">&crarr;</kbd> {t('ledger.keys.install')}</span>
            <span><kbd className="dk-kbd">d</kbd> {t('ledger.keys.density')}</span>
            <span><kbd className="dk-kbd">esc</kbd> {t('ledger.keys.close')}</span>
          </p>
        </div>

        <aside className="ledger__pane" data-testid="discover-pane" aria-label={t('ledger.inspector')}>
          {selectedModel ? (
            <DiscoverDetail
              model={selectedModel}
              estimate={estimates[selectedName]}
              contextSize={contextSize}
              onPickContext={setContextSize}
              budget={budget}
              ramAvailable={ramAvailable}
              disk={disk}
              totalGpuMemory={totalGpuMemory}
              fitsGpu={fitsGpu}
              budgetNode={budget.scope === 'cluster' ? budget.nodeName : ''}
              installing={isInstalling(selectedName)}
              progress={getOperationProgress(selectedName)}
              failed={failedOp(selectedName)}
              onInstall={handleInstall}
              onRetry={handleRetry}
              installedProfile={installedProfiles[selectedName]}
              onOpen={route => navigate(route)}
              onOpenPage={openPage}
              onManage={name => setSearchParams(previous => {
                const next = new URLSearchParams(previous)
                next.set('view', 'installed')
                next.set('model', name)
                return next
              })}
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
                {!statsLoaded && !loadError ? (
                  <span className="dk-skeleton dk-skeleton--title" aria-hidden="true" />
                ) : (
                <h2 className="zero-pane__title">
                  {/* The resources endpoint reports system RAM when there is
                      no accelerator, so calling it "GPU memory" was a claim
                      the data did not support. Without a list there is no
                      count to state, so the line is the machine alone. */}
                  {!statsLoaded
                    ? (totalGpuMemory <= 0
                      ? t('ledger.hostOnly.none')
                      : t(hasGpu ? 'ledger.hostOnly.gpu' : 'ledger.hostOnly.ram', { memory: formatBytes(totalGpuMemory) }))
                    : totalGpuMemory <= 0
                    ? t('shelves.heroNoGpu', { count: stats.total })
                    : budget.scope === 'cluster'
                      // Naming the node is the point: a cluster figure with
                      // no owner reads as this machine's, which is the very
                      // confusion the cluster reading exists to end.
                      ? t(budget.nodeCount > 1 ? 'shelves.heroWithCluster' : 'shelves.heroWithNode', {
                        vram: formatBytes(totalGpuMemory), node: budget.nodeName, nodes: budget.nodeCount, count: stats.total,
                      })
                      : hasGpu
                        ? t('shelves.heroWithGpu', { vram: formatBytes(totalGpuMemory), count: stats.total })
                        : t('shelves.heroWithRam', { ram: formatBytes(totalGpuMemory), count: stats.total })}
                </h2>
                )}
                {galleryEmpty
                  ? <p className="zero-pane__text">{t('ledger.emptyGalleryHint')}</p>
                  : statsLoaded && <p className="zero-pane__text">{t('shelves.heroHint')}</p>}
              </div>

              {/* The hardware-fit strip is the curation, and here it finally
                  gets the width to argue for a model rather than list one.
                  Once a model is installed it narrows to the best fit. */}
              {!galleryEmpty && <RecommendedModels addToast={addToast} installedCount={statsLoaded ? stats.installed : 0} />}

              {/* Somewhere to start when the recommendations are not it.
                  These set the use-case filter rather than fetching a second
                  list, so a shelf costs nothing and cannot go stale. */}
              {!galleryEmpty && statsLoaded && (
              <div className="zero-pane__shelf">
                <div className="zero-pane__shelf-head">
                  <h3 className="zero-pane__shelf-title">{t('shelves.byUseCase')}</h3>
                </div>
                {/* Lanes, not tiles. These are a list of ways in, read in
                    order — a grid of equal cards asks the reader to compare
                    them, which is not the choice being offered. */}
                <ul className="lanes lanes--usecase">
                  {USE_CASE_LANES.map(lane => (
                    <li key={lane.id}>
                      <button
                        type="button"
                        className="lane"
                        onClick={() => { setFilters([lane.pick]); setPage(1) }}
                      >
                        <span className="lane__tag">
                          <Icon name={lane.icon} /> {t(lane.labelKey)}
                        </span>
                        <span className="lane__desc">{t(lane.blurbKey)}</span>
                        <span className="lane__go" aria-hidden="true">→</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
              )}
            </div>
          )}
        </aside>
      </div>
      {overlays}
    </div>,
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
      <th scope="row" className="ledger-detail__label">{label}</th>
      <td className="ledger-detail__value">{children}</td>
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
    <div className={`ledger-detail${nested ? ' ledger-detail--nested' : ''}`}>
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
      <table className="ledger-detail__table">
        <tbody>
          <DetailRow label={t('detail.gallery')}>
            {model.gallery && (
              <span className="dk-badge">
                {typeof model.gallery === 'string' ? model.gallery : model.gallery.name || '—'}
              </span>
            )}
          </DetailRow>
          <DetailRow label={t('detail.backend')}>
            {model.backend && (
              <span className="dk-badge">
                {model.backend}
              </span>
            )}
          </DetailRow>
          <DetailRow label={t('detail.size')}>
            {sizeDisplay && sizeDisplay !== '0 B' ? sizeDisplay : null}
          </DetailRow>
          <DetailRow label={t('detail.vram')}>
            {vramDisplay && vramDisplay !== '0 B' ? (
              <span className="ledger-detail__vram">
                {vramDisplay}
                {fit !== null && (
                  <span className={`ledger-detail__fit ledger-detail__fit--${fit ? 'ok' : 'bad'}`}>
                    <Icon name="cpu" /> {fit ? t('detail.fitsGpu') : t('detail.mayNotFitGpu')}
                  </span>
                )}
              </span>
            ) : null}
          </DetailRow>
          {variantData?.loading && (
            <DetailRow label={t('variants.title')}>
              <span className="ledger-detail__muted">
                <Icon name="spinner" spin className="icon-before" />{t('variants.loading')}
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
                      <Icon name="info" />
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
                            <Icon name="check-circle" /> {t('variants.autoSelected')}
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
                            <Icon name="bolt" /> {variantFeatureLabel(f, t)}
                          </span>
                        ))}
                      </span>
                      <Icon name="download" className="variant-row__action" />
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
                            <Icon name="spinner" spin />
                            <span>{t('variants.detailsLoading')}</span>
                          </div>
                        )}
                        {detail?.error && (
                          // Stated, not blank: an empty panel reads as a
                          // rendering fault rather than as a lookup that
                          // failed.
                          <div className="variant-detail__state variant-detail__state--error" role="status">
                            <Icon name="warning" />
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
              <div className="ledger-detail__tags">
                {model.tags.map(tag => (
                  <span key={tag} className="dk-badge">{tag}</span>
                ))}
              </div>
            )}
          </DetailRow>
          <DetailRow label={t('detail.links')}>
            {model.urls?.length > 0 && (
              <div className="ledger-detail__links">
                {model.urls.map((url, i) => (
                  <a key={i} className="dk-link" href={safeHref(url)} target="_blank" rel="noopener noreferrer">
                    <Icon name="external-link" className="icon-before" />{url}
                  </a>
                ))}
              </div>
            )}
          </DetailRow>
          {model.trustRemoteCode && (
            <DetailRow label={t('detail.warning')}>
              <span className="dk-badge dk-badge--error">
                <Icon name="alert-circle" /> {t('detail.requiresTrustRemoteCode')}
              </span>
            </DetailRow>
          )}
          {files.length > 0 && (
            <DetailRow label={t('detail.files')}>
              <div>
                <button
                  className="dk-btn dk-btn--secondary dk-btn--sm"
                  aria-expanded={expandedFiles}
                  onClick={(e) => { e.stopPropagation(); setExpandedFiles(!expandedFiles) }}
                >
                  <Icon name={`chevron-${expandedFiles ? 'down' : 'right'}`} />
                  {t('detail.fileCount', { count: files.length })}
                </button>
                {expandedFiles && (
                  <div className="dk-table-wrap ledger-files">
                    <table className="dk-table dk-table--compact">
                      <thead>
                        <tr>
                          <th scope="col">{t('detail.filename')}</th>
                          <th scope="col">{t('detail.uri')}</th>
                          <th scope="col">{t('detail.sha256')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {files.map((f, i) => (
                          <tr key={i}>
                            <td className="dk-table-id">{f.filename || '—'}</td>
                            <td className="ledger-files__uri">{f.uri || '—'}</td>
                            <td className="dk-table-id">
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

// VramByContext plots the estimate the fits filter actually tests against, at
// every context length the page asks the server for.
//
// It exists because a single number answers the wrong question. "Needs 6.2 GB"
// invites "so will it run?", and the honest answer is usually "yes, up to a
// 32k context" - which is a shape, not a number. The limit line is what makes
// the bars mean anything, so a host with no GPU gets no chart at all rather
// than a chart with nothing to compare against.
function VramByContext({ estimate, contextSize, onPickContext, totalGpuMemory, hasGpu = true, t }) {
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
      <span className="discover__chart-title">{t(hasGpu ? 'chart.title' : 'chart.titleRam')}</span>
      <div className="discover__chart-plot">
        <div
          className="discover__chart-limit"
          style={{ '--discover-limit': `${(limit / max) * track}px` }}
        />
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
      {/* The dashed line's name sits under the plot, not on it, where it would
          cover the bars it is meant to be read against. */}
      <span className="discover__chart-limit-label">{t('chart.available', { vram: formatBytes(totalGpuMemory) })}</span>
      <p className={`discover__chart-verdict discover__chart-verdict--${verdictClass}`}>
        <Icon name="cpu" /> {verdict}
      </p>
    </div>
  )
}

// FitSummary says in a sentence what the memory bars below it show. The words
// carry the verdict; the bars and their colour repeat it.
function FitSummary({ fit, hasGpu, ramAvailable, contextLabel, budgetNode, t }) {
  if (!fit) return null
  const gb = bytes => gbLabel(bytes)
  const sentence = fit.state === 'fits'
    ? t('ledger.summary.fits', { need: gb(fit.need), free: gb(fit.amount), context: contextLabel })
    : fit.state === 'spill'
      ? t('ledger.summary.spill', { need: gb(fit.need), cpu: gb(fit.amount), context: contextLabel })
      : hasGpu
        ? t('ledger.summary.overGpu', { need: gb(fit.need), over: gb(fit.amount), context: contextLabel })
        : t('ledger.summary.overRam', { need: gb(fit.need), over: gb(fit.amount), context: contextLabel })
  const gpuUsed = Math.min(fit.need, fit.limit)
  const cpuPart = fit.state === 'fits' ? 0 : Math.min(fit.need - fit.limit, ramAvailable || 0)
  const where = budgetNode ? t('ledger.summary.onNode', { node: budgetNode }) : ''
  return (
    <section className="ledger-fitblock" data-fit={fit.state} data-testid="fit-summary" aria-labelledby="fit-summary-h">
      <h3 className="detail-pane__label" id="fit-summary-h">{t('ledger.summary.title')}{where}</h3>
      <p className="ledger-fitblock__words">
        <Icon name={fit.state === 'fits' ? 'check-circle' : fit.state === 'spill' ? 'alert-circle' : 'close-circle'} />
        <span>{sentence}</span>
      </p>
      <div className="ledger-fitblock__rows">
        <div className="ledger-fitblock__row">
          <span className="ledger-fitblock__dev">{hasGpu ? t('ledger.summary.gpu') : t('ledger.summary.ram')}</span>
          <span className="ledger-fit__bar" aria-hidden="true"><span className="ledger-fit__fill" style={fitStyle(gpuUsed / fit.total)} /></span>
          <span className="ledger-fitblock__fig">{gbNumber(gpuUsed)} / {gbNumber(fit.total)} GB</span>
        </div>
        {hasGpu && ramAvailable > 0 && fit.state !== 'fits' && (
          <div className="ledger-fitblock__row">
            <span className="ledger-fitblock__dev">{t('ledger.summary.ram')}</span>
            <span className="ledger-fit__bar" aria-hidden="true"><span className="ledger-fit__fill" style={fitStyle(cpuPart / ramAvailable)} /></span>
            <span className="ledger-fitblock__fig">{gbNumber(cpuPart)} / {gbNumber(ramAvailable)} GB</span>
          </div>
        )}
      </div>
    </section>
  )
}

// DiscoverDetail is the inspector with a model selected. It owns the part a
// table row could not hold - the headline numbers, the fit summary, the VRAM
// curve and the actions - and hands the rest to ModelDetail, which already
// knows how to render an entry's fields and is shared with the per-variant
// panel.
function DiscoverDetail({
  model, estimate, contextSize, onPickContext, budget, ramAvailable, disk, totalGpuMemory, fitsGpu, budgetNode,
  installing, progress, failed, onInstall, onRetry, installedProfile, onOpen, onOpenPage, onManage, onBack,
  expandedFiles, setExpandedFiles, variantData, variantDetails, onLoadVariantDetail, t,
}) {
  const name = model.name || model.id
  const sizeDisplay = estimate?.sizeDisplay
  const vramBytes = estimate?.estimates?.[String(contextSize)]?.vramBytes
  const fit = fitsGpu(vramBytes)
  const ledgerFit = fitFor(vramBytes, budget, ramAvailable)
  const contextLabel = CONTEXT_LABELS[CONTEXT_SIZES.indexOf(contextSize)]
  const headroom = totalGpuMemory > 0 && vramBytes ? totalGpuMemory * 0.95 - vramBytes : null
  const openUseCase = modelUseCases(installedProfile).find(useCase => useCase.route)
  const leaves = leavesFree(disk, estimate?.sizeBytes)
  const canInstall = !installing && !model.installed

  return (
    <ModelLifecycleDetailShell
      testId="discover"
      icon={groupForEntity(model).icon}
      name={name}
      lede={model.description ? stripMarkdown(model.description).slice(0, 220) : null}
      ledeTitle={model.description ? stripMarkdown(model.description) : null}
      onBack={onBack}
      backLabel={t('ledger.closeInspector')}
      closeIcon
      warning={model.trustRemoteCode ? t('detail.requiresTrustRemoteCode') : null}
      error={failed ? t('ledger.failedInstall', { message: failed.error }) : null}
      actions={(
        <>
          {installing ? (
            <div className="inline-install">
              <div className="inline-install__row">
                <div className="operation-spinner" />
                <span className="inline-install__label">
                  {progress > 0 ? t('table.installingPct', { percent: Math.round(progress) }) : `${t('table.installing')}...`}
                </span>
              </div>
              {progress > 0 && (
                <div className="ledger-progress ledger-progress--wide" aria-hidden="true">
                  <span className="ledger-progress__bar" style={fitStyle(progress / 100)} />
                </div>
              )}
            </div>
          ) : model.installed ? (
            <>
              {openUseCase && (
                <button className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => onOpen(openUseCase.route(name))}>
                  <Icon name="external-link" />
                  {t('lifecycle.actions.open', { useCase: t(`lifecycle.open.${openUseCase.labelKey}`) })}
                </button>
              )}
              <button className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onManage(name)}>
                <Icon name="sliders" /> {t('lifecycle.actions.manageInstallation')}
              </button>
            </>
          ) : failed ? (
            <button className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => onRetry(name, failed)} data-testid="discover-install">
              <Icon name="refresh" /> {t('ledger.retry')}
            </button>
          ) : (
            <button className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => onInstall(name)} data-testid="discover-install">
              <Icon name="download" /> {t('actions.install')}
            </button>
          )}
          <button
            type="button"
            className="dk-btn dk-btn--ghost dk-btn--sm"
            onClick={() => onOpenPage(name)}
            data-testid="inspector-open-page"
            aria-keyshortcuts="o"
          >
            <Icon name="arrow-right" /> {t('page.openDetails')}
          </button>
        </>
      )}
      stats={[
        { label: t('detail.size'), value: sizeDisplay && sizeDisplay !== '0 B' ? sizeDisplay : '—' },
        { label: t(budget.hasGpu ? 'detail.vramAt' : 'detail.memoryAt', { context: contextLabel }), value: vramBytes ? formatBytes(vramBytes) : '—' },
        {
          // Headroom is headroom somewhere. On a distributed controller that
          // somewhere is a worker, and an unqualified figure reads as this
          // machine's, which is the confusion the cluster reading exists to
          // end.
          label: budgetNode ? t('detail.headroomOn', { node: budgetNode }) : t('detail.headroom'),
          value: headroom === null ? '—' : (headroom < 0 ? '−' : '') + formatBytes(Math.abs(headroom)),
          tone: headroom === null ? undefined : headroom < 0 ? 'bad' : 'ok',
        },
      ]}
    >
      {canInstall && leaves !== null && (
        <p className={`ledger-leaves${leaves < 0 ? ' ledger-leaves--short' : ''}`} data-testid="leaves-free">
          {leaves < 0
            ? t('disk.notEnough', { amount: gbLabel(-leaves), size: gbLabel(estimate.sizeBytes) })
            : t('disk.leaves', { size: gbLabel(estimate.sizeBytes), amount: gbLabel(leaves) })}
        </p>
      )}

      <FitSummary
        fit={ledgerFit}
        hasGpu={budget.hasGpu}
        ramAvailable={ramAvailable}
        contextLabel={contextLabel}
        budgetNode={budgetNode}
        t={t}
      />

      <VramByContext
        estimate={estimate}
        contextSize={contextSize}
        onPickContext={onPickContext}
        totalGpuMemory={totalGpuMemory}
        hasGpu={budget.hasGpu}
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
    </ModelLifecycleDetailShell>
  )
}
