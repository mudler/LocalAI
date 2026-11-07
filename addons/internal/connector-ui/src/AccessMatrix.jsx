/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Who can do what": the one place where the owner sets what a bot may do.
// Each row is a capability. The column "On for this bot" is the ceiling (caps)
// and, for a kind that declares roles, each role column is a checkbox (grants).
// Every other cell is worked out by the host from the form as it is now; the
// page decides nothing there. A cell is an icon and a word, never colour
// alone, and a cell opens the host's message, the one fix and the checks that
// were not made, under the table. A row opens a disclosure with what the
// capability does and, for the ones that ask first, the choices for that.
import { useEffect, useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { FixNotice, Hint, Select, Switch } from './controls'
import { FixButton } from './accessHooks'
import { hasPairing, remainingChecks, sentenceCase } from './access'
import { agentHas, agentHeld, grantableFor, roleHas, setCap, setGrant } from './connector'
import { matrixColumns, matrixRows } from './matrix'
import ApprovalOptions, { approvalEffect } from './ApprovalFields'

const ICON = { yes: 'check-circle', asks: 'help-circle', no: 'close', waiting: 'clock', off: 'minus', unknown: 'minus' }

// Why a cell is allowed, in the words of the page.
function viaText(d, s) {
  if (!d?.allowed || !d.via) return ''
  if (d.via.startsWith('role:')) return s('can.via.role', { role: d.via.slice(5) })
  if (['paired', 'allowlist', 'channel'].includes(d.via)) return s(`can.via.${d.via}`)
  if (d.via === 'agent-connection') return s('can.via.agentConnection')
  return ''
}

function Word({ state, s }) {
  return <span className="cn-mx__word"><Icon name={ICON[state] || 'minus'} /><span>{s(`matrix.state.${state}`)}</span></span>
}

// A cell the host worked out. With a decision it opens the detail.
function Cell({ row, col, cell, s, picked, onPick, children, describedBy, active }) {
  const { state, decision } = cell
  const id = `connector-matrix-cell-${row.key}-${col.id}`
  return (
    <td className={`cn-mx__cell cn-mx__cell--${state}`} data-col={col.id} data-state={state} data-active={active ? 'true' : undefined}>
      {children}
      {decision
        ? <button type="button" className="cn-mx__btn" data-testid={id} data-state={state} aria-pressed={picked} aria-describedby={describedBy} onClick={onPick}><Word state={state} s={s} /></button>
        : <span className="cn-mx__static" data-testid={id} data-state={state}><Word state={state} s={s} /></span>}
    </td>
  )
}

// Whether the screen is as narrow as the phone layout of the table (one kind of
// sender at a time). A row that spans the table must span only the columns that show.
const PHONE = '(max-width: 640px)'
function usePhone() {
  const [phone, setPhone] = useState(() => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(PHONE).matches : false))
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return undefined
    const q = window.matchMedia(PHONE)
    const on = () => setPhone(q.matches)
    on()
    q.addEventListener?.('change', on)
    return () => q.removeEventListener?.('change', on)
  }, [])
  return phone
}

