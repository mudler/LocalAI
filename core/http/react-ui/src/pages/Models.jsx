import { useState, useCallback, useEffect, useRef, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import { formatBytes } from '../utils/format'


const LOADING_PHRASES = [
  { text: 'Rounding up the neural networks...', icon: 'fa-brain' },
  { text: 'Asking the models to line up nicely...', icon: 'fa-people-line' },
  { text: 'Convincing transformers to transform...', icon: 'fa-wand-magic-sparkles' },
  { text: 'Herding digital llamas...', icon: 'fa-horse' },
  { text: 'Downloading more RAM... just kidding', icon: 'fa-memory' },
  { text: 'Counting parameters... lost count at a billion', icon: 'fa-calculator' },
  { text: 'Untangling attention heads...', icon: 'fa-diagram-project' },
  { text: 'Warming up the GPUs...', icon: 'fa-fire' },
  { text: 'Teaching AI to sit and stay...', icon: 'fa-graduation-cap' },
  { text: 'Polishing the weights and biases...', icon: 'fa-gem' },
  { text: 'Stacking layers like pancakes...', icon: 'fa-layer-group' },
  { text: 'Negotiating with the token budget...', icon: 'fa-coins' },
  { text: 'Fetching models from the cloud mines...', icon: 'fa-cloud-arrow-down' },
  { text: 'Calibrating the vibe check algorithm...', icon: 'fa-gauge-high' },
  { text: 'Optimizing inference with good intentions...', icon: 'fa-bolt' },
  { text: 'Measuring GPU with a ruler...', icon: 'fa-ruler' },
  { text: 'Will it fit? Asking the VRAM oracle...', icon: 'fa-microchip' },
  { text: 'Playing Tetris with model layers...', icon: 'fa-cubes' },
  { text: 'Checking if we need more RGB...', icon: 'fa-rainbow' },
  { text: 'Squeezing tensors into memory...', icon: 'fa-compress' },
  { text: 'Whispering sweet nothings to CUDA cores...', icon: 'fa-heart' },
  { text: 'Asking the electrons to scoot over...', icon: 'fa-atom' },
  { text: 'Defragmenting the flux capacitor...', icon: 'fa-clock-rotate-left' },
  { text: 'Consulting the tensor gods...', icon: 'fa-hands-praying' },
  { text: 'Checking under the GPU\'s hood...', icon: 'fa-car' },
  { text: 'Seeing if the hamsters can run faster...', icon: 'fa-fan' },
  { text: 'Running very important math... carry the 1...', icon: 'fa-square-root-variable' },
  { text: 'Poking the memory bus gently...', icon: 'fa-bus' },
  { text: 'Bribing the scheduler with clock cycles...', icon: 'fa-stopwatch' },
  { text: 'Asking models to share their VRAM nicely...', icon: 'fa-handshake' },
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

const CATEGORY_FILTERS = [
  { key: '', label: 'All', icon: 'fa-layer-group' },
  { key: 'llm', label: 'LLM', icon: 'fa-brain' },
  { key: 'sd', label: 'Image', icon: 'fa-image' },
  { key: 'multimodal', label: 'Multimodal', icon: 'fa-shapes' },
  { key: 'vision', label: 'Vision', icon: 'fa-eye' },
  { key: 'tts', label: 'TTS', icon: 'fa-microphone' },
  { key: 'stt', label: 'STT', icon: 'fa-headphones' },
  { key: 'embedding', label: 'Embedding', icon: 'fa-vector-square' },
  { key: 'reranker', label: 'Rerank', icon: 'fa-sort' },
]

function ModelCard({ model, installing, progress, fit, onInfo, onInstall, onDelete }) {
  const name = model.name || model.id

  return (
    <div style={{
      background: 'var(--gradient-card)',
      border: '1px solid var(--color-border-subtle)',
      borderRadius: 'var(--radius-lg)',
      padding: 'var(--spacing-md)',
      display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)',
      transition: 'border-color var(--duration-fast) ease, box-shadow var(--duration-fast) ease',
      cursor: 'pointer',
      position: 'relative',
    }}
      onMouseEnter={e => {
        e.currentTarget.style.borderColor = 'var(--color-border-primary)'
        e.currentTarget.style.boxShadow = 'var(--shadow-md)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.borderColor = 'var(--color-border-subtle)'
        e.currentTarget.style.boxShadow = 'none'
      }}
      onClick={() => onInfo(model)}
    >
      {/* Header row: icon + status */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{
          width: 40, height: 40, borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border-subtle)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'var(--color-bg-primary)', overflow: 'hidden', flexShrink: 0,
        }}>
          {model.icon ? (
            <img src={model.icon} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} loading="lazy" />
          ) : (
            <i className="fas fa-brain" style={{ fontSize: '1rem', color: 'var(--color-accent)' }} />
          )}
        </div>
        {installing ? (
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-primary)' }}>
            <i className="fas fa-spinner fa-spin" /> {progress > 0 ? `${progress}%` : 'Installing...'}
          </span>
        ) : model.installed ? (
          <span className="badge badge-success" style={{ fontSize: '0.625rem' }}>
            <i className="fas fa-check-circle" /> Installed
          </span>
        ) : null}
      </div>

      {/* Name */}
      <div style={{ fontWeight: 600, fontSize: '0.875rem', lineHeight: 1.3 }}>
        {name}
        {model.trustRemoteCode && (
          <span className="badge badge-error" style={{ fontSize: '0.5625rem', marginLeft: 6, verticalAlign: 'middle' }}>
            <i className="fas fa-circle-exclamation" /> Trust Remote Code
          </span>
        )}
      </div>

      {/* Description */}
      <div style={{
        fontSize: '0.75rem', color: 'var(--color-text-secondary)',
        overflow: 'hidden', textOverflow: 'ellipsis',
        display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical',
        lineHeight: 1.4, minHeight: '2.1em',
      }}>
        {model.description || 'No description available'}
      </div>

      {/* Tags */}
      {model.tags?.length > 0 && (
        <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
          {model.tags.slice(0, 3).map(tag => (
            <span key={tag} style={{
              fontSize: '0.625rem', padding: '1px 6px',
              borderRadius: 'var(--radius-full)',
              background: 'var(--color-accent-light)', color: 'var(--color-accent)',
              border: '1px solid rgba(139, 92, 246, 0.15)',
            }}>{tag}</span>
          ))}
          {model.tags.length > 3 && (
            <span style={{ fontSize: '0.625rem', color: 'var(--color-text-muted)' }}>+{model.tags.length - 3}</span>
          )}
        </div>
      )}

      {/* Size / VRAM row */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 'auto', paddingTop: 'var(--spacing-xs)' }}>
        <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
          {model.estimated_size_display && model.estimated_size_display !== '0 B'
            ? model.estimated_size_display
            : model.estimated_vram_display && model.estimated_vram_display !== '0 B'
              ? `VRAM: ${model.estimated_vram_display}`
              : ''}
        </span>
        {fit !== null && (
          <span style={{ fontSize: '0.625rem', display: 'flex', alignItems: 'center', gap: '3px' }}>
            <i className="fas fa-microchip" style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }} />
            <span style={{ color: fit ? 'var(--color-success)' : 'var(--color-error)' }}>
              {fit ? 'Fits' : 'May not fit'}
            </span>
          </span>
        )}
      </div>

      {/* Action buttons */}
      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', paddingTop: 'var(--spacing-xs)', borderTop: '1px solid var(--color-border-subtle)' }}
        onClick={e => e.stopPropagation()}
      >
        {model.installed ? (
          <>
            <button className="btn btn-secondary btn-sm" style={{ flex: 1, fontSize: '0.6875rem' }} onClick={() => onInstall(name)} title="Reinstall">
              <i className="fas fa-rotate" /> Reinstall
            </button>
            <button className="btn btn-danger btn-sm" style={{ fontSize: '0.6875rem' }} onClick={() => onDelete(name)} title="Delete">
              <i className="fas fa-trash" />
            </button>
          </>
        ) : (
          <button className="btn btn-primary btn-sm" style={{ flex: 1, fontSize: '0.6875rem' }} onClick={() => onInstall(name)} disabled={installing} title="Install">
            <i className="fas fa-download" /> Install
          </button>
        )}
      </div>
    </div>
  )
}

