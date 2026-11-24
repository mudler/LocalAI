/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, LoadError, Segmented, SkeletonRows } from '../components/ui'
import PageHeader from '../components/PageHeader'
import UpdateRow from '../components/updates/UpdateRow'
import UpdateStrip, { RunStrip, UpdateSources } from '../components/updates/UpdateStrip'
import ReviewDialog from '../components/updates/ReviewDialog'
import RunItemRow from '../components/updates/RunItemRow'
import { ModelActionDialogs } from '../components/updates/ModelUpgradeDialog'
import { useModelUpgradeActions } from '../hooks/useModelUpgradeActions'
import ApproveUpgradeDialog from '../components/addons/ApproveUpgradeDialog'
import Icon from '../components/Icon'
import { useUpdates, CHECK_COOLDOWN_MS } from '../contexts/UpdatesContext'
import { useUpdateActions } from '../hooks/useUpdateActions'
import { useUpgradeRun } from '../hooks/useUpgradeRun'
import { backendsApi } from '../utils/api'
import { isRunning, runKeys } from '../utils/upgradeRun'
import { upgradeFailureWords } from '../components/addons/updateReview'
import {
  breakdownOf, currentBackendRows, filterCountsWithRun, filterRows, filterRunItems, groupRows, headlineOf, isPending, normalizeFilter,
} from '../utils/updates'
import '../components/updates/updates.css'

const NARROW = '(max-width: 639px)'

