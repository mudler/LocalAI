import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import FilterBar from '../components/FilterBar'
import GalleryLoader from '../components/GalleryLoader'
import NodeDistributionChip from '../components/NodeDistributionChip'
import SplitView from '../components/split/SplitView'
import EntityRail from '../components/split/EntityRail'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { useModels } from '../hooks/useModels'
import { useGalleryEnrichment } from '../hooks/useGalleryEnrichment'
import { useOperations } from '../hooks/useOperations'
import { backendControlApi, modelsApi, nodesApi, systemApi } from '../utils/api'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import {
  CAP_CHAT, CAP_COMPLETION, CAP_IMAGE, CAP_VIDEO, CAP_TTS,
  CAP_TRANSCRIPT, CAP_SOUND_GENERATION, CAP_FACE_RECOGNITION,
  CAP_SPEAKER_RECOGNITION, CAP_EMBEDDINGS, CAP_RERANK,
  CAP_VAD, CAP_SCORE,
} from '../utils/capabilities'

const USE_CASES = [
  { cap: CAP_CHAT, labelKey: 'chat', route: id => `/app/chat/${encodeURIComponent(id)}` },
  { cap: CAP_COMPLETION, labelKey: 'completion', route: id => `/app/chat/${encodeURIComponent(id)}`, hideIf: CAP_CHAT },
  { cap: CAP_IMAGE, labelKey: 'image', route: id => `/app/image/${encodeURIComponent(id)}` },
  { cap: CAP_VIDEO, labelKey: 'video', route: id => `/app/video/${encodeURIComponent(id)}` },
  { cap: CAP_TTS, labelKey: 'tts', route: id => `/app/tts/${encodeURIComponent(id)}` },
  { cap: CAP_TRANSCRIPT, labelKey: 'transcribe', route: () => '/app/talk' },
  { cap: CAP_SOUND_GENERATION, labelKey: 'sound', route: id => `/app/sound/${encodeURIComponent(id)}` },
  { cap: CAP_FACE_RECOGNITION, labelKey: 'face', route: id => `/app/face/${encodeURIComponent(id)}` },
  { cap: CAP_SPEAKER_RECOGNITION, labelKey: 'voice', route: id => `/app/voice/${encodeURIComponent(id)}` },
  { cap: CAP_EMBEDDINGS, labelKey: 'embeddings' },
  { cap: CAP_RERANK, labelKey: 'rerank' },
  { cap: CAP_VAD, labelKey: 'vad' },
  { cap: CAP_SCORE, labelKey: 'score' },
]

export function modelUseCases(model) {
  const capabilities = Array.isArray(model?.capabilities) ? model.capabilities : []
  return USE_CASES.filter(item => (
    capabilities.includes(item.cap) && !(item.hideIf && capabilities.includes(item.hideIf))
  ))
}

const MODEL_STATE_GROUPS = [
  { id: 'running', labelKey: 'running', icon: 'fa-circle-play' },
  { id: 'idle', labelKey: 'idle', icon: 'fa-pause' },
  { id: 'disabled', labelKey: 'disabled', icon: 'fa-ban' },
]

export function ModelLifecycleDetailShell({
  testId,
  icon,
  name,
  lede,
  ledeTitle,
  onBack,
  backLabel,
  warning,
  actions,
  stats,
  error,
  children,
}) {
  return (
    <div className="detail-pane">
      <DetailHeader
        testId={testId}
        icon={icon}
        name={name}
        lede={lede}
        ledeTitle={ledeTitle}
        onBack={onBack}
        backLabel={backLabel}
        warning={warning}
        actions={actions}
      />
      {Array.isArray(stats) && <StatGrid stats={stats} />}
      {error && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span><i className="fas fa-circle-exclamation icon-before" aria-hidden="true" />{error}</span>
        </div>
      )}
      {children}
    </div>
  )
}

