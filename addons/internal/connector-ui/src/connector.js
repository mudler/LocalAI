// Helpers of the connector pages of the add-ons (Telegram, Slack). No React,
// so a node test drives them.

// The modes a binding answers with. The server has the same defaults.
export const REPLY_MODES = ['off', 'progress', 'stream']
export const CONVERSATIONS = ['sender', 'chat', 'window', 'thread']
export const MAX_WINDOW_MINUTES = 7 * 24 * 60

export function emptyConnector() {
  return {
    id: '', name: '', token: '', agent_id: '', enabled: true, accept_any: false, allow: [], notify_chat: '',
    reply_mode: 'progress', conversation: 'sender', window_minutes: 60, send_files: true,
    options: {},
    file_receive_mib: '', file_send_mib: '',
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
export function canSave(form, confirmed, hasToken = Boolean((form.token || '').trim()), files = null) {
  if (!(form.name || '').trim() || !form.agent_id) return false
  if (!form.id && !hasToken) return false
  if (form.accept_any && !confirmed) return false
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
