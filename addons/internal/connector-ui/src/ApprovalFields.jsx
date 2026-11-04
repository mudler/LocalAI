/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// How the triggers that the agent proposes in a chat are approved. The same on
// every platform, so the words live here as defaults, like the other shared
// fields. The server enforces the limits; the page only bounds what is typed.
import { Field, RadioCards, TextInput } from './controls'
import { APPROVAL_LIMITS, APPROVAL_MODES, approvalProblem } from './connector'

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
  'approval.short.ask': 'asks first',
  'approval.short.auto': 'set up at once',
  'approval.short.auto_readonly': 'at once if read-only',
}

export default function ApprovalFields({ form, setForm, s, triggersOff = false }) {
  const a = form.approval
  const set = (patch) => setForm({ ...form, approval: { ...a, ...patch } })
  const auto = a.mode !== 'ask'
  const bad = approvalProblem(a)
  return (
    <fieldset className="cn-group" data-testid="connector-approval">
      <legend>{s('approval.title')}</legend>
      <p className="dk-hint">{s('approval.help')}</p>
      {triggersOff && <p className="cn-muted" data-testid="connector-approval-off">{s('perm.triggersNeed')}</p>}
      <RadioCards name="approval" legend={s('approval.title')} value={a.mode} onChange={(mode) => set({ mode })}
        options={APPROVAL_MODES.map((m) => ({ value: m, label: s(`approval.mode.${m}`) }))} />
      {auto && (<>
        <p className="dk-hint">{s('approval.limitsHelp')}</p>
        {APPROVAL_LIMITS.map(({ key, max, fallback }) => (
          <Field key={key} label={s(`approval.${key}`)} hint={s('approval.default', { value: fallback })} error={bad === key ? s('approval.invalid', { max }) : undefined}>
            {(p) => <TextInput {...p} type="number" inputMode="numeric" min="1" max={max} step="1" data-testid={`connector-approval-${key}`}
              value={a[key]} onChange={(e) => set({ [key]: e.target.value })} />}
          </Field>
        ))}
      </>)}
    </fieldset>
  )
}
