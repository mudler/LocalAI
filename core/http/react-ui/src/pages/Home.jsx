import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useLocation, useNavigate, useOutletContext } from 'react-router-dom'
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
// eslint-disable-next-line no-unused-vars
import HomeWidgets from '../components/wellknown/HomeWidgets'
// eslint-disable-next-line no-unused-vars
import HomeExamples, { useExamplesGone } from '../components/wellknown/HomeExamples'
import { useWellKnown } from '../hooks/useWellKnown'
import { widgetList } from '../utils/wellKnown'
import { useResources } from '../hooks/useResources'
import { usePolling } from '../hooks/usePolling'
import { useOperations } from '../hooks/useOperations'
import { useAddonPage } from '../addons/useAddonPage'
import { backendControlApi, chatsApi, modelsApi, systemApi, nodesApi } from '../utils/api'
import { greetingKey } from '../utils/greeting'
import { resumeItems } from '../utils/homeConversations'
// eslint-disable-next-line no-unused-vars
import Skeleton from '../components/Skeleton'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, LoadError } from '../components/ui'
import './home.css'
import Icon from '../components/Icon'

const DOCS_URL = 'https://localai.io'
const ADDONS_DOCS_URL = 'https://localai.io/features/addons/#turning-the-owner-on-or-off'

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
  const { t: tw } = useTranslation('workspace')
  const { isAdmin, hasFeature, user } = useAuth()
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
  const [assistantOn, setAssistantOn] = useState(false)
  const [collectionsOn, setCollectionsOn] = useState(false)
  const [clusterData, setClusterData] = useState(null)
  const [stripOpen, setStripOpen] = useState(false)
  const [conversations, setConversations] = useState([])
  const textareaRef = useRef(null)
  const pickerRef = useRef(null)
  const stripRef = useRef(null)
  const location = useLocation()
  const [addonsOn, setAddonsOn] = useState(true)
  // An add-on address opened while add-ons are off on the server lands here
  // (see AddonsShell); so does /studio without Studio installed. The notice
  // says why, and how to get the add-on.
  const [addonNotice, setAddonNotice] = useState(() => location.state?.addonsOff || '')

  // Detect distributed mode.
  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(data => {
        setDistributedMode(!!data.distributed)
        setAssistantOn(!!data.localai_assistant)
        setCollectionsOn(data.collections !== false)
        setAddonsOn(data.addons !== false)
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

  // The LocalAI assistant is a chat like any other, started with the management
  // tools. It needs a model; the server refuses anyone but an admin.
  const openAssistantChat = useCallback(async () => {
    if (!selectedModel) {
      addToast(t('assistant.noModel'), 'warning')
      return
    }
    try {
      const conv = await chatsApi.create(selectedModel, t('assistant.chatTitle'), '', true)
      navigate(`/app/chats/${encodeURIComponent(conv.id)}`)
    } catch (err) {
      addToast(t('assistant.startFailed', { message: err.message }), 'error')
    }
  }, [selectedModel, addToast, navigate, t])

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
  // The widgets of home.json. A chats widget takes the place of the list of
  // recent chats below, so the page does not show them twice.
  const chatsOn = hasFeature('chats')
  const homeFile = useWellKnown('home', { enabled: chatsOn })
  const chatsWidget = useMemo(() => widgetList(homeFile.doc).some(w => w.type === 'chats'), [homeFile.doc])
  // The "Make LocalAI yours" examples. A card puts its prompt into the
  // composer and sends nothing; the line under the composer says so.
  const userFile = useWellKnown('user', { enabled: chatsOn })
  const [examplePicked, setExamplePicked] = useState(false)
  const [liveNote, setLiveNote] = useState('')
  const [editLayout, setEditLayout] = useState(0)
  const pickExample = useCallback((prompt) => {
    setMessage(prompt)
    setExamplePicked(true)
    setLiveNote(tw('discovery.ex.live'))
    requestAnimationFrame(() => {
      const el = textareaRef.current
      if (!el) return
      el.focus()
      el.setSelectionRange?.(prompt.length, prompt.length)
    })
  }, [tw])
  useEffect(() => { if (!message.trim()) setExamplePicked(false) }, [message])
  const askLayout = useCallback(() => {
    const q = selectedModel ? `?model=${encodeURIComponent(selectedModel)}` : ''
    navigate(`/app/chats${q}`, { state: { draft: tw('wellKnown.home.askPrompt') } })
  }, [navigate, selectedModel, tw])

  // ----- Derived state -----------------------------------------------------

  const modelsLoading = configuredModels === null && !modelsFailed
  const hasModels = configuredModels === null || configuredModels.length > 0
  const firstRun = configuredModels !== null && configuredModels.length === 0
  const examplesOn = chatsOn && configuredModels !== null && !firstRun
  const examplesGone = useExamplesGone(examplesOn)
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
    () => ({
      isAdmin, hasModels: hasModels && !firstRun, loadedCount, studioInstalled: Boolean(studioPage), assistantAvailable: assistantOn,
      hasChats: hasFeature('chats'), hasCollections: hasFeature('collections') && collectionsOn,
    }),
    [isAdmin, hasModels, firstRun, loadedCount, studioPage, assistantOn, hasFeature, collectionsOn],
  )

  const runAction = useCallback((id) => {
    switch (id) {
      case 'model': pickerRef.current?.open(); break
      case 'new': openNewChat(); break
      case 'chat': navigate('/app/chats'); break
      case 'assistant': openAssistantChat(); break
      case 'gallery': navigate('/app/models'); break
      case 'installed': navigate('/app/models?view=installed'); break
      case 'import': navigate('/app/import-model'); break
      case 'stop':
        setStripOpen(true)
        stripRef.current?.scrollIntoView?.({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' })
        break
      case 'agents': navigate('/app/agents'); break
      case 'mcp': navigate('/app/mcp-connectors'); break
      case 'memory': navigate('/app/collections'); break
      case 'studio': if (studioPage) navigate(studioPage); break
      case 'studioInstall':
        if (addonsOn) navigate('/app/addons?tab=browse&q=studio')
        else setAddonNotice('studio')
        break
      case 'settings': navigate('/app/settings'); break
      case 'docs': window.open(DOCS_URL, '_blank', 'noopener,noreferrer'); break
      default: break
    }
  }, [navigate, openNewChat, openAssistantChat, studioPage, addonsOn])

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
      <div className="home-col">
        <header className="home-header">
          <h1 className="home-greeting">{t(`greeting.${greetingKey()}`)}</h1>
          <span className="home-date">{dateLabel}</span>
        </header>

        <div>
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
          {examplePicked && (
            <p className="wk-ex__hint" data-testid="home-example-hint">
              <Icon name="info" /> {tw('discovery.ex.inComposer')}
            </p>
          )}
          <span className="sr-only" aria-live="polite">{liveNote}</span>
        </div>

        {examplesOn && (
          <HomeExamples
            enabled={examplesOn}
            name={userFile.doc?.data?.name || user?.name || ''}
            onPick={pickExample}
            addToast={addToast}
          />
        )}

        <div className="home-below">
          {addonNotice && (
            <Callout
              tone="info" icon="puzzle" role="note" testId="home-addons-off"
              title={t('notice.addonsOffTitle')}
              actions={(
                <>
                  <Button size="sm" href={ADDONS_DOCS_URL} target="_blank" rel="noopener noreferrer">{t('notice.addonsDocs')}</Button>
                  <Button size="sm" variant="ghost" onClick={() => setAddonNotice('')}>{t('notice.dismiss')}</Button>
                </>
              )}
            >
              {t(isAdmin ? 'notice.addonsOffAdmin' : 'notice.addonsOffReader', { name: addonNotice === 'studio' ? 'Studio' : addonNotice === 'addons' ? t('notice.thisPage') : addonNotice })}
            </Callout>
          )}

          {modelsFailed && configuredModels === null && (
            <LoadError title={`${t('notice.loadFailedTitle')}.`} nothingChanged={false} onRetry={fetchSystemInfo} testId="home-load-error">
              {t('notice.loadFailedBody')}
            </LoadError>
          )}

          {failedOp && (
            <Callout
              tone="error" role="alert" testId="home-staging-error" className="home-staging"
              title={t('notice.stagingFailedTitle', { name: failedOp.name || failedOp.id })}
              actions={<Button size="sm" onClick={() => navigate('/app/activity')}>{t('notice.openActivity')}</Button>}
            >
              {failedOp.nodeName ? t('notice.stagingFailedBodyNode', { node: failedOp.nodeName }) : t('notice.stagingFailedBody')}
              <details className="home-staging__details">
                <summary>{t('notice.details')}</summary>
                <pre>{failedOp.error}</pre>
              </details>
            </Callout>
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

        {chatsOn && !firstRun && (
          <div>
            <HomeWidgets wk={homeFile} conversations={conversations} onResume={resumeConversation} onAsk={askLayout} editRequest={editLayout} />
          </div>
        )}

        {firstRun && (
          <div className="home-first">
            <HomeFirstRun
              addToast={addToast}
              onInstallStarted={fetchSystemInfo}
              onGallery={() => navigate('/app/models')}
              onImport={() => navigate('/app/import-model')}
            />
          </div>
        )}

        {!chatsWidget && <div>
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
        </div>}

        <div className="home-libline" data-testid="home-library">
          <span>{t('library.label')}</span>
          {examplesOn && examplesGone && (
            <button type="button" className="home-ghost" onClick={() => setEditLayout(n => n + 1)} data-testid="home-customise">
              {tw('discovery.home.customise')}
            </button>
          )}
          {isAdmin && (
            <>
              {assistantOn && !firstRun && (
                <button type="button" className="home-ghost" data-testid="home-assistant" title={t('assistant.description')} onClick={openAssistantChat}>
                  {t('assistant.title')} <code>/assistant</code>
                </button>
              )}
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
