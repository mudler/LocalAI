/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// What the senders of a binding may do. The host owns the list of capabilities
// and enforces it on every turn; this section edits the owner's side of it:
// the capabilities the binding allows, what each role of the kind gets, the
// approved channels for talk-back, and the paired senders. A binding that was
// saved before the host enforced permissions has none switched on, so it denies
// everything gated; the notice and the preset below say so and fix it in a click.
import { Field, Hint, Notice, Switch, TextInput } from './controls'
import Icon from '../../host-compat/src/components/Icon'
import { MAX_CHANNELS, MAX_CHANNEL_ID, MAX_CHANNEL_LABEL, channelsProblem, grantableFor, hasNoCaps, roleHas, setCap, setGrant, usualCaps } from './connector'
import PairingFields from './PairingFields'

export const PERMISSION_TEXT = {
  'perm.title': 'What senders may do',
  'perm.help': 'Choose what the people who write to this bot may ask the agent to do. Anything switched off stays off for everyone, whatever a role or a pairing says.',
  'perm.noCapsTitle': 'No permission is on, so these features are off',
  'perm.noCapsText': 'This bot was saved before permissions existed, or no permission was chosen yet. Until you turn some on, senders cannot send files to the agent, set up triggers, save to memory or have the agent post to a channel. Messages are still answered.',
  'perm.usual': 'Allow the usual',
  'perm.usualHelp': 'Switches on the everyday permissions that stay inside LocalAI: {{names}}. Sensitive ones are never included.',
  'perm.usualDone': 'The usual permissions are on. Save to keep them.',
  'perm.usualSaved': 'The usual permissions are saved for {{name}}.',
  'perm.sensitive': 'Paired senders only',
  'perm.sensitiveHelp': 'Sensitive. Never given to a role. Only a sender you paired can use it, and every use is recorded.',
  'perm.capsLegend': 'Permissions of this bot',
  'perm.capsHelp': 'The ceiling for everyone. A role can be given less, never more.',
  'perm.rolesTitle': 'Roles',
  'perm.rolesHelp': 'The platform tells the host which roles a sender has. Choose what each role may do. Sensitive permissions are never given to a role, and a role can only get a permission that is switched on above.',
  'perm.rolesNone': 'Switch on a permission above that this add-on can give to a role, and it shows here.',
  'perm.rolesGrid': 'What each role may do',
  'perm.roleCap': '{{role}}: {{cap}}',
  'perm.channelsTitle': 'Approved channels',
  'perm.channelsHelp': 'Chats where the agent may post when a sender asks, besides the chat it answers in. A channel that is not approved needs a paired sender for each message. At most {{max}}.',
  'perm.channelsOff': 'Switch on "Post to approved channels" above to choose channels.',
  'perm.channelId': 'Channel ID',
  'perm.channelIdHelp': 'The ID the platform gives the chat. No spaces.',
  'perm.channelLabel': 'Name',
  'perm.channelLabelHelp': 'A name for you and the agent. Optional.',
  'perm.channelApproved': 'Approved',
  'perm.channelApprovedHelp': 'On: the agent may post here on any allowed sender\'s request.',
  'perm.channelAdd': 'Add a channel',
  'perm.channelRemove': 'Remove channel {{id}}',
  'perm.remove': 'Remove',
  'perm.channelFull': 'The list is full.',
  'perm.channelProblem.id': 'Each channel needs an ID of 1 to {{max}} bytes, with no space.',
  'perm.channelProblem.label': 'A name has at most {{max}} characters.',
  'perm.channelProblem.repeat': 'A channel is listed twice.',
  'perm.channelProblem.count': 'At most {{max}} channels.',
  'perm.triggersNeed': 'These settings apply only while "Propose triggers" is on above.',
}

export function permissionT(t) {
  return (key, opts = {}) => t(key, { defaultValue: PERMISSION_TEXT[key], ...opts })
}

function Cap({ cap, on, onChange, s }) {
  return (
    <li className="cn-cap" data-testid={`connector-cap-${cap.name}`}>
      <Switch checked={on} onChange={onChange} testId={`connector-cap-switch-${cap.name}`}>
        <span className="cn-cap__title">{cap.title}</span>
        {cap.sensitive && <span className="cn-badge cn-badge--warn" data-testid={`connector-cap-sensitive-${cap.name}`}>{s('perm.sensitive')}</span>}
      </Switch>
      <Hint more={s('more')} less={s('less')}>{cap.description}{cap.sensitive ? ` ${s('perm.sensitiveHelp')}` : ''}</Hint>
    </li>
  )
}

