import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The connectors page with the API stubbed: the token only goes in, "anyone"
// needs a confirmation, and a sender in the log can be allowed.

const AGENTS = [
  { id: 'a1', name: 'Support', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] },
  { id: 'a2', name: 'No model', model: '', mcp_servers: [], addons: [], collections: [], skills: [] },
]

function api(page, initial = []) {
  const s = { items: structuredClone(initial), calls: [], senders: [{ id: '42', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }] }
  const rec = (method, url, body) => s.calls.push({ method, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/agents*', r => r.fulfill({ json: { agents: AGENTS } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders\/[^/?]+\/allow$/, r => {
        rec('POST', r.request().url(), null)
        s.senders = s.senders.map(x => ({ ...x, allowed: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/senders$/, r => r.fulfill({ json: { senders: s.senders } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: 1, bot_username: 'support_bot' } }))
      await page.route(/\/api\/chats\/connectors\/[^/?]+\/(enable|disable)$/, r => {
        const verb = new URL(r.request().url()).pathname.split('/').pop()
        rec('POST', r.request().url(), null)
        s.items = s.items.map(x => ({ ...x, enabled: verb === 'enable' }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors\/[^/?]+$/, r => {
        const req = r.request()
        rec(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') { s.items = []; return r.fulfill({ status: 204, body: '' }) }
        s.items = s.items.map(x => ({ ...x, ...req.postDataJSON(), token: undefined, has_token: true }))
        return r.fulfill({ json: s.items[0] })
      })
      await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
        const req = r.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          rec('POST', req.url(), b)
          s.items.push({ id: 'b1', kind: 'telegram', allow: [], ...b, token: undefined, has_token: true })
          return r.fulfill({ status: 201, json: s.items.at(-1) })
        }
        return r.fulfill({ json: { connectors: s.items } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, r => r.fulfill({ json: { conversations: [], persistent: true, files: true } }))
      await page.route('**/api/chats/notifications*', r => r.fulfill({ json: { notifications: [], unread: 0 } }))
    },
  }
}

test.describe('Chat connectors', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('creates a binding with a token and an agent, and lists who it accepts', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.goto('/app/chats/connectors')

    await expect(page.getByText('No connectors yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New connector' }).click()
    await expect(page.getByRole('option', { name: 'No model (needs a model)' })).toHaveAttribute('disabled', '')
    await page.getByLabel('Name', { exact: true }).fill('Support bot')
    await page.getByLabel('Bot token').fill('555:secret-token')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('combobox', { name: 'Allowed senders +' }).fill('42')
    await page.getByRole('combobox', { name: 'Allowed senders +' }).press('Enter')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('connector-row')).toContainText('Support bot')
    await expect(page.getByTestId('connector-row')).toContainText('Agent: Support')
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body).toMatchObject({ name: 'Support bot', token: '555:secret-token', agent_id: 'a1', allow: ['42'], accept_any: false })
    await expect(page.locator('body')).not.toContainText('secret-token')
  })

  test('the token field is empty on edit, and an update without a new token sends none', async ({ page }) => {
    const a = api(page, [{ id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: ['42'], has_token: true }])
    await a.install()
    await page.goto('/app/chats/connectors')
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByLabel('Bot token')).toHaveValue('')
    await expect(page.getByLabel('Bot token')).toHaveAttribute('placeholder', /Saved/)
    await page.getByLabel('Name', { exact: true }).fill('Renamed')
    await page.getByRole('button', { name: 'Save' }).click()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.url).toBe('/api/chats/connectors/b1')
    expect('token' in put.body).toBe(false)
  })

  test('anyone needs the warning confirmed before it can be saved', async ({ page }) => {
    await api(page).install()
    await page.goto('/app/chats/connectors')
    await page.getByRole('button', { name: 'New connector' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Public bot')
    await page.getByLabel('Bot token').fill('1:x')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('radio', { name: 'Anyone who can message the bot' }).check()
    await expect(page.getByTestId('anyone-warning')).toContainText("can use this agent's tools")
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await page.getByRole('checkbox', { name: 'I understand' }).check()
    await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()
  })

  test('tests the token, shows the sender log and allows a sender', async ({ page }) => {
    const a = api(page, [{ id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: [], has_token: true }])
    await a.install()
    await page.goto('/app/chats/connectors')
    await page.getByRole('button', { name: 'Test' }).click()
    await expect(page.getByText('Connected to @support_bot.')).toBeVisible()
    await page.getByRole('button', { name: 'Senders' }).click()
    await expect(page.getByTestId('sender-log')).toContainText('Newcomer · 2 messages · 42')
    await expect(page.getByTestId('sender-log')).toContainText('Ignored')
    await page.getByRole('button', { name: 'Allow' }).click()
    await expect(page.getByTestId('sender-log')).toContainText('Accepted')
    expect(a.s.calls.some(c => c.url === '/api/chats/connectors/b1/senders/42/allow')).toBe(true)
  })

  test('switches a binding off and deletes it', async ({ page }) => {
    const a = api(page, [{ id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: [], has_token: true }])
    await a.install()
    await page.goto('/app/chats/connectors')
    await page.getByRole('checkbox', { name: 'Support bot on' }).uncheck()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/disable'))).toBe(true)
    await page.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('connector-row')).toHaveCount(0)
  })
})
