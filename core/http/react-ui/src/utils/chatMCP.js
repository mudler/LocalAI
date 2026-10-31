// Helpers of the MCP connectors, the tools panel and the saved-agent picker. No
// React, so a node test drives them. A secret (token, header value, variable)
// only goes in: nothing here reads one back from the server.

import { relativeDate } from './fileBrowser.js'

export const PREFIX = 'connector:'

export function emptyMCP() {
  return {
    id: '', name: '', transport: 'http', url: '', command: '', argsText: '', enabled: false,
    token: '', headers: [], env: [], clearToken: false, clearHeaders: false, clearEnv: false,
    has_token: false, has_headers: false, has_env: false,
  }
}

const pairs = (rows) => {
  const out = {}
  for (const r of rows || []) {
    const k = (r.name || '').trim()
    if (k) out[k] = r.value ?? ''
  }
  return out
}

// The body of a create or replace request. Token, headers and environment go only
// when typed, so an update keeps what the server stored; a clear flag sends the
// empty value that removes it.
export function mcpPayload(form) {
  const body = { name: (form.name || '').trim(), transport: form.transport, enabled: Boolean(form.enabled) }
  if (form.transport === 'stdio') {
    body.command = (form.command || '').trim()
    body.args = (form.argsText || '').split('\n').map(x => x.trim()).filter(Boolean)
    const env = pairs(form.env)
    if (form.clearEnv) body.env = {}
    else if (Object.keys(env).length) body.env = env
    return body
  }
  body.url = (form.url || '').trim()
  const token = (form.token || '').trim()
  if (form.clearToken) body.token = ''
  else if (token) body.token = token
  const headers = pairs(form.headers)
  if (form.clearHeaders) body.headers = {}
  else if (Object.keys(headers).length) body.headers = headers
  return body
}

export function canSaveMCP(form, taken = []) {
  if (nameProblem(form.name, taken)) return false
  if (form.transport === 'stdio') return Boolean((form.command || '').trim())
  return !urlProblem(form.url)
}

// Where a connector points, for the list: the host of an address (never its path
// or query, which may hold a key) or the name of a command.
export function mcpTarget(c) {
  if (c.transport === 'stdio') return (c.command || '').split('/').filter(Boolean).pop() || ''
  try { return new URL(c.url).hostname || c.url } catch { return c.url || '' }
}

// The address for the details pane: scheme, host and path. The query is
// replaced by "?…" because it may hold a key.
export function mcpAddress(c) {
  if (c.transport === 'stdio') return [c.command || '', ...(c.args || [])].filter(Boolean).join(' ')
  try {
    const u = new URL(c.url)
    return `${u.protocol}//${u.host}${u.pathname === '/' ? '' : u.pathname}${u.search ? '?…' : ''}`
  } catch { return c.url || '' }
}

// Why an address cannot be saved, as a key of mcpPage.errors, or '' when it is
// fine. The server checks again (private networks, credentials in the address).
export function urlProblem(value) {
  const v = (value || '').trim()
  if (!v) return 'urlRequired'
  let u
  try { u = new URL(v) } catch { return 'urlInvalid' }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'urlScheme'
  if (u.username || u.password) return 'urlCredentials'
  return ''
}

// Why a name cannot be saved, as a key of mcpPage.errors, or ''. taken is the
// list of the other connectors' names.
export function nameProblem(value, taken = []) {
  const v = (value || '').trim()
  if (!v) return 'nameRequired'
  if (taken.some(x => x.trim().toLowerCase() === v.toLowerCase())) return 'nameTaken'
  return ''
}

// What a form holds that the server would store, to tell whether it changed.
export function formFingerprint(form) {
  if (!form) return ''
  const rows = (list) => (list || []).filter(r => (r.name || '').trim() || (r.value || '')).map(r => [r.name, r.value])
  return JSON.stringify([form.name, form.transport, form.url, form.command, form.argsText, form.token,
    rows(form.headers), rows(form.env), form.clearToken, form.clearHeaders, form.clearEnv])
}

// The form of an existing connector: secrets are never read back, so they start
// empty.
export function formOf(c) {
  return { ...emptyMCP(), ...c, argsText: (c.args || []).join('\n'), token: '', headers: [], env: [] }
}

// --- the last connection test of each connector, kept in this browser ---

