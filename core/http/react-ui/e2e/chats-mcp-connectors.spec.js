import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { checkRecord } from '../src/utils/chatMCP.js'

// The MCP connectors page with the API stubbed: the list with a state per
// connector, search and filter, the details pane with the tools, the add and
// edit sheet (save, then test, then turn on), secrets that only go in, the
// switch, removal with a confirmation, the empty and error states, and the
// servers the old chat page kept in this browser.

const KEY = 'localai_client_mcp_servers'
const CHECKS = 'localai_mcp_checks'
const OK = { ok: true, tools: ['search', 'fetch'], tool_details: [{ name: 'search', description: 'Search the docs.' }, { name: 'fetch', description: '' }] }

function api(page, initial = [], { refuse = [], tests = {} } = {}) {
  const s = { items: structuredClone(initial), calls: [], test: OK, tests, failList: false }
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/connectors*', r => r.fulfill({ json: { connectors: [] } }))
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: [] } }))
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+\/test$/, r => {
        const id = new URL(r.request().url()).pathname.split('/').at(-2)
        rec('POST', r.request().url(), null)
        return r.fulfill({ json: s.tests[id] || s.test })
      })
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+\/(enable|disable)$/, r => {
        const parts = new URL(r.request().url()).pathname.split('/')
        const verb = parts.at(-1)
        const id = parts.at(-2)
        rec('POST', r.request().url(), null)
        s.items = s.items.map(x => (x.id === id ? { ...x, enabled: verb === 'enable', updated: new Date().toISOString() } : x))
        return r.fulfill({ json: s.items.find(x => x.id === id) })
      })
      await page.route(/\/api\/chats\/mcp-connectors\/[^/?]+$/, r => {
        const req = r.request()
        const id = new URL(req.url()).pathname.split('/').at(-1)
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.items = s.items.filter(x => x.id !== id); return r.fulfill({ status: 204, body: '' }) }
        const b = req.postDataJSON()
        s.items = s.items.map(x => (x.id === id ? { ...x, name: b.name, url: b.url, enabled: b.enabled } : x))
        return r.fulfill({ json: s.items.find(x => x.id === id) })
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
        if (s.failList) return r.fulfill({ status: 500, json: { error: { message: 'database is locked' } } })
        return r.fulfill({ json: { connectors: s.items } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
    },
  }
}

const rowOf = { id: 'm1', name: 'Docs', transport: 'http', url: 'https://mcp.example/path?key=abc', args: [], has_token: true, has_headers: true, has_env: false, enabled: true, created: '2026-10-20T10:00:00Z' }
const FIVE = [
  rowOf,
  { id: 'm2', name: 'Issues', transport: 'http', url: 'https://issues.example/mcp', args: [], has_token: true, has_headers: false, has_env: false, enabled: true },
  { id: 'm3', name: 'Weather', transport: 'http', url: 'https://weather.example/mcp', args: [], has_token: false, has_headers: false, has_env: false, enabled: false },
  { id: 'm4', name: 'Calendar', transport: 'http', url: 'https://calendar.example/mcp', args: [], has_token: false, has_headers: false, has_env: false, enabled: true },
  { id: 'm5', name: 'Wiki', transport: 'http', url: 'https://wiki.example/mcp', args: [], has_token: false, has_headers: false, has_env: false, enabled: true },
]
const row = (page, name) => page.getByTestId('mcp-row').filter({ hasText: name })
const menu = async (page, name, item) => {
  await row(page, name).getByRole('button', { name: `Actions for ${name}` }).click()
  await page.getByRole('menuitem', { name: item }).click()
}

