import test from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { MODULE_KEY, inlinePageAssets, pageRefs, resolvePagePath, scanTags } from '../../src/utils/pageAssets.js'
import { PAGE_CSP, buildPageSrcdoc, createPageHost, pageCsp } from '../../src/utils/pagesBridge.js'

// A folder of a page as the reads of the inliner see it. A missing file throws
// like the API does: an error with its HTTP status.
function folder(files, images = {}) {
  const calls = []
  const miss = (path) => Object.assign(new Error(`not found: ${path}`), { status: 404 })
  return {
    calls,
    read: async (path) => {
      calls.push(['read', path])
      if (!Object.hasOwn(files, path)) throw miss(path)
      return { path, content: files[path], version: 1 }
    },
    readImage: async (path) => {
      calls.push(['image', path])
      if (!Object.hasOwn(images, path)) throw miss(path)
      return { path, content: Buffer.from(images[path]).toString('base64'), encoding: 'base64', media_type: 'image/png' }
    },
  }
}

test('page paths: relative names resolve inside the folder, with query and hash dropped', () => {
  const ok = [
    ['data.json', 'data.json'], ['./data.json', 'data.json'], ['data/x.json', 'data/x.json'],
    ['data.json?x=1#y', 'data.json'], ['a/../b.json', 'b.json'], ['my%20file.json', 'my file.json'],
    ['  data.json  ', 'data.json'], ['x.json#', 'x.json'],
  ]
  for (const [ref, path] of ok) assert.deepEqual(resolvePagePath(ref), { path }, ref)
  assert.deepEqual(resolvePagePath('../img/a.png', 'css'), { path: 'img/a.png' })
  assert.deepEqual(resolvePagePath('./b.js', 'lib/sub'), { path: 'lib/sub/b.js' })
})

test('page paths: nothing outside the folder, no absolute or odd names', () => {
  const cases = [
    ['../secret.txt', 'outside'], ['../../secret.txt', 'outside'], ['a/../../x', 'outside'],
    ['%2e%2e/secret.txt', 'outside'], ['%2E%2E%2Fsecret.txt', 'outside'], ['..', 'outside'],
    ['/secret.txt', 'absolute'], ['\\secret.txt', 'absolute'], ['%2fsecret.txt', 'absolute'],
    ['a\\..\\..\\b', 'bad'], ['a%5c..%5cb', 'bad'], ['a//b', 'bad'], ['dir/', 'bad'], ['x%00.json', 'bad'],
    ['%zz', 'bad'], ['x'.repeat(300), 'bad'], ['a/b/c/d/e/f/g/h/i.json', 'bad'], ['a%3ab', 'bad'],
  ]
  for (const [ref, error] of cases) assert.deepEqual(resolvePagePath(ref), { error }, ref)
  assert.deepEqual(resolvePagePath('../../x', 'a'), { error: 'outside' })
  for (const remote of ['https://example.com/x.js', 'http://x', '//example.com/x.js', '\\\\host\\x', 'data:text/plain,hi', 'blob:null/1', 'javascript:alert(1)']) {
    assert.deepEqual(resolvePagePath(remote), { skip: 'remote' }, remote)
  }
  for (const empty of ['', '#top', '?q=1', null]) assert.ok(resolvePagePath(empty).skip === 'empty' || resolvePagePath(empty).error, String(empty))
})

test('tokenizer: tags, quoted and unquoted attributes, raw text, comments', () => {
  const html = `<!doctype html><!-- <script src="no.js"></script> --><img src=a.png alt='x &amp; y'><script>if (a < b) "<img src=c.png>"</script>
<SCRIPT SRC="app.js" defer></SCRIPT><textarea><link rel=stylesheet href=t.css></textarea><p>`
  const tags = scanTags(html)
  assert.deepEqual(tags.map((t) => t.name), ['img', 'script', 'script', 'textarea', 'p'])
  assert.equal(tags[0].attrs[0].value, 'a.png')
  assert.equal(tags[0].attrs[1].value, 'x & y')
  assert.equal(html.slice(tags[1].inner.start, tags[1].inner.end), 'if (a < b) "<img src=c.png>"')
  assert.deepEqual(tags[2].attrs.map((a) => [a.name, a.value]), [['src', 'app.js'], ['defer', '']])
  assert.equal(html.slice(tags[2].close - 9, tags[2].close), '</SCRIPT>')
})

