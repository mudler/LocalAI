// What the P2P settings page derives from the API: which modes can be chosen,
// how a relay row reads, what a relay check found, and the relay commands to
// copy. No React and no network: the node unit tests drive it directly
// (tests/unit/p2psettings.test.mjs).

export const MODES = ['off', 'connect', 'share']

export const RELAY_MODES = ['auto', 'on', 'off']

// The defaults of `local-ai p2p relay` (core/services/p2prelay/relaycmd).
export const RELAY_DEFAULTS = {
  circuitBytes: 1 << 30,
  circuitSeconds: 30 * 60,
  // An open relay gets 64 KiB/s per circuit; a gated one has no bandwidth limit.
  openRateBPS: 64 << 10,
}

// normalizeMode reads the mode the server reports; anything unknown is off.
export function normalizeMode(mode) {
  const m = String(mode || '').trim().toLowerCase()
  return MODES.includes(m) ? m : 'off'
}

// modeChoices says for each mode whether it can be chosen. Mode share needs
// authentication: the server refuses it while authentication is off, so the
// page shows it as unavailable instead of letting the save fail.
export function modeChoices(authEnabled) {
  return MODES.map(id => ({ id, disabled: id === 'share' && !authEnabled, needsAuth: id === 'share' && !authEnabled }))
}

// p2pSettingsFrom picks the P2P keys out of GET /api/settings, with the
// defaults of the registry for keys the answer does not have.
export function p2pSettingsFrom(all) {
  const s = all || {}
  return {
    p2p_mode: normalizeMode(s.p2p_mode),
    p2p_public_dht: s.p2p_public_dht !== false,
    p2p_mdns: s.p2p_mdns !== false,
    p2p_bootstrap_peers: Array.isArray(s.p2p_bootstrap_peers) ? s.p2p_bootstrap_peers : [],
    p2p_relay_paired: s.p2p_relay_paired !== false,
    p2p_relay_mode: RELAY_MODES.includes(s.p2p_relay_mode) ? s.p2p_relay_mode : 'auto',
    p2p_relay_rate_limit: Number.isFinite(s.p2p_relay_rate_limit) && s.p2p_relay_rate_limit > 0 ? s.p2p_relay_rate_limit : 0,
  }
}

// parsePeerList reads one address per line (or separated by commas) and drops
// empty lines and duplicates.
export function parsePeerList(text) {
  const out = []
  for (const part of String(text || '').split(/[\n,]/)) {
    const v = part.trim()
    if (v && !out.includes(v)) out.push(v)
  }
  return out
}

// relayPeerID returns the peer ID at the end of a relay address, or ''.
export function relayPeerID(addr) {
  const m = /\/p2p\/([^/\s]+)\/?$/.exec(String(addr || '').trim())
  return m ? m[1] : ''
}

// relayHost returns the host part of a multiaddr: the name or IP after
// /dns4, /dns6, /dnsaddr, /ip4 or /ip6. Empty when there is none.
export function relayHost(addr) {
  const m = /^\/(?:dns4|dns6|dns|dnsaddr|ip4|ip6)\/([^/]+)/.exec(String(addr || '').trim())
  return m ? m[1] : ''
}

// relayName is what a sentence calls a relay: its label, else its host, else
// its shortened peer ID.
export function relayName(relay) {
  return relay?.label || relayHost(relay?.addr) || shortID(relayPeerID(relay?.addr))
}

// isRelayAddr says whether text looks like a relay address the server takes:
// a multiaddr that ends in /p2p/<peer ID>. The server checks it again.
export function isRelayAddr(text) {
  const v = String(text || '').trim()
  return v.startsWith('/') && relayPeerID(v) !== ''
}

// shortID shortens a peer ID for a sentence: the first 8 and the last 4.
export function shortID(id) {
  const s = String(id || '')
  return s.length > 16 ? `${s.slice(0, 8)}…${s.slice(-4)}` : s
}

// The tone of a relay state, for its chip.
const STATE_TONE = {
  in_sync: 'ok',
  out_of_sync: 'warn',
  open: 'warn',
  unclaimed: 'warn',
  not_admin: 'warn',
  denied: 'error',
  unreachable: 'error',
  checking: 'accent',
}

export function relayTone(state) {
  return STATE_TONE[state] || 'idle'
}

