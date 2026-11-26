// Helpers of the Kubernetes page. No React, so a node test drives them. The
// add-on checks everything again; these only help the person see a problem
// before they save.
//
// The one sealed credential of a connection is a kubeconfig. The resource of
// the connection is <server>#context=<name>: the API server and the context of
// the kubeconfig the add-on uses. The page has no YAML library: it reads the
// shape kubectl writes with a line scanner, and gives up (an empty answer)
// on anything else, so the person types the context and the server instead.

export const MAX_CREDENTIAL = 16384
export const MAX_KEY = 200
export const TOKEN_CONTEXT = 'localai'

export const METHODS = ['kubeconfig', 'token']
export const methodOf = (m) => (METHODS.includes(m) ? m : 'kubeconfig')

const byteLength = (text) => new TextEncoder().encode(String(text || '')).length
export const credentialBytes = byteLength

// A YAML scalar as written: quotes removed, a trailing comment dropped.
function scalar(raw) {
  const v = String(raw || '').trim()
  if (v.startsWith('"') && v.endsWith('"') && v.length >= 2) {
    try { return JSON.parse(v) } catch (_e) { return null }
  }
  if (v.startsWith("'") && v.endsWith("'") && v.length >= 2) return v.slice(1, -1).replace(/''/g, "'")
  return v.replace(/\s+#.*$/, '')
}

const indentOf = (line) => line.length - line.trimStart().length
const meaningful = (line) => line.trim() !== '' && !line.trimStart().startsWith('#')

// The items of a top-level list (clusters:, contexts:) as flat maps: the
// keys of the item and the keys of its one nested map (cluster:, context:).
// null when the block has a shape this scanner does not read.
function topList(lines, name) {
  const start = lines.findIndex((l) => new RegExp(`^${name}:\\s*(#.*)?$`).test(l))
  if (start < 0) {
    // A key with an inline value (a flow list, null) is a shape we do not read,
    // unless it is an empty list.
    const inline = lines.find((l) => l.startsWith(`${name}:`))
    if (inline && /^[a-z-]+:\s*\[\s*\]\s*$/.test(inline)) return []
    return inline ? null : []
  }
  const items = []
  let item = null
  let dash = -1
  let nested = -1
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i]
    if (!meaningful(line)) continue
    const ind = indentOf(line)
    const body = line.trim()
    if (ind === 0 && !body.startsWith('- ')) break
    if (body.startsWith('- ')) {
      if (dash < 0) dash = ind
      if (ind !== dash) return null
      item = {}
      items.push(item)
      nested = -1
      const rest = body.slice(2)
      const m = /^([A-Za-z-]+):\s*(.*)$/.exec(rest)
      if (!m) return null
      if (m[2] === '') nested = 1
      else item[m[1]] = scalar(m[2])
      continue
    }
    if (!item) return null
    const m = /^([A-Za-z-]+):\s*(.*)$/.exec(body)
    if (!m) continue
    if (ind === dash + 2) {
      nested = m[2] === '' ? ind : -1
      if (m[2] !== '') item[m[1]] = scalar(m[2])
    } else if (ind > dash + 2 && m[2] !== '') {
      // A key of the nested map. Deeper blocks (exec:, extensions:) also land
      // here; only server and cluster are ever read.
      const key = `.${m[1]}`
      if (!(key in item)) item[key] = scalar(m[2])
    }
  }
  return items
}

function parse(text) {
  const raw = String(text || '')
  if (!raw.trim()) return null
  if (raw.trimStart().startsWith('{')) {
    try {
      const j = JSON.parse(raw)
      const flat = (list, inner) => (Array.isArray(list) ? list : []).map((x) => {
        const o = { name: x?.name }
        for (const [k, v] of Object.entries(x?.[inner] || {})) o[`.${k}`] = v
        return o
      })
      return { contexts: flat(j.contexts, 'context'), clusters: flat(j.clusters, 'cluster'), current: typeof j['current-context'] === 'string' ? j['current-context'] : '' }
    } catch (_e) { return null }
  }
  const lines = raw.replace(/\r\n?/g, '\n').split('\n')
  const contexts = topList(lines, 'contexts')
  const clusters = topList(lines, 'clusters')
  if (!contexts || !clusters) return null
  const cur = lines.find((l) => /^current-context:/.test(l))
  return { contexts, clusters, current: cur ? scalar(cur.slice('current-context:'.length)) || '' : '' }
}

