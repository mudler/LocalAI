import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { apiUrl } from '../utils/basePath'
import { useAuth } from '../context/AuthContext'
import ModelSelector from '../components/ModelSelector'
import { CAP_CHAT } from '../utils/capabilities'
import UnifiedMCPDropdown from '../components/UnifiedMCPDropdown'
import ConfirmDialog from '../components/ConfirmDialog'
import { useResources } from '../hooks/useResources'
import { fileToBase64, backendControlApi, systemApi, modelsApi, mcpApi } from '../utils/api'
import { API_CONFIG } from '../utils/config'

export default function Home() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { isAdmin } = useAuth()
  const { resources } = useResources()
  const [configuredModels, setConfiguredModels] = useState(null)
  const configuredModelsRef = useRef(configuredModels)
  configuredModelsRef.current = configuredModels
  const [loadedModels, setLoadedModels] = useState([])
  const [selectedModel, setSelectedModel] = useState('')
  const [message, setMessage] = useState('')
  const [imageFiles, setImageFiles] = useState([])
  const [audioFiles, setAudioFiles] = useState([])
  const [textFiles, setTextFiles] = useState([])
  const [mcpMode, setMcpMode] = useState(false)
  const [mcpAvailable, setMcpAvailable] = useState(false)
  const [mcpServerList, setMcpServerList] = useState([])
  const [mcpServersLoading, setMcpServersLoading] = useState(false)
  const [mcpServerCache, setMcpServerCache] = useState({})
  const [mcpSelectedServers, setMcpSelectedServers] = useState([])
  const [clientMCPSelectedIds, setClientMCPSelectedIds] = useState([])
  const [confirmDialog, setConfirmDialog] = useState(null)
  const imageInputRef = useRef(null)
  const audioInputRef = useRef(null)
  const fileInputRef = useRef(null)

  // Fetch configured models (to know if any exist) and loaded models (currently running)
  const fetchSystemInfo = useCallback(async () => {
    try {
      const [sysInfo, v1Models] = await Promise.all([
        systemApi.info().catch(() => null),
        modelsApi.listV1().catch(() => null),
      ])
      if (sysInfo?.loaded_models) {
        setLoadedModels(sysInfo.loaded_models)
      }
      if (v1Models?.data) {
        setConfiguredModels(v1Models.data)
      } else if (configuredModelsRef.current === null) {
        setConfiguredModels([])
      }
    } catch (_e) {
      if (configuredModelsRef.current === null) setConfiguredModels([])
    }
  }, [])

  useEffect(() => {
    fetchSystemInfo()
    const interval = setInterval(fetchSystemInfo, 5000)
    return () => clearInterval(interval)
  }, [fetchSystemInfo])

  // Check MCP availability when selected model changes
  useEffect(() => {
    if (!selectedModel) {
      setMcpAvailable(false)
      setMcpMode(false)
      setMcpSelectedServers([])
      return
    }
    let cancelled = false
    modelsApi.getConfigJson(selectedModel).then(cfg => {
      if (cancelled) return
      const hasMcp = !!(cfg?.mcp?.remote || cfg?.mcp?.stdio)
      setMcpAvailable(hasMcp)
      if (!hasMcp) {
        setMcpMode(false)
        setMcpSelectedServers([])
      }
    }).catch(() => {
      if (!cancelled) {
        setMcpAvailable(false)
        setMcpMode(false)
        setMcpSelectedServers([])
      }
    })
    return () => { cancelled = true }
  }, [selectedModel])

  const allFiles = [...imageFiles, ...audioFiles, ...textFiles]

  const addFiles = useCallback(async (fileList, setter) => {
    const newFiles = []
    for (const file of fileList) {
      const base64 = await fileToBase64(file)
      newFiles.push({ name: file.name, type: file.type, base64 })
    }
    setter(prev => [...prev, ...newFiles])
  }, [])

  const removeFile = useCallback((file) => {
    const removeFn = (prev) => prev.filter(f => f !== file)
    if (file.type?.startsWith('image/')) setImageFiles(removeFn)
    else if (file.type?.startsWith('audio/')) setAudioFiles(removeFn)
    else setTextFiles(removeFn)
  }, [])

  const fetchMcpServers = useCallback(async () => {
    if (!selectedModel) return
    if (mcpServerCache[selectedModel]) {
      setMcpServerList(mcpServerCache[selectedModel])
      return
    }
    setMcpServersLoading(true)
    try {
      const data = await mcpApi.listServers(selectedModel)
      const servers = data?.servers || []
      setMcpServerList(servers)
      setMcpServerCache(prev => ({ ...prev, [selectedModel]: servers }))
    } catch (_e) {
      setMcpServerList([])
    } finally {
      setMcpServersLoading(false)
    }
  }, [selectedModel, mcpServerCache])

  const toggleMcpServer = useCallback((serverName) => {
    setMcpSelectedServers(prev =>
      prev.includes(serverName) ? prev.filter(s => s !== serverName) : [...prev, serverName]
    )
  }, [])

  const doSubmit = useCallback(() => {
    const text = message.trim()
    if (!text && allFiles.length === 0) return
    if (!selectedModel) {
      addToast('Please select a model first', 'warning')
      return
    }

    const chatData = {
      message: text,
      model: selectedModel,
      files: allFiles,
      mcpMode,
      mcpServers: mcpSelectedServers,
      clientMCPServers: clientMCPSelectedIds,
      newChat: true,
    }
    localStorage.setItem('localai_index_chat_data', JSON.stringify(chatData))
    navigate(`/app/chat/${encodeURIComponent(selectedModel)}`)
  }, [message, allFiles, selectedModel, mcpMode, mcpSelectedServers, clientMCPSelectedIds, addToast, navigate])

  const handleSubmit = (e) => {
    if (e) e.preventDefault()
    doSubmit()
  }

  const handleStopModel = async (modelName) => {
    setConfirmDialog({
      title: 'Stop Model',
      message: `Stop model ${modelName}?`,
      confirmLabel: `Stop ${modelName}`,
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await backendControlApi.shutdown({ model: modelName })
          addToast(`Stopped ${modelName}`, 'success')
          setTimeout(fetchSystemInfo, 500)
        } catch (err) {
          addToast(`Failed to stop: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleStopAll = async () => {
    setConfirmDialog({
      title: 'Stop All Models',
      message: `Stop all ${loadedModels.length} loaded models?`,
      confirmLabel: 'Stop all',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await Promise.all(loadedModels.map(m => backendControlApi.shutdown({ model: m.id })))
          addToast('All models stopped', 'success')
          setTimeout(fetchSystemInfo, 1000)
        } catch (err) {
          addToast(`Failed to stop: ${err.message}`, 'error')
        }
      },
    })
  }

  const modelsLoading = configuredModels === null
  const hasModels = modelsLoading || configuredModels.length > 0
  const loadedCount = loadedModels.length

  // Resource display
  const resType = resources?.type
  const usagePct = resources?.aggregate?.usage_percent ?? resources?.ram?.usage_percent ?? 0
  const pctColor = usagePct > 90 ? 'var(--color-error)' : usagePct > 70 ? 'var(--color-warning)' : 'var(--color-success)'

  return (
    <div className="home-page">
      {hasModels ? (
        <>
          {/* Hero with logo */}
          <div className="home-hero">
            <img src={apiUrl('/static/logo.png')} alt="LocalAI" className="home-logo" />
          </div>

          {/* Resource monitor - prominent placement */}
          {resources && (
            <div className="home-resource-bar">
              <div className="home-resource-bar-header">
                <i className={`fas ${resType === 'gpu' ? 'fa-microchip' : 'fa-memory'}`} />
                <span className="home-resource-label">{resType === 'gpu' ? 'GPU' : 'RAM'}</span>
                <span className="home-resource-pct" style={{ color: pctColor }}>
                  {usagePct.toFixed(0)}%
                </span>
              </div>
              <div className="home-resource-track">
                <div
                  className="home-resource-fill"
                  style={{ width: `${usagePct}%`, background: pctColor }}
                />
              </div>
            </div>
          )}

          {/* Chat input form */}
          <div className="home-chat-card">
            <form onSubmit={handleSubmit}>
              {/* Model selector + MCP toggle */}
              <div className="home-model-row">
                <ModelSelector value={selectedModel} onChange={setSelectedModel} capability={CAP_CHAT} />
                <UnifiedMCPDropdown
                  serverMCPAvailable={mcpAvailable}
                  mcpServerList={mcpServerList}
                  mcpServersLoading={mcpServersLoading}
                  selectedServers={mcpSelectedServers}
                  onToggleServer={toggleMcpServer}
                  onSelectAllServers={() => {
                    const allNames = mcpServerList.map(s => s.name)
                    const allSelected = allNames.every(n => mcpSelectedServers.includes(n))
                    setMcpSelectedServers(allSelected ? [] : allNames)
                  }}
                  onFetchServers={fetchMcpServers}
                  clientMCPActiveIds={clientMCPSelectedIds}
                  onClientToggle={(id) => setClientMCPSelectedIds(prev =>
                    prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id]
                  )}
                  onClientAdded={(server) => setClientMCPSelectedIds(prev => [...prev, server.id])}
                  onClientRemoved={(id) => setClientMCPSelectedIds(prev => prev.filter(s => s !== id))}
                />
              </div>

              {/* File attachment tags */}
              {allFiles.length > 0 && (
                <div className="home-file-tags">
                  {allFiles.map((f, i) => (
                    <span key={i} className="home-file-tag">
                      <i className={`fas ${f.type?.startsWith('image/') ? 'fa-image' : f.type?.startsWith('audio/') ? 'fa-microphone' : 'fa-file'}`} />
                      {f.name}
                      <button type="button" onClick={() => removeFile(f)}>
                        <i className="fas fa-times" />
                      </button>
                    </span>
                  ))}
                </div>
              )}

              {/* Input container with inline send */}
              <div className="home-input-container">
                <textarea
                  className="home-textarea"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder="Message..."
                  rows={3}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      doSubmit()
                    }
                  }}
                />
                <div className="home-input-footer">
                  <div className="home-attach-buttons">
                    <button type="button" className="home-attach-btn" onClick={() => imageInputRef.current?.click()} title="Attach image">
                      <i className="fas fa-image" />
                    </button>
                    <button type="button" className="home-attach-btn" onClick={() => audioInputRef.current?.click()} title="Attach audio">
                      <i className="fas fa-microphone" />
                    </button>
                    <button type="button" className="home-attach-btn" onClick={() => fileInputRef.current?.click()} title="Attach file">
                      <i className="fas fa-file" />
                    </button>
                  </div>
                  <span className="home-input-hint">Enter to send</span>
                  <button
                    type="submit"
                    className="home-send-btn"
                    disabled={!selectedModel}
                    title={!selectedModel ? 'Select a model first' : 'Send message'}
                  >
                    <i className="fas fa-arrow-up" />
                  </button>
                </div>
                <input ref={imageInputRef} type="file" multiple accept="image/*" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setImageFiles)} />
                <input ref={audioInputRef} type="file" multiple accept="audio/*" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setAudioFiles)} />
                <input ref={fileInputRef} type="file" multiple accept=".txt,.md,.pdf" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setTextFiles)} />
              </div>
            </form>
          </div>

          {/* Quick links */}
          <div className="home-quick-links">
            {isAdmin && (
              <>
                <button className="home-link-btn" onClick={() => navigate('/app/manage')}>
                  <i className="fas fa-desktop" /> Installed Models
                </button>
                <button className="home-link-btn" onClick={() => navigate('/app/models')}>
                  <i className="fas fa-download" /> Browse Gallery
                </button>
                <button className="home-link-btn" onClick={() => navigate('/app/import-model')}>
                  <i className="fas fa-upload" /> Import Model
                </button>
              </>
            )}
            <a className="home-link-btn" href="https://localai.io" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Documentation
            </a>
          </div>

          {/* Loaded models status */}
          {loadedCount > 0 && (
            <div className="home-loaded-models">
              <span className="home-loaded-dot" />
              <span className="home-loaded-text">{loadedCount} model{loadedCount !== 1 ? 's' : ''} loaded</span>
              <div className="home-loaded-list">
                {loadedModels.map(m => (
                  <span key={m.id} className="home-loaded-item">
                    {m.id}
                    <button onClick={() => handleStopModel(m.id)} title="Stop model">
                      <i className="fas fa-times" />
                    </button>
                  </span>
                ))}
              </div>
              {loadedCount > 1 && (
                <button className="home-stop-all" onClick={handleStopAll}>
                  Stop all
                </button>
              )}
            </div>
          )}
        </>
      ) : isAdmin ? (
        /* No models installed - compact getting started */
        <div className="home-wizard">
          <div className="home-wizard-hero">
            <img src={apiUrl('/static/logo.png')} alt="LocalAI" className="home-logo" />
            <h1>Get started with LocalAI</h1>
            <p>Install your first model to begin. Browse the gallery or import your own.</p>
          </div>

          <div className="home-wizard-steps card">
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">1</div>
              <div>
                <strong>Browse the Model Gallery</strong>
                <p>Find the right model for your needs from our curated collection.</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">2</div>
              <div>
                <strong>Install a Model</strong>
                <p>Click install to download and configure it automatically.</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">3</div>
              <div>
                <strong>Start Chatting</strong>
                <p>Chat with your model right from the browser or use the API.</p>
              </div>
            </div>
          </div>

          <div className="home-wizard-actions">
            <button className="btn btn-primary" onClick={() => navigate('/app/models')}>
              <i className="fas fa-store" /> Browse Model Gallery
            </button>
            <button className="btn btn-secondary" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> Import Model
            </button>
            <a className="btn btn-secondary" href="https://localai.io/docs/getting-started" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Docs
            </a>
          </div>
        </div>
      ) : (
        /* No models available (non-admin) */
        <div className="home-wizard">
          <div className="home-wizard-hero">
            <img src={apiUrl('/static/logo.png')} alt="LocalAI" className="home-logo" />
            <h1>No Models Available</h1>
            <p>There are no models installed yet. Ask your administrator to set up models so you can start chatting.</p>
          </div>
          <div className="home-wizard-actions">
            <a className="btn btn-secondary" href="https://localai.io" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Documentation
            </a>
          </div>
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
