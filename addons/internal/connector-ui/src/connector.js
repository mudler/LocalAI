// Helpers of the connector pages of the add-ons (Telegram, Slack). No React,
// so a node test drives them.

// The modes a binding answers with. The server has the same defaults.
export const REPLY_MODES = ['off', 'progress', 'stream']
export const CONVERSATIONS = ['sender', 'chat', 'window', 'thread']
export const MAX_WINDOW_MINUTES = 7 * 24 * 60

// How the triggers that the agent proposes in a chat are approved, and the
// limits of the two modes that make one without asking. The server has the
// same names, defaults and bounds (connectors.Approval).
export const APPROVAL_MODES = ['ask', 'auto', 'auto_readonly']
export const APPROVAL_LIMITS = [
  { key: 'max_pending', max: 50, fallback: 5 },
  { key: 'min_gap_minutes', max: 1440, fallback: 60 },
  { key: 'max_runs_per_day', max: 200, fallback: 10 },
  { key: 'max_lifetime_days', max: 365, fallback: 30 },
]

export function emptyApproval() {
  return { mode: 'ask', max_pending: '', min_gap_minutes: '', max_runs_per_day: '', max_lifetime_days: '' }
}

// The approval of a saved binding as a form: a limit of zero is the default
// and shows empty.
export function approvalForm(a) {
  const f = { ...emptyApproval(), mode: a?.mode || 'ask' }
  for (const { key } of APPROVAL_LIMITS) f[key] = a?.[key] > 0 ? String(a[key]) : ''
  return f
}

// Why a typed limit cannot be saved: not a whole number from 1 to its bound.
// Empty is fine (the default).
export function approvalProblem(a) {
  if (!a) return ''
  if (!APPROVAL_MODES.includes(a.mode)) return 'mode'
  for (const { key, max } of APPROVAL_LIMITS) {
    const v = String(a[key] ?? '').trim()
    if (v === '') continue
    const n = Number(v)
    if (!Number.isInteger(n) || n < 1 || n > max) return key
  }
  return ''
}

export function approvalPayload(a) {
  const body = { mode: APPROVAL_MODES.includes(a?.mode) ? a.mode : 'ask' }
  for (const { key } of APPROVAL_LIMITS) body[key] = Number(String(a?.[key] ?? '').trim()) || 0
  return body
}

// What a binding lets its senders do. The host owns the closed list of
// capabilities (the kinds call returns it with a title and a description of
// each); a sensitive one is never granted to a role, and the host keeps the
// same limits.
export const MAX_CHANNELS = 10
export const MAX_CHANNEL_ID = 64
export const MAX_CHANNEL_LABEL = 60

// A binding with no caps denies every gated feature: files from senders,
// triggers, memory writes and talk-back. A binding saved before the host
// enforced permissions has none.
export function hasNoCaps(c) {
  return !(c?.caps || []).length
}

// The caps that the one-click preset turns on: the ones the host marks usual,
// never a sensitive one, whatever the list says.
export function usualCaps(capabilities) {
  return (capabilities || []).filter((c) => c.usual && !c.sensitive).map((c) => c.name)
}

// The binding capabilities that are also grants of a saved agent. A sender can
// only use one of them when the agent holds the grant too, so the presets only
// tick what the agent holds. proposals.confirm and tools.write are not grants:
// they belong to the binding alone.
export const AGENT_GRANT_CAPS = [
  'memory.write', 'profile.write', 'skills.write', 'pages.write', 'dashboard.write',
  'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel',
]

// What an agent without a grant list holds for its messaging connections (the
// server rule is core/services/studio/connectorgrants.go). As soon as the list
// names any grant, only the named ones count.
export const DEFAULT_CONNECTOR_GRANTS = ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload']

// The grants the agent holds for a connection, or null when its permissions
// cannot be read (no agent, an agent that is not in the list, a host that does
// not send them).
export function agentHeld(agent) {
  if (!agent || !Array.isArray(agent.builtins)) return null
  return agent.builtins.length > 0 ? agent.builtins : DEFAULT_CONNECTOR_GRANTS
}

