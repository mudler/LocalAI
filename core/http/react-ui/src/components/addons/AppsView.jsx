/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, LoadError, Notice, PaneLayout, SearchField, Segmented, SkeletonRows } from '../ui'
import { pollDelay } from '../catalog/catalogState'
import { RegistryContext } from '../../addons/registryContext'
import { navigationItems } from '../../addons/registryState'
import { hasAddonSettings } from '../../addons/hostControls'
import { SETTINGS_VIEW, paneView } from '../../utils/addonSettings'
import { useAuth } from '../../context/AuthContext'
import { FEATURE_ADDONS } from '../../utils/capabilities'
import SideSheet from '../SideSheet'
import AddonPreview from './AddonPreview'
import AddonSlot from '../../addons/AddonSlot'
import { useHubAuth } from '../hub/useHubAuth'
import AddonRow from './AddonRow'
import { apiUrl } from '../../utils/basePath'
import ConfirmDialog from '../ConfirmDialog'
import { useNavigation } from '../../context/NavigationContext'
import { buildTiles, filterTiles, resolvePins } from '../../utils/addonTiles'
import { useAddonConnections } from '../../hooks/useAddonConnections'
import { rowState } from '../../utils/chatConnectors'
import { AppIcon, UnknownNotice } from './AddonBits'
import { OperatorFacts } from './OperatorFacts'
import { AddonUpgradeAction, UpgradeConfirm } from './UpgradeControls'
import { useAddonUpgrade } from './useAddonUpgrade'
import { updateView, upgradeAvailability } from './updateState'
import FirstRun from './FirstRun'
import { attentionOf, filterByState, groupTiles, listState, loadLastKnown, localizeRecord, saveLastKnown, sortByAttention, staleKind, STATE_FILTERS, upgradingTitles } from './addonState'
import { describeError, describeOutcome } from './errors'
import ApproveUpgradeDialog, { WaitingBanner } from './ApproveUpgradeDialog'
import { pendingApprovals } from './hostGrantsState'
import { useAddonActions } from './useAddonActions'

const NO_RECORDS = {}

// The apps every caller with the add-ons feature may use. The owner decides
// what is listed; this view only draws it. The list is refreshed on a bounded
// cadence, slower in a hidden tab and backing off while the owner is
// unreachable. Start and stop are offered to administrators and the server
// refuses everyone else whatever this view shows.
// The tab keeps the last list it got for this person. A page loaded while the
// owner is busy has nothing in memory, and an empty page would read as "no
// add-ons"; the stored copy stands in for the list, marked stale.
const browserStore = () => { try { return globalThis.sessionStorage || null } catch { return null } }

