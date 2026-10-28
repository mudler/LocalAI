/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from './SideSheet'
import Icon from './Icon'
import { initialSelection, isSelectable, selectableKeys, selectedItems, summarize, toggleAll, toggleKey, upgradableItems } from '../utils/updateReview'
import './UpdateReviewSheet.css'

// The review step both upgrade lists share (add-ons and backends). It lists
// what is pending as installed version -> candidate version and runs nothing
// until the administrator presses a button in it.
//
//   items        [{ key, name, from, to, blocked: string[] }]; an item with
//                `blocked` reasons is listed but cannot be selected
//   preselect    keys checked when the sheet opens
//   run          async (items, onProgress) => results; the caller decides how
//                one item is upgraded (see runBatch). It must not throw.
//   onSettled    (results, items) after a run, for the caller's toasts
//   started      the run only starts the work (a backend upgrade runs as a job
//                in Activity), so a done row says "Started" and not "Upgraded"
//
// The list is frozen when the sheet opens, so a row that was just upgraded does
// not vanish under the reader when the page behind it reloads.
export default function UpdateReviewSheet({ title, description, items: initialItems, preselect = [], run, onSettled, onClose, started = false, testId = 'update-review' }) {
  const { t } = useTranslation('common')
  const [items] = useState(initialItems)
  const [selection, setSelection] = useState(() => initialSelection(items, preselect))
  const [status, setStatus] = useState({})
  const [running, setRunning] = useState(false)
  const [results, setResults] = useState(null)
  const alive = useRef(true)
  // Rows that were upgraded are done; a failed one can be tried again.
  const done = useMemo(() => new Set(Object.keys(status).filter(k => status[k].state === 'done')), [status])
  const open = useMemo(() => items.filter(i => !done.has(i.key)), [items, done])
  const selectableCount = selectableKeys(open).length

  const start = useCallback(async (chosen) => {
    if (chosen.length === 0 || running) return
    setRunning(true)
    setResults(null)
    setStatus(s => {
      const next = { ...s }
      for (const item of chosen) next[item.key] = { state: 'queued' }
      return next
    })
    const out = await run(chosen, (key, state, message) => {
      if (alive.current) setStatus(s => ({ ...s, [key]: { state, message: message || s[key]?.message || '' } }))
    })
    if (!alive.current) return
    // The final results carry the message of each failure.
    setStatus(s => {
      const next = { ...s }
      for (const r of out) next[r.key] = { state: r.ok ? 'done' : 'failed', message: r.message || '' }
      return next
    })
    setSelection(sel => {
      const next = new Set(sel)
      for (const r of out) if (r.ok) next.delete(r.key)
      return next
    })
    setResults(out)
    setRunning(false)
    if (onSettled) onSettled(out, chosen)
  }, [run, running, onSettled])

  const chosen = selectedItems(open, selection)
  const everything = upgradableItems(open)
  const summary = results ? summarize(results) : null
  const allChecked = selectableCount > 0 && selectableKeys(open).every(k => selection.has(k))

  return (
    <SideSheet
      title={title || t('updateReview.title')}
      description={description || t('updateReview.description')}
      onClose={() => { alive.current = false; onClose() }}
      closeLabel={t('updateReview.close')}
      testId={testId}
      labelId="update-review-title"
      wide
      footer={(
        <>
          <button type="button" className="dk-btn dk-btn--ghost" onClick={() => { alive.current = false; onClose() }} data-testid="update-review-close">
            {results ? t('updateReview.done') : t('updateReview.close')}
          </button>
          <button type="button" className="dk-btn" disabled={running || chosen.length === 0} onClick={() => start(chosen)} data-testid="update-review-selected">
            {t('updateReview.upgradeSelected', { count: chosen.length })}
          </button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={running || everything.length === 0} onClick={() => start(everything)} data-testid="update-review-all">
            <Icon name={running ? 'spinner' : 'arrow-up'} spin={running} /> {running ? t('updateReview.running') : t('updateReview.upgradeAll', { count: everything.length })}
          </button>
        </>
      )}
    >
      {items.length === 0 ? (
        <p className="ur-empty">{t('updateReview.empty')}</p>
      ) : (
        <>
          <label className="ur-all">
            <input type="checkbox" checked={allChecked} disabled={running || selectableCount === 0} onChange={() => setSelection(sel => toggleAll(sel, open))} data-testid="update-review-select-all" />
            <span>{t('updateReview.selectAll')}</span>
          </label>
          <ul className="ur-list" aria-label={title || t('updateReview.title')}>
            {items.map(item => {
              const st = status[item.key]
              const blocked = !isSelectable(item)
              const finished = st && st.state === 'done'
              return (
                <li key={item.key} className="ur-row" data-testid="update-review-row" data-key={item.key} data-blocked={blocked ? 'true' : undefined} data-state={st ? st.state : 'idle'}>
                  <input
                    type="checkbox"
                    className="ur-row__check"
                    aria-label={t('updateReview.select', { name: item.name })}
                    checked={selection.has(item.key)}
                    disabled={running || blocked || finished}
                    onChange={() => setSelection(sel => toggleKey(sel, items, item.key))}
                  />
                  <div className="ur-row__main">
                    <span className="ur-row__name">{item.name}</span>
                    <span className="ur-row__versions" data-testid="update-review-versions">
                      <span className="ur-ver">{item.from || t('updateReview.unknownVersion')}</span>
                      <Icon name="arrow-right" aria-hidden="true" />
                      <span className="ur-ver ur-ver--to">{item.to || t('updateReview.latest')}</span>
                    </span>
                    {item.detail && <span className="ur-row__detail" data-testid="update-review-detail">{item.detail}</span>}
                    {blocked && (
                      <span className="ur-row__blocked" data-testid="update-review-blocked">
                        {t('updateReview.blocked')}{' '}{item.blocked.join('; ')}
                      </span>
                    )}
                    {st && st.state === 'failed' && st.message && <span className="ur-row__error" role="alert">{st.message}</span>}
                  </div>
                  {st && (
                    <span className="ur-row__state" data-state={st.state}>
                      {st.state === 'queued' && t('updateReview.queued')}
                      {st.state === 'running' && <><Icon name="spinner" spin aria-hidden="true" /> {t('updateReview.statusRunning')}</>}
                      {st.state === 'done' && <><Icon name="check" aria-hidden="true" /> {t(started ? 'updateReview.statusStarted' : 'updateReview.statusDone')}</>}
                      {st.state === 'failed' && <><Icon name="alert-circle" aria-hidden="true" /> {t('updateReview.statusFailed')}</>}
                    </span>
                  )}
                </li>
              )
            })}
          </ul>
          {summary && (
            <p className="ur-summary" role="status" data-testid="update-review-summary" data-failed={summary.failed > 0 ? 'true' : undefined}>
              {t(started ? 'updateReview.summaryStarted' : 'updateReview.summary', { succeeded: summary.succeeded, failed: summary.failed })}
            </p>
          )}
        </>
      )}
    </SideSheet>
  )
}
