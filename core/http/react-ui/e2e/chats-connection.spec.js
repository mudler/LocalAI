import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installAgents, DRAFT_TOOLS } from './agents-fixtures.js'

// The connections a chat may use for an add-on that runs with a credential.
// The host never picks them for the person: with one connection it is the set,
// with several the person ticks the ones this chat may use in the Tools menu,
// with none the row says so, and an agent that names a set decides it. The
// add-ons here are stand-ins: nothing depends on which product they are. The
// API is stubbed.

const SHOTS = process.env.CONNECTION_SHOT_DIR
// A screenshot for review waits for the menu's opening motion to end.
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(400); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

const WORK = { id: 'b1', name: 'Work', handle: 'work' }
const HOME = { id: 'b2', name: 'Home', handle: 'home' }
const PLAY = { id: 'b3', name: 'Play', handle: 'play' }

async function stubChat(page, makeRow) {
  const detail = {
    id: 'c1', title: 'Issues', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
    running: false, last_turn: 'done', pending: [],
  }
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({
    json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'list the open issues', at: '2026-10-25T10:00:00Z' }] },
  }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' },
  }))
  const state = { picked: [], row: makeRow(null) }
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [state.row], commands: 'none' } }))
  await page.route('**/api/chats/c1/connections', route => {
    const body = route.request().postDataJSON()
    state.picked.push(body)
    state.row = makeRow(body.connections)
    return route.fulfill({ json: { addons: [state.row], commands: 'none' } })
  })
  return state
}

const row = (extra) => ({ id: 'tracker', tools: 14, off: false, title: 'Tracker', description: 'Read and change items.', credentialed: true, ...extra })
const openTools = async (page) => {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
}

