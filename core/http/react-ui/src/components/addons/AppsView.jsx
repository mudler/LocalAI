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
import AddonPreview from './AddonPreview'
import AddonSlot from '../../addons/AddonSlot'
import { useHubAuth } from '../hub/useHubAuth'
import AddonTile from './AddonTile'
import ConfirmDialog from '../ConfirmDialog'
import { useNavigation } from '../../context/NavigationContext'
import { buildTiles, filterTiles, resolvePins } from '../../utils/addonTiles'
import { UnknownNotice } from './AddonBits'
import { showFirstRun } from './addonState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// The apps every caller with the add-ons feature may use. The owner decides
// what is listed; this view only draws it. The list is refreshed on a bounded
// cadence, slower in a hidden tab and backing off while the owner is
// unreachable. Start and stop are offered to administrators and the server
// refuses everyone else whatever this view shows.
export default function AppsView({ api, adapter }) {
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

  // Remove is the typed uninstall action: the data is kept, so installing the
  // add-on again picks it up. The owner refuses anyone but an administrator.
  const [removing, setRemoving] = useState(null)
  const remove = useCallback((tile) => {
    actions.run(`uninstall:${tile.id}:${tile.app.generation}`, {
      addonId: tile.id,
      submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'uninstall', addonId: tile.id, generation: 0 }, signal),
      verify: async (signal) => !(await api.installed(signal)).some(a => a.id === tile.id),
    })
  }, [actions, adapter, api])

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
  const pinOrder = useMemo(() => resolvePins(nav.pinIds, tiles).map(x => x.id), [nav.pinIds, tiles])
  const pinnedIds = useMemo(() => new Set(pinOrder), [pinOrder])
  // Pinned apps come first, in pin order; the rest keep the catalog order.
  const shown = useMemo(() => {
    const found = filterTiles(tiles, query, titleOf)
    const rank = new Map(pinOrder.map((id, i) => [id, i]))
    return [...found.filter(x => rank.has(x.id)).sort((a, b) => rank.get(a.id) - rank.get(b.id)), ...found.filter(x => !rank.has(x.id))]
  }, [tiles, query, titleOf, pinOrder])
  const detail = tiles.find(x => x.id === detailId) || null

  // The source of an add-on is not in the apps list. An administrator's pane
  // reads it once from the installed list the host already serves.
  const [installedInfo, setInstalledInfo] = useState({})
  const detailApp = detail ? detail.app : null
  useEffect(() => {
    if (!isAdmin || !detailApp) return undefined
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(list => setInstalledInfo(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => { /* the pane says the source is not known */ })
    return () => ctl.abort()
  }, [api, isAdmin, detailApp && detailApp.id, detailApp && detailApp.generation])

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

  const firstRun = showFirstRun({ apps: state.apps, isAdmin, canList })
  return (
    <div className="ad-apps">
      {state.stale && (
        <div className="op-notice" role="status">
          <Icon name="alert-circle" />
          <span>{t('apps.stale')}</span>
        </div>
      )}
      {Object.entries(actions.state).filter(([key, r]) => key.startsWith('uninstall:') && r.outcome && r.outcome !== 'completed' && !actions.unknown[key.split(':')[1]]).map(([key, r]) => (
        <p key={key} className="ad-card__note" data-tone="error" role="alert" data-testid="remove-error">{describeOutcome(r.outcome, t, r.error)}</p>
      ))}
      {Object.keys(actions.unknown).filter(id => actions.unknown[id].key.startsWith('uninstall:')).map(id => (
        <UnknownNotice key={id} addonId={id} actions={actions} className="ad-card__note" />
      ))}
      <div className="ad-split" data-open={detail ? 'true' : 'false'}>
        <div className="ad-split__list">
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
          {/* One way into Browse per screen: the first-run card carries it while
              nothing is installed, and the bar carries it afterwards. */}
          {isAdmin && !firstRun && (
            <Link className="dk-btn dk-btn--primary dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-add"><Icon name="plus" /> {t('drawer.add')}</Link>
          )}
        </div>
        {firstRun ? (
          <section className="ad-card ad-first" data-testid="addons-first-run" aria-labelledby="ad-first-title">
            <h2 className="ad-card__title" id="ad-first-title">{t('apps.firstRun.title')}</h2>
            <ol className="ad-first__steps">
              <li><strong>{t('apps.firstRun.libraryTitle')}</strong> {t('apps.firstRun.libraryBody')}</li>
              <li><strong>{t('apps.firstRun.browseTitle')}</strong> {t('apps.firstRun.browseBody')}</li>
              <li><strong>{t('apps.firstRun.startTitle')}</strong> {t('apps.firstRun.startBody')}</li>
            </ol>
            <div className="ad-section__acts">
              <Link className="dk-btn dk-btn--primary" to="/app/addons?tab=browse" data-testid="addons-first-run-browse"><Icon name="store" /> {t('apps.firstRun.browse')}</Link>
            </div>
          </section>
        ) : shown.length === 0 ? (
          <CatalogEmpty icon="puzzle" title={t('apps.emptyTitle')} text={t('apps.emptyBody')} />
        ) : (
          <ul className="ad-tiles" aria-label={t('apps.listLabel')}>
            {shown.map(tile => (
              <AddonTile key={tile.id} tile={tile} pinned={pinnedIds.has(tile.id)} selected={tile.id === detailId} onPin={onPin} onMove={nav.move} onOpenDetail={openDetail} onRemove={isAdmin ? setRemoving : undefined} />
            ))}
          </ul>
        )}
        </div>
        {detail && (
          <AddonPreview
            tile={detail}
            title={titleOf(detail)}
            language={i18n.language}
            pinned={pinnedIds.has(detail.id)}
            onPin={() => onPin(detail.id)}
            onMove={(delta) => nav.move(detail.id, delta)}
            onClose={closeDetail}
            actions={actions}
            onStartStop={startStop}
            onRemove={isAdmin && detail.app ? () => setRemoving(detail) : undefined}
            isAdmin={isAdmin}
            source={installedInfo[detail.id]}
            links={detail.app ? navigationItems([detail.app], { language: i18n.language }) : []}
          />
        )}
      </div>
      {!legacy && <div className="ad-nav-slot"><AddonSlot slot="app.nav" context={null} /></div>}
      <ConfirmDialog
        open={Boolean(removing)}
        danger
        title={removing ? t('card.removeTitle', { name: removing.title }) : ''}
        message={removing ? <p>{t('card.removeBody', { name: removing.title })}</p> : null}
        confirmLabel={t('card.remove')}
        onConfirm={() => { const x = removing; setRemoving(null); closeDetail(); remove(x) }}
        onCancel={() => setRemoving(null)}
      />
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}
