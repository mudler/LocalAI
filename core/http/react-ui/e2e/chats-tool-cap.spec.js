import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { mockUser, setTheme } from './home-fixtures.js'

// A chat says when the limit on add-on tools leaves tools out: a notice in the
// Tools menu from tool_cap of the tools list, naming the add-ons that lost
// tools. Switching an add-on off frees room, and the notice goes away. The API
// is stubbed.

const SHOTS = process.env.TOOLCAP_SHOT_DIR
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(400); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

const CAP = {
  limit: 64, offered: 64, total: 80, cut: 16,
  addons: [{ id: 'notes', title: 'Notes', cut: 11 }, { id: 'crm', cut: 5 }],
}
const rows = (off = []) => [
  { id: 'crm', tools: 12, off: off.includes('crm'), title: 'CRM' },
  { id: 'notes', tools: 32, off: off.includes('notes'), title: 'Notes' },
  { id: 'sandbox', tools: 20, off: off.includes('sandbox'), title: 'Sandbox' },
]

async function stubChat(page, { cap = CAP, freedBy = 'sandbox' } = {}) {
  const detail = {
    id: 'c1', title: 'Deck', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
    running: false, last_turn: 'done', pending: [],
  }
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' },
  }))
  let off = []
  await page.route('**/api/chats/c1/addons', route => {
    if (route.request().method() === 'PUT') off = route.request().postDataJSON().off
    return route.fulfill({ json: { addons: rows(off), commands: 'available', commands_provider: 'sandbox', execution_status: { readiness: 'ready', isolation: 'isolated' } } })
  })
  // The cap is gone once the add-on that frees room is off.
  await page.route('**/api/chats/c1/tools', route => route.fulfill({
    json: { builtins: [], servers: [], connectors: [], off: [], addons: rows(off), ...(cap && !off.includes(freedBy) ? { tool_cap: cap } : {}) },
  }))
}

test.describe('Chat with add-on tools left out by the limit', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page); await setTheme(page, 'light') })

  test('an administrator is told which add-ons lost tools and where to raise the limit', async ({ page }) => {
    await stubChat(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-tool-cap')
    await expect(notice).toContainText('Some add-on tools are not available in this chat')
    await expect(notice).toContainText('64 of 80 add-on tools are available.')
    await expect(page.getByTestId('chat-tool-cap-addon-notes')).toHaveText('Notes: 11 not available')
    await expect(page.getByTestId('chat-tool-cap-addon-crm')).toHaveText('crm: 5 not available')
    await expect(notice).toContainText('raise the limit in Settings')
    await expect(page.getByTestId('chat-tool-cap-link')).toHaveAttribute('href', /\/app\/settings\?group=tools#settings-field-chats_addon_tool_limit$/)
    await shot(page, 'tool-cap-admin')
  })

  test('a person who is not an administrator is told to ask one, with no link', async ({ page }) => {
    await mockUser(page, { chats: true })
    await stubChat(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-tool-cap')
    await expect(notice).toContainText('ask an administrator to raise the limit')
    await expect(page.getByTestId('chat-tool-cap-link')).toHaveCount(0)
    await shot(page, 'tool-cap-user')
  })

  test('switching an add-on off frees room and the notice goes away', async ({ page }) => {
    await stubChat(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-tool-cap')).toBeVisible()
    await page.getByRole('switch', { name: /Sandbox/ }).click()
    await expect(page.getByTestId('chat-tool-cap')).toHaveCount(0)
  })

  test('nothing is shown when the limit cuts nothing', async ({ page }) => {
    await stubChat(page, { cap: null })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-tool-cap')).toHaveCount(0)
  })
})
