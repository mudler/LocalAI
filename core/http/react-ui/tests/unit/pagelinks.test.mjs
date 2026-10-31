import test from 'node:test'
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { LINK_ERRORS, pageLinkTarget, punycodeDecode } from '../../src/utils/pageLinks.js'
import { buildPageSrcdoc, createPageHost } from '../../src/utils/pagesBridge.js'

test('page links: only plain http and https addresses open', () => {
  const ok = pageLinkTarget('https://news.example.com/a?b=1#c')
  assert.deepEqual(ok, { href: 'https://news.example.com/a?b=1#c', host: 'news.example.com', display: 'news.example.com', site: 'example.com', lookalike: false })
  assert.equal(pageLinkTarget('HTTP://Example.COM.').host, 'example.com')
  assert.equal(pageLinkTarget('http://203.0.113.7:8080/x').site, '203.0.113.7')
  const refused = [
    ['javascript:alert(1)', 'scheme'], ['JaVaScRiPt:alert(1)', 'scheme'], ['data:text/html,<b>x</b>', 'scheme'],
    ['file:///etc/passwd', 'scheme'], ['blob:null/1234', 'scheme'], ['mailto:a@b.c', 'scheme'], ['ftp://x.org', 'scheme'],
    ['//evil.example/x', 'relative'], ['other.html', 'relative'], ['/app/settings', 'relative'], ['../x', 'relative'],
    ['https://user:pass@example.com/', 'credentials'], ['https://user@example.com/', 'credentials'],
    ['', 'not_a_url'], ['https://', 'not_a_url'], [`https://example.com/${'x'.repeat(2100)}`, 'not_a_url'], [null, 'not_a_url'],
  ]
  for (const [url, error] of refused) assert.deepEqual(pageLinkTarget(url), { error }, String(url).slice(0, 40))
  for (const e of Object.values(LINK_ERRORS)) assert.ok(e.length > 10)
})

test('page links: a look-alike host is shown in punycode and as it reads', () => {
  const t = pageLinkTarget('https://аpple.com/login') // Cyrillic а
  assert.equal(t.host, 'xn--pple-43d.com')
  assert.equal(t.display, 'аpple.com')
  assert.equal(t.site, 'xn--pple-43d.com')
  assert.equal(t.lookalike, true)
  assert.equal(pageLinkTarget('https://münchen.de').display, 'münchen.de')
  assert.equal(punycodeDecode('bcher-kva'), 'bücher')
  assert.equal(punycodeDecode('wgv71a119e'), '日本語')
  assert.equal(pageLinkTarget('https://xn--zz.com').lookalike, true, 'a broken label still warns')
})

function host(links, clock) {
  const sent = []
  const h = createPageHost({ post: (m) => sent.push(m), files: {}, chat: {}, links, now: () => clock.t })
  return { h, sent }
}

test('page host: link.open checks the URL, answers with the choice and limits a burst', async () => {
  const asked = []
  let answer
  const clock = { t: 0 }
  const { h, sent } = host({ open: (target) => { asked.push(target.href); return new Promise((r) => { answer = r }) } }, clock)
  await h.receive({ id: 1, method: 'link.open', params: { url: 'javascript:alert(1)' } })
  assert.match(sent.at(-1).error.message, /only http and https/)
  await h.receive({ id: 4, method: 'link.open', params: { url: 'https://user:pw@example.com/' } })
  assert.match(sent.at(-1).error.message, /user name or a password/)
  const first = h.receive({ id: 2, method: 'link.open', params: { url: 'https://example.com/' } })
  answer({ opened: true })
  await first
  assert.deepEqual(sent.find((m) => m.id === 2).result, { opened: true })
  assert.deepEqual(asked, ['https://example.com/'])
  const quick = { open: async () => ({ opened: false }) }
  const burst = host(quick, clock)
  for (let i = 0; i < 7; i++) await burst.h.receive({ id: 10 + i, method: 'link.open', params: { url: 'https://example.com/' } })
  assert.equal(burst.sent.filter((m) => m.result).length, 5)
  clock.t += 6000
  await burst.h.receive({ id: 30, method: 'link.open', params: { url: 'https://example.com/' } })
  assert.ok(burst.sent.at(-1).result, 'the budget comes back with time')
  const none = host(undefined, clock)
  await none.h.receive({ id: 1, method: 'link.open', params: { url: 'https://example.com/' } })
  assert.match(none.sent[0].error.message, /do not open here/)
})

test('frame: a click on a link asks the host, an anchor stays in the page, window.open asks too', async () => {
  const listeners = {}
  const logs = []
  const calls = []
  let onMessage = null
  const sandbox = {
    console: { warn: (m) => logs.push(m), error: (m) => logs.push(m), log() {} },
    setTimeout, Promise, Response, Request, Blob, TextEncoder, DOMException, JSON, Object, String, Error, TypeError, URL,
    document: {
      baseURI: 'http://localai.test/app/pages/x', readyState: 'complete',
      documentElement: { setAttribute() {}, style: {} },
      addEventListener: (t, fn) => { listeners[t] = fn },
      getElementById: () => null, getElementsByName: () => [],
    },
    scrollTo() {},
    addEventListener: (t, fn) => { if (t === 'message') onMessage = fn },
  }
  sandbox.window = sandbox
  sandbox.parent = {}
  vm.createContext(sandbox)
  vm.runInContext(/<script>([\s\S]*?)<\/script>/.exec(buildPageSrcdoc('', { name: 'x' }))[1], sandbox)
  const port = { postMessage: (m) => calls.push(m) }
  onMessage({ source: sandbox.parent, data: { type: 'localai:init' }, ports: [port] })
  const click = (href, extra = {}) => {
    let prevented = false
    const a = { getAttribute: () => href }
    listeners[extra.type || 'click']({ type: extra.type || 'click', button: extra.button ?? 0, defaultPrevented: false, target: { closest: () => a }, preventDefault: () => { prevented = true } })
    return prevented
  }
  assert.equal(click('https://example.com/a'), true)
  assert.equal(click('#section'), true, 'an anchor is handled in the page, not resolved against the app')
  assert.equal(click('https://example.com/b', { type: 'auxclick', button: 1 }), true)
  assert.equal(click('other.html'), true)
  assert.equal(sandbox.window.open('https://example.com/c'), null)
  const opened = calls.filter((m) => m.method === 'link.open').map((m) => m.params.url)
  assert.deepEqual(opened, ['https://example.com/a', 'https://example.com/b', 'https://example.com/c'])
  assert.ok(logs.some((m) => /other\.html opens nothing/.test(m)))
})
