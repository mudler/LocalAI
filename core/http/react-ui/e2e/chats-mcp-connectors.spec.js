import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The MCP server kind of the connectors page, with the API stubbed: secrets only
// go in, a connection test shows the tools or the reason, the servers the old
// chat page kept in this browser can be imported, and the saved-agent form lists
// the user's connectors.

const KEY = 'localai_client_mcp_servers'

function api(page, initial = [], { refuse = [] } = {}) {
  const s = { items: structuredClone(initial), calls: [], test: { ok: true, tools: ['search', 'fetch'] } }
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/connectors*', r => r.fulfill({ json: { connectors: [] } }))
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: [] } }))
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+\/test$/, r => {
        rec('POST', r.request().url(), null)
        return r.fulfill({ json: s.test })
      })
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+\/(enable|disable)$/, r => {
        const verb = new URL(r.request().url()).pathname.split('/').pop()
        rec('POST', r.request().url(), null)
        s.items = s.items.map(x => ({ ...x, enabled: verb === 'enable' }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+$/, r => {
        const req = r.request()
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.items = []; return r.fulfill({ status: 204, body: '' }) }
        const b = req.postDataJSON()
        s.items = s.items.map(x => ({ ...x, name: b.name, url: b.url, enabled: b.enabled }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/mcp-connectors(\?.*)?$/, r => {
        const req = r.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          rec('POST', req.url(), b)
          if (refuse.includes(b.name)) return r.fulfill({ status: 400, json: { error: { message: 'the address is on a private network' } } })
          const c = { id: `m${s.items.length + 1}`, args: [], has_token: Boolean(b.token), has_headers: Boolean(b.headers), has_env: false, ...b }
          delete c.token; delete c.headers
          s.items.push(c)
          return r.fulfill({ status: 201, json: c })
        }
        return r.fulfill({ json: { connectors: s.items } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
    },
  }
}

const rowOf = { id: 'm1', name: 'Docs', transport: 'http', url: 'https://mcp.example/path?key=abc', args: [], has_token: true, has_headers: true, has_env: false, enabled: false }

test.describe('Chat MCP connectors', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the page has a kind switch, and Telegram stays the first kind', async ({ page }) => {
    await api(page).install()
    await page.goto('/app/chats/connectors')
    await expect(page.getByRole('tab', { name: 'Telegram' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByRole('button', { name: 'New connector' })).toBeVisible()
    await page.getByRole('tab', { name: 'MCP server' }).click()
    await expect(page.getByRole('tab', { name: 'MCP server' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByText('No MCP servers yet.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'New connector' })).toHaveCount(0)
  })

  test('creates a server with a token and a header that only go in, and never shows them', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('button', { name: 'New MCP server' }).click()
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await page.getByLabel('Name', { exact: true }).fill('Docs')
    await page.getByLabel('Address').fill('https://mcp.example/mcp')
    await page.getByLabel('Bearer token').fill('SPEC-token-123')
    await page.getByRole('button', { name: 'Add a header' }).click()
    await page.getByLabel('Header name 1').fill('X-Team')
    await page.getByLabel('Value 1').fill('SPEC-header-456')
    await page.getByRole('button', { name: 'Save' }).click()

    const row = page.getByTestId('mcp-row')
    await expect(row).toContainText('Docs')
    await expect(row).toContainText('mcp.example')
    await expect(row).toContainText('token saved')
    await expect(row).toContainText('headers saved')
    const body = a.s.calls.find(c => c.method === 'POST').body
    expect(body).toMatchObject({ name: 'Docs', transport: 'http', url: 'https://mcp.example/mcp', token: 'SPEC-token-123', headers: { 'X-Team': 'SPEC-header-456' }, enabled: false })
    await expect(page.locator('body')).not.toContainText('SPEC-')
  })

  test('the secret fields are empty on edit, and an update without new ones sends none', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByLabel('Bearer token')).toHaveValue('')
    await expect(page.getByLabel('Bearer token')).toHaveAttribute('placeholder', /Saved/)
    await expect(page.getByText('Headers are saved. Add rows to replace them.')).toBeVisible()
    await page.getByLabel('Name', { exact: true }).fill('Renamed')
    await page.getByRole('button', { name: 'Save' }).click()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.url).toBe('/api/chats/mcp-connectors/m1')
    expect(put.body).toMatchObject({ name: 'Renamed' })
    expect('token' in put.body).toBe(false)
    expect('headers' in put.body).toBe(false)
  })

  test('removing a saved token sends the empty value on purpose', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('button', { name: 'Edit' }).click()
    await page.getByRole('checkbox', { name: 'Remove the saved token' }).check()
    await expect(page.getByLabel('Bearer token')).toBeDisabled()
    await page.getByRole('button', { name: 'Save' }).click()
    expect(a.s.calls.find(c => c.method === 'PUT').body.token).toBe('')
  })

  test('test connection shows the tools, or the reason it failed', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('button', { name: 'Test connection' }).click()
    await expect(page.getByTestId('mcp-test-result')).toContainText('Connected. Tools: search, fetch.')
    a.s.test = { ok: false, tools: [], error: 'the address is on a private network' }
    await page.getByRole('button', { name: 'Test connection' }).click()
    await expect(page.getByTestId('mcp-test-result')).toContainText('Could not connect: the address is on a private network')
  })

  test('switches on and off, and asks before it deletes', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('checkbox', { name: 'Docs on' }).check()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/enable'))).toBe(true)
    await page.getByTestId('mcp-row').getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('deleted with its saved token')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'DELETE')).toBe(true)
    await expect(page.getByText('No MCP servers yet.')).toBeVisible()
  })

  test('shows the servers the old chat page kept in this browser, without their secrets, and imports them switched off', async ({ page }) => {
    await page.addInitScript((key) => {
      if (!localStorage.getItem(key) && !sessionStorage.getItem('seeded')) {
        sessionStorage.setItem('seeded', '1')
        localStorage.setItem(key, JSON.stringify([
          { id: 'x1', name: 'Docs', url: 'https://mcp.example/mcp', headers: { Authorization: 'Bearer SPEC-browser-secret' }, useProxy: true },
          { id: 'x2', name: 'Refused', url: 'http://10.0.0.2/mcp', headers: {} },
        ]))
      }
    }, KEY)
    const a = api(page, [], { refuse: ['Refused'] })
    await a.install()
    await page.goto('/app/chats/connectors?kind=mcp')
    const banner = page.getByTestId('mcp-import')
    await expect(banner).toContainText('Servers kept in this browser')
    await expect(banner).toContainText('Docs')
    await expect(banner).toContainText('1 headers')
    await expect(page.locator('body')).not.toContainText('SPEC-browser-secret')
    await banner.getByRole('button', { name: 'Import 2 servers from this browser' }).click()

    await expect(page.getByRole('status').filter({ hasText: 'Imported 1' })).toContainText('Refused was not imported')
    const posts = a.s.calls.filter(c => c.method === 'POST').map(c => c.body)
    expect(posts[0]).toMatchObject({ name: 'Docs', transport: 'http', url: 'https://mcp.example/mcp', headers: { Authorization: 'Bearer SPEC-browser-secret' }, enabled: false })
    // Only what the server accepted leaves the browser.
    const left = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '[]').map(s => s.id), KEY)
    expect(left).toEqual(['x2'])
    await expect(page.locator('body')).not.toContainText('SPEC-browser-secret')
  })

  test('fits a phone: no sideways scroll, and the actions stay reachable', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await api(page, [rowOf]).install()
    await page.goto('/app/chats/connectors?kind=mcp')
    await page.getByRole('button', { name: 'New MCP server' }).click()
    await expect(page.getByRole('button', { name: 'Test connection' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
