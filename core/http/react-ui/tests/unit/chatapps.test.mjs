import test from 'node:test'
import assert from 'node:assert/strict'
import { MAX_HEIGHT, MIN_HEIGHT, PROTOCOL, appText, buildCsp, buildSrcdoc, createHost, linkLabel } from '../../src/utils/chatApps.js'

const tick = () => new Promise(r => setTimeout(r, 0))

// A host with a recording post and handlers.
function setup(over = {}) {
  const sent = []
  const calls = { tools: [], messages: [], links: [], sizes: [] }
  const host = createHost({
    post: (m) => sent.push(m),
    input: { zone: 'UTC' },
    result: { content: [{ type: 'text', text: '12:00' }], structuredContent: { time: '12:00' } },
    callTool: async (name, args) => { calls.tools.push([name, args]); return { content: [{ type: 'text', text: 'ok' }] } },
    onMessage: (text) => calls.messages.push(text),
    onOpenLink: (url) => calls.links.push(url),
    onSize: (h) => calls.sizes.push(h),
    ...over,
  })
  return { host, sent, calls }
}
const req = (id, method, params) => ({ jsonrpc: '2.0', id, method, params })
const note = (method, params) => ({ jsonrpc: '2.0', method, params })
async function init(h) {
  await h.host.receive(req(1, 'ui/initialize', { protocolVersion: PROTOCOL, appInfo: { name: 'a', version: '1' }, appCapabilities: {} }))
}

test('chat apps csp: the default reaches no network and the page may only use inline code', () => {
  const csp = buildCsp(undefined)
  assert.match(csp, /default-src 'none'/)
  assert.match(csp, /connect-src 'none'/)
  assert.match(csp, /frame-src 'none'/)
  assert.match(csp, /base-uri 'none'/)
  assert.match(csp, /form-action 'none'/)
  assert.doesNotMatch(csp, /'self'/)
  assert.doesNotMatch(csp, /unsafe-eval/)
})

test('chat apps csp: declared origins are added and anything that could change the header is dropped', () => {
  const csp = buildCsp({ csp: { connectDomains: ['https://api.example.com', 'https://x.com; script-src *', "'unsafe-eval'", '*', 'http://plain.example.com', 'wss://live.example.com:8443'], resourceDomains: ['https://*.cdn.example.com'] } })
  assert.match(csp, /connect-src https:\/\/api\.example\.com wss:\/\/live\.example\.com:8443(;|$)/)
  assert.match(csp, /script-src 'unsafe-inline' https:\/\/\*\.cdn\.example\.com/)
  assert.doesNotMatch(csp, /plain\.example/)
  assert.doesNotMatch(csp, /unsafe-eval/)
  assert.equal(csp.split(';').filter(p => p.trim().startsWith('script-src')).length, 1)
})

test('chat apps srcdoc: the policy comes first, so a comment in the page cannot hide it', () => {
  const html = '<!-- <head> --><!doctype html><html><head></head><body>x</body></html>'
  const doc = buildSrcdoc(html, undefined)
  assert.ok(doc.indexOf('Content-Security-Policy') >= 0)
  assert.ok(doc.indexOf('Content-Security-Policy') < doc.indexOf('<!--'))
  assert.ok(doc.endsWith(html))
})

test('chat apps srcdoc: a leading doctype stays first', () => {
  const doc = buildSrcdoc('<!DOCTYPE html><html><body>x</body></html>', undefined)
  assert.ok(doc.toLowerCase().startsWith('<!doctype html>'))
  assert.ok(doc.indexOf('Content-Security-Policy') > 0)
})

test('chat apps host: initialize answers with the protocol, the host and what it can do', async () => {
  const h = setup()
  await init(h)
  assert.equal(h.sent.length, 1)
  const r = h.sent[0]
  assert.equal(r.jsonrpc, '2.0')
  assert.equal(r.id, 1)
  assert.equal(r.result.protocolVersion, PROTOCOL)
  assert.equal(r.result.hostInfo.name, 'LocalAI')
  assert.ok(r.result.hostCapabilities.serverTools)
  assert.ok(r.result.hostCapabilities.openLinks)
  assert.equal(r.result.hostContext.displayMode, 'inline')
})

