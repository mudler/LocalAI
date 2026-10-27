import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { CAP_CHAT } from '../utils/capabilities'
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
import { useResources } from '../hooks/useResources'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { useAddonPage } from '../addons/useAddonPage'
import { backendControlApi, chatsApi, modelsApi, systemApi, nodesApi } from '../utils/api'
import { greetingKey } from '../utils/greeting'
import { resumeItems } from '../utils/homeConversations'
// eslint-disable-next-line no-unused-vars
import Skeleton from '../components/Skeleton'
import { staggerStyle } from '../hooks/useStagger'
import Icon from '../components/Icon'

const DOCS_URL = 'https://localai.io'

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
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [distributedMode, setDistributedMode] = useState(false)
  const [clusterData, setClusterData] = useState(null)
  const [stripOpen, setStripOpen] = useState(false)
  const [conversations, setConversations] = useState([])
  const textareaRef = useRef(null)
  const pickerRef = useRef(null)
  const stripRef = useRef(null)

  // Detect distributed mode.
  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(data => {
        setDistributedMode(!!data.distributed)
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

  // A message typed here starts a chat on the chats page with the chosen model.
  const doSubmit = useCallback(() => {
    const text = message.trim()
    if (!text) return
    if (!selectedModel) {
      addToast(t('input.selectModelToast'), 'warning')
      return
    }
    setSending(true)
    navigate(`/app/chats?model=${encodeURIComponent(selectedModel)}`, { state: { start: text } })
  }, [message, selectedModel, addToast, navigate, t])

  // An empty new chat on the chosen model.
  const openNewChat = useCallback(() => {
    navigate(selectedModel ? `/app/chats?model=${encodeURIComponent(selectedModel)}` : '/app/chats')
  }, [navigate, selectedModel])

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

  // The newest chat the server keeps. The list is best effort: a server without
  // chats leaves the strip empty.
  useEffect(() => {
    let live = true
    chatsApi.list(1, 'chat').then(r => { if (live) setConversations(resumeItems(r.conversations)) }).catch(() => { if (live) setConversations([]) })
    return () => { live = false }
  }, [])

  const resumeConversation = useCallback((conv) => navigate(`/app/chats/${encodeURIComponent(conv.id)}`), [navigate])

  // ----- Derived state -----------------------------------------------------

  const modelsLoading = configuredModels === null && !modelsFailed
  const hasModels = configuredModels === null || configuredModels.length > 0
  const firstRun = configuredModels !== null && configuredModels.length === 0
  const loadedCount = loadedModels.length
  const loadedIds = useMemo(() => new Set(loadedModels.map(m => m.id)), [loadedModels])

  // Staging a model onto a worker is the one load this page can see: the
  // operations list reports it. An OOM on a single host surfaces in the chat.
  const stagingOp = operations.find(op => op.taskType === 'staging' && !op.error && !op.isQueued && !op.isDeletion)
  const failedOp = operations.find(op => op.taskType === 'staging' && op.error)
  const stripBusy = !!stagingOp || !!failedOp
  useEffect(() => { if (stripBusy) setStripOpen(true) }, [stripBusy])

  // Studio is an add-on: its command is offered only while it is installed.
  const studioPage = useAddonPage('studio', 'studio')
  const slashContext = useMemo(
    () => ({ isAdmin, hasModels: hasModels && !firstRun, loadedCount, studioInstalled: Boolean(studioPage) }),
    [isAdmin, hasModels, firstRun, loadedCount, studioPage],
  )

  const runAction = useCallback((id) => {
    switch (id) {
      case 'model': pickerRef.current?.open(); break
      case 'new': openNewChat(); break
      case 'chat': navigate('/app/chats'); break
      case 'gallery': navigate('/app/models'); break
      case 'installed': navigate('/app/models?view=installed'); break
      case 'import': navigate('/app/import-model'); break
      case 'stop':
        setStripOpen(true)
        stripRef.current?.scrollIntoView?.({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' })
        break
      case 'studio': if (studioPage) navigate(studioPage); break
      case 'settings': navigate('/app/settings'); break
      case 'docs': window.open(DOCS_URL, '_blank', 'noopener,noreferrer'); break
      default: break
    }
  }, [navigate, openNewChat, studioPage])

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
  const hasInput = message.trim().length > 0
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
  return (
    <div className="home-page">
      <div className="home-col reveal-stagger">
        <header className="home-header" style={staggerStyle(0)}>
          <h1 className="home-greeting">{t(`greeting.${greetingKey()}`)}</h1>
          <span className="home-date">{dateLabel}</span>
        </header>

        <div style={staggerStyle(1)}>
          <HomeComposer
          onDictateError={(m) => addToast(m, 'error')}
            message={message}
            onMessage={setMessage}
            onSubmit={doSubmit}
            canSend={canSend}
            sending={sending}
            sendTitle={sendTitle}
            textareaRef={textareaRef}
            picker={picker}
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
            items={conversations}
            onResume={resumeConversation}
            emptyHint={(
              <div className="home-examples" aria-label={t('jump.examples')}>
                {(isAdmin ? ['/gallery', '/import'] : []).concat(studioPage ? ['/studio'] : []).map(c => (
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
            </>
          )}
          <a className="home-ghost" href={DOCS_URL} target="_blank" rel="noopener noreferrer">
            {t('quickLinks.documentation')} <code>/docs</code>
          </a>
        </div>

        <HomeConnect />
      </div>

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
