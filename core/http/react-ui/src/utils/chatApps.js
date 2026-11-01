// MCP Apps in the chats: the pure parts. An app is an HTML page that an MCP
// server offers for a tool; the chat shows it in a sandboxed frame and talks to
// it with JSON-RPC over postMessage. The server holds the MCP connection: the
// page is read and its tool calls run through two routes of the chat, so this
// module does no network and touches no DOM.

export const PROTOCOL = '2026-01-26'
export const MIN_HEIGHT = 60
export const MAX_HEIGHT = 600
export const DEFAULT_HEIGHT = 200

const MAX_METHOD = 100
const MAX_TOOL_NAME = 200
const MAX_ARGS_BYTES = 16 * 1024
const MAX_MESSAGE = 4000
const MAX_URL = 2048
const MAX_IN_FLIGHT = 4

// A source the page may name in its policy: an https or wss origin, with at most
// one wildcard label. Anything else could change the meaning of the header.
const ORIGIN = /^(https|wss):\/\/(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$/
const MAX_DOMAINS = 20

function origins(list) {
  if (!Array.isArray(list)) return []
  const out = []
  for (const d of list) {
    if (typeof d === 'string' && ORIGIN.test(d) && !out.includes(d)) out.push(d)
    if (out.length === MAX_DOMAINS) break
  }
  return out
}

// The content security policy of the frame. The default reaches no network. The
// page asks for origins in the metadata of its resource (csp.connectDomains,
// resourceDomains, frameDomains, baseUriDomains); only clean origins are used.
export function buildCsp(meta) {
  const c = meta?.csp || {}
  const connect = origins(c.connectDomains)
  const res = origins(c.resourceDomains)
  const frame = origins(c.frameDomains)
  const base = origins(c.baseUriDomains)
  const list = (extra, first = []) => [...first, ...extra].join(' ') || "'none'"
  return [
    "default-src 'none'",
    `script-src ${list(res, ["'unsafe-inline'"])}`,
    `style-src ${list(res, ["'unsafe-inline'"])}`,
    `img-src ${list(res, ['data:', 'blob:'])}`,
    `font-src ${list(res, ['data:'])}`,
    `media-src ${list(res, ['data:', 'blob:'])}`,
    `connect-src ${list(connect)}`,
    `frame-src ${list(frame)}`,
    `base-uri ${list(base)}`,
    "form-action 'none'",
    "object-src 'none'",
  ].join('; ')
}

// The document of the frame: the policy, then the page. The policy goes before
// everything the page wrote, so no comment or markup in the page can hide it. A
// leading doctype stays first, so the page does not fall into quirks mode.
export function buildSrcdoc(html, meta) {
  return withHead(html, `<meta http-equiv="Content-Security-Policy" content="${buildCsp(meta)}">`)
}

// withHead puts head (trusted markup of the host) before everything the page
// wrote; a leading doctype stays first. The pages of the workspace use it too.
export function withHead(html, head) {
  const text = typeof html === 'string' ? html : ''
  const m = /^\s*<!doctype html>/i.exec(text)
  if (m) return m[0].trim() + head + text.slice(m[0].length)
  return '<!doctype html>' + head + text
}

// The text of a tool result, for when the page cannot be shown.
export function appText(result) {
  if (!result || !Array.isArray(result.content)) return ''
  return result.content.filter(b => b && b.type === 'text' && typeof b.text === 'string').map(b => b.text).join('\n')
}

// The host of an address, for the line that asks before a link is opened.
export function linkLabel(url) {
  try {
    return new URL(url).host
  } catch {
    return ''
  }
}

// Only a plain http or https address, without credentials, of a sane length.
function safeLink(url) {
  if (typeof url !== 'string' || url.length > MAX_URL) return null
  let u
  try {
    u = new URL(url)
  } catch {
    return null
  }
  if ((u.protocol !== 'https:' && u.protocol !== 'http:') || u.username || u.password || !u.host) return null
  return u.href
}

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)

function clampHeight(h) {
  if (typeof h !== 'number' || !Number.isFinite(h)) return null
  return Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, Math.ceil(h)))
}

const rpcError = (code, message) => ({ code, message })
const INVALID = (what) => rpcError(-32602, `invalid params: ${what}`)

