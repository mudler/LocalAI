import { useState, useCallback, useEffect, useRef } from 'react'
import { useNavigate, useOutletContext, useLocation } from 'react-router-dom'
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
import ResponsiveTable from '../components/ResponsiveTable'
import RecommendedModels from '../components/RecommendedModels'
import Popover from '../components/Popover'
import { formatBytes } from '../utils/format'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import React from 'react'


const CONTEXT_SIZES = [8192, 16384, 32768, 65536, 131072, 262144]
const CONTEXT_LABELS = ['8K', '16K', '32K', '64K', '128K', '256K']
const FITS_FILTER_STORAGE_KEY = 'localai-models-fits-filter'

const FILTERS = [
  { key: '', labelKey: 'filters.all', icon: 'fa-layer-group' },
  { key: 'chat', labelKey: 'filters.llm', icon: 'fa-brain' },
  { key: 'image', labelKey: 'filters.image', icon: 'fa-image' },
  { key: 'video', labelKey: 'filters.video', icon: 'fa-video' },
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
  const [expandedRow, setExpandedRow] = useState(null)
  const [expandedFiles, setExpandedFiles] = useState(false)
  const [stats, setStats] = useState({ total: 0, installed: 0, repositories: 0 })
  // Distinguishes "nothing installed" from "not asked yet". The recommendations
  // panel defaults off the installed count, so it must not read the initial 0.
  const [statsLoaded, setStatsLoaded] = useState(false)
  const [backendFilter, setBackendFilter] = useState('')
  const [allBackends, setAllBackends] = useState([])
  const [backendUsecases, setBackendUsecases] = useState({})
  const [estimates, setEstimates] = useState({})
  const [contextSize, setContextSize] = useState(CONTEXT_SIZES[0])
  const [confirmDialog, setConfirmDialog] = useState(null)
  // Index of the row whose variant split-menu is open, or null. A single
  // Popover is re-anchored per row rather than one instance per row.
  const [variantMenuFor, setVariantMenuFor] = useState(null)
  const variantMenuAnchorRef = useRef(null)
  // Variant descriptions, keyed by model name. The listing only tells us
  // whether an entry declares any; describing them costs the server a network
  // probe per variant, so we ask for one entry at a time and keep the answer
  // for the rest of the page session.
  const [variantData, setVariantData] = useState({})
  const [fitsFilter, setFitsFilter] = useState(() => {
    try {
      return localStorage.getItem(FITS_FILTER_STORAGE_KEY) === '1'
    } catch {
      return false
    }
  })
  // Total GPU memory for "fits" check
  const totalGpuMemory = resources?.aggregate?.total_memory || 0

  const fetchModels = useCallback(async (params = {}) => {
    try {
      setLoading(true)
      const searchVal = params.search !== undefined ? params.search : search
      const filtersVal = params.filters !== undefined ? params.filters : filters
      const sortVal = params.sort !== undefined ? params.sort : sort
      const backendVal = params.backendFilter !== undefined ? params.backendFilter : backendFilter
      const queryParams = {
        page: params.page || page,
        items: 9,
        // The deduplicated gallery is what a user asking "what can I install"
        // wants, so the UI always asks for it. There is no control for this
        // because a search term already bypasses the collapse server-side, so
        // a build hidden behind a parent is still findable by name. The
        // parameter stays optional on the API: other clients want either view,
        // and an absent parameter still returns the full listing.
        collapse_variants: 'true',
      }
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
      setLoading(false)
    }
  }, [page, search, filters, sort, order, backendFilter, addToast, t])

  useEffect(() => {
    fetchModels()
  }, [page, filters, sort, order, backendFilter])

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

  // Fetch VRAM/size estimates asynchronously for visible models.
  useEffect(() => {
    if (models.length === 0) return
    let cancelled = false
    models.forEach(model => {
      const id = model.name || model.id
      if (estimates[id]) return
      modelsApi.estimate(id, CONTEXT_SIZES).then(est => {
        if (cancelled) return
        if (est && (est.sizeBytes || est.estimates)) {
          setEstimates(prev => ({ ...prev, [id]: est }))
        }
      }).catch(() => {})
    })
    return () => { cancelled = true }
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

  const visibleModels = models.filter((model) => {
    if (!fitsFilter) return true
    const name = model.name || model.id
    const vramBytes = estimates[name]?.estimates?.[String(contextSize)]?.vramBytes
    const fit = fitsGpu(vramBytes)
    // Keep models visible while estimate is still loading; hide only explicit non-fits.
    return fit !== false
  })

  return (
    <div className="page page--wide">
      <PageHeader
        title={t('title')}
        supporting={t('subtitle')}
        actions={
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center' }}>
            <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.8125rem' }}>
              <div style={{ textAlign: 'center' }}>
                <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-primary)' }}>{stats.total}</div>
                <div style={{ color: 'var(--color-text-muted)' }}>{t('stats.available')}</div>
              </div>
              <div style={{ textAlign: 'center' }}>
                <a onClick={() => navigate('/app/manage')} style={{ cursor: 'pointer' }}>
                  <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-success)' }}>{stats.installed}</div>
                  <div style={{ color: 'var(--color-text-muted)' }}>{t('stats.installed')}</div>
                </a>
              </div>
            </div>
            <button className="btn btn-primary btn-sm" onClick={() => navigate('/app/model-editor', { state: fromState(location, t('models')) })}>
              <i className="fas fa-plus" /> {t('actions.addModel')}
            </button>
            <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> {t('actions.importModel')}
            </button>
          </div>
        }
      />

      <RecommendedModels addToast={addToast} installedCount={statsLoaded ? stats.installed : null} />

      {/* Filters, in three deliberate bands.
          1. Query scope: free-text search plus the backend select. The backend
             select leads the taxonomy row rather than trailing it because
             picking a backend disables the use-cases that backend cannot serve
             (see isFilterAvailable), so it reads as the gate on what follows.
          2. Taxonomy: the use-case chips, which wrap freely.
          3. Refinements: fits-in-GPU and context size. These two are one
             control group, not two strays - the context size is the length the
             VRAM estimate is computed at, and that estimate is exactly what the
             fits filter tests against.
          Each band owns its container, so how many chips happen to wrap at a
          given width can no longer decide where the other controls land. */}
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

        <div className="filter-bar" role="group" aria-label={t('filters.useCaseLabel')}>
          {FILTERS.map(f => {
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
                <i className={`fas ${f.icon}`} aria-hidden="true" style={{ marginRight: 4 }} />
                {t(f.labelKey)}
              </button>
            )
          })}
        </div>

        <div className="models-filters__refine" data-testid="models-filters-refine">
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

      {/* Table */}
      {loading ? (
        <GalleryLoader />
      ) : visibleModels.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-search" /></div>
          <h2 className="empty-state-title">{t('empty.title')}</h2>
          <p className="empty-state-text">
            {search || filters.length > 0 || backendFilter || fitsFilter ? t('empty.withFilters') : t('empty.noFilters')}
          </p>
          {(search || filters.length > 0 || backendFilter || fitsFilter) && (
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => { handleSearch(''); setFilters([]); setBackendFilter(''); setFitsFilter(false); setPage(1) }}
            >
              <i className="fas fa-times" /> {t('search.clearFilters')}
            </button>
          )}
        </div>
      ) : (
        <ResponsiveTable containerStyle={{ background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-lg)', overflow: 'hidden' }} style={{ minWidth: '800px' }}>
              <thead>
                <tr>
                  <th style={{ width: '30px' }}></th>
                  <th style={{ width: '60px' }}></th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('name')}>
                    {t('table.modelName')} {sort === 'name' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th>{t('table.description')}</th>
                  <th>{t('table.backend')}</th>
                  <th>{t('table.sizeVram')}</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('status')}>
                    {t('table.status')} {sort === 'status' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th style={{ textAlign: 'right' }}>{t('table.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {visibleModels.map((model, idx) => {
                  const name = model.name || model.id
                  const estData = estimates[name]
                  const sizeDisplay = estData?.sizeDisplay
                  const ctxEst = estData?.estimates?.[String(contextSize)]
                  const vramDisplay = ctxEst?.vramDisplay
                  const vramBytes = ctxEst?.vramBytes
                  const installing = isInstalling(name)
                  const progress = getOperationProgress(name)
                  const fit = fitsGpu(vramBytes)
                  const isExpanded = expandedRow === idx
                  const hasVariants = !!model.has_variants

                  return (
                    <React.Fragment key={name}>
                    <tr
                      onClick={() => {
                        if (!isExpanded && hasVariants) loadVariants(name)
                        setExpandedRow(isExpanded ? null : idx)
                        setExpandedFiles(false)
                      }}
                      style={{ cursor: 'pointer' }}
                    >
                      {/* Chevron */}
                      <td style={{ width: 30 }}>
                        <i className={`fas fa-chevron-${isExpanded ? 'down' : 'right'}`} style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)', transition: 'transform 150ms' }} />
                      </td>
                      {/* Icon */}
                      <td>
                        <div style={{
                          width: 48, height: 48, borderRadius: 'var(--radius-md)',
                          border: '1px solid var(--color-border-subtle)',
                          display: 'flex', alignItems: 'center', justifyContent: 'center',
                          background: 'var(--color-bg-primary)', overflow: 'hidden',
                        }}>
                          {model.icon ? (
                            <img src={model.icon} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} loading="lazy" />
                          ) : (
                            <i className="fas fa-brain" style={{ fontSize: '1.25rem', color: 'var(--color-accent)' }} />
                          )}
                        </div>
                      </td>

                      {/* Name */}
                      <td>
                        <div>
                          <span style={{ fontSize: '0.875rem', fontWeight: 600 }}>{name}</span>
                          {model.trustRemoteCode && (
                            <div style={{ marginTop: '2px' }}>
                              <span className="badge badge-error" style={{ fontSize: '0.625rem' }}>
                                <i className="fas fa-circle-exclamation" /> {t('table.trustRemoteCode')}
                              </span>
                            </div>
                          )}
                        </div>
                      </td>

                      {/* Description */}
                      <td>
                        {(() => {
                          // Gallery descriptions are Markdown. This cell is a single
                          // truncated line, so it gets the text without the syntax.
                          const desc = stripMarkdown(model.description)
                          return (
                            <div style={{
                              fontSize: '0.8125rem', color: 'var(--color-text-secondary)',
                              maxWidth: '200px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                            }} title={desc}>
                              {desc || '—'}
                            </div>
                          )
                        })()}
                      </td>

                      {/* Backend */}
                      <td>
                        {model.backend ? (
                          <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                            {model.backend}
                          </span>
                        ) : (
                          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>—</span>
                        )}
                      </td>

                      {/* Size / VRAM */}
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                          {(sizeDisplay || vramDisplay) ? (
                            <>
                              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>
                                {sizeDisplay && sizeDisplay !== '0 B' && (
                                  <span>{t('table.size', { size: sizeDisplay })}</span>
                                )}
                                {sizeDisplay && sizeDisplay !== '0 B' && vramDisplay && vramDisplay !== '0 B' && ' · '}
                                {vramDisplay && vramDisplay !== '0 B' && (
                                  <span>{t('table.vram', { vram: vramDisplay })}</span>
                                )}
                              </span>
                              {fit !== null && (
                                <span style={{ fontSize: '0.6875rem', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                                  <i className="fas fa-microchip" style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }} />
                                  <span style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
                                    {fit ? t('table.fits') : t('table.mayNotFit')}
                                  </span>
                                </span>
                              )}
                            </>
                          ) : (
                            <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>—</span>
                          )}
                        </div>
                      </td>

                      {/* Status */}
                      <td>
                        {installing ? (
                          <div className="inline-install">
                            <div className="inline-install__row">
                              <div className="operation-spinner" />
                              <span className="inline-install__label">
                                {progress > 0 ? t('table.installingPct', { percent: Math.round(progress) }) : `${t('table.installing')}...`}
                              </span>
                            </div>
                            {progress > 0 && (
                              <div className="operation-bar-container" style={{ flex: 'none', width: '120px', marginTop: 4 }}>
                                <div className="operation-bar" style={{ width: `${progress}%` }} />
                              </div>
                            )}
                          </div>
                        ) : model.installed ? (
                          <span className="badge badge-success">
                            <i className="fas fa-check-circle" /> {t('table.installed')}
                          </span>
                        ) : (
                          <span className="badge" style={{ background: 'var(--color-surface-sunken)', color: 'var(--color-text-muted)', border: '1px solid var(--color-border-default)' }}>
                            <i className="fas fa-circle" /> {t('table.notInstalled')}
                          </span>
                        )}
                      </td>

                      {/* Actions */}
                      <td>
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }} onClick={e => e.stopPropagation()}>
                          {model.installed ? (
                            <>
                              <button className="btn btn-secondary btn-sm" onClick={() => handleInstall(name)} title={t('actions.reinstall')}>
                                <i className="fas fa-rotate" />
                              </button>
                              <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name)} title={t('actions.delete')}>
                                <i className="fas fa-trash" />
                              </button>
                            </>
                          ) : hasVariants ? (
                            // Split button: the primary keeps installing the
                            // auto-selected build, so the default path is
                            // unchanged. The chevron is the deliberate
                            // override.
                            <div style={{ display: 'inline-flex' }}>
                              <button
                                className="btn btn-primary btn-sm"
                                onClick={() => handleInstall(name)}
                                disabled={installing}
                                title={t('actions.install')}
                                style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
                              >
                                <i className="fas fa-download" />
                              </button>
                              <button
                                ref={variantMenuFor === idx ? variantMenuAnchorRef : undefined}
                                className="btn btn-primary btn-sm"
                                onClick={() => {
                                  if (variantMenuFor !== idx) loadVariants(name)
                                  setVariantMenuFor(variantMenuFor === idx ? null : idx)
                                }}
                                aria-haspopup="menu"
                                aria-expanded={variantMenuFor === idx}
                                aria-label={t('variants.chooseVariant')}
                                disabled={installing}
                                style={{ padding: '0 8px', borderLeft: '1px solid rgba(0,0,0,0.15)', borderTopLeftRadius: 0, borderBottomLeftRadius: 0 }}
                              >
                                <i className={`fas fa-chevron-${variantMenuFor === idx ? 'up' : 'down'}`} style={{ fontSize: '0.6875rem' }} />
                              </button>
                            </div>
                          ) : (
                            <button
                              className="btn btn-primary btn-sm"
                              onClick={() => handleInstall(name)}
                              disabled={installing}
                              title={t('actions.install')}
                            >
                              <i className="fas fa-download" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                    {/* Expanded detail row */}
                    {isExpanded && (
                      <tr>
                        <td colSpan="8" style={{ padding: 0 }}>
                          <ModelDetail model={model} fit={fit} sizeDisplay={sizeDisplay} vramDisplay={vramDisplay} expandedFiles={expandedFiles} setExpandedFiles={setExpandedFiles} variantData={hasVariants ? variantData[name] : null} installing={installing} onInstall={handleInstall} t={t} />
                        </td>
                      </tr>
                    )}
                    </React.Fragment>
                  )
                })}
              </tbody>
        </ResponsiveTable>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="pagination">
          <button className="pagination-btn" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1}>
            <i className="fas fa-chevron-left" />
          </button>
          <span style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', padding: '0 var(--spacing-sm)' }}>
            {page} / {totalPages}
          </span>
          <button className="pagination-btn" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page === totalPages}>
            <i className="fas fa-chevron-right" />
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

      <VariantMenu
        anchor={variantMenuAnchorRef}
        model={variantMenuFor !== null ? visibleModels[variantMenuFor] : null}
        variantData={variantData}
        onClose={() => setVariantMenuFor(null)}
        onChoose={handleInstall}
        t={t}
      />
    </div>
  )
}

