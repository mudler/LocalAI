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
import { OperatorFacts, UpgradeSummary } from './OperatorFacts'
import { AddonsError } from '../../utils/addonsApi'
import { newOperationId } from '../catalog/catalogState'
import { updateView, upgradeBody } from './updateState'
import FirstRun from './FirstRun'
import { listState } from './addonState'
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

  // The installed record of an add-on, for the administrator's Details in the
  // pane. A change reads it again.
  const [installedInfo, setInstalledInfo] = useState({})
  const [installedReload, setInstalledReload] = useState(0)
  const [checks, setChecks] = useState({})
  const [upgraded, setUpgraded] = useState({})
  const actions = useAddonActions(api, {
    onSettled: (key, result) => {
      reloadRef.current()
      setInstalledReload(n => n + 1)
      // The key names the generation the click saw, which the list moves past
      // on success, so the outcome of an upgrade is kept by add-on.
      const [verb, id] = String(key).split(':')
      if (verb === 'upgrade' && result && result.outcome === 'completed') setUpgraded(u => ({ ...u, [id]: true }))
      if (verb === 'upgrade' && result && result.outcome !== 'completed') setUpgraded(u => { const n = { ...u }; delete n[id]; return n })
    },
    adapter,
  })

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

  // The explicit upgrade: the digest of the installed package this page showed
  // and the digest of the candidate the owner's check resolved. The owner
  // checks both again and refuses before it changes anything.
  const [confirmUpgrade, setConfirmUpgrade] = useState(null)
  const upgrade = useCallback((addon) => {
    actions.run(`upgrade:${addon.id}:${addon.generation}`, {
      addonId: addon.id,
      submit: (operationId, signal) => {
        const body = upgradeBody(addon, operationId)
        if (!body) return Promise.reject(new AddonsError({ kind: 'invalid' }))
        return api.updates.upgrade(addon.id, body, signal)
      },
      verify: async (signal) => {
        const now = (await api.installed(signal)).find(a => a.id === addon.id)
        return Boolean(now) && now.generation > addon.generation
      },
    })
  }, [actions, api])
  // Asks the owner to read the library again. It never applies anything.
  const aborter = useRef(null)
  useEffect(() => {
    aborter.current = new AbortController()
    return () => aborter.current.abort()
  }, [])
  const check = useCallback(async (addon) => {
    const signal = aborter.current.signal
    setChecks(c => ({ ...c, [addon.id]: { phase: 'working' } }))
    try {
      const status = await api.updates.check(addon.id, { operationId: newOperationId() }, signal)
      setChecks(c => ({ ...c, [addon.id]: { phase: 'done', throttled: Boolean(status && status.throttled) } }))
      setInstalledReload(n => n + 1)
    } catch (error) {
      if (error && error.name === 'AbortError') return
      setChecks(c => ({ ...c, [addon.id]: { phase: 'error', error } }))
    }
  }, [api])

  const nav = useNavigation()
  // Under the legacy switch the Build hub row draws the app.nav slot instead.
  const legacy = useHubAuth().features.addon_navigation === false
  const [query, setQuery] = useState('')
  const [params, setParams] = useSearchParams()
  const [limit, setLimit] = useState(false)
  // The management link of an add-on (?manage=id) opens its Details.
  const manageId = isAdmin ? (params.get('manage') || '') : ''
  const detailId = params.get('detail') || manageId
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

  const detailApp = detail ? detail.app : null
  useEffect(() => {
    if (!isAdmin || !detailApp) return undefined
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(list => setInstalledInfo(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => { /* the pane says the source is not known */ })
    return () => ctl.abort()
  }, [api, isAdmin, detailApp && detailApp.id, detailApp && detailApp.generation, installedReload])

  const onPin = async (id) => {
    if (pinnedIds.has(id)) { await nav.unpin(id); return }
    const result = await nav.pin(id)
    if (result.needsUnpin) setLimit(true)
  }
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); out.delete('manage'); return out })
  const closeDetail = () => setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); out.delete('manage'); return out }, { replace: true })

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

  const view = listState({ apps: state.apps, shownCount: shown.length, query, isAdmin, canList })
  const firstRun = view === 'first-run'
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
          {view === 'first-run' ? (
            <FirstRun adapter={adapter} actions={actions} />
          ) : view === 'no-match' ? (
            <CatalogEmpty icon="search" title={t('apps.noMatchTitle')} text={t('apps.noMatchBody', { query: query.trim() })} role="status">
              <div className="ad-section__acts">
                <button type="button" className="dk-btn dk-btn--sm" onClick={() => setQuery('')}>{t('apps.clearSearch')}</button>
                {isAdmin && <Link className="dk-btn dk-btn--ghost dk-btn--sm" to="/app/addons?tab=browse">{t('drawer.browse')}</Link>}
              </div>
            </CatalogEmpty>
          ) : view === 'none' ? (
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
            onClose={() => { const id = detail.id; closeDetail(); requestAnimationFrame(() => document.querySelector(`[data-testid="addon-tile"][data-app="${CSS.escape(id)}"] .ad-tile__open`)?.focus()) }}
            actions={actions}
            onStartStop={startStop}
            onRemove={isAdmin && detail.app ? () => setRemoving(detail) : undefined}
            isAdmin={isAdmin}
            source={installedInfo[detail.id]}
            links={detail.app ? navigationItems([detail.app], { language: i18n.language }) : []}
          >
            {isAdmin && installedInfo[detail.id] && (
              <details className="ad-pane__more" open={manageId === detail.id} data-testid="addon-more">
                <summary>{t('preview.moreDetails')}</summary>
                <OperatorFacts
                  addon={installedInfo[detail.id]}
                  api={api}
                  language={i18n.language}
                  actions={actions}
                  check={checks[detail.id]}
                  upgraded={Boolean(upgraded[detail.id])}
                  onCheck={() => check(installedInfo[detail.id])}
                  onUpgrade={() => setConfirmUpgrade(installedInfo[detail.id])}
                />
              </details>
            )}
          </AddonPreview>
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
      {manageId && state.status === 'ready' && !detail && (
        <div className="op-notice" role="status"><Icon name="info" /><span>{t('installed.missing', { id: manageId })}</span></div>
      )}
      <ConfirmDialog
        open={Boolean(confirmUpgrade)}
        title={confirmUpgrade ? t('updates.confirm.title', { name: confirmUpgrade.title, version: updateView(confirmUpgrade.update).version }) : ''}
        message={confirmUpgrade ? <UpgradeSummary addon={confirmUpgrade} t={t} /> : null}
        confirmLabel={t('updates.confirm.action')}
        onConfirm={() => { const a = confirmUpgrade; setConfirmUpgrade(null); upgrade(a) }}
        onCancel={() => setConfirmUpgrade(null)}
      />
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}
