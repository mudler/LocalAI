import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// MCP Apps in a chat, with the chat API stubbed: a tool line of the transcript
// that carries an app shows the page in a sandboxed frame, the frame talks to the
// host over postMessage, and the host answers through the chat's two mcp-apps
// routes. The app below is a small page that drives every message the host
// supports, and probes the sandbox from the inside.

const chat = { id: 'c1', title: 'Clock', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false }

const APP_HTML = `<!doctype html><html><body>
<p id="out">waiting</p><p id="input"></p><p id="probe"></p><p id="reply"></p>
<button id="call">call</button><button id="size">size</button><button id="suggest">suggest</button>
<button id="link">link</button><button id="badlink">badlink</button><button id="unknown">unknown</button>
<script>
let next = 1
const pending = new Map()
function send(method, params) {
  const id = next++
  parent.postMessage({ jsonrpc: '2.0', id, method, params }, '*')
  return new Promise((resolve) => pending.set(id, resolve))
}
function note(method, params) { parent.postMessage({ jsonrpc: '2.0', method, params }, '*') }
window.addEventListener('message', (e) => {
  const m = e.data
  if (m && m.id !== undefined && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
  if (m && m.method === 'ui/notifications/tool-input') document.getElementById('input').textContent = JSON.stringify(m.params.arguments)
  if (m && m.method === 'ui/notifications/tool-result') document.getElementById('out').textContent = m.params.content[0].text
})
const reply = (r) => { document.getElementById('reply').textContent = JSON.stringify(r) }
document.getElementById('call').onclick = async () => reply(await send('tools/call', { name: 'refresh', arguments: { zone: 'EU' } }))
document.getElementById('size').onclick = () => note('ui/notifications/size-changed', { height: 333 })
document.getElementById('suggest').onclick = async () => reply(await send('ui/message', { role: 'user', content: [{ type: 'text', text: 'Show me March' }] }))
document.getElementById('link').onclick = async () => reply(await send('ui/open-link', { url: 'https://example.com/docs' }))
document.getElementById('badlink').onclick = async () => reply(await send('ui/open-link', { url: 'javascript:alert(1)' }))
document.getElementById('unknown').onclick = async () => reply(await send('resources/read', { uri: 'ui://x' }))
let probe = []
try { void parent.document.title; probe.push('parent-readable') } catch (e) { probe.push('parent-blocked') }
try { void localStorage.length; probe.push('storage-readable') } catch (e) { probe.push('storage-blocked') }
fetch('https://example.com/x', { mode: 'no-cors' }).then(() => probe.push('net-open'), () => probe.push('net-blocked')).then(() => { document.getElementById('probe').textContent = probe.join(',') })
send('ui/initialize', { protocolVersion: '2026-01-26', appInfo: { name: 'probe', version: '1' }, appCapabilities: {} }).then(() => note('ui/notifications/initialized', {}))
</script></body></html>`

const toolLine = {
  seq: 2, turn: 1, role: 'tool', text: 'get-time: {"zone":"UTC"}\n12:00 UTC', at: '2026-10-25T10:00:01Z',
  app: {
    server: 'connector:abc', tool: 'get-time', resource_uri: 'ui://clock/app.html', visibility: ['model', 'app'],
    input: { zone: 'UTC' }, result: { content: [{ type: 'text', text: '12:00 UTC' }], structuredContent: { time: '12:00' } },
  },
}

async function stub(page, { resource, calls = [], sent = [] } = {}) {
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [chat], persistent: true, files: true } }))
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') {
      sent.push(route.request().postDataJSON())
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'what time is it', at: '2026-10-25T10:00:00Z' },
      toolLine,
      { seq: 3, turn: 1, role: 'assistant', text: 'It is noon.', at: '2026-10-25T10:00:02Z' },
    ] } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...chat, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/mcp-apps\/resource/, (route) => {
    if (resource) return resource(route)
    return route.fulfill({ json: { uri: 'ui://clock/app.html', mime_type: 'text/html;profile=mcp-app', html: APP_HTML, meta: { csp: {} } } })
  })
  await page.route(/\/api\/chats\/c1\/mcp-apps\/call/, (route) => {
    calls.push(route.request().postDataJSON())
    return route.fulfill({ json: { result: { content: [{ type: 'text', text: 'refreshed' }] } } })
  })
}

const frame = (page) => page.frameLocator('[data-testid="chat-app-frame"]')

