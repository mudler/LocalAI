/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The two switches of what the agent may do later in a chat by itself: wait
// for a change (loops.wait) and run on a timer (loops.start). The host marks
// them binding_only: they belong to the bot, never to a role, so they sit
// apart from the matrix. The titles and texts come from the host's list, in
// the language of the page. The stored capability names do not change.
import { Switch } from './controls'
import { loopCaps, setCap } from './connector'

/**
 * @param {object} props
 * @param {object} props.form          the form of the binding
 * @param {Function} props.setForm
 * @param {object[]} props.capabilities the host's list of capabilities
 * @param {Function} props.s            the translation function with the defaults
 */
export default function LoopSwitches({ form, setForm, capabilities, s }) {
  const caps = loopCaps(capabilities)
  if (caps.length === 0) return null
  return (
    <section className="cn-sub cn-loops" data-testid="connector-loops" aria-labelledby="cn-loops-title">
      <h4 className="cn-sub__title" id="cn-loops-title">{s('loops.title')}</h4>
      <p className="dk-hint">{s('loops.help')}</p>
      <ul className="cn-loops__list">
        {caps.map((c) => {
          const on = (form.caps || []).includes(c.name)
          const desc = `cn-loop-desc-${c.name.replace('.', '-')}`
          return (
            <li key={c.name} className="cn-loops__row" data-testid={`connector-loop-${c.name}`} data-on={on ? 'true' : 'false'}>
              <div className="cn-loops__words">
                <span className="cn-loops__title">
                  {c.title}
                  {c.sensitive && <span className="cn-badge cn-badge--warn" data-testid={`connector-loop-sensitive-${c.name}`}>{s('perm.sensitive')}</span>}
                </span>
                <p className="dk-hint" id={desc}>{c.description}</p>
              </div>
              <Switch checked={on} label={c.title} testId={`connector-loop-switch-${c.name}`} describedBy={desc}
                onChange={(v) => setForm(setCap(form, c.name, v))} />
            </li>
          )
        })}
      </ul>
    </section>
  )
}
