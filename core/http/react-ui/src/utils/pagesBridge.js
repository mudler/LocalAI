// The pages of the workspace: the frame a page runs in and the host side of its
// bridge. A page is HTML from the user's files, so it is untrusted: it runs in
// <iframe sandbox="allow-scripts"> with an opaque origin, under a policy that
// reaches no network, and talks to LocalAI only through a MessageChannel that
// the host hands over when the frame loads. No network and no DOM here.

import { withHead } from './chatApps.js'
import { scriptJSON } from './pageAssets.js'
import { LINK_ERRORS, pageLinkTarget } from './pageLinks.js'

export const MAX_TEXT = 4000
export const MAX_PATH = 256
// A file is at most 1 MiB; a string of that many UTF-16 units is at least as
// many bytes, and the server checks the bytes.
export const MAX_CONTENT = 1 << 20
const MAX_IN_FLIGHT = 8
// Link opens of one page: a burst, then one every few seconds, so a page
// cannot bury the person in questions.
const LINK_BURST = 5
const LINK_EVERY_MS = 3000

// The policy of every page: inline script and style, images and fonts as data:
// URLs, and nothing that reaches out. A page with ES modules of its folder
// also gets blob: for scripts (see pageCsp): the host puts each module in as a
// blob: URL that the frame makes itself, which reaches no network.
export const PAGE_CSP = pageCsp(false)

export function pageCsp(modules) {
  return [
  "default-src 'none'",
  modules ? "script-src 'unsafe-inline' blob:" : "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data:',
  'font-src data:',
  "connect-src 'none'",
  "frame-src 'none'",
  "form-action 'none'",
  "base-uri 'none'",
  "object-src 'none'",
  ].join('; ')
}