// VariantMenu is the split-button dropdown. It is one instance re-anchored to
// whichever row is active, so Escape, outside-click and focus return come from
// Popover rather than being reimplemented per row.
function VariantMenu({ anchor, model, variantData, onClose, onChoose, t }) {
  const name = model ? (model.name || model.id) : null
  const data = name ? variantData[name] : null
  return (
    <Popover
      anchor={anchor}
      open={!!model}
      onClose={onClose}
      ariaLabel={t('variants.chooseVariant')}
    >
      <div className="action-menu">
        {data?.loading && (
          // The description is a round trip, so the menu says so rather than
          // opening empty and looking broken.
          <div className="action-menu__item" style={{ color: 'var(--color-text-muted)', cursor: 'default' }}>
            <i className="fas fa-spinner fa-spin action-menu__icon" />
            <span>{t('variants.loading')}</span>
          </div>
        )}
        {(data?.variants || []).map(v => {
          const isAuto = v.model === data?.auto_selected
          return (
            <button
              key={v.model}
              type="button"
              className="action-menu__item"
              onClick={() => {
                onClose()
                if (name) onChoose(name, v.model)
              }}
              // A variant that does not fit stays selectable: an explicit
              // choice is an override the server honours with a warning.
              style={{ opacity: v.fits ? 1 : 0.6 }}
            >
              <i className={`fas ${isAuto ? 'fa-circle-check' : 'fa-download'} action-menu__icon`} />
              <span style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 2 }}>
                <span>
                  {v.model}
                  {isAuto && <span className="badge badge-success" style={{ fontSize: '0.625rem', marginLeft: 6 }}>{t('variants.auto')}</span>}
                  {v.is_base && <span className="badge badge-info" style={{ fontSize: '0.625rem', marginLeft: 6 }}>{t('variants.base')}</span>}
                  {!v.fits && <span className="badge badge-warning" style={{ fontSize: '0.625rem', marginLeft: 6 }}>{t('variants.doesNotFit')}</span>}
                </span>
                <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                  {v.backend || t('variants.unknownBackend')} · {variantSizeLabel(v, t)}
                </span>
              </span>
            </button>
          )
        })}
      </div>
    </Popover>
  )
}

