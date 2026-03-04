import { useState, useCallback, useEffect, useRef } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import { formatBytes } from '../utils/format'
import LoadingSpinner from '../components/LoadingSpinner'

const FILTERS = [
  { key: '', label: 'All' },
  { key: 'tts', label: 'TTS' },
  { key: 'sd', label: 'Image (SD)' },
  { key: 'llm', label: 'LLM' },
  { key: 'multimodal', label: 'Multimodal' },
  { key: 'embedding', label: 'Embedding' },
  { key: 'reranker', label: 'Rerank' },
  { key: 'whisper', label: 'Whisper' },
  { key: 'vision', label: 'Vision' },
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
  const [filter, setFilter] = useState('')
  const [sort, setSort] = useState('')
  const [order, setOrder] = useState('asc')
  const [installing, setInstalling] = useState(new Set())
  const [selectedModel, setSelectedModel] = useState(null)
  const [stats, setStats] = useState({ total: 0, installed: 0, repositories: 0 })
  const debounceRef = useRef(null)

  // Total GPU memory for "fits" check
  const totalGpuMemory = resources?.aggregate?.total_memory || 0

  const fetchModels = useCallback(async (params = {}) => {
    try {
      setLoading(true)
      const searchVal = params.search !== undefined ? params.search : search
      const filterVal = params.filter !== undefined ? params.filter : filter
      const sortVal = params.sort !== undefined ? params.sort : sort
      // Combine search text and filter into 'term' param
      const term = searchVal || filterVal || ''
      const queryParams = {
        page: params.page || page,
        items: 21,
      }
      if (term) queryParams.term = term
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
    } catch (err) {
      addToast(`Failed to load models: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [page, search, filter, sort, order, addToast])

  useEffect(() => {
    fetchModels()
  }, [page, filter, sort, order])

  const handleSearch = (value) => {
    setSearch(value)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      setPage(1)
      fetchModels({ search: value, page: 1 })
    }, 500)
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
      setInstalling(prev => new Set(prev).add(modelId))
      await modelsApi.install(modelId)
      addToast(`Installing ${modelId}...`, 'info')
    } catch (err) {
      addToast(`Failed to install: ${err.message}`, 'error')
    }
  }

  const handleDelete = async (modelId) => {
    if (!confirm(`Delete model ${modelId}?`)) return
    try {
      await modelsApi.delete(modelId)
      addToast(`Deleting ${modelId}...`, 'info')
      fetchModels()
    } catch (err) {
      addToast(`Failed to delete: ${err.message}`, 'error')
    }
  }

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
          {/* Stats row */}
          <div style={{ display: 'flex', gap: 'var(--spacing-md)', marginTop: 'var(--spacing-sm)', fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
            <span>{stats.total} models available</span>
            <span>·</span>
            <span style={{ color: 'var(--color-success)' }}>{stats.installed} installed</span>
          </div>
        </div>
        <button className="btn btn-secondary btn-sm" onClick={() => navigate('/import-model')}>
          <i className="fas fa-upload" /> Import Model
        </button>
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
        {FILTERS.map(f => (
          <button
            key={f.key}
            className={`filter-btn ${filter === f.key ? 'active' : ''}`}
            onClick={() => { setFilter(f.key); setPage(1) }}
          >
            {f.label}
          </button>
        ))}
      </div>

      {/* Table */}
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <LoadingSpinner size="lg" />
        </div>
      ) : models.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-search" /></div>
          <h2 className="empty-state-title">No models found</h2>
          <p className="empty-state-text">Try adjusting your search or filters</p>
        </div>
      ) : (
        <div className="table-container" style={{ background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-lg)', overflow: 'hidden' }}>
          <div style={{ overflowX: 'auto' }}>
            <table className="table" style={{ minWidth: '800px' }}>
              <thead>
                <tr>
                  <th style={{ width: '60px' }}></th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('name')}>
                    Model Name {sort === 'name' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th>Description</th>
                  <th>Size / VRAM</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('status')}>
                    Status {sort === 'status' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {models.map(model => {
                  const name = model.name || model.id
                  const installing = isInstalling(name)
                  const progress = getOperationProgress(name)
                  const fit = fitsGpu(model.estimated_vram_bytes)

                  return (
                    <tr key={name}>
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

                      {/* Size / VRAM */}
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                          {(model.estimated_size_display || model.estimated_vram_display) ? (
                            <>
                              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>
                                {model.estimated_size_display && model.estimated_size_display !== '0 B' && (
                                  <span>Size: {model.estimated_size_display}</span>
                                )}
                                {model.estimated_size_display && model.estimated_size_display !== '0 B' && model.estimated_vram_display && model.estimated_vram_display !== '0 B' && ' · '}
                                {model.estimated_vram_display && model.estimated_vram_display !== '0 B' && (
                                  <span>VRAM: {model.estimated_vram_display}</span>
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
                        <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                          <button
                            className="btn btn-secondary btn-sm"
                            onClick={() => setSelectedModel(model)}
                            title="Details"
                          >
                            <i className="fas fa-info-circle" />
                          </button>
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

      {/* Detail Modal */}
      {selectedModel && (
        <div style={{
          position: 'fixed', inset: 0, zIndex: 100,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'rgba(0,0,0,0.6)', backdropFilter: 'blur(4px)',
        }} onClick={() => setSelectedModel(null)}>
          <div style={{
            background: 'var(--color-bg-secondary)',
            border: '1px solid var(--color-border-subtle)',
            borderRadius: 'var(--radius-lg)',
            maxWidth: '600px', width: '90%', maxHeight: '80vh',
            display: 'flex', flexDirection: 'column',
          }} onClick={e => e.stopPropagation()}>
            {/* Modal header */}
            <div style={{
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              padding: 'var(--spacing-md)', borderBottom: '1px solid var(--color-border-subtle)',
            }}>
              <h3 style={{ fontSize: '1rem', fontWeight: 600 }}>{selectedModel.name}</h3>
              <button className="btn btn-secondary btn-sm" onClick={() => setSelectedModel(null)}>
                <i className="fas fa-times" />
              </button>
            </div>
            {/* Modal body */}
            <div style={{ padding: 'var(--spacing-md)', overflowY: 'auto', flex: 1 }}>
              {/* Icon */}
              {selectedModel.icon && (
                <div style={{
                  width: 48, height: 48, borderRadius: 'var(--radius-md)',
                  border: '1px solid var(--color-border-subtle)', overflow: 'hidden',
                  marginBottom: 'var(--spacing-md)',
                }}>
                  <img src={selectedModel.icon} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                </div>
              )}
              {/* Description */}
              {selectedModel.description && (
                <p style={{ fontSize: '0.875rem', color: 'var(--color-text-secondary)', lineHeight: 1.6, marginBottom: 'var(--spacing-md)' }}>
                  {selectedModel.description}
                </p>
              )}
              {/* Size/VRAM */}
              {(selectedModel.estimated_size_display || selectedModel.estimated_vram_display) && (
                <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>
                  {selectedModel.estimated_size_display && <div>Size: {selectedModel.estimated_size_display}</div>}
                  {selectedModel.estimated_vram_display && <div>VRAM: {selectedModel.estimated_vram_display}</div>}
                </div>
              )}
              {/* Tags */}
              {selectedModel.tags?.length > 0 && (
                <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap', marginBottom: 'var(--spacing-md)' }}>
                  {selectedModel.tags.map(tag => (
                    <span key={tag} className="badge badge-info">{tag}</span>
                  ))}
                </div>
              )}
              {/* Links */}
              {selectedModel.urls?.length > 0 && (
                <div style={{ marginBottom: 'var(--spacing-md)' }}>
                  <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: 'var(--spacing-xs)' }}>Links</h4>
                  {selectedModel.urls.map((url, i) => (
                    <a key={i} href={url} target="_blank" rel="noopener noreferrer" style={{ display: 'block', fontSize: '0.8125rem', color: 'var(--color-primary)', marginBottom: '2px' }}>
                      {url}
                    </a>
                  ))}
                </div>
              )}
            </div>
            {/* Modal footer */}
            <div style={{
              padding: 'var(--spacing-sm) var(--spacing-md)',
              borderTop: '1px solid var(--color-border-subtle)',
              display: 'flex', justifyContent: 'flex-end',
            }}>
              <button className="btn btn-secondary btn-sm" onClick={() => setSelectedModel(null)}>Close</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
