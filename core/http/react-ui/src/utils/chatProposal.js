// The trigger proposals a model makes in a chat (tool card trigger_proposal).
// Confirming one keeps its id: the trigger that results has the same id, and
// the server keeps the answer, so a reload shows the same receipt. Pure
// rules, so a node test drives them.

export const PROPOSAL_CARD = 'trigger_proposal'

// What the card of a proposal shows, from the server's answer to
// GET /api/chats/triggers/proposals/{id} (or the error that call raised): one of
// pending, accepted, declined or undone, as the server keeps it, removed when
// the trigger is gone from the Triggers page (404) and error otherwise. An
// accepted proposal says whether Undo is still allowed and, if not, why
// ('ran' or 'edited'). A state this code does not know is an error, not a guess.
export function proposalView(res, err) {
  if (err) return err.status === 404 ? { kind: 'removed' } : { kind: 'error', message: err.message }
  switch (res?.state) {
    case 'pending':
    case 'declined':
    case 'undone':
      return { kind: res.state, trigger: res.trigger, at: res.answered_at || '' }
    case 'accepted':
      return { kind: 'accepted', trigger: res.trigger, at: res.answered_at || '', canUndo: !!res.can_undo, blocked: res.undo_blocked || '' }
    default:
      return { kind: 'error', message: `unknown proposal state ${res?.state}` }
  }
}

// When the trigger runs, as the receipt says it: the server's plain summary
// ("Every Monday at 09:00 (Europe/Rome)", "new files in /Contracts"), and
// whether it watches the clock or a folder.
export function receiptWhen(t) {
  return { mode: t?.kind === 'file' ? 'file' : 'schedule', text: String(t?.summary || t?.schedule || t?.folder || '') }
}

// The body of PUT /api/chats/triggers/proposals/{id}: the server replaces every
// field, so the ones the card does not edit are sent back as they are.
export function proposalEdit(p, { name, when }) {
  const body = {
    name: name.trim(),
    prompt: p.prompt,
    kind: p.kind,
    enabled: false,
    daily_cap: p.daily_cap || 0,
  }
  if (p.kind === 'file') {
    body.folder = when.trim()
    if (p.quiet_seconds) body.quiet_seconds = p.quiet_seconds
  } else {
    body.schedule = when.trim()
    if (p.time_zone) body.time_zone = p.time_zone
  }
  for (const k of ['agent_id', 'model', 'source_chat_id']) if (p[k]) body[k] = p[k]
  return body
}

// The ids of the proposals in a run of transcript lines.
export function proposalIds(items) {
  return (items || []).filter((m) => m.role === 'tool' && m.card?.kind === PROPOSAL_CARD && m.card.id).map((m) => m.card.id)
}

// The changes of a trigger that a model asks for in a chat (tool card
// trigger_change): an edit or a delete waits for the person, a pause or a
// resume is applied at once and the card is a receipt with Undo.
export const CHANGE_CARD = 'trigger_change'

// What the card of a change shows, from the server's answer to
// GET /api/chats/triggers/changes/{id} (or the error of that call): pending,
// applied, dismissed or undone, removed once the server no longer keeps it
// (404), and error otherwise. A state this code does not know is an error.
export function changeView(res, err) {
  if (err) return err.status === 404 ? { kind: 'removed' } : { kind: 'error', message: err.message }
  const known = ['pending', 'applied', 'dismissed', 'undone']
  if (!known.includes(res?.state)) return { kind: 'error', message: `unknown change state ${res?.state}` }
  return {
    kind: res.state,
    op: res.op,
    before: res.before || {},
    after: res.after || null,
    fields: res.fields || [],
    at: res.answered_at || '',
    stale: !!res.stale,
    canUndo: !!res.can_undo,
    blocked: res.undo_blocked || '',
    lastRun: res.last_run || null,
  }
}

// The text of one field of a trigger, as the before/after rows show it. A
// field the trigger leaves empty is ''; the card says "none" for it.
export function changeValue(field, t) {
  if (!t) return ''
  switch (field) {
    case 'when': return String(t.summary || t.schedule || t.folder || '')
    case 'enabled': return t.enabled ? 'on' : 'off'
    case 'reads': return t.plan?.reads || ''
    case 'writes': return t.plan?.writes || ''
    case 'widget': return t.plan?.widget || ''
    case 'estimated_tokens': return t.cost?.estimated_tokens ? String(t.cost.estimated_tokens) : ''
    case 'agent': return t.agent_id || ''
    case 'daily_cap': return t.daily_cap ? String(t.daily_cap) : ''
    default: return t[field] == null ? '' : String(t[field])
  }
}

// The trigger cards of a run of transcript lines, in order: proposals and
// changes, each { kind, id }.
export function triggerCards(items) {
  return (items || [])
    .filter((m) => m.role === 'tool' && (m.card?.kind === PROPOSAL_CARD || m.card?.kind === CHANGE_CARD) && m.card.id)
    .map((m) => ({ kind: m.card.kind, id: m.card.id }))
}