// Whether the agent lets a capability be switched on: a capability that is not
// a grant of the agent is not gated. held is agentHeld(agent); null gates nothing.
export const agentHas = (held, name) => held === null || !AGENT_GRANT_CAPS.includes(name) || held.includes(name)

// Why a preset cannot be used: 'noAgent', 'unreadable', 'nothing' or ''.
export function presetBlock(agentId, held, capabilities) {
  if (!agentId) return 'noAgent'
  if (held === null) return 'unreadable'
  if (!(capabilities || []).some((c) => AGENT_GRANT_CAPS.includes(c.name) && held.includes(c.name))) return 'nothing'
  return ''
}

// "Allow the usual set": the caps the host marks usual (never a sensitive one)
// that the agent holds. skipped are the usual ones the agent does not hold.
export function usualFor(capabilities, held) {
  const usual = usualCaps(capabilities)
  return { caps: usual.filter((n) => agentHas(held, n)), skipped: usual.filter((n) => !agentHas(held, n)) }
}

// "Same as the agent": every cap the agent holds that is a capability of the
// host's list, sensitive ones too. sensitive names the ones that are.
export function copyFromAgent(capabilities, held) {
  const list = (capabilities || []).filter((c) => AGENT_GRANT_CAPS.includes(c.name) && (held || []).includes(c.name))
  return { caps: list.map((c) => c.name), sensitive: list.filter((c) => c.sensitive).map((c) => c.name) }
}

// The form after the caps of the agent were copied in. The caps that are not
// grants of an agent stay as they were; the others become exactly the copy, and
// a role never keeps a cap the ceiling lost.
export function withCopiedCaps(form, caps) {
  const keep = (form.caps || []).filter((n) => !AGENT_GRANT_CAPS.includes(n))
  const next = [...new Set([...keep, ...caps])]
  const grants = (form.grants || []).map((g) => ({ ...g, caps: g.caps.filter((x) => next.includes(x)) })).filter((g) => g.caps.length > 0)
  return { ...form, caps: next, grants }
}

// The form with these caps added to the ones it has.
export function withAddedCaps(form, caps) {
  return { ...form, caps: [...new Set([...(form.caps || []), ...caps])] }
}

// The ticked caps that no sender can use yet. The host lets a sender use a cap
// only when the sender is paired, or has a role the owner granted the cap to
// (a sensitive cap needs a pairing always; see Authorize in
// core/services/studio/connectoraccess.go). So with nobody paired, a ticked
// cap that no role holds is dead, however it is ticked.
export function unusableCaps(form, paired, now = Date.now()) {
  const caps = form?.caps || []
  const active = (paired || []).some((p) => !p.expires_at || new Date(p.expires_at).getTime() > now)
  if (caps.length === 0 || active) return []
  const granted = new Set((form.grants || []).flatMap((g) => g.caps || []))
  return caps.filter((c) => !granted.has(c))
}

// The form after cap name was turned on or off. Turning one off also takes it
// from every role grant, so a role never holds more than the ceiling.
export function setCap(form, name, on) {
  const caps = (form.caps || []).filter((x) => x !== name)
  if (on) caps.push(name)
  const grants = (form.grants || [])
    .map((g) => ({ ...g, caps: g.caps.filter((x) => caps.includes(x)) }))
    .filter((g) => g.caps.length > 0)
  return { ...form, caps, grants }
}

export function roleHas(form, role, cap) {
  return (form.grants || []).some((g) => g.role === role && g.caps.includes(cap))
}

// The caps that a kind can give to a role: listed by the kind, not sensitive,
// and inside the ceiling the owner enabled.
export function grantableFor(kind, caps) {
  return (kind?.permissions || []).filter((p) => !p.sensitive && (caps || []).includes(p.name)).map((p) => p.name)
}

