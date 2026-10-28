// Helpers of the connector pages of the add-ons (Telegram, Slack). No React,
// so a node test drives them.

export function emptyConnector() {
  return { id: '', name: '', token: '', agent_id: '', enabled: true, accept_any: false, allow: [], notify_chat: '' }
}

// The body of a create or replace request for a binding of kind. token is the
// secret the add-on's fields make; it goes only when it was typed: an update
// without one keeps the stored token.
export function connectorPayload(form, kind, token = form.token) {
  const body = {
    kind,
    name: (form.name || '').trim(),
    agent_id: form.agent_id || '',
    enabled: Boolean(form.enabled),
    accept_any: Boolean(form.accept_any),
    allow: form.accept_any ? [] : (form.allow || []).map(x => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i),
    notify_chat: (form.notify_chat || '').trim(),
  }
  const secret = (token || '').trim()
  if (secret) body.token = secret
  return body
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
export function canSave(form, confirmed, hasToken = Boolean((form.token || '').trim())) {
  if (!(form.name || '').trim() || !form.agent_id) return false
  if (!form.id && !hasToken) return false
  if (form.accept_any && !confirmed) return false
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