// The bridge inside the frame. INFO is replaced with the page's name, title,
// theme, mode ('page', or 'widget' in a tile on Home) and tile size, the notes of the files that did not go in (written to the console)
// and the banner that says so in the page. It is plain ES5 and must never
// contain a closing script tag.
//
// It also gives fetch() and XMLHttpRequest the files of the page's folder: a
// relative URL (data.json, ./data/x.json?v=2) is read with files.read and
// answered with a Response (200, or 404 for a missing file); any other URL is
// refused with a TypeError, because the frame has no network. The resolution
// of a URL is the one of resolvePagePath in pageAssets.js; the unit tests
// check that both agree.
const BOOTSTRAP = `(function () {
  'use strict'
  var info = INFO
  document.documentElement.setAttribute('data-theme', info.theme)
  document.documentElement.setAttribute('data-mode', info.mode)
  if (info.size) document.documentElement.setAttribute('data-size', info.size)
  document.documentElement.style.colorScheme = info.theme
  var port = null, queue = [], seq = 0, waiting = {}, listeners = { 'files.changed': [], 'visibility': [] }
  function deliver(ev) {
    var m = ev.data
    if (!m || typeof m !== 'object') return
    if (typeof m.id === 'number' && waiting[m.id]) {
      var w = waiting[m.id]
      delete waiting[m.id]
      if (m.error) {
        var err = new Error(String(m.error.message || 'the call failed'))
        err.code = m.error.code
        if (typeof m.error.status === 'number') err.status = m.error.status
        w[1](err)
      } else w[0](m.result)
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
    page: Object.freeze({ name: info.name, title: info.title, mode: info.mode, size: info.size }),
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

  var NET_OFF = 'network is off in pages; put the data in a file next to the page'
  var TYPES = { json: 'application/json', geojson: 'application/geo+json', txt: 'text/plain', md: 'text/markdown', csv: 'text/csv', tsv: 'text/tab-separated-values',
    html: 'text/html', htm: 'text/html', css: 'text/css', js: 'text/javascript', mjs: 'text/javascript', svg: 'image/svg+xml', xml: 'application/xml', yaml: 'application/yaml', yml: 'application/yaml' }
  var STATUS = { 200: 'OK', 404: 'Not Found', 413: 'Payload Too Large', 415: 'Unsupported Media Type' }
  function typeOf(p) {
    var m = /\\.([a-z0-9]+)$/i.exec(p)
    var t = (m && TYPES[m[1].toLowerCase()]) || 'text/plain'
    return t + '; charset=utf-8'
  }
  // A relative URL to a path of the page's folder: {path} or {error}.
  function pagePath(ref) {
    var s = String(ref).trim()
    if (s === '' || s[0] === '#' || s[0] === '?') return { error: 'fetch needs a file name next to the page' }
    if (/^[a-z][a-z0-9+.-]*:/i.test(s) || /^[\\/\\\\]/.test(s)) return { error: NET_OFF + ' (' + s.slice(0, 80) + ')' }
    s = s.replace(/[?#][\\s\\S]*$/, '')
    var dec
    try { dec = decodeURIComponent(s) } catch (e) { return { error: 'not a valid file name: ' + s.slice(0, 80) } }
    if (/[\\\\:\\u0000-\\u001f\\u007f]/.test(dec) || dec[0] === '/') return { error: 'not a valid file name: ' + s.slice(0, 80) }
    var out = [], segs = dec.split('/')
    for (var i = 0; i < segs.length; i++) {
      if (segs[i] === '') return { error: 'not a valid file name: ' + s.slice(0, 80) }
      if (segs[i] === '.') continue
      if (segs[i] === '..') {
        if (!out.length) return { error: s.slice(0, 80) + ' is outside the page\\'s folder' }
        out.pop()
        continue
      }
      out.push(segs[i])
    }
    if (!out.length || out.length > 8 || out.join('/').length > 256) return { error: 'not a valid file name: ' + s.slice(0, 80) }
    return { path: out.join('/') }
  }
  // new URL('x', document.baseURI) and new Request('x') resolve against the
  // address of LocalAI's own window; under that folder is the page's folder.
  var baseDir = (function () {
    var b = String(document.baseURI || '').replace(/[?#][\\s\\S]*$/, '')
    return /^https?:/.test(b) ? b.slice(0, b.lastIndexOf('/') + 1) : ''
  })()
  function target(input) {
    var s = typeof input === 'string' ? input
      : (typeof Request === 'function' && input instanceof Request) ? input.url : String(input)
    if (baseDir && s.indexOf(baseDir) === 0) s = s.slice(baseDir.length)
    return pagePath(s)
  }
  var active = 0, pending = []
  function pump() {
    while (active < 4 && pending.length) {
      var job = pending.shift()
      active++
      call('files.read', { path: job[0] }).then(function (j) {
        return function (f) { active--; pump(); j[1](f) }
      }(job), function (j) {
        return function (e) { active--; pump(); j[2](e) }
      }(job))
    }
  }
  function readFile(path) {
    return new Promise(function (resolve, reject) { pending.push([path, resolve, reject]); pump() })
  }
  var known = { 404: 'not found: ', 413: 'larger than 1 MiB: ', 415: 'not a text file: ' }
  if (typeof Response === 'function') {
    window.fetch = function (input, init) {
      var method = String((init && init.method) || (input && typeof input === 'object' && input.method) || 'GET').toUpperCase()
      var signal = (init && init.signal) || (input && typeof input === 'object' && input.signal)
      var t = target(input)
      if (t.error) return Promise.reject(new TypeError(t.error))
      if (method !== 'GET' && method !== 'HEAD') return Promise.reject(new TypeError('fetch in a page only reads files (GET); save with localai.files.write'))
      if (signal && signal.aborted) return Promise.reject(new DOMException('The fetch was aborted', 'AbortError'))
      function respond(status, body, type) {
        var r = new Response(method === 'HEAD' ? null : body, { status: status, statusText: STATUS[status], headers: { 'content-type': type } })
        try { Object.defineProperty(r, 'url', { value: t.path }) } catch (e) { /* keep the empty url */ }
        return r
      }
      return readFile(t.path).then(function (f) {
        if (signal && signal.aborted) throw new DOMException('The fetch was aborted', 'AbortError')
        return respond(200, String(f.content), typeOf(t.path))
      }, function (e) {
        if (known[e.status]) return respond(e.status, known[e.status] + t.path, 'text/plain; charset=utf-8')
        throw new TypeError('could not read ' + t.path + ': ' + e.message)
      })
    }
  }

  function PageXHR() {
    this.readyState = 0; this.status = 0; this.statusText = ''; this.responseText = ''; this.response = ''
    this.responseType = ''; this.responseURL = ''; this.timeout = 0; this.withCredentials = false
    this.upload = { addEventListener: function () {}, removeEventListener: function () {} }
    this._l = {}; this._m = 'GET'; this._u = ''; this._ct = ''; this._run = 0
  }
  var X = PageXHR.prototype
  var states = ['UNSENT', 'OPENED', 'HEADERS_RECEIVED', 'LOADING', 'DONE']
  for (var si = 0; si < states.length; si++) { PageXHR[states[si]] = si; X[states[si]] = si }
  X.open = function (method, url, async) {
    if (async === false) throw new DOMException('synchronous XMLHttpRequest does not work in pages; use the async form', 'InvalidAccessError')
    this._m = String(method || 'GET').toUpperCase(); this._u = url; this._run++
    this.status = 0; this.statusText = ''; this.responseText = ''; this.response = ''; this._ct = ''
    this._state(1)
  }
  X.setRequestHeader = function () {}
  X.overrideMimeType = function () {}
  X.getResponseHeader = function (n) { return this.readyState >= 2 && String(n).toLowerCase() === 'content-type' ? this._ct || null : null }
  X.getAllResponseHeaders = function () { return this.readyState >= 2 && this._ct ? 'content-type: ' + this._ct + '\\r\\n' : '' }
  X.addEventListener = function (type, fn) { if (typeof fn === 'function') (this._l[type] = this._l[type] || []).push(fn) }
  X.removeEventListener = function (type, fn) {
    var l = this._l[type] || [], i = l.indexOf(fn)
    if (i >= 0) l.splice(i, 1)
  }
  X._fire = function (type) {
    var self = this, ev = { type: type, target: this, currentTarget: this, lengthComputable: false, loaded: 0, total: 0 }
    var fns = (typeof this['on' + type] === 'function' ? [this['on' + type]] : []).concat(this._l[type] || [])
    fns.forEach(function (fn) {
      try { fn.call(self, ev) } catch (err) { setTimeout(function () { throw err }) }
    })
  }
  X._state = function (n) { this.readyState = n; this._fire('readystatechange') }
  X.abort = function () {
    this._run++
    if (this.readyState > 0 && this.readyState < 4) { this._state(4); this._fire('abort'); this._fire('loadend') }
    this.readyState = 0
  }
  X.send = function () {
    if (this.readyState !== 1) throw new DOMException('send() needs open() first', 'InvalidStateError')
    var x = this, run = this._run, t = target(this._u)
    function fail(msg) {
      if (x._run !== run) return
      console.error('XMLHttpRequest: ' + msg)
      x._state(4); x._fire('error'); x._fire('loadend')
    }
    function done(status, text, type) {
      if (x._run !== run) return
      x.status = status; x.statusText = STATUS[status]; x.responseURL = t.path; x._ct = type
      x._state(2); x._state(3)
      var rt = x.responseType
      if (rt === 'json') { try { x.response = JSON.parse(text) } catch (e) { x.response = null } }
      else if (rt === 'arraybuffer') x.response = new TextEncoder().encode(text).buffer
      else if (rt === 'blob') x.response = new Blob([text], { type: type })
      else { x.responseText = text; x.response = text }
      x._state(4); x._fire('load'); x._fire('loadend')
    }
    this._fire('loadstart')
    if (t.error) return void setTimeout(function () { fail(t.error) })
    if (this._m !== 'GET' && this._m !== 'HEAD') return void setTimeout(function () { fail('a page only reads files (GET); save with localai.files.write') })
    readFile(t.path).then(function (f) { done(200, String(f.content), typeOf(t.path)) }, function (e) {
      if (known[e.status]) done(e.status, known[e.status] + t.path, 'text/plain; charset=utf-8')
      else fail('could not read ' + t.path + ': ' + e.message)
    })
  }
  window.XMLHttpRequest = PageXHR

  // Links: the sandbox opens no window and cannot leave the frame, so a link
  // to another site is asked to the host, which asks the person. A link to a
  // place in the page (#id) scrolls to it.
  function openLink(href) {
    var s = String(href == null ? '' : href).trim()
    if (s === '' || s[0] === '#') return false
    if (!/^[a-z][a-z0-9+.-]*:/i.test(s) && s.slice(0, 2) !== '//') {
      console.warn('[LocalAI page] the link ' + s.slice(0, 80) + ' opens nothing: a page has no other pages; use an http or https address')
      return true
    }
    call('link.open', { url: s }).then(null, function (e) { console.error('[LocalAI page] the link was not opened: ' + e.message) })
    return true
  }
  function onLinkClick(e) {
    if (e.defaultPrevented || (e.type === 'click' && e.button !== 0) || (e.type === 'auxclick' && e.button !== 1)) return
    var a = e.target && e.target.closest ? e.target.closest('a[href], area[href]') : null
    if (!a) return
    var href = String(a.getAttribute('href')).trim()
    e.preventDefault()
    // A srcdoc resolves #id against LocalAI's address, so the browser would
    // load that address in the frame: scroll to the element here instead.
    if (href[0] === '#') {
      var id = href.slice(1)
      try { id = decodeURIComponent(id) } catch (err) { /* keep it as written */ }
      var el = id ? document.getElementById(id) || document.getElementsByName(id)[0] : null
      if (el) el.scrollIntoView()
      else if (id === '' || id.toLowerCase() === 'top') window.scrollTo(0, 0)
      return
    }
    openLink(href)
  }
  document.addEventListener('click', onLinkClick)
  document.addEventListener('auxclick', onLinkClick)
  window.open = function (url) {
    openLink(url)
    return null
  }

  var notes = info.notes || []
  for (var ni = 0; ni < notes.length; ni++) (notes[ni].level === 'error' ? console.error : console.warn)('[LocalAI page] ' + notes[ni].message)
  if (info.banner) {
    var show = function () {
      var host = document.createElement('localai-notice')
      var root = host.attachShadow({ mode: 'closed' })
      var dark = info.theme === 'dark'
      var style = document.createElement('style')
      style.textContent = ':host{all:initial;position:fixed;left:12px;right:12px;bottom:12px;z-index:2147483647;display:flex;justify-content:center;pointer-events:none}' +
        '.n{pointer-events:auto;display:flex;gap:12px;align-items:flex-start;max-width:640px;box-sizing:border-box;padding:10px 12px;border-radius:8px;' +
        'border:1px solid ' + (dark ? '#5c4a1f' : '#e7cf8f') + ';border-left:4px solid ' + (dark ? '#e3b341' : '#b7791f') + ';' +
        'background:' + (dark ? '#1f1b12' : '#fffaf0') + ';color:' + (dark ? '#f0e6cf' : '#3d2f0f') + ';' +
        'font:13px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif;box-shadow:0 6px 24px rgba(0,0,0,.18)}' +
        'p{margin:0;flex:1;overflow-wrap:anywhere}button{all:unset;cursor:pointer;min-width:24px;min-height:24px;text-align:center;border-radius:4px;font-size:16px;line-height:24px}' +
        'button:focus-visible{outline:2px solid currentColor;outline-offset:1px}button:hover{background:rgba(127,127,127,.18)}'
      var box = document.createElement('div')
      box.className = 'n'
      box.setAttribute('role', 'alert')
      var p = document.createElement('p')
      p.textContent = info.banner
      var close = document.createElement('button')
      close.type = 'button'
      close.textContent = '\\u00d7'
      close.setAttribute('aria-label', info.dismiss || 'Dismiss')
      close.onclick = function () { host.remove() }
      box.appendChild(p); box.appendChild(close)
      root.appendChild(style); root.appendChild(box)
      document.documentElement.appendChild(host)
    }
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', show)
    else show()
  }
})()`