test.describe('The connections of an add-on in the Tools menu', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('several connections: none is enabled for the person, the tools wait, and ticking switches the row on', async ({ page }) => {
    const state = await stubChat(page, (picked) => row({
      connections: [WORK, HOME, PLAY], enabled_connections: picked || [], connection_by: picked?.length ? 'chat' : '', needs_connection: !picked?.length,
    }))
    await openTools(page)
    const item = page.getByTestId('chat-tools-item')
    await expect(item).toContainText('Choose at least one connection to use these tools')
    await expect(item).toHaveAttribute('data-on', 'false')
    const checks = page.getByTestId('chat-tools-pick-check')
    await expect(checks).toHaveCount(3)
    for (const c of await checks.all()) await expect(c).not.toBeChecked()
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('Choose the connections this chat may use')
    await expect(page.getByRole('group', { name: 'Connections' })).toBeVisible()
    await shot(page, 'choose')
    await page.getByLabel('Home').check()
    await expect.poll(() => state.picked).toEqual([{ addon: 'tracker', connections: ['b2'] }])
    await expect(item).toHaveAttribute('data-on', 'true')
    await expect(item).not.toContainText('Choose at least one connection')
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('Choose the connections this chat may use')
    await page.getByLabel('Work').check()
    await expect.poll(() => state.picked.at(-1)).toEqual({ addon: 'tracker', connections: ['b1', 'b2'] })
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('names the connection in each action')
    await shot(page, 'chosen')
    await page.getByLabel('Home').uncheck()
    await expect.poll(() => state.picked.at(-1)).toEqual({ addon: 'tracker', connections: ['b1'] })
  })

  test('one connection is enabled already and cannot be unticked', async ({ page }) => {
    await stubChat(page, () => row({ connections: [WORK], enabled_connections: ['b1'], connection_by: 'only', needs_connection: false }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toHaveAttribute('data-on', 'true')
    const check = page.getByTestId('chat-tools-pick-check')
    await expect(check).toBeChecked()
    await expect(check).toBeDisabled()
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('your only connection')
  })

  test('no connection yet: the row says so and links to the Connectors page', async ({ page }) => {
    await stubChat(page, () => row({ connections: [], needs_connection: true }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toContainText('No connection yet')
    await expect(page.getByTestId('chat-tools-pick-check')).toHaveCount(0)
    await expect(page.getByTestId('chat-tools-pick-link')).toHaveAttribute('href', '/app/chats/connectors')
  })

  test('the set an agent names is shown and cannot be changed here', async ({ page }) => {
    await stubChat(page, () => row({ connections: [WORK, HOME, PLAY], enabled_connections: ['b1', 'b2'], connection_by: 'agent', needs_connection: false }))
    await openTools(page)
    const checks = page.getByTestId('chat-tools-pick-check')
    await expect(checks.nth(0)).toBeChecked()
    await expect(checks.nth(1)).toBeChecked()
    await expect(checks.nth(2)).not.toBeChecked()
    for (const c of await checks.all()) await expect(c).toBeDisabled()
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('The agent of this chat sets the connections')
  })

  test('two add-ons with a credential keep a set each', async ({ page }) => {
    await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [
      row({ connections: [WORK, HOME], enabled_connections: ['b1', 'b2'], connection_by: 'chat' }),
      row({ id: 'ledger', title: 'Ledger', connections: [{ id: 'l1', name: 'Books', handle: 'books' }], enabled_connections: ['l1'], connection_by: 'only' }),
    ], commands: 'none' } }))
    await page.route('**/api/chats/c1', route => route.fulfill({ json: { id: 'c1', title: 'Issues', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: 'done', pending: [] } }))
    await page.route('**/api/chats/c1/events', route => route.abort())
    await openTools(page)
    await expect(page.getByTestId('chat-tools-pick')).toHaveCount(2)
    await expect(page.getByTestId('chat-tools-pick-check')).toHaveCount(3)
    await expect(page.getByLabel('Books')).toBeDisabled()
    await expect(page.getByLabel('Work')).toBeEnabled()
  })

  test('an add-on with no credential has no connection control', async ({ page }) => {
    await stubChat(page, () => ({ id: 'office', tools: 3, off: false, title: 'Office' }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toBeVisible()
    await expect(page.getByTestId('chat-tools-pick')).toHaveCount(0)
  })
})

test.describe('The connections an agent names', () => {
  test('the add-on row names a set, ticking switches it on, and none ticked says what happens in a chat and in a run', async ({ page }) => {
    const s = await installAgents(page)
    // Later routes win: the draft list now has an add-on with a credential.
    await page.route('**/api/chats/draft/tools*', route => route.fulfill({
      json: {
        ...DRAFT_TOOLS, servers: [], off: [], connectors: [],
        addons: [{ id: 'tracker', tools: 14, off: false, title: 'Tracker', credentialed: true, connections: [WORK, HOME, PLAY], enabled_connections: [], needs_connection: true }],
      },
    }))
    await page.goto('/app/agents/a2/edit')
    await page.getByLabel('Name', { exact: false }).first().fill('Reviewer')
    // The account sits in the row of the add-on (it was a Connections card).
    const row = page.getByTestId('agent-addon-box').filter({ hasText: 'Tracker' })
    const group = row.getByRole('group', { name: 'Accounts for Tracker' })
    await expect(group).toBeVisible()
    // Off, nothing ticked: nothing is offered, and the sentence says it is off.
    await expect(row.getByTestId('agent-addon-now')).toContainText('Off')
    await expect(row.getByRole('switch', { name: 'Tracker' })).not.toBeChecked()
    await group.getByLabel('Home').check()
    await group.getByLabel('Play').check()
    // Ticking an account switched the add-on on.
    await expect(row.getByRole('switch', { name: 'Tracker' })).toBeChecked()
    await expect(row.getByTestId('agent-addon-now')).toContainText('using Home, Play')
    await expect(row.getByTestId('agent-addon-now')).toHaveAttribute('data-tone', 'ok')
    // Unticking all accounts leaves the add-on on with two accounts and none chosen: amber.
    await group.getByLabel('Home').uncheck()
    await group.getByLabel('Play').uncheck()
    await expect(row.getByTestId('agent-addon-now')).toHaveAttribute('data-tone', 'warn')
    await expect(row.getByTestId('agent-addon-now')).toContainText('a run with nobody there gets none')
    await group.getByLabel('Home').check()
    await group.getByLabel('Play').check()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'PUT').body
    expect(body.connections).toEqual({ tracker: ['b2', 'b3'] })
    expect(body.addons).toEqual(['tracker'])
  })
})