// The form after role was given or refused cap. A sensitive cap, or one that
// is not in the ceiling, is refused: the form is returned as it was. The host's
// list says which caps are sensitive.
export function setGrant(form, role, cap, on, capabilities = []) {
  const sensitive = capabilities.some((c) => c.name === cap && c.sensitive)
  if (on && (sensitive || !(form.caps || []).includes(cap))) return form
  const rest = (form.grants || []).filter((g) => g.role !== role)
  const mine = (form.grants || []).find((g) => g.role === role)?.caps || []
  const caps = on ? [...mine.filter((x) => x !== cap), cap] : mine.filter((x) => x !== cap)
  return { ...form, grants: caps.length ? [...rest, { role, caps }] : rest }
}

// Why the list of channels cannot be saved: 'count', 'id', 'label' or
// 'repeat'; else ''. The server has the same limits.
export function channelsProblem(list) {
  const channels = list || []
  if (channels.length > MAX_CHANNELS) return 'count'
  for (let i = 0; i < channels.length; i++) {
    const { id = '', label = '' } = channels[i]
    if (!id || new TextEncoder().encode(id).length > MAX_CHANNEL_ID || /\s/u.test(id)) return 'id'
    if ([...label].length > MAX_CHANNEL_LABEL) return 'label'
    if (channels.slice(0, i).some((o) => o.id === id)) return 'repeat'
  }
  return ''
}

export function emptyConnector() {
  return {
    id: '', name: '', token: '', agent_id: '', enabled: true, accept_any: false, allow: [], notify_chat: '',
    reply_mode: 'progress', conversation: 'sender', window_minutes: 60, send_files: true,
    options: {},
    file_receive_mib: '', file_send_mib: '',
    approval: emptyApproval(),
    caps: [], grants: [], channels: [],
  }
}

const MIB = 1024 * 1024

// The file limits of a binding are typed in MiB and stored in bytes. Empty
// means no limit of the owner: the limit of the kind applies.
export function bytesToMib(bytes) {
  return bytes > 0 ? String(bytes / MIB) : '' // a power of two divides exactly, so a saved limit reads back unchanged
}

export function mibToBytes(mib) {
  const n = Number(String(mib ?? '').trim())
  return String(mib ?? '').trim() === '' || !Number.isFinite(n) ? 0 : Math.round(n * MIB)
}

// The form of a saved binding, with its limits in MiB and no token.
export function connectorForm(c) {
  return {
    ...emptyConnector(), ...c, token: '',
    file_receive_mib: bytesToMib(c.file_receive_max_bytes), file_send_mib: bytesToMib(c.file_send_max_bytes),
    approval: approvalForm(c.approval),
    caps: [...(c.caps || [])], grants: (c.grants || []).map((g) => ({ role: g.role, caps: [...g.caps] })),
    channels: (c.channels || []).map((x) => ({ id: x.id, label: x.label || '', approved: Boolean(x.approved) })),
  }
}

// Why a typed limit cannot be saved: 'invalid' (not a positive number) or
// 'tooHigh' (above what the kind allows), else ''. allowedBytes may be 0 when
// the page does not know it; the server checks then.
export function fileLimitProblem(mib, allowedBytes) {
  if (String(mib ?? '').trim() === '') return ''
  const n = Number(mib)
  if (!Number.isFinite(n) || n <= 0 || mibToBytes(mib) < 1) return 'invalid'
  return allowedBytes > 0 && mibToBytes(mib) > allowedBytes ? 'tooHigh' : ''
}

// The limit that a typed value gives, for the line "now" under the field. base
// is the limit of the kind without a limit of the owner (the server's number),
// so a typed value can only lower it.
export function previewLimit(base, mib) {
  const bytes = mibToBytes(mib)
  if (!base) return null
  if (bytes > 0 && bytes <= base.effective_bytes) return { bytes, layer: 'owner' }
  return { bytes: base.effective_bytes, layer: base.layer }
}

// What the add-on declares for one direction, from the limits of the kind:
// 'off' (it moves no file that way), 'declared' (it names a largest file),
// 'open' (it names none, so the platform limit applies) or 'unknown' (the page
// has no numbers yet).
export function fileDirection(base) {
  if (!base) return 'unknown'
  if (!(base.effective_bytes > 0) && !(base.allowed_bytes > 0)) return 'off'
  return base.declared_bytes > 0 ? 'declared' : 'open'
}