test('chat apps host: the input and the result go out only after the app says it is initialized, input first', async () => {
  const h = setup()
  await init(h)
  assert.equal(h.sent.some(m => m.method), false)
  await h.host.receive(note('ui/notifications/initialized', {}))
  const methods = h.sent.filter(m => m.method).map(m => m.method)
  assert.deepEqual(methods, ['ui/notifications/tool-input', 'ui/notifications/tool-result'])
  assert.deepEqual(h.sent[1].params, { arguments: { zone: 'UTC' } })
  assert.equal(h.sent[2].params.structuredContent.time, '12:00')
})

test('chat apps host: initialized twice sends the input once', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(note('ui/notifications/initialized'))
  await h.host.receive(note('ui/notifications/initialized'))
  assert.equal(h.sent.filter(m => m.method === 'ui/notifications/tool-input').length, 1)
})

test('chat apps host: tools/call goes to the proxy and its result comes back', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(req(7, 'tools/call', { name: 'refresh', arguments: { a: 1 } }))
  assert.deepEqual(h.calls.tools, [['refresh', { a: 1 }]])
  const r = h.sent.find(m => m.id === 7)
  assert.equal(r.result.content[0].text, 'ok')
})

test('chat apps host: a failing tool call is a JSON-RPC error with the reason', async () => {
  const h = setup({ callTool: async () => { throw new Error('the tool is not visible to apps') } })
  await init(h)
  await h.host.receive(req(8, 'tools/call', { name: 'x' }))
  const r = h.sent.find(m => m.id === 8)
  assert.equal(r.result, undefined)
  assert.equal(r.error.code, -32000)
  assert.match(r.error.message, /not visible/)
})

test('chat apps host: tools/call needs a name, an object for arguments and a modest size', async () => {
  const h = setup()
  await init(h)
  for (const [id, params] of [[10, {}], [11, { name: 5 }], [12, { name: 'x', arguments: [1] }], [13, { name: 'x', arguments: { a: 'y'.repeat(20000) } }], [14, { name: 'x'.repeat(300) }]]) {
    await h.host.receive(req(id, 'tools/call', params))
    assert.equal(h.sent.find(m => m.id === id).error.code, -32602, `id ${id}`)
  }
  assert.equal(h.calls.tools.length, 0)
})

test('chat apps host: nothing but initialize and ping works before the handshake', async () => {
  const h = setup()
  await h.host.receive(req(2, 'tools/call', { name: 'x' }))
  assert.equal(h.sent.find(m => m.id === 2).error.code, -32002)
  await h.host.receive(req(3, 'ping'))
  assert.deepEqual(h.sent.find(m => m.id === 3).result, {})
  assert.equal(h.calls.tools.length, 0)
})

test('chat apps host: a request the host does not implement gets method not found', async () => {
  const h = setup()
  await init(h)
  for (const [id, m] of [[20, 'resources/read'], [21, 'ui/update-model-context'], [22, 'sampling/createMessage'], [23, 'nope']]) {
    await h.host.receive(req(id, m, {}))
    assert.equal(h.sent.find(x => x.id === id).error.code, -32601, m)
  }
})

test('chat apps host: a notification it does not know is ignored without an answer', async () => {
  const h = setup()
  await init(h)
  const before = h.sent.length
  await h.host.receive(note('notifications/message', { level: 'info', data: 'x' }))
  await h.host.receive(note('ui/notifications/unknown'))
  assert.equal(h.sent.length, before)
})

test('chat apps host: data that is not a JSON-RPC message is ignored', async () => {
  const h = setup()
  for (const bad of [null, undefined, 5, 'x', [], {}, { jsonrpc: '1.0', method: 'ping', id: 1 }, { jsonrpc: '2.0' }, { jsonrpc: '2.0', method: 7, id: 1 },
    { jsonrpc: '2.0', method: 'x'.repeat(500), id: 1 }, { jsonrpc: '2.0', method: 'ping', id: {} }, { jsonrpc: '2.0', id: 1, result: {} }]) {
    await h.host.receive(bad)
  }
  assert.equal(h.sent.length, 0)
})

