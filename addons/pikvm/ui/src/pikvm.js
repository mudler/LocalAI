// Helpers of the PiKVM page. No React, so a node test drives them. The add-on
// checks everything again; these only help the person see a problem before
// they save.
//
// The one sealed credential of a connection holds "user:password" (a v1 format;
// a form with separate user and password fields will replace it). It splits at
// the first colon, so the password may hold colons but the user may not.

export const MAX_CREDENTIAL = 4096

// What is wrong with the typed user and password, as a key of the page's words,
// or ''. Nothing typed is no problem: an update without them keeps the stored one.
export function credentialProblem(form) {
  const user = form.user || ''
  const pass = form.passwd || ''
  if (!user && !pass) return ''
  if (!user) return 'userMissing'
  if (!pass) return 'passMissing'
  if (user.includes(':')) return 'userColon'
  if (/[\u0000-\u001f\u007f]/.test(user + pass)) return 'credControl'
  if (user !== user.trim() || pass !== pass.trim()) return 'credSpace'
  if (`${user}:${pass}`.length > MAX_CREDENTIAL) return 'credLong'
  return ''
}

export function pikvmSecret(form) {
  return credentialProblem(form) || !form.user || !form.passwd ? '' : `${form.user}:${form.passwd}`
}

const LOCAL_SUFFIXES = ['.local', '.lan', '.home', '.internal', '.home.arpa', '.localdomain']

function isPrivateV4(host) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host)
  if (!m) return null
  const [a, b] = [Number(m[1]), Number(m[2])]
  if ([m[1], m[2], m[3], m[4]].some((x) => Number(x) > 255)) return null
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)
}

// The address as the add-on will read it: lower-case scheme and host, no
// trailing slash, and the fingerprint pin kept as #sha256=<64 hex digits>.
// Returns { url, pin, problem } where problem is a key of the page's words or ''.
// https, or http on a local network only.
export function parseAddress(text) {
  const raw = String(text || '').trim()
  if (!raw) return { url: '', pin: '', problem: '' }
  let u
  try { u = new URL(raw) } catch (_e) { return { url: '', pin: '', problem: 'addressShape' } }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { url: '', pin: '', problem: 'addressShape' }
  if (u.username || u.password || u.search || !u.hostname || raw.includes('?')) return { url: '', pin: '', problem: 'addressShape' }
  let pin = ''
  if (u.hash || raw.includes('#')) {
    const m = /^#sha256=([0-9a-fA-F:]+)$/.exec(u.hash)
    const hex = m ? m[1].replace(/:/g, '').toLowerCase() : ''
    if (u.protocol !== 'https:') return { url: '', pin: '', problem: 'pinHttp' }
    if (!/^[0-9a-f]{64}$/.test(hex)) return { url: '', pin: '', problem: 'pinShape' }
    pin = hex
  }
  const host = u.hostname.toLowerCase()
  if (u.protocol === 'http:') {
    const v4 = isPrivateV4(host)
    const local = v4 === null ? (!host.includes('.') || LOCAL_SUFFIXES.some((s) => host.endsWith(s))) : v4
    if (!local) return { url: '', pin: '', problem: 'addressHttp' }
  }
  const path = u.pathname.replace(/\/+$/, '')
  const base = `${u.protocol}//${u.host.toLowerCase()}${path}`
  return { url: pin ? `${base}#sha256=${pin}` : base, pin, problem: '' }
}

export const addressProblem = (text) => parseAddress(text).problem

// The address without the pin: it is what the settings of the add-on are keyed by.
export const deviceKey = (address) => String(address || '').split('#')[0]

// The name a new connection gets from its address.
export function defaultName(text) {
  const { url } = parseAddress(text)
  if (!url) return ''
  return `PiKVM: ${new URL(url).host}`
}

// The name after the address changed: an empty name, or one that this page
// made itself from the earlier text, follows the address.
export function followName(name, oldText, newText) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldText)) return current
  return defaultName(newText)
}

// The write mode of the host. A PiKVM write tool is confirmed by a person on
// every call whatever the mode is; the mode only decides whether the write
// tools are refused at once (off) or wait for the person (propose).
export const WRITE_MODES = ['off', 'propose']
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

export const extraForm = (c) => ({ address_text: (c.resources || [])[0] || '', write_mode: writeModeOf(c.write_mode) })
export const extraBody = (form) => {
  const { url } = parseAddress(form.address_text)
  return { resources: url ? [url] : [], write_mode: writeModeOf(form.write_mode) }
}
// An address is required: a connection without one reaches nothing.
export const extraValid = (form) => {
  const { url, problem } = parseAddress(form.address_text)
  return Boolean(url) && !problem
}

// Whether an address has no fingerprint pin while it is https. A PiKVM comes
// with a self-signed certificate, so the page nudges toward pinning.
export const needsPinHint = (address) => {
  const { url, pin } = parseAddress(address)
  return url.startsWith('https:') && !pin
}

// What the Test button shows: { tone, key, vars }.
export function checkItem(r) {
  if (r?.failed === 'refused') return { tone: 'warn', key: 'check.refused', vars: {} }
  if (r?.failed === 'certificate') return { tone: 'warn', key: 'check.certificate', vars: {} }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: {} }
  if (!r || r.ok !== true) return { tone: 'warn', key: 'check.failed', vars: {} }
  return { tone: 'ok', key: 'check.ok', vars: { model: r.model || 'PiKVM', version: r.version || '?' } }
}

// Why a call of get-info failed, as the failed field of checkItem.
export function checkFailure(e) {
  const status = Number(e?.status || e?.code)
  const text = String(e?.message || '')
  if (/certificate|pinned/i.test(text)) return 'certificate'
  if (status === 401 || status === 403 || /refused the user name|unauthori[sz]ed|forbidden|\b40[13]\b/i.test(text)) return 'refused'
  return 'other'
}
