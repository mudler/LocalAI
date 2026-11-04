// Helpers of the Home Assistant page. No React, so a node test drives them.
// The add-on checks everything again; these only help the person see a
// problem before they save.

export const MIN_TOKEN = 20

// What is wrong with the typed token, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without a token keeps the stored one.
export function tokenProblem(form) {
  const token = (form.token || '').trim()
  if (!token) return ''
  if (/\s/.test(token)) return 'tokenSpace'
  if (token.length < MIN_TOKEN) return 'tokenShort'
  return ''
}

export function haSecret(form) {
  return tokenProblem(form) ? '' : (form.token || '').trim()
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
// trailing slash. Returns { url, problem } where problem is a key of the
// page's words or ''. The rules are those of the add-on: https, or http on a
// local network only.
export function parseAddress(text) {
  const raw = String(text || '').trim()
  if (!raw) return { url: '', problem: '' }
  let u
  try { u = new URL(raw) } catch (_e) { return { url: '', problem: 'addressShape' } }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { url: '', problem: 'addressShape' }
  if (u.username || u.password || u.search || u.hash || !u.hostname) return { url: '', problem: 'addressShape' }
  const host = u.hostname.toLowerCase()
  if (u.protocol === 'http:') {
    const v4 = isPrivateV4(host)
    const local = v4 === null ? (!host.includes('.') || LOCAL_SUFFIXES.some((s) => host.endsWith(s))) : v4
    if (!local) return { url: '', problem: 'addressHttp' }
  }
  const path = u.pathname.replace(/\/+$/, '')
  return { url: `${u.protocol}//${u.host.toLowerCase()}${path}`, problem: '' }
}

export const addressProblem = (text) => parseAddress(text).problem

// The name a new connection gets from its address.
export function defaultName(text) {
  const { url } = parseAddress(text)
  if (!url) return ''
  return `Home Assistant: ${new URL(url).host}`
}

// The name after the address changed: an empty name, or one that this page
// made itself from the earlier text, follows the address. A name the person
// typed stays.
export function followName(name, oldText, newText) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldText)) return current
  return defaultName(newText)
}

// What the agent may change with this connection. The host keeps it on the
// binding and enforces it: off runs no write tool, propose stores each change
// for you to confirm, auto lets a chat of yours make a quiet change at once.
// A change with a physical effect always waits for you.
export const WRITE_MODES = ['off', 'propose', 'auto']
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

// What the Test button shows for the address: { tone, key, vars }.
export function checkItem(r) {
  if (r?.failed === 'refused') return { tone: 'warn', key: 'check.refused', vars: {} }
  if (r?.failed === 'certificate') return { tone: 'warn', key: 'check.certificate', vars: {} }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: {} }
  if (!r || r.ok !== true) return { tone: 'warn', key: 'check.failed', vars: {} }
  return { tone: 'ok', key: 'check.ok', vars: { version: r.version || '?', name: r.name || '', count: r.integrations ?? 0 } }
}

// Why a call of get-config failed, as the failed field of checkItem.
export function checkFailure(e) {
  const status = Number(e?.status || e?.code)
  const text = String(e?.message || '')
  if (/certificate|pin/i.test(text)) return 'certificate'
  if (status === 401 || status === 403 || /refused the token|unauthori[sz]ed|forbidden|\b40[13]\b/i.test(text)) return 'refused'
  return 'other'
}
