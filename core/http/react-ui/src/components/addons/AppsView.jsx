/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import CatalogEmpty from '../catalog/CatalogEmpty'
import { pollDelay } from '../catalog/catalogState'
import { RegistryContext } from '../../addons/registryContext'
import { navigationItems } from '../../addons/registryState'
import { useAuth } from '../../context/AuthContext'
import { FEATURE_ADDONS } from '../../utils/capabilities'
import SideSheet from '../SideSheet'
import AddonSlot from '../../addons/AddonSlot'
import { useHubAuth } from '../hub/useHubAuth'
import AddonTile from './AddonTile'
import { useNavigation } from '../../context/NavigationContext'
import { buildTiles, filterTiles, resolvePins } from '../../utils/addonTiles'
import { apiUrl } from '../../utils/basePath'
import { StateChip, AppIcon, UnknownNotice, UpdateChip } from './AddonBits'
import { appActions, localizedText, toolSummary } from './addonState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// The apps every caller with the add-ons feature may use. The owner decides
// what is listed; this view only draws it. The list is refreshed on a bounded
// cadence, slower in a hidden tab and backing off while the owner is
// unreachable. Start and stop are offered to administrators and the server
// refuses everyone else whatever this view shows.
export default function AppsView({ api }) {
  const { t, i18n } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', apps: [], error: null, stale: false })
  const [reload, setReload] = useState(0)
  const reloadRef = useRef(() => {})
  const { hasFeature, isAdmin } = useAuth()
  // Without the feature there is no list to read and the route would answer 403.
  const canList = hasFeature(FEATURE_ADDONS)
  // The add-on registry does not read the list again while this tab is open:
  // every list read here is handed to it.
  const registry = useContext(RegistryContext)
  const ingestRef = useRef(null)
  ingestRef.current = registry ? registry.ingest : null

  // Poll with a timer chain: one request at a time, aborted on unmount. A
  // refresh asked for while one is in flight runs right after it, so there is
  // never more than one chain.
  useEffect(() => {
    if (!canList) { setState({ status: 'ready', apps: [], error: null, stale: false }); return undefined }
    const ctl = new AbortController()
    let timer = null
    let failures = 0
    let alive = true
    let inflight = false
    let queued = false
    const tick = async () => {
      if (inflight) { queued = true; return }
      inflight = true
      try {
        const apps = await api.apps.list(ctl.signal)
        failures = 0
        if (alive) {
          setState({ status: 'ready', apps, error: null, stale: false })
          ingestRef.current?.(apps)
        }
      } catch (error) {
        if (!alive || (error && error.name === 'AbortError')) return
        failures += 1
        // Keep what was on screen and say it may be out of date.
        setState(s => ({ status: s.status === 'ready' ? 'ready' : 'error', apps: s.apps, error, stale: true }))
      } finally {
        inflight = false
      }
      if (!alive) return
      const wait = queued ? 0 : pollDelay(failures, typeof document !== 'undefined' && document.hidden)
      queued = false
      clearTimeout(timer)
      timer = setTimeout(tick, wait)
    }
    reloadRef.current = () => { if (inflight) { queued = true } else { clearTimeout(timer); tick() } }
    tick()
    return () => { alive = false; ctl.abort(); clearTimeout(timer) }
  }, [api, reload, canList])

  const actions = useAddonActions(api, { onSettled: () => reloadRef.current() })

  const startStop = useCallback((app, verb) => {
    actions.run(`${verb}:${app.id}:${app.generation}`, {
      addonId: app.id,
      submit: (operationId, signal) => api.apps[verb](app.id, { operationId, generation: app.generation }, signal),
      // A completed status is not proof for this click: the owner answers a
      // replayed operation with its old status and applies nothing.
      verify: async (signal) => {
        const now = (await api.apps.list(signal)).find(a => a.id === app.id)
        return Boolean(now) && now.enabled === (verb === 'start')
      },
    })
  }, [actions, api])

  const nav = useNavigation()
  // Under the legacy switch the Build hub row draws the app.nav slot instead.
  const legacy = useHubAuth().features.addon_navigation === false
  const [query, setQuery] = useState('')
  const [params, setParams] = useSearchParams()
  const [limit, setLimit] = useState(false)
  const detailId = params.get('detail') || ''
  const { t: tn } = useTranslation('nav')

  const tiles = useMemo(() => buildTiles(state.apps, nav.builtin, { language: i18n.language }), [state.apps, nav.builtin, i18n.language])
  const titleOf = useCallback((tile) => (tile.titleKey ? tn(tile.titleKey) : tile.title), [tn])
  const shown = useMemo(() => filterTiles(tiles, query, titleOf), [tiles, query, titleOf])
  const pinnedIds = useMemo(() => new Set(resolvePins(nav.pinIds, tiles).map(x => x.id)), [nav.pinIds, tiles])
  const detail = tiles.find(x => x.id === detailId) || null

  const onPin = async (id) => {
    if (pinnedIds.has(id)) { await nav.unpin(id); return }
    const result = await nav.pin(id)
    if (result.needsUnpin) setLimit(true)
  }
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); return out })
  const closeDetail = () => setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); return out }, { replace: true })

  if (state.status === 'loading') {
    return <div className="op-loading" role="status" aria-label={t('apps.loading')}><LoadingSpinner size="lg" /></div>
  }
  if (state.status === 'error') {
    return (
      <div className="op-error" role="alert">
        <Icon name="alert-circle" />
        <span>{describeError(state.error, t)}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => { setState({ status: 'loading', apps: [], error: null, stale: false }); setReload(n => n + 1) }}>
          <Icon name="refresh" /> {t('catalog.retry')}
        </button>
      </div>
    )
  }

  return (
    <div className="ad-apps">
      {state.stale && (
        <div className="op-notice" role="status">
          <Icon name="alert-circle" />
          <span>{t('apps.stale')}</span>
        </div>
      )}
      <div className="ad-drawer__bar">
        <input
          type="search"
          className="dk-input ad-drawer__search"
          data-testid="addon-search"
          aria-label={t('drawer.search')}
          placeholder={t('drawer.search')}
          value={query}
          onChange={e => setQuery(e.target.value)}
        />
        {isAdmin && (
          <>
            <Link className="dk-btn dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-browse"><Icon name="search" /> {t('drawer.browse')}</Link>
            <Link className="dk-btn dk-btn--primary dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-add"><Icon name="plus" /> {t('drawer.add')}</Link>
          </>
        )}
      </div>
      {shown.length === 0 ? (
        <CatalogEmpty icon="puzzle" title={t('apps.emptyTitle')} text={t('apps.emptyBody')} />
      ) : (
        <ul className="ad-tiles" aria-label={t('apps.listLabel')}>
          {shown.map(tile => (
            <AddonTile key={tile.id} tile={tile} pinned={pinnedIds.has(tile.id)} onPin={onPin} onMove={nav.move} onOpenDetail={openDetail} />
          ))}
        </ul>
      )}
      {!legacy && <div className="ad-nav-slot"><AddonSlot slot="app.nav" context={null} /></div>}
      {detail && (
        <SideSheet title={titleOf(detail)} onClose={closeDetail} closeLabel={t('drawer.close')} testId="addon-detail">
          {detail.app
            ? (
              <>
                <ul className="ad-grid"><AppCard app={detail.app} t={t} language={i18n.language} actions={actions} onStartStop={startStop} /></ul>
                {navigationItems([detail.app], { language: i18n.language }).map(link => (
                  <Link key={link.path} className="dk-btn dk-btn--ghost dk-btn--sm" to={link.path}>{link.label}</Link>
                ))}
              </>
            )
            : <p>{t('drawer.builtinDetail')}</p>}
        </SideSheet>
      )}
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}

