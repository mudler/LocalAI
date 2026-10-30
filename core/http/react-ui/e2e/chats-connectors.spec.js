import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The connectors page lists the messaging bindings by the connector add-on that
// serves them and sends the person to the add-on's own page to add or edit one.
// It has no form of its own and no switch between kinds. The API is stubbed;
// the add-on page has its own spec (telegram-addon.spec.js).

const AGENTS = [{ id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] }]
const KIND = { addon: 'telegram', kind: 'telegram', title: 'Telegram', description: 'Talk to one of your saved agents from a Telegram bot.', page: 'bots' }
const BINDING = { id: 'b1', name: 'Support bot', kind: 'telegram', addon: 'telegram', active: true, agent_id: 'a1', enabled: true, accept_any: false, allow: ['42'], has_token: true }

async function api(page, { kinds = [KIND], items = [BINDING] } = {}) {
  const s = { items: structuredClone(items), calls: [] }
  await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
  await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds } }))
  await page.route(/\/api\/chats\/connectors\/[^/?]+\/(enable|disable)$/, r => {
    const verb = new URL(r.request().url()).pathname.split('/').pop()
    s.calls.push(verb)
    s.items = s.items.map(x => ({ ...x, enabled: verb === 'enable' }))
    return r.fulfill({ json: s.items[0] })
  })
  await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => r.fulfill({ json: { connectors: s.items } }))
  await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
  await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
  return s
}

test.describe('Chat connectors', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('lists the bindings under the add-on that serves them, with a link to its page', async ({ page }) => {
    await api(page)
    await page.goto('/app/chats/connectors')
    const group = page.getByTestId('connector-group')
    await expect(group.getByRole('heading', { name: 'Telegram' })).toBeVisible()
    await expect(group.getByRole('link', { name: 'Add or change bindings' })).toHaveAttribute('href', '/app/addons/telegram/bots')
    await expect(group.getByTestId('connector-row')).toContainText('Support bot')
    await expect(group.getByTestId('connector-row')).toContainText('Agent: Support')
    await expect(group.getByTestId('connector-row')).toContainText('Accepts: 1 listed sender')
  })

  test('has no form, no kind switch and no Telegram fields', async ({ page }) => {
    await api(page)
    await page.goto('/app/chats/connectors')
    await expect(page.getByTestId('connector-row')).toBeVisible()
    await expect(page.getByRole('tablist')).toHaveCount(0)
    // The page has no New button; the sidebar's New chat (recent chats) is not part of it.
    await expect(page.getByRole('main').getByRole('button', { name: /New/ })).toHaveCount(0)
    await expect(page.getByLabel('Bot token')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0)
  })

  test('with no connector add-on installed it says so and links to the gallery', async ({ page }) => {
    await api(page, { kinds: [], items: [] })
    await page.goto('/app/chats/connectors')
    const note = page.getByTestId('no-connector-addon')
    await expect(note).toContainText('No connector add-on is installed.')
    await expect(note.getByRole('link', { name: 'Open the add-on gallery' })).toHaveAttribute('href', '/app/addons')
  })

  test('a binding whose add-on is missing is kept, with the reason', async ({ page }) => {
    await api(page, { kinds: [], items: [{ ...BINDING, active: false, reason: 'install the Telegram add-on' }] })
    await page.goto('/app/chats/connectors')
    await expect(page.getByTestId('connector-waiting')).toContainText('Support bot')
    await expect(page.getByTestId('connector-inactive')).toHaveText('Not running: install the Telegram add-on.')
  })

  test('switches a binding off', async ({ page }) => {
    const s = await api(page)
    await page.goto('/app/chats/connectors')
    await page.getByRole('checkbox', { name: 'Support bot on' }).uncheck()
    await expect.poll(() => s.calls.includes('disable')).toBe(true)
  })

  test('an old link to the MCP kind opens the MCP connectors page', async ({ page }) => {
    await api(page)
    await page.route('**/api/chats/mcp-connectors*', r => r.fulfill({ json: { connectors: [] } }))
    await page.goto('/app/chats/connectors?kind=mcp')
    await expect(page).toHaveURL(/\/app\/mcp-connectors$/)
    await expect(page.getByTestId('mcp-connectors-page')).toBeVisible()
  })
})