// The document of the frame: the policy, then the bridge, then the loader of
// the page's modules (head, from inlinePageAssets), then the page. All go
// before the page's own markup, so nothing the page wrote runs first or can
// hide the policy.
export const PAGE_MODES = ['page', 'widget']
export const WIDGET_SIZES = ['small', 'medium', 'large']

export function buildPageSrcdoc(html, { name, title, theme, head = '', modules = false, notes = [], banner = '', dismiss = '', mode = 'page', size = '' } = {}) {
  const widget = mode === 'widget'
  const info = scriptJSON({
    name: String(name || ''), title: String(title || name || ''), theme: theme === 'dark' ? 'dark' : 'light',
    mode: widget ? 'widget' : 'page', size: widget ? (WIDGET_SIZES.includes(size) ? size : 'medium') : '',
    notes: (Array.isArray(notes) ? notes : []).slice(0, 50).map((n) => ({ level: n.level === 'error' ? 'error' : 'warn', message: String(n.message || '').slice(0, 500) })),
    banner: String(banner || '').slice(0, 1000), dismiss: String(dismiss || ''),
  })
  const pre = `<meta http-equiv="Content-Security-Policy" content="${pageCsp(modules)}"><script>${BOOTSTRAP.replace('INFO', () => info)}</script>${head}`
  return withHead(html, pre)
}

