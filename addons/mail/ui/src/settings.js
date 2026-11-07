// Helpers of the Mail page. No React, so a node test drives them.
//
// A connection has no free-form place in a binding, so the page seals all of
// it as one JSON record, the credential, and keeps only the mailbox address in
// the list of the binding, where the host checks every tool call against it.
// The record has the shape of addons/mail/internal/account.

export const SECURITY = ['tls', 'starttls', 'none']
export const DEFAULT_PORT = {
  imap: { tls: 993, starttls: 143, none: 143 },
  smtp: { tls: 465, starttls: 587, none: 587 },
}
export const PORTS = { imap: [143, 993], smtp: [465, 587] }
export const MAX_CREDENTIAL_BYTES = 4096

const HOST = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/
const ADDRESS = /^[^\s<>,;"\\@]+@[^\s<>,;"\\@]+\.[^\s<>,;"\\@]+$/
const control = (s) => /[\u0000-\u001f\u007f]/.test(s)

export const securityOf = (s) => (SECURITY.includes(s) ? s : 'tls')

// A host name or an IPv4 address, nothing else: no scheme, port or path.
export const hostProblem = (h) => (HOST.test(String(h || '').trim()) ? '' : 'hostShape')
export const addressProblem = (a) => (ADDRESS.test(String(a || '').trim()) && String(a).trim().length <= 254 ? '' : 'addressShape')

const TLS_PORT = { imap: 993, smtp: 465 }

// Implicit TLS belongs to 993 and 465; STARTTLS and clear text to 143 and 587.
// The other pairing makes the server hang, which looks like a timeout.
export function portProblem(kind, port, security) {
  const n = Number(port)
  if (!Number.isInteger(n) || !PORTS[kind].includes(n)) return 'portShape'
  if ((security === 'tls') !== (n === TLS_PORT[kind])) return 'portSecurityMismatch'
  return ''
}

// The folders of a text box: one per line, no blank, no repeat.
export function parseFolders(text) {
  const seen = new Set()
  const out = []
  for (const line of String(text || '').split(/\r?\n/)) {
    const f = line.trim()
    if (!f || seen.has(f)) continue
    seen.add(f)
    out.push(f)
  }
  return out
}

export const foldersProblem = (text) => {
  const l = parseFolders(text)
  if (l.length > 20) return 'foldersMany'
  return l.some((f) => f.length > 200 || control(f)) ? 'foldersShape' : ''
}

// The defaults of a new form.
export const emptyMail = () => ({
  mail_address: '', mail_display: '', mail_user: '', mail_pass: '',
  imap_host: '', imap_port: 993, imap_sec: 'tls',
  smtp_host: '', smtp_port: 465, smtp_sec: 'tls',
  folders_text: '', drafts_folder: '', replace: true, plain_ok: false, write_mode: 'off',
})

// The mode of a saved binding: only off and propose exist, since every send
// and draft waits for a confirmation whatever the mode says.
export const WRITE_MODES = ['off', 'propose']
export const writeModeOf = (m) => (m === 'propose' || m === 'auto' ? 'propose' : 'off')

// Everything wrong with the connection fields of the form, by field, each as
// a key of the page's words. A saved connection that is not being replaced has
// nothing to check. An empty required field is a problem too: the page shows
// it only once the field has text, and Save stays off until it is filled.
export function fieldProblems(form) {
  if (!form.replace) return {}
  const p = {}
  if (addressProblem(form.mail_address)) p.address = 'addressShape'
  if (!String(form.mail_user || '').trim() || control(form.mail_user) || form.mail_user.length > 256) p.user = 'userShape'
  if (!form.mail_pass) p.pass = 'passMissing'
  else if (form.mail_pass.length > 512 || control(form.mail_pass)) p.pass = 'passShape'
  if (String(form.mail_display || '').length > 100 || control(form.mail_display || '')) p.display = 'displayShape'
  for (const k of ['imap', 'smtp']) {
    const sec = securityOf(form[`${k}_sec`])
    if (hostProblem(form[`${k}_host`])) p[`${k}Host`] = `${k}HostShape`
    const port = portProblem(k, form[`${k}_port`], sec)
    if (port) p[`${k}Port`] = `${k}${port[0].toUpperCase()}${port.slice(1)}`
    if (sec === 'none' && !form.plain_ok) p.plain = 'plainNotConfirmed'
  }
  const f = foldersProblem(form.folders_text)
  if (f) p.folders = f
  if (form.drafts_folder && (control(form.drafts_folder) || form.drafts_folder.length > 200)) p.folders = 'foldersShape'
  if (!Object.keys(p).length && new TextEncoder().encode(record(form)).length > MAX_CREDENTIAL_BYTES) p.size = 'tooLong'
  return p
}

// The first problem, or ''.
export function connectionProblem(form) {
  return Object.values(fieldProblems(form))[0] || ''
}

// The record of the form as JSON, without checking it.
function record(form) {
  const rec = {
    v: 1,
    address: String(form.mail_address || '').trim(),
    username: String(form.mail_user || '').trim(),
    password: form.mail_pass || '',
    imap: { host: String(form.imap_host || '').trim(), port: Number(form.imap_port), security: securityOf(form.imap_sec) },
    smtp: { host: String(form.smtp_host || '').trim(), port: Number(form.smtp_port), security: securityOf(form.smtp_sec) },
  }
  if (String(form.mail_display || '').trim()) rec.name = String(form.mail_display).trim()
  const folders = parseFolders(form.folders_text)
  if (folders.length) rec.folders = folders
  if (String(form.drafts_folder || '').trim()) rec.draftsFolder = String(form.drafts_folder).trim()
  return JSON.stringify(rec)
}

// The sealed record of the form: '' when the connection is not being replaced
// or the fields are not right.
export function mailSecret(form) {
  if (!form.replace || connectionProblem(form)) return ''
  return record(form)
}

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving.
export const extraForm = (c) => {
  const address = (c.resources || [])[0] || ''
  return c.id
    ? { ...emptyMail(), mail_address: address, replace: false, write_mode: writeModeOf(c.write_mode) }
    : emptyMail()
}
export const extraBody = (form) => ({
  resources: String(form.mail_address || '').trim() ? [String(form.mail_address).trim()] : [],
  write_mode: writeModeOf(form.write_mode),
})
export const extraValid = (form) => !connectionProblem(form)

// The name a new connection gets from its address.
export const defaultName = (address) => (String(address || '').trim() ? `Mail: ${String(address).trim()}` : '')
export const followName = (name, oldAddress, newAddress) => {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldAddress)) return current
  return defaultName(newAddress)
}

// A port that follows the security when the person changes it, unless they
// typed a port of their own.
export function portAfterSecurity(kind, oldSec, newSec, port) {
  return Number(port) === DEFAULT_PORT[kind][securityOf(oldSec)] ? DEFAULT_PORT[kind][securityOf(newSec)] : port
}

// What the Test shows for the two logins: [{ part, tone, key, vars }].
export function checkItems(data) {
  const out = []
  for (const part of ['imap', 'smtp']) {
    const r = data && typeof data[part] === 'object' && data[part] ? data[part] : null
    if (r && r.ok === true) out.push({ part, tone: 'ok', key: `check.${part}Ok`, vars: {} })
    else out.push({ part, tone: 'warn', key: `check.${part}Failed`, vars: { reason: r && typeof r.error === 'string' ? r.error : '' } })
  }
  return out
}
