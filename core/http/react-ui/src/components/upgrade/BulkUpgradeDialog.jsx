/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Dialog from '../Dialog'
import './upgrade.css'

// The "upgrade all" button and its dialog. They draw the state of
// useBulkUpgrade and know nothing about what is being upgraded: the page gives
// the items and the words. `labels` may replace: title, intro, trigger, action.
// `quiet` drops the idle arrow, for a trigger that follows text which already
// says "update"; the spinner still shows while a run goes.
export function BulkUpgradeTrigger({ bulk, labels = {}, className = 'dk-btn dk-btn--primary', quiet = false }) {
  const { t } = useTranslation('upgrade')
  const count = bulk.items.filter(i => i.startable).length
  const { run, running, summary } = bulk
  if (!run && count === 0) return null
  const finished = run && !running
  return (
    <span className="up-trigger">
      <button type="button" className={className} onClick={() => { if (finished && summary.failed === 0) bulk.reset(); bulk.show() }} data-testid="bulk-upgrade-open">
        {(running || !quiet) && <Icon name={running ? 'spinner' : 'arrow-up'} spin={running} aria-hidden="true" />}
        {running
          ? t('trigger.running', { current: Math.min(summary.done + summary.failed + summary.skipped + 1, summary.total), total: summary.total })
          : (labels.trigger || t('trigger.all', { count }))}
      </button>
      {finished && (
        <span className="up-trigger__note" role="status" data-testid="bulk-upgrade-summary" data-tone={summary.failed > 0 ? 'error' : 'ok'}>
          {summary.failed > 0 ? t('trigger.summary', summary) : t('trigger.summaryOk', summary)}
        </span>
      )}
    </span>
  )
}

export function BulkUpgradeDialog({ bulk, labels = {} }) {
  const { t } = useTranslation('upgrade')
  if (!bulk.open) return null
  const { run, running, summary, items, selected } = bulk
  const finished = Boolean(run) && !running
  const rows = run ? run.items : items
  const startableCount = items.filter(i => i.startable).length
  const L = (key, fallback) => labels[key] || fallback
  const foot = run
    ? (
      <>
        {finished && bulk.failed.length > 0 && (
          <button type="button" className="dk-btn dk-btn--secondary" onClick={bulk.retry} data-testid="bulk-upgrade-retry">
            <Icon name="refresh" aria-hidden="true" /> {t('dialog.retry', { count: bulk.failed.length })}
          </button>
        )}
        {finished && (
          <button type="button" className="dk-btn dk-btn--ghost" onClick={bulk.reset} data-testid="bulk-upgrade-again">{t('dialog.again')}</button>
        )}
        <button type="button" className="dk-btn dk-btn--primary" onClick={bulk.hide} data-testid="bulk-upgrade-close">
          {running ? t('dialog.closeRunning') : t('dialog.close')}
        </button>
      </>
    )
    : (
      <>
        <button type="button" className="dk-btn dk-btn--ghost" onClick={bulk.hide}>{t('dialog.close')}</button>
        <button type="button" className="dk-btn dk-btn--primary" disabled={selected.size === 0} onClick={bulk.start} data-testid="bulk-upgrade-start">
          <Icon name="arrow-up" aria-hidden="true" /> {L('action', t('dialog.start'))}
        </button>
      </>
    )
  return (
    <Dialog
      title={L('title', t('dialog.title'))}
      description={run ? undefined : L('intro', t('dialog.intro'))}
      onClose={bulk.hide}
      closeLabel={t('dialog.close')}
      testId="bulk-upgrade-dialog"
      labelId="bulk-upgrade-title"
      foot={foot}
    >
      {!run && (
        <div className="up-pick">
          <div className="up-pick__bar">
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={startableCount === 0} onClick={bulk.selectAll} data-testid="bulk-upgrade-all">{t('dialog.selectAll')}</button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={selected.size === 0} onClick={bulk.selectNone} data-testid="bulk-upgrade-none">{t('dialog.selectNone')}</button>
            <span className="up-pick__count" aria-live="polite">{t('dialog.selectedCount', { count: selected.size })}</span>
          </div>
        </div>
      )}
      {run && (
        <p className="up-progress" role="status" data-testid="bulk-upgrade-progress">
          {running
            ? t('dialog.progress', { current: Math.min(summary.done + summary.failed + summary.skipped + 1, summary.total), total: summary.total })
            : t('dialog.summary', summary)}
          {running && <span className="up-progress__note"> {t('dialog.runningNote')}</span>}
        </p>
      )}
      {rows.length === 0 && <p className="up-empty">{t('dialog.empty')}</p>}
      <ul className="up-list" aria-label={t('dialog.selection')}>
        {rows.map(item => {
          const st = run ? run.status[item.id] : null
          return (
            <li key={item.id} className="up-row" data-testid="bulk-upgrade-item" data-item={item.id} data-phase={st ? st.phase : undefined} data-startable={item.startable ? 'true' : 'false'}>
              {!run && (
                <input
                  className="dk-check" type="checkbox"
                  checked={selected.has(item.id)}
                  disabled={!item.startable}
                  onChange={() => bulk.toggle(item.id)}
                  aria-label={t('dialog.item', { name: item.name })}
                />
              )}
              <div className="up-row__main">
                <span className="up-row__name">{item.name}</span>
                <span className="up-row__versions">{t('dialog.versions', { from: item.from || '?', to: item.to || '?' })}</span>
                {!run && !item.startable && item.reason && <span className="up-row__why" data-testid="bulk-upgrade-why">{t('dialog.blocked')}: {item.reason}</span>}
                {st && st.phase === 'failed' && st.message && <span className="up-row__why" role="alert" data-testid="bulk-upgrade-error">{st.message}</span>}
              </div>
              {st && (
                <span className="up-row__state" data-phase={st.phase}>
                  <Icon name={st.phase === 'upgrading' ? 'spinner' : st.phase === 'done' ? 'check' : st.phase === 'failed' ? 'alert-circle' : 'clock'} spin={st.phase === 'upgrading'} aria-hidden="true" />
                  {t(`item.${st.phase}`)}
                </span>
              )}
            </li>
          )
        })}
      </ul>
    </Dialog>
  )
}
