/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The rest of what a connection lets its senders do, besides the matrix of
// who can do what (AccessMatrix): the places the bot may post (talk-back). The
// people and channels the bot listens to are in WhoFields. The host owns the
// list of capabilities and enforces it on every turn.
import { Field, Hint, Switch, TextInput } from './controls'
import Icon from '../../host-compat/src/components/Icon'
import { MAX_CHANNELS, MAX_CHANNEL_ID, MAX_CHANNEL_LABEL, channelsProblem } from './connector'

function Channels({ form, setForm, s }) {
  const list = form.channels || []
  const problem = channelsProblem(list)
  const set = (i, patch) => setForm({ ...form, channels: list.map((c, j) => (j === i ? { ...c, ...patch } : c)) })
  return (
    <div className="cn-sub" data-testid="connector-channels">
      <h3 className="cn-sub__title">{s('perm.channelsTitle')}</h3>
      <p className="dk-hint">{s('perm.channelsHelp', { max: MAX_CHANNELS })}</p>
      {!(form.caps || []).includes('talkback.channels') ? <p className="cn-muted">{s('perm.channelsOff')}</p> : (<>
        <ul className="cn-channels">
          {list.map((c, i) => (
            <li key={i} className="cn-channel" data-testid="connector-channel">
              <Field label={s('perm.channelId')} hint={s('perm.channelIdHelp')}>
                {(p) => <TextInput {...p} mono maxLength={MAX_CHANNEL_ID} value={c.id} data-testid="connector-channel-id" onChange={(e) => set(i, { id: e.target.value })} />}
              </Field>
              <Field label={s('perm.channelLabel')} hint={s('perm.channelLabelHelp')} optional>
                {(p) => <TextInput {...p} maxLength={MAX_CHANNEL_LABEL} value={c.label} data-testid="connector-channel-label" onChange={(e) => set(i, { label: e.target.value })} />}
              </Field>
              <div className="cn-switchfield">
                <Switch checked={Boolean(c.approved)} testId="connector-channel-approved" onChange={(on) => set(i, { approved: on })}>{s('perm.channelApproved')}</Switch>
                <Hint more={s('more')} less={s('less')}>{s('perm.channelApprovedHelp')}</Hint>
              </div>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={s('perm.channelRemove', { id: c.id || '' })}
                onClick={() => setForm({ ...form, channels: list.filter((_, j) => j !== i) })}>
                <Icon name="close" /> {s('perm.remove')}
              </button>
            </li>
          ))}
        </ul>
        {problem && <p className="dk-field-error" role="alert" data-testid="connector-channel-problem">{s(`perm.channelProblem.${problem}`, { max: problem === 'id' ? MAX_CHANNEL_ID : problem === 'label' ? MAX_CHANNEL_LABEL : MAX_CHANNELS })}</p>}
        <div>
          <button type="button" className="dk-btn dk-btn--secondary" disabled={list.length >= MAX_CHANNELS} data-testid="connector-channel-add"
            onClick={() => setForm({ ...form, channels: [...list, { id: '', label: '', approved: false }] })}>
            <Icon name="plus" /> {list.length >= MAX_CHANNELS ? s('perm.channelFull') : s('perm.channelAdd')}
          </button>
        </div>
      </>)}
    </div>
  )
}

/**
 * @param {object} props
 * @param {object} props.form       the form of the binding
 * @param {Function} props.setForm
 * @param {Function} props.s        the translation function with the defaults
 */
export default function PermissionFields({ form, setForm, s }) {
  return (
    <div className="cn-places" data-testid="connector-permissions">
      <Channels form={form} setForm={setForm} s={s} />
    </div>
  )
}
