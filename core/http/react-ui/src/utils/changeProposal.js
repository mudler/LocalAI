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
  return { kind, proposal: p, canUndo: kind === 'applied' && (!!p.rev || writesElsewhere(p)) }
}

// A knowledge change adds an entry to a collection instead of writing a file of
// the home, so it has no change set (rev stays 0) and its target is the name
// of the collection, not a path. Undo removes the entry.
export const writesElsewhere = (p) => p?.kind === 'knowledge'

// The most a knowledge text may hold, as the server bounds it
// (contract.MaxKnowledgeBytes).
export const KNOWLEDGE_MAX_BYTES = 64 * 1024

// What the server stores for a knowledge change: the summary as a title, then
// the text (see knowledgeEntryText in core/services/studio/knowledge.go).
export const knowledgeEntryText = (p) => `# ${p?.summary || ''}\n\n${String(p?.content || '').trim()}\n`

// How many passages a text becomes when it is added to a collection, and so how
// many are embedded. The engine cuts a text at word boundaries into passages of
// at most `size` characters that share `overlap` characters, so this is an
// estimate: a text that fits in one passage is exactly one, a longer one is
// about length / (size - overlap). The defaults are the ones of the server.
export function estimatePassages(text, size = 400, overlap = 0) {
  const bytes = new TextEncoder().encode(String(text || '')).length
  const max = Number.isFinite(size) && size > 0 ? size : 400
  if (bytes <= max) return 1
  const ov = Number.isFinite(overlap) && overlap > 0 ? Math.min(overlap, max - 1) : 0
  return Math.ceil((bytes - ov) / (max - ov))
}

// The first lines of a text, for the preview of a knowledge change.
export function previewLines(text, max = 20) {
  const lines = String(text || '').replace(/\s+$/, '').split('\n')
  return { shown: lines.slice(0, max), more: Math.max(0, lines.length - max) }
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
export const KNOWN_KINDS = ['memory', 'profile', 'skill', 'page', 'dashboard', 'knowledge']
export const KNOWN_SCOPES = ['user', 'agent', 'channel']

// A short, readable size.
export function byteText(n) {
  if (!Number.isFinite(n)) return ''
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KiB`
}