test.describe('Chat MCP connectors', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the empty page teaches what a connector is and carries the add action; the old link opens it', async ({ page }) => {
    await api(page).install()
    await page.goto('/app/mcp-connectors')
    await expect(page.getByRole('heading', { name: 'MCP connectors', level: 1 })).toBeVisible()
    const empty = page.getByTestId('mcp-empty')
    await expect(empty.getByRole('heading', { name: 'No connectors yet' })).toBeVisible()
    await expect(empty).toContainText('Tokens and headers stored encrypted')
    await expect(empty.getByRole('link', { name: 'How MCP works' })).toHaveAttribute('href', /features\/chats-connectors\/#mcp-servers/)
    // The empty state carries the primary action, so the header does not.
    await expect(page.getByTestId('mcp-add')).toHaveCount(1)
    await expect(page.getByRole('tablist')).toHaveCount(0)
    await page.goto('/app/chats/connectors?kind=mcp')
    await expect(page).toHaveURL(/\/app\/mcp-connectors$/)
  })

  test('adds a server in the sheet: the secrets only go in, the test shows the tools, and Turn on switches it on', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.goto('/app/mcp-connectors')
    await page.getByTestId('mcp-add').click()
    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet.getByRole('heading', { name: 'Add connector' })).toBeVisible()
    await expect(sheet).toContainText('stored encrypted on the server')
    await sheet.getByLabel('Name', { exact: true }).fill('Docs')
    await sheet.getByLabel('Address').fill('https://mcp.example/mcp')
    await sheet.getByLabel('Bearer token').fill('SPEC-token-123')
    await expect(sheet.getByLabel('Bearer token')).toHaveAttribute('type', 'password')
    await sheet.getByRole('button', { name: 'Show the value' }).first().click()
    await expect(sheet.getByLabel('Bearer token')).toHaveAttribute('type', 'text')
    await sheet.getByRole('button', { name: 'Add a header' }).click()
    await sheet.getByLabel('Header name 1').fill('X-Team')
    await sheet.getByLabel('Value 1').fill('SPEC-header-456')
    // Enter in a field saves, like the primary button.
    await sheet.getByLabel('Address').press('Enter')

    await expect(sheet.getByTestId('mcp-sheet-ok')).toContainText('Docs offers 2 tools.')
    await expect(sheet.getByTestId('mcp-sheet-tools')).toContainText('Search the docs.')
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/mcp-connectors').body
    expect(body).toMatchObject({ name: 'Docs', transport: 'http', url: 'https://mcp.example/mcp', token: 'SPEC-token-123', headers: { 'X-Team': 'SPEC-header-456' }, enabled: false })
    expect(a.s.calls.some(c => c.url === '/api/chats/mcp-connectors/m1/test')).toBe(true)

    await sheet.getByTestId('mcp-sheet-turn-on').click()
    await expect(sheet).toHaveCount(0)
    expect(a.s.calls.some(c => c.url === '/api/chats/mcp-connectors/m1/enable')).toBe(true)
    const r = row(page, 'Docs')
    await expect(r.getByTestId('mcp-state')).toHaveAttribute('data-state', 'ok')
    await expect(r).toContainText('2 tools')
    await expect(r).toContainText('Token saved, Headers saved')
    await expect(page.locator('body')).not.toContainText('SPEC-')
  })

  test('checks the fields as the person types and saves nothing that is not valid', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await page.getByTestId('mcp-add').click()
    const sheet = page.getByTestId('mcp-sheet')
    await sheet.getByTestId('mcp-save').click()
    await expect(sheet).toContainText('Give it a name.')
    await expect(sheet).toContainText('Enter the address of the server.')
    await sheet.getByLabel('Name', { exact: true }).fill('docs')
    await expect(sheet).toContainText('Another connector has this name.')
    await sheet.getByLabel('Name', { exact: true }).fill('Second')
    await sheet.getByLabel('Address').fill('ftp://mcp.example')
    await expect(sheet).toContainText('The address must start with http:// or https://.')
    await expect(sheet.getByLabel('Address')).toHaveAttribute('aria-invalid', 'true')
    await sheet.getByTestId('mcp-save').click()
    expect(a.s.calls.filter(c => c.method === 'POST')).toHaveLength(0)
  })

  test('asks before it drops typed changes, on Escape and on Cancel', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await page.getByTestId('mcp-add').click()
    const sheet = page.getByTestId('mcp-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Draft')
    await page.keyboard.press('Escape')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Discard your changes?')
    await dialog.getByRole('button', { name: 'Keep editing' }).click()
    await expect(sheet.getByLabel('Name', { exact: true })).toHaveValue('Draft')
    await sheet.getByRole('button', { name: 'Cancel' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Discard' }).click()
    await expect(sheet).toHaveCount(0)
    expect(a.s.calls.filter(c => c.method === 'POST')).toHaveLength(0)
    // An untouched sheet closes at once.
    await page.getByTestId('mcp-add').click()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('mcp-sheet')).toHaveCount(0)
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
  })

  test('edits with empty secret fields, and an update without new ones sends none', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await menu(page, 'Docs', 'Edit')
    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet.getByRole('heading', { name: 'Edit Docs' })).toBeVisible()
    await expect(sheet.getByLabel('Bearer token')).toHaveValue('')
    await expect(sheet.getByLabel('Bearer token')).toHaveAttribute('placeholder', 'Saved')
    await expect(sheet.getByText('Headers are saved. New rows replace all of them.')).toBeVisible()
    await sheet.getByLabel('Name', { exact: true }).fill('Renamed')
    await sheet.getByTestId('mcp-save').click()
    await expect(sheet.getByTestId('mcp-sheet-test')).toBeVisible()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.url).toBe('/api/chats/mcp-connectors/m1')
    expect(put.body).toMatchObject({ name: 'Renamed' })
    expect('token' in put.body).toBe(false)
    expect('headers' in put.body).toBe(false)
    await sheet.getByTestId('mcp-sheet-done').click()
    await expect(row(page, 'Renamed')).toBeVisible()
  })

  test('removing a saved token sends the empty value on purpose', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await menu(page, 'Docs', 'Edit')
    const sheet = page.getByTestId('mcp-sheet')
    await sheet.getByRole('checkbox', { name: 'Remove the saved token' }).check()
    await expect(sheet.getByLabel('Bearer token')).toBeDisabled()
    await sheet.getByTestId('mcp-save').click()
    await expect(sheet.getByTestId('mcp-sheet-test')).toBeVisible()
    expect(a.s.calls.find(c => c.method === 'PUT').body.token).toBe('')
  })

  test('a test from the row gives the state: connected with the tools, an error with the reason, or a sign-in to replace', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    const r = row(page, 'Docs')
    await expect(r.getByTestId('mcp-state')).toHaveAttribute('data-state', 'unchecked')
    await r.getByRole('button', { name: 'Test the connection to Docs' }).click()
    await expect(r.getByTestId('mcp-state')).toHaveAttribute('data-state', 'ok')
    await expect(r.getByTestId('mcp-row-line')).toHaveText('search, fetch')
    await expect(r).toContainText(/Tested now|Tested .* ago/)

    a.s.test = { ok: false, tools: [], error: 'the address is on a private network' }
    await menu(page, 'Docs', 'Test connection')
    await expect(r.getByTestId('mcp-state')).toHaveAttribute('data-state', 'error')
    await expect(r.getByTestId('mcp-row-line')).toHaveText('The address is on a private network.')

    a.s.test = { ok: false, tools: [], error: 'the server answered 401 Unauthorized' }
    await r.getByRole('button', { name: 'Test the connection to Docs' }).click()
    await expect(r.getByTestId('mcp-state')).toHaveAttribute('data-state', 'auth')
    await expect(r.getByTestId('mcp-state')).toContainText('Needs sign-in')
    // Replace token opens the edit sheet on the token field.
    await r.getByTestId('mcp-row-reauth').click()
    await expect(page.getByTestId('mcp-sheet').getByLabel('Bearer token')).toBeFocused()
  })

  test('the last test stays in this browser across a reload, until the connector changes', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await row(page, 'Docs').getByRole('button', { name: 'Test the connection to Docs' }).click()
    await expect(row(page, 'Docs').getByTestId('mcp-state')).toHaveAttribute('data-state', 'ok')
    await page.reload()
    await expect(row(page, 'Docs').getByTestId('mcp-state')).toHaveAttribute('data-state', 'ok')
    const kept = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), CHECKS)
    expect(kept.m1.tools.map(x => x.name)).toEqual(['search', 'fetch'])
    expect(JSON.stringify(kept)).not.toContain('key=abc')
    // A new address makes the old result stale.
    a.s.items = a.s.items.map(x => ({ ...x, url: 'https://other.example/mcp' }))
    await page.reload()
    await expect(row(page, 'Docs').getByTestId('mcp-state')).toHaveAttribute('data-state', 'unchecked')
  })

  test('the details pane lists the tools with their descriptions, hides the query of the address, and switches the connector', async ({ page }) => {
    const many = { ok: true, tools: [], tool_details: Array.from({ length: 7 }, (_, i) => ({ name: `tool_${i}`, description: i === 3 ? 'Reads the calendar.' : `Tool number ${i}.` })) }
    many.tools = many.tool_details.map(x => x.name)
    const a = api(page, [rowOf], { tests: { m1: many } })
    await a.install()
    await page.goto('/app/mcp-connectors')
    await row(page, 'Docs').locator('.ad-tile__open').click()
    const pane = page.getByTestId('mcp-details')
    await expect(pane.getByRole('heading', { name: 'Docs' })).toBeVisible()
    await expect(pane).toContainText('Not tested in this browser yet.')
    await expect(pane.getByTestId('mcp-details-address')).toHaveText('https://mcp.example/path?…')
    await expect(pane).toContainText('Stored encrypted on the server.')
    await pane.getByTestId('mcp-details-test').click()
    await expect(pane.getByTestId('mcp-tools').locator('li')).toHaveCount(7)
    await pane.getByTestId('mcp-tools-search').fill('calendar')
    await expect(pane.getByTestId('mcp-tools').locator('li')).toHaveCount(1)
    await expect(pane.getByTestId('mcp-tools')).toContainText('tool_3')
    await pane.getByRole('switch', { name: 'Use in chats' }).click()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/m1/disable'))).toBe(true)
    await expect(row(page, 'Docs').getByTestId('mcp-state')).toHaveAttribute('data-state', 'off')
    await page.keyboard.press('Escape')
    await expect(pane).toHaveCount(0)
    await expect(page).not.toHaveURL(/detail=/)
  })

  test('the row switch applies at once and rolls back when the server refuses', async ({ page }) => {
    const a = api(page, [{ ...rowOf, enabled: false }])
    await a.install()
    await page.goto('/app/mcp-connectors')
    const sw = row(page, 'Docs').getByRole('switch', { name: 'Use Docs in chats' })
    await expect(sw).toHaveAttribute('aria-checked', 'false')
    await sw.click()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/m1/enable'))).toBe(true)
    await expect(sw).toHaveAttribute('aria-checked', 'true')
    await page.route(/\/api\/chats\/mcp-connectors\/m1\/disable$/, r => r.fulfill({ status: 400, json: { error: { message: 'not allowed now' } } }))
    await sw.click()
    await expect(page.getByTestId('mcp-error')).toContainText('not allowed now')
    await expect(sw).toHaveAttribute('aria-checked', 'true')
  })

  test('removes after an in-page confirmation, and the page returns to its empty state', async ({ page }) => {
    const a = api(page, [rowOf])
    await a.install()
    await page.goto('/app/mcp-connectors')
    await menu(page, 'Docs', 'Remove')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Remove Docs?')
    await expect(dialog).toContainText('deleted with its saved token')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(a.s.calls.some(c => c.method === 'DELETE')).toBe(false)
    await menu(page, 'Docs', 'Remove')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Remove' }).click()
    await expect.poll(() => a.s.calls.some(c => c.method === 'DELETE')).toBe(true)
    await expect(page.getByTestId('mcp-empty')).toBeVisible()
  })

  test('search and the state filter narrow the list, with counts, and say when nothing matches', async ({ page }) => {
    const seed = {
      m2: checkRecord(FIVE[1], { ok: false, error: 'the server answered 503' }),
      m4: checkRecord(FIVE[3], { ok: false, error: 'the server answered 401 Unauthorized' }),
    }
    await page.addInitScript(([k, v]) => localStorage.setItem(k, JSON.stringify(v)), [CHECKS, seed])
    await api(page, FIVE).install()
    await page.goto('/app/mcp-connectors')
    await expect(page.getByTestId('mcp-row')).toHaveCount(5)
    await expect(page.getByTestId('mcp-filter-attention')).toContainText('2')
    await page.getByTestId('mcp-filter-attention').click()
    await expect(page.getByTestId('mcp-row')).toHaveCount(2)
    await expect(row(page, 'Calendar').getByTestId('mcp-state')).toContainText('Needs sign-in')
    await page.getByTestId('mcp-filter-off').click()
    await expect(page.getByTestId('mcp-row')).toHaveCount(1)
    await expect(page.getByTestId('mcp-row')).toContainText('Weather')
    await page.getByTestId('mcp-filter-all').click()
    await page.getByTestId('mcp-search').fill('wiki')
    await expect(page.getByTestId('mcp-row')).toHaveCount(1)
    await page.getByTestId('mcp-search').fill('calendar.example')
    await expect(page.getByTestId('mcp-row')).toHaveCount(1)
    await page.getByTestId('mcp-search').fill('nothing here')
    await expect(page.getByTestId('mcp-no-match')).toContainText('No connector matches “nothing here”')
    await page.getByRole('button', { name: 'Clear search' }).click()
    await expect(page.getByTestId('mcp-row')).toHaveCount(5)
  })

  test('a failed list read is a calm error with Try again, never an empty page', async ({ page }) => {
    const a = api(page, [rowOf])
    a.s.failList = true
    await a.install()
    await page.goto('/app/mcp-connectors')
    const err = page.getByTestId('mcp-load-error')
    await expect(err).toContainText('Could not load your connectors.')
    await expect(err).toContainText('database is locked')
    await expect(page.getByTestId('mcp-empty')).toHaveCount(0)
    a.s.failList = false
    await err.getByRole('button', { name: 'Try again' }).click()
    await expect(row(page, 'Docs')).toBeVisible()
  })

  test('a failed test in the sheet says why and offers Test again; Keep it off closes it', async ({ page }) => {
    const a = api(page)
    a.s.test = { ok: false, tools: [], error: 'the server answered 401 Unauthorized' }
    await a.install()
    await page.goto('/app/mcp-connectors')
    await page.getByTestId('mcp-add').click()
    const sheet = page.getByTestId('mcp-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Billing')
    await sheet.getByLabel('Address').fill('https://billing.example/mcp')
    await sheet.getByTestId('mcp-save').click()
    await expect(sheet.getByTestId('mcp-sheet-fail')).toContainText('The server answered 401 Unauthorized.')
    await expect(sheet.getByTestId('mcp-sheet-fail')).toContainText('Check the token or the headers.')
    await expect(sheet.getByTestId('mcp-sheet-turn-on')).toHaveCount(0)
    a.s.test = OK
    await sheet.getByTestId('mcp-sheet-retry').click()
    await expect(sheet.getByTestId('mcp-sheet-ok')).toBeVisible()
    await sheet.getByTestId('mcp-sheet-keep-off').click()
    await expect(row(page, 'Billing').getByTestId('mcp-state')).toHaveAttribute('data-state', 'off')
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
    await page.goto('/app/mcp-connectors')
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

  test('fits a phone: no sideways scroll, 44 px targets on the row, and the sheet keeps its actions in reach', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await api(page, FIVE).install()
    await page.goto('/app/mcp-connectors')
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(await overflow()).toBeLessThanOrEqual(0)
    const kebab = await row(page, 'Docs').getByRole('button', { name: 'Actions for Docs' }).boundingBox()
    expect(kebab.height).toBeGreaterThanOrEqual(44)
    expect(kebab.width).toBeGreaterThanOrEqual(44)
    await page.getByTestId('mcp-add').click()
    await expect(page.getByTestId('mcp-save')).toBeInViewport()
    expect(await overflow()).toBeLessThanOrEqual(0)
  })
})
