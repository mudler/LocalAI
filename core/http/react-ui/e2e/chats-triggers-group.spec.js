import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The Triggers group of the chat Tools menu: one switch for the four trigger
// tools (propose, list, update, delete). The API is stubbed, so the request a
// switch sends can be read.

const chat = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [] }
const TRIGGER_TOOLS = ['propose_trigger', 'list_triggers', 'update_trigger', 'delete_trigger']

async function stub(page, { triggers = {} } = {}) {
  const state = {
    builtins: [
      { group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin', providers: { web_search: 'builtin', web_fetch: 'builtin' } },
      { group: 'triggers', tools: TRIGGER_TOOLS, off: false, usable: true, provider: 'builtin', providers: Object.fromEntries(TRIGGER_TOOLS.map(t => [t, 'builtin'])), ...triggers },
    ],
    puts: [],
  }
  const tools = () => ({ addons: [], builtins: state.builtins, off: [], servers: [], connectors: [] })
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route(/\/api\/chats\/c1(\?.*)?$/, route => route.fulfill({ json: chat }))
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
  await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: tools() }))
  await page.route('**/api/chats/c1/builtins', route => {
    const body = route.request().postDataJSON()
    state.puts.push(body)
    state.builtins = state.builtins.map(b => ({ ...b, off: (body.off || []).includes(b.group) }))
    return route.fulfill({ json: tools() })
  })
  return state
}

const sw = (page, name) => page.getByRole('switch', { name, exact: true })
const open = async (page) => {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
  await expect(page.getByTestId('chat-tools-menu')).toBeVisible()
}

test.describe('Chat tools menu: the Triggers group', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('shows a Triggers row with a switch that is on by default', async ({ page }) => {
    await stub(page)
    await open(page)
    const row = page.getByTestId('chat-tools-item').filter({ has: sw(page, 'Triggers') })
    await expect(row).toHaveCount(1)
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'true')
    await expect(sw(page, 'Triggers')).toBeEnabled()
  })

  test('switching it off sends the group in the off list, and on again clears it', async ({ page }) => {
    const state = await stub(page)
    await open(page)
    await sw(page, 'Triggers').click()
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'false')
    await expect.poll(() => state.puts).toEqual([{ off: ['triggers'] }])
    await sw(page, 'Triggers').click()
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'true')
    await expect.poll(() => state.puts).toEqual([{ off: ['triggers'] }, { off: [] }])
  })

  test('Disable all and Select all switch the group with the others', async ({ page }) => {
    const state = await stub(page)
    await open(page)
    await page.getByTestId('chat-tools-none').click()
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'false')
    await expect(sw(page, 'Web')).toHaveAttribute('aria-checked', 'false')
    await expect.poll(() => state.puts).toEqual([{ off: ['triggers', 'web'] }])
    await page.getByTestId('chat-tools-all').click()
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'true')
    await expect.poll(() => state.puts.at(-1)).toEqual({ off: [] })
  })

  test('a chat that cannot use the group shows the row disabled, with the reason', async ({ page }) => {
    const reason = 'a run that started by itself cannot set triggers'
    await stub(page, { triggers: { usable: false, reason } })
    await open(page)
    await expect(sw(page, 'Triggers')).toBeDisabled()
    await expect(sw(page, 'Triggers')).toHaveAccessibleDescription(reason)
    await expect(sw(page, 'Triggers')).toHaveAttribute('aria-checked', 'false')
  })
})