// variantSizeLabel renders a variant footprint. memory_bytes is omitempty on
// the wire, so an absent key means the probe could not determine a size; it
// must never render as "0 B", which would read as "needs nothing".
function variantSizeLabel(variant, t) {
  return variant?.memory_bytes ? formatBytes(variant.memory_bytes) : t('variants.unknownSize')
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

function ModelDetail({ model, fit, sizeDisplay, vramDisplay, expandedFiles, setExpandedFiles, variantData, installing, onInstall, t }) {
  const files = model.additionalFiles || model.files || []
  const name = model.name || model.id
  return (
    <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)', background: 'var(--color-bg-primary)', borderTop: '1px solid var(--color-border-subtle)' }}>
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
                  return (
                    // Listing the alternatives without offering them made the
                    // detail view read as a menu that could not be ordered
                    // from; installing one is the same call the split-button
                    // chevron already makes.
                    <button
                      key={v.model}
                      type="button"
                      className={`variant-row${v.fits ? '' : ' variant-row--unfit'}`}
                      disabled={installing}
                      aria-label={t('variants.installVariant', { variant: v.model })}
                      onClick={(e) => { e.stopPropagation(); onInstall(name, v.model) }}
                    >
                      <span className="variant-row__name">{v.model}</span>
                      <span className="variant-row__backend">{v.backend || t('variants.unknownBackend')}</span>
                      <span className="variant-row__size">{variantSizeLabel(v, t)}</span>
                      <span className="variant-row__status">
                        {isAuto && (
                          <span className="badge badge-success">
                            <i className="fas fa-circle-check" /> {t('variants.autoSelected')}
                          </span>
                        )}
                        {!v.fits && <span className="badge badge-warning">{t('variants.doesNotFit')}</span>}
                        {v.is_base && !isAuto && <span className="badge badge-info">{t('variants.base')}</span>}
                      </span>
                      <i className="fas fa-download variant-row__action" aria-hidden="true" />
                    </button>
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
