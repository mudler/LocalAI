import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, answerWith, CHATS_KEY } from './chat-fixtures.js'
import { MEMBER, app, sessionFor, mockAddons } from './addons-fixtures.js'

// The add-ons control in the chat composer, next to the MCP control, lists the add-ons that ship tools,
// one switch each, and shows how many are on. The /addons command of the
// composer flips all of them and stays in step with it.

const TOOL = { name: 'run', kind: 'read', description: 'x', inputSchema: { type: 'object' } }
const NOTES = app({ id: 'notes', title: 'Notes', toolDefinitions: [TOOL] })
const INDEXER = app({ id: 'indexer', title: 'Indexer', icon: undefined, toolDefinitions: [TOOL] })
// Running, but its manifest declares no tool: never listed.
const VIEWER = app({ id: 'viewer', title: 'Viewer', toolDefinitions: [] })
// Has tools but is stopped: not offered.
const STOPPED = app({ id: 'idle', title: 'Idle', enabled: false, state: 'stopped', toolDefinitions: [TOOL] })

const button = (page) => page.getByTestId('chat-addons-button')
const items = (page) => page.getByTestId('chat-addon-item')
const input = (page) => page.getByTestId('chat-input')

// The signed-in person's chats live under a key of their own.
const KEY = `${CHATS_KEY}:${MEMBER.id}`

async function openChat(page, chats) {
  await page.addInitScript(([key, data]) => {
    if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(data))
  }, [KEY, { chats, activeChatId: chats[0].id, lastSaved: Date.now() }])
  await page.goto('/app/chat')
  await page.getByTestId('chat-composer').waitFor()
}

const storedChats = (page) => page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '{}'), KEY)

async function setup(page, apps, user = MEMBER) {
  await mockChat(page)
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, { addons: true }) }))
  await mockAddons(page, { apps })
}