export default function AppsView({ api, adapter, onSummary, grants }) {
  const { t, i18n } = useTranslation('addons')
  const [storedState, setState] = useState({ status: 'loading', apps: [], error: null, stale: false })
  const [reload, setReload] = useState(0)
  const reloadRef = useRef(() => {})
  const { hasFeature, isAdmin, user, sessionEpoch } = useAuth()
  // Without the feature there is no list to read and the route would answer 403.
  const canList = hasFeature(FEATURE_ADDONS)
  // The add-on registry does not read the list again while this tab is open:
  // every list read here is handed to it.
  const registry = useContext(RegistryContext)
  const actor = JSON.stringify([user?.id, sessionEpoch, isAdmin, canList])
  const scope = useRef(actor)
  scope.current = actor
  const state = storedState.actor === actor ? storedState : { status: 'loading', apps: [], error: null, stale: false }
  const ingest = registry?.ingest

  // Poll with a timer chain: one request at a time, aborted on unmount. A
  // refresh asked for while one is in flight runs right after it, so there is
  // never more than one chain.
  useEffect(() => {
    if (!canList) { setState({ actor, status: 'ready', apps: [], error: null, stale: false }); return undefined }
    const current = () => scope.current === actor
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
        const read = await api.apps.read(ctl.signal)
        const apps = read.apps
        failures = 0
        if (alive && current()) {
          setState({ actor, status: 'ready', apps, error: null, stale: false })
          ingest?.(apps)
          const store = browserStore()
          if (store) saveLastKnown(store, actor, apps)
        }
      } catch (error) {
        if (!alive || !current() || (error && error.name === 'AbortError')) return
        failures += 1
        // Keep what was on screen and say it may be out of date. A page that
        // has nothing yet takes the copy this tab stored, so a refresh during
        // an upgrade does not show an empty page.
        const store = browserStore()
        const remembered = store ? loadLastKnown(store, actor) : null
        setState(s => {
          const kept = s.actor === actor && s.status === 'ready' ? s.apps : remembered
          return { actor, status: kept ? 'ready' : 'error', apps: kept || [], error, stale: true }
        })
      } finally {
        inflight = false
      }
      if (!alive || !current()) return
      const wait = queued ? 0 : pollDelay(failures, typeof document !== 'undefined' && document.hidden)
      queued = false
      clearTimeout(timer)
      timer = setTimeout(tick, wait)
    }
    reloadRef.current = () => { if (inflight) { queued = true } else { clearTimeout(timer); tick() } }
    tick()
    return () => { alive = false; ctl.abort(); clearTimeout(timer) }
  }, [api, reload, canList, actor, ingest])

  // The installed record of an add-on, for the administrator's Details in the
  // pane. A change reads it again.
  const [installedRecords, setInstalledInfo] = useState({})
  const installedRaw = installedRecords.actor === actor ? installedRecords.value : NO_RECORDS
  // The records name an add-on in dialogs and cards: in the user's language.
  const installedInfo = useMemo(() => (installedRaw === NO_RECORDS ? NO_RECORDS : Object.fromEntries(Object.entries(installedRaw).map(([id, rec]) => [id, localizeRecord(rec, i18n.language)]))), [installedRaw, i18n.language])
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
  const [approving, setApproving] = useState(null)
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
  const installedList = useMemo(() => Object.values(installedInfo), [installedInfo])

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
  // The messaging connections, for the groups, the setup strip and the details.
  const conns = useAddonConnections({ enabled: canList && hasFeature('chats') })

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
    const current = () => scope.current === actor && !ctl.signal.aborted
    api.installed(ctl.signal)
      .then(list => { if (current()) setInstalledInfo({ actor, value: Object.fromEntries(list.map(a => [a.id, a])) }) })
      .catch(() => { /* the pane says the source is not known */ })
    return () => ctl.abort()
  }, [api, isAdmin, canList, appsKey, installedReload, actor])

  const firstRunNow = state.status === 'ready' && !state.stale && listState({ apps: state.apps, shownCount: tiles.length, query: '', isAdmin, canList }) === 'first-run'
  useEffect(() => {
    if (!onSummary) return
    onSummary(state.status === 'ready' ? { count: tiles.length, firstRun: firstRunNow } : null)
  }, [onSummary, state.status, tiles.length, firstRunNow])

  // What needs a person, for the one strip over the list: an add-on that failed
  // and an add-on with a connection that cannot answer yet (no token, or its
  // add-on does not run). An update is not setup: it is the action of its row.
  const attention = useMemo(() => {
    const failed = []
    const setup = []
    for (const tile of tiles) {
      if (!tile.app) continue
      if (attentionOf(tile.app, installedInfo[tile.id]) === 'failed') { failed.push(tile); continue }
      const broken = (conns.byAddon[tile.id] || []).filter(c => rowState(c) === 'attention')
      if (broken.length > 0) setup.push({ tile, connections: broken })
    }
    return { failed, setup }
  }, [tiles, installedInfo, conns.byAddon])
  const setupIds = useMemo(() => new Set(attention.setup.map(x => x.tile.id)), [attention.setup])

  const onPin = async (id) => {
    if (pinnedIds.has(id)) { await nav.unpin(id); return }
    const result = await nav.pin(id)
    if (result.needsUnpin) setLimit(true)
  }
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); out.delete('manage'); out.delete('view'); return out })
  const closeDetail = () => setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); out.delete('manage'); out.delete('view'); return out }, { replace: true })
  // The Settings tab of the details, for an administrator, when the add-on
  // declares a settings section (or has server settings of its own feature).
  const detailHasSettings = Boolean(isAdmin && detail && detail.app && hasAddonSettings(detail.app))
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
  // An error, or a stale copy of an empty list, is never the empty state: the
  // page only says "no add-ons" when the owner answered with an empty list.
  if (state.status === 'error' || (state.stale && state.apps.length === 0)) {
    const loadError = state.error || { kind: 'busy' }
    const kind = loadError.kind
    return (
      <LoadError
        title={t('apps.loadError.title')}
        testId="addons-load-error"
        nothingChanged={false}
        onRetry={() => { setState({ status: 'loading', apps: [], error: null, stale: false }); setReload(n => n + 1) }}
      >
        {[
          describeError(loadError, t),
          kind !== 'auth' && kind !== 'denied' && kind !== 'invalid' ? t('apps.loadError.paused') : '',
          t('common:ui.loadError.nothingChanged'),
        ].filter(Boolean).join(' ')}
      </LoadError>
    )
  }

  const view = listState({ apps: state.apps, shownCount: shown.length, query, isAdmin, canList })
  const firstRun = view === 'first-run'
  // Upgrades held for host access: the old version runs on, and the
  // administrator approves what the new one asks for. The plain Upgrade of
  // such an add-on would be refused, so only the banner offers it.
  const approvals = isAdmin && grants && grants.data ? pendingApprovals(grants.data) : []
  const waitingIds = new Set(approvals.map(a => a.id))
  const showAttention = !firstRun && (attention.failed.length > 0 || attention.setup.length > 0 || approvals.length > 0)
  const needCount = attention.failed.length + attention.setup.length
  return (
    <div className="ad-apps">
      {state.stale && <StaleNotice state={state} onRetry={() => reloadRef.current()} />}
      {Object.entries(actions.state).filter(([key, r]) => key.startsWith('uninstall:') && r.outcome && r.outcome !== 'completed' && !actions.unknown[key.split(':')[1]]).map(([key, r]) => (
        <Callout key={key} tone="error" role="alert" className="ad-callout" testId="remove-error">{describeOutcome(r.outcome, t, r.error)}</Callout>
      ))}
      {Object.keys(actions.unknown).filter(id => actions.unknown[id].key.startsWith('uninstall:')).map(id => (
        <UnknownNotice key={id} addonId={id} actions={actions} className="ad-card__note" />
      ))}
      <PaneLayout className="ad-split">
        <div className="ad-split__list">
          {showAttention && (
            <section className="ad-attention" aria-label={t('attention.label')} data-testid="addons-attention">
              {needCount > 0 && (
                <Callout tone={attention.setup.length === 0 ? 'error' : 'warn'} title={t('attention.title', { count: needCount })} testId="attention-setup">
                  <ul className="ad-strip">
                    {attention.failed.map(tile => {
                      const restarts = Number(installedInfo[tile.id]?.restarts) || 0
                      return (
                        <li key={tile.id} className="ad-strip__row" data-testid="attention-failed" data-app={tile.id}>
                          <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} id={tile.id} name={titleOf(tile)} size={32} />
                          <span className="ad-strip__text">
                            <b>{titleOf(tile)}</b> {restarts > 0 ? t('attention.failedRestarts', { count: restarts }) : t('attention.failedOnce')}
                          </span>
                          <Button size="sm" onClick={() => openDetail(tile.id)} aria-label={t('card.reviewNamed', { name: titleOf(tile) })}>{t('card.review')}</Button>
                        </li>
                      )
                    })}
                    {attention.setup.map(({ tile, connections }) => (
                      <li key={tile.id} className="ad-strip__row" data-testid="attention-connection" data-app={tile.id}>
                        <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} id={tile.id} name={titleOf(tile)} size={32} />
                        <span className="ad-strip__text">
                          <b>{titleOf(tile)}</b> {t('attention.connectionBody', { count: connections.length, reason: connections[0].reason || '' })}
                        </span>
                        <Button size="sm" onClick={() => openDetail(tile.id)} aria-label={t('card.setUpNamed', { name: titleOf(tile) })}>{t('card.setUp')}</Button>
                      </li>
                    ))}
                  </ul>
                </Callout>
              )}
              {approvals.map(a => <WaitingBanner key={a.id} addon={a} busy={Boolean(actions.unknown[a.id])} onReview={() => setApproving(a)} />)}
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
                <Button size="sm" variant="ghost" icon="arrow-up" to="/app/updates" data-testid="addons-open-updates">{t('updates.open')}</Button>
              )}
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
            <div className="ad-groups" aria-label={t('apps.listLabel')}>
              {groupTiles(rows, { connectorAddons: conns.addonIds }).map(group => (
                <section key={group.id} className="ad-group" data-testid="addon-group" data-group={group.id} aria-labelledby={`ad-group-${group.id}`}>
                  <h2 className="ad-group__title" id={`ad-group-${group.id}`}>{t(`groups.${group.id}`)}</h2>
                  <ul className="ad-tiles">
                    {group.tiles.map(tile => {
                      const rec = installedInfo[tile.id]
                      const uv = isAdmin && rec ? updateView(rec.update) : null
                      return (
                        <AddonRow
                          key={tile.id}
                          tile={tile}
                          pinned={pinnedIds.has(tile.id)}
                          selected={tile.id === detailId}
                          attention={tile.app ? attentionOf(tile.app, rec) : ''}
                          needsSetup={setupIds.has(tile.id)}
                          updateVersion={uv && uv.state === 'update-available' ? uv.version : ''}
                          waitingApproval={waitingIds.has(tile.id)}
                          sourceId={rec ? rec.sourceId : ''}
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
                              working={Boolean(actions.unknown[tile.id]) || upgrade.working(tile.id) || Boolean(tile.app.operation)}
                            />
                          ) : null}
                        />
                      )
                    })}
                  </ul>
                </section>
              ))}
            </div>
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
            connections={detail.app ? (conns.byAddon[detail.id] || []) : []}
            agents={conns.agents}
            kinds={conns.kinds}
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
      </PaneLayout>
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
      {approving && (
        <ApproveUpgradeDialog
          addon={approving}
          sourceId={installedInfo[approving.id]?.sourceId}
          api={api}
          actions={actions}
          onClose={() => setApproving(null)}
          onDone={() => { setApproving(null); grants?.reload(); reloadRef.current() }}
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

// The banner over a list that may be out of date. It says why, names the
// add-ons the owner is upgrading when the last list knew of it, and offers a
// retry now; the page also asks again by itself, slower after each failure.
function StaleNotice({ state, onRetry }) {
  const { t } = useTranslation('addons')
  const kind = staleKind(state)
  const names = upgradingTitles(state.apps)
  const title = kind === 'unreachable'
    ? t('apps.busy.unreachable')
    : names.length > 0 ? t('apps.busy.upgrading', { names: names.join(', ') }) : t('apps.busy.generic')
  return (
    <Notice
      severity="warn"
      title={title}
      reason={t('apps.busy.reason')}
      fix={{ label: t('apps.busy.retry'), onClick: onRetry, testId: 'addons-stale-retry' }}
      className="ad-callout"
      testId="addons-stale"
      data-kind={kind}
    />
  )
}