export default function InstalledModels({
  addToast,
  query,
  state,
  selectedName,
  onQueryChange,
  onStateChange,
  onSelect,
}) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('models')
  const { models, loading, error: loadError, refetch } = useModels()
  const { enrichModel } = useGalleryEnrichment()
  const { operations } = useOperations()
  const [loadedModelIds, setLoadedModelIds] = useState(() => new Set())
  const [aliasTargets, setAliasTargets] = useState({})
  const [distributedMode, setDistributedMode] = useState(false)
  const [pendingActions, setPendingActions] = useState(() => new Set())
  const [actionErrors, setActionErrors] = useState({})
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())
  const loadedOnce = useRef(false)

  const fetchLoadedModels = useCallback(async () => {
    try {
      const info = await systemApi.info()
      const loaded = Array.isArray(info?.loaded_models) ? info.loaded_models : []
      setLoadedModelIds(new Set(loaded.map(model => model.id)))
    } catch {
      setLoadedModelIds(new Set())
    }
  }, [])

  const fetchAliases = useCallback(async () => {
    try {
      const aliases = await modelsApi.listAliases()
      const next = {}
      for (const alias of Array.isArray(aliases) ? aliases : []) next[alias.name] = alias.target
      setAliasTargets(next)
    } catch {
      setAliasTargets({})
    }
  }, [])

  useEffect(() => {
    fetchLoadedModels()
    fetchAliases()
    nodesApi.list().then(() => setDistributedMode(true)).catch(() => setDistributedMode(false))
  }, [fetchAliases, fetchLoadedModels])

  useEffect(() => {
    if (!distributedMode) return
    const interval = setInterval(() => {
      refetch()
      fetchLoadedModels()
    }, 10000)
    return () => clearInterval(interval)
  }, [distributedMode, fetchLoadedModels, refetch])

  useEffect(() => {
    if (!loading) loadedOnce.current = true
  }, [loading])

  useEffect(() => {
    refetch()
    fetchLoadedModels()
  }, [operations.length, fetchLoadedModels, refetch])

  const isRunning = useCallback(model => (
    !model.disabled && (
      loadedModelIds.has(model.id) ||
      (Array.isArray(model.loaded_on) && model.loaded_on.length > 0)
    )
  ), [loadedModelIds])

  const filters = [
    { key: 'all', label: t('lifecycle.filters.all'), icon: 'fa-layer-group' },
    { key: 'running', label: t('lifecycle.filters.running'), icon: 'fa-circle-play' },
    { key: 'idle', label: t('lifecycle.filters.idle'), icon: 'fa-pause' },
    { key: 'disabled', label: t('lifecycle.filters.disabled'), icon: 'fa-ban' },
    { key: 'pinned', label: t('lifecycle.filters.pinned'), icon: 'fa-thumbtack' },
    { key: 'distributed', label: t('lifecycle.filters.distributed'), icon: 'fa-server' },
  ]

  const matchesState = model => {
    if (state === 'running') return isRunning(model)
    if (state === 'idle') return !model.disabled && !isRunning(model)
    if (state === 'disabled') return !!model.disabled
    if (state === 'pinned') return !!model.pinned
    if (state === 'distributed') return Array.isArray(model.loaded_on) && model.loaded_on.length > 0
    return true
  }
  const normalizedQuery = query.trim().toLowerCase()
  const visibleModels = models.filter(model => (
    matchesState(model) && (
      !normalizedQuery ||
      model.id.toLowerCase().includes(normalizedQuery) ||
      (model.backend || '').toLowerCase().includes(normalizedQuery)
    )
  ))
  const selectedModel = selectedName
    ? models.find(model => model.id === selectedName) || null
    : null

  const toggleGroup = useCallback(id => {
    setCollapsedGroups(previous => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const setPending = (name, pending) => {
    setPendingActions(previous => {
      const next = new Set(previous)
      if (pending) next.add(name)
      else next.delete(name)
      return next
    })
  }

  const runAction = async (modelName, action, request, successMessage) => {
    setPending(modelName, true)
    setActionErrors(previous => ({ ...previous, [modelName]: null }))
    try {
      await request()
      if (successMessage) addToast(successMessage, 'success')
      refetch()
      await fetchLoadedModels()
      return true
    } catch (err) {
      setActionErrors(previous => ({
        ...previous,
        [modelName]: t('lifecycle.errors.action', { action, model: modelName, message: err.message }),
      }))
      return false
    } finally {
      setPending(modelName, false)
    }
  }

  const handleLoad = modelName => runAction(
    modelName,
    t('lifecycle.actionNames.load'),
    () => backendControlApi.load({ model: modelName }),
    t('lifecycle.toasts.loaded', { model: modelName }),
  )

  const handleStop = modelName => {
    setConfirmDialog({
      title: t('lifecycle.confirm.stopTitle'),
      message: t('lifecycle.confirm.stopMessage', { model: modelName }),
      confirmLabel: t('lifecycle.actions.stop'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        await runAction(
          modelName,
          t('lifecycle.actionNames.stop'),
          () => backendControlApi.shutdown({ model: modelName }),
          t('lifecycle.toasts.stopped', { model: modelName }),
        )
      },
    })
  }

  const handleToggleState = (modelName, disabled) => {
    const operation = disabled ? 'enable' : 'disable'
    return runAction(
      modelName,
      t(`lifecycle.actionNames.${operation}`),
      () => modelsApi.toggleState(modelName, operation),
      t(`lifecycle.toasts.${operation}d`, { model: modelName }),
    )
  }

  const handleTogglePinned = (modelName, pinned) => {
    const operation = pinned ? 'unpin' : 'pin'
    return runAction(
      modelName,
      t(`lifecycle.actionNames.${operation}`),
      () => modelsApi.togglePinned(modelName, operation),
      t(`lifecycle.toasts.${operation}ned`, { model: modelName }),
    )
  }

  const handleDelete = modelName => {
    setConfirmDialog({
      title: t('lifecycle.confirm.deleteTitle'),
      message: t('lifecycle.confirm.deleteMessage', { model: modelName }),
      confirmLabel: t('lifecycle.actions.delete'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        const deleted = await runAction(
          modelName,
          t('lifecycle.actionNames.delete'),
          () => modelsApi.deleteByName(modelName),
          t('lifecycle.toasts.deleted', { model: modelName }),
        )
        if (deleted) onSelect(null)
      },
    })
  }

  const handleReload = () => runAction(
    'models',
    t('lifecycle.actionNames.update'),
    modelsApi.reload,
    t('lifecycle.toasts.updated'),
  )

  const railItems = visibleModels.map(model => {
    const running = isRunning(model)
    let groupId = 'idle'
    let stripe = 'idle'
    let meta = t('lifecycle.states.idle')
    let metaTone
    if (model.disabled) {
      groupId = 'disabled'
      stripe = 'off'
      meta = t('lifecycle.states.disabled')
    } else if (pendingActions.has(model.id)) {
      meta = t('lifecycle.states.working')
      metaTone = 'busy'
    } else if (running) {
      groupId = 'running'
      stripe = 'run'
      meta = t('lifecycle.states.running')
      metaTone = 'ok'
    }
    return { id: model.id, name: model.id, icon: 'fa-brain', groupId, stripe, meta, metaTone }
  })

  const groups = MODEL_STATE_GROUPS.map(group => ({
    id: group.id,
    icon: group.icon,
    label: t(`lifecycle.filters.${group.labelKey}`),
  }))

  const selectedPane = selectedModel ? (() => {
    const enriched = enrichModel(selectedModel.id)
    const useCases = modelUseCases(selectedModel)
    const running = isRunning(selectedModel)
    const pending = pendingActions.has(selectedModel.id)
    return (
      <ModelLifecycleDetailShell
        testId="installed-models"
        icon="fa-brain"
        name={selectedModel.id}
        lede={enriched?.description ? stripMarkdown(enriched.description).slice(0, 220) : null}
        ledeTitle={enriched?.description ? stripMarkdown(enriched.description) : null}
        onBack={() => onSelect(null)}
        backLabel={t('lifecycle.installed.backToAll')}
        error={actionErrors[selectedModel.id]}
        stats={[
          {
            label: t('lifecycle.detail.state'),
            value: selectedModel.disabled
              ? t('lifecycle.states.disabled')
              : running
                ? t('lifecycle.states.running')
                : t('lifecycle.states.idle'),
            tone: running ? 'ok' : undefined,
          },
          { label: t('lifecycle.detail.backend'), value: selectedModel.backend || t('lifecycle.detail.auto') },
          selectedModel.pinned
            ? { label: t('lifecycle.detail.pinned'), value: t('lifecycle.detail.yes'), tone: 'warn' }
            : null,
        ]}
        actions={(
          <>
            {!selectedModel.disabled && !running && (
              <button className="btn btn-primary btn-sm" onClick={() => handleLoad(selectedModel.id)} disabled={pending}>
                <i className={`fas ${pending ? 'fa-spinner fa-spin' : 'fa-bolt'}`} aria-hidden="true" />
                {pending ? t('lifecycle.actions.loading') : t('lifecycle.actions.load')}
              </button>
            )}
            {running && (
              <button className="btn btn-secondary btn-sm" onClick={() => handleStop(selectedModel.id)} disabled={pending}>
                <i className="fas fa-stop" aria-hidden="true" /> {t('lifecycle.actions.stop')}
              </button>
            )}
            <ActionMenu
              ariaLabel={t('lifecycle.actions.forModel', { model: selectedModel.id })}
              triggerLabel={t('lifecycle.actions.forModel', { model: selectedModel.id })}
              items={[
                {
                  key: 'toggle',
                  icon: selectedModel.disabled ? 'fa-toggle-on' : 'fa-toggle-off',
                  label: selectedModel.disabled ? t('lifecycle.actions.enable') : t('lifecycle.actions.disable'),
                  onClick: () => handleToggleState(selectedModel.id, selectedModel.disabled),
                  disabled: pending,
                },
                {
                  key: 'pin',
                  icon: 'fa-thumbtack',
                  label: selectedModel.pinned ? t('lifecycle.actions.unpin') : t('lifecycle.actions.pin'),
                  onClick: () => handleTogglePinned(selectedModel.id, selectedModel.pinned),
                  disabled: pending || !!selectedModel.disabled,
                },
                {
                  key: 'edit',
                  icon: 'fa-pen-to-square',
                  label: t('lifecycle.actions.edit'),
                  onClick: () => navigate(`/app/model-editor/${encodeURIComponent(selectedModel.id)}`, {
                    state: fromState(location, t('lifecycle.title')),
                  }),
                },
                {
                  key: 'logs',
                  icon: 'fa-terminal',
                  label: t('lifecycle.actions.logs'),
                  onClick: () => navigate(`/app/backend-logs/${encodeURIComponent(selectedModel.id)}`),
                },
                { divider: true },
                {
                  key: 'delete',
                  icon: 'fa-trash',
                  label: t('lifecycle.actions.delete'),
                  danger: true,
                  onClick: () => handleDelete(selectedModel.id),
                },
              ]}
            />
          </>
        )}
      >
        {(aliasTargets[selectedModel.id] || selectedModel.source === 'registry-only') && (
          <div className="badge-row">
            {selectedModel.source === 'registry-only' && (
              <span className="badge badge-warning" title={t('lifecycle.detail.adoptedHint')}>
                <i className="fas fa-ghost" /> {t('lifecycle.detail.adopted')}
              </span>
            )}
            {aliasTargets[selectedModel.id] && (
              <span className="badge badge-info" title={t('lifecycle.detail.aliasTitle', { target: aliasTargets[selectedModel.id] })}>
                <i className="fas fa-arrow-right-arrow-left" /> {t('lifecycle.detail.alias', { target: aliasTargets[selectedModel.id] })}
              </span>
            )}
          </div>
        )}

        {useCases.length > 0 && (
          <div>
            <span className="detail-pane__label">{t('lifecycle.open.title')}</span>
            <div className="badge-row">
              {useCases.map(useCase => useCase.route ? (
                <button
                  key={useCase.cap}
                  type="button"
                  className="badge badge-info badge-link"
                  onClick={() => navigate(useCase.route(selectedModel.id))}
                >
                  {t(`lifecycle.open.${useCase.labelKey}`)}
                </button>
              ) : (
                <span key={useCase.cap} className="badge">{t(`lifecycle.open.${useCase.labelKey}`)}</span>
              ))}
            </div>
          </div>
        )}

        <InstalledModelDetail
          model={selectedModel}
          enriched={enriched}
          distributedMode={distributedMode}
          t={t}
        />
      </ModelLifecycleDetailShell>
    )
  })() : (
    <div className="zero-pane">
      <div className="zero-pane__hero">
        <span className="zero-pane__eyebrow">{t('lifecycle.installed.eyebrow')}</span>
        <h2 className="zero-pane__title">{t('lifecycle.installed.summary', { count: models.length })}</h2>
        <p className="zero-pane__text">{t('lifecycle.installed.summaryHint')}</p>
      </div>
    </div>
  )

  return (
    <>
      <FilterBar
        search={query}
        onSearchChange={onQueryChange}
        searchPlaceholder={t('lifecycle.installed.searchPlaceholder')}
        filters={filters}
        activeFilter={state}
        onFilterChange={onStateChange}
        rightSlot={(
          <button className="btn btn-secondary btn-sm" onClick={handleReload} disabled={pendingActions.has('models')}>
            <i className={`fas ${pendingActions.has('models') ? 'fa-spinner fa-spin' : 'fa-rotate'}`} />
            {pendingActions.has('models') ? t('lifecycle.actions.updating') : t('lifecycle.actions.update')}
          </button>
        )}
      />

      {loadError && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span>{t('lifecycle.errors.loadList', { message: loadError })}</span>
        </div>
      )}
      {actionErrors.models && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span><i className="fas fa-circle-exclamation icon-before" aria-hidden="true" />{actionErrors.models}</span>
        </div>
      )}

      {loading && !loadedOnce.current ? (
        <GalleryLoader />
      ) : models.length === 0 ? (
        <div className="empty-state empty-state--page">
          <div className="empty-state-icon"><i className="fas fa-brain" /></div>
          <h2 className="empty-state-title">{t('lifecycle.empty.title')}</h2>
          <p className="empty-state-text">{t('lifecycle.empty.text')}</p>
          <div className="empty-state__actions">
            <button className="btn btn-primary btn-sm" onClick={() => navigate('/app/models')}>
              <i className="fas fa-store" /> {t('lifecycle.empty.explore')}
            </button>
            <button className="btn btn-secondary btn-sm" onClick={() => navigate('/app/import-model')}>
              <i className="fas fa-upload" /> {t('lifecycle.empty.import')}
            </button>
          </div>
        </div>
      ) : visibleModels.length === 0 ? (
        <div className="empty-state">
          <i className="fas fa-filter" />
          <p>{t('lifecycle.empty.noMatches')}</p>
          <button className="btn btn-ghost btn-sm" onClick={() => { onQueryChange(''); onStateChange('all') }}>
            {t('lifecycle.empty.clear')}
          </button>
        </div>
      ) : (
        <SplitView
          testId="installed-models"
          detail={!!selectedModel}
          rail={(
            <EntityRail
              items={railItems}
              groups={groups}
              grouped={!query.trim() && state === 'all'}
              collapsedGroups={collapsedGroups}
              onToggleGroup={toggleGroup}
              busy={loading}
              selectedId={selectedName}
              onSelect={onSelect}
              countLabel={t('lifecycle.installed.count', { shown: visibleModels.length, total: models.length })}
              ariaLabel={t('lifecycle.views.installed')}
              testId="installed-models-rail"
            />
          )}
          pane={selectedPane}
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
    </>
  )
}

function InstalledModelDetail({ model, enriched, distributedMode, t }) {
  const description = enriched?.description
  const license = enriched?.license
  const tags = Array.isArray(enriched?.tags) ? enriched.tags : []
  const urls = Array.isArray(enriched?.urls) ? enriched.urls : []
  const files = Array.isArray(enriched?.additionalFiles)
    ? enriched.additionalFiles
    : Array.isArray(enriched?.files)
      ? enriched.files
      : []

  return (
    <div className="resource-row__detail">
      <h3><i className="fas fa-circle-info" /> {t('lifecycle.detail.title')}</h3>
      <dl className="resource-row__detail-grid">
        <dt>{t('lifecycle.detail.description')}</dt>
        <dd>
          {description ? (
            <div
              className="resource-row__detail-md markdown-body"
              dangerouslySetInnerHTML={{ __html: renderMarkdown(description) }}
            />
          ) : (
            <span className="cell-muted">{t('lifecycle.detail.noDescription')}</span>
          )}
        </dd>

        <dt>{t('lifecycle.detail.backend')}</dt>
        <dd><span className="badge badge-info">{model.backend || t('lifecycle.detail.auto')}</span></dd>

        {license && (<>
          <dt>{t('lifecycle.detail.license')}</dt>
          <dd>{license}</dd>
        </>)}

        {tags.length > 0 && (<>
          <dt>{t('lifecycle.detail.tags')}</dt>
          <dd><div className="badge-row">{tags.map(tag => <span key={tag} className="badge badge-info">{tag}</span>)}</div></dd>
        </>)}

        {urls.length > 0 && (<>
          <dt>{t('lifecycle.detail.links')}</dt>
          <dd>
            <div className="stack stack--xs">
              {urls.map(url => (
                <a key={url} href={safeHref(url)} target="_blank" rel="noopener noreferrer" className="badge badge-info badge-link">
                  <i className="fas fa-external-link-alt icon-before text-xs" />{url}
                </a>
              ))}
            </div>
          </dd>
        </>)}

        {distributedMode && Array.isArray(model.loaded_on) && model.loaded_on.length > 0 && (<>
          <dt>{t('lifecycle.detail.distributed')}</dt>
          <dd><NodeDistributionChip nodes={model.loaded_on} context="models" compactThreshold={20} /></dd>
        </>)}

        {model.source && (<>
          <dt>{t('lifecycle.detail.source')}</dt>
          <dd className="cell-muted">{model.source}</dd>
        </>)}

        {files.length > 0 && (<>
          <dt>{t('lifecycle.detail.files')}</dt>
          <dd className="cell-muted">{t('lifecycle.detail.fileCount', { count: files.length })}</dd>
        </>)}
      </dl>
    </div>
  )
}
