import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { mockUser } from './home-fixtures.js'

// A chat that cannot run commands says so: a notice in the Tools menu and a
// chip in the composer's tools area, from the "commands" field of the chat's
// add-on list. The API is stubbed.

const SHOTS = process.env.COMMANDS_SHOT_DIR
// A screenshot for review waits for the menu's opening motion to end.
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(400); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

async function stubChat(page, addons) {
  const detail = {
    id: 'c1', title: 'Deck', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
    running: false, last_turn: 'done', pending: [],
  }
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({
    json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'build the Excel deck', at: '2026-10-25T10:00:00Z' }] },
  }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' },
  }))
  const calls = []
  await page.route('**/api/chats/c1/addons', route => {
    if (route.request().method() === 'PUT') {
      const off = route.request().postDataJSON().off
      calls.push(off)
      return route.fulfill({ json: addons(off) })
    }
    return route.fulfill({ json: addons(null) })
  })
  return calls
}

const none = () => ({ addons: [], commands: 'none' })

test.describe('Chat that cannot run commands', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('an administrator sees the chip and a notice in the Tools menu with the way to install the Sandbox', async ({ page }) => {
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-commands-chip')
    await expect(chip).toHaveText("Commands can't run here")
    // The Tools menu shows for the notice even though there is nothing to switch.
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText("Commands can't run here")
    await expect(notice).toContainText('No add-on that runs code is installed, so the assistant can write files but cannot run scripts or commands.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveText('Install the Sandbox')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveAttribute('href', '/app/addons?tab=browse&q=sandbox')
    await expect(notice).not.toContainText('Ask an administrator')
    await shot(page, 'tools-menu-admin')
  })

  test('the chip opens the same notice from the keyboard', async ({ page }) => {
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-commands-chip')
    await chip.focus()
    await page.keyboard.press('Enter')
    await expect(chip).toHaveAttribute('aria-expanded', 'true')
    const dialog = page.getByRole('dialog', { name: "Commands can't run here" })
    await expect(dialog.getByTestId('chat-commands-pop')).toContainText('No add-on that runs code is installed')
    await shot(page, 'chip-popover')
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(chip).toBeFocused()
  })

  test('a person who is not an administrator is told to ask one, with no install link', async ({ page }) => {
    await mockUser(page, { chats: true })
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('Ask an administrator to install the Sandbox add-on.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
    await shot(page, 'tools-menu-user')
  })

  test('hiding the notice hides the chip and leaves one line in the Tools menu, also after a reload', async ({ page }) => {
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-commands-notice-hide').click()
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-notice')).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-notice-line')).toHaveText("Commands can't run here")
    await page.reload()
    await expect(page.getByTestId('chat-tools-button')).toBeVisible()
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-commands-notice-line')).toBeVisible()
    await shot(page, 'tools-menu-hidden')
  })

  test('a Sandbox that is off for the chat points at its switch, and switching it on removes the notice', async ({ page }) => {
    const calls = await stubChat(page, (off) => {
      const isOff = off === null ? true : off.includes('sandbox')
      return { addons: [{ id: 'sandbox', tools: 7, off: isOff }], commands: isOff ? 'off' : 'available' }
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('The Sandbox add-on is off for this chat. Switch it on in the Tools menu to let the assistant run code.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
    await page.getByRole('checkbox', { name: 'sandbox' }).check()
    await expect.poll(() => calls.at(-1)).toEqual([])
    await expect(notice).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
  })

  test('a Sandbox that cannot run here gives the reason and the add-on page', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 1, off: false }], commands: 'unavailable', commands_reason: 'no sandbox backend works on this host, so commands cannot run' }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('The Sandbox add-on cannot run commands on this server: no sandbox backend works on this host, so commands cannot run')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveAttribute('href', '/app/addons?detail=sandbox')
  })

  test('a chat that runs commands shows neither the chip nor the notice', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 7, off: false }], commands: 'available' }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('checkbox', { name: 'sandbox' })).toBeChecked()
    await expect(page.getByTestId('chat-commands-notice')).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
  })

  test('on a phone the chip and its notice stay on the screen', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 740 })
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-commands-chip')
    await expect(chip).toBeVisible()
    // The words are hidden on a phone; the button keeps its name.
    await expect(chip).toHaveAccessibleName("Commands can't run here")
    const box = await chip.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(375)
    await chip.click()
    const pop = page.getByTestId('chat-commands-pop')
    await expect(pop).toBeVisible()
    const p = await pop.boundingBox()
    expect(p.x).toBeGreaterThanOrEqual(0)
    expect(p.x + p.width).toBeLessThanOrEqual(375)
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(width).toBeLessThanOrEqual(375)
    await shot(page, 'phone-popover')
  })
})