export default function AccessMatrix({ form, setForm, kindInfo, capabilities, preview, dirty, s, ctx, agent, paired = [], onReset }) {
  const view = preview.view
  const phone = usePhone()
  const columns = matrixColumns(form, view, kindInfo?.roles)
  const rows = matrixRows(view, capabilities, form, columns)
  const [picked, setPicked] = useState(null) // { row, col }
  const [open, setOpen] = useState(() => new Set())
  const kinds = columns.filter((c) => c.id !== 'on')
  const [mobile, setMobile] = useState('')
  const shown = kinds.some((c) => c.id === mobile) ? mobile : kinds[0]?.id || ''
  const loading = preview.status === 'loading'
  const held = agentHeld(agent)
  const grantable = grantableFor(kindInfo, form.caps)
  const grantableKind = (cap) => (kindInfo?.permissions || []).some((p) => p.name === cap && !p.sensitive)
  const colLabel = (c) => (c.role ? s('can.sender.role', { title: c.title }) : s(`matrix.col.${c.id}`))
  const rowTitle = (r) => (r.parent ? s('matrix.toolRow', { addon: r.addon, tool: r.tool }) : r.cap === 'tools.write' ? s('matrix.row.tools') : sentenceCase(r.title))
  const capOf = (name) => capabilities.find((c) => c.name === name)
  const selRow = picked && rows.find((r) => r.key === picked.row)
  const sel = selRow?.cells[picked.col]
  const d = sel?.decision
  const toggle = (key) => setOpen((o) => { const n = new Set(o); if (n.has(key)) n.delete(key); else n.add(key); return n })
  const verifiedCol = columns.find((c) => c.id === 'paired')
  const nobodyVerified = Boolean(verifiedCol) && !hasPairing(paired)
  return (
    <section className="cn-mx" data-testid="connector-matrix" aria-busy={loading ? 'true' : undefined} aria-labelledby="cn-mx-title">
      <h3 id="cn-mx-title" className="cn-sub__title">{s('matrix.title')}</h3>
      <p className="dk-hint" data-testid="connector-matrix-note">{s('matrix.note')}</p>
      <p className="dk-hint" data-testid="connector-rule">{s('perm.rule')}</p>
      {nobodyVerified && <p className="cn-muted" data-testid="connector-matrix-nobody-verified">{s('matrix.nobodyVerified')}</p>}
      <div className="cn-mx__tools">
        <button type="button" className="cn-link cn-link--text" data-testid="connector-usual" onClick={onReset}>{s('matrix.reset')}</button>
      </div>
      {dirty && <p className="cn-muted" data-testid="connector-can-dirty">{s(form.id ? 'can.fromForm' : 'can.fromFormNew')}</p>}
      <p className="cn-muted cn-can__live" role="status" aria-live="polite" data-testid="connector-can-status">{loading ? s(view ? 'can.stale' : 'can.loading') : ''}</p>
      {preview.status === 'error' && (
        <FixNotice severity="error" title={s('can.errorTitle')} reason={preview.error} testId="connector-can-error"
          fix={{ label: s('can.retry'), onClick: preview.reload, testId: 'connector-can-retry' }} />
      )}
      {kinds.length > 0 && (
        <div className="dk-field cn-mx__pick">
          <label className="dk-label" htmlFor="cn-mx-sender">{s('matrix.pick')}</label>
          <Select id="cn-mx-sender" data-testid="connector-matrix-sender" value={shown} onChange={(e) => setMobile(e.target.value)}>
            {kinds.map((c) => <option key={c.id} value={c.id}>{colLabel(c)}</option>)}
          </Select>
        </div>
      )}
      <div className="cn-matrix__wrap">
        <table className="cn-matrix cn-mx__table" aria-label={s('matrix.title')} data-dim={loading ? 'true' : undefined}>
          <thead>
            <tr>
              <th scope="col"><span className="cn-vh">{s('matrix.capability')}</span></th>
              {columns.map((c) => (
                <th key={c.id} scope="col" id={`cn-mx-col-${c.id}`} data-col={c.id} data-active={c.id === shown ? 'true' : undefined}>
                  {colLabel(c)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.flatMap((r) => {
              const cap = r.parent ? null : capOf(r.cap)
              const on = (form.caps || []).includes(r.cap)
              const lacks = Boolean(cap) && Boolean(form.agent_id) && held !== null && !agentHas(held, r.cap)
              const expanded = open.has(r.key)
              const out = [(
                <tr key={r.key} className={r.parent ? 'cn-mx__sub' : undefined} data-testid={`connector-matrix-row-${r.key}`}>
                  <th scope="row">
                    <span className="cn-mx__title" id={`cn-mx-row-${r.key}`}>{rowTitle(r)}</span>
                    {cap?.sensitive && <span className="cn-badge cn-badge--warn" data-testid={`connector-cap-sensitive-${r.cap}`}>{s('perm.sensitive')}</span>}
                    {cap && (
                      <button type="button" className="cn-hint__more cn-mx__more" aria-expanded={expanded} data-testid={`connector-matrix-more-${r.key}`}
                        onClick={() => toggle(r.key)}>{expanded ? s('matrix.less') : s('matrix.more')}</button>
                    )}
                  </th>
                  {columns.map((c) => {
                    const active = c.id === shown
                    if (c.id === 'on') {
                      return (
                        <td key={c.id} className="cn-mx__cell cn-mx__cell--on" data-col="on" data-testid={cap ? `connector-cap-${r.cap}` : undefined} data-lacks={lacks ? 'true' : undefined}>
                          {cap ? (<>
                            <Switch checked={on} label={`${s('matrix.col.on')}: ${rowTitle(r)}`} testId={`connector-cap-switch-${r.cap}`}
                              disabled={lacks && !on} describedBy={lacks ? `cn-lacks-${r.cap}` : undefined} onChange={(v) => setForm(setCap(form, r.cap, v))} />
                            {lacks && <span className="cn-mx__why" id={`cn-lacks-${r.cap}`} data-testid={`connector-cap-lacks-${r.cap}`}>{s('perm.agentLacks')}</span>}
                          </>) : <span className="cn-mx__static" aria-hidden="true" />}
                        </td>
                      )
                    }
                    const edit = c.role && cap && grantableKind(r.cap)
                    return (
                      <Cell key={c.id} row={r} col={c} cell={r.cells[c.id]} s={s} describedBy={`cn-mx-row-${r.key} cn-mx-col-${c.id}`} active={active}
                        picked={picked?.row === r.key && picked?.col === c.id}
                        onPick={() => setPicked(picked?.row === r.key && picked?.col === c.id ? null : { row: r.key, col: c.id })}>
                        {edit && (
                          <input type="checkbox" className="dk-check cn-mx__grant" data-testid={`connector-grant-${c.role}-${r.cap}`}
                            aria-label={s('perm.roleCap', { role: c.title, cap: rowTitle(r) })} disabled={!grantable.includes(r.cap)}
                            checked={roleHas(form, c.role, r.cap)} onChange={(e) => setForm(setGrant(form, c.role, r.cap, e.target.checked, capabilities))} />
                        )}
                      </Cell>
                    )
                  })}
                </tr>
              )]
              if (cap && expanded) {
                out.push((
                  <tr key={`${r.key}:more`} className="cn-mx__disclosure" data-testid={`connector-matrix-disclosure-${r.key}`}>
                    <td colSpan={phone ? Math.min(3, columns.length + 1) : columns.length + 1}>
                      <Hint>{cap.description}{cap.sensitive ? ` ${s('perm.sensitiveHelp')}` : ''}</Hint>
                      {r.cap === 'triggers.propose' && form.approval && (<>
                        <p className="cn-cap__effect" data-testid="connector-cap-effect-triggers.propose">{approvalEffect(form.approval, s)}</p>
                        <ApprovalOptions form={form} setForm={setForm} s={s} off={!on} held={held} />
                      </>)}
                    </td>
                  </tr>
                ))
              }
              return out
            })}
          </tbody>
        </table>
      </div>
      <div className="cn-mx__detail" data-testid="connector-matrix-detail" role="region" aria-live="polite" aria-label={s('matrix.detail.label')}>
        {!d ? <p className="cn-muted">{s('matrix.detail.none')}</p> : (
          <div data-testid={`connector-can-${selRow.cap}`} data-allowed={d.allowed ? 'true' : 'false'} className="cn-mx__detail-body">
            <p className="cn-mx__detail-title">{s('matrix.detail.title', { capability: rowTitle(selRow), who: colLabel(columns.find((c) => c.id === picked.col)) })}</p>
            {d.allowed
              ? <p data-testid="connector-matrix-message">{viaText(d, s) ? sentenceCase(viaText(d, s)) : s('can.allowed')}</p>
              : <p data-testid="connector-matrix-message">{d.message || d.reason || s('can.noMessage')}</p>}
            {!d.allowed && remainingChecks(d.then_check).length > 0 && (
              <p className="cn-muted">{s('can.then', { list: remainingChecks(d.then_check).map((x) => s(`can.then.${x}`)).join(', ') })}</p>
            )}
            {!d.allowed && d.fix && <div className="cn-can__fix"><FixButton fix={d.fix} ctx={ctx} s={s} /></div>}
          </div>
        )}
      </div>
    </section>
  )
}
