import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import ResourceMonitor from '../components/ResourceMonitor'
import ConfirmDialog from '../components/ConfirmDialog'
import { useModels } from '../hooks/useModels'
import { backendControlApi, modelsApi, backendsApi, systemApi } from '../utils/api'

const TABS = [
  { key: 'models', label: 'Models', icon: 'fa-brain' },
  { key: 'backends', label: 'Backends', icon: 'fa-server' },
]

export default function Manage() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') || localStorage.getItem('manage-tab') || 'models'
  const [activeTab, setActiveTab] = useState(TABS.some(t => t.key === initialTab) ? initialTab : 'models')
  const { models, loading: modelsLoading, refetch: refetchModels } = useModels()
  const [loadedModelIds, setLoadedModelIds] = useState(new Set())
  const [backends, setBackends] = useState([])
  const [backendsLoading, setBackendsLoading] = useState(true)
  const [reloading, setReloading] = useState(false)
  const [reinstallingBackends, setReinstallingBackends] = useState(new Set())
  const [confirmDialog, setConfirmDialog] = useState(null)

  const handleTabChange = (tab) => {
    setActiveTab(tab)
    localStorage.setItem('manage-tab', tab)
    setSearchParams({ tab })
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
  }, [fetchLoadedModels, fetchBackends])

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

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">System</h1>
        <p className="page-subtitle">Manage installed models and backends</p>
      </div>

      {/* Resource Monitor */}
      <ResourceMonitor />

      {/* Tabs */}
      <div className="tabs" style={{ marginTop: 'var(--spacing-lg)', marginBottom: 'var(--spacing-md)' }}>
        {TABS.map(t => (
          <button
            key={t.key}
            className={`tab ${activeTab === t.key ? 'tab-active' : ''}`}
            onClick={() => handleTabChange(t.key)}
          >
            <i className={`fas ${t.icon}`} style={{ marginRight: 6 }} />
            {t.label}
            {t.key === 'models' && !modelsLoading && ` (${models.length})`}
            {t.key === 'backends' && !backendsLoading && ` (${backends.length})`}
          </button>
        ))}
      </div>

      {/* Models Tab */}
      {activeTab === 'models' && (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', marginBottom: 'var(--spacing-md)' }}>
          <button className="btn btn-secondary btn-sm" onClick={handleReload} disabled={reloading}>
            <i className={`fas ${reloading ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
            {reloading ? 'Updating...' : 'Update'}
          </button>
        </div>

        {modelsLoading ? (
          <div className="card" style={{ padding: 'var(--spacing-xl)', textAlign: 'center', color: 'var(--color-text-muted)' }}>
            <i className="fas fa-circle-notch fa-spin" /> Loading models...
          </div>
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
        ) : (
          <div className="table-container">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Backend</th>
                  <th>Use Cases</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {models.map(model => (
                  <tr key={model.id}>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                        <i className="fas fa-brain" style={{ color: 'var(--color-accent)' }} />
                        <span className="badge badge-success" style={{ width: 6, height: 6, padding: 0, borderRadius: '50%', minWidth: 'auto' }} />
                        <span style={{ fontWeight: 500 }}>{model.id}</span>
                        <a
                          href="#"
                          onClick={(e) => { e.preventDefault(); navigate(`/app/model-editor/${encodeURIComponent(model.id)}`) }}
                          style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}
                          title="Edit config"
                        >
                          <i className="fas fa-pen-to-square" />
                        </a>
                        <a
                          href="#"
                          onClick={(e) => { e.preventDefault(); navigate(`/app/backend-logs/${encodeURIComponent(model.id)}`) }}
                          style={{ fontSize: '0.75rem', color: 'var(--color-primary)' }}
                          title="Backend logs"
                        >
                          <i className="fas fa-terminal" />
                        </a>
                      </div>
                    </td>
                    <td>
                      {loadedModelIds.has(model.id) ? (
                        <span className="badge badge-success">
                          <i className="fas fa-circle" style={{ fontSize: '6px' }} /> Running
                        </span>
                      ) : (
                        <span className="badge" style={{ background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)' }}>
                          <i className="fas fa-circle" style={{ fontSize: '6px' }} /> Idle
                        </span>
                      )}
                    </td>
                    <td>
                      <span className="badge badge-info">{model.backend || 'Auto'}</span>
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                        <a href="#" onClick={(e) => { e.preventDefault(); navigate(`/app/chat/${encodeURIComponent(model.id)}`) }} className="badge badge-info" style={{ textDecoration: 'none', cursor: 'pointer' }}>Chat</a>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                        {loadedModelIds.has(model.id) && (
                          <button
                            className="btn btn-secondary btn-sm"
                            onClick={() => handleStopModel(model.id)}
                            title="Stop model"
                          >
                            <i className="fas fa-stop" />
                          </button>
                        )}
                        <button
                          className="btn btn-danger btn-sm"
                          onClick={() => handleDeleteModel(model.id)}
                          title="Delete model"
                        >
                          <i className="fas fa-trash" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      )}

      {/* Backends Tab */}
      {activeTab === 'backends' && (
      <div>
        {backendsLoading ? (
          <div style={{ textAlign: 'center', padding: 'var(--spacing-md)', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
            Loading backends...
          </div>
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
        ) : (
          <div className="table-container">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Metadata</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {backends.map((backend, i) => (
                  <tr key={backend.Name || i}>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                        <i className="fas fa-cog" style={{ color: 'var(--color-accent)', fontSize: '0.75rem' }} />
                        <span style={{ fontWeight: 500 }}>{backend.Name}</span>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                        {backend.IsSystem ? (
                          <span className="badge badge-info" style={{ fontSize: '0.625rem' }}>
                            <i className="fas fa-shield-alt" style={{ fontSize: '0.5rem', marginRight: 2 }} />System
                          </span>
                        ) : (
                          <span className="badge badge-success" style={{ fontSize: '0.625rem' }}>
                            <i className="fas fa-download" style={{ fontSize: '0.5rem', marginRight: 2 }} />User
                          </span>
                        )}
                        {backend.IsMeta && (
                          <span className="badge" style={{ background: 'var(--color-accent-light)', color: 'var(--color-accent)', fontSize: '0.625rem' }}>
                            <i className="fas fa-layer-group" style={{ fontSize: '0.5rem', marginRight: 2 }} />Meta
                          </span>
                        )}
                      </div>
                    </td>
                    <td>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 2, fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>
                        {backend.Metadata?.alias && (
                          <span>
                            <i className="fas fa-tag" style={{ fontSize: '0.5rem', marginRight: 4 }} />
                            Alias: <span style={{ color: 'var(--color-text-primary)' }}>{backend.Metadata.alias}</span>
                          </span>
                        )}
                        {backend.Metadata?.meta_backend_for && (
                          <span>
                            <i className="fas fa-link" style={{ fontSize: '0.5rem', marginRight: 4 }} />
                            For: <span style={{ color: 'var(--color-accent)' }}>{backend.Metadata.meta_backend_for}</span>
                          </span>
                        )}
                        {backend.Metadata?.installed_at && (
                          <span>
                            <i className="fas fa-calendar" style={{ fontSize: '0.5rem', marginRight: 4 }} />
                            {backend.Metadata.installed_at}
                          </span>
                        )}
                        {!backend.Metadata?.alias && !backend.Metadata?.meta_backend_for && !backend.Metadata?.installed_at && '—'}
                      </div>
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                        {!backend.IsSystem ? (
                          <>
                            <button
                              className="btn btn-secondary btn-sm"
                              onClick={() => handleReinstallBackend(backend.Name)}
                              disabled={reinstallingBackends.has(backend.Name)}
                              title="Reinstall"
                            >
                              <i className={`fas ${reinstallingBackends.has(backend.Name) ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
                            </button>
                            <button
                              className="btn btn-danger btn-sm"
                              onClick={() => handleDeleteBackend(backend.Name)}
                              title="Delete"
                            >
                              <i className="fas fa-trash" />
                            </button>
                          </>
                        ) : (
                          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>—</span>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