// A size as a person reads it: bytes, KiB, MiB, GiB or TiB, one decimal at most.
export function formatBytes(n) {
  if (n === 1) return '1 byte'
  if (n < 1024) return `${n} bytes`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${Number(v.toFixed(1))} ${units[i]}`
}

// The body of a create or replace request for a binding of kind. token is the
// secret the add-on's fields make; it goes only when it was typed: an update
// without one keeps the stored token.
export function connectorPayload(form, kind, token = form.token, extra = {}) {
  const body = {
    kind,
    name: (form.name || '').trim(),
    agent_id: form.agent_id || '',
    enabled: Boolean(form.enabled),
    accept_any: Boolean(form.accept_any),
    allow: form.accept_any ? [] : (form.allow || []).map(x => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i),
    notify_chat: (form.notify_chat || '').trim(),
    reply_mode: form.reply_mode || 'progress',
    conversation: form.conversation || 'sender',
    send_files: form.send_files !== false,
    // The choices for the options of the kind; a missing one has its default.
    options: Object.fromEntries(Object.entries(form.options || {}).filter(([, v]) => v)),
    file_receive_max_bytes: mibToBytes(form.file_receive_mib),
    file_send_max_bytes: mibToBytes(form.file_send_mib),
    approval: approvalPayload(form.approval),
    caps: [...(form.caps || [])],
    grants: (form.grants || []).map((g) => ({ role: g.role, caps: [...g.caps] })),
    channels: (form.channels || []).map((x) => ({ id: x.id.trim(), label: (x.label || '').trim(), approved: Boolean(x.approved) })),
  }
  if (body.conversation === 'window') body.window_minutes = Number(form.window_minutes) || 0
  const secret = (token || '').trim()
  if (secret) body.token = secret
  // Fields of the add-on's own (a list of resources) and overrides, such as a
  // binding that carries no messages and so is never switched on.
  return { ...body, ...extra }
}

// Who the binding accepts, in a few words.
export function modeText(c) {
  if (c.accept_any) return 'anyone'
  const n = (c.allow || []).length
  return n === 0 ? 'nobody yet' : `${n} listed sender${n === 1 ? '' : 's'}`
}

// A sender of the log as a short line: name, how many messages, and the id.
export function senderLine(s) {
  return `${s.name || s.id} · ${s.count} message${s.count === 1 ? '' : 's'} · ${s.id}`
}

// "Accept any message" is only saved after the person confirms the warning.
// hasToken says whether the add-on's fields hold a complete secret.
export function canSave(form, confirmed, hasToken = Boolean((form.token || '').trim()), files = null, agentOptional = false) {
  if (!(form.name || '').trim() || (!form.agent_id && !agentOptional)) return false
  if (!form.id && !hasToken) return false
  if (form.accept_any && !confirmed) return false
  if (approvalProblem(form.approval)) return false
  if (channelsProblem(form.channels)) return false
  if (fileLimitProblem(form.file_receive_mib, files?.receive?.allowed_bytes) || fileLimitProblem(form.file_send_mib, files?.send?.allowed_bytes)) return false
  if (form.conversation === 'window') {
    const m = Number(form.window_minutes)
    if (!Number.isInteger(m) || m < 1 || m > MAX_WINDOW_MINUTES) return false
  }
  return true
}

// The entries of the allowlist that one of several lists shows. Each list
// has a match function; the last list takes what no other list matches.
export function listEntries(allow, lists, index) {
  const own = (x) => {
    const first = lists.findIndex((l) => l.match && l.match(x))
    return first === -1 ? index === lists.length - 1 : first === index
  }
  return (allow || []).filter(own)
}

// The allowlist after list index changed to values.
export function replaceList(allow, lists, index, values) {
  const others = lists.flatMap((_, i) => (i === index ? [] : listEntries(allow, lists, i)))
  return [...others, ...values]
}
