import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi } from './connector-fixtures.js'

// The page of the Slack add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed: both tokens go in as one secret and are
// checked on save, people and channels are two lists of one allowlist, and
// the setup shows the app manifest.

const PAGE = '/app/addons/slack/apps'
const NEWCOMER = { id: 'U042', name: 'Newcomer', count: 2, allowed: false, first: '2026-10-27T08:00:00Z', last: '2026-10-27T08:05:00Z' }
const BINDING = { id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'C001'], has_token: true }
const api = (page, initial = []) => connectorApi(page, initial, 'slack', [NEWCOMER])

test.describe('Slack add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
  })

  test('creates a binding with both tokens, people and channels, and checks the tokens', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: 'UBOT', bot_username: 'localai (Acme)' } }))
    await page.goto(PAGE)

    await expect(page.getByText('No Slack apps yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Team app')
    await expect(page.getByTestId('connector-thread-context')).toHaveCount(0)
    await page.getByLabel('Bot token').fill('xoxb-secret-bot')
    await expect(page.getByTestId('token-problem')).toContainText('Type both tokens')
    await expect(page.getByRole('button', { name: 'Save' })).toBeDisabled()
    await page.getByLabel('App-level token').fill('xapp-secret-app')
    await expect(page.getByTestId('token-problem')).toHaveCount(0)
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('textbox', { name: 'Allowed people +' }).fill('U001')
    await page.getByRole('textbox', { name: 'Allowed people +' }).press('Enter')
    await page.getByRole('textbox', { name: 'Allowed channels +' }).fill('C001')
    await page.getByRole('textbox', { name: 'Allowed channels +' }).press('Enter')
    await expect(page.getByTestId('connector-allow')).toContainText('U001')
    await expect(page.getByTestId('connector-allow')).not.toContainText('C001')
    await expect(page.getByTestId('connector-allow-channels')).toContainText('C001')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('connector-row')).toContainText('Team app')
    await expect(page.getByTestId('connector-row')).toContainText('Accepts: 2 listed entries')
    await expect(page.getByText('Connected to localai (Acme).')).toBeVisible()
    const body = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors').body
    expect(body).toMatchObject({ kind: 'slack', name: 'Team app', token: 'xoxb-secret-bot xapp-secret-app', agent_id: 'a1', allow: ['U001', 'C001'] })
    expect(body).not.toHaveProperty('bot_token')
    // The host always reads the thread of a mention: no setting is offered or sent.
    expect(body).not.toHaveProperty('thread_context')
    await expect(page.locator('body')).not.toContainText('secret-bot')
  })

  test('a token the platform refuses is saved and the reason is shown', async ({ page }) => {
    await api(page).install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ status: 502, json: { error: { message: 'the platform refused the bot token' } } }))
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Team app')
    await page.getByLabel('Bot token').fill('xoxb-1')
    await page.getByLabel('App-level token').fill('xapp-2')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('alert')).toContainText('Saved. The token check failed: the platform refused the bot token')
    await expect(page.getByTestId('connector-row')).toHaveCount(1)
  })

  test('an edit keeps the stored tokens unless both are typed, and keeps both lists', async ({ page }) => {
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByLabel('Bot token')).toHaveValue('')
    await expect(page.getByLabel('App-level token')).toHaveAttribute('placeholder', /Saved/)
    await expect(page.getByTestId('connector-allow-channels')).toContainText('C001')
    await page.getByRole('button', { name: 'C001 ×' }).click()
    await page.getByRole('button', { name: 'Save' }).click()
    const put = a.s.calls.find(c => c.method === 'PUT')
    expect(put.url).toBe('/api/chats/connectors/b1')
    expect('token' in put.body).toBe(false)
    expect(put.body.allow).toEqual(['U001'])
  })

  // The host marks two enabled bindings of one bot with `duplicate`; the page shows it on both.
  const TWIN = { ...BINDING, id: 'b2', name: 'Team app copy' }
  const duplicate = (other) => ({ code: 'duplicate_connection', message: `This bot is also connected as ${other.name}. Both connections will answer the same message.`, others: [other] })
  async function twins(page, a) {
    const state = { items: [{ ...BINDING }, { ...TWIN }], disabled: [] }
    const view = () => state.items.map(c => {
      const other = state.items.find(o => o.id !== c.id)
      return c.enabled && other?.enabled ? { ...c, duplicate: duplicate({ id: other.id, name: other.name }) } : c
    })
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/disable$/, r => {
      const id = new URL(r.request().url()).pathname.split('/').at(-2)
      state.disabled.push(id)
      state.items = state.items.map(c => (c.id === id ? { ...c, enabled: false } : c))
      return r.fulfill({ json: state.items.find(c => c.id === id) })
    })
    await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
      if (r.request().method() !== 'GET') return r.fallback()
      return r.fulfill({ json: { connectors: view() } })
    })
    return state
  }

  test('warns on both connections of the same bot, opens the other and turns this one off', async ({ page }) => {
    const a = api(page)
    const state = await twins(page, a)
    await page.goto(PAGE)

    const rows = page.getByTestId('connector-row')
    await expect(rows).toHaveCount(2)
    const warnings = page.getByTestId('duplicate-warning')
    await expect(warnings).toHaveCount(2)
    await expect(warnings.first()).toContainText('This bot is also connected as Team app copy. Both connections will answer the same message.')
    await expect(warnings.last()).toContainText('This bot is also connected as Team app. Both connections will answer the same message.')

    // The link opens the other connection, and its own form shows the warning too.
    await rows.first().getByTestId('duplicate-open').click()
    const form = page.getByTestId('connector-form')
    await expect(form.getByLabel('Name', { exact: true })).toHaveValue('Team app copy')
    await expect(form.getByTestId('duplicate-warning')).toContainText('This bot is also connected as Team app.')
    await form.getByRole('button', { name: 'Cancel' }).click()

    // The page does not scroll sideways on a phone.
    await page.setViewportSize({ width: 360, height: 800 })
    await page.reload()
    await expect(warnings).toHaveCount(2)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)

    await rows.last().getByRole('button', { name: 'Turn this one off' }).click()
    await expect(page.getByTestId('duplicate-warning')).toHaveCount(0)
    expect(state.disabled).toEqual(['b2'])
    await expect(rows.last().getByRole('switch', { name: 'Team app copy on' })).toHaveAttribute('aria-checked', 'false')
  })

  test('asks before it saves a connection that is the same bot as another, and saves it when confirmed', async ({ page }) => {
    const a = api(page, [BINDING])
    await a.install()
    const bodies = []
    await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
      const req = r.request()
      if (req.method() !== 'POST') return r.fallback()
      const body = req.postDataJSON()
      bodies.push(body)
      if (body.confirm_duplicate !== 'answer twice') {
        return r.fulfill({ status: 409, json: { error: { type: 'duplicate_connection', code: 409, param: 'Team app', message: 'This bot is also connected as Team app. Both connections will answer the same message.' } } })
      }
      return r.fulfill({ status: 201, json: { ...TWIN, token: undefined, has_token: true } })
    })
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Team app copy')
    await page.getByLabel('Bot token').fill('xoxb-second')
    await page.getByLabel('App-level token').fill('xapp-second')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByRole('button', { name: 'Save' }).click()

    const ask = page.getByTestId('duplicate-confirm')
    await expect(ask).toContainText('This bot is also connected as Team app. Both connections will answer the same message.')
    await expect(page.getByTestId('connector-form')).toBeVisible()
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).not.toHaveProperty('confirm_duplicate')

    await ask.getByRole('button', { name: 'Go back' }).click()
    await expect(ask).toHaveCount(0)
    await page.getByRole('button', { name: 'Save' }).click()
    await page.getByTestId('duplicate-save-anyway').click()
    await expect(page.getByTestId('connector-form')).toHaveCount(0)
    expect(bodies.at(-1)).toMatchObject({ name: 'Team app copy', confirm_duplicate: 'answer twice' })
  })

  test('shows how to make the Slack app, with its manifest', async ({ page }) => {
    await api(page).install()
    await page.goto(PAGE)
    const setup = page.getByTestId('slack-setup')
    await setup.getByText('How to make the Slack app').click()
    await expect(setup).toContainText('connections:write')
    await expect(setup.getByLabel('Slack app manifest')).toContainText('socket_mode_enabled: true')
  })

  test('tests the tokens, shows the sender log and allows a sender', async ({ page }) => {
    const a = api(page, [BINDING])
    await a.install()
    await page.route(/\/api\/chats\/connectors\/[^/?]+\/test$/, r => r.fulfill({ json: { bot_id: 'UBOT', bot_username: 'localai (Acme)' } }))
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Test' }).click()
    await expect(page.getByText('Connected to localai (Acme).')).toBeVisible()
    await page.getByRole('button', { name: 'Senders' }).click()
    await expect(page.getByTestId('sender-log')).toContainText('Newcomer · 2 messages · U042')
    await page.getByRole('button', { name: 'Allow', exact: true }).click()
    await expect(page.getByTestId('sender-log')).toContainText('Accepted')
    expect(a.s.calls.some(c => c.url === '/api/chats/connectors/b1/senders/U042/allow')).toBe(true)
  })

  test('a token the server refuses on save keeps the form open with the reason', async ({ page }) => {
    const a = api(page)
    await a.install()
    await page.route(/\/api\/chats\/connectors(\?.*)?$/, r => {
      if (r.request().method() !== 'POST') return r.fallback()
      return r.fulfill({ status: 400, json: { error: { message: 'the platform refused the bot token' } } })
    })
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Slack app' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Team app')
    await page.getByLabel('Bot token').fill('xoxb-1')
    await page.getByLabel('App-level token').fill('xapp-2')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByTestId('connector-reply').selectOption('progress')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('alert')).toContainText('the platform refused the bot token')
    await expect(page.getByRole('form')).toBeVisible()
    await expect(page.getByTestId('connector-row')).toHaveCount(0)
    expect(a.s.items).toHaveLength(0)
  })

  test('a list of channels only is named in the readiness list, and a person added fixes it', async ({ page }) => {
    const a = api(page, [{ ...BINDING, allow: ['C001'], caps: ['memory.write'] }])
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'Edit' }).click()
    const who = page.getByTestId('connector-ready-who')
    await expect(who).toContainText('Only channels are on the list')
    await expect(page.getByTestId('connector-can-sender').locator('option')).toContainText(['A person on the allowed list', 'A member of an allowed channel'])
    await who.getByRole('button', { name: 'Add a person' }).click()
    await page.getByTestId('connector-allow').getByRole('textbox').fill('U001')
    await page.getByTestId('connector-allow').getByRole('textbox').press('Enter')
    await expect(who).toHaveAttribute('data-state', 'after_save')
    await expect.poll(() => [...(a.s.previews.at(-1)?.body.allow || [])].sort()).toEqual(['C001', 'U001'])
  })
})
