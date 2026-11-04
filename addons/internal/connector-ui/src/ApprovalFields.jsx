/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// How the triggers that the agent proposes in a chat are approved. It is shown
// under the permission "Propose triggers" (see PermissionFields), as an option
// of it. The same on every platform, so the words live here as defaults, like the other shared
// fields. The server enforces the limits; the page only bounds what is typed.
import { useId, useState } from 'react'
import { Field, RadioCards, Switch, TextInput } from './controls'
import { APPROVAL_ACTS, APPROVAL_LIMITS, APPROVAL_MODES, agentHoldsAct, approvalProblem } from './connector'

export const APPROVAL_TEXT = {
  'approval.title': 'Timers and triggers from the chat',
  'approval.help': 'When the agent sets up something that runs later ("in 10 minutes", "every day"), every run is one model run.',
  'approval.mode.ask': 'Ask me: the bot proposes it in the chat and the person answers yes or no',
  'approval.mode.auto': 'Set it up at once and tell the person how to cancel it',
  'approval.mode.auto_readonly': 'At once when the runs only read and report; otherwise ask',
  'approval.limitsHelp': 'These limits bound what is set up without asking. Leave a field empty for its default. Anything over a limit is asked instead.',
  'approval.max_pending': 'Most timers and triggers at once',
  'approval.min_gap_minutes': 'Fewest minutes between runs of a recurring trigger',
  'approval.max_runs_per_day': 'Most runs a day, all together',
  'approval.max_lifetime_days': 'Longest lifetime, in days',
  'approval.invalid': 'Use a whole number from 1 to {{max}}, or leave it empty.',
  'approval.default': 'Default: {{value}}.',
  'approval.summary': 'Triggers: {{mode}}',
  'approval.effect': 'Proposals: {{mode}}',
  'approval.limits': 'Limits',
  'approval.acts.title': 'Runs may act',
  'approval.acts.help': 'Off: the runs of a trigger made from this chat only read and report. On: they may also do what you tick below, and only what the agent has.',
  'approval.acts.modes': 'Ask me: you always decide. At once: only when you are paired as sender and every act is ticked here.',
  'approval.acts.act.files': 'Write files in the agent\'s folders',
  'approval.acts.act.files.effect': 'A run can create and change files the agent keeps.',
  'approval.acts.act.addons': 'Use the actions of the agent\'s add-ons',
  'approval.acts.act.addons.effect': 'A run can use what the add-ons of the agent do, including commands.',
  'approval.acts.act.send': 'Send messages beyond where it reports',
  'approval.acts.act.send.effect': 'A run can write to other chats and channels on its own.',
  'approval.acts.act.spend': 'Use paid or external tools and the network',
  'approval.acts.act.spend.effect': 'A run can call services outside LocalAI, which may cost money.',
  'approval.acts.lacks': 'The agent does not have this.',
  'approval.acts.sensitive': 'Sensitive',
  'approval.effectActs': ', runs may: {{acts}}',
  'approval.actShort.act.files': 'write files',
  'approval.actShort.act.addons': 'use add-ons',
  'approval.actShort.act.send': 'send messages',
  'approval.actShort.act.spend': 'use paid or external tools',
  'approval.short.ask': 'asks first',
  'approval.short.auto': 'set up at once',
  'approval.short.auto_readonly': 'at once if read-only',
}

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