// relayPass says what an invite gets from a relay. A gated relay this server
// administers holds a single-use relay pass for each new invite; an open relay
// needs no pass and goes into invites only with a warning; any other relay is
// not used in invites.
export function relayPass(relay) {
  const state = relay?.state
  if (state === 'in_sync' || state === 'out_of_sync') return 'pass'
  if (state === 'open') return 'open'
  return 'none'
}

// hasLimits says whether the relay reported any limit.
export function hasLimits(limits) {
  const l = limits || {}
  return (l.circuit_bytes || 0) > 0 || (l.circuit_seconds || 0) > 0 || (l.rate_bps || 0) > 0
}

// formatBytes writes a byte count in binary units: 1 GiB, 64 KiB.
export function formatBytes(n) {
  const v = Number(n) || 0
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let x = v
  while (x >= 1024 && i < units.length - 1) { x /= 1024; i++ }
  const r = Math.round(x * 10) / 10
  return `${Number.isInteger(r) ? r : r.toFixed(1)} ${units[i]}`
}

// formatSeconds writes a duration: 30 min, 1 h, 1 h 30 min, 45 s.
export function formatSeconds(n) {
  const v = Math.max(0, Math.round(Number(n) || 0))
  if (v < 60) return `${v} s`
  const h = Math.floor(v / 3600)
  const m = Math.floor((v % 3600) / 60)
  if (h === 0) return `${m} min`
  return m === 0 ? `${h} h` : `${h} h ${m} min`
}

// limitParts lists the limits a relay reports, as the words to join: data
// per circuit, time per circuit and bandwidth per circuit. Empty when the
// relay reports none.
export function limitParts(limits) {
  const l = limits || {}
  const parts = []
  if (l.circuit_bytes > 0) parts.push(formatBytes(l.circuit_bytes))
  if (l.circuit_seconds > 0) parts.push(formatSeconds(l.circuit_seconds))
  if (l.rate_bps > 0) parts.push(`${formatBytes(l.rate_bps)}/s`)
  return parts
}

// The steps of a relay check, in the order the dialog shows them.
export const CHECK_STEPS = ['reserve', 'circuit', 'allowlist']

const detailOf = (check, prefix) => (check?.detail || []).find(d => d.startsWith(`${prefix}:`)) || ''
const afterColon = (line) => line.slice(line.indexOf(':') + 1).trim()

// PEER_MISMATCH is the libp2p handshake error for an address whose peer ID is
// not the key the relay holds, for example after a restart without its key
// volume.
const PEER_MISMATCH = /peer id mismatch: expected (\S+?),? but remote key matches (\S+)/i

// wrongPeer returns {expected, actual} when the check or the relay health says
// the relay answered with another peer ID, or null.
export function wrongPeer(check) {
  const texts = [...(check?.detail || []), check?.relay?.health || '']
  for (const t of texts) {
    const m = PEER_MISMATCH.exec(t)
    if (m) return { expected: m[1], actual: m[2] }
  }
  return null
}

// checkOutcome reads POST /api/p2p/relays/{id}/check. It returns the result
// kind and the state of each step:
//   ok          gated, this server is admin, a circuit went through
//   open        an open relay that carried a circuit: usable with a warning
//   circuit     the relay answers but the circuit test failed
//   unclaimed   the relay has no admin yet: it waits for its claim code
//   not_admin   the relay does not list this server as its admin
//   wrong_peer  the relay answered with another peer ID
//   unreachable no connection
// A step is ok, failed, skipped or waiting (not reached).
export function checkOutcome(check) {
  const c = check || {}
  const state = c.relay?.state || ''
  const steps = { reserve: 'waiting', circuit: 'waiting', allowlist: 'waiting' }
  const details = { reserve: '', circuit: '', allowlist: '' }

  const peer = wrongPeer(c)
  if (!c.connected) {
    steps.reserve = 'failed'
    details.reserve = afterColon(detailOf(c, 'connect')) || c.relay?.health || ''
    return { kind: peer ? 'wrong_peer' : 'unreachable', steps, details, peer }
  }
  steps.reserve = c.reserved ? 'ok' : 'failed'
  details.reserve = afterColon(detailOf(c, 'reserve'))

  const circuitLine = afterColon(detailOf(c, 'circuit'))
  steps.circuit = c.circuit_ok ? 'ok' : (circuitLine.startsWith('skipped') ? 'skipped' : 'failed')
  details.circuit = circuitLine

  if (state === 'open') {
    steps.allowlist = 'skipped'
  } else if (state === 'unclaimed') {
    steps.allowlist = 'waiting'
  } else if (c.admin_ok) {
    steps.allowlist = c.in_sync ? 'ok' : 'failed'
  } else {
    steps.allowlist = 'failed'
  }
  details.allowlist = afterColon(detailOf(c, 'admin'))

  let kind
  if (state === 'unclaimed') kind = 'unclaimed'
  else if (state === 'open') kind = c.circuit_ok ? 'open' : 'circuit'
  else if (!c.admin_ok) kind = 'not_admin'
  else if (!c.circuit_ok) kind = 'circuit'
  else kind = 'ok'
  return { kind, steps, details, peer: null }
}

