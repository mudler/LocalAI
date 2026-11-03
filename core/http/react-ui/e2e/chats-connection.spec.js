import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installAgents, DRAFT_TOOLS } from './agents-fixtures.js'

// The connection a chat uses for an add-on that runs with a credential. The
// host never picks it for the person: with one connection it is selected, with
// several the person chooses in the Tools menu, with none the row says so, and
// an agent that names a connection decides it. The API is stubbed.

const SHOTS = process.env.CONNECTION_SHOT_DIR
// A screenshot for review waits for the menu's opening motion to end.
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(400); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

const WORK = { id: 'b1', name: 'Work' }
const HOME = { id: 'b2', name: 'Home' }

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
    state.row = makeRow(body.connection)
    return route.fulfill({ json: { addons: [state.row], commands: 'none' } })
  })
  return state
}

const row = (extra) => ({ id: 'github', tools: 14, off: false, title: 'GitHub', description: 'Read repositories.', credentialed: true, ...extra })
const openTools = async (page) => {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
}

test.describe('The connection of an add-on in the Tools menu', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('several connections: none is chosen for the person, the tools wait, and choosing switches the row on', async ({ page }) => {
    const state = await stubChat(page, (picked) => row({
      connections: [WORK, HOME], connection: picked || '', connection_by: picked ? 'chat' : '', needs_connection: !picked,
    }))
    await openTools(page)
    const item = page.getByTestId('chat-tools-item')
    await expect(item).toContainText('Choose a connection to use these tools')
    await expect(item).toHaveAttribute('data-on', 'false')
    const select = page.getByTestId('chat-tools-pick-select')
    await expect(select).toHaveValue('')
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('The model cannot pick or change it')
    await expect(page.getByLabel('Connection')).toBeVisible()
    await shot(page, 'choose')
    await select.selectOption('b2')
    await expect.poll(() => state.picked).toEqual([{ addon: 'github', connection: 'b2' }])
    await expect(item).toHaveAttribute('data-on', 'true')
    await expect(item).not.toContainText('Choose a connection to use these tools')
    await expect(select).toHaveValue('b2')
    await shot(page, 'chosen')
  })

  test('one connection is selected already', async ({ page }) => {
    await stubChat(page, () => row({ connections: [WORK], connection: 'b1', connection_by: 'only', needs_connection: false }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toHaveAttribute('data-on', 'true')
    await expect(page.getByTestId('chat-tools-pick-select')).toHaveValue('b1')
  })

  test('no connection yet: the row says so and links to the Connectors page', async ({ page }) => {
    await stubChat(page, () => row({ connections: [], needs_connection: true }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toContainText('No connection yet')
    await expect(page.getByTestId('chat-tools-pick-select')).toHaveCount(0)
    await expect(page.getByTestId('chat-tools-pick-link')).toHaveAttribute('href', '/app/chats/connectors')
  })

  test('the connection an agent names is shown and cannot be changed here', async ({ page }) => {
    await stubChat(page, () => row({ connections: [WORK, HOME], connection: 'b1', connection_by: 'agent', needs_connection: false }))
    await openTools(page)
    const select = page.getByTestId('chat-tools-pick-select')
    await expect(select).toHaveValue('b1')
    await expect(select).toBeDisabled()
    await expect(page.getByTestId('chat-tools-pick-hint')).toContainText('The agent of this chat sets the connection')
  })

  test('an add-on with no credential has no connection control', async ({ page }) => {
    await stubChat(page, () => ({ id: 'office', tools: 3, off: false, title: 'Office' }))
    await openTools(page)
    await expect(page.getByTestId('chat-tools-item')).toBeVisible()
    await expect(page.getByTestId('chat-tools-pick')).toHaveCount(0)
  })
})

test.describe('The connection an agent names', () => {
  test('the agent form names a connection per add-on, and a write that clears it sends an empty object', async ({ page }) => {
    const s = await installAgents(page)
    // Later routes win: the draft list now has an add-on with a credential.
    await page.route('**/api/chats/draft/tools*', route => route.fulfill({
      json: {
        ...DRAFT_TOOLS, servers: [], off: [], connectors: [],
        addons: [{ id: 'github', tools: 14, off: false, title: 'GitHub', credentialed: true, connections: [WORK, HOME], connection: '', needs_connection: true }],
      },
    }))
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Reviewer')
    await page.locator('[data-section="Tools"] .ag-fold__button').click()
    const panel = page.getByTestId('agent-tools-panel')
    const select = panel.getByLabel('Connection for chats of this agent')
    await expect(select).toHaveValue('')
    await expect(panel.getByTestId('agent-tools-pick-hint')).toContainText('cannot change it')
    await select.selectOption('b2')
    await panel.getByRole('switch', { name: /GitHub/ }).click()
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'POST').body
    expect(body.connections).toEqual({ github: 'b2' })
    expect(body.addons).toEqual(['github'])
  })
})
