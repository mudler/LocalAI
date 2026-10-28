/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
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
import { OperatorFacts } from './OperatorFacts'
import UpdateReviewSheet from '../UpdateReviewSheet'
import { newOperationId } from '../catalog/catalogState'
import { updateView } from './updateState'
import { reviewItems, upgradeFailureWords, upgradeVersionOf } from './updateReview'
import { runBatch } from '../../utils/updateReview'
import { addonUpgrades } from '../../utils/addonUpgrades'
import { useAddonUpgrades } from '../../hooks/useAddonUpgrades'
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
  const actions = useAddonActions(api, {
    onSettled: () => {
      reloadRef.current()
      setInstalledReload(n => n + 1)
    },
    adapter,
  })
  // The upgrades this browser started live in a store of their own, so one
  // goes on when this page is left. The lists are read again whenever one ends.
  const upgrades = useAddonUpgrades()
  const upgradeOf = useCallback((id) => upgrades.live.find(op => op.addonId === id) || null, [upgrades.live])
  const upgradedIds = useMemo(() => new Set(upgrades.records.filter(r => r.outcome === 'completed').map(r => r.addonId)), [upgrades.records])
  const endedCount = upgrades.records.length
  useEffect(() => {
    if (endedCount === 0) return
    reloadRef.current()
    setInstalledReload(n => n + 1)
  }, [endedCount])

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

  const outlet = useOutletContext()
  const addToast = outlet?.addToast
  const installedList = useMemo(() => Object.values(installedInfo), [installedInfo])
  const pending = useMemo(() => reviewItems(installedList, t), [installedList, t])

  // Reads the library of every installed add-on again. Only reads: it never
  // applies anything, and one add-on that cannot be checked does not stop the
  // rest.
  const [checkingAll, setCheckingAll] = useState(false)
  const checkAll = useCallback(async () => {
    const signal = aborter.current.signal
    setCheckingAll(true)
    let failed = 0
    for (const addon of installedList) {
      try {
        await api.updates.check(addon.id, { operationId: newOperationId() }, signal)
      } catch (error) {
        if (error && error.name === 'AbortError') return
        failed += 1
      }
    }
    setCheckingAll(false)
    setInstalledReload(n => n + 1)
    addToast?.(failed > 0 ? t('updates.checkAllFailed', { count: failed }) : t('updates.checkAllDone', { count: installedList.length }), failed > 0 ? 'error' : 'info')
  }, [api, installedList, addToast, t])

  // The review sheet: one place where upgrades are chosen and started.
  const [review, setReview] = useState(null)
  const installedRef = useRef(installedInfo)
  installedRef.current = installedInfo
  const runReview = useCallback((chosen, onProgress) => runBatch(chosen, async (item) => {
    const addon = installedRef.current[item.key]
    if (!addon) return { ok: false, message: upgradeFailureWords('stale_installed', t) }
    const out = await addonUpgrades.start(api, addon)
    return { ok: out.ok, message: out.ok ? '' : upgradeFailureWords(out.message, t) }
  }, { onProgress }), [api, t])
  const reviewSettled = useCallback((results, chosen) => {
    for (const r of results) {
      const item = chosen.find(i => i.key === r.key)
      const name = item ? item.name : r.key
      if (r.ok) addToast?.(t('updates.toastDone', { name, version: item ? item.to : '' }), 'success')
      else addToast?.(t('updates.toastFailed', { name, message: r.message }), 'error')
    }
    setInstalledReload(n => n + 1)
    reloadRef.current()
  }, [addToast, t])

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
  const detailAppId = detailApp ? detailApp.id : ''
  const detailAppGeneration = detailApp ? detailApp.generation : 0
  // The installed records carry the owner's update answer with the versions
  // and digests, which the apps list does not. An administrator reads them
  // for the whole page (the update badge, the cards, the review sheet).
  const listSignature = state.apps.map(a => `${a.id}:${a.generation}:${a.update || ''}`).join(',')
  useEffect(() => {
    if (!isAdmin || !canList) return undefined
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(list => setInstalledInfo(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => { /* the pane says the source is not known */ })
    return () => ctl.abort()
  }, [api, isAdmin, canList, listSignature, detailAppId, detailAppGeneration, installedReload])

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
          {isAdmin && !firstRun && pending.length > 0 && (
            <button type="button" className="dk-btn dk-btn--sm dk-btn--primary ur-badge" onClick={() => setReview({ preselect: [] })} data-testid="addons-updates">
              <Icon name="arrow-up" /> {t('updates.reviewButton', { count: pending.length })}
            </button>
          )}
          {isAdmin && !firstRun && installedList.length > 0 && (
            <button type="button" className="dk-btn dk-btn--sm dk-btn--ghost" disabled={checkingAll} onClick={checkAll} data-testid="addons-check-all">
              <Icon name={checkingAll ? 'spinner' : 'refresh'} spin={checkingAll} /> {checkingAll ? t('updates.checkingAll') : t('updates.checkAll')}
            </button>
          )}
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
              <AddonTile key={tile.id} tile={tile} pinned={pinnedIds.has(tile.id)} selected={tile.id === detailId} onPin={onPin} onMove={nav.move} onOpenDetail={openDetail} onRemove={isAdmin ? setRemoving : undefined} upgradeTo={isAdmin ? upgradeVersionOf(installedInfo[tile.id]) : ''} onUpgrade={isAdmin ? (id) => setReview({ preselect: [id] }) : undefined} />
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
                  upgraded={upgradedIds.has(detail.id)}
                  upgradeOp={upgradeOf(detail.id)}
                  onCheck={() => check(installedInfo[detail.id])}
                  onUpgrade={() => setReview({ preselect: [detail.id] })}
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
      {review && (
        <UpdateReviewSheet
          title={t('updates.reviewTitle')}
          description={t('updates.reviewDescription')}
          items={pending}
          preselect={review.preselect}
          run={runReview}
          onSettled={reviewSettled}
          onClose={() => setReview(null)}
        />
      )}
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}
