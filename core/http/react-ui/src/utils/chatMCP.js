// Helpers of the MCP connectors, the tools panel and the saved-agent picker. No
// React, so a node test drives them. A secret (token, header value, variable)
// only goes in: nothing here reads one back from the server.

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

export function canSaveMCP(form) {
  if (!(form.name || '').trim()) return false
  if (form.transport === 'stdio') return Boolean((form.command || '').trim())
  return Boolean((form.url || '').trim())
}

// Where a connector points, for the list: the host of an address (never its path
// or query, which may hold a key) or the name of a command.
export function mcpTarget(c) {
  if (c.transport === 'stdio') return (c.command || '').split('/').filter(Boolean).pop() || ''
  try { return new URL(c.url).hostname || c.url } catch { return c.url || '' }
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
