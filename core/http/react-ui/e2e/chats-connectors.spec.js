import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The connections page lists the bindings by the connector add-on that serves
// them and sends the person to the add-on's own page to add or edit one. Its
// tabs split the kinds: Accounts (kinds that need no agent) and Messaging
// (bots that answer as an agent); the MCP servers tab is its own page. It has
// no form of its own. The API is stubbed; the add-on page has its own spec
// (telegram-addon.spec.js).

const MESSAGING = '/app/chats/connectors?tab=messaging'
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
    await page.goto(MESSAGING)
    const group = page.getByTestId('connector-group')
    await expect(group.getByRole('heading', { name: 'Telegram' })).toBeVisible()
    await expect(group.getByRole('link', { name: 'Add or change bindings' })).toHaveAttribute('href', '/app/addons/telegram/bots')
    await expect(group.getByTestId('connector-row')).toContainText('Support bot')
    await expect(group.getByTestId('connector-row')).toContainText('Answers as Support')
    await expect(group.getByTestId('connector-row')).toContainText('Accepts 1 person')
  })

  test('has no form, no kind switch and no Telegram fields', async ({ page }) => {
    await api(page)
    await page.goto(MESSAGING)
    await expect(page.getByTestId('connector-row')).toBeVisible()
    await expect(page.getByRole('tablist')).toHaveCount(0)
    await expect(page.getByTestId('chat-connectors-page')).toHaveAttribute('data-tab', 'messaging')
    // The one New button is a menu of platforms; the form itself is on the page of the add-on.
    await expect(page.getByRole('main').getByRole('button', { name: /New/ })).toHaveCount(1)
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
    await page.goto(MESSAGING)
    await expect(page.getByTestId('connector-waiting')).toContainText('Support bot')
    await expect(page.getByTestId('connector-inactive')).toHaveText('Not running: install the Telegram add-on.')
  })

  test('each row has a state chip and a switch that applies at once', async ({ page }) => {
    const s = await api(page)
    await page.goto(MESSAGING)
    await expect(page.getByTestId('connector-state')).toHaveText('Running')
    await expect(page.getByRole('checkbox')).toHaveCount(0)
    await page.getByRole('switch', { name: 'Support bot on' }).click()
    await expect.poll(() => s.calls.includes('disable')).toBe(true)
    await expect(page.getByTestId('connector-state')).toHaveText('Off')
  })

  test('a binding with no token needs attention', async ({ page }) => {
    await api(page, { items: [{ ...BINDING, has_token: false }] })
    await page.goto(MESSAGING)
    await expect(page.getByTestId('connector-state')).toHaveText('Needs attention')
  })

  test('New connection lists the installed platforms and opens the add form of the one you pick', async ({ page }) => {
    await api(page, { kinds: [KIND, { addon: 'slack', kind: 'slack', title: 'Slack', page: 'apps' }, { addon: 'github', kind: 'github', title: 'GitHub' }] })
    await page.goto(MESSAGING)
    await page.getByTestId('connector-new').click()
    const menu = page.getByRole('menu', { name: 'Choose the platform of the new connection' })
    // A kind with no declared page is not offered.
    await expect(menu.getByRole('menuitem')).toHaveText(['Telegram', 'Slack'])
    await menu.getByRole('menuitem', { name: 'Slack' }).click()
    await expect(page).toHaveURL(/\/app\/addons\/slack\/apps\?new=1$/)
  })

  test('with no platform that has a page there is no New connection menu', async ({ page }) => {
    await api(page, { kinds: [{ addon: 'github', kind: 'github', title: 'GitHub' }], items: [] })
    await page.goto(MESSAGING)
    await expect(page.getByTestId('connector-new')).toHaveCount(0)
  })

  test('warns on a binding that is the same bot as another, links to it and turns it off', async ({ page }) => {
    const twin = { ...BINDING, id: 'b2', name: 'Support bot copy' }
    const pair = (x, y) => ({ ...x, duplicate: { code: 'duplicate_connection', message: '', others: [{ id: y.id, name: y.name }] } })
    const s = await api(page, { items: [pair(BINDING, twin), pair(twin, BINDING)] })
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/disable$/, r => {
      s.calls.push('disable')
      s.items = s.items.map(x => ({ ...x, enabled: false, duplicate: undefined }))
      return r.fulfill({ json: s.items[0] })
    })
    await page.goto(MESSAGING)
    const rows = page.getByTestId('connector-row')
    await expect(rows).toHaveCount(2)
    await expect(rows.first().getByTestId('connector-duplicate')).toContainText('This bot is also connected as Support bot copy. Both connections will answer the same message.')
    await expect(rows.last().getByTestId('connector-duplicate')).toContainText('This bot is also connected as Support bot. Both connections will answer the same message.')
    await expect(rows.first().getByTestId('connector-duplicate-open')).toHaveAttribute('href', '/app/addons/telegram/bots?binding=b2')
    await expect(rows.last().getByTestId('connector-duplicate-open')).toHaveAttribute('href', '/app/addons/telegram/bots?binding=b1')

    await page.setViewportSize({ width: 360, height: 800 })
    await page.reload()
    await expect(rows).toHaveCount(2)
    await expect(page.getByTestId('connector-duplicate')).toHaveCount(2)
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)

    await rows.last().getByRole('button', { name: 'Turn this one off' }).click()
    await expect.poll(() => s.calls.includes('disable')).toBe(true)
    await expect(page.getByTestId('connector-duplicate')).toHaveCount(0)
  })

  test('an old link to the MCP kind opens the MCP connectors page', async ({ page }) => {
    await api(page)
    await page.route('**/api/chats/mcp-connectors*', r => r.fulfill({ json: { connectors: [] } }))
    await page.goto('/app/chats/connectors?kind=mcp')
    await expect(page).toHaveURL(/\/app\/mcp-connectors$/)
    await expect(page.getByTestId('mcp-connectors-page')).toBeVisible()
  })

  test('Accounts is the first tab: it lists the kinds that need no agent, and Messaging the others', async ({ page }) => {
    const github = { addon: 'github', kind: 'github', title: 'GitHub', page: 'apps', agent_optional: true, tool_only: true }
    const account = { id: 'b2', name: 'Work repos', kind: 'github', addon: 'github', active: true, agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], has_token: true }
    await api(page, { kinds: [KIND, github], items: [BINDING, account] })
    await page.goto('/app/chats/connectors')
    await expect(page.getByTestId('chat-connectors-page')).toHaveAttribute('data-tab', 'accounts')
    await expect(page.getByTestId('connector-group').getByRole('heading')).toHaveText(['GitHub'])
    await expect(page.getByTestId('connector-row')).toContainText('Work repos')
    await page.getByTestId('connections-tab-messaging').click()
    await expect(page).toHaveURL(/\/app\/chats\/connectors\?tab=messaging$/)
    await expect(page.getByTestId('connector-group').getByRole('heading')).toHaveText(['Telegram'])
    await expect(page.getByTestId('connector-row')).toContainText('Support bot')
  })

  test('an older link with ?kind= opens the tab that holds the kind', async ({ page }) => {
    await api(page)
    await page.goto('/app/chats/connectors?kind=telegram')
    await expect(page.getByTestId('chat-connectors-page')).toHaveAttribute('data-tab', 'messaging')
  })

  test('the tab strip is on both pages, opens the other page, and the title is Connections', async ({ page }) => {
    await api(page)
    await page.route('**/api/chats/mcp-connectors*', r => r.fulfill({ json: { connectors: [] } }))
    await page.goto('/app/chats/connectors')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Connections')
    const tabs = page.getByTestId('connections-tabs')
    await expect(tabs.getByRole('link')).toHaveText(['Accounts', 'Messaging', 'MCP servers'])
    await expect(tabs.getByTestId('connections-tab-accounts')).toHaveAttribute('aria-current', 'page')
    await tabs.getByTestId('connections-tab-mcp').click()
    await expect(page).toHaveURL(/\/app\/mcp-connectors$/)
    await expect(page.getByTestId('mcp-connectors-page')).toBeVisible()
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Connections')
    await expect(page.getByTestId('connections-tab-mcp')).toHaveAttribute('aria-current', 'page')
    await page.getByTestId('connections-tab-accounts').click()
    await expect(page).toHaveURL(/\/app\/chats\/connectors$/)
  })
})
