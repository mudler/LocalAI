import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { backendsApi } from '../utils/api'
import { useOperations } from '../hooks/useOperations'
import LoadingSpinner from '../components/LoadingSpinner'
import { renderMarkdown } from '../utils/markdown'

export default function Backends() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { operations } = useOperations()
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
  const [selectedBackend, setSelectedBackend] = useState(null)
  const debounceRef = useRef(null)

  const [allBackends, setAllBackends] = useState([])

  const fetchBackends = useCallback(async () => {
    try {
      setLoading(true)
      const params = { page: 1, items: 9999, sort: sortBy, order: sortOrder }
      if (search) params.term = search
      const data = await backendsApi.list(params)
      const list = Array.isArray(data?.backends) ? data.backends : Array.isArray(data) ? data : []
      setAllBackends(list)
      setInstalledCount(list.filter(b => b.installed).length)
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

  // Client-side filtering by tag
  const filteredBackends = filter
    ? allBackends.filter(b => {
        const tags = (b.tags || []).map(t => t.toLowerCase())
        const name = (b.name || '').toLowerCase()
        const desc = (b.description || '').toLowerCase()
        const f = filter.toLowerCase()
        // Match against tags, or name/description containing the filter keyword
        return tags.some(t => t.includes(f)) || name.includes(f) || desc.includes(f)
      })
    : allBackends

  // Client-side pagination
  const ITEMS_PER_PAGE = 21
  const totalPages = Math.max(1, Math.ceil(filteredBackends.length / ITEMS_PER_PAGE))
  const backends = filteredBackends.slice((page - 1) * ITEMS_PER_PAGE, page * ITEMS_PER_PAGE)

  const handleSearch = (value) => {
    setSearch(value)
    setPage(1)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => fetchBackends(), 500)
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
      addToast(`Install failed: ${err.message}`, 'error')
    }
  }

  const handleDelete = async (id) => {
    if (!confirm(`Delete backend ${id}?`)) return
    try {
      await backendsApi.delete(id)
      addToast(`Deleting ${id}...`, 'info')
      setTimeout(fetchBackends, 1000)
    } catch (err) {
      addToast(`Delete failed: ${err.message}`, 'error')
    }
  }

  const handleManualInstall = async (e) => {
    e.preventDefault()
    if (!manualUri.trim()) { addToast('Please enter a URI', 'warning'); return }
    try {
      const body = { uri: manualUri.trim() }
      if (manualName.trim()) body.name = manualName.trim()
      if (manualAlias.trim()) body.alias = manualAlias.trim()
      await backendsApi.installExternal(body)
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
    <div className="page">
      {/* Header */}
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">Backend Management</h1>
          <p className="page-subtitle">Discover and install AI backends to power your models</p>
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
          </div>
          <a className="btn btn-secondary btn-sm" href="https://localai.io/docs/getting-started/manual/" target="_blank" rel="noopener noreferrer">
            <i className="fas fa-book" /> Docs
          </a>
        </div>
      </div>

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

      <div className="filter-bar" style={{ marginBottom: 'var(--spacing-md)' }}>
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
                <th style={{ width: 40 }}></th>
                <SortHeader col="name">Backend</SortHeader>
                <th>Description</th>
                <SortHeader col="repository">Repository</SortHeader>
                <SortHeader col="license">License</SortHeader>
                <SortHeader col="status">Status</SortHeader>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {backends.map(b => {
                const op = getBackendOp(b)
                const isProcessing = !!op

                return (
                  <tr key={b.name || b.id}>
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
                      <span
                        style={{ fontWeight: 500, cursor: 'pointer', color: 'var(--color-primary)' }}
                        onClick={() => setSelectedBackend(b)}
                      >
                        {b.name || b.id}
                      </span>
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

                    {/* Status */}
                    <td>
                      {isProcessing ? (
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                          <div style={{
                            width: 80, height: 6, background: 'var(--color-bg-tertiary)',
                            borderRadius: 3, overflow: 'hidden',
                          }}>
                            <div style={{
                              width: `${op.progress || 0}%`, height: '100%',
                              background: 'var(--color-primary)',
                              borderRadius: 3, transition: 'width 300ms',
                            }} />
                          </div>
                          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
                            {op.isDeletion ? 'Deleting...' : op.isQueued ? 'Queued' : 'Installing...'}
                          </span>
                        </div>
                      ) : b.installed ? (
                        <span className="badge badge-success">
                          <i className="fas fa-check" style={{ fontSize: '0.5rem', marginRight: 2 }} /> Installed
                        </span>
                      ) : (
                        <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                          <i className="fas fa-circle" style={{ fontSize: '0.5rem', marginRight: 2 }} /> Not Installed
                        </span>
                      )}
                    </td>

                    {/* Actions */}
                    <td>
                      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                        <button className="btn btn-secondary btn-sm" onClick={() => setSelectedBackend(b)} title="Details">
                          <i className="fas fa-info-circle" />
                        </button>
                        {b.installed ? (
                          <>
                            <button className="btn btn-secondary btn-sm" onClick={() => handleInstall(b.name || b.id)} title="Reinstall" disabled={isProcessing}>
                              <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
                            </button>
                            <button className="btn btn-danger btn-sm" onClick={() => handleDelete(b.name || b.id)} title="Delete" disabled={isProcessing}>
                              <i className="fas fa-trash" />
                            </button>
                          </>
                        ) : (
                          <button className="btn btn-primary btn-sm" onClick={() => handleInstall(b.name || b.id)} title="Install" disabled={isProcessing}>
                            <i className={`fas ${isProcessing ? 'fa-spinner fa-spin' : 'fa-download'}`} />
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

      {/* Detail Modal */}
      {selectedBackend && (
        <div style={{
          position: 'fixed', inset: 0, zIndex: 1000,
          background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center',
        }} onClick={() => setSelectedBackend(null)}>
          <div className="card" style={{ maxWidth: 600, width: '90%', maxHeight: '80vh', overflow: 'auto' }} onClick={e => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 'var(--spacing-md)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                {selectedBackend.icon ? (
                  <img src={selectedBackend.icon} alt="" style={{ width: 48, height: 48, borderRadius: 'var(--radius-md)' }} />
                ) : (
                  <div style={{
                    width: 48, height: 48, borderRadius: 'var(--radius-md)',
                    background: 'var(--color-bg-tertiary)', display: 'flex',
                    alignItems: 'center', justifyContent: 'center',
                  }}>
                    <i className="fas fa-cog" style={{ fontSize: '1.25rem', color: 'var(--color-text-muted)' }} />
                  </div>
                )}
                <div>
                  <h3 style={{ fontWeight: 600, fontSize: '1.125rem' }}>{selectedBackend.name || selectedBackend.id}</h3>
                  {selectedBackend.installed && <span className="badge badge-success">Installed</span>}
                </div>
              </div>
              <button className="btn btn-secondary btn-sm" onClick={() => setSelectedBackend(null)}>
                <i className="fas fa-xmark" />
              </button>
            </div>

            {/* Description */}
            {selectedBackend.description && (
              <div style={{ marginBottom: 'var(--spacing-md)' }}>
                <div
                  style={{ fontSize: '0.875rem', color: 'var(--color-text-secondary)', lineHeight: 1.6 }}
                  dangerouslySetInnerHTML={{ __html: renderMarkdown(selectedBackend.description) }}
                />
              </div>
            )}

            {/* Tags */}
            {selectedBackend.tags && selectedBackend.tags.length > 0 && (
              <div style={{ marginBottom: 'var(--spacing-md)' }}>
                <span className="form-label">Tags</span>
                <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                  {selectedBackend.tags.map(tag => (
                    <span key={tag} className="badge badge-info" style={{ fontSize: '0.6875rem' }}>{tag}</span>
                  ))}
                </div>
              </div>
            )}

            {/* URLs */}
            {selectedBackend.urls && selectedBackend.urls.length > 0 && (
              <div style={{ marginBottom: 'var(--spacing-md)' }}>
                <span className="form-label">Links</span>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {selectedBackend.urls.map((url, i) => (
                    <a key={i} href={url} target="_blank" rel="noopener noreferrer" style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', wordBreak: 'break-all' }}>
                      <i className="fas fa-external-link-alt" style={{ marginRight: 4 }} />{url}
                    </a>
                  ))}
                </div>
              </div>
            )}

            {/* Repository / License */}
            <div style={{ display: 'flex', gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
              {selectedBackend.gallery && (
                <div>
                  <span className="form-label">Repository</span>
                  <p style={{ fontSize: '0.8125rem' }}>{typeof selectedBackend.gallery === 'string' ? selectedBackend.gallery : selectedBackend.gallery.name || '-'}</p>
                </div>
              )}
              {selectedBackend.license && (
                <div>
                  <span className="form-label">License</span>
                  <p style={{ fontSize: '0.8125rem' }}>{selectedBackend.license}</p>
                </div>
              )}
            </div>

            {/* Actions */}
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end', borderTop: '1px solid var(--color-border-subtle)', paddingTop: 'var(--spacing-md)' }}>
              {selectedBackend.installed ? (
                <>
                  <button className="btn btn-secondary btn-sm" onClick={() => { handleInstall(selectedBackend.name || selectedBackend.id); setSelectedBackend(null) }}>
                    <i className="fas fa-rotate" /> Reinstall
                  </button>
                  <button className="btn btn-danger btn-sm" onClick={() => { handleDelete(selectedBackend.name || selectedBackend.id); setSelectedBackend(null) }}>
                    <i className="fas fa-trash" /> Delete
                  </button>
                </>
              ) : (
                <button className="btn btn-primary btn-sm" onClick={() => { handleInstall(selectedBackend.name || selectedBackend.id); setSelectedBackend(null) }}>
                  <i className="fas fa-download" /> Install
                </button>
              )}
              <button className="btn btn-secondary btn-sm" onClick={() => setSelectedBackend(null)}>Close</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
