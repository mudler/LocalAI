// Helpers of the SSH page. No React, so a node test drives them. The add-on
// checks everything again; these only help the person see a problem before
// they save.
//
// The one sealed credential of a connection is either a private key (PEM or
// OpenSSH text) or "user:password" (a v1 format; a form with typed fields will
// replace it). The address of the connection is user@host[:port]#sha256=<hex>:
// the user, the host and the pinned host key.

export const MAX_CREDENTIAL = 4096
export const METHODS = ['password', 'key']
export const methodOf = (m) => (METHODS.includes(m) ? m : 'password')

const USER_RE = /^[A-Za-z0-9_][A-Za-z0-9._\\+-]{0,63}$/
const HOST_RE = /^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$/
const HEX64 = /^[0-9a-f]{64}$/

// What is wrong with the typed user, as a key of the page's words, or ''.
export function userProblem(user) {
  const u = String(user || '')
  if (!u) return ''
  return USER_RE.test(u) ? '' : 'userShape'
}

// What is wrong with the typed secret, as a key of the page's words, or ''.
// Nothing typed is no problem: an update without it keeps the stored one.
export function credentialProblem(form) {
  const method = methodOf(form.method)
  if (method === 'key') {
    const key = String(form.key || '').trim()
    if (!key) return ''
    if (!key.startsWith('-----BEGIN')) return 'keyShape'
    if (/ENCRYPTED|Proc-Type: 4,ENCRYPTED/.test(key)) return 'keyPassphrase'
    if (key.length > MAX_CREDENTIAL) return 'credLong'
    return ''
  }
  const pass = form.passwd || ''
  if (!pass) return ''
  if (!form.user) return 'userMissing'
  if (/[\u0000-\u001f\u007f]/.test(pass)) return 'credControl'
  if (`${form.user}:${pass}`.length > MAX_CREDENTIAL) return 'credLong'
  return ''
}

// The secret the fields make: the key text, or user:password. '' when nothing
// is typed or something is wrong.
export function sshSecret(form) {
  if (credentialProblem(form)) return ''
  if (methodOf(form.method) === 'key') return String(form.key || '').trim()
  return form.user && form.passwd ? `${form.user}:${form.passwd}` : ''
}

// The host as typed ("example.com", "10.0.0.5:2222", "[::1]:22") as { host,
// port, problem }. Host and port are normalised: lower case, port 22 left out
// of the key the add-on gets only when typed that way (the add-on adds it).
export function parseHost(text) {
  const raw = String(text || '').trim()
  if (!raw) return { host: '', port: '', problem: '' }
  let host = raw
  let port = ''
  if (raw.startsWith('[')) {
    const m = /^\[([0-9a-fA-F:]+)\](?::(\d+))?$/.exec(raw)
    if (!m) return { host: '', port: '', problem: 'hostShape' }
    host = `[${m[1].toLowerCase()}]`
    port = m[2] || ''
  } else {
    const parts = raw.split(':')
    if (parts.length > 2) return { host: '', port: '', problem: 'hostShape' }
    host = parts[0].toLowerCase()
    port = parts[1] || ''
    if (!HOST_RE.test(host)) return { host: '', port: '', problem: 'hostShape' }
  }
  if (port !== '') {
    const n = Number(port)
    if (!/^[1-9]\d*$/.test(port) || n > 65535) return { host: '', port: '', problem: 'hostShape' }
  }
  return { host, port, problem: '' }
}

export const hostProblem = (text) => parseHost(text).problem

// The pin as typed (colons, spaces and capitals allowed) as 64 lower-case hex
// digits, or '' when it is not one.
export function normalizePin(text) {
  const p = String(text || '').replace(/[\s:]/g, '').toLowerCase()
  return HEX64.test(p) ? p : ''
}

// The pin the way ssh-keygen shows a fingerprint: SHA256: and unpadded base64.
export function fingerprintOfPin(pin) {
  const p = normalizePin(pin)
  if (!p) return ''
  let bin = ''
  for (let i = 0; i < p.length; i += 2) bin += String.fromCharCode(parseInt(p.slice(i, i + 2), 16))
  return `SHA256:${btoa(bin).replace(/=+$/, '')}`
}

