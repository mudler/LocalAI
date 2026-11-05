/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// How the triggers that the agent proposes in a chat are approved. It is shown
// under the permission "Propose triggers" (see PermissionFields), as an option
// of it. The same on every platform, so the words live here as defaults, like the other shared
// fields. The server enforces the limits; the page only bounds what is typed.
import { useId, useState } from 'react'
import { Field, RadioCards, Switch, TextInput } from './controls'
import { APPROVAL_ACTS, APPROVAL_LIMITS, APPROVAL_MODES, agentHoldsAct, approvalProblem } from './connector'

/** The line under "Propose triggers": the mode, and the acts a run may do. */
export function approvalEffect(a, s) {
  const base = s('approval.effect', { mode: s(`approval.short.${a.mode || 'ask'}`) })
  const acts = APPROVAL_ACTS.filter((n) => (a.acts || []).includes(n))
  return acts.length === 0 ? base : base + s('approval.effectActs', { acts: acts.map((n) => s(`approval.actShort.${n}`)).join(', ') })
}

/**
 * The approval mode and its limits, as an option under "Propose triggers".
 * With off set (the permission is not ticked) the choice is greyed, with the
 * reason, and nothing can be changed. The limits sit in a closed disclosure.
 */
export default function ApprovalOptions({ form, setForm, s, off = false, held = null }) {
  const a = form.approval
  const reasonId = useId()
  const [actsOpen, setActsOpen] = useState(false)
  const set = (patch) => setForm({ ...form, approval: { ...a, ...patch } })
  const bad = approvalProblem(a)
  const acts = a.acts || []
  const actsOn = acts.length > 0 || actsOpen
  const toggle = (name, v) => set({ acts: APPROVAL_ACTS.filter((n) => (n === name ? v : acts.includes(n))) })
  return (
    <fieldset className="cn-option" data-testid="connector-approval" data-off={off ? 'true' : undefined} disabled={off} aria-describedby={off ? reasonId : undefined}>
      <legend className="cn-vh">{s('approval.title')}</legend>
      <p className="cn-option__title">{s('approval.title')}</p>
      <p className="dk-hint">{s('approval.help')}</p>
      {off && <p className="cn-muted" id={reasonId} data-testid="connector-approval-off">{s('perm.triggersNeed')}</p>}
      <RadioCards name="approval" legend={s('approval.title')} value={a.mode} onChange={(mode) => set({ mode })}
        options={APPROVAL_MODES.map((m) => ({ value: m, label: s(`approval.mode.${m}`) }))} />
      <div className="cn-acts" data-testid="connector-acts">
        <Switch checked={actsOn} testId="connector-acts-switch" onChange={(v) => { setActsOpen(v); if (!v) set({ acts: [] }) }}>
          <span className="cn-cap__title">{s('approval.acts.title')}</span>
        </Switch>
        <p className="dk-hint">{s('approval.acts.help')}</p>
        {actsOn && (
          <ul className="cn-acts__list">
            {APPROVAL_ACTS.map((n) => {
              const on = acts.includes(n)
              const lacks = !agentHoldsAct(held, n)
              const blocked = lacks && !on
              return (
                <li key={n} className="cn-acts__item" data-testid={`connector-act-${n}`} data-lacks={lacks ? 'true' : undefined}>
                  <label className="cn-acts__label">
                    <input type="checkbox" className="dk-check" data-testid={`connector-act-check-${n}`} checked={on} disabled={blocked}
                      aria-describedby={lacks ? `${reasonId}-${n}` : undefined} onChange={(e) => toggle(n, e.target.checked)} />
                    <span className="cn-acts__text">
                      <span className="cn-cap__title">{s(`approval.acts.${n}`)}</span>
                      <span className="cn-badge cn-badge--warn">{s('approval.acts.sensitive')}</span>
                    </span>
                  </label>
                  <p className="cn-cap__effect">{s(`approval.acts.${n}.effect`)}</p>
                  {lacks && <p className="cn-cap__lacks" id={`${reasonId}-${n}`} data-testid={`connector-act-lacks-${n}`}>{s('approval.acts.lacks')}</p>}
                </li>
              )
            })}
          </ul>
        )}
        <p className="dk-hint" data-testid="connector-acts-modes">{s('approval.acts.modes')}</p>
      </div>
      <details className="cn-option__limits" data-testid="connector-approval-limits" open={bad ? true : undefined}>
        <summary>{s('approval.limits')}</summary>
        <p className="dk-hint">{s('approval.limitsHelp')}</p>
        {APPROVAL_LIMITS.map(({ key, max, fallback }) => (
          <Field key={key} label={s(`approval.${key}`)} hint={s('approval.default', { value: fallback })} error={bad === key ? s('approval.invalid', { max }) : undefined}>
            {(p) => <TextInput {...p} type="number" inputMode="numeric" min="1" max={max} step="1" data-testid={`connector-approval-${key}`}
              value={a[key]} onChange={(e) => set({ [key]: e.target.value })} />}
          </Field>
        ))}
      </details>
    </fieldset>
  )
}