function AppCard({ app, t, language, actions, onStartStop }) {
  const title = localizedText(app, 'title', language)
  const description = localizedText(app, 'description', language)
  const control = appActions(app)
  const tools = toolSummary(app)
  const startKey = `start:${app.id}:${app.generation}`
  const stopKey = `stop:${app.id}:${app.generation}`
  const pending = Boolean(actions.unknown[app.id])
  const working = actions.state[startKey]?.phase === 'working' || actions.state[stopKey]?.phase === 'working'
  const locked = working || pending
  const result = actions.state[startKey]?.phase !== 'working' ? actions.state[startKey] : null
  const stopResult = actions.state[stopKey]?.phase !== 'working' ? actions.state[stopKey] : null
  const outcome = result || stopResult
  const titleId = `app-${app.id}`
  return (
    <li className="ad-card" data-app={app.id} aria-labelledby={titleId}>
      <div className="ad-card__head">
        <AppIcon src={app.icon ? apiUrl(app.icon) : ''} />
        <div className="ad-card__lead">
          <h3 className="ad-card__title" id={titleId}>{title}</h3>
          <p className="ad-card__meta">
            <span className="dk-mono">{app.id}</span>
            <span aria-hidden="true">·</span>
            <span>{t('apps.version', { version: app.version })}</span>
          </p>
        </div>
        <div className="ad-card__chips">
          <StateChip app={app} />
          {app.update === 'update-pending' && <UpdateChip />}
        </div>
      </div>
      {description && <p className="ad-card__text">{description}</p>}
      {!app.mainPage && tools.count > 0 && (
        <p className="ad-card__tools" data-testid="app-tools">
          <Icon name="wrench" aria-hidden="true" />
          <span>
            {t('apps.tools', { count: tools.count })}
            {': '}
            <span className="dk-mono">{tools.shown.join(', ')}</span>
            {tools.more > 0 ? ` ${t('apps.toolsMore', { count: tools.more })}` : ''}
          </span>
        </p>
      )}
      {!app.mainPage && !app.hasUI && <p className="ad-card__quiet">{t('apps.headless')}</p>}
      <div className="ad-card__acts">
        {app.mainPage && (
          <Link className="dk-btn dk-btn--primary dk-btn--sm" to={app.mainPage}>
            <Icon name="external-link" /> {t('apps.open')}
          </Link>
        )}
        {control.start && (
          <button type="button" className="dk-btn dk-btn--sm" disabled={locked} onClick={() => onStartStop(app, 'start')}>
            <Icon name={working ? 'spinner' : 'play'} spin={working} /> {t('apps.start')}
          </button>
        )}
        {control.stop && (
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={locked} onClick={() => onStartStop(app, 'stop')}>
            <Icon name={working ? 'spinner' : 'stop'} spin={working} /> {t('apps.stop')}
          </button>
        )}
        {control.manage && (
          <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={control.manage}>
            <Icon name="settings" /> {t('apps.manage')}
          </Link>
        )}
      </div>
      {working && <p className="ad-card__note" role="status">{t('apps.working')}</p>}
      {pending
        ? <UnknownNotice addonId={app.id} actions={actions} className="ad-card__note" />
        : outcome && outcome.outcome && <ActionResult outcome={outcome} t={t} />}
    </li>
  )
}

function ActionResult({ outcome, t }) {
  const text = describeOutcome(outcome.outcome, t)
  if (!text) return null
  return <p className="ad-card__note" data-tone="error" role="alert">{text}</p>
}
