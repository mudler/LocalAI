import { useState, useCallback, useEffect, useRef } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import SearchableSelect from '../components/SearchableSelect'
import ConfirmDialog from '../components/ConfirmDialog'
import React from 'react'


const LOADING_PHRASES = [
  { text: 'Loading models...', icon: 'fa-brain' },
  { text: 'Fetching gallery...', icon: 'fa-download' },
  { text: 'Checking availability...', icon: 'fa-circle-check' },
  { text: 'Almost ready...', icon: 'fa-hourglass-half' },
  { text: 'Preparing gallery...', icon: 'fa-store' },
]

function GalleryLoader() {
  const [idx, setIdx] = useState(() => Math.floor(Math.random() * LOADING_PHRASES.length))
  const [fade, setFade] = useState(true)

  useEffect(() => {
    const interval = setInterval(() => {
      setFade(false)
      setTimeout(() => {
        setIdx(prev => (prev + 1) % LOADING_PHRASES.length)
        setFade(true)
      }, 300)
    }, 2800)
    return () => clearInterval(interval)
  }, [])

  const phrase = LOADING_PHRASES[idx]

  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center',
      justifyContent: 'center', padding: 'var(--spacing-xl) var(--spacing-md)',
      minHeight: '280px', gap: 'var(--spacing-lg)',
    }}>
      {/* Animated dots */}
      <div style={{ display: 'flex', gap: '8px' }}>
        {[0, 1, 2, 3, 4].map(i => (
          <div key={i} style={{
            width: 10, height: 10, borderRadius: '50%',
            background: 'var(--color-primary)',
            animation: `galleryDot 1.4s ease-in-out ${i * 0.15}s infinite`,
          }} />
        ))}
      </div>
      {/* Rotating phrase */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
        opacity: fade ? 1 : 0,
        transition: 'opacity 300ms ease',
        color: 'var(--color-text-secondary)',
        fontSize: '0.9375rem',
        fontWeight: 500,
      }}>
        <i className={`fas ${phrase.icon}`} style={{ color: 'var(--color-accent)', fontSize: '1.125rem' }} />
        {phrase.text}
      </div>
      {/* Skeleton rows */}
      <div style={{ width: '100%', maxWidth: '700px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
        {[0.9, 0.7, 0.5].map((opacity, i) => (
          <div key={i} style={{
            height: '48px', borderRadius: 'var(--radius-md)',
            background: 'var(--color-bg-tertiary)', opacity,
            animation: `galleryShimmer 1.8s ease-in-out ${i * 0.2}s infinite`,
          }} />
        ))}
      </div>
      <style>{`
        @keyframes galleryDot {
          0%, 80%, 100% { transform: scale(0.4); opacity: 0.3; }
          40% { transform: scale(1); opacity: 1; }
        }
        @keyframes galleryShimmer {
          0%, 100% { opacity: var(--shimmer-base, 0.15); }
          50% { opacity: var(--shimmer-peak, 0.3); }
        }
      `}</style>
    </div>
  )
}


const CONTEXT_SIZES = [8192, 16384, 32768, 65536, 131072, 262144]
const CONTEXT_LABELS = ['8K', '16K', '32K', '64K', '128K', '256K']

const FILTERS = [
  { key: '', label: 'All', icon: 'fa-layer-group' },
  { key: 'chat', label: 'Chat', icon: 'fa-brain' },
  { key: 'image', label: 'Image', icon: 'fa-image' },
  { key: 'multimodal', label: 'Multimodal', icon: 'fa-shapes' },
  { key: 'vision', label: 'Vision', icon: 'fa-eye' },
  { key: 'tts', label: 'TTS', icon: 'fa-microphone' },
  { key: 'transcript', label: 'STT', icon: 'fa-headphones' },
  { key: 'embeddings', label: 'Embeddings', icon: 'fa-vector-square' },
  { key: 'rerank', label: 'Rerank', icon: 'fa-sort' },
]

