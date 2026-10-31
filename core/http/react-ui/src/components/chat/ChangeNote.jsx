import { useRef, useState } from 'react'
import { addonChange, toolRefusal } from '../../utils/addonChange'
import { addonsApi } from '../../utils/addonsClient'
import { runUndo, undoMessageKey } from '../../utils/addonUndo'
import { newOperationId } from '../catalog/catalogState'

// What a change tool of an add-on did, under its tool entry. The add-on's switch
// for the chat is the consent, so a change is made at once and there is no
// Confirm: the line says what was done and whether it can be undone.
export default function ChangeNote({ text, t }) {
  const c = addonChange(text)
  if (!c) return <RefusalNote text={text} t={t} />
  if (c.kind === 'proposed') return <div className="cx-change">{t('tool.waiting', { addon: c.addonId })}</div>
  return (
    <div className="cx-change">
      {c.count > 0 ? t('tool.applied', { addon: c.addonId, count: c.count }) : t('tool.appliedNone', { addon: c.addonId })}
      {' '}
      {c.undoable
        ? <UndoChange change={c} t={t} />
        : (c.reason ? t('tool.notUndoableWhy', { reason: c.reason }) : t('tool.notUndoable'))}
    </div>
  )
}

// The Undo button of a change the add-on kept a change set for. It is the
// person's own action: a model has no way to press it. The operation ID is kept
// for the life of the card, so a repeat after an unknown outcome is the same
// undo and never a second one. After use the card says what happened, and a
// refusal (the data moved on, the record expired) says why and changes nothing.
// eslint-disable-next-line no-unused-vars -- used as JSX above
function UndoChange({ change, t }) {
  const [state, setState] = useState({ kind: 'idle' })
  const op = useRef('')
  const press = async () => {
    if (!op.current) op.current = newOperationId()
    setState({ kind: 'busy' })
    const out = await runUndo({ api: addonsApi, addonId: change.addonId, resourceId: change.resourceId, changeId: change.changeId, operationId: op.current })
    // A refusal is final: the card stops offering Undo. An unknown outcome and an
    // outage keep the operation ID, so the next press repeats the same undo.
    if (out.kind !== 'unknown' && out.kind !== 'unavailable') op.current = ''
    setState(out)
  }
  if (state.kind === 'busy') return <span role="status">{t('tool.undoing')}</span>
  if (state.kind === 'done' || state.kind === 'already_undone') {
    return <span role="status" data-testid="undo-result">{t(undoMessageKey(state.kind), { summary: change.undo })}</span>
  }
  const retry = state.kind === 'unknown' || state.kind === 'unavailable'
  return (
    <>
      {state.kind !== 'idle' && (
        <span role="status" data-testid="undo-result">{t(undoMessageKey(state.kind), { detail: state.message || '' })}{retry ? ' ' : ''}</span>
      )}
      {(state.kind === 'idle' || retry) && (
        <button type="button" className="cx-link" data-testid="undo-change" title={change.undo || undefined} onClick={press}>
          {t('tool.undo')}
        </button>
      )}
    </>
  )
}

// A tool call the add-on refused with a typed code: what it means for the
// person, in plain words, and the add-on's own reason when it gave one.
// eslint-disable-next-line no-unused-vars -- used as JSX above
function RefusalNote({ text, t }) {
  const r = toolRefusal(text)
  if (!r) return null
  return (
    <div className="cx-change cx-change--refused" role="note" data-testid="tool-refusal" data-code={r.code}>
      {t(`tool.refused.${r.code}`, { addon: r.addonId || t('tool.refused.anAddon') })}
      {r.message && r.code !== 'invalid_request' ? ` ${t('tool.refused.said', { message: r.message })}` : ''}
      {r.code === 'invalid_request' ? ` ${r.message}` : ''}
    </div>
  )
}
