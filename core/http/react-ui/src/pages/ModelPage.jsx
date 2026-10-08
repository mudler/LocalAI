import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useLocation, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { groupForEntity } from '../utils/entityGroups'
import { modelBudget } from '../utils/modelBudget'
import { cssVars, diskState, fitFor, gbLabel, leavesFree } from '../utils/modelLedger'
import { modelPath, walkFor } from '../utils/modelWalk'
import { formatBytes } from '../utils/format'
import { stripMarkdown } from '../utils/markdown'
import { useModels } from '../hooks/useModels'
import { useModelStorage } from '../hooks/useModelStorage'
import { useModelActions } from '../hooks/useModelActions'
import { useOperations } from '../hooks/useOperations'
import { useResources } from '../hooks/useResources'
import { useGalleryEntry, useLoadedModels, useModelEstimate, useVariants } from '../hooks/useModelPage'
import { isTypingTarget } from '../components/models/rowKeys'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import Popover from '../components/Popover'
import Icon from '../components/Icon'
import { modelUseCases } from './InstalledModels'
// eslint-disable-next-line no-unused-vars
import OverviewTab from '../components/models/page/OverviewTab'
// eslint-disable-next-line no-unused-vars
import FitTab from '../components/models/page/FitTab'
// eslint-disable-next-line no-unused-vars
import VariantsTab from '../components/models/page/VariantsTab'
// eslint-disable-next-line no-unused-vars
import UsageTab from '../components/models/page/UsageTab'
// eslint-disable-next-line no-unused-vars
import ConfigTab from '../components/models/page/ConfigTab'
// eslint-disable-next-line no-unused-vars
import LogsTab from '../components/models/page/LogsTab'
import '../components/models/page/model-page.css'

const CONTEXT_DEFAULT = 8192

// How long "starting" outlasts a click when the operation never shows up in the
// list: the install call returned but the server queued nothing visible.
const STARTING_GRACE_MS = 5000

function buildLabel(variant) {
  return variant?.quantization || variant?.model || ''
}