export default function Models() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
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
  const [backendFilter, setBackendFilter] = useState('')
  const [allBackends, setAllBackends] = useState([])
  const [backendUsecases, setBackendUsecases] = useState({})
  const [estimates, setEstimates] = useState({})
  const [contextSize, setContextSize] = useState(CONTEXT_SIZES[0])
  const debounceRef = useRef(null)
  const [confirmDialog, setConfirmDialog] = useState(null)

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
      setAllBackends(data?.allBackends || [])
    } catch (err) {
      addToast(`Failed to load models: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [page, search, filters, sort, order, backendFilter, addToast])

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
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      setPage(1)
      fetchModels({ search: value, page: 1 })
    }, 500)
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

  const handleInstall = async (modelId) => {
    try {
      setInstalling(prev => new Map(prev).set(modelId, Date.now()))
      await modelsApi.install(modelId)
    } catch (err) {
      addToast(`Failed to install: ${err.message}`, 'error')
    }
  }

  const handleDelete = (modelId) => {
    setConfirmDialog({
      title: 'Delete Model',
      message: `Delete model ${modelId}?`,
      confirmLabel: `Delete ${modelId}`,
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await modelsApi.delete(modelId)
          addToast(`Deleting ${modelId}...`, 'info')
          fetchModels()
        } catch (err) {
          addToast(`Failed to delete: ${err.message}`, 'error')
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

  return (
    <div className="page">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">Model Gallery</h1>
          <p className="page-subtitle">Discover and install AI models for your workflows</p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-md)', alignItems: 'center' }}>
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.8125rem' }}>
            <div style={{ textAlign: 'center' }}>
              <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-primary)' }}>{stats.total}</div>
              <div style={{ color: 'var(--color-text-muted)' }}>Available</div>
            </div>
            <div style={{ textAlign: 'center' }}>
              <a onClick={() => navigate('/app/manage')} style={{ cursor: 'pointer' }}>
                <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-success)' }}>{stats.installed}</div>
                <div style={{ color: 'var(--color-text-muted)' }}>Installed</div>
              </a>
            </div>
          </div>
          <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
            <i className="fas fa-upload" /> Import Model
          </button>
        </div>
      </div>

      {/* Search */}
      <div className="search-bar" style={{ marginBottom: 'var(--spacing-md)' }}>
        <i className="fas fa-search search-icon" />
        <input
          className="input"
          type="text"
          placeholder="Search models..."
          value={search}
          onChange={(e) => handleSearch(e.target.value)}
        />
      </div>

      {/* Filter buttons */}
      <div className="filter-bar">
        {FILTERS.map(f => {
          const isAll = f.key === ''
          const active = isAll ? filters.length === 0 : filters.includes(f.key)
          const available = isFilterAvailable(f.key)
          return (
            <button
              key={f.key}
              className={`filter-btn ${active ? 'active' : ''}`}
              disabled={!available}
              style={!available ? { opacity: 0.4, cursor: 'not-allowed' } : undefined}
              onClick={() => toggleFilter(f.key)}
            >
              <i className={`fas ${f.icon}`} style={{ marginRight: 4 }} />
              {f.label}
            </button>
          )
        })}
        {allBackends.length > 0 && (
          <SearchableSelect
            value={backendFilter}
            onChange={(v) => { setBackendFilter(v); setPage(1) }}
            options={allBackends}
            placeholder="All Backends"
            allOption="All Backends"
            searchPlaceholder="Search backends..."
            style={{ marginLeft: 'auto' }}
          />
        )}
      </div>

      {/* Context size slider for VRAM estimates */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', fontSize: '0.8125rem' }}>
        <label style={{ color: 'var(--color-text-muted)', whiteSpace: 'nowrap' }}>
          <i className="fas fa-memory" style={{ marginRight: 4 }} />
          Context:
        </label>
        <input
          type="range"
          min={0}
          max={CONTEXT_SIZES.length - 1}
          value={CONTEXT_SIZES.indexOf(contextSize)}
          onChange={(e) => setContextSize(CONTEXT_SIZES[e.target.value])}
          style={{ width: 140, accentColor: 'var(--color-primary)' }}
        />
        <span style={{ fontWeight: 600, minWidth: '3em' }}>
          {CONTEXT_LABELS[CONTEXT_SIZES.indexOf(contextSize)]}
        </span>
      </div>

      {/* Table */}
      {loading ? (
        <GalleryLoader />
      ) : models.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-search" /></div>
          <h2 className="empty-state-title">No models found</h2>
          <p className="empty-state-text">
            {search || filters.length > 0 || backendFilter
              ? 'No models match your current search or filters.'
              : 'The model gallery is empty.'}
          </p>
          {(search || filters.length > 0 || backendFilter) && (
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => { handleSearch(''); setFilters([]); setBackendFilter(''); setPage(1) }}
            >
              <i className="fas fa-times" /> Clear filters
            </button>
          )}
        </div>
      ) : (
        <div className="table-container" style={{ background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-lg)', overflow: 'hidden' }}>
          <div style={{ overflowX: 'auto' }}>
            <table className="table" style={{ minWidth: '800px' }}>
              <thead>
                <tr>
                  <th style={{ width: '30px' }}></th>
                  <th style={{ width: '60px' }}></th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('name')}>
                    Model Name {sort === 'name' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th>Description</th>
                  <th>Backend</th>
                  <th>Size / VRAM</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('status')}>
                    Status {sort === 'status' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {models.map((model, idx) => {
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

                  return (
                    <React.Fragment key={name}>
                    <tr
                      onClick={() => { setExpandedRow(isExpanded ? null : idx); setExpandedFiles(false) }}
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
                                <i className="fas fa-circle-exclamation" /> Trust Remote Code
                              </span>
                            </div>
                          )}
                        </div>
                      </td>

                      {/* Description */}
                      <td>
                        <div style={{
                          fontSize: '0.8125rem', color: 'var(--color-text-secondary)',
                          maxWidth: '200px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        }} title={model.description}>
                          {model.description || '—'}
                        </div>
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
                                  <span>Size: {sizeDisplay}</span>
                                )}
                                {sizeDisplay && sizeDisplay !== '0 B' && vramDisplay && vramDisplay !== '0 B' && ' · '}
                                {vramDisplay && vramDisplay !== '0 B' && (
                                  <span>VRAM: {vramDisplay}</span>
                                )}
                              </span>
                              {fit !== null && (
                                <span style={{ fontSize: '0.6875rem', display: 'flex', alignItems: 'center', gap: '4px' }}>
                                  <i className="fas fa-microchip" style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }} />
                                  <span style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
                                    {fit ? 'Fits' : 'May not fit'}
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
                          <div>
                            <span style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}>
                              <i className="fas fa-spinner fa-spin" /> Installing...
                            </span>
                            {progress > 0 && (
                              <div style={{ marginTop: '4px', width: '100%', maxWidth: '120px' }}>
                                <div style={{ height: 3, background: 'var(--color-bg-tertiary)', borderRadius: 2, overflow: 'hidden' }}>
                                  <div style={{ height: '100%', width: `${progress}%`, background: 'var(--color-primary)', borderRadius: 2, transition: 'width 300ms' }} />
                                </div>
                              </div>
                            )}
                          </div>
                        ) : model.installed ? (
                          <span className="badge badge-success">
                            <i className="fas fa-check-circle" /> Installed
                          </span>
                        ) : (
                          <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                            <i className="fas fa-circle" /> Not Installed
                          </span>
                        )}
                      </td>

                      {/* Actions */}
                      <td>
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }} onClick={e => e.stopPropagation()}>
                          {model.installed ? (
                            <>
                              <button className="btn btn-secondary btn-sm" onClick={() => handleInstall(name)} title="Reinstall">
                                <i className="fas fa-rotate" />
                              </button>
                              <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name)} title="Delete">
                                <i className="fas fa-trash" />
                              </button>
                            </>
                          ) : (
                            <button
                              className="btn btn-primary btn-sm"
                              onClick={() => handleInstall(name)}
                              disabled={installing}
                              title="Install"
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
                          <ModelDetail model={model} fit={fit} sizeDisplay={sizeDisplay} vramDisplay={vramDisplay} expandedFiles={expandedFiles} setExpandedFiles={setExpandedFiles} />
                        </td>
                      </tr>
                    )}
                    </React.Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
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
    </div>
  )
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

function ModelDetail({ model, fit, sizeDisplay, vramDisplay, expandedFiles, setExpandedFiles }) {
  const files = model.additionalFiles || model.files || []
  return (
    <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)', background: 'var(--color-bg-primary)', borderTop: '1px solid var(--color-border-subtle)' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          <DetailRow label="Description">
            {model.description && (
              <span style={{ color: 'var(--color-text-secondary)', lineHeight: 1.6 }}>{model.description}</span>
            )}
          </DetailRow>
          <DetailRow label="Gallery">
            {model.gallery && (
              <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                {typeof model.gallery === 'string' ? model.gallery : model.gallery.name || '—'}
              </span>
            )}
          </DetailRow>
          <DetailRow label="Backend">
            {model.backend && (
              <span className="badge badge-info" style={{ fontSize: '0.6875rem' }}>
                {model.backend}
              </span>
            )}
          </DetailRow>
          <DetailRow label="Size">
            {sizeDisplay && sizeDisplay !== '0 B' ? sizeDisplay : null}
          </DetailRow>
          <DetailRow label="VRAM">
            {vramDisplay && vramDisplay !== '0 B' ? (
              <span style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                {vramDisplay}
                {fit !== null && (
                  <span style={{ fontSize: '0.75rem', color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
                    <i className="fas fa-microchip" /> {fit ? 'Fits in GPU' : 'May not fit in GPU'}
                  </span>
                )}
              </span>
            ) : null}
          </DetailRow>
          <DetailRow label="License">
            {model.license && <span>{model.license}</span>}
          </DetailRow>
          <DetailRow label="Tags">
            {model.tags?.length > 0 && (
              <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                {model.tags.map(tag => (
                  <span key={tag} className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{tag}</span>
                ))}
              </div>
            )}
          </DetailRow>
          <DetailRow label="Links">
            {model.urls?.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                {model.urls.map((url, i) => (
                  <a key={i} href={url} target="_blank" rel="noopener noreferrer" style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', wordBreak: 'break-all' }}>
                    <i className="fas fa-external-link-alt" style={{ marginRight: 4, fontSize: '0.6875rem' }} />{url}
                  </a>
                ))}
              </div>
            )}
          </DetailRow>
          {model.trustRemoteCode && (
            <DetailRow label="Warning">
              <span className="badge badge-error" style={{ fontSize: '0.6875rem' }}>
                <i className="fas fa-circle-exclamation" /> Requires Trust Remote Code
              </span>
            </DetailRow>
          )}
          {files.length > 0 && (
            <DetailRow label="Files">
              <div>
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={(e) => { e.stopPropagation(); setExpandedFiles(!expandedFiles) }}
                  style={{ marginBottom: expandedFiles ? 'var(--spacing-sm)' : 0 }}
                >
                  <i className={`fas fa-chevron-${expandedFiles ? 'down' : 'right'}`} style={{ fontSize: '0.5rem', marginRight: 4 }} />
                  {files.length} file{files.length !== 1 ? 's' : ''}
                </button>
                {expandedFiles && (
                  <div style={{ border: '1px solid var(--color-border)', borderRadius: 'var(--radius-md)', overflow: 'hidden' }}>
                    <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.75rem' }}>
                      <thead>
                        <tr style={{ background: 'var(--color-bg-tertiary)' }}>
                          <th style={{ padding: '4px 8px', textAlign: 'left', fontWeight: 500 }}>Filename</th>
                          <th style={{ padding: '4px 8px', textAlign: 'left', fontWeight: 500 }}>URI</th>
                          <th style={{ padding: '4px 8px', textAlign: 'left', fontWeight: 500 }}>SHA256</th>
                        </tr>
                      </thead>
                      <tbody>
                        {files.map((f, i) => (
                          <tr key={i} style={{ borderTop: '1px solid var(--color-border-subtle)' }}>
                            <td style={{ padding: '4px 8px', fontFamily: 'monospace' }}>{f.filename || '—'}</td>
                            <td style={{ padding: '4px 8px', wordBreak: 'break-all', maxWidth: 300 }}>{f.uri || '—'}</td>
                            <td style={{ padding: '4px 8px', fontFamily: 'monospace', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
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
