// Helpers of the connector pages of the add-ons (Telegram, Slack). No React,
// so a node test drives them.

// The modes a binding answers with. The server has the same defaults.
export const REPLY_MODES = ['off', 'progress', 'stream']
export const CONVERSATIONS = ['sender', 'chat', 'window', 'thread']
// The language of the sentences the bot writes itself (auto follows the sender).
export const REPLY_LANGUAGES = ['auto', 'en', 'it', 'de', 'es', 'fr', 'pt-BR']
// The host answers with English text and, when the request names a language it
// translates, localized_* fields beside it, found by code. These two fold the
// localized fields into the plain ones, so the rest of the page reads one name.
// A field the host did not translate keeps its English text.
export function localizeCapabilities(list) {
  return (list || []).map((c) => ({ ...c, title: c.localized_title || c.title, description: c.localized_description || c.description }))
}

export function localizeAccess(view) {
  if (!view || !Array.isArray(view.subjects)) return view
  return {
    ...view,
    tool_outcomes: (view.tool_outcomes || []).map((e) => (e.localized_message ? { ...e, message: e.localized_message } : e)),
    subjects: view.subjects.map((subject) => ({
      ...subject,
      decisions: (subject.decisions || []).map((d) => (d.localized_message ? { ...d, message: d.localized_message } : d)),
    })),
  }
}

// Two enabled connections that are the same bot answer every message twice.
// The host marks each of them with `duplicate` ({ code, message, others }); the
// page only shows it. Saving one such connection first gets a 409 of type
// duplicate_connection, and the same request with the confirmation goes through.
export const DUPLICATE_CONFIRMATION = 'answer twice'
export const duplicateOther = (c) => c?.duplicate?.others?.[0] || null
export const isDuplicateRefusal = (err) => err?.status === 409 && err.body?.error?.type === 'duplicate_connection'
export const duplicateRefusalName = (err) => (typeof err?.body?.error?.param === 'string' ? err.body.error.param : '')

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

// The acts a run of a trigger proposed from a chat may be allowed to do. The
// names are also the grants of the agent, which must hold the same one. Every
// one is sensitive.
export const APPROVAL_ACTS = ['act.files', 'act.addons', 'act.send', 'act.spend']

export function emptyApproval() {
  return { mode: 'ask', acts: [], max_pending: '', min_gap_minutes: '', max_runs_per_day: '', max_lifetime_days: '' }
}

// Whether the agent holds the grant behind an act. held is agentHeld(agent);
// null (permissions unreadable) gates nothing.
export const agentHoldsAct = (held, act) => held === null || held === undefined || held.includes(act)

// The approval of a saved binding as a form: a limit of zero is the default
// and shows empty.
export function approvalForm(a) {
  const f = { ...emptyApproval(), mode: a?.mode || 'ask', acts: Array.isArray(a?.acts) ? [...a.acts] : [] }
  for (const { key } of APPROVAL_LIMITS) f[key] = a?.[key] > 0 ? String(a[key]) : ''
  return f
}

// Why a typed limit cannot be saved: not a whole number from 1 to its bound.
// Empty is fine (the default).
export function approvalProblem(a) {
  if (!a) return ''
  if (!APPROVAL_MODES.includes(a.mode)) return 'mode'
  if ((a.acts || []).some((n) => !APPROVAL_ACTS.includes(n))) return 'acts'
  for (const { key, max } of APPROVAL_LIMITS) {
    const v = String(a[key] ?? '').trim()
    if (v === '') continue
    const n = Number(v)
    if (!Number.isInteger(n) || n < 1 || n > max) return key
  }
  return ''
}

