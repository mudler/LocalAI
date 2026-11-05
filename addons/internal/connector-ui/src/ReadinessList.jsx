/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The readiness list at the top of the form: one row per thing that must agree
// for the bot to work, each with a state, one sentence and at most one fix.
// The rows come from access.js; this file only draws them.
import { useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { FixButton } from './CapabilitiesPanel'
import { readinessRows, visibleRows } from './access'

// The words of a row. A key with a count has plural forms (_one, _other, ...)
// that the translator picks by the rules of the language.
function sentence(row, s) {
  const params = row.params || {}
  const { messageKey, modeKey, ...rest } = params
  const words = messageKey ? { ...rest, message: s(messageKey) } : rest
  return s(row.key, modeKey ? { ...words, mode: s(modeKey) } : words)
}

const ICON = { ok: 'check-circle', attention: 'warning', off: 'minus', after_save: 'save', broken: 'alert-circle', checking: 'spinner', unknown: 'help-circle' }

export default function ReadinessList({ rowsInput, s, ctx, onTest, onSave }) {
  const rows = readinessRows(rowsInput)
  const [expanded, setExpanded] = useState(false)
  const { shown, hidden } = visibleRows(rows, expanded)
  const problems = rows.length - rows.filter((r) => ['ok', 'off', 'checking'].includes(r.state)).length
  return (
    <section className="cn-ready" aria-labelledby="cn-ready-title" data-testid="connector-readiness">
      <div className="cn-ready__head">
        <h2 id="cn-ready-title" className="cn-sub__title">{s('ready.title')}</h2>
        <span className="cn-muted" data-testid="connector-ready-summary">{problems > 0 ? s('ready.problems', { count: problems }) : rows.some((r) => r.state === 'checking') ? s('ready.checkingSummary') : s('ready.allGood')}</span>
      </div>
      <ul className="cn-ready__list" aria-label={s('ready.title')}>
        {shown.map((r) => (
          <li key={r.id} className="cn-ready__row" data-testid={`connector-ready-${r.id}`} data-state={r.state}>
            <span className={`cn-ready__state cn-ready__state--${r.state}`}>
              <Icon name={ICON[r.state] || 'info'} />
              <span>{s(`ready.state.${r.state}`)}</span>
            </span>
            <div className="cn-ready__text">
              <span className="cn-ready__label">{s(r.labelKey || `ready.row.${r.id}`)}</span>
              <span className="cn-ready__sentence">{sentence(r, s)}</span>
            </div>
            {r.fix && <div className="cn-ready__fix"><FixButton fix={r.fix} ctx={ctx} s={s} onTest={onTest} onSave={onSave} /></div>}
          </li>
        ))}
      </ul>
      {(hidden > 0 || expanded) && (
        <button type="button" className="cn-hint__more cn-ready__toggle" aria-expanded={expanded} data-testid="connector-ready-toggle" onClick={() => setExpanded(!expanded)}>
          {expanded ? s('ready.showProblems') : s('ready.showAll', { count: rows.length })}
        </button>
      )}
    </section>
  )
}
