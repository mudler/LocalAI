/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, LoadError, SearchField, Segmented, SkeletonRows } from '../ui'
import { newOperationId, pollDelay } from '../catalog/catalogState'
import { RegistryContext } from '../../addons/registryContext'
import { navigationItems } from '../../addons/registryState'
import { declaresSlot } from '../../addons/slots'
import { SETTINGS_VIEW, paneView } from '../../utils/addonSettings'
import { useAuth } from '../../context/AuthContext'
import { FEATURE_ADDONS } from '../../utils/capabilities'
import SideSheet from '../SideSheet'
import AddonPreview from './AddonPreview'
import AddonSlot from '../../addons/AddonSlot'
import { useHubAuth } from '../hub/useHubAuth'
import AddonRow from './AddonRow'
import ConfirmDialog from '../ConfirmDialog'
import { useNavigation } from '../../context/NavigationContext'
import { buildTiles, filterTiles, resolvePins } from '../../utils/addonTiles'
import { UnknownNotice } from './AddonBits'
import { OperatorFacts } from './OperatorFacts'
import { AddonUpgradeAction, UpgradeConfirm, reasonText } from './UpgradeControls'
import { useAddonUpgrade } from './useAddonUpgrade'
import { BulkUpgradeTrigger, BulkUpgradeDialog } from '../upgrade/BulkUpgradeDialog'
import { useBulkUpgrade } from '../../hooks/useBulkUpgrade'
import { updateView, upgradeAvailability } from './updateState'
import { upgradeFailureWords } from './updateReview'
import FirstRun from './FirstRun'
import { addedByAddon, attentionOf, filterByState, listState, sortByAttention, STATE_FILTERS } from './addonState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// The apps every caller with the add-ons feature may use. The owner decides
// what is listed; this view only draws it. The list is refreshed on a bounded
// cadence, slower in a hidden tab and backing off while the owner is
// unreachable. Start and stop are offered to administrators and the server
// refuses everyone else whatever this view shows.
export default function AppsView({ api, adapter, onSummary }) {
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
  const actions = useAddonActions(api, {
    onSettled: () => {
      reloadRef.current()
      setInstalledReload(n => n + 1)
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

  const upgrade = useAddonUpgrade(api, { onChanged: () => { setInstalledReload(n => n + 1); reloadRef.current() } })
  const checks = upgrade.checks
  const upgradedIds = useMemo(() => new Set(upgrade.records.filter(r => r.outcome === 'completed').map(r => r.addonId)), [upgrade.records])
  // An upgrade runs in the upgrade store and may end after this page was left
  // and opened again, or from Activity: the lists are read again whenever one
  // ends.
  const endedCount = upgrade.records.length
  useEffect(() => {
    if (endedCount === 0) return
    reloadRef.current()
    setInstalledReload(n => n + 1)
  }, [endedCount])
  const aborter = useRef(null)
  useEffect(() => {
    aborter.current = new AbortController()
    return () => aborter.current.abort()
  }, [])

  const outlet = useOutletContext()
  const addToast = outlet?.addToast
  const installedList = useMemo(() => Object.values(installedInfo), [installedInfo])

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

  const nav = useNavigation()
  // Under the legacy switch the Build hub row draws the app.nav slot instead.
  const legacy = useHubAuth().features.addon_navigation === false
  const [query, setQuery] = useState('')
  const [stateFilter, setStateFilter] = useState('all')
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
  // What needs a person goes first (failed, update, starting); the filter
  // narrows the rows but never the attention callouts above them.
  const rows = useMemo(() => filterByState(sortByAttention(shown, installedInfo), stateFilter), [shown, installedInfo, stateFilter])

  // The installed records, for the administrator: Details and the upgrade
  // action on the list both read them. A change of any add-on (generation,
  // notice) reads them again.
  const appsKey = state.apps.map(a => `${a.id}:${a.generation}:${a.update || ''}`).join('|')
  useEffect(() => {
    if (!isAdmin || !canList || !appsKey) return undefined
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(list => setInstalledInfo(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => { /* the pane says the source is not known */ })
    return () => ctl.abort()
  }, [api, isAdmin, canList, appsKey, installedReload])

  // Everything that has an update notice, for "Upgrade all": the owner's
  // record when the administrator can read it, with the reason when the
  // upgrade cannot start. The upgrade of each is the single-add-on path.
  const upgradeItems = useMemo(() => {
    if (!isAdmin) return []
    return state.apps.filter(a => a.update === 'update-pending' || updateView(installedInfo[a.id]?.update).state === 'update-available').map(a => {
      const rec = installedInfo[a.id]
      const av = upgradeAvailability(rec, { isAdmin, pending: true })
      return { id: a.id, name: a.title || a.id, from: rec ? rec.version : a.version, to: rec ? updateView(rec.update).version : '', startable: av.startable, reason: reasonText(av, t), addon: rec }
    })
  }, [state.apps, installedInfo, isAdmin, t])
  const bulk = useBulkUpgrade({
    items: upgradeItems,
    runOne: async (item) => {
      if (!item.addon) return { ok: false, message: upgradeFailureWords('stale_installed', t) }
      const out = await upgrade.upgrade(item.addon)
      return out.ok ? { ok: true } : { ok: false, message: upgradeFailureWords(out.message, t) }
    },
  })

  const firstRunNow = state.status === 'ready' && listState({ apps: state.apps, shownCount: tiles.length, query: '', isAdmin, canList }) === 'first-run'
  useEffect(() => {
    if (!onSummary) return
    onSummary(state.status === 'ready' ? { count: tiles.length, firstRun: firstRunNow } : null)
  }, [onSummary, state.status, tiles.length, firstRunNow])

  // The rows that need a person, for the callouts over the list: failures
  // first, then updates an administrator can start.
  const attention = useMemo(() => {
    const failed = []
    const updates = []
    for (const tile of tiles) {
      if (!tile.app) continue
      const kind = attentionOf(tile.app, installedInfo[tile.id])
      if (kind === 'failed') failed.push(tile)
      else if (kind === 'update' && isAdmin) {
        const rec = installedInfo[tile.id]
        const view = updateView(rec && rec.update)
        if (rec && view.state === 'update-available' && upgradeAvailability(rec, { isAdmin, pending: true }).startable) updates.push({ tile, rec, version: view.version })
      }
    }
    return { failed, updates }
  }, [tiles, installedInfo, isAdmin])

  const onPin = async (id) => {
    if (pinnedIds.has(id)) { await nav.unpin(id); return }
    const result = await nav.pin(id)
    if (result.needsUnpin) setLimit(true)
  }
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); out.delete('manage'); out.delete('view'); return out })
  const closeDetail = () => setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); out.delete('manage'); out.delete('view'); return out }, { replace: true })
  // The Settings tab of the details, for an administrator, when the add-on
  // declares a settings section (or has server settings of its own feature).
  const detailHasSettings = Boolean(isAdmin && detail && detail.app && declaresSlot(detail.app, 'settings.sections'))
  const paneTab = paneView(params, detailHasSettings)
  const setView = (next) => setParams(prev => {
    const out = new URLSearchParams(prev)
    if (next === SETTINGS_VIEW) out.set('view', SETTINGS_VIEW)
    else out.delete('view')
    return out
  }, { replace: true })

  if (state.status === 'loading') {
    return <SkeletonRows count={3} label={t('apps.loading')} className="ad-skeleton" />
  }
  if (state.status === 'error') {
    const kind = state.error && state.error.kind
    return (
      <LoadError
        title={t('apps.loadError.title')}
        testId="addons-load-error"
        nothingChanged={false}
        onRetry={() => { setState({ status: 'loading', apps: [], error: null, stale: false }); setReload(n => n + 1) }}
      >
        {[
          describeError(state.error, t),
          kind !== 'auth' && kind !== 'denied' && kind !== 'invalid' ? t('apps.loadError.paused') : '',
          t('common:ui.loadError.nothingChanged'),
        ].filter(Boolean).join(' ')}
      </LoadError>
    )
  }

  const view = listState({ apps: state.apps, shownCount: shown.length, query, isAdmin, canList })
  const firstRun = view === 'first-run'
  const showAttention = !firstRun && (attention.failed.length > 0 || attention.updates.length > 0)
  return (
    <div className="ad-apps">
      {state.stale && (
        <Callout tone="info" role="status" className="ad-callout">{t('apps.stale')}</Callout>
      )}
      {Object.entries(actions.state).filter(([key, r]) => key.startsWith('uninstall:') && r.outcome && r.outcome !== 'completed' && !actions.unknown[key.split(':')[1]]).map(([key, r]) => (
        <Callout key={key} tone="error" role="alert" className="ad-callout" testId="remove-error">{describeOutcome(r.outcome, t, r.error)}</Callout>
      ))}
      {Object.keys(actions.unknown).filter(id => actions.unknown[id].key.startsWith('uninstall:')).map(id => (
        <UnknownNotice key={id} addonId={id} actions={actions} className="ad-card__note" />
      ))}
      <div className="ad-split" data-open={detail ? 'true' : 'false'} data-view={detail ? paneTab : undefined}>
        <div className="ad-split__list">
          {showAttention && (
            <section className="ad-attention" aria-label={t('attention.label')} data-testid="addons-attention">
              {attention.failed.map(tile => {
                const added = addedByAddon(tile.app)
                const restarts = Number(installedInfo[tile.id]?.restarts) || 0
                return (
                  <Callout
                    key={tile.id}
                    tone="error"
                    title={t('attention.failedTitle', { name: titleOf(tile) })}
                    testId="attention-failed"
                    data-app={tile.id}
                    actions={<Button size="sm" variant="ghost" onClick={() => openDetail(tile.id)}>{t('card.details')}</Button>}
                  >
                    {restarts > 0 ? t('attention.failedRestarts', { count: restarts }) : t('attention.failedOnce')}{' '}
                    {added.tools > 0 ? t('attention.toolsOff', { count: added.tools }) : added.page ? t('attention.pageOff') : ''}
                  </Callout>
                )
              })}
              {attention.updates.map(({ tile, rec, version }) => {
                const working = Boolean(actions.unknown[tile.id]) || upgrade.working(tile.id)
                return (
                  <Callout
                    key={tile.id}
                    tone="warn"
                    icon="arrow-up"
                    title={t('attention.updateTitle', { name: titleOf(tile), version })}
                    testId="attention-update"
                    data-app={tile.id}
                    actions={(
                      <>
                        <Button size="sm" variant="ghost" onClick={() => openDetail(tile.id)}>{t('card.details')}</Button>
                        <Button size="sm" busy={working} disabled={working} onClick={() => upgrade.ask(rec)} aria-label={t('updates.upgradeNamed', { name: titleOf(tile) })}>{t('updates.upgrade')}</Button>
                      </>
                    )}
                  >
                    {t('attention.updateBody', { current: rec.version })}
                  </Callout>
                )
              })}
            </section>
          )}
          {!firstRun && (
            <div className="ad-toolbar">
              <SearchField value={query} onChange={setQuery} label={t('drawer.search')} testId="addon-search" />
              <Segmented
                options={STATE_FILTERS.map(id => ({ id, label: t(`filter.${id}`) }))}
                value={stateFilter}
                onChange={setStateFilter}
                label={t('filter.label')}
              />
              {isAdmin && installedList.length > 0 && (
                <Button size="sm" variant="ghost" icon="refresh" busy={checkingAll} disabled={checkingAll} onClick={checkAll} data-testid="addons-check-all">
                  {checkingAll ? t('updates.checkingAll') : t('updates.checkAll')}
                </Button>
              )}
              {isAdmin && <BulkUpgradeTrigger bulk={bulk} className="dk-btn dk-btn--sm" />}
            </div>
          )}
          {view === 'first-run' ? (
            <FirstRun adapter={adapter} actions={actions} />
          ) : view === 'no-match' ? (
            <EmptyState
              variant="no-results"
              title={t('apps.noMatchTitle')}
              body={t('apps.noMatchBody', { query: query.trim() })}
              actions={<Button size="sm" variant="ghost" onClick={() => setQuery('')}>{t('apps.clearSearch')}</Button>}
            />
          ) : view === 'none' ? (
            <EmptyState variant="unavailable" title={t('apps.emptyTitle')} body={t('apps.emptyBody')} />
          ) : rows.length === 0 ? (
            <EmptyState
              variant="no-results"
              title={t(`filter.none.${stateFilter}`)}
              actions={<Button size="sm" variant="ghost" onClick={() => setStateFilter('all')}>{t('filter.showAll')}</Button>}
            />
          ) : (
            <ul className="ad-tiles" aria-label={t('apps.listLabel')}>
              {rows.map(tile => {
                const rec = installedInfo[tile.id]
                const uv = isAdmin && rec ? updateView(rec.update) : null
                return (
                  <AddonRow
                    key={tile.id}
                    tile={tile}
                    pinned={pinnedIds.has(tile.id)}
                    selected={tile.id === detailId}
                    attention={tile.app ? attentionOf(tile.app, rec) : ''}
                    source={isAdmin ? rec : undefined}
                    updateVersion={uv && uv.state === 'update-available' ? uv.version : ''}
                    onPin={onPin}
                    onMove={nav.move}
                    onOpenDetail={openDetail}
                    onRemove={isAdmin ? setRemoving : undefined}
                    upgradeInline={Boolean(tile.app) && upgradeAvailability(rec, { isAdmin, pending: tile.app.update === 'update-pending' }).startable}
                    upgrade={tile.app ? (
                      <AddonUpgradeAction
                        addon={rec}
                        pending={tile.app.update === 'update-pending'}
                        isAdmin={isAdmin}
                        upgrade={upgrade}
                        variant="secondary"
                        working={Boolean(actions.unknown[tile.id]) || upgrade.working(tile.id)}
                      />
                    ) : null}
                  />
                )
              })}
            </ul>
          )}
        </div>
        {/* Below 1280 px the pane covers the page; the scrim closes it. */}
        {detail && <div className="ad-scrim" aria-hidden="true" onClick={closeDetail} />}
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
            upgrade={upgrade}
            hasSettings={detailHasSettings}
            view={paneTab}
            onView={setView}
          >
            {isAdmin && installedInfo[detail.id] && (
                <OperatorFacts
                  addon={installedInfo[detail.id]}
                  api={api}
                  language={i18n.language}
                  actions={actions}
                  check={checks[detail.id]}
                  upgraded={upgradedIds.has(detail.id)}
                  upgradeOp={upgrade.opOf(detail.id)}
                  onCheck={() => upgrade.check(installedInfo[detail.id])}
                  onUpgrade={() => upgrade.ask(installedInfo[detail.id])}
                />
            )}
          </AddonPreview>
        )}
      </div>
      {!legacy && <div className="ad-nav-slot"><AddonSlot slot="app.nav" context={null} /></div>}
      <ConfirmDialog
        open={Boolean(removing)}
        danger
        title={removing ? t('card.removeTitle', { name: titleOf(removing) }) : ''}
        message={removing ? <p>{t('card.removeBody', { name: removing.title })}</p> : null}
        confirmLabel={t('card.remove')}
        onConfirm={() => { const x = removing; setRemoving(null); closeDetail(); remove(x) }}
        onCancel={() => setRemoving(null)}
      />
      {manageId && state.status === 'ready' && !detail && (
        <Callout tone="info" role="status" className="ad-callout">{t('installed.missing', { id: manageId })}</Callout>
      )}
      <UpgradeConfirm upgrade={upgrade} />
      <BulkUpgradeDialog bulk={bulk} />
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}