export function approvalPayload(a) {
  // The whole approval is sent each time (the server replaces it), acts too;
  // an empty list means runs only read.
  const body = { mode: APPROVAL_MODES.includes(a?.mode) ? a.mode : 'ask', acts: APPROVAL_ACTS.filter((n) => (a?.acts || []).includes(n)) }
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

// The caps of the usual set: the ones the host marks usual,
// never a sensitive one, whatever the list says.
export function usualCaps(capabilities) {
  return (capabilities || []).filter((c) => c.usual && !c.sensitive).map((c) => c.name)
}

// The binding capabilities that are also grants of a saved agent. A sender can
// only use one of them when the agent holds the grant too, so the presets only
// tick what the agent holds. proposals.confirm, tools.write, loops.wait and
// loops.start are not grants: they belong to the binding alone.
export const AGENT_GRANT_CAPS = [
  'memory.write', 'profile.write', 'skills.write', 'pages.write', 'dashboard.write', 'knowledge.write',
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

// The usual set: the caps the host marks usual (never a sensitive one)
// that the agent holds. skipped are the usual ones the agent does not hold.
export function usualFor(capabilities, held) {
  const usual = usualCaps(capabilities)
  return { caps: usual.filter((n) => agentHas(held, n)), skipped: usual.filter((n) => !agentHas(held, n)) }
}

// The form with the caps reset to the usual set for the agent, which is what a
// new connection starts with. A role never keeps a cap the ceiling lost.
export function withUsualCaps(form, capabilities, held) {
  const caps = usualFor(capabilities, held).caps
  const grants = (form.grants || []).map((g) => ({ ...g, caps: g.caps.filter((x) => caps.includes(x)) })).filter((g) => g.caps.length > 0)
  return { ...form, caps, grants }
}

// Whether the allowlist holds channel entries only: members of a channel are not
// trusted one by one, so no person can use a ticked permission through it. Only
// a page with a list of people before its other lists can tell (Slack).
export function channelOnly(form, lists) {
  if (!Array.isArray(lists) || lists.length < 2 || form?.accept_any) return false
  const allow = form?.allow || []
  return allow.length > 0 && listEntries(allow, lists, 0).length === 0 && !allow.some((x) => trustOf(form, x) === 'act')
}

// The trust level of an allowlist entry that is a channel: 'act' (everyone who
// writes there may use the actions that are not sensitive) or 'answer' (the
// bot answers there, but its members are not trusted one by one). An entry with
// no level, such as one stored before the setting, is 'answer'.
export function trustOf(form, entry) {
  return form?.allow_trust?.[entry] === 'act' ? 'act' : 'answer'
}

// The levels the request carries: only for entries that are on the allowlist.
function trustPayload(form) {
  if (form.accept_any) return {}
  const allow = new Set((form.allow || []).map((x) => x.trim()))
  return Object.fromEntries(Object.entries(form.allow_trust || {}).filter(([k, v]) => allow.has(k) && (v === 'act' || v === 'answer')))
}

// The channel entries of the allowlist, in the order of the list. isChannel
// tells whether an entry names a channel or group (a kind knows its IDs).
export function channelEntries(form, isChannel) {
  return (form?.allow || []).filter((x) => isChannel(x))
}

// The levels after the allowlist changed to allow: an entry that is new and is a
// channel starts as 'act' (what the owner expects from allowing a channel), and
// the level of an entry that left the list is dropped. The levels of the
// entries that stayed do not change, so a channel stored before the setting
// stays 'answer' until the owner changes it.
export function trustAfter(form, allow, isChannel) {
  const out = {}
  for (const x of allow) {
    if (form.allow_trust?.[x]) out[x] = form.allow_trust[x]
    else if (!(form.allow || []).includes(x) && isChannel(x)) out[x] = 'act'
  }
  return out
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

// The form of a new binding. With the host's capability list it starts with the
// usual permission set ticked for what the agent holds (held is agentHeld(agent),
// null when no agent is chosen yet); without it, with no permission on. Only the
// non-sensitive usual ones are ever ticked, and the restrictions stay off.
export function emptyConnector(capabilities, held = null) {
  return {
    id: '', name: '', token: '', agent_id: '', enabled: true, accept_any: false, allow: [], allow_trust: {}, notify_chat: '',
    reply_mode: 'progress', conversation: 'sender', window_minutes: 60, send_files: true, reply_language: 'auto',
    options: {},
    file_receive_mib: '', file_send_mib: '',
    approval: emptyApproval(),
    caps: capabilities ? usualFor(capabilities, held).caps : [], grants: [], channels: [],
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
    approval: approvalForm(c.approval), allow_trust: { ...(c.allow_trust || {}) },
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
    allow_trust: trustPayload(form),
    notify_chat: (form.notify_chat || '').trim(),
    reply_mode: form.reply_mode || 'progress',
    conversation: form.conversation || 'sender',
    send_files: form.send_files !== false,
    reply_language: REPLY_LANGUAGES.includes(form.reply_language) ? form.reply_language : 'auto',
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

// The language of the page, for dates: the web UI keeps it on the document, so
// a date reads in the language of the words around it. [] is the browser's.
export function pageLocale() {
  const lang = typeof document !== 'undefined' ? document.documentElement?.lang : ''
  return lang || []
}

// A sender of the log as a short line: name, how many messages, and the id.
// With a translator t, the words follow the language of the page.
export function senderLine(s, t) {
  const name = s.name || s.id
  if (typeof t === 'function') return t('sender.line', { name, count: s.count, id: s.id })
  return `${name} · ${s.count} message${s.count === 1 ? '' : 's'} · ${s.id}`
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

// The listening list and the places the bot may post are two settings. These
// helpers put them side by side on the page without changing what is saved
// (channels[] with approved).

// The channels of the listening list, as IDs. A list that mixes people and
// channels (a kind that cannot tell them apart) and "anyone" give none: the
// page cannot tell which entries are channels.
export function listeningChannels(form, isChannel, mixed) {
  if (mixed || form.accept_any) return []
  return (form.allow || []).filter((x) => isChannel(x))
}

// The place of a channel ID, or null.
export const placeFor = (form, id) => (form.channels || []).find((c) => c.id === id) || null

// The listening channels that are not a place yet.
export const offeredPlaces = (form, listening) => listening.filter((id) => !placeFor(form, id))

// The form with the channel as a place, with the safe default: only a verified
// person can make the bot post there. A channel that is a place already stays.
export function withPlace(form, id, label = '') {
  if (!id || placeFor(form, id)) return form
  return { ...form, channels: [...(form.channels || []), { id, label, approved: false }] }
}
