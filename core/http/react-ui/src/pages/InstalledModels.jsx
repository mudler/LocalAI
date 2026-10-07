import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import NodeDistributionChip from '../components/NodeDistributionChip'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { rowKeyDown, useRestoreRowFocus } from '../components/models/rowKeys'
import { gbLabel } from '../utils/modelLedger'
import { useModelSizes } from '../hooks/useModelSizes'
import { useModels } from '../hooks/useModels'
import { useGalleryEnrichment } from '../hooks/useGalleryEnrichment'
import { useOperations } from '../hooks/useOperations'
import useFailoverChains from '../hooks/useFailoverChains'
import { backendControlApi, modelsApi, nodesApi, systemApi } from '../utils/api'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import {
  CAP_CHAT, CAP_COMPLETION, CAP_IMAGE, CAP_VIDEO, CAP_TTS,
  CAP_TRANSCRIPT, CAP_SOUND_GENERATION, CAP_FACE_RECOGNITION,
  CAP_SPEAKER_RECOGNITION, CAP_EMBEDDINGS, CAP_RERANK,
  CAP_VAD, CAP_SCORE, CAP_DECISIONS,
} from '../utils/capabilities'
import Icon from '../components/Icon'

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
  { cap: CAP_DECISIONS, labelKey: 'decisions' },
]

export function modelUseCases(model) {
  const capabilities = Array.isArray(model?.capabilities) ? model.capabilities : []
  return USE_CASES.filter(item => (
    capabilities.includes(item.cap) && !(item.hideIf && capabilities.includes(item.hideIf))
  ))
}

export function ModelLifecycleDetailShell({
  testId,
  icon,
  name,
  lede,
  ledeTitle,
  onBack,
  backLabel,
  closeIcon,
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
        closeIcon={closeIcon}
        warning={warning}
        actions={actions}
      />
      {Array.isArray(stats) && <StatGrid stats={stats} />}
      {error && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span><Icon name="alert-circle" className="icon-before" />{error}</span>
        </div>
      )}
      {children}
    </div>
  )
}

// A small glyph after a model's name that says one thing about it. The words
// are on the glyph as its name and tooltip, not as text, so the same words in
// the inspector are the only visible copy of them.
function RowMark({ icon, label }) {
  return (
    <span className="ledger-name__pin" role="img" aria-label={label} title={label}>
      <Icon name={icon} />
    </span>
  )
}

// How many rows the loading state draws.
const SKELETON_ROWS = 6

