/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The places the bot may post (talk-back), besides the matrix of who can do
// what (AccessMatrix): a table with a name, an ID and one choice for who may
// make the bot post there. The people and channels the bot listens to are in
// WhoFields. The host owns the list of capabilities and enforces it on every
// turn. The saved field does not change: channels[] with approved.
import { Select, TextInput } from './controls'
import Icon from '../../host-compat/src/components/Icon'
import { MAX_CHANNELS, MAX_CHANNEL_ID, MAX_CHANNEL_LABEL, channelsProblem } from './connector'

// The two values of the choice, mapped to approved: any allowed person
// (approved true) or only a verified person (approved false).
export const POST_ANY = 'any'
export const POST_VERIFIED = 'verified'
export const postValueOf = (c) => (c.approved ? POST_ANY : POST_VERIFIED)

function Channels({ form, setForm, s, chatNames, onSwitchOn }) {
  const list = form.channels || []
  const problem = channelsProblem(list)
  const set = (i, patch) => setForm({ ...form, channels: list.map((c, j) => (j === i ? { ...c, ...patch } : c)) })
  const on = (form.caps || []).includes('talkback.channels')
  return (
    <div className="cn-sub" data-testid="connector-channels">
      <h3 className="cn-sub__title">{s('perm.channelsTitle')}</h3>
      <p className="dk-hint">{s('perm.channelsHelp', { max: MAX_CHANNELS })}</p>
      {!on ? (
        <p className="cn-muted">
          {s('perm.channelsOff')}{' '}
          <button type="button" className="cn-link cn-link--text" data-testid="connector-channels-switch" onClick={onSwitchOn}>{s('perm.channelsSwitch')}</button>
        </p>
      ) : (<>
        <ul className="cn-channels" aria-label={s('perm.channelsTitle')}>
          {list.map((c, i) => (
            <li key={i} className="cn-channel" data-testid="connector-channel" data-approved={c.approved ? 'true' : 'false'}>
              <div className="cn-channel__cell" data-label={s('perm.channelLabel')}>
                <TextInput maxLength={MAX_CHANNEL_LABEL} value={c.label} data-testid="connector-channel-label" aria-label={s('perm.channelLabel')}
                  placeholder={chatNames?.get(c.id) || ''} onChange={(e) => set(i, { label: e.target.value })} />
              </div>
              <div className="cn-channel__cell" data-label={s('perm.channelId')}>
                <TextInput mono maxLength={MAX_CHANNEL_ID} value={c.id} data-testid="connector-channel-id" aria-label={s('perm.channelId')}
                  onChange={(e) => set(i, { id: e.target.value })} />
              </div>
              <div className="cn-channel__cell cn-channel__cell--who" data-label={s('perm.channelWho')}>
                <Select data-testid="connector-channel-approved" aria-label={s('perm.channelWhoFor', { id: c.label || c.id || '' })} value={postValueOf(c)}
                  onChange={(e) => set(i, { approved: e.target.value === POST_ANY })}>
                  <option value={POST_ANY}>{s('perm.channelWho.any')}</option>
                  <option value={POST_VERIFIED}>{s('perm.channelWho.verified')}</option>
                </Select>
              </div>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-label={s('perm.channelRemove', { id: c.label || c.id || '' })}
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
 * @param {Map} [props.chatNames]   chat ID -> name, from the sender log
 * @param {Function} props.onSwitchOn  takes the person to the switch of "Post to other channels"
 */
export default function PermissionFields({ form, setForm, s, chatNames, onSwitchOn }) {
  return (
    <div className="cn-places" data-testid="connector-permissions">
      <Channels form={form} setForm={setForm} s={s} chatNames={chatNames} onSwitchOn={onSwitchOn} />
    </div>
  )
}
