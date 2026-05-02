import { useState, useCallback, useEffect } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useDebouncedCallback } from '../hooks/useDebounce'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import SearchableSelect from '../components/SearchableSelect'
import ConfirmDialog from '../components/ConfirmDialog'
import GalleryLoader from '../components/GalleryLoader'
import React from 'react'


const FILTERS = [
  { key: '', labelKey: 'filters.all', icon: 'fa-layer-group' },
  { key: 'llm', labelKey: 'filters.llm', icon: 'fa-brain' },
  { key: 'sd', labelKey: 'filters.image', icon: 'fa-image' },
  { key: 'multimodal', labelKey: 'filters.multimodal', icon: 'fa-shapes' },
  { key: 'vision', labelKey: 'filters.vision', icon: 'fa-eye' },
  { key: 'tts', labelKey: 'filters.tts', icon: 'fa-microphone' },
  { key: 'stt', labelKey: 'filters.stt', icon: 'fa-headphones' },
  { key: 'embedding', labelKey: 'filters.embedding', icon: 'fa-vector-square' },
  { key: 'reranker', labelKey: 'filters.rerank', icon: 'fa-sort' },
]

export default function Models() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('models')
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
  const [installing, setInstalling] = useState(new Map())
  const [expandedRow, setExpandedRow] = useState(null)
  const [expandedFiles, setExpandedFiles] = useState(false)
  const [stats, setStats] = useState({ total: 0, installed: 0, repositories: 0 })
  const [backendFilter, setBackendFilter] = useState('')
  const [allBackends, setAllBackends] = useState([])
  const [confirmDialog, setConfirmDialog] = useState(null)

  // Total GPU memory for "fits" check
  const totalGpuMemory = resources?.aggregate?.total_memory || 0

  const fetchModels = useCallback(async (params = {}) => {
    try {
      setLoading(true)
      const searchVal = params.search !== undefined ? params.search : search
      const filterVal = params.filter !== undefined ? params.filter : filter
      const sortVal = params.sort !== undefined ? params.sort : sort
      const backendVal = params.backendFilter !== undefined ? params.backendFilter : backendFilter
      const queryParams = {
        page: params.page || page,
        items: 9,
      }
      if (filterVal) queryParams.tag = filterVal
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
      addToast(t('errors.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [page, search, filter, sort, order, backendFilter, addToast, t])

  useEffect(() => {
    fetchModels()
  }, [page, filter, sort, order, backendFilter])

  // Re-fetch when operations change (install/delete completion)
  useEffect(() => {
    if (!loading) fetchModels()
  }, [operations.length])

  const debouncedFetch = useDebouncedCallback((value) => {
    setPage(1)
    fetchModels({ search: value, page: 1 })
  })

  const handleSearch = (value) => {
    setSearch(value)
    debouncedFetch(value)
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

  return (
    <div className="page page--wide">
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">{t('title')}</h1>
          <p className="page-subtitle">{t('subtitle')}</p>
        </div>
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
          <button className="btn btn-primary btn-sm" onClick={() => navigate('/app/model-editor')}>
            <i className="fas fa-plus" /> {t('actions.addModel')}
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
            <i className="fas fa-upload" /> {t('actions.importModel')}
          </button>
        </div>
      </div>

      {/* Search */}
      <div className="search-bar" style={{ marginBottom: 'var(--spacing-md)' }}>
        <i className="fas fa-search search-icon" />
        <input
          className="input"
          type="text"
          placeholder={t('search.placeholder')}
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
            <i className={`fas ${f.icon}`} style={{ marginRight: 4 }} />
            {t(f.labelKey)}
          </button>
        ))}
        {allBackends.length > 0 && (
          <SearchableSelect
            value={backendFilter}
            onChange={(v) => { setBackendFilter(v); setPage(1) }}
            options={allBackends}
            placeholder={t('filters.allBackends')}
            allOption={t('filters.allBackends')}
            searchPlaceholder={t('filters.searchBackends')}
            style={{ marginLeft: 'auto' }}
          />
        )}
      </div>

      {/* Table */}
      {loading ? (
        <GalleryLoader />
      ) : models.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-search" /></div>
          <h2 className="empty-state-title">{t('empty.title')}</h2>
          <p className="empty-state-text">
            {search || filter || backendFilter ? t('empty.withFilters') : t('empty.noFilters')}
          </p>
          {(search || filter || backendFilter) && (
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => { handleSearch(''); setFilter(''); setBackendFilter(''); setPage(1) }}
            >
              <i className="fas fa-times" /> {t('search.clearFilters')}
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
                {models.map((model, idx) => {
                  const name = model.name || model.id
                  const installing = isInstalling(name)
                  const progress = getOperationProgress(name)
                  const fit = fitsGpu(model.estimated_vram_bytes)
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
                                <i className="fas fa-circle-exclamation" /> {t('table.trustRemoteCode')}
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
                          {(model.estimated_size_display || model.estimated_vram_display) ? (
                            <>
                              <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>
                                {model.estimated_size_display && model.estimated_size_display !== '0 B' && (
                                  <span>{t('table.size', { size: model.estimated_size_display })}</span>
                                )}
                                {model.estimated_size_display && model.estimated_size_display !== '0 B' && model.estimated_vram_display && model.estimated_vram_display !== '0 B' && ' · '}
                                {model.estimated_vram_display && model.estimated_vram_display !== '0 B' && (
                                  <span>{t('table.vram', { vram: model.estimated_vram_display })}</span>
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
                          <ModelDetail model={model} fit={fit} expandedFiles={expandedFiles} setExpandedFiles={setExpandedFiles} t={t} />
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

function ModelDetail({ model, fit, expandedFiles, setExpandedFiles, t }) {
  const files = model.additionalFiles || model.files || []
  return (
    <div style={{ padding: 'var(--spacing-md) var(--spacing-lg)', background: 'var(--color-bg-primary)', borderTop: '1px solid var(--color-border-subtle)' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          <DetailRow label={t('detail.description')}>
            {model.description && (
              <span style={{ color: 'var(--color-text-secondary)', lineHeight: 1.6 }}>{model.description}</span>
            )}
          </DetailRow>
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
            {model.estimated_size_display && model.estimated_size_display !== '0 B' ? model.estimated_size_display : null}
          </DetailRow>
          <DetailRow label={t('detail.vram')}>
            {model.estimated_vram_display && model.estimated_vram_display !== '0 B' ? (
              <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                {model.estimated_vram_display}
                {fit !== null && (
                  <span style={{ fontSize: '0.75rem', color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
                    <i className="fas fa-microchip" /> {fit ? t('detail.fitsGpu') : t('detail.mayNotFitGpu')}
                  </span>
                )}
              </span>
            ) : null}
          </DetailRow>
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
                  <a key={i} href={url} target="_blank" rel="noopener noreferrer" style={{ fontSize: '0.8125rem', color: 'var(--color-primary)', wordBreak: 'break-all' }}>
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