test('chat apps host: the size the app asks for is kept within bounds', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(note('ui/notifications/size-changed', { height: 10, width: 300 }))
  await h.host.receive(note('ui/notifications/size-changed', { height: 99999 }))
  await h.host.receive(note('ui/notifications/size-changed', { height: 320.4 }))
  await h.host.receive(note('ui/notifications/size-changed', { height: 'tall' }))
  await h.host.receive(note('ui/notifications/size-changed', { height: NaN }))
  await h.host.receive(note('ui/notifications/size-changed', { width: 100 }))
  assert.deepEqual(h.calls.sizes, [MIN_HEIGHT, MAX_HEIGHT, 321])
})

test('chat apps host: ui/message becomes a suggestion and never a send', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(req(30, 'ui/message', { role: 'user', content: [{ type: 'text', text: 'Show me March' }] }))
  assert.deepEqual(h.calls.messages, ['Show me March'])
  assert.deepEqual(h.sent.find(m => m.id === 30).result, {})
  for (const [id, params] of [[31, { role: 'assistant', content: [{ type: 'text', text: 'x' }] }], [32, { role: 'user', content: 'x' }], [33, { role: 'user', content: [{ type: 'image', data: 'x' }] }], [34, { role: 'user', content: [{ type: 'text', text: 'y'.repeat(5000) }] }]]) {
    await h.host.receive(req(id, 'ui/message', params))
    assert.equal(h.sent.find(m => m.id === id).error.code, -32602, `id ${id}`)
  }
  assert.equal(h.calls.messages.length, 1)
})

test('chat apps host: ui/open-link accepts http and https only', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(req(40, 'ui/open-link', { url: 'https://example.com/a?b=1' }))
  await h.host.receive(req(41, 'ui/open-link', { url: 'http://example.com' }))
  assert.deepEqual(h.calls.links, ['https://example.com/a?b=1', 'http://example.com/'])
  for (const [id, url] of [[42, 'javascript:alert(1)'], [43, 'data:text/html,x'], [44, 'file:///etc/passwd'], [45, '//evil.example'], [46, 'https://user:pw@example.com'], [47, 'https://x.com/' + 'a'.repeat(3000)], [48, 5]]) {
    await h.host.receive(req(id, 'ui/open-link', { url }))
    const r = h.sent.find(m => m.id === id)
    assert.ok(r.result?.isError === true || r.error, `id ${id}`)
  }
  assert.equal(h.calls.links.length, 2)
})

test('chat apps host: ui/request-display-mode keeps the app inline', async () => {
  const h = setup()
  await init(h)
  await h.host.receive(req(50, 'ui/request-display-mode', { mode: 'fullscreen' }))
  assert.deepEqual(h.sent.find(m => m.id === 50).result, { mode: 'inline' })
})

test('chat apps host: only a few tool calls run at once', async () => {
  let release
  const gate = new Promise(r => { release = r })
  const h = setup({ callTool: async () => { await gate; return { content: [] } } })
  await init(h)
  const pending = []
  for (let i = 0; i < 6; i++) pending.push(h.host.receive(req(100 + i, 'tools/call', { name: 'x' })))
  await tick()
  const refused = h.sent.filter(m => m.error?.message?.includes('too many'))
  assert.equal(refused.length, 2)
  release()
  await Promise.all(pending)
  assert.equal(h.sent.filter(m => m.result).length >= 5, true)
})

test('chat apps host: a closed host answers nothing', async () => {
  const h = setup()
  await init(h)
  h.host.close()
  const before = h.sent.length
  await h.host.receive(req(60, 'ping'))
  assert.equal(h.sent.length, before)
})

test('chat apps text: the fallback reads the text blocks of the result', () => {
  assert.equal(appText({ content: [{ type: 'text', text: 'a' }, { type: 'image', data: 'x' }, { type: 'text', text: 'b' }] }), 'a\nb')
  assert.equal(appText(undefined), '')
  assert.equal(appText({ content: 'x' }), '')
})

test('chat apps link label: only the host of the address is shown', () => {
  assert.equal(linkLabel('https://example.com/a/b?c=1'), 'example.com')
  assert.equal(linkLabel('nonsense'), '')
})
