/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// What the senders of a binding may do. The host owns the list of capabilities
// and enforces it on every turn; this section edits the owner's side of it:
// the capabilities the binding allows, what each role of the kind gets, the
// channels it may post to (talk-back), and the paired senders. A binding that was
// saved before the host enforced permissions has none switched on, so it denies
// everything gated; the notice and the preset below say so and fix it in a click.
import { useCallback, useState } from 'react'
import { Field, Hint, Notice, Switch, TextInput } from './controls'
import Icon from '../../host-compat/src/components/Icon'
import { MAX_CHANNELS, MAX_CHANNEL_ID, MAX_CHANNEL_LABEL, agentHas, agentHeld, channelsProblem, copyFromAgent, grantableFor, hasNoCaps, presetBlock, roleHas, setCap, setGrant, unpairedSensitive, usualFor, withAddedCaps, withCopiedCaps } from './connector'
import PairingFields from './PairingFields'
import ApprovalOptions, { approvalEffect } from './ApprovalFields'

function Cap({ cap, on, onChange, s, lacks, children, effect }) {
  // A permission the agent does not have cannot be switched on; one that is
  // already on can still be switched off.
  const blocked = lacks && !on
  return (
    <li className="cn-cap" data-testid={`connector-cap-${cap.name}`} data-lacks={lacks ? 'true' : undefined}>
      <Switch checked={on} onChange={onChange} disabled={blocked} describedBy={lacks ? `cn-lacks-${cap.name}` : undefined} testId={`connector-cap-switch-${cap.name}`}>
        <span className="cn-cap__title">{cap.title}</span>
        {cap.sensitive && <span className="cn-badge cn-badge--warn" data-testid={`connector-cap-sensitive-${cap.name}`}>{s('perm.sensitive')}</span>}
      </Switch>
      <Hint more={s('more')} less={s('less')}>{cap.description}{cap.sensitive ? ` ${s('perm.sensitiveHelp')}` : ''}</Hint>
      {effect && <p className="cn-cap__effect" data-testid={`connector-cap-effect-${cap.name}`}>{effect}</p>}
      {children}
      {lacks && <p className="cn-cap__lacks" id={`cn-lacks-${cap.name}`} data-testid={`connector-cap-lacks-${cap.name}`}>{s('perm.agentLacks')}</p>}
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
  // Inside the form the presets below do the same job, limited to the agent.
  const usual = (capabilities || []).filter((c) => c.usual && !c.sensitive)
  return (
    <Notice tone="warn" role="note" testId="connector-nocaps" className="cn-nocaps">
      <p className="cn-notice__title">{s('perm.noCapsTitle')}</p>
      <p className="cn-notice__text">{s('perm.noCapsText')}</p>
      {usual.length > 0 && <p className="cn-notice__text">{s('perm.usualHelp', { names: usual.map((c) => c.title).join(', ') })}</p>}
      {onUsual && (
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="connector-usual" disabled={busy || usual.length === 0}
          aria-label={name ? `${s('perm.usual')}: ${name}` : undefined} onClick={onUsual}>{s('perm.usual')}</button>
      )}
    </Notice>
  )
}


/**
 * Two buttons that fill the permissions of the form from the agent of the
 * binding: the usual set (what the agent has), and a copy of everything the
 * agent has. They only change the form; the owner saves as for any change.
 */
function Presets({ form, setForm, capabilities, agent, s }) {
  const [done, setDone] = useState(null)
  const [pending, setPending] = useState(null)
  const held = agentHeld(agent)
  const block = presetBlock(form.agent_id, held, capabilities)
  const title = (name) => capabilities.find((c) => c.name === name)?.title || name
  const names = (list) => list.map(title).join(', ')
  const reasonId = 'cn-preset-reason'

  const usual = () => {
    const { caps, skipped } = usualFor(capabilities, held)
    const fresh = caps.filter((n) => !(form.caps || []).includes(n))
    setPending(null)
    setForm(withAddedCaps(form, caps))
    setDone({ kind: 'usual', text: [fresh.length ? s('perm.usualTicked', { count: fresh.length, names: names(fresh) }) : s('perm.usualNone'), skipped.length ? s('perm.usualSkipped', { names: names(skipped) }) : ''].filter(Boolean).join(' ') })
  }
  const copy = (caps) => {
    setPending(null)
    setForm(withCopiedCaps(form, caps))
    setDone({ kind: 'same', text: s('perm.sameTicked', { count: caps.length, agent: agent?.name || '', names: names(caps) }) })
  }
  const same = () => {
    const { caps, sensitive } = copyFromAgent(capabilities, held)
    if (sensitive.length > 0) { setDone(null); setPending({ caps, sensitive }); return }
    copy(caps)
  }

  return (
    <div className="cn-presets" data-testid="connector-presets">
      <div className="cn-presets__row">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-usual" disabled={Boolean(block)}
          aria-describedby={block ? reasonId : undefined} onClick={usual}>{s('perm.usualSet')}</button>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-same" disabled={Boolean(block)}
          aria-describedby={block ? reasonId : undefined} onClick={same}>{s('perm.sameAs')}</button>
      </div>
      {block ? <p className="dk-hint" id={reasonId} data-testid="connector-preset-reason">{s(`perm.block.${block}`)}</p>
        : <p className="dk-hint">{s('perm.usualSetHelp')} {s('perm.sameAsHelp')}</p>}
      {pending && (
        <Notice tone="warn" role="alertdialog" testId="connector-same-confirm">
          <p className="cn-notice__title">{s('perm.confirmTitle')}</p>
          <p className="cn-notice__text">{s('perm.confirmText')}</p>
          <ul className="cn-confirm__list">
            {pending.sensitive.map((n) => <li key={n}><strong>{title(n)}</strong>: {capabilities.find((c) => c.name === n)?.description}</li>)}
          </ul>
          <div className="cn-presets__row">
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="connector-same-yes" onClick={() => copy(pending.caps)}>{s('perm.confirmYes')}</button>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-same-no" onClick={() => setPending(null)}>{s('perm.confirmNo')}</button>
          </div>
        </Notice>
      )}
      {done && <p className="cn-presets__done" role="status" data-testid={`connector-preset-done-${done.kind}`}>{done.text}</p>}
    </div>
  )
}

/**
 * @param {object} props
 * @param {object} props.form       the form of the binding
 * @param {object} props.agent      the saved agent of the binding, when it is known
 * @param {Function} props.setForm
 * @param {object} props.kind       the kind from the kinds call (roles, permissions)
 * @param {Array} props.capabilities the closed list from the kinds call
 * @param {Function} props.s        the translation function with the defaults
 * @param {Function} props.onPaired called when pairing changed the binding
 */
export default function PermissionFields({ form, setForm, kind, capabilities, s, onPaired, agent, channelsOnly = false }) {
  const caps = capabilities || []
  if (caps.length === 0) return null
  const on = (name) => (form.caps || []).includes(name)
  const held = agentHeld(agent)
  // What the pairing list is now: the saved one, and what pairing here changes.
  const [paired, setPaired] = useState(form.paired || [])
  const pairedHere = useCallback((list) => { if (Array.isArray(list)) setPaired(list); onPaired?.() }, [onPaired])
  const stuck = unpairedSensitive(form, paired, caps)
  const title = (name) => caps.find((c) => c.name === name)?.title || name
  const toPairing = () => {
    const el = document.querySelector('[data-testid="connector-pair-start"]') || document.querySelector('[data-testid="connector-pairing"]')
    el?.scrollIntoView?.({ block: 'center' })
    el?.focus?.()
  }
  return (
    <fieldset className="cn-group" data-testid="connector-permissions">
      <legend>{s('perm.title')}</legend>
      <p className="dk-hint">{s('perm.help')}</p>
      <p className="dk-hint" data-testid="connector-rule">{s('perm.rule')}</p>
      {channelsOnly && (form.caps || []).length > 0 && (
        <Notice tone="warn" role="note" testId="connector-channel-only">
          <p className="cn-notice__title">{s('perm.channelOnlyTitle')}</p>
          <p className="cn-notice__text">{s('perm.channelOnlyText')}</p>
        </Notice>
      )}
      {hasNoCaps(form) && (
        <NoCapsNotice capabilities={caps} s={s} />
      )}
      {stuck.length > 0 && (
        <Notice tone="warn" role="note" testId="connector-stuck">
          <p className="cn-notice__title">{s('perm.stuckTitle')}</p>
          <p className="cn-notice__text">{s('perm.stuckText', { names: stuck.map(title).join(', ') })}</p>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-stuck-pair" onClick={toPairing}>{s('perm.stuckPair')}</button>
        </Notice>
      )}
      <Presets key={`${form.id || 'new'}:${form.agent_id || ''}`} form={form} setForm={setForm} capabilities={caps} agent={agent} s={s} />
      <ul className="cn-caps" aria-label={s('perm.capsLegend')}>
        {caps.map((c) => <Cap key={c.name} cap={c} on={on(c.name)} s={s}
          effect={c.name === 'triggers.propose' && form.approval ? approvalEffect(form.approval, s) : undefined} lacks={Boolean(form.agent_id) && held !== null && !agentHas(held, c.name)} onChange={(v) => setForm(setCap(form, c.name, v))}>
            {c.name === 'triggers.propose' && form.approval && <ApprovalOptions form={form} setForm={setForm} s={s} off={!on(c.name)} held={held} />}
          </Cap>)}
      </ul>
      <Roles form={form} setForm={setForm} kind={kind} capabilities={caps} s={s} />
      <Channels form={form} setForm={setForm} s={s} />
      <PairingFields binding={form} s={s} onPaired={pairedHere} />
    </fieldset>
  )
}
