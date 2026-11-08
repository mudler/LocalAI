/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, EmptyState, LoadError, Segmented, SkeletonRows } from '../components/ui'
import PageHeader from '../components/PageHeader'
import UpdateRow from '../components/updates/UpdateRow'
import UpdateStrip, { UpdateSources } from '../components/updates/UpdateStrip'
import { BulkUpgradeDialog } from '../components/upgrade/BulkUpgradeDialog'
import NodeUpdateDialog from '../components/updates/NodeUpdateDialog'
import { UpgradeConfirm } from '../components/addons/UpgradeControls'
import ApproveUpgradeDialog from '../components/addons/ApproveUpgradeDialog'
import Icon from '../components/Icon'
import { useUpdates, CHECK_COOLDOWN_MS } from '../contexts/UpdatesContext'
import { useUpdateActions } from '../hooks/useUpdateActions'
import { backendsApi } from '../utils/api'
import { isClusterRow, nodeView, splitUpdate } from '../utils/nodeUpdates'
import { addonsApi } from '../utils/addonsClient'
import {
  breakdownOf, currentBackendRows, filterCounts, filterRows, groupRows, headlineOf, normalizeFilter,
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
// library backends and add-ons, with the freshness of every source. The data
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
  const [params, setParams] = useSearchParams()
  const filter = normalizeFilter(params.get('filter'))
  const [installedBackends, setInstalledBackends] = useState(null)
  const [showCurrent, setShowCurrent] = useState(false)
  const [now, setNow] = useState(Date.now())
  const [nodePlan, setNodePlan] = useState(null)
  const narrow = useNarrow()
  const actions = useUpdateActions({ updates })
  const { bulk } = actions

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
    () => [...actions.rows, ...currentBackendRows(installedBackends || [], updates.snapshot)],
    [actions.rows, installedBackends, updates.snapshot],
  )
  const groups = useMemo(() => groupRows(filterRows(rows, filter)), [rows, filter])
  const counts = useMemo(() => filterCounts(rows), [rows])
  const breakdown = useMemo(() => breakdownOf(rows), [rows])
  const headline = headlineOf({ rows, sources: updates.sources, loading: updates.loading, loadError: updates.loadError })
  const lastChecked = updates.snapshot ? updates.snapshot.checkedAt : ''

  // A backend that nodes report is updated node by node, after a confirmation
  // that names the models that stop. Every path that would start such an
  // update (a row's Update, its Retry, the Update ready button) opens the one
  // dialog; nothing starts until it is confirmed. The rows that need no
  // confirmation (single machine, library, add-on) start when it is.
  const { update: updateRow, retry: retryRow } = actions
  const todoOf = useCallback((row) => {
    const item = updates.snapshot && (updates.snapshot.items || []).find(i => i.id === row.id)
    return item ? nodeView(item).todo : []
  }, [updates.snapshot])
  const onUpdate = useCallback((row) => {
    if (isClusterRow(row)) {
      const todo = todoOf(row)
      if (todo.length > 0) setNodePlan({ entries: todo, restIds: new Set() })
      return
    }
    updateRow(row)
  }, [todoOf, updateRow])
  const onRetry = useCallback((row) => {
    if (isClusterRow(row)) onUpdate(row)
    else retryRow(row)
  }, [onUpdate, retryRow])
  const { bulk: bulkRun } = actions
  const onBulkStart = useCallback(() => {
    const { entries, restIds } = splitUpdate({ rows: actions.rows, selected: bulkRun.selected, items: updates.snapshot ? updates.snapshot.items : [] })
    if (entries.length === 0) { bulkRun.start(restIds); return }
    setNodePlan({ entries, restIds })
  }, [actions.rows, bulkRun, updates.snapshot])

  const setFilter = useCallback((next) => {
    setParams((prev) => {
      const out = new URLSearchParams(prev)
      if (next === 'all') out.delete('filter')
      else out.set('filter', next)
      return out
    }, { replace: true })
  }, [setParams])

  const checkNow = useCallback(() => { updates.checkNow() }, [updates])

  // One line under the sentence: the run in progress or its summary, else the
  // answer of the last check, else the result of the last single update. It is
  // the one live region of the page.
  const { summary, running } = bulk
  let result = ''
  if (running) result = t('bulk.running', { current: Math.min(summary.done + summary.failed + summary.skipped + 1, summary.total), total: summary.total })
  else if (bulk.run) result = t('bulk.summary', summary)
  else if (actions.announcement) result = actions.announcement
  else if (updates.lastCheck && !updates.checking) {
    const failed = updates.sources.filter(s => s.state === 'failed').length
    const found = rows.filter(r => r.state !== 'current').length
    result = [
      t('check.result', { count: found }),
      failed > 0 ? t('check.failedSources', { count: failed }) : '',
      updates.lastCheck.throttled ? t('check.throttled') : '',
    ].filter(Boolean).join(' ')
  }

  const startable = bulk.items.filter(i => i.startable).length
  const primary = (running || bulk.selected.size > 0) ? (
    <span className="uc-primary">
      <Button
        variant="primary"
        icon="arrow-up"
        busy={running}
        disabled={running || bulk.selected.size === 0}
        onClick={onBulkStart}
        data-testid="updates-update-ready"
      >
        {running
          ? t('bulk.running', { current: Math.min(summary.done + summary.failed + summary.skipped + 1, summary.total), total: summary.total })
          : t('bulk.start', { count: bulk.selected.size })}
      </Button>
    </span>
  ) : null

  const bulkTail = bulk.run ? (
    <div className="uc-bulk" data-testid="updates-bulk">
      <Button size="sm" variant="ghost" onClick={bulk.show}>{t('bulk.details')}</Button>
      {!running && bulk.failed.length > 0 && (
        <Button size="sm" icon="refresh" onClick={bulk.retry} data-testid="updates-retry-failed">{t('bulk.retryFailed', { count: bulk.failed.length })}</Button>
      )}
      {!running && <Button size="sm" variant="ghost" onClick={bulk.reset}>{t('bulk.dismiss')}</Button>}
    </div>
  ) : null

  const filterOptions = ['all', 'ready', 'needs-you', 'updating', 'current'].map(id => ({ id, label: t(`filter.${id}`), count: counts[id] }))

  const renderRow = (row) => (
    <UpdateRow
      key={row.id}
      row={row}
      selected={bulk.selected.has(row.id)}
      onToggle={bulk.toggle}
      onUpdate={onUpdate}
      onRetry={onRetry}
      onReview={(r) => actions.setReview(r.grant)}
      onCancel={actions.cancel}
      onCheck={actions.check}
      checking={actions.checking(row)}
      locked={actions.busy}
    />
  )

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
    const nothingInstalled = installedBackends != null && installedBackends.length === 0 && updates.addons.installed.length === 0 && rows.length === 0
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
    const shown = groups.needs.length + groups.updating.length + groups.ready.length
    return (
      <>
        {rows.length > 0 && (
          <Segmented options={filterOptions} value={filter} onChange={setFilter} label={t('filter.label')} testId="updates-filter" itemTestId="updates-filter-" />
        )}
        {rows.length > 0 && shown === 0 && groups.current.length === 0 && (
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
            <div className="uc-group__head">
              <h2 className="uc-group__title" id="uc-g-ready">{t('group.ready')} <span className="uc-group__count">{groups.ready.length}</span></h2>
              {startable > 0 && (
                <span className="uc-group__acts">
                  <Button size="sm" variant="ghost" disabled={actions.busy} onClick={bulk.selectAll} data-testid="updates-select-all">{t('group.selectAll')}</Button>
                  <Button size="sm" variant="ghost" disabled={actions.busy || bulk.selected.size === 0} onClick={bulk.selectNone}>{t('group.selectNone')}</Button>
                </span>
              )}
            </div>
            <ul className="uc-list">{groups.ready.map(renderRow)}</ul>
          </section>
        )}
        {groups.current.length > 0 && (
          <section className="uc-group uc-current" aria-labelledby="uc-g-current" data-group="current">
            <div className="uc-current__line">
              <Icon name="check" />
              <h2 className="uc-current__text" id="uc-g-current">
                <strong>{t('group.current', { count: groups.current.length })}</strong>
                {!(showCurrent || filter === 'current') && <span className="uc-current__names">: {groups.current.slice(0, 4).map(r => r.name).join(', ')}{groups.current.length > 4 ? '\u2026' : ''}</span>}
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
      {bulkTail}
      {!updates.loading && !updates.loadError && (
        <UpdateSources
          sources={updates.sources}
          auto={updates.autoUpdate}
          onCheck={checkNow}
          checking={updates.checking}
          installsDisabled={Boolean(updates.snapshot && updates.snapshot.installsDisabled)}
        />
      )}
      {body()}
      {narrow && primary && <div className="uc-sticky" data-testid="updates-sticky">{primary}</div>}
      {nodePlan && (
        <NodeUpdateDialog
          entries={nodePlan.entries}
          onClose={() => setNodePlan(null)}
          onStart={() => { if (nodePlan.restIds.size > 0) bulkRun.start(nodePlan.restIds) }}
        />
      )}
      <UpgradeConfirm upgrade={actions.confirmApi} />
      {actions.review && (
        <ApproveUpgradeDialog
          addon={actions.review}
          api={addonsApi}
          actions={actions.addonActions}
          onClose={() => actions.setReview(null)}
          onDone={() => { actions.setReview(null); updates.readAddons() }}
        />
      )}
      <BulkUpgradeDialog bulk={bulk} />
    </div>
  )
}