export const CHECKS_KEY = 'localai_mcp_checks'

// A test answered 401 or 403, or named the token: the server wants a (new)
// credential rather than being down.
// A server's reason as a sentence: the errors of the MCP client start in lower
// case and have no full stop ("the server answered 503").
export function sentence(text) {
  const v = (text || '').trim()
  if (!v) return ''
  const s = v.charAt(0).toUpperCase() + v.slice(1)
  return /[.!?…]$/.test(s) ? s : `${s}.`
}

export function isAuthError(text) {
  return /\b(401|403)\b|unauthori[sz]ed|forbidden|invalid[_ ]token|access token|authenticat/i.test(text || '')
}

// What a test depends on: where the connector points and what is saved for it.
// Switching it on or off changes its update time but not this, so the last test
// still holds.
// It is a hash: the address may hold a key in its query, and browser storage
// must not keep it.
export function connectorSig(c) {
  const text = JSON.stringify([c.transport, c.url || '', c.command || '', c.args || [], Boolean(c.has_token), Boolean(c.has_headers), Boolean(c.has_env)])
  // FNV-1a, 32 bits twice with two seeds: enough to tell a change, not a secret.
  let a = 0x811c9dc5
  let b = 0x01000193 ^ text.length
  for (let i = 0; i < text.length; i++) {
    const ch = text.charCodeAt(i)
    a = Math.imul(a ^ ch, 0x01000193)
    b = Math.imul(b ^ ch, 0x5bd1e995)
  }
  return (a >>> 0).toString(16).padStart(8, '0') + (b >>> 0).toString(16).padStart(8, '0')
}

// The record kept for a test result, with the signature of the connector it
// tested: a later change of the address or the secrets makes it stale.
export function checkRecord(c, res, at = Date.now()) {
  const details = Array.isArray(res.tool_details) && res.tool_details.length
    ? res.tool_details.map(d => ({ name: d.name, description: d.description || '' }))
    : (res.tools || []).map(name => ({ name, description: '' }))
  return { at, ok: Boolean(res.ok), tools: res.ok ? details : [], error: res.ok ? '' : (res.error || ''), sig: connectorSig(c) }
}

export function readChecks(storage) {
  try {
    const data = JSON.parse(storage.getItem(CHECKS_KEY) || '{}')
    return data && typeof data === 'object' && !Array.isArray(data) ? data : {}
  } catch { return {} }
}

export function writeChecks(storage, checks) {
  try { storage.setItem(CHECKS_KEY, JSON.stringify(checks)) } catch { /* storage blocked: the page keeps them in memory */ }
}

// When a test ran, in words and in full (for a tooltip).
export function testedWhen(at, locale) {
  if (!at) return { label: '', exact: '' }
  return relativeDate(new Date(at).toISOString(), { locale })
}

// The check of a connector, unless the connector changed since.
export function currentCheck(c, checks) {
  const k = checks?.[c.id]
  if (!k || k.sig !== connectorSig(c)) return null
  return k
}

// The checks without the given connector: an edit saved from this page may
// replace a secret without changing the signature.
export function dropCheck(checks, id) {
  const rest = { ...(checks || {}) }
  delete rest[id]
  return rest
}

// One state per connector, for the chip and the filter:
//   off        switched off
//   unchecked  on, never tested in this browser (or changed since)
//   ok         the last test connected
//   auth       the last test was refused for its credential
//   error      the last test could not connect
export function connectorState(c, check) {
  if (!c.enabled) return 'off'
  if (!check) return 'unchecked'
  if (check.ok) return 'ok'
  return isAuthError(check.error) ? 'auth' : 'error'
}

export const STATE_FILTERS = ['all', 'on', 'attention', 'off']

export function matchesFilter(state, filter) {
  if (filter === 'on') return state !== 'off'
  if (filter === 'attention') return state === 'auth' || state === 'error'
  if (filter === 'off') return state === 'off'
  return true
}