test.describe('Chat add-ons control', () => {
  test('lists only the add-ons that ship tools, with name and icon, and nothing else', async ({ page }) => {
    await setup(page, [NOTES, INDEXER, VIEWER, STOPPED])
    await openChat(page, [chatOf('c1', 'Add-ons', 'qwen3-8b', pair('hi', 'hello'))])
    await expect(button(page)).toBeVisible()
    await expect(page.getByTestId('chat-addons-count')).toHaveCount(0)
    await button(page).click()
    await expect(page.getByTestId('chat-addons-menu')).toBeVisible()
    await expect(items(page)).toHaveCount(2)
    await expect(items(page).nth(0)).toContainText('Notes')
    await expect(items(page).nth(1)).toContainText('Indexer')
    await expect(items(page).nth(0).locator('img.ad-icon')).toBeVisible()
    await expect(items(page).nth(1).locator('.ad-icon--empty')).toBeVisible()
    await expect(page.getByTestId('chat-addons-menu')).not.toContainText('Viewer')
    await expect(page.getByTestId('chat-addons-menu')).not.toContainText('Idle')
    // The menu closes on Escape.
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-addons-menu')).toHaveCount(0)
  })

  test('sits right after the MCP control in the composer, not in the chat header', async ({ page }) => {
    await setup(page, [NOTES])
    await openChat(page, [chatOf('c1', 'Place', 'qwen3-8b', pair('hi', 'hello'))])
    await expect(button(page)).toBeVisible()
    await expect(page.getByTestId('chat-header').getByTestId('chat-addons')).toHaveCount(0)
    const beside = await page.getByTestId('chat-addons').evaluate(el => {
      const before = el.previousElementSibling
      return Boolean(before && before.classList.contains('chat-mcp-dropdown') && before.textContent.includes('MCP') && el.parentElement === before.parentElement)
    })
    expect(beside).toBe(true)
  })

  test('says in one line that an add-on that is off does nothing in this chat', async ({ page }) => {
    await setup(page, [NOTES, INDEXER, VIEWER])
    await openChat(page, [chatOf('c1', 'Line', 'qwen3-8b', pair('hi', 'hello'))])
    await button(page).click()
    const menu = page.getByTestId('chat-addons-menu')
    const note = menu.getByTestId('chat-addons-note')
    await expect(note).toHaveText('An add-on that is off here does nothing in this chat.')
    // One line: a single text node paragraph, tied to the control by its description.
    await expect(button(page)).toHaveAttribute('aria-describedby', await note.getAttribute('id'))
    // Only the add-ons that ship tools are listed under it.
    await expect(items(page)).toHaveCount(2)
    await expect(menu).not.toContainText('Viewer')
  })

  test('shows nothing when no add-on ships tools', async ({ page }) => {
    await setup(page, [VIEWER, STOPPED])
    await openChat(page, [chatOf('c1', 'None', 'qwen3-8b', pair('hi', 'hello'))])
    await expect(page.getByTestId('chat-header')).toBeVisible()
    await expect(page.getByTestId('chat-addons')).toHaveCount(0)
    await input(page).fill('/')
    await expect(page.getByRole('option', { name: /\/addons/ })).toHaveCount(0)
  })

  test('shows nothing without the add-ons feature', async ({ page }) => {
    await mockChat(page)
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: false }) }))
    await mockAddons(page, { apps: [NOTES] })
    await openChat(page, [chatOf('c1', 'None', 'qwen3-8b', pair('hi', 'hello'))])
    await expect(page.getByTestId('chat-addons')).toHaveCount(0)
  })

  test('a switch turns one add-on on for this chat, counts it, and stores the list on the chat', async ({ page }) => {
    await setup(page, [NOTES, INDEXER])
    await openChat(page, [chatOf('c1', 'One', 'qwen3-8b', pair('hi', 'hello')), chatOf('c2', 'Two', 'qwen3-8b', pair('a', 'b'), {}, Date.now() - 1000)])
    await button(page).click()
    await items(page).nth(0).getByRole('checkbox').check()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('1')
    await expect(items(page).nth(0).getByRole('checkbox')).toBeChecked()
    await expect(items(page).nth(1).getByRole('checkbox')).not.toBeChecked()
    await items(page).nth(1).getByRole('checkbox').check()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('2')
    await items(page).nth(0).getByRole('checkbox').uncheck()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('1')
    await expect.poll(async () => (await storedChats(page)).chats?.find(c => c.id === 'c1')?.addonToolIds).toEqual(['indexer'])
    // The other chat keeps its own selection.
    const stored = await storedChats(page)
    expect(stored.chats.find(c => c.id === 'c2').addonToolIds).toBeUndefined()
  })

  test('the select-all link flips every add-on', async ({ page }) => {
    await setup(page, [NOTES, INDEXER])
    await openChat(page, [chatOf('c1', 'All', 'qwen3-8b', pair('hi', 'hello'))])
    await button(page).click()
    await page.getByTestId('chat-addons-all').click()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('2')
    await expect(page.getByTestId('chat-addons-all')).toHaveText('Deselect all')
    await page.getByTestId('chat-addons-all').click()
    await expect(page.getByTestId('chat-addons-count')).toHaveCount(0)
  })

  test('/addons flips all add-ons and the composer control shows it, in step with the switches', async ({ page }) => {
    await setup(page, [NOTES, INDEXER])
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await input(page).fill('/addons')
    await input(page).press('Enter')
    await expect(page.getByText('Add-on tools on for this chat')).toBeVisible()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('2')
    await button(page).click()
    await items(page).nth(0).getByRole('checkbox').uncheck()
    await expect(page.getByTestId('chat-addons-count')).toHaveText('1')
    // With some on, /addons turns them all off.
    await input(page).fill('/addons')
    await input(page).press('Enter')
    await expect(page.getByText('Add-on tools off for this chat')).toBeVisible()
    await expect(page.getByTestId('chat-addons-count')).toHaveCount(0)
    await expect(items(page).nth(1).getByRole('checkbox')).not.toBeChecked()
  })

  test('the request sends the boolean when every add-on is on, the list when some are, nothing when none', async ({ page }) => {
    await setup(page, [NOTES, INDEXER])
    const sent = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Send', 'qwen3-8b', pair('hi', 'hello'))])
    const send = async (text) => {
      await input(page).fill(text)
      await input(page).press('Enter')
      await expect(page.getByText('ok').last()).toBeVisible()
    }

    await send('none on')
    expect(sent.request().metadata?.addon_tools).toBeUndefined()
    expect(sent.request().metadata?.addon_tool_ids).toBeUndefined()

    await button(page).click()
    await items(page).nth(1).getByRole('checkbox').check()
    await send('some on')
    expect(sent.request().metadata).toMatchObject({ addon_tools: 'true', addon_tool_ids: 'indexer' })

    await items(page).nth(0).getByRole('checkbox').check()
    await send('all on')
    expect(sent.request().metadata.addon_tools).toBe('true')
    expect(sent.request().metadata.addon_tool_ids).toBeUndefined()
  })

  test('a chat saved with the older boolean means every add-on', async ({ page }) => {
    await setup(page, [NOTES, INDEXER])
    const sent = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Old', 'qwen3-8b', pair('hi', 'hello'), { addonTools: true })])
    await expect(page.getByTestId('chat-addons-count')).toHaveText('2')
    await input(page).fill('hello')
    await input(page).press('Enter')
    await expect(page.getByText('ok').last()).toBeVisible()
    expect(sent.request().metadata).toMatchObject({ addon_tools: 'true' })
    expect(sent.request().metadata.addon_tool_ids).toBeUndefined()
  })
})
