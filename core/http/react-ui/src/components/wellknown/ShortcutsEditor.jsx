/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { StoredIn } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { useSwapMotion } from '../../hooks/useSwapMotion'
import { shortcutList, cleanShortcuts, move } from '../../utils/wellKnown'
import './wellKnown.css'

export function defaultStarters(t) {
  const list = t('empty.suggestionsChat', { returnObjects: true })
  return Array.isArray(list) ? list : []
}

// ShortcutsEditor edits shortcuts.json: add, rename, reorder and delete. With
// no file it starts from the default starters, so a first save keeps them.
// Used in the popover of the shortcuts row and on the Account page.
export default function ShortcutsEditor({ inline = false, onDone }) {
  const { t } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  const wk = useWellKnown('shortcuts')
  const [rows, setRows] = useState(null)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  // The version the rows came from: a save starts from it, so a change by
  // someone else while editing is a conflict and not lost.
  const baseVersion = useRef(0)

  useEffect(() => {
    if (!wk.doc || dirty) return
    baseVersion.current = wk.doc.version ?? 0
    setRows(shortcutList(wk.doc, defaultStarters(tc)).items)
  }, [wk.doc, dirty, tc])

  const edit = (next) => { setRows(next); setDirty(true); setProblem(null) }
  const { listRef, swap } = useSwapMotion([rows])
  const moveRow = (i, j) => { swap(i, j); edit(move(rows, i, j)) }
  const setRow = (i, k, v) => edit(rows.map((r, j) => (j === i ? { ...r, [k]: v } : r)))

  const save = async () => {
    setBusy(true)
    try {
      await wk.save({ shortcuts: cleanShortcuts(rows) }, { version: baseVersion.current })
      setDirty(false)
      onDone?.()
    } catch (e) {
      if (e.conflict) { setProblem({ message: t('wellKnown.conflict', { file: 'shortcuts.json' }), errors: [], conflict: true }) } else setProblem({ message: e.message, errors: e.errors || [] })
    } finally { setBusy(false) }
  }
  const reload = async () => { setDirty(false); setProblem(null); await wk.reload() }

  if (!rows) return <div className="wk-loading"><LoadingSpinner size="sm" /></div>

  return (
    <div className={inline ? 'wk-form' : 'wk-pop'} data-testid="wk-shortcuts-editor">
      {!inline && <h3>{t('wellKnown.shortcuts.edit')}</h3>}
      {inline && <StoredIn file="shortcuts.json" onRevert={async () => { await wk.revert(); setDirty(false) }} canRevert={!!wk.doc?.exists} busy={busy} />}
      {!inline && <StoredIn file="shortcuts.json" />}
      <ol className="wk-rows" ref={listRef}>
        {rows.length === 0 && <li className="wk-row--empty">{t('wellKnown.shortcuts.none')}</li>}
        {rows.map((r, i) => (
          <li key={i} className="wk-row" data-testid="wk-shortcut-row">
            <div className="wk-row__fields">
              <input className="dk-input" aria-label={t('wellKnown.shortcuts.label')} placeholder={t('wellKnown.shortcuts.label')} value={r.label} maxLength={40} onChange={e => setRow(i, 'label', e.target.value)} />
              <textarea className="dk-input" aria-label={t('wellKnown.shortcuts.prompt')} placeholder={t('wellKnown.shortcuts.prompt')} value={r.prompt} maxLength={2000} onChange={e => setRow(i, 'prompt', e.target.value)} />
              <label className="dk-choice">
                <input type="checkbox" className="dk-check" checked={!!r.send} onChange={e => setRow(i, 'send', e.target.checked)} />
                {t('wellKnown.shortcuts.send')}
              </label>
            </div>
            <div className="wk-row__tools">
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveUp')} disabled={i === 0} onClick={() => moveRow(i, i - 1)}><Icon name="arrow-up" /></button>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveDown')} disabled={i === rows.length - 1} onClick={() => moveRow(i, i + 1)}><Icon name="arrow-down" /></button>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.remove')} onClick={() => edit(rows.filter((_, j) => j !== i))}><Icon name="trash" /></button>
            </div>
          </li>
        ))}
      </ol>
      {problem && (
        <div className="wk-note wk-note--error" role="alert">
          <Icon name="alert-circle" />
          <div>
            <p>{problem.message}</p>
            {problem.errors.length > 0 && <ul>{problem.errors.map(e => <li key={e}><code>{e}</code></li>)}</ul>}
            {problem.conflict && <div className="wk-actions"><button type="button" className="dk-btn dk-btn--sm dk-btn--secondary" onClick={reload}>{t('wellKnown.useTheirs')}</button></div>}
          </div>
        </div>
      )}
      <div className="wk-actions">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={rows.length >= 24} onClick={() => edit([...rows, { label: '', prompt: '', icon: '', send: false }])} data-testid="wk-shortcut-add">
          <Icon name="plus" /> {t('wellKnown.shortcuts.add')}
        </button>
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={!dirty || busy} onClick={save} data-testid="wk-shortcut-save">
          {busy ? <LoadingSpinner size="sm" /> : <Icon name="save" />} {t('wellKnown.save')}
        </button>
        {!inline && onDone && <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onDone}>{t('wellKnown.cancel')}</button>}
      </div>
    </div>
  )
}
