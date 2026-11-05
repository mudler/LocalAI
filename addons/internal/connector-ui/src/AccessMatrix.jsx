/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Who can do what": the table of capabilities by kind of sender, drawn from
// the host's check run on the form as it is now. It decides nothing and has no
// controls that change the form: a cell is an icon and a word, never colour
// alone, and a cell that says No or Off opens the host's message, the one fix
// and the checks that were not made, under the table.
import { useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { FixNotice, Select } from './controls'
import { FixButton } from './CapabilitiesPanel'
import { remainingChecks, sentenceCase } from './access'
import { MATRIX_COLUMNS, matrixColumns, matrixRows, shortReason } from './matrix'

const ICON = { yes: 'check-circle', asks: 'help-circle', no: 'close', waiting: 'clock', off: 'minus', unknown: 'minus' }
const SELECTABLE = new Set(['no', 'off'])

function Cell({ row, col, cell, s, picked, onPick, label }) {
  const { state, decision } = cell
  const word = s(`matrix.state.${state}`)
  const why = SELECTABLE.has(state) && decision ? shortReason(decision.message || decision.reason) : ''
  const word_ = <span className="cn-mx__word"><Icon name={ICON[state] || 'minus'} /><span>{word}</span></span>
  const id = `connector-matrix-cell-${row.key}-${col.id}`
  return (
    <td className={`cn-mx__cell cn-mx__cell--${state}`} data-col={col.id} data-state={state}>
      {SELECTABLE.has(state) && decision
        ? <button type="button" className="cn-mx__btn" data-testid={id} data-state={state} aria-pressed={picked} onClick={onPick}>{word_}</button>
        : <span className="cn-mx__static" data-testid={id} data-state={state}>{word_}</span>}
      {why && <span className="cn-mx__why" title={decision.message || decision.reason}>{why}</span>}
    </td>
  )
}

export default function AccessMatrix({ form, kindInfo, capabilities, preview, dirty, s, ctx }) {
  const view = preview.view
  const columns = matrixColumns(form, view)
  const rows = matrixRows(view, capabilities, form, columns)
  const [picked, setPicked] = useState(null) // { row, col }
  const kinds = columns.filter((c) => c.id !== 'on')
  const [mobile, setMobile] = useState('')
  const shown = kinds.some((c) => c.id === mobile) ? mobile : kinds[0]?.id || ''
  const loading = preview.status === 'loading'
  const colLabel = (id) => s(`matrix.col.${id}`)
  const rowTitle = (r) => (r.parent ? s('matrix.toolRow', { addon: r.addon, tool: r.tool }) : r.cap === 'tools.write' ? s('matrix.row.tools') : sentenceCase(r.title))
  const sel = picked && rows.find((r) => r.key === picked.row)?.cells[picked.col]
  const selRow = picked && rows.find((r) => r.key === picked.row)
  const d = sel?.decision
  return (
    <section className="cn-mx" data-testid="connector-matrix" aria-busy={loading ? 'true' : undefined} aria-labelledby="cn-mx-title">
      <h3 id="cn-mx-title" className="cn-sub__title">{s('matrix.title')}</h3>
      <p className="dk-hint" data-testid="connector-matrix-note">{s('matrix.note')}</p>
      {dirty && <p className="cn-muted">{s(form.id ? 'can.fromForm' : 'can.fromFormNew')}</p>}
      <p className="cn-muted cn-can__live" role="status" aria-live="polite">{loading ? s(view ? 'can.stale' : 'can.loading') : ''}</p>
      {preview.status === 'error' && (
        <FixNotice severity="error" title={s('can.errorTitle')} reason={preview.error} testId="connector-matrix-error"
          fix={{ label: s('can.retry'), onClick: preview.reload, testId: 'connector-matrix-retry' }} />
      )}
      {preview.status !== 'error' && view && (<>
        <div className="dk-field cn-mx__pick">
          <label className="dk-label" htmlFor="cn-mx-sender">{s('matrix.pick')}</label>
          <Select id="cn-mx-sender" data-testid="connector-matrix-sender" value={shown} onChange={(e) => setMobile(e.target.value)}>
            {kinds.map((c) => <option key={c.id} value={c.id}>{colLabel(c.id)}</option>)}
          </Select>
        </div>
        <div className="cn-matrix__wrap">
          <table className="cn-matrix cn-mx__table" data-sel={shown} aria-label={s('matrix.title')} data-dim={loading ? 'true' : undefined}>
            <thead>
              <tr>
                <th scope="col"><span className="cn-vh">{s('matrix.capability')}</span></th>
                {columns.map((c) => <th key={c.id} scope="col" data-col={c.id}>{colLabel(c.id)}</th>)}
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} className={r.parent ? 'cn-mx__sub' : undefined} data-testid={`connector-matrix-row-${r.key}`}>
                  <th scope="row"><span className="cn-mx__title">{rowTitle(r)}</span></th>
                  {columns.map((c) => (
                    <Cell key={c.id} row={r} col={c} cell={r.cells[c.id]} s={s} label={`${rowTitle(r)}, ${colLabel(c.id)}`}
                      picked={picked?.row === r.key && picked?.col === c.id} onPick={() => setPicked(picked?.row === r.key && picked?.col === c.id ? null : { row: r.key, col: c.id })} />
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="cn-mx__detail" data-testid="connector-matrix-detail" role="region" aria-live="polite" aria-label={s('matrix.detail.label')}>
          {!d ? <p className="cn-muted">{s('matrix.detail.none')}</p> : (<>
            <p className="cn-mx__detail-title">{s('matrix.detail.title', { capability: rowTitle(selRow), who: colLabel(picked.col) })}</p>
            <p data-testid="connector-matrix-message">{d.message || d.reason || s('can.noMessage')}</p>
            {remainingChecks(d.then_check).length > 0 && (
              <p className="cn-muted" data-testid="connector-matrix-then">{s('can.then', { list: remainingChecks(d.then_check).map((x) => s(`can.then.${x}`)).join(', ') })}</p>
            )}
            {d.fix && <div className="cn-can__fix"><FixButton fix={d.fix} ctx={ctx} s={s} /></div>}
          </>)}
        </div>
      </>)}
    </section>
  )
}