export default function Models() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { operations } = useOperations()
  const { resources } = useResources()
  const [models, setModels] = useState([])
  const [allModels, setAllModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const [totalPages, setTotalPages] = useState(1)
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('')
  const [selectedTags, setSelectedTags] = useState(new Set())
  const [sort, setSort] = useState('')
  const [order, setOrder] = useState('asc')
  const [installing, setInstalling] = useState(new Set())
  const [selectedModel, setSelectedModel] = useState(null)
  const [viewMode, setViewMode] = useState('grid')
  const [stats, setStats] = useState({ total: 0, installed: 0, repositories: 0 })
  const debounceRef = useRef(null)

  const totalGpuMemory = resources?.aggregate?.total_memory || 0

  // Extract unique tags from all loaded models
  const allTags = useMemo(() => {
    const tagCounts = {}
    allModels.forEach(m => {
      (m.tags || []).forEach(tag => {
        tagCounts[tag] = (tagCounts[tag] || 0) + 1
      })
    })
    return Object.entries(tagCounts)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 30)
  }, [allModels])

  // Client-side filtering by selected tags and search across fields
  const filteredModels = useMemo(() => {
    let result = models
    if (selectedTags.size > 0) {
      result = result.filter(m =>
        (m.tags || []).some(tag => selectedTags.has(tag))
      )
    }
    if (search) {
      const q = search.toLowerCase()
      result = result.filter(m => {
        const name = (m.name || m.id || '').toLowerCase()
        const desc = (m.description || '').toLowerCase()
        const tags = (m.tags || []).join(' ').toLowerCase()
        return name.includes(q) || desc.includes(q) || tags.includes(q)
      })
    }
    return result
  }, [models, selectedTags, search])

  const fetchModels = useCallback(async (params = {}) => {
    try {
      setLoading(true)
      const searchVal = params.search !== undefined ? params.search : search
      const filterVal = params.filter !== undefined ? params.filter : filter
      const sortVal = params.sort !== undefined ? params.sort : sort
      const term = filterVal || ''
      const queryParams = {
        page: params.page || page,
        items: 21,
      }
      if (term) queryParams.term = term
      if (searchVal) queryParams.term = searchVal
      if (sortVal) {
        queryParams.sort = sortVal
        queryParams.order = params.order || order
      }
      const data = await modelsApi.list(queryParams)
      const modelList = data?.models || []
      setModels(modelList)
      setAllModels(prev => {
        const map = new Map(prev.map(m => [m.name || m.id, m]))
        modelList.forEach(m => map.set(m.name || m.id, m))
        return Array.from(map.values())
      })
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

  const toggleTag = (tag) => {
    setSelectedTags(prev => {
      const next = new Set(prev)
      if (next.has(tag)) next.delete(tag)
      else next.add(tag)
      return next
    })
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
              <a onClick={() => navigate('/manage')} style={{ cursor: 'pointer' }}>
                <div style={{ fontSize: '1.25rem', fontWeight: 700, color: 'var(--color-success)' }}>{stats.installed}</div>
                <div style={{ color: 'var(--color-text-muted)' }}>Installed</div>
              </a>
            </div>
          </div>
          <button className="btn btn-secondary btn-sm" onClick={() => navigate('/import-model')}>
            <i className="fas fa-upload" /> Import Model
          </button>
        </div>
      </div>

      {/* Search + View toggle */}
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', alignItems: 'center' }}>
        <div className="search-bar" style={{ flex: 1, marginBottom: 0 }}>
          <i className="fas fa-search search-icon" />
          <input
            className="input"
            type="text"
            placeholder="Search by name, description, or tags..."
            value={search}
            onChange={(e) => handleSearch(e.target.value)}
          />
        </div>
        <div style={{ display: 'flex', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-md)', overflow: 'hidden' }}>
          <button
            style={{
              padding: '6px 10px', border: 'none', cursor: 'pointer',
              background: viewMode === 'grid' ? 'var(--color-primary)' : 'var(--color-bg-tertiary)',
              color: viewMode === 'grid' ? 'var(--color-primary-text)' : 'var(--color-text-secondary)',
              transition: 'background var(--duration-fast) ease',
            }}
            onClick={() => setViewMode('grid')}
            title="Grid view"
          >
            <i className="fas fa-grid-2" />
          </button>
          <button
            style={{
              padding: '6px 10px', border: 'none', cursor: 'pointer',
              borderLeft: '1px solid var(--color-border-default)',
              background: viewMode === 'table' ? 'var(--color-primary)' : 'var(--color-bg-tertiary)',
              color: viewMode === 'table' ? 'var(--color-primary-text)' : 'var(--color-text-secondary)',
              transition: 'background var(--duration-fast) ease',
            }}
            onClick={() => setViewMode('table')}
            title="Table view"
          >
            <i className="fas fa-list" />
          </button>
        </div>
      </div>

      {/* Category filter buttons */}
      <div className="filter-bar">
        {CATEGORY_FILTERS.map(f => (
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

      {/* Tag cloud */}
      {allTags.length > 0 && (
        <div style={{
          display: 'flex', gap: '6px', flexWrap: 'wrap',
          padding: 'var(--spacing-sm) 0', marginBottom: 'var(--spacing-sm)',
        }}>
          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', alignSelf: 'center', marginRight: 4 }}>
            <i className="fas fa-tags" /> Tags:
          </span>
          {allTags.map(([tag, count]) => (
            <button
              key={tag}
              onClick={() => toggleTag(tag)}
              style={{
                fontSize: '0.6875rem', padding: '2px 8px',
                borderRadius: 'var(--radius-full)',
                border: selectedTags.has(tag)
                  ? '1px solid var(--color-accent)'
                  : '1px solid var(--color-border-default)',
                background: selectedTags.has(tag)
                  ? 'var(--color-accent-light)'
                  : 'var(--color-bg-tertiary)',
                color: selectedTags.has(tag)
                  ? 'var(--color-accent)'
                  : 'var(--color-text-secondary)',
                cursor: 'pointer',
                transition: 'all var(--duration-fast) ease',
              }}
            >
              {tag} <span style={{ opacity: 0.6 }}>({count})</span>
            </button>
          ))}
          {selectedTags.size > 0 && (
            <button
              onClick={() => setSelectedTags(new Set())}
              style={{
                fontSize: '0.6875rem', padding: '2px 8px',
                borderRadius: 'var(--radius-full)',
                border: '1px solid var(--color-border-default)',
                background: 'var(--color-bg-tertiary)',
                color: 'var(--color-error)',
                cursor: 'pointer',
              }}
            >
              <i className="fas fa-times" /> Clear
            </button>
          )}
        </div>
      )}

      {/* Content */}
      {loading ? (
        <GalleryLoader />
      ) : filteredModels.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-search" /></div>
          <h2 className="empty-state-title">No models found</h2>
          <p className="empty-state-text">Try adjusting your search or filters</p>
        </div>
      ) : viewMode === 'grid' ? (
        /* Grid View */
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
          gap: 'var(--spacing-md)',
        }}>
          {filteredModels.map(model => {
            const name = model.name || model.id
            return (
              <ModelCard
                key={name}
                model={model}
                installing={isInstalling(name)}
                progress={getOperationProgress(name)}
                fit={fitsGpu(model.estimated_vram_bytes)}
                onInfo={setSelectedModel}
                onInstall={handleInstall}
                onDelete={handleDelete}
              />
            )
          })}
        </div>
      ) : (
        /* Table View */
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
                  <th>Tags</th>
                  <th>Size / VRAM</th>
                  <th style={{ cursor: 'pointer' }} onClick={() => handleSort('status')}>
                    Status {sort === 'status' && <i className={`fas fa-arrow-${order === 'asc' ? 'up' : 'down'}`} style={{ fontSize: '0.625rem' }} />}
                  </th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredModels.map(model => {
                  const name = model.name || model.id
                  const modelInstalling = isInstalling(name)
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
                          {model.description || '\u2014'}
                        </div>
                      </td>

                      {/* Tags */}
                      <td>
                        <div style={{ display: 'flex', gap: '3px', flexWrap: 'wrap', maxWidth: '150px' }}>
                          {(model.tags || []).slice(0, 3).map(tag => (
                            <span key={tag} style={{
                              fontSize: '0.5625rem', padding: '1px 5px',
                              borderRadius: 'var(--radius-full)',
                              background: 'var(--color-accent-light)', color: 'var(--color-accent)',
                              border: '1px solid rgba(139, 92, 246, 0.15)',
                            }}>{tag}</span>
                          ))}
                          {(model.tags || []).length > 3 && (
                            <span style={{ fontSize: '0.5625rem', color: 'var(--color-text-muted)' }}>+{model.tags.length - 3}</span>
                          )}
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
                                {model.estimated_size_display && model.estimated_size_display !== '0 B' && model.estimated_vram_display && model.estimated_vram_display !== '0 B' && ' \u00B7 '}
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
                            <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>{'\u2014'}</span>
                          )}
                        </div>
                      </td>

                      {/* Status */}
                      <td>
                        {modelInstalling ? (
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
                              disabled={modelInstalling}
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