test('pageRefs: only pages that name files of their folder need a read', () => {
  assert.equal(pageRefs('<p>hi</p><script>fetch("x.json")</script><img src="data:image/png;base64,AA">'), false)
  assert.equal(pageRefs('<a href="other.html">x</a><link rel=icon href=favicon.ico>'), false)
  assert.equal(pageRefs('<script src="https://cdn.example/x.js"></script>'), false)
  assert.equal(pageRefs('<script src="app.js"></script>'), true)
  assert.equal(pageRefs('<link rel="stylesheet" href="style.css">'), true)
  assert.equal(pageRefs('<img src="logo.png">'), true)
  assert.equal(pageRefs('<script type="module">import {a} from "./a.js"</script>'), true)
  assert.equal(pageRefs('<style>body{background:url(bg.png)}</style>'), true)
})

test('inliner: scripts, styles and images go in, in place, with notes for what cannot', async () => {
  const f = folder({
    'app.js': 'window.order = (window.order || "") + "a"; const s = "</script><b>"',
    'late.js': 'window.order += "d"',
    'css/style.css': '@import "base.css"; .x { background: url(../img/bg.png) } .f { src: url(f.woff2) }',
    'css/base.css': 'body { color: red } /* </style> */',
  }, { 'logo.png': 'PNG1', 'img/bg.png': 'PNG2' })
  const html = `<!doctype html><html><head><link rel="stylesheet" href="css/style.css" media="screen">
<script src="late.js" defer></script></head><body><img src="logo.png" alt=logo><img src="https://example.com/r.png">
<script src="app.js"></script><script src="missing.js"></script><script src="../../secret.js"></script>
<img src="nope.png"></body></html>`
  const out = await inlinePageAssets(html, f)
  assert.equal(out.modules, false)
  assert.equal(out.head, '')
  assert.deepEqual(out.deps, ['app.js', 'css/base.css', 'css/style.css', 'img/bg.png', 'late.js', 'logo.png'])
  assert.match(out.html, /<style media="screen">body \{ color: red \} \/\* <\\\/style> \*\/ \.x \{ background: url\("data:image\/png;base64,UE5HMg=="\) \}/)
  assert.match(out.html, /<img src="data:image\/png;base64,UE5HMQ==" alt=logo>/)
  assert.match(out.html, /<img src="https:\/\/example\.com\/r\.png">/, 'a remote image is left alone')
  // The script text is JSON in a loader: no "</script>" can end it early.
  assert.equal((out.html.match(/<\/script>/gi) || []).length, (out.html.match(/<script/gi) || []).length)
  assert.match(out.html, /\\u003c\/script>\\u003cb>/)
  assert.match(out.html, /sourceURL=app\.js/)
  // The deferred script moved to the end of the body.
  assert.ok(out.html.indexOf('late.js') > out.html.indexOf('sourceURL=app.js'))
  assert.ok(out.html.indexOf('sourceURL=late.js') < out.html.indexOf('</body>'))
  assert.ok(!out.html.includes('src="late.js"'))
  // What did not load stays as it was and says why.
  assert.match(out.html, /<script src="missing\.js"><\/script>/)
  assert.ok(!f.calls.some(([, p]) => p.includes('secret')), 'nothing outside the folder is read')
  const errors = out.notes.filter((n) => n.level === 'error').map((n) => n.message)
  assert.ok(errors.some((m) => /missing\.js is not in the page's folder/.test(m)), errors.join('\n'))
  assert.ok(errors.some((m) => /\.\.\/\.\.\/secret\.js is outside the page's folder/.test(m)))
  const warns = out.notes.filter((n) => n.level === 'warn').map((n) => n.message)
  assert.ok(warns.some((m) => /font css\/f\.woff2/.test(m)))
  assert.ok(warns.some((m) => /image nope\.png is not in the page's folder/.test(m)))
  assert.equal(out.tooLarge, false)
})

test('inliner: modules become an import map of blob URLs, with cycles and import()', async () => {
  const f = folder({
    'main.js': "import { a } from './lib/a.js'\nimport def from \"./lib/c.js\"\nexport * from './lib/a.js'\nconst lazy = () => import('./lib/c.js')\nimport x from 'react'",
    'lib/a.js': "import { b } from './b.js'\nexport const a = 'a' + b",
    'lib/b.js': "import { a } from './a.js'\nexport const b = 'b'\nexport const later = () => a",
    'lib/c.js': 'export default 1',
  })
  const html = '<script type="module" src="main.js"></script><script type="module">import { b } from "./lib/b.js"; console.log(b)</script><script type="module">console.log(1)</script>'
  const out = await inlinePageAssets(html, f)
  assert.equal(out.modules, true)
  assert.deepEqual(out.deps, ['lib/a.js', 'lib/b.js', 'lib/c.js', 'main.js'])
  assert.match(out.html, new RegExp(`^<script type="module">import "${MODULE_KEY}main\\.js"</script>`))
  assert.match(out.html, new RegExp(`import \\{ b \\} from "${MODULE_KEY}lib/b\\.js"`))
  assert.match(out.html, /<script type="module">console\.log\(1\)<\/script>$/)
  const json = /\}\)\((\{.*\})\)<\/script>$/.exec(out.head)[1]
  const sources = JSON.parse(json)
  assert.deepEqual(Object.keys(sources).sort(), ['lib/a.js', 'lib/b.js', 'lib/c.js', 'main.js'].map((p) => MODULE_KEY + p))
  assert.match(sources[`${MODULE_KEY}lib/b.js`], new RegExp(`from '${MODULE_KEY}lib/a\\.js'`))
  assert.match(sources[`${MODULE_KEY}main.js`], new RegExp(`import\\('${MODULE_KEY}lib/c\\.js'\\)`))
  assert.match(sources[`${MODULE_KEY}main.js`], new RegExp(`export \\* from '${MODULE_KEY}lib/a\\.js'`))
  assert.match(sources[`${MODULE_KEY}main.js`], /from 'react'/, 'a bare specifier is left alone')
  assert.ok(out.notes.some((n) => n.level === 'error' && /react .*package name/.test(n.message)))
  assert.ok(!out.head.includes('</script><'), 'the sources cannot end the loader')
  assert.match(pageCsp(true), /script-src 'unsafe-inline' blob:/)
  assert.doesNotMatch(PAGE_CSP, /blob:/)
})

test('inliner: a missing module throws a clear error instead of a resolution failure', async () => {
  const out = await inlinePageAssets('<script type="module" src="main.js"></script>', folder({ 'main.js': "import './gone.js'" }))
  const sources = JSON.parse(/\}\)\((\{.*\})\)<\/script>$/.exec(out.head)[1])
  assert.match(sources[`${MODULE_KEY}gone.js`], /throw new Error\("gone\.js did not load/)
  assert.ok(out.notes.some((n) => /module gone\.js is not in the page's folder/.test(n.message)))
  const outside = await inlinePageAssets('<script type="module">import "../x.js"</script>', folder({}))
  assert.equal(outside.modules, false)
  assert.ok(outside.notes.some((n) => /outside the page's folder/.test(n.message)))
})

test('inliner: the budgets leave files out and say so', async () => {
  const f = folder({ 'a.js': 'x'.repeat(600), 'b.js': 'y'.repeat(600) }, { 'big.png': 'z'.repeat(900), 'small.png': 'ok' })
  const out = await inlinePageAssets('<script src="a.js"></script><script src="b.js"></script>', f, { maxTotal: 1000 })
  assert.equal(out.tooLarge, true)
  assert.match(out.html, /<script src="b\.js"><\/script>$/)
  assert.deepEqual(out.deps, ['a.js'])
  const img = await inlinePageAssets('<img src="big.png"><img src="small.png">', f, { maxImages: 500 })
  assert.equal(img.tooLarge, false)
  assert.match(img.html, /^<img src="big\.png"><img src="data:/)
  assert.ok(img.notes.some((n) => n.level === 'warn' && /images of a page are at most/.test(n.message)))
})

test('inliner: a page with nothing to put in comes back unchanged and reads nothing', async () => {
  const f = folder({})
  const html = '<!doctype html><p>x</p><script>1</script><img src="data:image/png;base64,AA">'
  const out = await inlinePageAssets(html, f)
  assert.equal(out.html, html)
  assert.deepEqual(f.calls, [])
})

test('srcdoc: notes and the banner reach the bridge as data, never as markup', () => {
  const doc = buildPageSrcdoc('<p>x</p>', { name: 'n', notes: [{ level: 'error', message: '</script><script>alert(1)</script>' }], banner: '<b>x</b>', modules: true, head: '<script>1</script>' })
  assert.match(doc, /script-src 'unsafe-inline' blob:/)
  assert.equal((doc.match(/<\/script>/g) || []).length, 2, 'the bridge and the module loader')
  assert.ok(doc.indexOf('<script>1</script>') < doc.indexOf('<p>x</p>'))
})

// --- The shim inside the frame, run in a VM against a real host -------------

function frame({ files = {}, baseURI = 'http://localai.test/app/pages/news' } = {}) {
  const reads = []
  const logs = []
  let onMessage = null
  const sandbox = {
    console: { warn: (m) => logs.push(['warn', m]), error: (m) => logs.push(['error', m]), log() {} },
    setTimeout, Promise, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI, readyState: 'complete',
      documentElement: { setAttribute() {}, style: {}, appendChild() {} },
      addEventListener() {}, createElement: () => ({ setAttribute() {}, appendChild() {}, attachShadow: () => ({ appendChild() {} }), style: {} }),
    },
    addEventListener: (type, fn) => { if (type === 'message') onMessage = fn },
  }
  sandbox.window = sandbox
  sandbox.parent = { marker: 'parent' }
  vm.createContext(sandbox)
  const doc = buildPageSrcdoc('', { name: 'news', theme: 'light' })
  const code = /<script>([\s\S]*?)<\/script>/.exec(doc)[1]
  vm.runInContext(code, sandbox)
  const host = createPageHost({
    post: (m) => port.onmessage({ data: m }),
    files: {
      read: async (path) => {
        reads.push(path)
        if (!Object.hasOwn(files, path)) throw Object.assign(new Error('page or file not found'), { status: 404 })
        if (files[path] === 415) throw Object.assign(new Error('pages: the file is not UTF-8 text'), { status: 415 })
        return { path, content: files[path], version: 1, size: files[path].length }
      },
      write: async () => ({}),
      list: async () => [],
    },
    chat: { send: async () => ({ sent: false }) },
  })
  const port = { onmessage: null, postMessage: (m) => { host.receive(m) } }
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port] })
  return { win: sandbox, reads, logs }
}

test('shim: fetch of a file next to the page answers like a server', async () => {
  const { win, reads } = frame({ files: { 'news.json': '{"items":[1,2]}', 'data/a.csv': 'a,b', 'bin.dat': 415 } })
  const r = await win.fetch('news.json')
  assert.equal(r.status, 200)
  assert.equal(r.ok, true)
  assert.equal(r.headers.get('content-type'), 'application/json; charset=utf-8')
  assert.deepEqual(await r.json(), { items: [1, 2] })
  assert.equal(r.url, 'news.json')
  assert.equal(await (await win.fetch('./data/a.csv?v=2#x')).text(), 'a,b')
  assert.equal(await (await win.fetch(new win.Request('http://localai.test/app/pages/data/a.csv'))).text(), 'a,b', 'a Request as the browser resolves it against the base URL')
  assert.equal(await (await win.fetch(new URL('data/a.csv', win.document.baseURI))).text(), 'a,b')
  const missing = await win.fetch('missing.json')
  assert.equal(missing.status, 404)
  assert.equal(missing.ok, false)
  assert.equal((await win.fetch('bin.dat')).status, 415)
  assert.equal((await win.fetch('news.json', { method: 'HEAD' })).status, 200)
  assert.deepEqual(reads, ['news.json', 'data/a.csv', 'data/a.csv', 'data/a.csv', 'missing.json', 'bin.dat', 'news.json'])
})

test('shim: fetch refuses the network, other folders and writes, and reads nothing for them', async () => {
  const { win, reads } = frame({ files: { 'x.json': '1' } })
  for (const url of ['https://example.com/x.json', '//example.com/x', '/api/chats/files', 'http://localai.test/api/chats/files']) {
    await assert.rejects(win.fetch(url), (e) => e instanceof TypeError && /^network is off in pages; put the data in a file next to the page/.test(e.message), url)
  }
  for (const url of ['../x.json', '%2e%2e/x.json', '..%2Fx.json', 'a\\..\\x.json', 'http://localai.test/app/pages/a/../../x.json']) {
    await assert.rejects(win.fetch(url), TypeError, url)
  }
  await assert.rejects(win.fetch('x.json', { method: 'POST', body: '1' }), /only reads files/)
  assert.deepEqual(reads, [])
  // Many fetches at once are queued, not refused as busy.
  const all = await Promise.all(Array.from({ length: 20 }, () => win.fetch('x.json')))
  assert.ok(all.every((r) => r.status === 200))
})

test('shim: XMLHttpRequest reads text, json and arraybuffer, and gives 404 and errors', async () => {
  const { win, logs } = frame({ files: { 'd.json': '{"a":1}' } })
  const send = (url, responseType = '', method = 'GET') => new Promise((resolve) => {
    const x = new win.XMLHttpRequest()
    const seen = []
    x.onreadystatechange = () => seen.push(x.readyState)
    x.addEventListener('load', () => resolve({ x, ev: 'load', seen }))
    x.onerror = () => resolve({ x, ev: 'error', seen })
    x.open(method, url)
    x.responseType = responseType
    x.send()
  })
  let { x, ev, seen } = await send('d.json')
  assert.equal(ev, 'load')
  assert.equal(x.status, 200)
  assert.equal(x.responseText, '{"a":1}')
  assert.equal(x.getResponseHeader('Content-Type'), 'application/json; charset=utf-8')
  assert.deepEqual(seen, [1, 2, 3, 4])
  ;({ x } = await send('d.json', 'json'))
  assert.deepEqual(x.response, { a: 1 })
  ;({ x } = await send('d.json', 'arraybuffer'))
  assert.equal(Buffer.from(x.response).toString(), '{"a":1}')
  ;({ x, ev } = await send('nope.json'))
  assert.equal(ev, 'load')
  assert.equal(x.status, 404)
  ;({ x, ev } = await send('https://example.com/x'))
  assert.equal(ev, 'error')
  assert.equal(x.status, 0)
  assert.ok(logs.some(([l, m]) => l === 'error' && /network is off in pages/.test(m)))
  ;({ ev } = await send('d.json', '', 'PUT'))
  assert.equal(ev, 'error')
  const sync = new win.XMLHttpRequest()
  assert.throws(() => sync.open('GET', 'd.json', false), /synchronous/)
})

test('shim and inliner resolve a URL the same way', async () => {
  const refs = ['data.json', './a/b.json', 'a/../b.json', 'x.json?y#z', 'my%20f.json', '../x', '%2e%2e/x', 'a//b', 'a\\b', '/abs', '//h/x', 'https://h/x', 'a%3ab', 'a/b/c/d/e/f/g/h/i']
  const files = {}
  for (const r of refs) { const p = resolvePagePath(r).path; if (p) files[p] = 'ok' }
  const { win, reads } = frame({ files })
  for (const ref of refs) {
    const want = resolvePagePath(ref).path
    const before = reads.length
    const got = await win.fetch(ref).then((r) => r.status, () => 'refused')
    assert.equal(got, want ? 200 : 'refused', ref)
    if (want) assert.equal(reads[before], want, ref)
  }
})

test('inliner: a shared page gets only the files of code its consent covers', async () => {
  const reads = []
  const files = { 'app.js': 'console.log(1)', 'extra.js': 'steal()', 'style.css': 'b{}', 'm.js': "import './n.js'", 'n.js': 'x' }
  const io = {
    read: async (p) => { reads.push(p); if (!(p in files)) throw Object.assign(new Error('nf'), { status: 404 }); return { content: files[p] } },
    readImage: async () => { throw Object.assign(new Error('nf'), { status: 404 }) },
  }
  const html = '<link rel="stylesheet" href="style.css"><script src="app.js"></script><script src="extra.js"></script><script type="module" src="m.js"></script>'
  const allowed = new Set(['app.js', 'style.css', 'm.js'])
  const out = await inlinePageAssets(html, io, { allowText: (p) => allowed.has(p) })
  assert.ok(!reads.includes('extra.js'), 'a file outside the set is never read')
  assert.ok(!reads.includes('n.js'))
  assert.match(out.html, /console\.log\(1\)/)
  assert.doesNotMatch(out.html, /steal/)
  assert.ok(out.notes.some((n) => n.path === 'extra.js' && n.level === 'error' && /not one of the files of code/.test(n.message)))
  assert.ok(out.notes.some((n) => n.path === 'n.js'))
  const own = await inlinePageAssets(html, io)
  assert.match(own.html, /steal/, 'an own page has no such limit')
})