test.describe('Chat MCP apps', () => {
  test('shows the app under its tool line in a frame that has scripts and nothing else', async ({ page }) => {
    await stub(page)
    await page.goto('/app/chats/c1')
    const el = page.getByTestId('chat-app-frame')
    await expect(el).toBeVisible()
    await expect(el).toHaveAttribute('sandbox', 'allow-scripts')
    await expect(el).toHaveAttribute('referrerpolicy', 'no-referrer')
    const srcdoc = await el.getAttribute('srcdoc')
    expect(srcdoc.toLowerCase().startsWith('<!doctype html><meta http-equiv="content-security-policy"')).toBe(true)
    expect(srcdoc).toContain("connect-src 'none'")
    // The frame sits inside the tool entry, after its line.
    await expect(page.locator('.ch-tools').filter({ has: page.getByTestId('chat-app') })).toContainText('get-time')
  })

  test('the page gets the input and the result, calls a tool through the proxy and asks for a size', async ({ page }) => {
    const calls = []
    await stub(page, { calls })
    await page.goto('/app/chats/c1')
    await expect(frame(page).locator('#out')).toHaveText('12:00 UTC')
    await expect(frame(page).locator('#input')).toHaveText('{"zone":"UTC"}')

    await frame(page).locator('#call').click()
    await expect(frame(page).locator('#reply')).toContainText('refreshed')
    expect(calls).toEqual([{ server: 'connector:abc', tool: 'refresh', arguments: { zone: 'EU' } }])

    await frame(page).locator('#size').click()
    await expect.poll(async () => page.getByTestId('chat-app-frame').evaluate(el => Math.round(el.getBoundingClientRect().height))).toBe(333)

    await frame(page).locator('#unknown').click()
    await expect(frame(page).locator('#reply')).toContainText('-32601')
  })

  test('the frame cannot read the page, its storage or the network', async ({ page }) => {
    await stub(page)
    await page.goto('/app/chats/c1')
    await expect(frame(page).locator('#probe')).toHaveText('parent-blocked,storage-blocked,net-blocked')
  })

  test('a message the app suggests waits for a click, and a link opens only after one', async ({ page, context }) => {
    const sent = []
    await stub(page, { sent })
    await page.goto('/app/chats/c1')
    await expect(frame(page).locator('#out')).toHaveText('12:00 UTC')

    await frame(page).locator('#suggest').click()
    const ask = page.getByTestId('chat-app-suggestion')
    await expect(ask).toContainText('Show me March')
    expect(sent).toEqual([])
    await ask.getByRole('button', { name: 'Send' }).click()
    await expect.poll(() => sent).toEqual([{ text: 'Show me March' }])
    await expect(ask).toBeHidden()

    await frame(page).locator('#badlink').click()
    await expect(frame(page).locator('#reply')).toContainText('"isError":true')
    await expect(page.getByTestId('chat-app-link')).toHaveCount(0)

    await frame(page).locator('#link').click()
    const link = page.getByTestId('chat-app-link').getByRole('link', { name: 'Open' })
    await expect(link).toHaveAttribute('href', 'https://example.com/docs')
    await expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    await expect(link).toHaveAttribute('target', '_blank')
    expect(context.pages()).toHaveLength(1)
  })

  test('a message from any other window is ignored', async ({ page }) => {
    const calls = []
    await stub(page, { calls })
    await page.goto('/app/chats/c1')
    await expect(frame(page).locator('#out')).toHaveText('12:00 UTC')
    await page.evaluate(() => {
      const call = { jsonrpc: '2.0', id: 99, method: 'tools/call', params: { name: 'refresh', arguments: {} } }
      // From the page itself, and from a second sandboxed frame.
      window.postMessage(call, '*')
      const other = document.createElement('iframe')
      other.setAttribute('sandbox', 'allow-scripts')
      other.srcdoc = `<script>parent.postMessage(${JSON.stringify(call)}, '*')<\/script>`
      document.body.appendChild(other)
    })
    await page.waitForTimeout(500)
    expect(calls).toEqual([])
    // The frame's own messages still work.
    await frame(page).locator('#call').click()
    await expect(frame(page).locator('#reply')).toContainText('refreshed')
    expect(calls).toHaveLength(1)
  })

  test('shows the result as text when the page cannot be loaded', async ({ page }) => {
    await stub(page, { resource: route => route.fulfill({ status: 403, json: { error: { message: 'the connector cannot be used', type: 'mcp_app_refused' } } }) })
    await page.goto('/app/chats/c1')
    const fallback = page.getByTestId('chat-app-fallback')
    await expect(fallback).toContainText('The view could not be loaded')
    await expect(fallback).toContainText('the connector cannot be used')
    await expect(fallback).toContainText('12:00 UTC')
    await expect(page.getByTestId('chat-app-frame')).toHaveCount(0)
  })

  test.describe('on a phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    test('the frame fits the width and the page does not scroll sideways', async ({ page }) => {
      await stub(page)
      await page.goto('/app/chats/c1')
      await expect(frame(page).locator('#out')).toHaveText('12:00 UTC')
      const box = await page.getByTestId('chat-app-frame').boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(390)
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
    })
  })
})