export default function InstalledModels({
  addToast,
  query,
  state,
  selectedName,
  onQueryChange,
  onStateChange,
  onClearFilters,
  onSelect,
  hiddenIds,
  refreshToken,
  density,
  onDensity,
  searchRef,
  onOpenCleanup,
  disk,
}) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('models')
  const { models: allModels, loading, error: loadError, refetch } = useModels()
  const { enrichModel } = useGalleryEnrichment()
  const { operations } = useOperations()
  const { byName: failoverChains } = useFailoverChains()
  const [loadedModelIds, setLoadedModelIds] = useState(() => new Set())
  const [aliasTargets, setAliasTargets] = useState({})
  const [distributedMode, setDistributedMode] = useState(false)
  const [pendingActions, setPendingActions] = useState(() => new Set())
  const [actionErrors, setActionErrors] = useState({})
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [sort, setSort] = useState({ key: 'name', dir: 'asc' })
  const loadedOnce = useRef(false)
  const bodyRef = useRef(null)
  // Models waiting in the cleanup sheet's undo window are out of the list: they
  // are as good as gone, and showing them would invite a second action on them.
  const models = allModels.filter(model => !hiddenIds?.has(model.id))

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
  }, [operations.length, refreshToken, fetchLoadedModels, refetch])

  const isRunning = useCallback(model => (
    !model.disabled && (
      loadedModelIds.has(model.id) ||
      (Array.isArray(model.loaded_on) && model.loaded_on.length > 0)
    )
  ), [loadedModelIds])

  const matchesStateKey = (model, key) => {
    if (key === 'running') return isRunning(model)
    if (key === 'idle') return !model.disabled && !isRunning(model)
    if (key === 'disabled') return !!model.disabled
    if (key === 'pinned') return !!model.pinned
    if (key === 'distributed') return Array.isArray(model.loaded_on) && model.loaded_on.length > 0
    return true
  }
  const countFor = key => models.filter(model => matchesStateKey(model, key)).length
  const filters = [
    { key: 'all', label: t('lifecycle.filters.all'), icon: 'layers' },
    { key: 'running', label: t('lifecycle.filters.running'), icon: 'play-circle' },
    { key: 'idle', label: t('lifecycle.filters.idle'), icon: 'pause' },
    { key: 'disabled', label: t('lifecycle.filters.disabled'), icon: 'ban' },
    { key: 'pinned', label: t('lifecycle.filters.pinned'), icon: 'pin' },
    { key: 'distributed', label: t('lifecycle.filters.distributed'), icon: 'server' },
  ].map(f => ({ ...f, count: f.key === 'all' ? models.length : countFor(f.key) }))

  const normalizedQuery = query.trim().toLowerCase()
  const matching = models.filter(model => (
    matchesStateKey(model, state) && (
      !normalizedQuery ||
      model.id.toLowerCase().includes(normalizedQuery) ||
      (model.backend || '').toLowerCase().includes(normalizedQuery)
    )
  ))

  // Sizes come from the gallery's file sizes for the models it lists; a model
  // it does not know has none, and the cell says so rather than guessing.
  const galleryBacked = models.filter(model => enrichModel(model.id)).map(model => model.id)
  const sizes = useModelSizes(galleryBacked, true)

  const direction = sort.dir === 'asc' ? 1 : -1
  const visibleModels = [...matching].sort((a, b) => {
    if (sort.key === 'size') {
      // Unknown sizes sort last in both directions: a model with no reading is
      // not the smallest one, it is the one we know least about.
      const sa = sizes[a.id]
      const sb = sizes[b.id]
      if (sa == null && sb == null) return a.id.localeCompare(b.id)
      if (sa == null) return 1
      if (sb == null) return -1
      return (sa - sb) * direction || a.id.localeCompare(b.id)
    }
    return a.id.localeCompare(b.id) * direction
  })
  const selectedModel = selectedName
    ? models.find(model => model.id === selectedName) || null
    : null

  const onSort = key => setSort(prev => (
    prev.key === key ? { key, dir: prev.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' }
  ))

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

  // The same menu on a row and in the inspector, so the two cannot drift.
  const menuFor = model => [
    {
      key: 'toggle',
      icon: model.disabled ? 'toggle-on' : 'toggle-off',
      label: model.disabled ? t('lifecycle.actions.enable') : t('lifecycle.actions.disable'),
      onClick: () => handleToggleState(model.id, model.disabled),
      disabled: pendingActions.has(model.id),
    },
    {
      key: 'pin',
      icon: 'pin',
      label: model.pinned ? t('lifecycle.actions.unpin') : t('lifecycle.actions.pin'),
      onClick: () => handleTogglePinned(model.id, model.pinned),
      disabled: pendingActions.has(model.id) || !!model.disabled,
    },
    {
      key: 'edit',
      icon: 'edit',
      label: t('lifecycle.actions.edit'),
      onClick: () => navigate(`/app/model-editor/${encodeURIComponent(model.id)}`, {
        state: fromState(location, t('lifecycle.title')),
      }),
    },
    {
      key: 'logs',
      icon: 'terminal',
      label: t('lifecycle.actions.logs'),
      onClick: () => navigate(`/app/backend-logs/${encodeURIComponent(model.id)}`),
    },
    { divider: true },
    {
      key: 'delete',
      icon: 'trash',
      label: t('lifecycle.actions.delete'),
      danger: true,
      onClick: () => handleDelete(model.id),
    },
  ]

  const move = useCallback(name => {
    onSelect(name, { replace: true })
    bodyRef.current?.querySelector(`[data-entity="${CSS.escape(name)}"]`)?.focus()
  }, [onSelect])
  useRestoreRowFocus(selectedName, bodyRef)
  const names = visibleModels.map(model => model.id)
  const tabbable = names.includes(selectedName) ? selectedName : names[0]

  const stateOf = model => {
    if (model.disabled) return { key: 'disabled', label: t('lifecycle.states.disabled') }
    if (pendingActions.has(model.id)) return { key: 'busy', label: t('lifecycle.states.working') }
    if (isRunning(model)) return { key: 'running', label: t('lifecycle.states.running') }
    return { key: 'idle', label: t('lifecycle.states.idle') }
  }

  const rowActions = model => {
    const pending = pendingActions.has(model.id)
    const running = isRunning(model)
    return (
      <>
        {!model.disabled && !running && (
          <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={e => { e.stopPropagation(); handleLoad(model.id) }} disabled={pending}>
            <Icon name={pending ? 'spinner' : 'bolt'} spin={Boolean(pending)} />
            {pending ? t('lifecycle.actions.loading') : t('lifecycle.actions.load')}
          </button>
        )}
        {running && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={e => { e.stopPropagation(); handleStop(model.id) }} disabled={pending}>
            <Icon name="stop" /> {t('lifecycle.actions.stop')}
          </button>
        )}
        {model.disabled && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={e => { e.stopPropagation(); handleToggleState(model.id, true) }} disabled={pending}>
            <Icon name="toggle-on" /> {t('lifecycle.actions.enable')}
          </button>
        )}
        <span onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}>
          <ActionMenu
            ariaLabel={t('lifecycle.actions.forModel', { model: model.id })}
            triggerLabel={t('lifecycle.actions.forModel', { model: model.id })}
            items={menuFor(model)}
          />
        </span>
      </>
    )
  }

  const selectedPane = selectedModel ? (() => {
    const enriched = enrichModel(selectedModel.id)
    const useCases = modelUseCases(selectedModel)
    const running = isRunning(selectedModel)
    const chain = failoverChains[selectedModel.id]
    const pending = pendingActions.has(selectedModel.id)
    return (
      <ModelLifecycleDetailShell
        testId="installed-models"
        icon="brain"
        name={selectedModel.id}
        lede={enriched?.description ? stripMarkdown(enriched.description).slice(0, 220) : null}
        ledeTitle={enriched?.description ? stripMarkdown(enriched.description) : null}
        onBack={() => onSelect(null)}
        backLabel={t('ledger.closeInspector')}
        closeIcon
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
          sizes[selectedModel.id]
            ? { label: t('ledger.columns.size'), value: gbLabel(sizes[selectedModel.id]) }
            : null,
          selectedModel.pinned
            ? { label: t('lifecycle.detail.pinned'), value: t('lifecycle.detail.yes'), tone: 'warn' }
            : null,
        ]}
        actions={(
          <>
            {!selectedModel.disabled && !running && (
              <button className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => handleLoad(selectedModel.id)} disabled={pending}>
                <Icon name={pending ? 'spinner' : 'bolt'} spin={Boolean(pending)} />
                {pending ? t('lifecycle.actions.loading') : t('lifecycle.actions.load')}
              </button>
            )}
            {running && (
              <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => handleStop(selectedModel.id)} disabled={pending}>
                <Icon name="stop" /> {t('lifecycle.actions.stop')}
              </button>
            )}
            <ActionMenu
              ariaLabel={t('lifecycle.actions.forModel', { model: selectedModel.id })}
              triggerLabel={t('lifecycle.actions.forModel', { model: selectedModel.id })}
              items={menuFor(selectedModel)}
            />
          </>
        )}
      >
        {(aliasTargets[selectedModel.id] || selectedModel.source === 'registry-only' || chain) && (
          <div className="badge-row">
            {selectedModel.source === 'registry-only' && (
              <span className="dk-badge dk-badge--warn" title={t('lifecycle.detail.adoptedHint')}>
                <Icon name="ghost" /> {t('lifecycle.detail.adopted')}
              </span>
            )}
            {aliasTargets[selectedModel.id] && (
              <span className="dk-badge" title={t('lifecycle.detail.aliasTitle', { target: aliasTargets[selectedModel.id] })}>
                <Icon name="swap" /> {t('lifecycle.detail.alias', { target: aliasTargets[selectedModel.id] })}
              </span>
            )}
            {chain && (
              <span className="dk-badge" title={t('lifecycle.detail.chainTitle', { target: chain.active })}>
                <Icon name="shuffle" /> {t('lifecycle.detail.chain', { target: chain.active })}
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
                  className="dk-chip"
                  onClick={() => navigate(useCase.route(selectedModel.id))}
                >
                  {t(`lifecycle.open.${useCase.labelKey}`)}
                </button>
              ) : (
                <span key={useCase.cap} className="dk-badge">{t(`lifecycle.open.${useCase.labelKey}`)}</span>
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
  })() : null

  const firstLoad = loading && !loadedOnce.current
  const sortAria = key => (sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined)

  return (
    <>
      <div className="ledger-controls">
        <div className="ledger-controls__row">
          <div className="dk-input-icon ledger-search">
            <Icon name="search" className="dk-icon" />
            <input
              ref={searchRef}
              className="dk-input"
              data-testid="models-search"
              placeholder={t('lifecycle.installed.searchPlaceholder')}
              aria-label={t('lifecycle.installed.searchPlaceholder')}
              aria-keyshortcuts="/"
              value={query ?? ''}
              onChange={e => onQueryChange(e.target.value)}
              onKeyDown={e => { if (e.key === 'Escape') e.currentTarget.blur() }}
            />
            <kbd className="dk-kbd ledger-search__key" aria-hidden="true">/</kbd>
          </div>
          <div className="ledger-controls__end">
            {disk && (
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onOpenCleanup} data-testid="installed-cleanup">
                <Icon name="broom" /> {t('disk.freeUp')}
              </button>
            )}
            <div className="dk-segmented ledger-density" role="group" aria-label={t('ledger.density.label')}>
              <button type="button" className="dk-seg" aria-pressed={density === 'comfortable'} aria-label={t('ledger.density.comfortable')} title={t('ledger.density.comfortable')} onClick={() => onDensity('comfortable')}>
                <Icon name="list" />
              </button>
              <button type="button" className="dk-seg" aria-pressed={density === 'compact'} aria-label={t('ledger.density.compact')} title={t('ledger.density.compact')} onClick={() => onDensity('compact')}>
                <Icon name="equals" />
              </button>
            </div>
            <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={handleReload} disabled={pendingActions.has('models')}>
              <Icon name={pendingActions.has('models') ? 'spinner' : 'refresh'} spin={Boolean(pendingActions.has('models'))} />
              {pendingActions.has('models') ? t('lifecycle.actions.updating') : t('lifecycle.actions.update')}
            </button>
          </div>
        </div>
        <div className="ledger-controls__row">
          <div className="dk-segmented ledger-states" role="tablist" aria-label={t('lifecycle.installed.filterLabel')}>
            {filters.map(f => (
              <button
                key={f.key}
                type="button"
                role="tab"
                className="dk-seg"
                aria-selected={state === f.key}
                onClick={() => onStateChange(f.key)}
              >
                <span>{f.label}</span>
                <span className="ledger-facet__count">{f.count}</span>
              </button>
            ))}
          </div>
        </div>
      </div>

      {loadError && (
        <div className="ledger-banner ledger-banner--error" role="alert">
          <Icon name="alert-circle" />
          <span>{t('lifecycle.errors.loadList', { message: loadError })}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => refetch()}>
            <Icon name="refresh" /> {t('ledger.retry')}
          </button>
        </div>
      )}
      {actionErrors.models && (
        <div className="ledger-banner ledger-banner--error" role="alert">
          <Icon name="alert-circle" />
          <span>{actionErrors.models}</span>
        </div>
      )}

      {!firstLoad && models.length === 0 && !loadError ? (
        <div className="dk-empty ledger-empty ledger-empty--page" data-testid="installed-empty">
          <div className="dk-empty-icon"><Icon name="brain" /></div>
          <h2 className="dk-empty-title">{t('lifecycle.empty.title')}</h2>
          <p className="dk-empty-text">{t('lifecycle.empty.text')}</p>
          <div className="ledger-empty__actions">
            <button className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => navigate('/app/models')}>
              <Icon name="store" /> {t('lifecycle.empty.explore')}
            </button>
            <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => navigate('/app/import-model')}>
              <Icon name="upload" /> {t('lifecycle.empty.import')}
            </button>
          </div>
        </div>
      ) : (
        <div className={`ledger ledger--installed${selectedModel ? ' ledger--detail' : ' ledger--solo'}`} data-testid="installed-models">
          <div className="ledger__table-col">
            {!firstLoad && visibleModels.length === 0 ? (
              <div className="dk-empty ledger-empty">
                <Icon name="filter" />
                <p className="dk-empty-text">{t('lifecycle.empty.noMatches')}</p>
                <button className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onClearFilters}>
                  {t('lifecycle.empty.clear')}
                </button>
              </div>
            ) : (
              <div className="dk-table-wrap ledger-wrap" role="region" aria-label={t('lifecycle.views.installed')} data-testid="installed-models-rail">
                <table className={`dk-table ledger-table ledger-table--installed${density === 'compact' ? ' dk-table--compact' : ''}`} aria-busy={firstLoad || undefined}>
                  <caption className="dk-sr-only">{t('lifecycle.installed.count', { shown: visibleModels.length, total: models.length })}</caption>
                  <thead>
                    <tr>
                      <th scope="col" className="ledger-mark-cell"><span className="dk-sr-only">{t('ledger.selected')}</span></th>
                      <th scope="col" aria-sort={sortAria('name')}>
                        <button type="button" className="dk-table-sort" onClick={() => onSort('name')}>{t('table.modelName')}<Icon name="arrow-up" /></button>
                      </th>
                      <th scope="col">{t('lifecycle.detail.state')}</th>
                      <th scope="col" className="dk-num dk-hide-phone" aria-sort={sortAria('size')}>
                        <button type="button" className="dk-table-sort" onClick={() => onSort('size')}>{t('ledger.columns.size')}<Icon name="arrow-up" /></button>
                      </th>
                      <th scope="col"><span className="dk-sr-only">{t('table.actions')}</span></th>
                    </tr>
                  </thead>
                  {firstLoad ? (
                    <tbody data-testid="gallery-loader" className="ledger-skeleton">
                      {Array.from({ length: SKELETON_ROWS }, (_, i) => (
                        <tr key={i} className="dk-table-loading" aria-hidden="true">
                          <td className="ledger-mark-cell" />
                          <td><span className="dk-skeleton dk-skeleton--line ledger-skeleton__name" /><span className="dk-skeleton dk-skeleton--line ledger-skeleton__sub" /></td>
                          <td><span className="dk-skeleton dk-skeleton--line" /></td>
                          <td className="dk-hide-phone"><span className="dk-skeleton dk-skeleton--line" /></td>
                          <td className="ledger-status"><span className="dk-skeleton dk-skeleton--line ledger-skeleton__action" /></td>
                        </tr>
                      ))}
                    </tbody>
                  ) : (
                    <tbody ref={bodyRef}>
                      {visibleModels.map(model => {
                        const st = stateOf(model)
                        const selected = model.id === selectedName
                        const chain = failoverChains[model.id]
                        return (
                          <tr
                            key={model.id}
                            data-row
                            data-clickable
                            data-entity={model.id}
                            data-testid="installed-models-rail-item"
                            data-selected={selected ? 'true' : 'false'}
                            data-error={actionErrors[model.id] ? '' : undefined}
                            aria-current={selected ? 'true' : undefined}
                            tabIndex={model.id === tabbable ? 0 : -1}
                            onClick={() => onSelect(model.id)}
                            onKeyDown={e => rowKeyDown(e, { names, current: model.id, move, close: () => onSelect(null) })}
                          >
                            <td className="ledger-mark-cell">
                              <span className="ledger-mark" aria-hidden="true"><Icon name="check" /></span>
                            </td>
                            <td className="ledger-name-cell">
                              <span className="dk-table-name ledger-name">
                                <span className="ledger-name__text">{model.id}</span>
                                {model.pinned && <RowMark icon="pin" label={t('lifecycle.detail.pinned')} />}
                                {aliasTargets[model.id] && <RowMark icon="swap" label={t('lifecycle.detail.alias', { target: aliasTargets[model.id] })} />}
                                {chain && <RowMark icon="shuffle" label={t('lifecycle.detail.chain', { target: chain.active })} />}
                                {Array.isArray(model.loaded_on) && model.loaded_on.length > 0 && distributedMode && (
                                  <RowMark icon="server" label={t('lifecycle.detail.distributed')} />
                                )}
                              </span>
                              <span className="dk-table-sub">
                                {actionErrors[model.id] || [model.backend || t('lifecycle.detail.auto'), model.source === 'registry-only' ? t('lifecycle.detail.adopted') : ''].filter(Boolean).join(' · ')}
                              </span>
                            </td>
                            <td>
                              <span className="dk-status ledger-state" data-state={st.key}>
                                <span className={`dk-dot${st.key === 'running' ? ' dk-dot--ok' : st.key === 'busy' ? ' dk-dot--accent' : ''}`} />
                                {st.label}
                              </span>
                            </td>
                            <td className="dk-num dk-hide-phone ledger-size">
                              {sizes[model.id] ? gbLabel(sizes[model.id]) : <span title={t('ledger.sizeUnknownTitle')}>&mdash;</span>}
                            </td>
                            <td className="dk-table-actions ledger-status">
                              <span className="ledger-rowactions">{rowActions(model)}</span>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  )}
                </table>
              </div>
            )}
            {!firstLoad && models.length > 0 && (
              <p className="ledger-bar__count ledger-bar__count--foot">{t('lifecycle.installed.count', { shown: visibleModels.length, total: models.length })}</p>
            )}
          </div>
          {selectedModel && (
            <aside className="ledger__pane" data-testid="installed-models-pane" aria-label={t('ledger.inspector')}>
              {selectedPane}
            </aside>
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
      <h3><Icon name="info" /> {t('lifecycle.detail.title')}</h3>
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
        <dd><span className="dk-badge">{model.backend || t('lifecycle.detail.auto')}</span></dd>

        {license && (<>
          <dt>{t('lifecycle.detail.license')}</dt>
          <dd>{license}</dd>
        </>)}

        {tags.length > 0 && (<>
          <dt>{t('lifecycle.detail.tags')}</dt>
          <dd><div className="badge-row">{tags.map(tag => <span key={tag} className="dk-badge">{tag}</span>)}</div></dd>
        </>)}

        {urls.length > 0 && (<>
          <dt>{t('lifecycle.detail.links')}</dt>
          <dd>
            <div className="stack stack--xs">
              {urls.map(url => (
                <a key={url} href={safeHref(url)} target="_blank" rel="noopener noreferrer" className="dk-link">
                  <Icon name="external-link" className="icon-before text-xs" />{url}
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