// createHost is the host side of one app. The caller passes every message that
// the frame's window sent and checked (same source); receive validates the shape
// again and answers through post. Nothing here trusts the app: a message of the
// wrong shape is dropped, a request the host does not implement gets the
// JSON-RPC "method not found" error, and what the app can reach is the five
// handlers it is given.
//
//   post(message)            sends a JSON-RPC message to the app
//   input, result            the tool call the app belongs to
//   callTool(name, args)     runs a tool of the app's server (resolves to the MCP result)
//   onMessage(text)          the app suggests a message to send
//   onOpenLink(url)          the app asks to open an address
//   onSize(height)           the app wants another height (already bounded)
export function createHost({ post, input, result, callTool, onMessage, onOpenLink, onSize, theme = 'light' }) {
  let state = 'new' // new, initialized, closed
  let sentInput = false
  let inFlight = 0

  const reply = (id, res) => { if (state !== 'closed') post({ jsonrpc: '2.0', id, result: res }) }
  const fail = (id, err) => { if (state !== 'closed') post({ jsonrpc: '2.0', id, error: err }) }
  const notify = (method, params) => { if (state !== 'closed') post({ jsonrpc: '2.0', method, params }) }

  function initialize() {
    state = state === 'closed' ? state : 'initialized'
    return {
      protocolVersion: PROTOCOL,
      hostInfo: { name: 'LocalAI', version: '1.0.0' },
      hostCapabilities: { openLinks: {}, serverTools: {}, message: { text: {} } },
      hostContext: {
        theme,
        displayMode: 'inline',
        availableDisplayModes: ['inline'],
        containerDimensions: { maxHeight: MAX_HEIGHT },
        platform: 'web',
      },
    }
  }

  async function request(id, method, params) {
    if (method === 'ping') return reply(id, {})
    if (method === 'ui/initialize') return reply(id, initialize())
    if (state !== 'initialized') return fail(id, rpcError(-32002, 'the app must send ui/initialize first'))
    switch (method) {
      case 'tools/call': {
        const p = isObject(params) ? params : {}
        if (typeof p.name !== 'string' || p.name === '' || p.name.length > MAX_TOOL_NAME) return fail(id, INVALID('name'))
        if (p.arguments !== undefined && !isObject(p.arguments)) return fail(id, INVALID('arguments must be an object'))
        const args = p.arguments || {}
        if (JSON.stringify(args).length > MAX_ARGS_BYTES) return fail(id, INVALID('arguments are too large'))
        if (inFlight >= MAX_IN_FLIGHT) return fail(id, rpcError(-32000, 'too many tool calls at once'))
        inFlight++
        try {
          reply(id, await callTool(p.name, args))
        } catch (e) {
          fail(id, rpcError(-32000, String(e?.message || 'the tool call failed').slice(0, 500)))
        } finally {
          inFlight--
        }
        return undefined
      }
      case 'ui/message': {
        const p = isObject(params) ? params : {}
        const blocks = Array.isArray(p.content) ? p.content : null
        if (p.role !== 'user' || !blocks || blocks.length === 0 || !blocks.every(b => isObject(b) && b.type === 'text' && typeof b.text === 'string')) {
          return fail(id, INVALID('a user message of text'))
        }
        const text = blocks.map(b => b.text).join('\n').trim()
        if (text === '' || text.length > MAX_MESSAGE) return fail(id, INVALID('the message is empty or too long'))
        onMessage(text)
        return reply(id, {})
      }
      case 'ui/open-link': {
        const href = safeLink(isObject(params) ? params.url : undefined)
        if (!href) return reply(id, { isError: true })
        onOpenLink(href)
        return reply(id, {})
      }
      case 'ui/request-display-mode':
        return reply(id, { mode: 'inline' })
      default:
        return fail(id, rpcError(-32601, `method not found: ${method}`))
    }
  }

  function notification(method, params) {
    if (state !== 'initialized') return
    if (method === 'ui/notifications/initialized' && !sentInput) {
      sentInput = true
      if (input !== undefined) notify('ui/notifications/tool-input', { arguments: input })
      if (result !== undefined) notify('ui/notifications/tool-result', result)
    } else if (method === 'ui/notifications/size-changed') {
      const h = clampHeight(isObject(params) ? params.height : undefined)
      if (h !== null) onSize(h)
    }
  }

  return {
    async receive(data) {
      if (state === 'closed' || !isObject(data) || data.jsonrpc !== '2.0') return
      if (typeof data.method !== 'string' || data.method.length > MAX_METHOD || !/^[A-Za-z0-9_/.-]+$/.test(data.method)) return
      if ('id' in data) {
        if (typeof data.id !== 'string' && typeof data.id !== 'number') return
        await request(data.id, data.method, data.params)
      } else {
        notification(data.method, data.params)
      }
    },
    close() { state = 'closed' },
  }
}
