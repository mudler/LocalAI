import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi as api } from './connector-fixtures.js'

// The page of the Telegram add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed: the token only goes in, "anyone"
// needs a confirmation, and a sender in the log can be allowed.

const PAGE = '/app/addons/telegram/bots'

test.describe('Telegram add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['telegram'] })
  })

  test('creates a binding with a token and an agent, and lists who it accepts', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.goto(PAGE)

    await expect(page.getByText('No Telegram bots yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New Telegram bot' }).click()
    await expect(page.getByRole('option', { name: 'No model (needs a model)' })).toHaveAttribute('disabled', '')
    await page.getByLabel('Name', { exact: true }).fill('Support bot')
    await page.getByLabel('Bot token').fill('555:secret-token')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('textbox', { name: 'Allowed senders +' }).fill('42')
    await page.getByRole('textbox', { name: 'Allowed senders +' }).press('Enter')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('connector-row')).toContainText('Support bot')
    await expect(page.getByTestId('connector-row')).toContainText('Agent: Support')
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body).toMatchObject({ name: 'Support bot', token: '555:secret-token', agent_id: 'a1', allow: ['42'], accept_any: false })
    await expect(page.locator('body')).not.toContainText('secret-token')
    // A new token is checked as soon as it is saved.
    await expect(page.getByText('Connected to @support_bot.')).toBeVisible()
  })

  test('the token field is empty on edit, and an update without a new token sends none', async ({ page }) => {
    const a = api(page, [{ id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: ['42'], has_token: true }])
    await a.install()
    await page.goto(PAGE)
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
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Telegram bot' }).click()
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
    await page.goto(PAGE)
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
    await page.goto(PAGE)
    await page.getByRole('checkbox', { name: 'Support bot on' }).uncheck()
    await expect.poll(() => a.s.calls.some(c => c.url.endsWith('/disable'))).toBe(true)
    // Deleting asks first, and says the bot token goes with it.
    await page.getByTestId('connector-row').getByRole('button', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete Support bot?')
    await expect(dialog).toContainText('bot token')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('connector-row')).toHaveCount(1)
    expect(a.s.calls.some(c => c.method === 'DELETE')).toBe(false)

    await page.getByTestId('connector-row').getByRole('button', { name: 'Delete' }).click()
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('connector-row')).toHaveCount(0)
  })

  test('a delete the server refuses shows its reason and keeps the binding', async ({ page }) => {
    const a = api(page, [{ id: 'b1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', enabled: true, accept_any: false, allow: [], has_token: true }])
    await a.install()
    await page.route(/\/api\/chats\/connectors\/b1$/, r => r.fulfill({ status: 500, json: { error: { message: 'the store is read-only' } } }))
    await page.goto(PAGE)
    await page.getByTestId('connector-row').getByRole('button', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByRole('alert')).toContainText('the store is read-only')
    await expect(page.getByTestId('connector-row')).toHaveCount(1)
  })
})
