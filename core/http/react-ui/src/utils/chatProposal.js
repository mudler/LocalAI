// The trigger proposals a model makes in a chat (tool card trigger_proposal).
// Confirming one keeps its id: the trigger that results has the same id. Pure
// rules, so a node test drives them.

export const PROPOSAL_CARD = 'trigger_proposal'

// What the card of the proposal with this id shows, from the proposals and the
// triggers of the user: { kind: 'proposed', proposal }, { kind: 'created',
// trigger } once confirmed, or { kind: 'gone' } once dismissed.
export function proposalState(id, proposals, triggers) {
  const p = (proposals || []).find((x) => x.id === id)
  if (p) return { kind: 'proposed', proposal: p }
  const tr = (triggers || []).find((x) => x.id === id)
  if (tr) return { kind: 'created', trigger: tr }
  return { kind: 'gone' }
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