function useNarrow() {
  const read = () => (typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(NARROW).matches : false)
  const [narrow, setNarrow] = useState(read)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined
    const mq = window.matchMedia(NARROW)
    const onChange = () => setNarrow(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return narrow
}

// Updates: what is out of date and what is changing, for gallery backends,
// library backends, add-ons and gallery models, with the freshness of every source. The data
// is the shell-level UpdatesProvider's; this page adds the installed backends
// (for the "up to date" line), the filter and the actions.
export default function Updates() {
  const updates = useUpdates()
  if (!updates || !updates.enabled) return <UpdatesUnavailable />
  return <UpdatesPage updates={updates} />
}

function UpdatesUnavailable() {
  const { t } = useTranslation('updates')
  return (
    <div className="page page--wide op-page uc-page" data-testid="updates-page">
      <PageHeader title={t('title')} supporting={t('supporting')} />
      <EmptyState variant="unavailable" title={t('adminOnly.title')} body={t('adminOnly.body')} />
    </div>
  )
}

function UpdatesPage({ updates }) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  const [params, setParams] = useSearchParams()
  const filter = normalizeFilter(params.get('filter'))
  const runParam = params.get('run') || ''
  const [installedBackends, setInstalledBackends] = useState(null)
  const [showCurrent, setShowCurrent] = useState(false)
  const [now, setNow] = useState(Date.now())
  const narrow = useNarrow()
  const actions = useUpdateActions({ updates })
  const outlet = useOutletContext()
  // A model row's Upgrade or Retry may start a run of one on the server: look
  // for it at once (the hook is made below, hence the ref).
  const runRefresh = useRef(null)
  const model = useModelUpgradeActions({
    readModels: updates.readModels, addToast: outlet && outlet.addToast, onStarted: () => { if (runRefresh.current) runRefresh.current() },
  })
  const { refresh } = updates
  // The run is the server's. When it ends the lists change: read them again.
  const upgrade = useUpgradeRun({ runId: runParam, onEnded: () => refresh() })
  runRefresh.current = upgrade.refresh
  const shownRun = upgrade.run
  const runActive = isRunning(shownRun)

  // The installed backends, for the ones that are known to be current. Read
  // again when a check answers.
  const checkedAt = updates.snapshot ? updates.snapshot.checkedAt : ''
  useEffect(() => {
    let alive = true
    backendsApi.listInstalled()
      .then((list) => { if (alive) setInstalledBackends(Array.isArray(list) ? list : (list && Array.isArray(list.backends) ? list.backends : [])) })
      .catch(() => { if (alive) setInstalledBackends([]) })
    return () => { alive = false }
  }, [checkedAt])

  // The button is disabled for a short time after a check, as the server
  // answers a check within that time from the last one.
  const lastCheckAt = updates.lastCheck ? updates.lastCheck.at : 0
  useEffect(() => {
    if (!lastCheckAt) return undefined
    const left = CHECK_COOLDOWN_MS - (Date.now() - lastCheckAt)
    setNow(Date.now())
    if (left <= 0) return undefined
    const timer = setTimeout(() => setNow(Date.now()), left + 50)
    return () => clearTimeout(timer)
  }, [lastCheckAt])
  const cooling = Boolean(lastCheckAt) && now - lastCheckAt < CHECK_COOLDOWN_MS

  const rows = useMemo(
    () => [...updates.rows, ...currentBackendRows(installedBackends || [], updates.snapshot)],
    [updates.rows, installedBackends, updates.snapshot],
  )
  const rowsByKey = useMemo(() => new Map(rows.map(r => [r.id, r])), [rows])
  // The names the page showed, so an item that left the lists (it was updated)
  // keeps the name the person knows (an add-on's title, not its id).
  const names = useRef(new Map())
  for (const r of rows) names.current.set(r.id, r.name)

  // An item of the shown run is listed once: in the run group.
  const inRun = useMemo(() => runKeys(shownRun), [shownRun])
  const listRows = useMemo(() => (inRun.size > 0 ? rows.filter(r => !inRun.has(r.id)) : rows), [rows, inRun])
  const groups = useMemo(() => groupRows(filterRows(listRows, filter)), [listRows, filter])
  // The items of the run are listed once, in the run group, and counted with
  // the filter they match there.
  const counts = useMemo(() => filterCountsWithRun(listRows, shownRun && shownRun.items, runActive), [listRows, shownRun, runActive])
  const breakdown = useMemo(() => breakdownOf(rows), [rows])
  const headline = headlineOf({ rows, sources: updates.sources, loading: updates.loading, loadError: updates.loadError })
  const lastChecked = updates.snapshot ? updates.snapshot.checkedAt : ''

  // Every update starts from a review: "Update N ready" reviews every ready
  // row (models included, unselected by default), a row's Update or Retry
  // reviews that row alone. A backend that nodes report is in the same review:
  // the server names the models that stop. Per-node control stays on the
  // row's Nodes page.
  const readyRows = useMemo(() => listRows.filter(r => r.state === 'ready'), [listRows])
  const readyCount = readyRows.filter(r => r.startable && r.kind !== 'model').length
  const { openReview } = upgrade
  const onUpdate = useCallback((row) => { openReview([row], { single: true }) }, [openReview])
  const onRetry = useCallback((row) => {
    // The approval of a held upgrade is spent when it is accepted: a new try is a new review.
    if (row.approval && row.grant) actions.setReview(row.grant)
    else openReview([row], { single: true })
  }, [actions, openReview])
  const onReviewAll = useCallback(() => { openReview(readyRows) }, [openReview, readyRows])
  const onStart = useCallback(async () => {
    const started = await upgrade.start()
    if (started && started.id) {
      setParams((prev) => {
        const out = new URLSearchParams(prev)
        out.set('run', started.id)
        return out
      }, { replace: true })
    }
  }, [upgrade, setParams])
  // A retry is a new run (the old one stays finished): follow it.
  const onRetryRun = useCallback(async () => {
    const next = await upgrade.retry()
    if (next && next.id) {
      setParams((prev) => {
        const out = new URLSearchParams(prev)
        out.set('run', next.id)
        return out
      }, { replace: true })
    }
  }, [upgrade, setParams])
  // The review found a run in progress: close it and show that run.
  const onFollow = useCallback((id) => {
    upgrade.closeReview()
    setParams((prev) => {
      const out = new URLSearchParams(prev)
      out.set('run', id)
      return out
    }, { replace: true })
  }, [upgrade, setParams])
  const onDismiss = useCallback(async () => {
    await upgrade.dismiss()
    setParams((prev) => {
      const out = new URLSearchParams(prev)
      out.delete('run')
      return out
    }, { replace: true })
  }, [upgrade, setParams])

  const setFilter = useCallback((next) => {
    setParams((prev) => {
      const out = new URLSearchParams(prev)
      if (next === 'all') out.delete('filter')
      else out.set('filter', next)
      return out
    }, { replace: true })
  }, [setParams])

  const checkNow = useCallback(() => { updates.checkNow() }, [updates])

  // One line under the sentence: a failed action on the run, else the answer
  // of the last check, else the result of the last model action. It is the one
  // live region of the page.
  let result = ''
  if (upgrade.error || upgrade.errorCode) result = t('run.actionFailed', { message: upgrade.errorCode ? upgradeFailureWords(upgrade.errorCode, ta, upgrade.error, 'run') : upgrade.error })
  else if (model.announcement) result = model.announcement
  else if (updates.lastCheck && !updates.checking) {
    const failed = updates.sources.filter(s => s.state === 'failed').length
    const found = rows.filter(isPending).length
    result = [
      t('check.result', { count: found }),
      failed > 0 ? t('check.failedSources', { count: failed }) : '',
      updates.lastCheck.throttled ? t('check.throttled') : '',
    ].filter(Boolean).join(' ')
  }

  // A server that keeps no update runs: nothing can start from this page.
  const noRuns = upgrade.unavailable
  const primary = (!shownRun && readyRows.length > 0) ? (
    <span className="uc-primary">
      <Button
        variant="primary"
        icon="arrow-up"
        disabled={upgrade.review != null || noRuns}
        title={noRuns ? t('run.unavailable') : undefined}
        onClick={onReviewAll}
        data-testid="updates-update-ready"
      >
        {readyCount > 0 ? t('bulk.start', { count: readyCount }) : t('bulk.review')}
      </Button>
    </span>
  ) : null

  const filterOptions = ['all', 'ready', 'needs-you', 'updating', 'current'].map(id => ({ id, label: t(`filter.${id}`), count: counts[id] }))

  const renderRow = (row) => (
    <UpdateRow
      key={row.id}
      row={row}
      onUpdate={onUpdate}
      onRetry={onRetry}
      onReview={(r) => actions.setReview(r.grant)}
      onCancel={actions.cancel}
      onCheck={actions.check}
      checking={actions.checking(row)}
      locked={runActive || noRuns}
      lockedReason={runActive ? t('run.busy') : noRuns ? t('run.unavailable') : ''}
      model={model.actions}
    />
  )

  // `rows` is a dependency: the names arrive with the lists, which may answer
  // after the run.
  const runItems = useMemo(() => (shownRun && Array.isArray(shownRun.items) ? shownRun.items : [])
    .map(i => ({ ...i, displayName: (rowsByKey.get(i.key) || {}).name || names.current.get(i.key) || i.title || i.name })), [shownRun, rowsByKey])
  // The run group follows the filter too; the queued reasons still read the
  // whole run.
  const shownRunItems = useMemo(() => filterRunItems(runItems, filter, runActive), [runItems, filter, runActive])

  const body = () => {
    if (updates.loading) return <SkeletonRows count={4} label={t('loading')} testId="updates-loading" />
    if (updates.loadError) {
      return (
        <LoadError
          title={t('loadError.title')}
          error={updates.backend.error}
          testId="updates-load-error"
          onRetry={() => updates.refresh()}
        />
      )
    }
    const nothingInstalled = installedBackends != null && installedBackends.length === 0 && updates.addons.installed.length === 0 && rows.length === 0 && runItems.length === 0
    if (nothingInstalled && updates.sources.every(s => s.state !== 'failed')) {
      return (
        <EmptyState
          title={t('empty.title')}
          body={t('empty.body')}
          actions={(
            <>
              <Button to="/app/backends">{t('empty.backends')}</Button>
              <Button to="/app/addons">{t('empty.addons')}</Button>
            </>
          )}
        />
      )
    }
    const shown = groups.needs.length + groups.updating.length + groups.ready.length + groups.other.length
    return (
      <>
        {(listRows.length > 0 || runItems.length > 0) && (
          <Segmented options={filterOptions} value={filter} onChange={setFilter} label={t('filter.label')} testId="updates-filter" itemTestId="updates-filter-" />
        )}
        {shownRunItems.length > 0 && (
          <section className="uc-group" aria-labelledby="uc-g-run" data-group="run" data-testid="updates-run">
            <h2 className="uc-group__title" id="uc-g-run">{t('run.group')} <span className="uc-group__count">{shownRunItems.length}</span></h2>
            <ul className="uc-list">
              {shownRunItems.map(item => (
                <RunItemRow
                  key={item.key}
                  item={item}
                  row={rowsByKey.get(item.key) || null}
                  items={runItems}
                  runActive={runActive}
                  busy={upgrade.busy}
                  onCancel={upgrade.cancelItem}
                  onRetry={onRetryRun}
                />
              ))}
            </ul>
          </section>
        )}
        {(listRows.length > 0 || runItems.length > 0) && shown === 0 && groups.current.length === 0 && shownRunItems.length === 0 && (
          <p className="uc-none" data-testid="updates-filter-empty">{t('filter.none')}</p>
        )}
        {groups.needs.length > 0 && (
          <section className="uc-group" aria-labelledby="uc-g-needs" data-group="needs">
            <h2 className="uc-group__title" id="uc-g-needs">{t('group.needs')} <span className="uc-group__count">{groups.needs.length}</span></h2>
            <ul className="uc-list">{groups.needs.map(renderRow)}</ul>
          </section>
        )}
        {groups.updating.length > 0 && (
          <section className="uc-group" aria-labelledby="uc-g-updating" data-group="updating">
            <h2 className="uc-group__title" id="uc-g-updating">{t('group.updating')} <span className="uc-group__count">{groups.updating.length}</span></h2>
            <ul className="uc-list">{groups.updating.map(renderRow)}</ul>
          </section>
        )}
        {groups.ready.length > 0 && (
          <section className="uc-group" aria-labelledby="uc-g-ready" data-group="ready">
            <h2 className="uc-group__title" id="uc-g-ready">{t('group.ready')} <span className="uc-group__count">{groups.ready.length}</span></h2>
            <ul className="uc-list">{groups.ready.map(renderRow)}</ul>
          </section>
        )}
        {groups.other.length > 0 && (
          <section className="uc-group" aria-labelledby="uc-g-other" data-group="other">
            <h2 className="uc-group__title" id="uc-g-other">{t('group.other')} <span className="uc-group__count">{groups.other.length}</span></h2>
            <ul className="uc-list">{groups.other.map(renderRow)}</ul>
          </section>
        )}
        {groups.current.length > 0 && (
          <section className="uc-group uc-current" aria-labelledby="uc-g-current" data-group="current">
            <div className="uc-current__line">
              <Icon name="check" />
              <h2 className="uc-current__text" id="uc-g-current">
                <strong>{t('group.current', { count: groups.current.length })}</strong>
                {!(showCurrent || filter === 'current') && <span className="uc-current__names">: {groups.current.slice(0, 4).map(r => r.name).join(', ')}{groups.current.length > 4 ? '…' : ''}</span>}
              </h2>
              {filter !== 'current' && (
                <Button size="sm" variant="ghost" aria-expanded={showCurrent} onClick={() => setShowCurrent(v => !v)} data-testid="updates-current-toggle">
                  {showCurrent ? t('group.hide') : t('group.show')}
                </Button>
              )}
            </div>
            {(showCurrent || filter === 'current') && <ul className="uc-list">{groups.current.map(renderRow)}</ul>}
          </section>
        )}
      </>
    )
  }

  const stripPrimary = narrow ? null : primary
  return (
    <div className="page page--wide op-page uc-page" data-testid="updates-page" data-narrow={narrow || undefined}>
      <PageHeader title={t('title')} supporting={t('supporting')} />
      {shownRun ? (
        <RunStrip
          run={shownRun}
          busy={upgrade.busy}
          result={result}
          onCheck={checkNow}
          checking={updates.checking}
          cooling={cooling}
          onCancel={upgrade.cancel}
          onRetry={onRetryRun}
          onDismiss={onDismiss}
        />
      ) : (
        <UpdateStrip
          headline={headline}
          breakdown={breakdown}
          lastChecked={lastChecked}
          checking={updates.checking}
          cooling={cooling}
          result={result}
          onCheck={checkNow}
          primary={stripPrimary}
        />
      )}
      {!updates.loading && !updates.loadError && (
        <UpdateSources
          sources={updates.sources}
          auto={updates.autoUpdate}
          onCheck={checkNow}
          checking={updates.checking}
          installsDisabled={Boolean(updates.snapshot && updates.snapshot.installsDisabled)}
        />
      )}
      {noRuns && <Callout tone="warn" testId="updates-runs-unavailable">{t('run.unavailable')}</Callout>}
      {body()}
      {narrow && primary && <div className="uc-sticky" data-testid="updates-sticky">{primary}</div>}
      {actions.review && (
        <ApproveUpgradeDialog
          addon={actions.review}
          upgrade={actions.addonUpgrade}
          onClose={() => actions.setReview(null)}
          onDone={(done) => { actions.setReview(cur => (cur && cur.id === done.id ? null : cur)); updates.readAddons(); upgrade.refresh() }}
        />
      )}
      <ReviewDialog
        review={upgrade.review}
        rowsByKey={rowsByKey}
        busy={upgrade.busy}
        onToggle={upgrade.toggle}
        onSelectAll={upgrade.selectAll}
        onSelectNone={upgrade.selectNone}
        onStart={onStart}
        onFollow={onFollow}
        onClose={upgrade.closeReview}
      />
      <ModelActionDialogs model={model} />
    </div>
  )
}
