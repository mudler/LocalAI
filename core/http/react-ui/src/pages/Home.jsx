import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { apiUrl } from '../utils/basePath'
import ModelSelector from '../components/ModelSelector'
import ClientMCPDropdown from '../components/ClientMCPDropdown'
import { useResources } from '../hooks/useResources'
import { fileToBase64, backendControlApi, systemApi, modelsApi, mcpApi } from '../utils/api'
import { API_CONFIG } from '../utils/config'

const placeholderMessages = [
  'What is the meaning of life?',
  'Write a poem about AI',
  'Explain quantum computing simply',
  'Help me debug my code',
  'Tell me a creative story',
  'How do neural networks work?',
  'Write a haiku about programming',
  'Explain blockchain in simple terms',
  'What are the best practices for REST APIs?',
  'Help me write a cover letter',
  'What is the Fibonacci sequence?',
  'Explain the theory of relativity',
]

export default function Home() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
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
  const [mcpServersOpen, setMcpServersOpen] = useState(false)
  const [mcpServerList, setMcpServerList] = useState([])
  const [mcpServersLoading, setMcpServersLoading] = useState(false)
  const [mcpServerCache, setMcpServerCache] = useState({})
  const [mcpSelectedServers, setMcpSelectedServers] = useState([])
  const [clientMCPSelectedIds, setClientMCPSelectedIds] = useState([])
  const mcpDropdownRef = useRef(null)
  const [placeholderIdx, setPlaceholderIdx] = useState(0)
  const [placeholderText, setPlaceholderText] = useState('')
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

  // Animated typewriter placeholder
  useEffect(() => {
    const target = placeholderMessages[placeholderIdx]
    let charIdx = 0
    setPlaceholderText('')
    const interval = setInterval(() => {
      if (charIdx <= target.length) {
        setPlaceholderText(target.slice(0, charIdx))
        charIdx++
      } else {
        clearInterval(interval)
        setTimeout(() => {
          setPlaceholderIdx(prev => (prev + 1) % placeholderMessages.length)
        }, 2000)
      }
    }, 50)
    return () => clearInterval(interval)
  }, [placeholderIdx])

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

  useEffect(() => {
    if (!mcpServersOpen) return
    const handleClick = (e) => {
      if (mcpDropdownRef.current && !mcpDropdownRef.current.contains(e.target)) {
        setMcpServersOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [mcpServersOpen])

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
    const text = message.trim() || placeholderText
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
  }, [message, placeholderText, allFiles, selectedModel, mcpMode, mcpSelectedServers, clientMCPSelectedIds, addToast, navigate])

  const handleSubmit = (e) => {
    if (e) e.preventDefault()
    doSubmit()
  }

  const handleStopModel = async (modelName) => {
    if (!confirm(`Stop model ${modelName}?`)) return
    try {
      await backendControlApi.shutdown({ model: modelName })
      addToast(`Stopped ${modelName}`, 'success')
      // Refresh loaded models list after a short delay
      setTimeout(fetchSystemInfo, 500)
    } catch (err) {
      addToast(`Failed to stop: ${err.message}`, 'error')
    }
  }

  const handleStopAll = async () => {
    if (!confirm('Stop all loaded models?')) return
    try {
      await Promise.all(loadedModels.map(m => backendControlApi.shutdown({ model: m.id })))
      addToast('All models stopped', 'success')
      setTimeout(fetchSystemInfo, 1000)
    } catch (err) {
      addToast(`Failed to stop: ${err.message}`, 'error')
    }
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
            <h1 className="home-heading">How can I help you today?</h1>
            <p className="home-subheading">Ask me anything, and I'll do my best to assist you.</p>
          </div>

          {/* Chat input form */}
          <div className="home-chat-card">
            <form onSubmit={handleSubmit}>
              {/* Model selector + MCP toggle */}
              <div className="home-model-row">
                <ModelSelector value={selectedModel} onChange={setSelectedModel} capability="FLAG_CHAT" />
                {mcpAvailable && (
                  <div className="chat-mcp-dropdown" ref={mcpDropdownRef}>
                    <button
                      type="button"
                      className={`btn btn-sm ${mcpSelectedServers.length > 0 ? 'btn-primary' : 'btn-secondary'}`}
                      title="Select MCP servers"
                      onClick={() => { setMcpServersOpen(!mcpServersOpen); if (!mcpServersOpen) fetchMcpServers() }}
                    >
                      <i className="fas fa-plug" /> MCP
                      {mcpSelectedServers.length > 0 && (
                        <span className="chat-mcp-badge">{mcpSelectedServers.length}</span>
                      )}
                    </button>
                    {mcpServersOpen && (
                      <div className="chat-mcp-dropdown-menu">
                        {mcpServersLoading ? (
                          <div className="chat-mcp-dropdown-loading"><i className="fas fa-spinner fa-spin" /> Loading servers...</div>
                        ) : mcpServerList.length === 0 ? (
                          <div className="chat-mcp-dropdown-empty">No MCP servers configured</div>
                        ) : (
                          <>
                            <div className="chat-mcp-dropdown-header">
                              <span>MCP Servers</span>
                              <button
                                type="button"
                                className="chat-mcp-select-all"
                                onClick={() => {
                                  const allNames = mcpServerList.map(s => s.name)
                                  const allSelected = allNames.every(n => mcpSelectedServers.includes(n))
                                  setMcpSelectedServers(allSelected ? [] : allNames)
                                }}
                              >
                                {mcpServerList.every(s => mcpSelectedServers.includes(s.name)) ? 'Deselect all' : 'Select all'}
                              </button>
                            </div>
                            {mcpServerList.map(server => (
                              <label key={server.name} className="chat-mcp-server-item">
                                <input
                                  type="checkbox"
                                  checked={mcpSelectedServers.includes(server.name)}
                                  onChange={() => toggleMcpServer(server.name)}
                                />
                                <div className="chat-mcp-server-info">
                                  <span className="chat-mcp-server-name">{server.name}</span>
                                  <span className="chat-mcp-server-tools">{server.tools?.length || 0} tools</span>
                                </div>
                              </label>
                            ))}
                          </>
                        )}
                      </div>
                    )}
                  </div>
                )}
                <ClientMCPDropdown
                  activeServerIds={clientMCPSelectedIds}
                  onToggleServer={(id) => setClientMCPSelectedIds(prev =>
                    prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id]
                  )}
                  onServerAdded={(server) => setClientMCPSelectedIds(prev => [...prev, server.id])}
                  onServerRemoved={(id) => setClientMCPSelectedIds(prev => prev.filter(s => s !== id))}
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

              {/* Textarea with attach buttons */}
              <div className="home-input-area">
                <textarea
                  className="home-textarea"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder={placeholderText}
                  rows={3}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      doSubmit()
                    }
                  }}
                />
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
                <input ref={imageInputRef} type="file" multiple accept="image/*" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setImageFiles)} />
                <input ref={audioInputRef} type="file" multiple accept="audio/*" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setAudioFiles)} />
                <input ref={fileInputRef} type="file" multiple accept=".txt,.md,.pdf" style={{ display: 'none' }} onChange={(e) => addFiles(e.target.files, setTextFiles)} />
              </div>

              <button
                type="submit"
                className="home-send-btn"
                disabled={!selectedModel}
              >
                <i className="fas fa-paper-plane" /> Send
              </button>
            </form>
          </div>

          {/* Quick links */}
          <div className="home-quick-links">
            <button className="home-link-btn" onClick={() => navigate('/app/manage')}>
              <i className="fas fa-desktop" /> Installed Models and Backends
            </button>
            <button className="home-link-btn" onClick={() => navigate('/app/models')}>
              <i className="fas fa-download" /> Browse Gallery
            </button>
            <button className="home-link-btn" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> Import Model
            </button>
            <a className="home-link-btn" href="https://localai.io" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Documentation
            </a>
          </div>

          {/* Compact resource indicator */}
          {resources && (
            <div className="home-resource-pill">
              <i className={`fas ${resType === 'gpu' ? 'fa-microchip' : 'fa-memory'}`} />
              <span className="home-resource-label">{resType === 'gpu' ? 'GPU' : 'RAM'}</span>
              <span className="home-resource-pct" style={{ color: pctColor }}>
                {usagePct.toFixed(0)}%
              </span>
              <div className="home-resource-bar-track">
                <div
                  className="home-resource-bar-fill"
                  style={{ width: `${usagePct}%`, background: pctColor }}
                />
              </div>
            </div>
          )}

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
      ) : (
        /* No models installed wizard */
        <div className="home-wizard">
          <div className="home-wizard-hero">
            <h1>No Models Installed</h1>
            <p>Get started with LocalAI by installing your first model. Browse our gallery of open-source AI models.</p>
          </div>

          {/* Feature preview cards */}
          <div className="home-wizard-features">
            <div className="home-wizard-feature">
              <div className="home-wizard-feature-icon" style={{ background: 'var(--color-primary-light)' }}>
                <i className="fas fa-images" style={{ color: 'var(--color-primary)' }} />
              </div>
              <h3>Model Gallery</h3>
              <p>Browse and install from a curated collection of open-source AI models</p>
            </div>
            <div className="home-wizard-feature" onClick={() => navigate('/app/import-model')} style={{ cursor: 'pointer' }}>
              <div className="home-wizard-feature-icon" style={{ background: 'var(--color-accent-light)' }}>
                <i className="fas fa-upload" style={{ color: 'var(--color-accent)' }} />
              </div>
              <h3>Import Models</h3>
              <p>Import your own models from HuggingFace or local files</p>
            </div>
            <div className="home-wizard-feature">
              <div className="home-wizard-feature-icon" style={{ background: 'var(--color-success-light)' }}>
                <i className="fas fa-code" style={{ color: 'var(--color-success)' }} />
              </div>
              <h3>API Download</h3>
              <p>Use the API to download and configure models programmatically</p>
            </div>
          </div>

          {/* Setup steps */}
          <div className="home-wizard-steps card">
            <h2>How to Get Started</h2>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">1</div>
              <div>
                <strong>Browse the Model Gallery</strong>
                <p>Visit the model gallery to find the right model for your needs.</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">2</div>
              <div>
                <strong>Install a Model</strong>
                <p>Click install on any model to download and configure it automatically.</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">3</div>
              <div>
                <strong>Start Chatting</strong>
                <p>Once installed, you can chat with your model right from the browser.</p>
              </div>
            </div>
          </div>

          {/* Action buttons */}
          <div className="home-wizard-actions">
            <button className="btn btn-primary" onClick={() => navigate('/app/models')}>
              <i className="fas fa-store" /> Browse Model Gallery
            </button>
            <button className="btn btn-secondary" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> Import Model
            </button>
            <a className="btn btn-secondary" href="https://localai.io/docs/getting-started" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> Getting Started
            </a>
          </div>
        </div>
      )}

      <style>{`
        .home-page {
          flex: 1;
          display: flex;
          flex-direction: column;
          align-items: center;
          justify-content: center;
          max-width: 48rem;
          margin: 0 auto;
          padding: var(--spacing-xl);
          width: 100%;
        }
        .home-hero {
          text-align: center;
          padding: var(--spacing-lg) 0;
        }
        .home-logo {
          width: 80px;
          height: auto;
          margin: 0 auto var(--spacing-md);
          display: block;
        }
        .home-heading {
          font-size: 1.5rem;
          font-weight: 600;
          margin-bottom: var(--spacing-xs);
        }
        .home-subheading {
          font-size: 0.875rem;
          color: var(--color-text-secondary);
        }

        /* Chat card */
        .home-chat-card {
          width: 100%;
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-lg);
          padding: var(--spacing-md);
          margin-bottom: var(--spacing-md);
        }
        .home-model-row {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          margin-bottom: var(--spacing-sm);
        }
        .home-file-tags {
          display: flex;
          flex-wrap: wrap;
          gap: var(--spacing-xs);
          margin-bottom: var(--spacing-sm);
        }
        .home-file-tag {
          display: inline-flex;
          align-items: center;
          gap: 4px;
          padding: 2px 8px;
          background: var(--color-bg-tertiary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-full);
          font-size: 0.75rem;
          color: var(--color-text-secondary);
        }
        .home-file-tag button {
          background: none;
          border: none;
          color: var(--color-text-muted);
          cursor: pointer;
          padding: 0;
          font-size: 0.625rem;
        }
        .home-input-area {
          position: relative;
          margin-bottom: var(--spacing-sm);
        }
        .home-textarea {
          width: 100%;
          background: var(--color-bg-tertiary);
          color: var(--color-text-primary);
          border: 1px solid var(--color-border-default);
          border-radius: var(--radius-md);
          padding: var(--spacing-sm) var(--spacing-md);
          padding-right: 7rem;
          font-size: 0.875rem;
          font-family: inherit;
          outline: none;
          resize: none;
          min-height: 80px;
          transition: border-color var(--duration-fast);
        }
        .home-textarea:focus { border-color: var(--color-border-strong); }
        .home-attach-buttons {
          position: absolute;
          right: var(--spacing-sm);
          bottom: var(--spacing-sm);
          display: flex;
          gap: 4px;
        }
        .home-attach-btn {
          background: none;
          border: none;
          color: var(--color-text-muted);
          cursor: pointer;
          padding: 4px 6px;
          font-size: 0.875rem;
          border-radius: var(--radius-sm);
          transition: color var(--duration-fast);
        }
        .home-attach-btn:hover { color: var(--color-primary); }
        .home-send-btn {
          display: flex;
          align-items: center;
          gap: var(--spacing-xs);
          padding: var(--spacing-sm) var(--spacing-lg);
          background: var(--color-primary);
          color: var(--color-primary-text);
          border: none;
          border-radius: var(--radius-md);
          font-size: 0.875rem;
          font-family: inherit;
          cursor: pointer;
          margin-left: auto;
          transition: background var(--duration-fast);
        }
        .home-send-btn:hover:not(:disabled) { background: var(--color-primary-hover); }
        .home-send-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        /* Quick links */
        .home-quick-links {
          display: flex;
          flex-wrap: wrap;
          gap: var(--spacing-sm);
          justify-content: center;
          margin: var(--spacing-md) 0;
        }
        .home-link-btn {
          display: inline-flex;
          align-items: center;
          gap: var(--spacing-xs);
          padding: var(--spacing-xs) var(--spacing-md);
          background: var(--color-bg-tertiary);
          color: var(--color-text-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-full);
          font-size: 0.8125rem;
          font-family: inherit;
          cursor: pointer;
          text-decoration: none;
          transition: all var(--duration-fast);
        }
        .home-link-btn:hover {
          border-color: var(--color-primary-border);
          color: var(--color-primary);
        }

        /* Resource pill */
        .home-resource-pill {
          display: flex;
          align-items: center;
          gap: var(--spacing-xs);
          padding: var(--spacing-xs) var(--spacing-sm);
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-full);
          font-size: 0.75rem;
          color: var(--color-text-secondary);
          margin: var(--spacing-sm) 0;
        }
        .home-resource-label {
          font-weight: 500;
        }
        .home-resource-pct {
          font-family: 'JetBrains Mono', monospace;
          font-weight: 500;
        }
        .home-resource-bar-track {
          width: 16px;
          height: 6px;
          background: var(--color-bg-tertiary);
          border-radius: 3px;
          overflow: hidden;
        }
        .home-resource-bar-fill {
          height: 100%;
          border-radius: 3px;
          transition: width 500ms ease;
        }

        /* Loaded models */
        .home-loaded-models {
          display: flex;
          flex-wrap: wrap;
          align-items: center;
          gap: var(--spacing-xs);
          padding: var(--spacing-sm);
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-lg);
          font-size: 0.8125rem;
          color: var(--color-text-secondary);
          width: 100%;
        }
        .home-loaded-dot {
          width: 6px;
          height: 6px;
          border-radius: 50%;
          background: var(--color-success);
        }
        .home-loaded-text {
          font-weight: 500;
          margin-right: var(--spacing-xs);
        }
        .home-loaded-list {
          display: flex;
          flex-wrap: wrap;
          gap: var(--spacing-xs);
        }
        .home-loaded-item {
          display: inline-flex;
          align-items: center;
          gap: 4px;
          padding: 2px 8px;
          background: var(--color-bg-tertiary);
          border-radius: var(--radius-full);
          font-size: 0.75rem;
        }
        .home-loaded-item button {
          background: none;
          border: none;
          color: var(--color-error);
          cursor: pointer;
          padding: 0;
          font-size: 0.625rem;
        }
        .home-stop-all {
          margin-left: auto;
          background: none;
          border: 1px solid var(--color-error);
          color: var(--color-error);
          padding: 2px 8px;
          border-radius: var(--radius-full);
          font-size: 0.75rem;
          cursor: pointer;
          font-family: inherit;
        }

        /* No models wizard */
        .home-wizard {
          max-width: 48rem;
          width: 100%;
        }
        .home-wizard-hero {
          text-align: center;
          padding: var(--spacing-xl) 0;
        }
        .home-wizard-hero h1 {
          font-size: 1.5rem;
          font-weight: 600;
          margin-bottom: var(--spacing-sm);
        }
        .home-wizard-hero p {
          color: var(--color-text-secondary);
          font-size: 0.9375rem;
        }
        .home-wizard-features {
          display: grid;
          grid-template-columns: repeat(3, 1fr);
          gap: var(--spacing-md);
          margin-bottom: var(--spacing-xl);
        }
        .home-wizard-feature {
          text-align: center;
          padding: var(--spacing-md);
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-lg);
        }
        .home-wizard-feature-icon {
          width: 48px;
          height: 48px;
          border-radius: 50%;
          display: flex;
          align-items: center;
          justify-content: center;
          margin: 0 auto var(--spacing-sm);
          font-size: 1.25rem;
        }
        .home-wizard-feature h3 {
          font-size: 0.9375rem;
          font-weight: 600;
          margin-bottom: var(--spacing-xs);
        }
        .home-wizard-feature p {
          font-size: 0.8125rem;
          color: var(--color-text-secondary);
          line-height: 1.4;
        }
        .home-wizard-steps {
          margin-bottom: var(--spacing-xl);
        }
        .home-wizard-steps h2 {
          font-size: 1.125rem;
          font-weight: 600;
          margin-bottom: var(--spacing-md);
        }
        .home-wizard-step {
          display: flex;
          gap: var(--spacing-md);
          align-items: flex-start;
          padding: var(--spacing-sm) 0;
        }
        .home-wizard-step-num {
          width: 28px;
          height: 28px;
          border-radius: 50%;
          background: var(--color-primary);
          color: white;
          display: flex;
          align-items: center;
          justify-content: center;
          font-size: 0.8125rem;
          font-weight: 600;
          flex-shrink: 0;
        }
        .home-wizard-step strong {
          display: block;
          margin-bottom: 2px;
        }
        .home-wizard-step p {
          font-size: 0.8125rem;
          color: var(--color-text-secondary);
          margin: 0;
        }
        .home-wizard-actions {
          display: flex;
          gap: var(--spacing-sm);
          justify-content: center;
        }
        @media (max-width: 640px) {
          .home-wizard-features {
            grid-template-columns: 1fr;
          }
        }
      `}</style>
    </div>
  )
}
