// Helpers of the connectors page. No React, so a node test drives them.

export function emptyConnector() {
  return { id: '', name: '', token: '', agent_id: '', enabled: true, accept_any: false, allow: [], notify_chat: '' }
}

// The body of a create or replace request. The token goes only when it was typed:
// an update without one keeps the stored token.
export function connectorPayload(form) {
  const body = {
    name: (form.name || '').trim(),
    agent_id: form.agent_id || '',
    enabled: Boolean(form.enabled),
    accept_any: Boolean(form.accept_any),
    allow: form.accept_any ? [] : (form.allow || []).map(x => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i),
    notify_chat: (form.notify_chat || '').trim(),
  }
  const token = (form.token || '').trim()
  if (token) body.token = token
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
export function canSave(form, confirmed) {
  if (!(form.name || '').trim() || !form.agent_id) return false
  if (!form.id && !(form.token || '').trim()) return false
  if (form.accept_any && !confirmed) return false
  return true
}
