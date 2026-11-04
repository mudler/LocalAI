// Helpers of the Jellyfin page. No React, so a node test drives them.

// The key of Jellyfin API keys has no fixed shape, so the page only refuses
// what cannot go in a header.
export function tokenProblem(form) {
  const token = (form.token || '').trim()
  if (!token) return ''
  if (/[\s"\\]/.test(token)) return 'tokenSpace'
  return ''
}

// The secret the field makes: the key, or '' when not typed or not right.
export function jellyfinSecret(form) {
  return tokenProblem(form) ? '' : (form.token || '').trim()
}

export const PIN_PREFIX = '#sha256='
const HEX64 = /^[0-9a-f]{64}$/

// The fingerprint as typed (colons, spaces and capitals allowed), as 64
// lower-case hex digits or '' when it is not one.
export function normalizePin(text) {
  const p = String(text || '').replace(/[\s:]/g, '').toLowerCase()
  return HEX64.test(p) ? p : ''
}

// What is wrong with the server address and the fingerprint, as a key of the
// page's words, or ''. An empty address is no problem on an untouched form:
// extraValid asks for one.
export function serverIssue(address, pin) {
  const a = String(address || '').trim()
  const p = String(pin || '').trim()
  if (a === '') return p === '' ? '' : 'serverShape'
  if (/[\s\\]/.test(a)) return 'serverShape'
  let u
  try { u = new URL(a) } catch (_e) { return 'serverShape' }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'serverScheme'
  if (!u.hostname) return 'serverShape'
  if (u.username || u.password) return 'serverUser'
  if (u.search || u.hash || a.includes('?') || a.includes('#')) return 'serverQuery'
  if (p !== '') {
    if (u.protocol !== 'https:') return 'pinHttp'
    if (!normalizePin(p)) return 'pinShape'
  }
  if (composeServerKey(a, p).length > 200) return 'serverLong'
  return ''
}

// The entry the binding lists: the address with no trailing slash, and
// #sha256=<hex> when a certificate is pinned. It is what the model passes as
// `server`, and the host compares it with this list.
export function composeServerKey(address, pin) {
  const a = String(address || '').trim().replace(/\/+$/, '')
  const p = normalizePin(pin)
  return p ? `${a}${PIN_PREFIX}${p}` : a
}

// The reverse: the address and the fingerprint of a saved entry.
export function splitServerKey(key) {
  const k = String(key || '')
  const i = k.indexOf('#')
  if (i < 0) return { address: k, pin: '' }
  const frag = k.slice(i + 1)
  return { address: k.slice(0, i), pin: frag.toLowerCase().startsWith('sha256=') ? frag.slice(7) : '' }
}

// The name a new connection gets from its address.
export function defaultName(address) {
  try { return `Jellyfin: ${new URL(String(address).trim()).host}` } catch (_e) { return '' }
}

// The name after the address changed: an empty name, or one this page made
// from the earlier address, follows the new one. A name the person typed stays.
export function followName(name, oldAddress, newAddress) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldAddress)) return current
  return defaultName(newAddress)
}

// What the agent may change on the server with this connection. The host
// keeps it on the binding and enforces it: off runs no write tool, propose
// stores each change for you to confirm, auto lets a chat of yours make it at
// once.
export const WRITE_MODES = ['off', 'propose', 'auto']

// The mode of a saved binding, off for one that has none or an unknown one.
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving. One connection holds one server.
export const extraForm = (c) => {
  const { address, pin } = splitServerKey((c.resources || [])[0] || '')
  return { server_text: address, pin_text: pin, write_mode: writeModeOf(c.write_mode) }
}
export const extraBody = (form) => {
  const a = String(form.server_text || '').trim()
  return { resources: a ? [composeServerKey(a, form.pin_text)] : [], write_mode: writeModeOf(form.write_mode) }
}
export const extraValid = (form) => String(form.server_text || '').trim() !== '' && !serverIssue(form.server_text, form.pin_text)

// True for an address that sends the key without encryption.
export const isPlainHttp = (address) => /^http:\/\//i.test(String(address || '').trim())

// What the Test button shows for the server: { tone, key, vars }, where key
// is a key of the page's words under check. A failure of the check itself is
// r.failed: 'refused', 'certificate' or 'other'.
export function checkItem(r) {
  if (r?.failed === 'certificate') return { tone: 'warn', key: 'check.certificate', vars: {} }
  if (r?.failed === 'refused') return { tone: 'warn', key: 'check.refused', vars: {} }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: {} }
  if (!r || r.reachable !== true) return { tone: 'warn', key: 'check.failed', vars: {} }
  const vars = { name: r.name || 'Jellyfin', version: r.version || '' }
  if (r.keyWorks === false) return { tone: 'warn', key: 'check.keyRefused', vars }
  return { tone: 'ok', key: 'check.ok', vars }
}

// The sentence of the check, for a test or a plain-text summary.
export const checkLine = (r, t) => { const i = checkItem(r); return t(i.key, i.vars) }

// Why a call of the check tool failed, as the failed field of checkItem.
export function checkFailure(e) {
  const status = Number(e?.status || e?.code)
  const msg = String(e?.message || '')
  if (/certificate|fingerprint/i.test(msg)) return 'certificate'
  if (status === 401 || status === 403 || /refused the api key|unauthori[sz]ed|forbidden|\b40[13]\b/i.test(msg)) return 'refused'
  return 'other'
}
