// The pages of the workspace: the frame a page runs in and the host side of its
// bridge. A page is HTML from the user's files, so it is untrusted: it runs in
// <iframe sandbox="allow-scripts"> with an opaque origin, under a policy that
// reaches no network, and talks to LocalAI only through a MessageChannel that
// the host hands over when the frame loads. No network and no DOM here.

import { withHead } from './chatApps.js'

export const MAX_TEXT = 4000
export const MAX_PATH = 256
// A file is at most 1 MiB; a string of that many UTF-16 units is at least as
// many bytes, and the server checks the bytes.
export const MAX_CONTENT = 1 << 20
const MAX_IN_FLIGHT = 8

// The policy of every page: inline script and style, images and fonts as data:
// URLs, and nothing that reaches out.
export const PAGE_CSP = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data:',
  'font-src data:',
  "connect-src 'none'",
  "frame-src 'none'",
  "form-action 'none'",
  "base-uri 'none'",
  "object-src 'none'",
].join('; ')

// The bridge inside the frame. INFO is replaced with the page's name, title
// and theme. It is plain ES5 and must never contain a closing script tag.
const BOOTSTRAP = `(function () {
  'use strict'
  var info = INFO
  document.documentElement.setAttribute('data-theme', info.theme)
  document.documentElement.style.colorScheme = info.theme
  var port = null, queue = [], seq = 0, waiting = {}, listeners = { 'files.changed': [] }
  function deliver(ev) {
    var m = ev.data
    if (!m || typeof m !== 'object') return
    if (typeof m.id === 'number' && waiting[m.id]) {
      var w = waiting[m.id]
      delete waiting[m.id]
      if (m.error) w[1](new Error(String(m.error.message || 'the call failed')))
      else w[0](m.result)
      return
    }
    if (typeof m.event === 'string' && listeners[m.event]) {
      listeners[m.event].slice().forEach(function (fn) {
        try { fn(m.data) } catch (err) { setTimeout(function () { throw err }) }
      })
    }
  }
  window.addEventListener('message', function (e) {
    if (port || e.source !== window.parent || !e.data || e.data.type !== 'localai:init' || !e.ports || e.ports.length !== 1) return
    port = e.ports[0]
    port.onmessage = deliver
    for (var i = 0; i < queue.length; i++) port.postMessage(queue[i])
    queue = []
  })
  function call(method, params) {
    return new Promise(function (resolve, reject) {
      var id = ++seq
      waiting[id] = [resolve, reject]
      var m = { id: id, method: method, params: params }
      if (port) port.postMessage(m)
      else queue.push(m)
    })
  }
  var api = {
    page: Object.freeze({ name: info.name, title: info.title }),
    files: Object.freeze({
      read: function (path) { return call('files.read', { path: path }) },
      write: function (path, content, opts) { return call('files.write', { path: path, content: content, base: opts && opts.base }) },
      list: function (dir) { return call('files.list', { dir: dir || '' }) }
    }),
    chat: Object.freeze({ send: function (text) { return call('chat.send', { text: text }) } }),
    on: function (event, fn) {
      if (!listeners[event] || typeof fn !== 'function') throw new TypeError('localai.on: unknown event ' + event)
      listeners[event].push(fn)
      return function () {
        var i = listeners[event].indexOf(fn)
        if (i >= 0) listeners[event].splice(i, 1)
      }
    }
  }
  Object.defineProperty(window, 'localai', { value: Object.freeze(api), enumerable: true })
})()`

// JSON that is safe inside a script element: no "<", and no line separators
// that older engines read as a line end.
function scriptJSON(v) {
  return JSON.stringify(v).replace(/</g, '\\u003c').replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029')
}

// The document of the frame: the policy, then the bridge, then the page. Both
// go before the page's own markup, so nothing the page wrote runs first or can
// hide the policy.
export function buildPageSrcdoc(html, { name, title, theme } = {}) {
  const info = scriptJSON({ name: String(name || ''), title: String(title || name || ''), theme: theme === 'dark' ? 'dark' : 'light' })
  const head = `<meta http-equiv="Content-Security-Policy" content="${PAGE_CSP}"><script>${BOOTSTRAP.replace('INFO', () => info)}</script>`
  return withHead(html, head)
}

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)
const bad = (what) => ({ code: 'invalid', message: `invalid params: ${what}` })
const okPath = (p, optional) => (optional && (p === undefined || p === '')) || (typeof p === 'string' && p !== '' && p.length <= MAX_PATH)

// createPageHost is the host side of one page's port. Every message from the
// frame goes to receive, which checks its shape and answers through post with
// {id, result} or {id, error}. What a page can reach is the four handlers:
//
//   files.read(path), files.write(path, content, base), files.list(dir)
//   chat.send(text)   resolves to {sent, chat_id}; the host asks the person first
//
// The server checks every path again with the user's rights; the checks here
// only refuse a malformed call early.
export function createPageHost({ post, files, chat }) {
  let closed = false
  let inFlight = 0

  const answer = (msg) => { if (!closed) post(msg) }

  async function dispatch(method, p) {
    switch (method) {
      case 'files.read':
        if (!okPath(p.path)) throw bad('path')
        return files.read(p.path)
      case 'files.write':
        if (!okPath(p.path)) throw bad('path')
        if (typeof p.content !== 'string') throw bad('content must be a string')
        if (p.content.length > MAX_CONTENT) throw { code: 'too_large', message: 'the file is larger than 1 MiB' }
        if (p.base !== undefined && p.base !== null && !(Number.isInteger(p.base) && p.base >= 0)) throw bad('base')
        return files.write(p.path, p.content, p.base || 0)
      case 'files.list':
        if (!okPath(p.dir, true)) throw bad('dir')
        return files.list(p.dir || '')
      case 'chat.send': {
        const text = typeof p.text === 'string' ? p.text.trim() : ''
        if (text === '' || text.length > MAX_TEXT) throw bad('text is empty or longer than 4000 characters')
        return chat.send(text)
      }
      default:
        throw { code: 'unknown', message: `unknown method: ${method}` }
    }
  }

  return {
    async receive(data) {
      if (closed || !isObject(data) || !Number.isInteger(data.id) || data.id < 1) return
      const { id, method } = data
      if (typeof method !== 'string' || method.length > 40) return answer({ id, error: bad('method') })
      if (inFlight >= MAX_IN_FLIGHT) return answer({ id, error: { code: 'busy', message: 'too many calls at once' } })
      inFlight++
      try {
        const result = await dispatch(method, isObject(data.params) ? data.params : {})
        answer({ id, result })
      } catch (e) {
        answer({ id, error: { code: e?.code || 'failed', message: String(e?.message || 'the call failed').slice(0, 500) } })
      } finally {
        inFlight--
      }
      return undefined
    },
    // Sends an event to the page: only files.changed exists.
    emit(event, payload) {
      if (event === 'files.changed') answer({ event, data: payload })
    },
    close() { closed = true },
  }
}