function Roles({ form, setForm, kind, capabilities, s }) {
  const roles = kind?.roles || []
  if (roles.length === 0) return null
  const cols = grantableFor(kind, form.caps)
  const title = (name) => capabilities.find((c) => c.name === name)?.title || name
  return (
    <div className="cn-sub" data-testid="connector-roles">
      <h3 className="cn-sub__title">{s('perm.rolesTitle')}</h3>
      <p className="dk-hint">{s('perm.rolesHelp')}</p>
      {cols.length === 0 ? <p className="cn-muted">{s('perm.rolesNone')}</p> : (
        <div className="cn-matrix__wrap">
          <table className="cn-matrix" aria-label={s('perm.rolesGrid')}>
            <thead>
              <tr>
                <th scope="col"><span className="cn-vh">{s('perm.rolesTitle')}</span></th>
                {cols.map((c) => <th key={c} scope="col">{title(c)}</th>)}
              </tr>
            </thead>
            <tbody>
              {roles.map((r) => (
                <tr key={r.id}>
                  <th scope="row">
                    <span>{r.title}</span>
                    {r.description && <span className="cn-muted cn-matrix__desc">{r.description}</span>}
                  </th>
                  {cols.map((c) => (
                    <td key={c}>
                      <input type="checkbox" className="dk-check" data-testid={`connector-grant-${r.id}-${c}`}
                        aria-label={s('perm.roleCap', { role: r.title, cap: title(c) })}
                        checked={roleHas(form, r.id, c)} onChange={(e) => setForm(setGrant(form, r.id, c, e.target.checked, capabilities))} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

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
 * The notice of a binding that has no permission on, with the one-click preset.
 * It stands alone so the list of bindings can show it too.
 */
export function NoCapsNotice({ capabilities, onUsual, busy, s, name }) {
  const usual = (capabilities || []).filter((c) => c.usual && !c.sensitive)
  return (
    <Notice tone="warn" role="note" testId="connector-nocaps" className="cn-nocaps">
      <p className="cn-notice__title">{s('perm.noCapsTitle')}</p>
      <p className="cn-notice__text">{s('perm.noCapsText')}</p>
      {usual.length > 0 && <p className="cn-notice__text">{s('perm.usualHelp', { names: usual.map((c) => c.title).join(', ') })}</p>}
      <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="connector-usual" disabled={busy || usual.length === 0}
        aria-label={name ? `${s('perm.usual')}: ${name}` : undefined} onClick={onUsual}>{s('perm.usual')}</button>
    </Notice>
  )
}

/**
 * @param {object} props
 * @param {object} props.form       the form of the binding
 * @param {Function} props.setForm
 * @param {object} props.kind       the kind from the kinds call (roles, permissions)
 * @param {Array} props.capabilities the closed list from the kinds call
 * @param {Function} props.s        the translation function with the defaults
 * @param {Function} props.onPaired called when pairing changed the binding
 */
export default function PermissionFields({ form, setForm, kind, capabilities, s, onPaired }) {
  const caps = capabilities || []
  if (caps.length === 0) return null
  const on = (name) => (form.caps || []).includes(name)
  const usual = usualCaps(caps)
  return (
    <fieldset className="cn-group" data-testid="connector-permissions">
      <legend>{s('perm.title')}</legend>
      <p className="dk-hint">{s('perm.help')}</p>
      {hasNoCaps(form) && (
        <NoCapsNotice capabilities={caps} s={s}
          onUsual={() => setForm({ ...form, caps: [...new Set([...(form.caps || []), ...usual])] })} />
      )}
      <ul className="cn-caps" aria-label={s('perm.capsLegend')}>
        {caps.map((c) => <Cap key={c.name} cap={c} on={on(c.name)} s={s} onChange={(v) => setForm(setCap(form, c.name, v))} />)}
      </ul>
      <Roles form={form} setForm={setForm} kind={kind} capabilities={caps} s={s} />
      <Channels form={form} setForm={setForm} s={s} />
      <PairingFields binding={form} s={s} onPaired={onPaired} />
    </fieldset>
  )
}
