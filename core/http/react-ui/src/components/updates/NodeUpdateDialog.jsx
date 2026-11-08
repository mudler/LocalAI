/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button } from '../ui'
import { useNodeUpdateRun } from '../../hooks/useNodeUpdateRun'
import { planTotals, stopKind } from '../../utils/nodeUpdates'
import './node-updates.css'

// The confirmation of an update that touches nodes, and the run that follows.
// A worker stops the running processes of a backend before it installs the
// new one, so the dialog says which models stop on which node, and says so
// plainly when the registry names none: the record is best effort and an
// empty list does not prove that nothing stops.
//
// The same dialog is used from the per-node view, from the Nodes page and
// from the page of one node. It confirms first, then shows each node's result
// as it ends, one node after another.
export default function NodeUpdateDialog({ entries, onClose, onSettled, onStart }) {
  const { t } = useTranslation('updates')
  const nu = useNodeUpdateRun({ entries, onSettled })
  const { run, running, summary } = nu
  const finished = Boolean(run) && !running
  const phase = !run ? 'confirm' : running ? 'running' : 'finished'
  const totals = planTotals(entries)
  const kind = stopKind(totals)
  const multi = totals.backends > 1
  const rootRef = useRef(null)

  // The button that was pressed goes away: put the focus on the next control.
  useEffect(() => {
    if (phase === 'confirm') return
    const next = document.querySelector('[data-testid="node-update-dialog"] [data-nu-focus]')
    if (next) next.focus()
  }, [phase])

  const models = (n) => t('node.models', { count: n })
  const stopsWords = (e) => (e.models.length > 0
    ? t('node.stops', { models: e.models.join(', ') })
    : t('node.noneRecorded'))

  let title
  if (phase === 'confirm') {
    title = multi
      ? t('node.titleMany', { count: totals.backends, nodes: t('node.nodes', { count: totals.nodes }) })
      : t('node.title', { backend: entries[0] ? entries[0].backend : '', count: totals.nodes })
  } else if (running) title = t('node.runningTitle')
  else title = t('node.doneTitle')

  const intro = multi ? t('node.introMany') : t('node.intro', { backend: entries[0] ? entries[0].backend : '' })

  let stopLine
  if (kind === 'known') stopLine = t('node.stopKnown', { count: totals.models })
  else if (kind === 'partial') stopLine = t('node.stopPartial', { count: totals.models, unknown: totals.unknown })
  else stopLine = t('node.stopNone')

  let confirmLabel
  if (kind === 'known') confirmLabel = t('node.confirmStop', { count: totals.nodes, models: models(totals.models) })
  else if (kind === 'partial') confirmLabel = t('node.confirmStopAtLeast', { count: totals.nodes, models: models(totals.models) })
  else confirmLabel = t('node.confirmPlain', { count: totals.nodes })

  let foot
  if (phase === 'confirm') {
    foot = (
      <>
        <Button data-autofocus onClick={onClose} data-testid="node-update-cancel">{t('node.cancel')}</Button>
        <Button variant="primary" icon="arrow-up" onClick={() => { if (nu.start() && onStart) onStart() }} data-testid="node-update-confirm">{confirmLabel}</Button>
      </>
    )
  } else if (phase === 'running') {
    foot = (
      <Button variant="ghost" onClick={nu.stop} data-nu-focus data-testid="node-update-stop">{t('node.stop')}</Button>
    )
  } else {
    foot = (
      <>
        {nu.failed.length > 0 && (
          <Button icon="refresh" onClick={() => nu.retry()} data-testid="node-update-retry-failed">{t('bulk.retryFailed', { count: nu.failed.length })}</Button>
        )}
        <Button variant="primary" onClick={onClose} data-nu-focus data-testid="node-update-close">{t('dialog.close')}</Button>
      </>
    )
  }

  return (
    <Dialog
      title={title}
      description={phase === 'confirm' ? intro : undefined}
      onClose={onClose}
      closeLabel={t('dialog.close')}
      role="alertdialog"
      pending={running}
      testId="node-update-dialog"
      labelId="node-update-title"
      foot={foot}
    >
      <div ref={rootRef} className="nu-body">
        <ul className="nu-list" aria-label={t('node.listLabel')} data-testid="node-update-list">
          {(run ? run.items : nu.items).map((e) => {
            const st = run ? run.status[e.id] : null
            return (
              <li key={e.key} className="nu-item" data-testid="node-update-item" data-node={e.nodeId} data-phase={st ? st.phase : undefined}>
                <div className="nu-item__main">
                  <strong className="nu-item__name">{e.nodeName}</strong>
                  {multi && <span className="nu-item__backend">{e.backend}</span>}
                  <span className="nu-item__versions">{t('row.versions', { from: e.from || '?', to: e.to || '?' })}</span>
                </div>
                <div className="nu-item__side">
                  {st ? (
                    <span className="nu-item__state" data-phase={st.phase}>
                      <Icon name={st.phase === 'upgrading' ? 'spinner' : st.phase === 'done' ? 'check' : st.phase === 'failed' ? 'alert-circle' : 'clock'} spin={st.phase === 'upgrading'} aria-hidden="true" />
                      {t(`item.${st.phase}`)}
                    </span>
                  ) : (
                    <span className="nu-item__stops" data-known={e.models.length > 0 ? 'true' : 'false'} data-testid="node-update-stops">{stopsWords(e)}</span>
                  )}
                </div>
                {st && st.phase === 'failed' && (
                  <div className="nu-item__fail">
                    <span role="alert" data-testid="node-update-error">{st.message}</span>
                    {finished && <Button size="sm" icon="refresh" onClick={() => nu.retry([e.id])} aria-label={t('row.retryNamed', { name: e.nodeName })} data-testid="node-update-retry">{t('row.retry')}</Button>}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
        {phase === 'confirm' && <p className="nu-total" data-testid="node-update-total">{stopLine} {t('node.sequence')}</p>}
        {phase === 'running' && (
          <p className="nu-total" role="status" data-testid="node-update-progress">
            {t('bulk.running', { current: Math.min(summary.done + summary.failed + summary.skipped + 1, summary.total), total: summary.total })}. {t('node.runningNote')}
          </p>
        )}
        {phase === 'finished' && <p className="nu-total" role="status" data-testid="node-update-summary" data-tone={summary.failed > 0 ? 'error' : 'ok'}>{t('bulk.summary', summary)}</p>}
      </div>
    </Dialog>
  )
}