// The files a page cannot write: code and documents, which the host would run
// or render on the next load. The same judgement as IsCodeFile in
// core/services/studio/pages/codefile.go; both run the table
// core/services/studio/pages/testdata/code-files.json. The server decides; this
// only refuses early.
const CODE_EXT = new Set(['js', 'mjs', 'cjs', 'css', 'html', 'htm', 'svg', 'xml', 'xhtml', 'wasm'])
export const CODE_WRITE_MESSAGE = 'a page can write data files only (such as .json or .txt); ask the assistant to change the code of the page'

export function isCodeFile(path) {
  let s = String(path)
  for (let i = 0; i < 3; i++) {
    let d
    try { d = decodeURIComponent(s) } catch { break }
    if (d === s) break
    s = d
  }
  s = s.normalize('NFKC').toLowerCase().replace(/[\\\u3002\uff61]/g, (c) => (c === '\\' ? '/' : '.'))
  s = s.slice(s.lastIndexOf('/') + 1)
  const colon = s.indexOf(':')
  if (colon >= 0) s = s.slice(0, colon)
  s = s.replace(/[ .\t\r\n\0]+$/, '')
  if (s === 'index' || s === 'page.json') return true
  const dot = s.lastIndexOf('.')
  return dot >= 0 && CODE_EXT.has(s.slice(dot + 1))
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
//   link.open(url)    resolves to {opened}; only http and https, and the host
//                     asks the person first (links.open gets the checked target)
//
// The server checks every path again with the user's rights; the checks here
// only refuse a malformed call early.
export function createPageHost({ post, files, chat, links, now = Date.now }) {
  let closed = false
  let inFlight = 0
  let linkTokens = LINK_BURST
  let linkAt = now()

  const answer = (msg) => { if (!closed) post(msg) }

  async function dispatch(method, p) {
    switch (method) {
      case 'files.read':
        if (!okPath(p.path)) throw bad('path')
        return files.read(p.path)
      case 'files.write':
        if (!okPath(p.path)) throw bad('path')
        if (isCodeFile(p.path)) throw { code: 'forbidden', message: CODE_WRITE_MESSAGE }
        if (typeof p.content !== 'string') throw bad('content must be a string')
        if (p.content.length > MAX_CONTENT) throw { code: 'too_large', message: 'the file is larger than 1 MiB' }
        if (p.base !== undefined && p.base !== null && !(Number.isInteger(p.base) && p.base >= 0)) throw bad('base')
        try {
          return await files.write(p.path, p.content, p.base || 0)
        } catch (e) {
          // The server refused the file with 403: answer as the early check does.
          if (e?.status === 403 && e?.body?.error?.type === 'authorization_error') throw { code: 'forbidden', message: String(e?.message || CODE_WRITE_MESSAGE) }
          throw e
        }
      case 'files.list':
        if (!okPath(p.dir, true)) throw bad('dir')
        return files.list(p.dir || '')
      case 'chat.send': {
        const text = typeof p.text === 'string' ? p.text.trim() : ''
        if (text === '' || text.length > MAX_TEXT) throw bad('text is empty or longer than 4000 characters')
        return chat.send(text)
      }
      case 'link.open': {
        const target = pageLinkTarget(p.url)
        if (target.error) throw { code: 'invalid', message: LINK_ERRORS[target.error] }
        if (!links) throw { code: 'unavailable', message: 'links do not open here' }
        const t = now()
        linkTokens = Math.min(LINK_BURST, linkTokens + Math.floor((t - linkAt) / LINK_EVERY_MS))
        if (t - linkAt >= LINK_EVERY_MS) linkAt = t
        if (linkTokens < 1) throw { code: 'busy', message: 'too many links at once; wait a moment' }
        linkTokens--
        // The host shows one question: a new link replaces the one before,
        // which answers {opened: false}.
        return { opened: Boolean((await links.open(target))?.opened) }
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
        // The status of a refused read (404, 413, 415) lets fetch() in the
        // frame answer like a server would.
        const error = { code: e?.code || 'failed', message: String(e?.message || 'the call failed').slice(0, 500) }
        if (Number.isInteger(e?.status)) error.status = e.status
        answer({ id, error })
      } finally {
        inFlight--
      }
      return undefined
    },
    // Sends an event to the page: files.changed ({paths}) and visibility
    // ({visible}: the tab or the tile went out of sight, or came back).
    emit(event, payload) {
      if (event === 'files.changed') answer({ event, data: payload })
      else if (event === 'visibility') answer({ event, data: { visible: Boolean(payload?.visible) } })
    },
    close() { closed = true },
  }
}