// The connectors a search and a state filter leave, with the count of each
// filter for its label. A search matches the name and the host or command.
export function filterConnectors(items, { query = '', filter = 'all', checks = {} } = {}) {
  const q = query.trim().toLowerCase()
  const found = (items || []).filter(c => !q || c.name.toLowerCase().includes(q) || mcpTarget(c).toLowerCase().includes(q))
  const states = new Map(found.map(c => [c.id, connectorState(c, currentCheck(c, checks))]))
  const counts = Object.fromEntries(STATE_FILTERS.map(f => [f, found.filter(c => matchesFilter(states.get(c.id), f)).length]))
  return { rows: found.filter(c => matchesFilter(states.get(c.id), filter)), counts, states }
}

// Tools that match a search of the details pane, by name or description.
export function filterTools(tools, query) {
  const q = (query || '').trim().toLowerCase()
  if (!q) return tools || []
  return (tools || []).filter(x => x.name.toLowerCase().includes(q) || (x.description || '').toLowerCase().includes(q))
}

export const connectorRef = (id) => PREFIX + id

// A saved agent holds one list: model servers by name, connectors as connector:<id>.
export function splitAgentMCP(list) {
  const servers = []
  const connectors = []
  for (const x of list || []) {
    if (x.startsWith(PREFIX)) connectors.push(x.slice(PREFIX.length))
    else servers.push(x)
  }
  return { servers, connectors }
}

export function joinAgentMCP(servers, connectorIds) {
  return [...servers, ...connectorIds.map(connectorRef)]
}

// The list of what a chat switched off after one switch changed.
export function toggleOff(off, key, on) {
  const rest = (off || []).filter(x => x !== key)
  return on ? rest : [...rest, key]
}

// What is on among what can be used, for the summary of the tools panel.
export function toolsCounts(panel) {
  const addons = [...(panel.builtins || []), ...(panel.addons || [])]
  const mcp = [...(panel.servers || []), ...(panel.connectors || [])]
  const all = [...addons, ...mcp]
  return { on: all.filter(x => !x.off && x.usable !== false).length, total: all.length, mcp: mcp.length }
}

// --- the servers the old chat page kept in this browser ---

export const BROWSER_KEY = 'localai_client_mcp_servers'

const hasControl = (s) => /[\x00-\x1f\x7f]/.test(s)

function validServer(s) {
  if (!s || typeof s !== 'object' || typeof s.url !== 'string') return null
  let u
  try { u = new URL(s.url) } catch { return null }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
  const headers = {}
  if (s.headers && typeof s.headers === 'object' && !Array.isArray(s.headers)) {
    for (const [k, v] of Object.entries(s.headers)) {
      if (typeof v === 'string' && !hasControl(k) && !hasControl(v)) headers[k] = v
    }
  }
  return { id: String(s.id || ''), name: typeof s.name === 'string' ? s.name.trim() : '', url: s.url.trim(), headers, headerCount: Object.keys(headers).length }
}

// The servers of the old page, validated. The result holds the header values the
// import needs; a page shows only name, url and headerCount.
export function readBrowserServers(storage) {
  try {
    const data = JSON.parse(storage.getItem(BROWSER_KEY) || 'null')
    return Array.isArray(data) ? data.map(validServer).filter(Boolean) : []
  } catch {
    return []
  }
}

// A name the user does not have yet: the old name, or the host, then " (2)", " (3)".
export function browserImportName(server, taken) {
  let base = server.name
  if (!base) { try { base = new URL(server.url).hostname } catch { base = 'MCP server' } }
  base = base.slice(0, 70)
  if (!taken.includes(base)) return base
  for (let n = 2; ; n++) {
    const name = `${base} (${n})`
    if (!taken.includes(name)) return name
  }
}

// The request that makes a switched-off connector from an old entry. The old
// page kept the bearer token as a header, so the headers carry it as it was.
export function browserImportBody(server, taken) {
  const body = { name: browserImportName(server, taken), transport: 'http', url: server.url }
  if (server.headers && Object.keys(server.headers).length) body.headers = server.headers
  body.enabled = false
  return body
}

// Removes the imported entries from the browser; the key goes when none is left.
export function forgetBrowserServers(storage, ids) {
  try {
    const data = JSON.parse(storage.getItem(BROWSER_KEY) || 'null')
    if (!Array.isArray(data)) return
    const left = data.filter(s => !(s && ids.includes(String(s.id || ''))))
    if (left.length === 0) storage.removeItem(BROWSER_KEY)
    else storage.setItem(BROWSER_KEY, JSON.stringify(left))
  } catch { /* storage blocked: nothing to forget */ }
}
