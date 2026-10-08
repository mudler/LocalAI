import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { CAP_CHAT } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import UnifiedMCPDropdown from '../components/UnifiedMCPDropdown'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import HomeConnect from '../components/HomeConnect'
// eslint-disable-next-line no-unused-vars
import HomeComposer from '../components/home/HomeComposer'
// eslint-disable-next-line no-unused-vars
import HomeModelPicker from '../components/home/HomeModelPicker'
// eslint-disable-next-line no-unused-vars
import HomeMemoryStrip from '../components/home/HomeMemoryStrip'
// eslint-disable-next-line no-unused-vars
import HomeResume from '../components/home/HomeResume'
// eslint-disable-next-line no-unused-vars
import HomeFirstRun from '../components/home/HomeFirstRun'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
import { useResources } from '../hooks/useResources'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { fileToBase64, backendControlApi, systemApi, modelsApi, mcpApi, nodesApi } from '../utils/api'
import { readAttachmentText } from '../utils/pdf'
import { greetingKey } from '../utils/greeting'
import {
  listConversations, setActiveConversation, removeConversation,
} from '../utils/homeConversations'
// eslint-disable-next-line no-unused-vars
import Skeleton from '../components/Skeleton'
import { staggerStyle } from '../hooks/useStagger'
import Icon from '../components/Icon'

const DOCS_URL = 'https://localai.io'
const ASSISTANT_TIP_KEY = 'localai_assistant_tip_dismissed'
// How long a row stays visible while it leaves, before the undo toast takes over.
const LEAVE_MS = 180

function isTyping(el) {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

function reducedMotion() {
  try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches } catch { return false }
}