// The contexts of a kubeconfig and its current one. Empty when the text has
// a shape the page cannot read.
export function kubeconfigContexts(text) {
  const empty = { contexts: [], current: '' }
  const cfg = parse(text)
  if (!cfg) return empty
  const names = []
  for (const c of cfg.contexts) {
    if (typeof c.name !== 'string' || !c.name) return empty
    if (!names.includes(c.name)) names.push(c.name)
  }
  return { contexts: names, current: names.includes(cfg.current) ? cfg.current : '' }
}

// The server URL one way, as the add-on writes it: https, host in lower case,
// no path. '' when it is not such a URL.
export function normalizeServer(s) {
  const m = /^https:\/\/([^/?#@\s]+)\/?$/i.exec(String(s || '').trim())
  if (!m) return ''
  // As the add-on writes it: host in lower case without trailing dots, an IP
  // address in its canonical form (a mapped IPv4 address as IPv4), the port
  // read as a number and left out when it is 443. The add-on keys the policy
  // by this form.
  const hp = /^(\[[^\]]+\]|[^:[\]]+)(?::(\d+))?$/.exec(m[1].toLowerCase())
  if (!hp) return ''
  let host = hp[1].replace(/\.+$/, '')
  if (!host) return ''
  if (host.startsWith('[')) {
    const ip = canonicalIPv6(host.slice(1, -1))
    if (!ip) return ''
    host = ip.includes(':') ? `[${ip}]` : ip
  }
  let port = ''
  if (hp[2] !== undefined) {
    const n = Number(hp[2])
    if (!Number.isInteger(n) || n < 1 || n > 65535) return ''
    if (n !== 443) port = `:${n}`
  }
  return `https://${host}${port}`
}

// The canonical text of an IPv6 address (RFC 5952), or an IPv4 address when
// it is an IPv4 address mapped into IPv6. '' when it is not an address.
export function canonicalIPv6(text) {
  let t = String(text || '').toLowerCase()
  if (!t || t.includes('%')) return ''
  let tail = []
  const v4 = /^(.*:)(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(t)
  if (v4) {
    const b = v4.slice(2).map(Number)
    if (b.some((x, i) => x > 255 || (v4[i + 2].length > 1 && v4[i + 2][0] === '0'))) return ''
    tail = [(b[0] << 8) | b[1], (b[2] << 8) | b[3]]
    t = v4[1].endsWith('::') ? v4[1] : v4[1].slice(0, -1)
  }
  const halves = t.split('::')
  if (halves.length > 2) return ''
  const part = (x) => (x === '' ? [] : x.split(':'))
  const head = part(halves[0])
  const rest = halves.length === 2 ? part(halves[1]) : []
  if ([...head, ...rest].some((g) => !/^[0-9a-f]{1,4}$/.test(g))) return ''
  const want = 8 - tail.length
  const given = head.length + rest.length
  if (halves.length === 2 ? given >= want : given !== want) return ''
  const groups = [...head.map((g) => parseInt(g, 16)), ...Array(want - given).fill(0), ...rest.map((g) => parseInt(g, 16)), ...tail]
  if (groups.slice(0, 5).every((g) => g === 0) && groups[5] === 0xffff) {
    return [groups[6] >> 8, groups[6] & 255, groups[7] >> 8, groups[7] & 255].join('.')
  }
  let best = -1
  let len = 0
  for (let i = 0; i < 8;) {
    if (groups[i] !== 0) { i++; continue }
    let j = i
    while (j < 8 && groups[j] === 0) j++
    if (j - i > len && j - i >= 2) { best = i; len = j - i }
    i = j
  }
  const hex = groups.map((g) => g.toString(16))
  if (best < 0) return hex.join(':')
  return `${hex.slice(0, best).join(':')}::${hex.slice(best + len).join(':')}`
}

// The server of a context: its cluster, then that cluster's server. '' when unsure.
export function kubeconfigServer(text, context) {
  const cfg = parse(text)
  if (!cfg || !context) return ''
  const ctx = cfg.contexts.filter((c) => c.name === context)
  if (ctx.length !== 1) return ''
  const cluster = ctx[0]['.cluster']
  const cl = cfg.clusters.filter((c) => c.name === cluster)
  if (!cluster || cl.length !== 1) return ''
  return normalizeServer(cl[0]['.server'])
}

// Fields the add-on refuses. The check covers the whole text, not only the
// chosen context: a kubeconfig made for LocalAI has one context (kubectl
// config view --minify --flatten).
const key = (names) => new RegExp(`^\\s*(?:-\\s+)?["']?(?:${names})["']?\\s*:`, 'm')
const PROBLEMS = [
  ['plugin', key('exec|auth-provider')],
  ['fileRef', key('certificate-authority|client-certificate|client-key|tokenFile')],
  ['insecure', /^\s*(?:-\s+)?["']?insecure-skip-tls-verify["']?\s*:\s*["']?true["']?\s*,?\s*(#.*)?$/m],
  ['proxy', key('proxy-url')],
  ['basicAuth', key('username|password')],
]

// What is wrong with a pasted kubeconfig, as a key of the page's words, or ''.
export function kubeconfigProblem(text) {
  const t = String(text || '')
  if (!t) return ''
  if (byteLength(t) > MAX_CREDENTIAL) return 'tooLarge'
  const body = t.split('\n').filter((l) => !l.trimStart().startsWith('#')).join('\n')
  for (const [name, re] of PROBLEMS) if (re.test(body)) return name
  return ''
}

function base64(text) {
  let bin = ''
  for (const b of new TextEncoder().encode(text)) bin += String.fromCharCode(b)
  return btoa(bin)
}

// A kubeconfig with one context (localai) from a server, a CA in PEM and a
// bearer token. '' when the server or the token is missing. Without a CA the
// add-on checks the server against the system roots.
export function tokenKubeconfig({ server, ca, token }) {
  const s = normalizeServer(server)
  const tok = String(token || '').trim()
  if (!s || !tok || /[\u0000-\u001f\u007f]/.test(tok)) return ''
  const pem = String(ca || '').trim()
  const cluster = [
    '- cluster:',
    ...(pem ? [`    certificate-authority-data: ${base64(`${pem}\n`)}`] : []),
    `    server: ${JSON.stringify(s)}`,
    `  name: ${TOKEN_CONTEXT}`,
  ]
  return [
    'apiVersion: v1',
    'kind: Config',
    'clusters:',
    ...cluster,
    'contexts:',
    '- context:',
    `    cluster: ${TOKEN_CONTEXT}`,
    `    user: ${TOKEN_CONTEXT}`,
    `  name: ${TOKEN_CONTEXT}`,
    `current-context: ${TOKEN_CONTEXT}`,
    'users:',
    `- name: ${TOKEN_CONTEXT}`,
    '  user:',
    `    token: ${JSON.stringify(tok)}`,
    '',
  ].join('\n')
}

const CONTEXT_MARK = '#context='
const validContext = (c) => typeof c === 'string' && c !== '' && !/[\s\u0000-\u001f\u007f]/.test(c)

// The resource key of a connection, or '' when the parts do not make one.
export function keyOf(server, context) {
  const s = normalizeServer(server)
  if (!s || !validContext(context)) return ''
  const k = `${s}${CONTEXT_MARK}${context}`
  return Array.from(k).length > MAX_KEY ? '' : k
}

// The parts of a saved key.
export function splitKey(k) {
  const text = String(k || '')
  const at = text.indexOf(CONTEXT_MARK)
  if (at < 0) return { server: '', context: '' }
  const server = normalizeServer(text.slice(0, at))
  const context = text.slice(at + CONTEXT_MARK.length)
  if (!server || !validContext(context)) return { server: '', context: '' }
  return { server, context }
}

// The write mode of the host. The host holds a write for the person to
// confirm in "propose" and applies it at once in "auto". "off" refuses it.
export const WRITE_MODES = ['off', 'propose', 'auto']
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// The context and server the kubeconfig form names: the typed context, or
// the only or current one; the server from the kubeconfig, or the typed one.
export function kubeconfigTarget(form) {
  const { contexts, current } = kubeconfigContexts(form.kubeconfig)
  const context = form.context || (contexts.length === 1 ? contexts[0] : current)
  const found = kubeconfigServer(form.kubeconfig, context)
  return { context: context || '', server: found || normalizeServer(form.server), serverFound: Boolean(found) }
}

// The server and the context the form names, by method.
export function targetOf(form) {
  if (methodOf(form.method) === 'token') {
    if (!form.token && form.id) return { server: normalizeServer(form.server), context: form.context || TOKEN_CONTEXT }
    return { server: normalizeServer(form.server), context: TOKEN_CONTEXT }
  }
  if (!form.kubeconfig) return { server: normalizeServer(form.server), context: form.context || '' }
  const t = kubeconfigTarget(form)
  return { server: t.server, context: t.context }
}

// The secret the fields make: the kubeconfig text. '' when nothing is typed
// or something is wrong.
export function kubeSecret(form) {
  if (methodOf(form.method) === 'token') {
    const text = tokenKubeconfig({ server: form.server, ca: form.ca, token: form.token })
    return text && !kubeconfigProblem(text) ? text : ''
  }
  const text = String(form.kubeconfig || '')
  if (!text.trim() || kubeconfigProblem(text)) return ''
  return text
}

// The values of the extra fields. A new connection starts on "propose": the
// person confirms each write. A saved one shows what it has.
export const extraForm = (c) => {
  const { server, context } = splitKey((c.resources || [])[0] || '')
  return {
    method: 'kubeconfig',
    kubeconfig: '',
    context,
    server,
    ca: '',
    token: '',
    write_mode: c.id ? writeModeOf(c.write_mode) : 'propose',
  }
}

export const extraBody = (form) => {
  const { server, context } = targetOf(form)
  const k = keyOf(server, context)
  return { resources: k ? [k] : [], write_mode: writeModeOf(form.write_mode) }
}

// Whether the form may be saved: a key, and a credential the add-on accepts.
// A saved connection may keep its credential when nothing new is typed.
export const extraValid = (form) => {
  const { server, context } = targetOf(form)
  if (!keyOf(server, context)) return false
  const typed = methodOf(form.method) === 'token' ? Boolean(form.token || form.ca) : Boolean(form.kubeconfig)
  if (!typed) return Boolean(form.id)
  return Boolean(kubeSecret(form))
}

// What the Test button shows first: { tone, key, vars }.
export function checkItem(r) {
  if (r?.failed && r.failed !== 'other') return { tone: 'warn', key: `check.${r.failed}`, vars: {} }
  if (!r || r.ok !== true) return { tone: 'warn', key: 'check.failed', vars: {} }
  return { tone: 'ok', key: 'check.ok', vars: { version: String(r.check?.version || '') || '?' } }
}

// Why a call of check failed, as the failed field of checkItem. The add-on's
// refusals each have their own sentence; the page matches on it.
export function checkFailure(e) {
  const text = String(e?.message || '')
  if (/\b401\b|refused the credential/i.test(text)) return 'unauthorized'
  if (/\b403\b|no RBAC permission/i.test(text)) return 'forbidden'
  if (/has no kubeconfig/i.test(text)) return 'nocred'
  if (/points at another server|has no context of the connection/i.test(text)) return 'mismatch'
  if (/cannot be used|is not supported|larger than/i.test(text)) return 'unsafe'
  if (/dial tcp|connection refused|no such host|timeout|timed out|could not be reached|x509|certificate/i.test(text)) return 'unreachable'
  return 'other'
}

const MAX_ROWS = 20

// The RBAC rules of a check as rows a person reads: at most 20, and how many
// more there are.
export function rbacRows(rules) {
  const list = Array.isArray(rules) ? rules : []
  const words = (a) => (Array.isArray(a) ? a.filter((x) => typeof x === 'string') : [])
  const rows = list.slice(0, MAX_ROWS).map((r) => {
    const groups = words(r?.apiGroups).filter((g) => g !== '')
    const res = words(r?.resources).flatMap((name) => (groups.length ? groups.map((g) => `${name}.${g}`) : [name]))
    const names = words(r?.resourceNames)
    return {
      verbs: words(r?.verbs).join(', '),
      resources: `${res.join(', ') || '-'}${names.length ? ` (${names.join(', ')})` : ''}`,
    }
  })
  return { rows, more: Math.max(0, list.length - MAX_ROWS) }
}

// The name a new connection gets from its context.
export const defaultName = (context) => (context ? `Kubernetes: ${context}` : '')

// The name after the context changed: an empty name, or one that this page
// made itself from the earlier context, follows the context.
export function followName(name, oldContext, newContext) {
  const current = String(name || '')
  if (current !== '' && current !== defaultName(oldContext)) return current
  return defaultName(newContext)
}

// The form after a change of the credential fields, with the name following.
export const withName = (form, next) => ({ ...next, name: followName(form.name, targetOf(form).context, targetOf(next).context) })
