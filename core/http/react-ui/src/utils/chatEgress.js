// The network of a chat's Sandbox commands, as the chat page shows it. Pure
// functions over GET /api/chats/{id}/egress, so the node unit tests cover them.

export const PRESETS = ['off', 'registries', 'all']

const rank = (p) => Math.max(0, PRESETS.indexOf(p))

// Whether a preset is above the administrator's limit.
export function aboveCeiling(preset, ceiling) { return rank(preset) > rank(ceiling) }

// The three choices of the Network row: each with whether it is chosen and
// whether the limit blocks it.
export function presetChoices(egress) {
  const ceiling = egress?.ceiling || 'off'
  const chosen = egress?.preset || 'off'
  return PRESETS.map(p => ({ id: p, chosen: p === chosen, limited: aboveCeiling(p, ceiling) }))
}

// What the Tools menu shows: the row, a line saying the settings do not
// apply to commands on this machine, or nothing.
export function networkMode(egress) {
  if (!egress || !egress.loaded) return 'hidden'
  if (egress.applies) return 'row'
  return egress.applies_reason === 'host' ? 'host' : 'hidden'
}

export const hostPort = (c) => `${c.host}:${c.port}`

// The cards of a chat, newest first, with the pending ones before the others.
export function sortedCards(cards) {
  return [...(cards || [])].sort((a, b) => {
    const pa = a.state === 'pending' ? 0 : 1
    const pb = b.state === 'pending' ? 0 : 1
    if (pa !== pb) return pa - pb
    return String(b.asked_at || '').localeCompare(String(a.asked_at || ''))
  })
}

// The card to draw under a tool line: the pending card a live call waits on,
// or an answered card of that call. byTool maps a tool_seq to its cards.
export function cardsByTool(cards) {
  const out = new Map()
  for (const c of cards || []) {
    const k = c.tool_seq || 0
    if (!out.has(k)) out.set(k, [])
    out.get(k).push(c)
  }
  return out
}

// The i18n key of a card's one-line summary once it is answered.
export function cardStateKey(card) {
  switch (card?.state) {
    case 'once': return 'allowedOnce'
    case 'chat': return 'allowedChat'
    case 'denied': return 'denied'
    case 'expired': return 'expired'
    case 'refused': return 'refused'
  }
  return 'pending'
}

// The answers a person may give a card: a private target needs an
// administrator to allow it; anyone who owns the chat may deny. A card that
// expired with its turn may still be answered: the answer is kept for the
// next command (Allow once for ten minutes, the others as a rule).
export function cardAnswers(card, isAdmin) {
  if (!card || (card.state !== 'pending' && card.state !== 'expired')) return []
  // The limit does not admit the host: no answer can make it work.
  if (card.above_ceiling) return []
  if (card.private && !isAdmin) return ['deny']
  return ['once', 'chat', 'deny']
}

// What the limit set by the administrator means for the chat: 'off' when it
// allows no network at all, 'capped' when it allows less than all public
// sites, '' when it does not limit anything.
export function ceilingKind(egress) {
  const c = egress?.ceiling || 'off'
  if (c === 'off') return 'off'
  return c === 'all' ? '' : 'capped'
}

// Where an administrator changes the limit: Settings > Tools, the Sandbox
// network section.
export const SANDBOX_NETWORK_SETTINGS = '/app/settings?group=tools'