// One model, at its own address: /app/models/<id>.
//
// The page reads three things about the id and does not care which list it was
// opened from. The installed list says whether the server has it and what it
// can do. The gallery says what it is (description, licence, builds, files).
// The estimate says what it needs. A model the gallery does not list still
// gets a page, from the first alone; a gallery entry that is not installed
// still gets one, from the other two.
export default function ModelPage() {
  const { id } = useParams()
  const { t } = useTranslation('models')
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()
  const outlet = useOutletContext()
  const addToast = outlet?.addToast || (() => {})

  const { models: installedList, loading: installedLoading, error: installedError, refetch: refetchInstalled } = useModels()
  const profile = installedList.find(m => m.id === id) || null
  const installed = !!profile
  // What this model takes on disk, for an admin. Read only for an installed one.
  const storage = useModelStorage(installed)
  const gallery = useGalleryEntry(id)
  const entry = gallery.entry
  const { loaded, refresh: refreshLoaded } = useLoadedModels()
  const { resources } = useResources()
  const { operations, cancelOperation, dismissFailedOp } = useOperations()

  const budget = modelBudget(resources)
  const ramAvailable = resources?.ram?.available ?? resources?.ram?.free ?? null
  const disk = diskState(resources)

  const running = installed && !profile.disabled && (loaded.has(id) || (Array.isArray(profile.loaded_on) && profile.loaded_on.length > 0))

  // --- builds and the estimate ---------------------------------------------
  const variantsState = useVariants(id, !!entry?.has_variants)
  const variants = variantsState.status === 'ready' ? variantsState.data.variants : []
  const autoBuild = variantsState.data?.auto_selected || null
  const [pickedBuild, setPickedBuild] = useState(null)
  // A pick from another model is not a pick here.
  useEffect(() => { setPickedBuild(null) }, [id])
  const build = pickedBuild && variants.some(v => v.model === pickedBuild) ? pickedBuild : (autoBuild || null)
  const buildName = build || id
  const buildVariant = variants.find(v => v.model === build) || null

  const estimateState = useModelEstimate(buildName, {
    installed: installed && buildName === id,
    enabled: !!id && (gallery.status === 'ready' || installed),
  })
  const [contextSize, setContextSize] = useState(CONTEXT_DEFAULT)
  const vramBytes = estimateState.data?.estimates?.[String(contextSize)]?.vramBytes
  const fit = fitFor(vramBytes, budget, ramAvailable)
  const sizeBytes = estimateState.data?.sizeBytes || 0

  // --- actions ---------------------------------------------------------------
  const afterAction = useCallback(async () => {
    refetchInstalled()
    await refreshLoaded()
  }, [refetchInstalled, refreshLoaded])
  const actions = useModelActions({ addToast, afterAction })
  const pending = actions.pendingActions.has(id)

  const [starting, setStarting] = useState(false)
  const installOp = operations.find(op => op.name === id && !op.completed && !op.error) || null
  const failedOp = !installOp ? operations.find(op => op.name === id && op.error) || null : null
  const installing = starting || !!installOp
  const wasInstalling = useRef(false)
  useEffect(() => {
    if (installOp) setStarting(false)
  }, [installOp])
  useEffect(() => {
    // The operation leaving the list is the one moment the installed state and
    // the gallery's installed flag can have changed.
    if (wasInstalling.current && !installing) {
      refetchInstalled()
      gallery.reload()
    }
    wasInstalling.current = installing
  // gallery.reload is stable; the effect follows the install flag alone.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [installing, refetchInstalled])

  const install = async (variant) => {
    setStarting(true)
    window.setTimeout(() => setStarting(false), STARTING_GRACE_MS)
    try {
      await modelsApi.install(id, variant || undefined)
    } catch (err) {
      setStarting(false)
      addToast(t('errors.installFailed', { message: err.message }), 'error')
    }
  }
  const retryInstall = async () => {
    if (failedOp?.jobID) await dismissFailedOp(failedOp.jobID)
    install(build && build !== id ? build : undefined)
  }

  // --- tabs ---------------------------------------------------------------------
  const hasGallery = gallery.status === 'ready'
  const tabs = useMemo(() => {
    const list = [{ id: 'overview', label: t('page.tabs.overview') }, { id: 'fit', label: t('page.tabs.fit') }]
    if (hasGallery) list.push({ id: 'variants', label: t('page.tabs.variants'), count: variants.length > 1 ? variants.length : null })
    if (installed) {
      list.push({ id: 'usage', label: t('page.tabs.usage') })
      list.push({ id: 'config', label: t('page.tabs.config') })
      list.push({ id: 'logs', label: t('page.tabs.logs') })
    }
    return list
  }, [t, hasGallery, installed, variants.length])
  const requested = searchParams.get('tab')
  const tab = tabs.some(item => item.id === requested) ? requested : 'overview'
  const goTab = useCallback((next) => {
    setSearchParams(previous => {
      const params = new URLSearchParams(previous)
      if (next === 'overview') params.delete('tab')
      else params.set('tab', next)
      return params
    }, { replace: true, state: location.state })
  }, [setSearchParams, location.state])

  // --- walking and leaving -------------------------------------------------------
  // Only a page opened from a list has a list to walk. A pasted link has none,
  // even though the list mounted behind it has rows of its own.
  const walk = location.state?.from ? walkFor(id) : null
  const goBack = useCallback(() => {
    // Arrived from the list: step back to it, so its filters and scroll are the
    // ones it still holds. A pasted link has no list behind it.
    if (location.state?.from) navigate(-1)
    else navigate(installed ? '/app/models?view=installed' : '/app/models')
  }, [location.state, navigate, installed])
  const goTo = useCallback((target) => {
    if (!target) return
    // The tab is read from the address, not from this render: a key pressed
    // right after a tab change can arrive before the page has re-rendered.
    const current = new URLSearchParams(window.location.search).get('tab')
    navigate(`${modelPath(target)}${current ? `?tab=${encodeURIComponent(current)}` : ''}`, { replace: true, state: location.state })
  }, [navigate, location.state])

  const keys = useRef({})
  keys.current = { tabs, goTab, goBack, goTo, walk }
  useEffect(() => {
    const onKey = (e) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
      if (isTypingTarget(e.target)) return
      if (document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"], [role="menu"]')) return
      const { tabs: list, goTab: pick, goBack: back, goTo: step, walk: w } = keys.current
      if (/^[1-9]$/.test(e.key)) {
        const target = list[Number(e.key) - 1]
        if (target) { e.preventDefault(); pick(target.id) }
      } else if (e.key === '[' || e.key === 'k') {
        if (w?.previous) { e.preventDefault(); step(w.previous) }
      } else if (e.key === ']' || e.key === 'j') {
        if (w?.next) { e.preventDefault(); step(w.next) }
      } else if (e.key === 'Escape' || e.key === 'Backspace') {
        e.preventDefault()
        back()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  // Tab list as a roving group: arrows, Home and End.
  const onTabKeyDown = (e) => {
    // The roving position is the tab that has focus, which is what the reader
    // is looking at, and cannot lag a render the way the selected tab can.
    const focused = e.target.closest?.('[role="tab"]')?.id?.replace('modelpage-tab-', '')
    const index = Math.max(0, tabs.findIndex(item => item.id === (focused || tab)))
    let next = index
    if (e.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (e.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = tabs.length - 1
    else return
    e.preventDefault()
    goTab(tabs[next].id)
    document.getElementById(`modelpage-tab-${tabs[next].id}`)?.focus()
  }

  // --- states that replace the page ------------------------------------------------
  const knownInstalledList = !installedLoading || installedList.length > 0
  const loadingPage = (installedLoading && !installed) || (gallery.status === 'loading' && !installed)
  const missing = !loadingPage && !installed && gallery.status === 'missing' && knownInstalledList
  const offline = !loadingPage && !installed && gallery.status === 'error'

  const backButton = (
    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={goBack} data-testid="model-page-back" aria-keyshortcuts="Escape">
      <Icon name="arrow-left" /> {t('page.back')}
    </button>
  )

  if (loadingPage) {
    return (
      <div className="page page--wide page--app modelpage" data-testid="model-page" data-state="loading" aria-busy="true">
        <div className="modelpage-top">{backButton}</div>
        <div className="modelpage-skeleton" role="status" aria-label={t('page.loading')}>
          <span className="dk-skeleton dk-skeleton--title modelpage-skeleton__title" />
          <span className="dk-skeleton dk-skeleton--line modelpage-skeleton__chips" />
          <span className="dk-skeleton dk-skeleton--line modelpage-skeleton__lede" />
          <span className="dk-skeleton dk-skeleton--block modelpage-skeleton__body" />
        </div>
      </div>
    )
  }

  if (missing) {
    return (
      <div className="page page--wide page--app modelpage" data-testid="model-page" data-state="missing">
        <div className="modelpage-top">{backButton}</div>
        <div className="dk-empty modelpage-empty" data-testid="model-page-missing">
          <div className="dk-empty-icon"><Icon name="search" /></div>
          <h1 className="dk-empty-title">{t('page.missing.title', { id })}</h1>
          <p className="dk-empty-text">{t('page.missing.text')}</p>
          {gallery.matches.length > 0 && (
            <ul className="modelpage-matches" aria-label={t('page.missing.closest')}>
              {gallery.matches.map(m => {
                const name = m.name || m.id
                return (
                  <li key={name}>
                    <Link className="dk-link dk-mono" to={modelPath(name)} state={location.state}>{name}</Link>
                  </li>
                )
              })}
            </ul>
          )}
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={goBack}>
            <Icon name="arrow-left" /> {t('page.missing.back')}
          </button>
        </div>
      </div>
    )
  }

  if (offline) {
    return (
      <div className="page page--wide page--app modelpage" data-testid="model-page" data-state="offline">
        <div className="modelpage-top">{backButton}</div>
        <header className="modelpage-head">
          <div className="modelpage-title">
            <h1 className="modelpage-name dk-mono">{id}</h1>
          </div>
          <div className="modelpage-primary">
            <button type="button" className="dk-btn dk-btn--primary" disabled data-testid="model-page-install">
              <Icon name="download" /> {t('actions.install')}
            </button>
            <span className="modelpage-primary__sub">{t('page.offline.installDisabled')}</span>
          </div>
        </header>
        <div className="ledger-banner ledger-banner--error" role="alert" data-testid="model-page-offline">
          <Icon name="alert-circle" />
          <span><strong>{t('ledger.offline.title')}</strong> {t('page.offline.text')}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={gallery.reload}>
            <Icon name="refresh" /> {t('ledger.retry')}
          </button>
        </div>
      </div>
    )
  }

  // --- the page --------------------------------------------------------------------
  const group = groupForEntity({ tags: entry?.tags, backend: entry?.backend || profile?.backend, name: id })
  const useCases = installed ? modelUseCases(profile) : []
  const backend = entry?.backend || profile?.backend || ''
  const license = entry?.license || ''
  const lede = entry?.description ? stripMarkdown(entry.description) : ''
  const stateLabel = !installed ? null
    : profile.disabled ? { key: 'disabled', text: t('lifecycle.states.disabled') }
      : running ? { key: 'running', text: t('lifecycle.states.running') }
        : { key: 'idle', text: t('lifecycle.states.idle') }

  const leaves = !installed && sizeBytes ? leavesFree(disk, sizeBytes) : null
  const installLabel = buildVariant ? t('page.installBuild', { build: buildLabel(buildVariant) }) : t('actions.install')
  const installSub = sizeBytes
    ? (leaves === null
      ? gbLabel(sizeBytes)
      : leaves < 0
        ? t('disk.notEnough', { amount: gbLabel(-leaves), size: gbLabel(sizeBytes) })
        : t('disk.leaves', { size: gbLabel(sizeBytes), amount: gbLabel(leaves) }))
    : ''

  const menuItems = installed ? [
    {
      key: 'toggle',
      icon: profile.disabled ? 'toggle-on' : 'toggle-off',
      label: profile.disabled ? t('lifecycle.actions.enable') : t('lifecycle.actions.disable'),
      onClick: () => actions.toggleState(id, profile.disabled),
      disabled: pending,
    },
    {
      key: 'pin',
      icon: 'pin',
      label: profile.pinned ? t('lifecycle.actions.unpin') : t('lifecycle.actions.pin'),
      onClick: () => actions.togglePinned(id, profile.pinned),
      disabled: pending || !!profile.disabled,
    },
    { key: 'edit', icon: 'edit', label: t('lifecycle.actions.edit'), onClick: () => goTab('config') },
    { key: 'logs', icon: 'terminal', label: t('lifecycle.actions.logs'), onClick: () => goTab('logs') },
    { divider: true },
    {
      key: 'delete',
      icon: 'trash',
      label: t('lifecycle.actions.delete'),
      danger: true,
      onClick: () => actions.remove(id, () => navigate('/app/models?view=installed', { replace: true })),
    },
  ] : []

  const progress = installOp?.progress > 0 ? Math.round(installOp.progress) : 0
  const bytesLine = Number.isFinite(installOp?.currentBytes) && installOp?.totalBytes > 0
    ? [
      t('page.install.bytes', { done: formatBytes(installOp.currentBytes), total: formatBytes(installOp.totalBytes) }),
      installOp.bytesPerSecond > 0 ? `${formatBytes(installOp.bytesPerSecond)}/s` : '',
      installOp.etaSeconds > 0 ? t('page.install.eta', { time: etaText(installOp.etaSeconds, t) }) : '',
    ].filter(Boolean).join(', ')
    : ''

  const view = {
    id, t, entry, profile, installed, running, group, backend, license, useCases,
    lede, estimateState, contextSize, setContextSize, fit, budget, ramAvailable, resources, disk, sizeBytes, vramBytes,
    variants, variantsState, build, buildName, pickBuild: setPickedBuild, autoBuild,
    installing, installOp, progress, failedOp, install, retryInstall, leaves,
    galleryStatus: gallery.status, location, goTab,
    storage,
    installedIds: new Set(installedList.map(m => m.id)),
    toast: addToast,
  }

  return (
    <div className="page page--wide page--app modelpage" data-testid="model-page" data-state="ready" data-model={id}>
      <div className="modelpage-top">
        {backButton}
        {walk && (
          <div className="modelpage-walk" role="group" aria-label={t('page.walk.label')} data-testid="model-page-walk">
            <button
              type="button"
              className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
              disabled={!walk.previous}
              onClick={() => goTo(walk.previous)}
              aria-label={t('page.walk.previous')}
              aria-keyshortcuts="["
              data-testid="model-page-prev"
            >
              <Icon name="chevron-up" />
            </button>
            <span className="modelpage-walk__count" data-testid="model-page-position">{t('page.walk.position', { index: walk.index + 1, total: walk.total })}</span>
            <button
              type="button"
              className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm"
              disabled={!walk.next}
              onClick={() => goTo(walk.next)}
              aria-label={t('page.walk.next')}
              aria-keyshortcuts="]"
              data-testid="model-page-next"
            >
              <Icon name="chevron-down" />
            </button>
          </div>
        )}
      </div>

      {gallery.status === 'error' && (
        <div className="ledger-banner ledger-banner--error" role="alert" data-testid="model-page-offline">
          <Icon name="alert-circle" />
          <span><strong>{t('ledger.offline.title')}</strong> {t('page.offline.installedOnly')}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={gallery.reload}>
            <Icon name="refresh" /> {t('ledger.retry')}
          </button>
        </div>
      )}
      {installedError && !installed && (
        <div className="ledger-banner ledger-banner--error" role="alert">
          <Icon name="alert-circle" />
          <span>{t('lifecycle.errors.loadList', { message: installedError })}</span>
        </div>
      )}

      <header className="modelpage-head">
        <div className="modelpage-title">
          <h1 className="modelpage-name dk-mono" data-testid="model-page-name">{id}</h1>
          <div className="modelpage-chips">
            <span className="dk-chip dk-chip--sm modelpage-chip">{t(group.labelKey)}</span>
            {backend && <span className="dk-chip dk-chip--sm modelpage-chip dk-mono">{backend}</span>}
            {license && <span className="dk-chip dk-chip--sm modelpage-chip dk-mono">{license}</span>}
            {stateLabel && (
              <span className="dk-status modelpage-state" data-state={stateLabel.key} data-testid="model-page-state">
                <span className={`dk-dot${stateLabel.key === 'running' ? ' dk-dot--ok' : ''}`} />
                {pending ? t('lifecycle.states.working') : stateLabel.text}
              </span>
            )}
            {profile?.pinned && <span className="dk-chip dk-chip--sm modelpage-chip"><Icon name="pin" /> {t('lifecycle.detail.pinned')}</span>}
          </div>
          {lede && <p className="modelpage-lede" title={lede}>{lede}</p>}
        </div>

        <div className="modelpage-primary" data-testid="model-page-primary">
          {installing ? (
            <div className="modelpage-install" data-testid="model-page-installing">
              <div className="modelpage-install__head">
                <strong>{t('page.install.title', { build: buildVariant ? buildLabel(buildVariant) : '' }).trim()}</strong>
                <span className="dk-mono">{progress > 0 ? `${progress}%` : ''}</span>
              </div>
              <div
                className="dk-progress"
                role="progressbar"
                aria-label={t('page.install.title', { build: '' }).trim()}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={progress}
                data-indeterminate={progress > 0 ? undefined : ''}
                style={cssVars({ '--dk-value': `${progress}%` })}
              >
                <span className="dk-progress-bar" />
              </div>
              <div className="modelpage-install__foot">
                <span className="modelpage-primary__sub">{bytesLine}</span>
                {installOp?.cancellable && (
                  <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => cancelOperation(installOp.jobID)} data-testid="model-page-cancel">
                    {t('page.install.cancel')}
                  </button>
                )}
              </div>
            </div>
          ) : installed ? (
            <>
              <div className="modelpage-primary__row">
                {!profile.disabled && !running && (
                  <button type="button" className="dk-btn dk-btn--primary" disabled={pending} onClick={() => actions.load(id)} data-testid="model-page-load">
                    <Icon name={pending ? 'spinner' : 'bolt'} spin={pending} /> {pending ? t('lifecycle.actions.loading') : t('lifecycle.actions.load')}
                  </button>
                )}
                {running && (
                  <button type="button" className="dk-btn dk-btn--secondary" disabled={pending} onClick={() => actions.stop(id)} data-testid="model-page-stop">
                    <Icon name="stop" /> {t('lifecycle.actions.stop')}
                  </button>
                )}
                {profile.disabled && (
                  <button type="button" className="dk-btn dk-btn--secondary" disabled={pending} onClick={() => actions.toggleState(id, true)} data-testid="model-page-enable">
                    <Icon name="toggle-on" /> {t('lifecycle.actions.enable')}
                  </button>
                )}
                <ActionMenu
                  ariaLabel={t('lifecycle.actions.forModel', { model: id })}
                  triggerLabel={t('lifecycle.actions.forModel', { model: id })}
                  items={menuItems}
                />
              </div>
              <span className="modelpage-primary__sub">
                {running ? t('page.primary.running') : profile.disabled ? t('page.primary.disabled') : t('page.primary.idle')}
              </span>
            </>
          ) : (
            <>
              <div className="modelpage-primary__row">
                {failedOp ? (
                  <button type="button" className="dk-btn dk-btn--primary" onClick={retryInstall} data-testid="model-page-install">
                    <Icon name="refresh" /> {t('ledger.retry')}
                  </button>
                ) : (
                  <div className="modelpage-split">
                    <button
                      type="button"
                      className="dk-btn dk-btn--primary modelpage-split__main"
                      onClick={() => install(build && build !== id ? build : undefined)}
                      data-testid="model-page-install"
                    >
                      <Icon name="download" /> {installLabel}
                    </button>
                    {variants.length > 1 && (
                      <BuildChooser variants={variants} build={build} autoBuild={autoBuild} onPick={setPickedBuild} t={t} />
                    )}
                  </div>
                )}
              </div>
              {installSub && <span className={`modelpage-primary__sub${leaves !== null && leaves < 0 ? ' modelpage-primary__sub--short' : ''}`} data-testid="leaves-free">{installSub}</span>}
            </>
          )}
        </div>
      </header>

      {failedOp && (
        <div className="attention-callout attention-callout--error modelpage-callout" role="alert" data-testid="model-page-failed">
          <span><Icon name="alert-circle" className="icon-before" />{t('ledger.failedInstall', { message: failedOp.error })}</span>
        </div>
      )}
      {actions.actionErrors[id] && (
        <div className="attention-callout attention-callout--error modelpage-callout" role="alert">
          <span><Icon name="alert-circle" className="icon-before" />{actions.actionErrors[id]}</span>
        </div>
      )}

      <div className="dk-tabs modelpage-tabs" role="tablist" aria-label={t('page.tabs.label')} onKeyDown={onTabKeyDown}>
        {tabs.map((item, index) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            id={`modelpage-tab-${item.id}`}
            aria-controls="modelpage-panel"
            aria-selected={tab === item.id}
            aria-keyshortcuts={String(index + 1)}
            tabIndex={tab === item.id ? 0 : -1}
            className="dk-tab"
            data-testid={`model-page-tab-${item.id}`}
            onClick={() => goTab(item.id)}
          >
            {item.label}
            {item.count ? <span className="dk-hubtab-count">{item.count}</span> : null}
          </button>
        ))}
      </div>

      <div className="modelpage-panel" role="tabpanel" id="modelpage-panel" aria-labelledby={`modelpage-tab-${tab}`} tabIndex={0} data-testid={`model-page-panel-${tab}`}>
        {tab === 'overview' && <OverviewTab view={view} />}
        {tab === 'fit' && <FitTab view={view} />}
        {tab === 'variants' && <VariantsTab view={view} />}
        {tab === 'usage' && <UsageTab view={view} />}
        {tab === 'config' && <ConfigTab view={view} />}
        {tab === 'logs' && <LogsTab view={view} />}
      </div>

      <p className="modelpage-keys dk-hide-phone" aria-hidden="true">
        {walk && <span><kbd className="dk-kbd">[</kbd><kbd className="dk-kbd">]</kbd> {t('page.keys.walk')}</span>}
        <span><kbd className="dk-kbd">1</kbd> - <kbd className="dk-kbd">{tabs.length}</kbd> {t('page.keys.tabs')}</span>
        <span><kbd className="dk-kbd">esc</kbd> {t('page.keys.back')}</span>
      </p>

      <ConfirmDialog
        open={!!actions.confirmDialog}
        title={actions.confirmDialog?.title}
        message={actions.confirmDialog?.message}
        confirmLabel={actions.confirmDialog?.confirmLabel}
        danger={actions.confirmDialog?.danger}
        onConfirm={actions.confirmDialog?.onConfirm}
        onCancel={() => actions.setConfirmDialog(null)}
      />
    </div>
  )
}

function etaText(seconds, t) {
  if (seconds < 90) return t('page.install.seconds', { count: Math.round(seconds) })
  return t('page.install.minutes', { count: Math.round(seconds / 60) })
}

// The chevron next to Install: picks which build the button installs.
// eslint-disable-next-line no-unused-vars
function BuildChooser({ variants, build, autoBuild, onPick, t }) {
  const anchor = useRef(null)
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        ref={anchor}
        type="button"
        className="dk-btn dk-btn--primary dk-btn--icon modelpage-split__chevron"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('page.chooseBuild')}
        data-testid="model-page-builds"
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="chevron-down" />
      </button>
      <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('page.chooseBuild')}>
        <div
          role="menu"
          className="action-menu modelpage-builds"
          aria-label={t('page.chooseBuild')}
          // Focus goes to the chosen build when the menu opens, and the arrow
          // keys move between builds: the menu lives outside the page's tab order.
          ref={el => { if (el && open && !el.contains(document.activeElement)) (el.querySelector('[aria-checked="true"]') || el.querySelector('button'))?.focus({ preventScroll: true }) }}
          onKeyDown={e => {
            const items = Array.from(e.currentTarget.querySelectorAll('[role="menuitemradio"]'))
            const at = items.indexOf(document.activeElement)
            if (e.key === 'ArrowDown') { e.preventDefault(); items[(at + 1) % items.length]?.focus() }
            else if (e.key === 'ArrowUp') { e.preventDefault(); items[(at - 1 + items.length) % items.length]?.focus() }
          }}
        >
          {variants.map(v => (
            <button
              key={v.model}
              type="button"
              role="menuitemradio"
              aria-checked={v.model === (build || autoBuild)}
              className="action-menu__item"
              data-testid={`model-page-build-${v.model}`}
              onClick={() => { onPick(v.model); setOpen(false) }}
            >
              <span className="action-menu__label dk-mono">{buildLabel(v)}</span>
              <span className="action-menu__shortcut">{v.memory_bytes ? formatBytes(v.memory_bytes) : t('variants.unknownSize')}</span>
            </button>
          ))}
        </div>
      </Popover>
    </>
  )
}
