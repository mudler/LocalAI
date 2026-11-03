// The change proposals of the chats (tool card change_proposal): a model asks
// to change something that lasts, the person confirms. One rule set for every
// kind (memory, profile, and the ones that come later) and every source (a
// Studio chat, a connector chat of any platform). Pure rules, so a node test
// drives them.

export const CHANGE_PROPOSAL_CARD = 'change_proposal'

// Sent on window when a card answers a proposal, so the counts read again.
export const CHANGES_EVENT = 'localai:changes'

// The ids of the change proposals in a run of transcript lines, in order.
export function changeProposalIds(items) {
  return (items || [])
    .filter((m) => m.role === 'tool' && m.card?.kind === CHANGE_PROPOSAL_CARD && m.card.id)
    .map((m) => m.card.id)
}

// What the card of a proposal shows, from the server's record (or the error
// of that call): pending, applied, dismissed, expired, reverted or stale as the
// server keeps it, removed on a 404 and error otherwise. A pending proposal
// whose time ran out counts as expired at once: the server settles it on the
// next read, and the card must not offer a button that cannot work.
export function changeProposalView(p, err, now = Date.now()) {
  if (err) return err.status === 404 ? { kind: 'removed' } : { kind: 'error', message: err.message }
  const known = ['pending', 'applied', 'dismissed', 'expired', 'reverted', 'stale']
  if (!known.includes(p?.status)) return { kind: 'error', message: `unknown change state ${p?.status}` }
  let kind = p.status
  if (kind === 'pending' && p.expires && Date.parse(p.expires) <= now) kind = 'expired'
  return { kind, proposal: p, canUndo: kind === 'applied' && !!p.rev }
}

// The key of the words that open the receipt of an applied change. A note that
// the host saved at once because the person chose so says "Saved to memory"; a
// change the person confirmed says "Applied". Both keep the Undo.
export function appliedReceiptKey(p) {
  return p?.auto ? 'changes.receipt.savedAuto' : 'changes.receipt.applied'
}

// Where a proposal came from, without naming a platform. kind '' is a chat
// the person watches; any other kind is the host's name for the origin (a
// connector), id names the binding and sender, when the host knows it, the
// person who wrote.
export function sourceOfProposal(p) {
  const s = p?.source || {}
  if (!s.kind) return { kind: 'chat', id: '', sender: '' }
  return { kind: s.kind, id: s.id || '', sender: s.sender || s.sender_name || '' }
}

// Whether the target and the scope are worth a line: a profile or an agent
// change names the agent.
export function scopeOfProposal(p) {
  return { scope: p?.scope || 'user', agent: p?.agent || '' }
}

// The path the proposal would write, inside the home, for the Files API.
export function targetPath(p) {
  const t = String(p?.target || '')
  return t.startsWith('/') ? t : `/${t}`
}

// The time left before a pending proposal expires: { hours, minutes } rounded
// up to the minute, or null once it is over.
export function timeLeft(p, now = Date.now()) {
  const ms = Date.parse(p?.expires || '') - now
  if (!Number.isFinite(ms) || ms <= 0) return null
  const total = Math.ceil(ms / 60000)
  return { hours: Math.floor(total / 60), minutes: total % 60 }
}

// A kind the card has no wording for is shown by its raw name.
export const KNOWN_KINDS = ['memory', 'profile', 'skill', 'page', 'dashboard']
export const KNOWN_SCOPES = ['user', 'agent', 'channel']

// A short, readable size.
export function byteText(n) {
  if (!Number.isFinite(n)) return ''
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KiB`
}
