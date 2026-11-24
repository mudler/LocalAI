import { natsOffered, natsState } from './clusterSettings.js'
import { weightsInForce } from './connectionWeights.js'

// What the Add worker flow decides, apart from drawing it. No React here, so
// the unit tests read it.

// The ways a worker can connect, in the order the cards show them.
export const WAYS = ['nats', 'websocket', 'p2p']

// The ways a join token carries: a token limits the connections of the worker
// to nats and websocket. P2P workers join with an invite link instead.
export const TOKEN_WAYS = ['nats', 'websocket']

// What each way offers right now, from the cluster settings and the mode of P2P
// on this server (`p2pMode`: share, or anything else; null while unknown).
//   available  the card can be chosen
//   state      the key of its availability words (cluster:add.way.<way>.<state>)
//   needs      where to go to fix it: 'cluster' (the settings), 'sharing', or null
export function availability(way, settings, p2pMode) {
  if (way === 'nats') {
    const state = natsState(settings)
    if (state === 'connected') return { available: true, state: 'ready', needs: null }
    if (state === 'unreachable') return { available: true, state: 'unreachable', needs: null }
    return natsOffered(settings)
      ? { available: true, state: 'ready', needs: null }
      : { available: false, state: 'off', needs: 'cluster' }
  }
  if (way === 'p2p') {
    if (p2pMode === null || p2pMode === undefined) return { available: false, state: 'checking', needs: null }
    return p2pMode === 'share' ? { available: true, state: 'ready', needs: null } : { available: false, state: 'off', needs: 'sharing' }
  }
  return { available: true, state: 'ready', needs: null }
}

// The ways that are chosen when the step opens: every way that works with no
// set-up. P2P is a deliberate choice, so it starts unchosen.
export function defaultWays(settings, p2pMode) {
  return WAYS.filter(way => way !== 'p2p' && availability(way, settings, p2pMode).available)
}

// The routes of the join token for a choice of ways, in token order.
export function tokenRoutes(ways) {
  return TOKEN_WAYS.filter(way => ways.includes(way))
}

// Whether the choice needs a join token and whether it needs a P2P invite.
export const needsToken = ways => tokenRoutes(ways).length > 0
export const needsInvite = ways => ways.includes('p2p')

// The preference weights of the chosen ways, in the order the sentence says
// them: [{ way, weight }].
export function chosenWeights(ways, settings) {
  const weights = weightsInForce(settings)
  return WAYS.filter(way => ways.includes(way)).map(way => ({ way, weight: weights[way] }))
}

// How long a token is good for, as the choices of the select (seconds).
export const EXPIRY_CHOICES = [600, 3600, 86400]
export const DEFAULT_EXPIRY = 600
export const MAX_USES = 100
export const DEFAULT_MANY = 5

// The request body that mints a join token.
export function mintBody({ ways, uses, ttl }) {
  const body = { ttl_seconds: ttl, uses, routes: tokenRoutes(ways) }
  return body
}

// A number of uses from what a person typed: a whole number from 2 to 100, or
// the nearest one.
export function clampUses(text) {
  const n = Math.floor(Number(text))
  if (!Number.isFinite(n)) return DEFAULT_MANY
  return Math.min(MAX_USES, Math.max(2, n))
}

// Seconds until a time, not below 0.
export function secondsUntil(iso, now = Date.now()) {
  const at = Date.parse(iso)
  return Number.isFinite(at) ? Math.max(0, Math.floor((at - now) / 1000)) : 0
}

// A countdown as m:ss, or h:mm:ss from an hour on.
export function countdown(seconds) {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const rest = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${rest}` : `${m}:${rest}`
}

// A token with its middle hidden: the start stays, so two tokens can be told
// apart, and the end is dots.
export function maskToken(token, shown = 12) {
  const text = String(token || '')
  return text.length <= shown ? '•'.repeat(text.length) : `${text.slice(0, shown)}${'•'.repeat(10)}`
}

// A command with the token hidden, for display. The copy button copies the real
// command: only the text on the page is masked.
export function maskCommand(command, token) {
  return token ? String(command || '').split(token).join('•'.repeat(12)) : String(command || '')
}

// The tabs of the commands, in order. Each one names the field of the answer
// of the mint that holds its command. The install script has a second command
// for Windows.
export const COMMAND_TABS = [
  { id: 'install', field: 'install_script', alt: 'windows' },
  { id: 'docker', field: 'docker' },
  { id: 'compose', field: 'docker_compose' },
  { id: 'kubernetes', field: 'kubernetes' },
  { id: 'manual', field: 'manual' },
]

// The command a tab shows: for the install script, the Windows one when
// `windows` is true.
export function commandOf(commands, tabId, windows = false) {
  const tab = COMMAND_TABS.find(entry => entry.id === tabId)
  if (!tab || !commands) return ''
  return commands[windows && tab.alt ? tab.alt : tab.field] || ''
}

// The uses a token has left, from its row in the list of tokens, or from the
// workers that arrived when the list is not there.
export function usesLeft(row, arrived, maxUses) {
  if (row && Number.isFinite(row.max_uses) && Number.isFinite(row.uses)) return Math.max(0, row.max_uses - row.uses)
  return Math.max(0, (maxUses || 1) - arrived)
}