export default function Home() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t, i18n } = useTranslation('home')
  const { isAdmin } = useAuth()
  const branding = useBranding()
  const { resources } = useResources()
  const { operations } = useOperations()
  const [configuredModels, setConfiguredModels] = useState(null)
  const [modelsFailed, setModelsFailed] = useState(false)
  const configuredModelsRef = useRef(configuredModels)
  configuredModelsRef.current = configuredModels
  const [loadedModels, setLoadedModels] = useState([])
  const [selectedModel, setSelectedModel] = useState('')
  const [message, setMessage] = useState('')
  const [sending, setSending] = useState(false)
  const [imageFiles, setImageFiles] = useState([])
  const [audioFiles, setAudioFiles] = useState([])
  const [textFiles, setTextFiles] = useState([])
  const [mcpMode, setMcpMode] = useState(false)
  const [mcpAvailable, setMcpAvailable] = useState(false)
  const [mcpServerList, setMcpServerList] = useState([])
  const [mcpServersLoading, setMcpServersLoading] = useState(false)
  const [mcpServerListError, setMcpServerListError] = useState('')
  const [mcpSelectedServers, setMcpSelectedServers] = useState([])
  const [clientMCPSelectedIds, setClientMCPSelectedIds] = useState([])
  const [assistantAvailable, setAssistantAvailable] = useState(false)
  // Progressive disclosure: the assistant line is a first-run affordance. Once
  // the admin has used it, or dismissed the line, it moves into the Library row
  // and the /assistant action.
  const [assistantUsed, setAssistantUsed] = useState(() => {
    try { return localStorage.getItem('localai_assistant_used') === '1' } catch { return false }
  })
  const [tipDismissed, setTipDismissed] = useState(() => {
    try { return localStorage.getItem(ASSISTANT_TIP_KEY) === '1' } catch { return false }
  })
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [distributedMode, setDistributedMode] = useState(false)
  const [clusterData, setClusterData] = useState(null)
  const [stripOpen, setStripOpen] = useState(false)
  const [conversations, setConversations] = useState(() => listConversations())
  const [leavingId, setLeavingId] = useState(null)
  const [pendingDelete, setPendingDelete] = useState(null)
  const textareaRef = useRef(null)
  const pickerRef = useRef(null)
  const stripRef = useRef(null)
  const pendingRef = useRef(null)
  const leaveTimer = useRef(null)

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

  // Poll cluster node data in distributed mode. Visibility-aware + gated on
  // distributedMode so a non-distributed or backgrounded tab makes no calls.
  const fetchCluster = useCallback(async () => {
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
      const perNode = backendNodes.map(n => {
        const total = isGPU ? (n.total_vram || 0) : (n.total_ram || 0)
        const avail = isGPU ? n.available_vram : n.available_ram
        return {
          id: n.id || n.name,
          name: n.name || n.id,
          total,
          used: total && avail != null ? total - avail : 0,
          healthy: n.status === 'healthy',
        }
      })
      setClusterData({
        totalMem: isGPU ? totalVRAM : totalRAM,
        usedMem: isGPU ? usedVRAM : usedRAM,
        isGPU,
        healthyCount,
        totalCount,
        nodes: perNode,
      })
    } catch { setClusterData(null) }
  }, [])
  usePolling(fetchCluster, 5000, { enabled: distributedMode })

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
        setModelsFailed(false)
      } else if (configuredModelsRef.current === null) {
        // Nothing answered and there is nothing to show yet. An empty list
        // would read as "no models installed" and send a working install to
        // the first-run steps, so it is an error until a reply arrives.
        setModelsFailed(true)
      }
    } catch {
      if (configuredModelsRef.current === null) setModelsFailed(true)
    }
  }, [])

  usePolling(fetchSystemInfo, 5000)

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

  const allFiles = useMemo(
    () => [...imageFiles, ...audioFiles, ...textFiles],
    [imageFiles, audioFiles, textFiles],
  )

  const addFiles = useCallback(async (fileList, setter) => {
    const newFiles = []
    for (const file of fileList) {
      const base64 = await fileToBase64(file)
      const entry = { name: file.name, type: file.type, base64 }
      if (!file.type.startsWith('image/') && !file.type.startsWith('audio/')) {
        try {
          entry.textContent = await readAttachmentText(file)
        } catch {
          addToast(t('input.pdfReadFailed', { name: file.name }), 'error')
          continue
        }
      }
      newFiles.push(entry)
    }
    setter(prev => [...prev, ...newFiles])
  }, [addToast, t])

  const attach = useCallback((kind, files) => {
    addFiles(files, kind === 'image' ? setImageFiles : kind === 'audio' ? setAudioFiles : setTextFiles)
  }, [addFiles])

  const removeFile = useCallback((file) => {
    const removeFn = (prev) => prev.filter(f => f !== file)
    if (file.type?.startsWith('image/')) setImageFiles(removeFn)
    else if (file.type?.startsWith('audio/')) setAudioFiles(removeFn)
    else setTextFiles(removeFn)
  }, [])

  const fetchMcpServers = useCallback(async () => {
    if (!selectedModel) return
    setMcpServersLoading(true)
    setMcpServerListError('')
    try {
      const data = await mcpApi.listServers(selectedModel)
      const servers = data?.servers || []
      setMcpServerList(servers)
      const unavailable = new Set(servers.filter(server => server.error).map(server => server.name))
      setMcpSelectedServers(prev => prev.filter(name => !unavailable.has(name)))
    } catch (e) {
      setMcpServerList([])
      setMcpServerListError(e.body?.message || e.message || 'Failed to discover MCP servers')
    } finally {
      setMcpServersLoading(false)
    }
  }, [selectedModel])

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
    setSending(true)
    navigate(`/app/chat/${encodeURIComponent(selectedModel)}`)
  }, [message, allFiles, selectedModel, mcpMode, mcpSelectedServers, clientMCPSelectedIds, addToast, navigate, t])

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

  // An empty new chat: Chat opens a fresh conversation on the chosen model.
  const openNewChat = useCallback(() => {
    localStorage.setItem('localai_index_chat_data', JSON.stringify({ model: selectedModel || '', mcpMode: false, newChat: true }))
    navigate('/app/chat')
  }, [navigate, selectedModel])

  const dismissTip = useCallback(() => {
    try { localStorage.setItem(ASSISTANT_TIP_KEY, '1') } catch { /* ignore */ }
    setTipDismissed(true)
  }, [])

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

  // ----- Conversations -----------------------------------------------------

  const refreshConversations = useCallback(() => setConversations(listConversations()), [])

  // Another tab can write chats, and Chat writes them after a short delay. Read
  // again when the tab changes or comes back into focus.
  useEffect(() => {
    const onStorage = (e) => { if (!e.key || e.key === 'localai_chats_data') refreshConversations() }
    window.addEventListener('storage', onStorage)
    window.addEventListener('focus', refreshConversations)
    return () => {
      window.removeEventListener('storage', onStorage)
      window.removeEventListener('focus', refreshConversations)
    }
  }, [refreshConversations])

  // A delete is final when its undo time ends. Until then the chat stays in
  // storage and only the row is hidden.
  const commitDelete = useCallback(() => {
    const id = pendingRef.current
    if (!id) return
    pendingRef.current = null
    removeConversation(id)
    setPendingDelete(null)
    refreshConversations()
  }, [refreshConversations])

  const commitRef = useRef(commitDelete)
  commitRef.current = commitDelete
  useEffect(() => () => {
    clearTimeout(leaveTimer.current)
    commitRef.current()
  }, [])

  const requestDelete = useCallback((conv) => {
    commitDelete()
    clearTimeout(leaveTimer.current)
    const hide = () => {
      pendingRef.current = conv.id
      setLeavingId(null)
      setPendingDelete({ id: conv.id, title: conv.title })
    }
    if (reducedMotion()) {
      hide()
    } else {
      setLeavingId(conv.id)
      leaveTimer.current = setTimeout(hide, LEAVE_MS)
    }
  }, [commitDelete])

  const undoDelete = useCallback(() => {
    pendingRef.current = null
    setPendingDelete(null)
  }, [])

  const resumeConversation = useCallback((conv) => {
    commitDelete()
    if (setActiveConversation(conv.id)) navigate('/app/chat')
    else refreshConversations()
  }, [commitDelete, navigate, refreshConversations])

  const visibleConversations = useMemo(
    () => conversations.filter(c => c.id !== pendingDelete?.id),
    [conversations, pendingDelete],
  )

  // ----- Derived state -----------------------------------------------------

  const modelsLoading = configuredModels === null && !modelsFailed
  const hasModels = configuredModels === null || configuredModels.length > 0
  const firstRun = configuredModels !== null && configuredModels.length === 0
  const loadedCount = loadedModels.length
  const loadedIds = useMemo(() => new Set(loadedModels.map(m => m.id)), [loadedModels])

  // Staging a model onto a worker is the one load this page can see: the
  // operations list reports it. An OOM on a single host surfaces in Chat.
  const stagingOp = operations.find(op => op.taskType === 'staging' && !op.error && !op.isQueued && !op.isDeletion)
  const failedOp = operations.find(op => op.taskType === 'staging' && op.error)
  const stripBusy = !!stagingOp || !!failedOp
  useEffect(() => { if (stripBusy) setStripOpen(true) }, [stripBusy])

  const slashContext = useMemo(
    () => ({ isAdmin, assistantAvailable, hasModels: hasModels && !firstRun, loadedCount }),
    [isAdmin, assistantAvailable, hasModels, firstRun, loadedCount],
  )

  const runAction = useCallback((id) => {
    switch (id) {
      case 'model': pickerRef.current?.open(); break
      case 'new': openNewChat(); break
      case 'chat': navigate('/app/chat'); break
      case 'assistant': openAssistantChat(); break
      case 'gallery': navigate('/app/models'); break
      case 'installed': navigate('/app/models?view=installed'); break
      case 'import': navigate('/app/import-model'); break
      case 'stop':
        setStripOpen(true)
        stripRef.current?.scrollIntoView?.({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' })
        break
      case 'studio': navigate('/app/studio'); break
      case 'settings': navigate('/app/settings'); break
      case 'docs': window.open(DOCS_URL, '_blank', 'noopener,noreferrer'); break
      default: break
    }
  }, [navigate, openNewChat, openAssistantChat])

  // "/" from anywhere that is not a text field starts a command.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return
      if (isTyping(document.activeElement)) return
      const el = textareaRef.current
      if (!el) return
      e.preventDefault()
      setMessage('/')
      el.focus()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const dateLabel = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { weekday: 'short', day: 'numeric', month: 'short' }).format(new Date()),
    [i18n.language],
  )

  const cluster = distributedMode && clusterData ? clusterData : null
  const hasInput = message.trim().length > 0 || allFiles.length > 0
  const canSend = !!selectedModel && hasInput && !sending
  const sendTitle = !selectedModel ? t('input.selectModelFirst') : t('input.sendMessage')

  // ----- Non-admin, nothing installed -------------------------------------

  if (firstRun && !isAdmin) {
    return (
      <div className="home-page">
        <div className="home-wizard">
          <div className="home-wizard-hero">
            <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="home-logo" />
            <h1>{t('wizard.noModelsTitle')}</h1>
            <p>{t('wizard.noModelsBody')}</p>
          </div>
          <div className="home-wizard-actions">
            <a className="home-secondary" href={DOCS_URL} target="_blank" rel="noopener noreferrer">
              <Icon name="book" /> {t('quickLinks.documentation')}
            </a>
          </div>
        </div>
        <HomeConnect />
      </div>
    )
  }

  const showTip = isAdmin && assistantAvailable && !assistantUsed && !tipDismissed && !firstRun
  const picker = (
    <HomeModelPicker
      ref={pickerRef}
      value={selectedModel}
      onChange={setSelectedModel}
      capability={CAP_CHAT}
      loadedIds={loadedIds}
      disabled={firstRun}
      placeholder={firstRun ? t('picker.noneSelected') : undefined}
    />
  )
  const mcp = (
    <UnifiedMCPDropdown
      serverMCPAvailable={mcpAvailable}
      mcpServerList={mcpServerList}
      mcpServersLoading={mcpServersLoading}
      serverListError={mcpServerListError}
      selectedServers={mcpSelectedServers}
      onToggleServer={toggleMcpServer}
      onSelectAllServers={() => {
        const allNames = mcpServerList.filter(s => !s.error).map(s => s.name)
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
  )

  return (
    <div className="home-page">
      <div className="home-col reveal-stagger">
        <header className="home-header" style={staggerStyle(0)}>
          <h1 className="home-greeting">{t(`greeting.${greetingKey()}`)}</h1>
          <span className="home-date">{dateLabel}</span>
        </header>

        <div style={staggerStyle(1)}>
          <HomeComposer
            message={message}
            onMessage={setMessage}
            onSubmit={doSubmit}
            canSend={canSend}
            sending={sending}
            sendTitle={sendTitle}
            textareaRef={textareaRef}
            picker={picker}
            mcp={mcp}
            files={allFiles}
            onRemoveFile={removeFile}
            onAttach={attach}
            placeholder={firstRun ? t('input.placeholderFirstRun') : undefined}
            slashContext={slashContext}
            onRunAction={runAction}
          />
        </div>

        <div className="home-below" style={staggerStyle(2)}>
          {modelsFailed && configuredModels === null && (
            <div className="home-notice home-notice--error" role="alert" data-testid="home-load-error">
              <Icon name="alert-circle" />
              <h3>{t('notice.loadFailedTitle')}</h3>
              <p>{t('notice.loadFailedBody')}</p>
              <div className="home-notice__acts">
                <button type="button" className="home-primary home-primary--sm" onClick={fetchSystemInfo}>{t('notice.retry')}</button>
              </div>
            </div>
          )}

          {failedOp && (
            <div className="home-notice home-notice--error" role="alert" data-testid="home-staging-error">
              <Icon name="alert-circle" />
              <h3>{t('notice.stagingFailedTitle', { name: failedOp.name || failedOp.id })}</h3>
              <p>{failedOp.nodeName ? t('notice.stagingFailedBodyNode', { node: failedOp.nodeName }) : t('notice.stagingFailedBody')}</p>
              <div className="home-notice__acts">
                <button type="button" className="home-secondary home-secondary--sm" onClick={() => navigate('/app/activity')}>{t('notice.openActivity')}</button>
              </div>
              <details>
                <summary>{t('notice.details')}</summary>
                <pre>{failedOp.error}</pre>
              </details>
            </div>
          )}

          <div ref={stripRef}>
            {modelsLoading ? (
              <div className="home-strip home-strip--skeleton" aria-hidden="true" data-testid="home-strip-skeleton">
                <Skeleton variant="line" width="60%" />
              </div>
            ) : firstRun ? (
              <div className="home-strip home-strip--idle" data-testid="home-strip-idle">
                <div className="home-strip__head home-strip__head--static">
                  <span className="home-dot home-dot--cold" aria-hidden="true" />
                  <span>{t('strip.firstRun')}</span>
                </div>
              </div>
            ) : !modelsFailed || configuredModels !== null ? (
              <HomeMemoryStrip
                open={stripOpen}
                onOpenChange={setStripOpen}
                models={loadedModels}
                resources={resources}
                cluster={cluster}
                stagingOp={stagingOp}
                failedOp={failedOp}
                onStop={handleStopModel}
                onStopAll={handleStopAll}
              />
            ) : null}
          </div>

          {showTip && (
            <div className="home-tip" data-testid="home-assistant-tip">
              <Icon name="sparkles" />
              <span><b>{t('assistant.title')}.</b> {t('assistant.description')}</span>
              <button type="button" className="home-link" onClick={openAssistantChat} title={t('assistant.tooltip')}>
                {t('assistant.open')}
              </button>
              <button type="button" className="home-tip__close" onClick={dismissTip} aria-label={t('assistant.dismiss')} title={t('assistant.dismiss')}>
                <Icon name="close" />
              </button>
            </div>
          )}
        </div>

        {firstRun && (
          <div className="home-first" style={staggerStyle(3)}>
            <HomeFirstRun
              addToast={addToast}
              onInstallStarted={fetchSystemInfo}
              onGallery={() => navigate('/app/models')}
              onImport={() => navigate('/app/import-model')}
            />
          </div>
        )}

        <div style={staggerStyle(4)}>
          <HomeResume
            items={visibleConversations}
            leavingId={leavingId}
            onResume={resumeConversation}
            onDelete={requestDelete}
            emptyHint={(
              <div className="home-examples" aria-label={t('jump.examples')}>
                {(isAdmin ? ['/gallery', '/import'] : []).concat(isAdmin && assistantAvailable ? ['/assistant'] : [], ['/studio']).map(c => (
                  <code key={c}>{c}</code>
                ))}
              </div>
            )}
          />
        </div>

        <div className="home-libline" style={staggerStyle(5)} data-testid="home-library">
          <span>{t('library.label')}</span>
          {isAdmin && (
            <>
              <button type="button" className="home-ghost" onClick={() => navigate('/app/models')}>
                {t('quickLinks.browseGallery')} <code>/gallery</code>
              </button>
              <button type="button" className="home-ghost" onClick={() => navigate('/app/models?view=installed')}>
                {t('quickLinks.installedModels')}
                {configuredModels && <code>{configuredModels.length}</code>}
              </button>
              <button type="button" className="home-ghost" onClick={() => navigate('/app/import-model')}>
                {t('quickLinks.importModel')} <code>/import</code>
              </button>
              {assistantAvailable && !showTip && (
                <button type="button" className="home-ghost" onClick={openAssistantChat} title={t('assistant.tooltip')}>
                  {t('quickLinks.manageByChat')} <code>/assistant</code>
                </button>
              )}
            </>
          )}
          <a className="home-ghost" href={DOCS_URL} target="_blank" rel="noopener noreferrer">
            {t('quickLinks.documentation')} <code>/docs</code>
          </a>
        </div>

        <HomeConnect />
      </div>

      {pendingDelete && (
        <HomeUndoToast
          key={pendingDelete.id}
          message={t('jump.deleted', { title: pendingDelete.title })}
          onUndo={undoDelete}
          onExpire={commitDelete}
        />
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