// The address the add-on gets: user@host[:port] and, when pinned, #sha256=<hex>.
export function addressOf(user, hostText, pin) {
  const { host, port, problem } = parseHost(hostText)
  if (problem || !host || !user || userProblem(user)) return ''
  const base = `${user}@${host}${port ? `:${port}` : ''}`
  const p = normalizePin(pin)
  return p ? `${base}#sha256=${p}` : base
}

// The parts of a saved address.
export function splitAddress(address) {
  const [base, frag = ''] = String(address || '').split('#')
  const at = base.lastIndexOf('@')
  const user = at > 0 ? base.slice(0, at) : ''
  const host = at > 0 ? base.slice(at + 1) : base
  const pin = frag.startsWith('sha256=') ? normalizePin(frag.slice(7)) : ''
  return { user, host, pin }
}

// The address without the pin: it names the machine.
export const targetKey = (address) => String(address || '').split('#')[0]

// The name a new connection gets from its address.
export function defaultName(user, hostText) {
  const { host, problem } = parseHost(hostText)
  if (problem || !host) return ''
  return user ? `SSH: ${user}@${host}` : `SSH: ${host}`
}

// The name after the address changed: an empty name, or one that this page
// made itself from the earlier text, follows the address.
export function followName(name, oldUser, oldHost, newUser, newHost) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldUser, oldHost)) return current
  return defaultName(newUser, newHost)
}

// The write mode of the host. The host holds a run for the person to confirm
// in "propose" and applies it at once in "auto". "off" refuses it.
export const WRITE_MODES = ['off', 'propose', 'auto']
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// The values of the extra fields. A new connection starts on "propose": the
// person confirms each command. A saved one shows what it has.
export const extraForm = (c) => {
  const { user, host, pin } = splitAddress((c.resources || [])[0] || '')
  return {
    user,
    host_text: host,
    pin_text: pin,
    // A saved pin was already approved by the person.
    pin_ok: pin !== '',
    saved_user: user,
    saved_host: host,
    method: 'password',
    write_mode: c.id ? writeModeOf(c.write_mode) : 'propose',
  }
}

export const extraBody = (form) => {
  const address = addressOf(form.user, form.host_text, form.pin_text)
  return { resources: address ? [address] : [], write_mode: writeModeOf(form.write_mode) }
}

// Whether the form may be saved: a user, a host, and a host key the person
// trusts. A change of user or host on a saved connection needs the secret
// again, because the old one belongs to the old user, and the key must be
// checked again for a new host.
export const extraValid = (form) => {
  if (!form.user || userProblem(form.user) || !form.host_text || hostProblem(form.host_text)) return false
  if (!normalizePin(form.pin_text) || !form.pin_ok) return false
  return !needsSecretAgain(form)
}

// Whether the saved secret no longer fits the form: the person changed the
// user or the host and typed no new secret.
export function needsSecretAgain(form) {
  if (!form.id) return false
  const moved = form.user !== form.saved_user || parseHost(form.host_text).host !== parseHost(form.saved_host).host || parseHost(form.host_text).port !== parseHost(form.saved_host).port
  return moved && !sshSecret(form)
}

// What the Test button shows: { tone, key, vars }.
export function checkItem(r) {
  if (r?.failed && r.failed !== 'other') return { tone: 'warn', key: `check.${r.failed}`, vars: {} }
  if (r?.failed) return { tone: 'warn', key: 'check.failed', vars: {} }
  if (!r || r.ok !== true) return { tone: 'warn', key: 'check.failed', vars: {} }
  return { tone: 'ok', key: 'check.ok', vars: { user: r.user || '?', system: String(r.uname || '').split(' ').slice(0, 2).join(' ') || '?' } }
}

// Why a call of get-info failed, as the failed field of checkItem. The
// add-on's refusals each have their own sentence; the page matches on it.
export function checkFailure(e) {
  const text = String(e?.message || '')
  if (/host key changed/i.test(text)) return 'changed'
  if (/no host key is pinned/i.test(text)) return 'nopin'
  if (/refused the login/i.test(text)) return 'refused'
  if (/could not be reached/i.test(text)) return 'unreachable'
  if (/another command is still running/i.test(text)) return 'busy'
  return 'other'
}
