// Pure helpers of the P2P sharing screens: the invite link, the kinds of
// invite, the life left of an invite and how a device reaches the server. No
// React and no fetch, so the unit tests read them directly.

// The kinds an admin can make from the Share dialog. Model-split and
// federated invites are made where those nodes are added.
export const SHARE_KINDS = ['device', 'remote']

// The kinds of the Share dialog on this server: a worker invite (a compute
// node over P2P) only on a server in distributed mode, which has nodes.
export function shareKinds(distributed) {
  return distributed ? [...SHARE_KINDS, 'worker'] : SHARE_KINDS
}

// Only device and remote invites are shown as a QR code: a node runs a
// command on its own machine and gets the link as text.
export const QR_KINDS = ['device', 'remote']

// The lives an invite can have, in seconds. The server takes up to a day.
export const TTL_CHOICES = [
  { id: '600', seconds: 600 },
  { id: '3600', seconds: 3600 },
  { id: '86400', seconds: 86400 },
]

// The last part of a multiaddr host: "/dns4/relay.example.org/udp/4001/..." is
// "relay.example.org". An address with no host part gives ''.
export function multiaddrHost(addr) {
  const parts = String(addr || '').split('/').filter(Boolean)
  for (let i = 0; i < parts.length - 1; i++) {
    if (['dns', 'dns4', 'dns6', 'dnsaddr', 'ip4', 'ip6'].includes(parts[i])) return parts[i + 1]
  }
  return ''
}

// "12D3KooWRk4e…Xk9": a peer ID short enough for a table cell. The full ID
// goes in the title of the cell.
export function shortPeer(id) {
  const text = String(id || '')
  return text.length > 16 ? `${text.slice(0, 10)}…${text.slice(-3)}` : text
}

// Reads an invite link (localai://pair?v=1&k=...) without trusting it: the
// server checks everything again when the link is redeemed. The answer is
// { ok: true, link } or { ok: false, reason } with reason empty, scheme,
// version or fields.
export function parseInviteLink(text) {
  const raw = String(text || '').trim()
  if (!raw) return { ok: false, reason: 'empty' }
  let url
  try {
    url = new URL(raw)
  } catch {
    return { ok: false, reason: 'scheme' }
  }
  if (url.protocol !== 'localai:' || url.host !== 'pair') return { ok: false, reason: 'scheme' }
  const q = url.searchParams
  if (q.get('v') !== '1') return { ok: false, reason: 'version' }
  const kind = q.get('k') || ''
  const peer = q.get('p') || ''
  const code = q.get('c') || ''
  const expires = Number(q.get('e'))
  if (!kind || !peer || !code || !Number.isFinite(expires) || expires <= 0) return { ok: false, reason: 'fields' }
  const relays = q.getAll('r')
  const open = new Set((q.get('o') || '').split(',').filter(Boolean).map(Number))
  return {
    ok: true,
    link: {
      kind,
      peer,
      label: q.get('n') || '',
      expiresAt: new Date(expires * 1000),
      addrs: q.getAll('a'),
      relays: relays.map((addr, i) => ({ addr, host: multiaddrHost(addr), open: open.has(i) })),
      pass: Boolean(q.get('x')),
    },
  }
}

// The seconds an invite still has at now, never below zero.
export function secondsLeft(expiresAt, now = Date.now()) {
  const end = new Date(expiresAt).getTime()
  if (!Number.isFinite(end)) return 0
  return Math.max(0, Math.floor((end - now) / 1000))
}