// canAdd says whether a checked relay may be kept: it works, or it works
// with a warning. A relay that cannot carry traffic is not added.
export function canAdd(kind) {
  return kind === 'ok' || kind === 'open'
}

// shellQuote quotes a value for a POSIX shell when it needs it.
function shellQuote(v) {
  return /^[A-Za-z0-9._/:=@+-]+$/.test(v) ? v : `'${String(v).replace(/'/g, `'\\''`)}'`
}

// relayCommands returns the two ways to start a gated relay for this server:
// the small container image and the binary. The host network lets the relay
// see and announce its public address.
export function relayCommands(peerID) {
  const admin = shellQuote(peerID || '<peer ID of this server>')
  return {
    docker: [
      'docker run -d --name localai-relay --restart unless-stopped \\',
      '  --network host -v localai-relay:/data \\',
      `  localai/relay --admin ${admin}`,
    ].join('\n'),
    binary: `local-ai p2p relay --admin ${admin}`,
  }
}

// limitsFromReported reads the limits a relay reports into the form of the
// limits dialog (GiB, minutes, KiB/s). Zero means no limit for that value.
export function limitsFromReported(limits) {
  const l = limits || {}
  return {
    dataGiB: round2((l.circuit_bytes || 0) / (1 << 30)),
    minutes: round2((l.circuit_seconds || 0) / 60),
    kibps: round2((l.rate_bps || 0) / 1024),
  }
}

// hasCeiling says whether the relay reported a ceiling for any limit. The
// operator of the relay sets it with the relay's flags.
export function hasCeiling(ceiling) {
  const c = ceiling || {}
  return (c.circuit_bytes || 0) > 0 || (c.circuit_seconds || 0) > 0 || (c.rate_bps || 0) > 0
}

// The ceiling of each field of the limits form, in the units of the API.
const CEILING_KEYS = { dataGiB: 'circuit_bytes', minutes: 'circuit_seconds', kibps: 'rate_bps' }

// limitsMaxText writes the ceiling of one field of the form (dataGiB,
// minutes or kibps) in words, or an empty string when it has none.
export function limitsMaxText(field, ceiling) {
  const v = Number((ceiling || {})[CEILING_KEYS[field]]) || 0
  if (v <= 0) return ''
  return field === 'minutes' ? formatSeconds(v) : field === 'kibps' ? `${formatBytes(v)}/s` : formatBytes(v)
}

// limitsIssues checks the form against the ceiling before it is sent, as the
// relay will: a value above the ceiling is 'above', and zero (no limit, or
// noLimit as a whole) where there is a ceiling is 'required'. It returns the
// issue of each field that has one.
export function limitsIssues({ form, noLimit, ceiling }) {
  const body = noLimit ? { circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 } : limitsPatch({ form })
  const issues = {}
  for (const [field, key] of Object.entries(CEILING_KEYS)) {
    const max = Number((ceiling || {})[key]) || 0
    if (max <= 0) continue
    if (body[key] === 0) issues[field] = 'required'
    else if (body[key] > max) issues[field] = 'above'
  }
  return issues
}

// defaultLimits is the form for "Reset to defaults". A default above the
// ceiling of the relay becomes the ceiling.
export function defaultLimits(open, ceiling) {
  const f = {
    dataGiB: RELAY_DEFAULTS.circuitBytes / (1 << 30),
    minutes: RELAY_DEFAULTS.circuitSeconds / 60,
    kibps: open ? RELAY_DEFAULTS.openRateBPS / 1024 : 0,
  }
  const c = limitsFromReported(ceiling)
  for (const k of Object.keys(f)) {
    if (c[k] > 0 && (f[k] === 0 || f[k] > c[k])) f[k] = c[k]
  }
  return f
}

function round2(n) {
  return Math.round(n * 100) / 100
}

// validLimits says whether every field of the limits form is a number of zero
// or more (zero means no limit for that field).
export function validLimits(f) {
  const ok = (v) => Number.isFinite(Number(v)) && Number(v) >= 0 && String(v).trim() !== ''
  return ok(f.dataGiB) && ok(f.minutes) && ok(f.kibps)
}

// limitsPatch is the body of PUT /api/p2p/relays/{id}/limits for the form.
// noLimit removes the data, time and bandwidth limits.
export function limitsPatch({ form, noLimit }) {
  if (noLimit) return { circuit_bytes: 0, circuit_seconds: 0, rate_bps: 0 }
  return {
    circuit_bytes: Math.round(Number(form.dataGiB) * (1 << 30)),
    circuit_seconds: Math.round(Number(form.minutes) * 60),
    rate_bps: Math.round(Number(form.kibps) * 1024),
  }
}

// ago says how long ago a time was, as a key and a count for the words:
// now, minutes, hours or days. Empty key when the time is unknown.
export function ago(ts, now = Date.now()) {
  const t = ts ? new Date(ts).getTime() : NaN
  if (!Number.isFinite(t)) return { key: '', count: 0 }
  const minutes = Math.floor(Math.max(0, now - t) / 60000)
  if (minutes < 1) return { key: 'now', count: 0 }
  if (minutes < 60) return { key: 'minutes', count: minutes }
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return { key: 'hours', count: hours }
  return { key: 'days', count: Math.floor(hours / 24) }
}

// The setting p2p_user_pairing: what an invite a user mints for their own
// device carries, and the limits of each user. The ranges are the ones the
// server accepts (PUT /api/p2p/user-pairing answers 400 outside them).
export const USER_PAIRING_RELAYS = ['auto', 'none']
export const USER_PAIRING_DEFAULTS = {
  relays: 'auto', allow_open_relay: false, omit_lan_addrs: false, max_devices: 5, max_pending: 2, max_ttl_seconds: 3600,
}
export const USER_PAIRING_RANGES = {
  max_devices: [1, 100],
  max_pending: [1, 20],
  // The life is edited in minutes: 60 to 86400 seconds.
  ttl_minutes: [1, 1440],
}

// userPairingFrom reads the answer of GET /api/p2p/user-pairing, with the
// defaults of the server for anything missing or unknown.
export function userPairingFrom(raw) {
  const r = raw || {}
  const num = (v, d) => (Number.isInteger(v) && v > 0 ? v : d)
  return {
    relays: USER_PAIRING_RELAYS.includes(r.relays) ? r.relays : USER_PAIRING_DEFAULTS.relays,
    allow_open_relay: r.allow_open_relay === true,
    omit_lan_addrs: r.omit_lan_addrs === true,
    max_devices: num(r.max_devices, USER_PAIRING_DEFAULTS.max_devices),
    max_pending: num(r.max_pending, USER_PAIRING_DEFAULTS.max_pending),
    max_ttl_seconds: num(r.max_ttl_seconds, USER_PAIRING_DEFAULTS.max_ttl_seconds),
  }
}

// The limits as the form edits them: text fields, the life in minutes.
export function userPairingForm(s) {
  return {
    max_devices: String(s.max_devices),
    max_pending: String(s.max_pending),
    ttl_minutes: String(Math.max(1, Math.round(s.max_ttl_seconds / 60))),
  }
}

// The fields of the form that are not a whole number in their range, as
// { field: true }. An empty object means the form can be saved.
export function userPairingIssues(form) {
  const issues = {}
  for (const [field, [lo, hi]] of Object.entries(USER_PAIRING_RANGES)) {
    const text = String(form?.[field] ?? '').trim()
    const n = Number(text)
    if (!/^\d+$/.test(text) || n < lo || n > hi) issues[field] = true
  }
  return issues
}

// The body of the PUT for the limits: only the fields that changed.
export function userPairingLimitsPatch(saved, form) {
  const patch = {}
  const devices = Number(form.max_devices)
  const pending = Number(form.max_pending)
  const ttl = Number(form.ttl_minutes) * 60
  if (devices !== saved.max_devices) patch.max_devices = devices
  if (pending !== saved.max_pending) patch.max_pending = pending
  if (ttl !== saved.max_ttl_seconds && Math.round(saved.max_ttl_seconds / 60) * 60 !== ttl) patch.max_ttl_seconds = ttl
  return patch
}
