import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi } from './connector-fixtures.js'

// The page of the Discord add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed: one bot token, one allowlist of IDs that
// refuses names, the anyone mode with its plain warning, and the setup that
// builds the invite link with the five permissions.

const PAGE = '/app/addons/discord/bots'
const TOKEN = 'MTAwMDAwMDAwMDAwMDAwMDAw.GabcDE.' + 'x'.repeat(27)
const USER = '123456789012345678'
const CHANNEL = '223456789012345678'
const NEWCOMER = { id: '323456789012345678', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }
const BINDING = { id: 'b1', name: 'Team bot', kind: 'discord', agent_id: 'a1', enabled: true, accept_any: false, allow: [USER, CHANNEL], has_token: true }
const api = (page, initial = []) => connectorApi(page, initial, 'discord', [NEWCOMER])

test.describe('Discord add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['discord'] })
  })

  test('creates a binding with a token and a list of IDs, and checks the token', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: '1', bot_username: 'localai-bot' } }))
    await page.goto(PAGE)

    await expect(page.getByText('No Discord bots yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New Discord bot' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Team bot')
    await page.getByLabel('Bot token').fill('short')
    await expect(page.getByTestId('token-problem')).toContainText('too short')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await page.getByLabel('Bot token').fill(TOKEN)
    await expect(page.getByTestId('token-problem')).toHaveCount(0)
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await expect(page.getByTestId('discord-rules')).toContainText('only when it is mentioned')
    const list = page.getByRole('textbox', { name: 'Allowed people and channels +' })
    await list.fill('@ada')
    await list.press('Enter')
    await expect(page.getByTestId('discord-bad-ids')).toContainText('@ada')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await page.getByRole('button', { name: '@ada ×' }).click()
    await expect(page.getByTestId('discord-bad-ids')).toHaveCount(0)
    for (const id of [USER, CHANNEL]) {
      await list.fill(id)
      await list.press('Enter')
    }
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('connector-row')).toContainText('Team bot')
    await expect(page.getByTestId('connector-row')).toContainText('Accepts: 2 listed entries')
    await expect(page.getByText('Connected to localai-bot.')).toBeVisible()
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body).toMatchObject({ kind: 'discord', name: 'Team bot', token: TOKEN, agent_id: 'a1', allow: [USER, CHANNEL] })
    await expect(page.locator('body')).not.toContainText(TOKEN)
  })

  test('the anyone mode says what it gives, and needs the confirmation', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: '1', bot_username: 'localai-bot' } }))
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Discord bot' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Public bot')
    await page.getByLabel('Bot token').fill(TOKEN)
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByText('Anyone who can message the bot, or mention it in a channel it can read').click()
    const warning = page.getByTestId('anyone-warning')
    await expect(warning).toContainText('public channels')
    await expect(warning).toContainText('hold no permission of their own')
    await expect(warning).toContainText('prompt injection')
    await expect(warning).toContainText('read-only tools')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await warning.getByRole('checkbox').check()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('connector-row')).toContainText('Accepts: anyone')
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body).toMatchObject({ kind: 'discord', accept_any: true, allow: [] })
  })

  test('an edit keeps the stored token unless one is typed', async ({ page }) => {
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByLabel('Bot token')).toHaveValue('')
    await expect(page.getByLabel('Bot token')).toHaveAttribute('placeholder', /Saved/)
    await expect(page.getByTestId('connector-allow')).toContainText(CHANNEL)
    await page.getByRole('button', { name: `${CHANNEL} ×` }).click()
    await page.getByRole('button', { name: 'Save' }).click()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.url).toBe('/api/chats/connectors/b1')
    expect('token' in put.body).toBe(false)
    expect(put.body.allow).toEqual([USER])
  })

  test('shows how to make the bot, and builds the invite link with the five permissions', async ({ page }) => {
    await api(page).install()
    await page.goto(PAGE)
    const setup = page.getByTestId('discord-setup')
    await setup.getByText('How to make the Discord bot').click()
    await expect(setup).toContainText('Message Content Intent')
    await expect(setup).toContainText('274878008320')
    await expect(setup).toContainText('Never give the bot Administrator')
    await setup.getByTestId('discord-app-id').fill('abc')
    await expect(setup.getByTestId('discord-invite')).toHaveCount(0)
    await setup.getByTestId('discord-app-id').fill(USER)
    await expect(setup.getByTestId('discord-invite')).toHaveText(`https://discord.com/oauth2/authorize?client_id=${USER}&scope=bot&permissions=274878008320`)
    for (const p of ['View Channels', 'Send Messages', 'Send Messages in Threads', 'Attach Files', 'Read Message History']) {
      await expect(setup).toContainText(p)
    }
  })

  test('offers the thread option and the !new hint', async ({ page }) => {
    await api(page).install()
    await page.route('**/api/chats/connectors/kinds*', r => r.fulfill({ json: { capabilities: [], kinds: [{ kind: 'discord', title: 'Discord', files: null,
      options: [{ id: 'followThreads', title: 'Threads the bot follows', description: 'd', default: 'mentioned', values: [{ id: 'mentioned', title: 'Threads where it was mentioned' }, { id: 'never', title: 'None: always ask with a mention' }] }] }] } }))
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Discord bot' }).click()
    await expect(page.getByTestId('connector-option-followThreads')).toHaveValue('mentioned')
    await expect(page.getByTestId('connector-answer')).toContainText('Send !new in Discord')
  })

  test('tests the token, shows the sender log and allows a sender', async ({ page }) => {
    const a = api(page, [BINDING])
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: '1', bot_username: 'localai-bot' } }))
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Test' }).click()
    await expect(page.getByText('Connected to localai-bot.')).toBeVisible()
    await page.getByRole('button', { name: 'Senders' }).click()
    await expect(page.getByTestId('sender-log')).toContainText(`Newcomer · 2 messages · ${NEWCOMER.id}`)
    await page.getByRole('button', { name: 'Allow', exact: true }).click()
    await expect(page.getByTestId('sender-log')).toContainText('Accepted')
  })
})