// "09:42" for less than an hour, "3:05:10" past it: a clock that counts down.
export function clock(seconds) {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${String(m).padStart(2, '0')}:${sec}`
}

// The tone of a path: direct and hole-punched paths are good, a relay is
// good when it carries data without a limit, a limited relay needs a look,
// and none means the device is not connected now.
export function pathTone(path) {
  switch (path?.kind) {
    case 'direct':
    case 'hole_punched':
      return 'ok'
    case 'relayed':
      return path.limited ? 'warn' : 'ok'
    default:
      return 'idle'
  }
}

// The words of a path, as an i18n key under `devices.path` and its values.
// A relayed path names the relay it uses; "Via your server" is this server's
// own relay, which the API reports with no label.
export function pathKey(path) {
  switch (path?.kind) {
    case 'direct': return { key: 'direct' }
    case 'hole_punched': return { key: 'holePunched' }
    case 'relayed': return path.relay_label ? { key: 'relayed', relay: path.relay_label } : { key: 'relayedUnknown' }
    default: return { key: 'none' }
  }
}

// The warnings of a minted invite the dialog explains, in the order it shows
// them. An unknown warning is dropped: the dialog has no words for it.
const WARNING_ORDER = ['relay_unreachable', 'no_relay', 'open_relay', 'no_public_addr']
export function inviteWarnings(result) {
  const list = Array.isArray(result?.warnings) ? result.warnings : []
  const set = new Set(list)
  // relay_unreachable says more than no_relay about the same missing pass.
  if (set.has('relay_unreachable')) set.delete('no_relay')
  return WARNING_ORDER.filter(w => set.has(w))
}

// The invites the "Pending invites" table lists: the pending ones, soonest to
// expire first.
export function pendingInvites(invites, now = Date.now()) {
  return (Array.isArray(invites) ? invites : [])
    .filter(i => i && i.status === 'pending' && secondsLeft(i.expires_at, now) > 0)
    .sort((a, b) => new Date(a.expires_at) - new Date(b.expires_at))
}

// The devices the table lists: active ones first, then revoked; in each group
// the one seen last comes first.
export function sortedDevices(devices) {
  const seen = d => (d.last_seen_at ? new Date(d.last_seen_at).getTime() : 0)
  return (Array.isArray(devices) ? devices : []).slice().sort((a, b) => {
    const ra = a.status === 'revoked' ? 1 : 0
    const rb = b.status === 'revoked' ? 1 : 0
    return ra - rb || seen(b) - seen(a)
  })
}

// The phrase words, for the large chips the confirmation shows.
export function phraseWords(phrase) {
  return String(phrase || '').split(/\s+/).filter(Boolean)
}

// What the remote invite step can do with a parsed link: a remote invite
// that has not expired can connect; another kind or an old one cannot.
export function inviteProblem(parsed, now = Date.now()) {
  if (!parsed?.ok) return parsed?.reason === 'empty' ? '' : 'malformed'
  if (parsed.link.kind !== 'remote') return 'wrongKind'
  if (secondsLeft(parsed.link.expiresAt, now) === 0) return 'expired'
  return ''
}

// How a failed invite connect reads to the person. 410 is the server's one
// answer for a wrong, used, canceled or expired code; 429 is too many tries;
// 400 a link the server could not read. The rest means the server was not
// reached, which is where the advice about relays and ports helps.
export function inviteFailure(err) {
  switch (err?.status) {
    case 410: return 'invalid'
    case 429: return 'rateLimited'
    case 400: return 'malformed'
    case 404: return 'unsupported'
    case 409: return 'conflict'
    default: return 'unreachable'
  }
}

// "5 min ago", "in 3 hours": a time next to now in the language of the page.
// An unknown date gives ''.
export function relativeTime(date, lang = 'en', now = Date.now()) {
  const t = new Date(date).getTime()
  if (!date || !Number.isFinite(t)) return ''
  const diff = Math.round((t - now) / 1000)
  const abs = Math.abs(diff)
  const fmt = new Intl.RelativeTimeFormat(lang, { numeric: 'auto', style: 'short' })
  if (abs < 45) return fmt.format(0, 'second')
  if (abs < 3600) return fmt.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return fmt.format(Math.round(diff / 3600), 'hour')
  return fmt.format(Math.round(diff / 86400), 'day')
}

// What a remote invite or device may use: { all: true } for every model, or
// { all: false, names } for a list. A row with no list is every model, as the
// server reads it. Device and worker rows grant no models and give null.
export function modelGrant(row) {
  if (!row || row.kind !== 'remote') return null
  const names = Array.isArray(row.models) ? row.models.filter(Boolean) : []
  if (row.all_models || names.length === 0) return { all: true, names: [] }
  return { all: false, names }
}

// The models part of the mint request. "All models" is sent as an explicit
// all_models; a list is sent as models. Other kinds carry neither.
export function mintModelFields(kind, scope, picked) {
  if (kind !== 'remote') return {}
  if (scope === 'all') return { all_models: true }
  return { models: [...picked] }
}

// The reason the Make invite button is off, or '' when it may be pressed: a
// remote invite limited to chosen models needs at least one.
export function mintBlockedReason(kind, scope, picked) {
  return kind === 'remote' && scope === 'specific' && picked.size === 0 ? 'noModels' : ''
}
