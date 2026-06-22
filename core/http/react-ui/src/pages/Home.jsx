import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import ModelSelector from '../components/ModelSelector'
import { CAP_CHAT } from '../utils/capabilities'
import UnifiedMCPDropdown from '../components/UnifiedMCPDropdown'
import ConfirmDialog from '../components/ConfirmDialog'
import HomeConnect from '../components/HomeConnect'
import { useResources } from '../hooks/useResources'
import { fileToBase64, backendControlApi, systemApi, modelsApi, mcpApi, nodesApi } from '../utils/api'
import { API_CONFIG } from '../utils/config'
import { greetingKey } from '../utils/greeting'
import StatusPill from '../components/StatusPill'
import Skeleton from '../components/Skeleton'
import SectionHeading from '../components/SectionHeading'
import EmptyState from '../components/EmptyState'
import { staggerStyle } from '../hooks/useStagger'

export default function Home() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('home')
  const { isAdmin } = useAuth()
  const branding = useBranding()
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
  const [assistantAvailable, setAssistantAvailable] = useState(false)
  // Progressive disclosure: the big "Manage by chatting" CTA card is a
  // first-run affordance. Once the admin has clicked it, we collapse to
  // a small entry in the quick-links row so the home page doesn't keep
  // shouting at them about a feature they already know.
  const [assistantUsed, setAssistantUsed] = useState(() => {
    try { return localStorage.getItem('localai_assistant_used') === '1' } catch { return false }
  })
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [distributedMode, setDistributedMode] = useState(false)
  const [clusterData, setClusterData] = useState(null)
  const imageInputRef = useRef(null)
  const audioInputRef = useRef(null)
  const fileInputRef = useRef(null)

  // Detect distributed mode + assistant feature availability in one fetch.
  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(data => {
        setDistributedMode(!!data.distributed)
        setAssistantAvailable(!!data.localai_assistant)
      })
      .catch(() => {})
  }, [])

  // Poll cluster node data in distributed mode
  useEffect(() => {
    if (!distributedMode) return
    const fetchCluster = async () => {
      try {
        const data = await nodesApi.list()
        const nodes = Array.isArray(data) ? data : []
        const backendNodes = nodes.filter(n => !n.node_type || n.node_type === 'backend')
        const totalVRAM = backendNodes.reduce((sum, n) => sum + (n.total_vram || 0), 0)
        const usedVRAM = backendNodes.reduce((sum, n) => {
          if (n.total_vram && n.available_vram != null) return sum + (n.total_vram - n.available_vram)
          return sum
        }, 0)
        const totalRAM = backendNodes.reduce((sum, n) => sum + (n.total_ram || 0), 0)
        const usedRAM = backendNodes.reduce((sum, n) => {
          if (n.total_ram && n.available_ram != null) return sum + (n.total_ram - n.available_ram)
          return sum
        }, 0)
        const isGPU = totalVRAM > 0
        const healthyCount = backendNodes.filter(n => n.status === 'healthy').length
        const totalCount = backendNodes.length
        setClusterData({
          totalMem: isGPU ? totalVRAM : totalRAM,
          usedMem: isGPU ? usedVRAM : usedRAM,
          isGPU,
          healthyCount,
          totalCount,
        })
      } catch { setClusterData(null) }
    }
    fetchCluster()
    const interval = setInterval(fetchCluster, 5000)
    return () => clearInterval(interval)
  }, [distributedMode])

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
      const entry = { name: file.name, type: file.type, base64 }
      if (!file.type.startsWith('image/') && !file.type.startsWith('audio/')) {
        entry.textContent = await file.text().catch(() => '')
      }
      newFiles.push(entry)
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
      addToast(t('input.selectModelToast'), 'warning')
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

  // Quick-launch: open a fresh chat already in assistant mode without
  // requiring an initial message or model selection. Useful when an admin
  // wants to start the assistant from a cold home page.
  const openAssistantChat = useCallback(() => {
    const chatData = {
      model: selectedModel || '',
      mcpMode: false,
      localaiAssistant: true,
      newChat: true,
    }
    localStorage.setItem('localai_index_chat_data', JSON.stringify(chatData))
    try { localStorage.setItem('localai_assistant_used', '1') } catch { /* ignore */ }
    setAssistantUsed(true)
    navigate('/app/chat')
  }, [navigate, selectedModel])

  const handleSubmit = (e) => {
    if (e) e.preventDefault()
    doSubmit()
  }

  const handleStopModel = async (modelName) => {
    setConfirmDialog({
      title: t('stopDialog.title'),
      message: t('stopDialog.message', { model: modelName }),
      confirmLabel: t('stopDialog.confirm', { model: modelName }),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await backendControlApi.shutdown({ model: modelName })
          addToast(t('stopDialog.stoppedToast', { model: modelName }), 'success')
          setTimeout(fetchSystemInfo, 500)
        } catch (err) {
          addToast(t('stopDialog.stopFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  const handleStopAll = async () => {
    setConfirmDialog({
      title: t('stopDialog.stopAllTitle'),
      message: t('stopDialog.stopAllMessage', { count: loadedModels.length }),
      confirmLabel: t('stopDialog.stopAllConfirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await Promise.all(loadedModels.map(m => backendControlApi.shutdown({ model: m.id })))
          addToast(t('stopDialog.allStoppedToast'), 'success')
          setTimeout(fetchSystemInfo, 1000)
        } catch (err) {
          addToast(t('stopDialog.stopFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  const modelsLoading = configuredModels === null
  const hasModels = modelsLoading || configuredModels.length > 0
  const loadedCount = loadedModels.length

  // Resource display - folded into the editorial status line.
  const resType = resources?.type
  const usagePct = resources?.aggregate?.usage_percent ?? resources?.ram?.usage_percent ?? 0

  return (
    <div className="home-page">
      {hasModels ? (
        <>
          {/* Editorial header */}
          <header className="home-header reveal-stagger">
            <div style={staggerStyle(0)}>
              <span className="home-eyebrow">{branding.instanceName}</span>
              <h1 className="home-greeting">{t(`greeting.${greetingKey()}`)}</h1>
            </div>
            <div className="home-status-line" style={staggerStyle(1)}>
              <StatusPill
                status={loadedCount > 0 ? 'healthy' : 'idle'}
                label={loadedCount > 0 ? t('statusLine.modelsLoaded', { count: loadedCount }) : t('statusLine.noModelsLoaded')}
              />
              {distributedMode && clusterData && (
                <StatusPill
                  status={clusterData.healthyCount > 0 ? 'healthy' : 'error'}
                  label={t('statusLine.nodes', { count: clusterData.totalCount })}
                />
              )}
              {!distributedMode && resources && (
                <span className="status-pill">
                  <i className={`fas ${resType === 'gpu' ? 'fa-microchip' : 'fa-memory'}`} aria-hidden="true" />
                  {(resType === 'gpu' ? t('resourceGpu') : t('resourceRam'))} {usagePct.toFixed(0)}%
                </span>
              )}
            </div>
          </header>

          {/* LocalAI Assistant — prominent CTA on first run. Once the
              admin has used it, the big card collapses to a small entry in
              the quick-links row below. */}
          {isAdmin && assistantAvailable && !assistantUsed && (
            <button
              type="button"
              onClick={openAssistantChat}
              className="home-assistant-card"
            >
              <span className="home-assistant-icon"><i className="fas fa-user-shield" /></span>
              <span className="home-assistant-text">
                <span className="home-assistant-title">{t('assistant.title')}</span>
                <span className="home-assistant-desc">{t('assistant.description')}</span>
              </span>
              <span className="home-assistant-cta">
                {t('assistant.open')} <i className="fas fa-arrow-right" />
              </span>
            </button>
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
                  placeholder={t('input.placeholder')}
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
                    <button type="button" className="home-attach-btn" onClick={() => imageInputRef.current?.click()} title={t('input.attachImage')}>
                      <i className="fas fa-image" />
                    </button>
                    <button type="button" className="home-attach-btn" onClick={() => audioInputRef.current?.click()} title={t('input.attachAudio')}>
                      <i className="fas fa-microphone" />
                    </button>
                    <button type="button" className="home-attach-btn" onClick={() => fileInputRef.current?.click()} title={t('input.attachFile')}>
                      <i className="fas fa-file" />
                    </button>
                  </div>
                  <span className="home-input-hint">{t('input.enterToSend')}</span>
                  <button
                    type="submit"
                    className="home-send-btn"
                    disabled={!selectedModel}
                    title={!selectedModel ? t('input.selectModelFirst') : t('input.sendMessage')}
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
                {assistantAvailable && assistantUsed && (
                  <button
                    className="home-link-btn"
                    onClick={openAssistantChat}
                    title={t('assistant.tooltip')}
                  >
                    <i className="fas fa-user-shield" /> {t('quickLinks.manageByChat')}
                  </button>
                )}
                <button className="btn btn-primary" onClick={() => navigate('/app/models')}>
                  <i className="fas fa-download" aria-hidden="true" /> {t('quickLinks.browseGallery')}
                </button>
                <button className="home-link-btn" onClick={() => navigate('/app/manage')}>
                  <i className="fas fa-desktop" aria-hidden="true" /> {t('quickLinks.installedModels')}
                </button>
                <button className="home-link-btn" onClick={() => navigate('/app/import-model')}>
                  <i className="fas fa-upload" aria-hidden="true" /> {t('quickLinks.importModel')}
                </button>
              </>
            )}
            <a className="home-link-btn home-link-btn--quiet" href="https://localai.io" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" aria-hidden="true" /> {t('quickLinks.documentation')}
            </a>
          </div>

          {/* Loaded models status */}
          <section className="home-loaded">
            <SectionHeading>{t('loadedModels.heading')}</SectionHeading>
            {modelsLoading ? (
              <Skeleton variant="line" count={2} />
            ) : loadedCount > 0 ? (
              <>
                <ul className="home-loaded-list reveal-stagger">
                  {[...loadedModels].sort((a, b) => a.id.localeCompare(b.id)).map((m, i) => (
                    <li key={m.id} className="home-loaded-item" style={staggerStyle(i)}>
                      <StatusPill status="healthy" label={m.id} />
                      <button
                        type="button"
                        onClick={() => handleStopModel(m.id)}
                        title={t('loadedModels.stop')}
                        aria-label={t('loadedModels.stop')}
                      >
                        <i className="fas fa-times" aria-hidden="true" />
                      </button>
                    </li>
                  ))}
                </ul>
                {loadedCount > 1 && (
                  <button className="btn btn-secondary btn-sm home-stop-all" onClick={handleStopAll}>
                    {t('loadedModels.stopAll')}
                  </button>
                )}
              </>
            ) : (
              <p className="home-loaded-empty">{t('statusLine.noModelsLoaded')}</p>
            )}
          </section>
        </>
      ) : isAdmin ? (
        /* No models installed - compact getting started */
        <div className="home-wizard">
          <EmptyState
            eyebrow={branding.instanceName}
            icon="fa-rocket"
            title={t('wizard.getStarted', { name: branding.instanceName })}
            body={t('wizard.intro')}
          />

          <div className="home-wizard-steps card">
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">1</div>
              <div>
                <strong>{t('wizard.steps.step1Title')}</strong>
                <p>{t('wizard.steps.step1Body')}</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">2</div>
              <div>
                <strong>{t('wizard.steps.step2Title')}</strong>
                <p>{t('wizard.steps.step2Body')}</p>
              </div>
            </div>
            <div className="home-wizard-step">
              <div className="home-wizard-step-num">3</div>
              <div>
                <strong>{t('wizard.steps.step3Title')}</strong>
                <p>{t('wizard.steps.step3Body')}</p>
              </div>
            </div>
          </div>

          <div className="home-wizard-actions">
            <button className="btn btn-primary" onClick={() => navigate('/app/models')}>
              <i className="fas fa-store" /> {t('wizard.browseGallery')}
            </button>
            <button className="btn btn-secondary" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> {t('wizard.importModel')}
            </button>
            <a className="btn btn-secondary" href="https://localai.io/docs/getting-started" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> {t('wizard.docs')}
            </a>
          </div>
        </div>
      ) : (
        /* No models available (non-admin) */
        <div className="home-wizard">
          <div className="home-wizard-hero">
            <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="home-logo" />
            <h1>{t('wizard.noModelsTitle')}</h1>
            <p>{t('wizard.noModelsBody')}</p>
          </div>
          <div className="home-wizard-actions">
            <a className="btn btn-secondary" href="https://localai.io" target="_blank" rel="noopener noreferrer">
              <i className="fas fa-book" /> {t('quickLinks.documentation')}
            </a>
          </div>
        </div>
      )}

      <HomeConnect />

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
