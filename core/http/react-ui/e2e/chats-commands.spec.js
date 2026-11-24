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

// No add-on runs commands and the server's built-in bash is off.
const none = () => ({ addons: [], commands: 'none', commands_host_exec: 'off' })
// An older server does not say the mode of the built-in bash.
const noneUnsaid = () => ({ addons: [], commands: 'none' })
const REASON = "The server's built-in bash is off, so the assistant can write files but cannot run scripts or commands."


test.describe('Chat that cannot run commands', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('an administrator sees command availability without promoting a provider', async ({ page }) => {
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-commands-chip')
    await expect(chip).toHaveText("Commands can't run here")
    // The Tools menu shows for the notice even though there is nothing to switch.
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText("Commands can't run here")
    await expect(notice).toContainText(REASON)
    // The administrator is told where to turn it on, and that Ask each time is the safer choice.
    await expect(notice).toContainText('You are an administrator. In Settings, Tools, turn on the Commands (bash) row. Ask each time is the safer choice.')
    await expect(notice).toContainText('without isolation')
    await expect(notice).not.toContainText('Ask an administrator')
    const link = notice.getByTestId('chat-commands-notice-link')
    await expect(link).toHaveText('Open the setting')
    await expect(link).toHaveAttribute('href', /\/app\/settings\?group=tools#settings-tools-row-bash$/)
    await expect(notice.getByTestId('chat-commands-notice-sandbox')).toHaveAttribute('href', /\/app\/addons$/)
    await expect(notice).toHaveAttribute('role', 'status')
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
    await expect(dialog.getByTestId('chat-commands-pop')).toContainText("The server's built-in bash is off")
    await shot(page, 'chip-popover')
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(chip).toBeFocused()
  })

  test('a non-administrator sees command availability without an install link', async ({ page }) => {
    await mockUser(page, { chats: true })
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText(REASON)
    await expect(notice).toContainText('Ask an administrator of this server to turn it on.')
    await expect(notice).not.toContainText('Open the setting')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
    await expect(notice.getByTestId('chat-commands-notice-sandbox')).toHaveCount(0)
    await shot(page, 'tools-menu-user')
  })

  test('a server that does not report the mode of its built-in bash keeps the plain notice', async ({ page }) => {
    await stubChat(page, noneUnsaid)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('No add-on that runs code is installed, so the assistant can write files but cannot run scripts or commands.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
  })

  test('hiding the notice hides the chip and leaves one line in the Tools menu, but returns after a reload', async ({ page }) => {
    await stubChat(page, none)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-commands-notice-hide').click()
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-notice')).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-notice-line')).toHaveText("Commands can't run here")
    await page.reload()
    await expect(page.getByTestId('chat-commands-chip')).toBeVisible()
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-commands-notice')).toBeVisible()
    await shot(page, 'tools-menu-hidden')
  })

  test('a Sandbox that is off for the chat points at its switch, and switching it on removes the notice', async ({ page }) => {
    const calls = await stubChat(page, (off) => {
      const isOff = off === null ? true : off.includes('sandbox')
      return { addons: [{ id: 'sandbox', tools: 7, off: isOff }], commands: isOff ? 'off' : 'available', commands_provider: 'sandbox', commands_isolation: 'isolated' }
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('Running commands is switched off for this chat.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
    await page.getByRole('switch', { name: 'sandbox' }).check()
    await expect.poll(() => calls.at(-1)).toEqual([])
    await expect(notice).toHaveCount(0)
    await expect(page.getByTestId('chat-commands-chip')).toHaveCount(0)
  })

  test('switching the Sandbox on reads the network controls of the chat again', async ({ page }) => {
    let egressReads = 0
    await stubChat(page, (off) => {
      const isOff = off === null ? true : off.includes('sandbox')
      return { addons: [{ id: 'sandbox', tools: 7, off: isOff }], commands: isOff ? 'off' : 'available', commands_provider: 'sandbox', commands_isolation: isOff ? '' : 'isolated',
        execution_status: isOff ? undefined : { readiness: 'available', isolation: 'isolated' } }
    })
    await page.route('**/api/chats/c1/egress', route => {
      egressReads++
      const on = egressReads > 1
      return route.fulfill({ json: { preset: 'off', ceiling: 'all', effective: 'off', applies: on, applies_reason: on ? '' : 'none', addon: on ? 'sandbox' : '', registries: [], rules: [], cards: [] } })
    })
    await page.goto('/app/chats/c1')
    await expect.poll(() => egressReads).toBe(1)
    await page.getByTestId('chat-tools-button').click()
    await page.getByRole('switch', { name: 'sandbox' }).check()
    await expect.poll(() => egressReads).toBeGreaterThan(1)
  })

  test('an unavailable selected provider gives its typed reason without an install link', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 1, off: false }], commands: 'unavailable', commands_provider: 'sandbox', execution_status: { readiness: 'unavailable', explanation: 'no sandbox backend works on this host, so commands cannot run' }, commands_reason: 'no sandbox backend works on this host, so commands cannot run' }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('The selected execution provider is unavailable.')
    await expect(notice).toContainText('no sandbox backend works on this host')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
  })

  test('an unknown status says why the host could not read it', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 1, off: false }], commands: 'available', commands_provider: 'sandbox', execution_status: { readiness: 'unknown', isolation: 'unknown', unknown_reason: 'owner_unreachable' } }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('Execution readiness or isolation is unknown: the add-on owner could not be reached.')
    await shot(page, 'commands-unknown-reason')
  })

  test('an unknown status without a reason keeps the generic text', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 1, off: false }], commands: 'available', commands_provider: 'sandbox', execution_status: { readiness: 'unknown', isolation: 'unknown' } }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-commands-notice')).toContainText('Execution readiness or isolation is unknown.')
    await expect(page.getByTestId('chat-commands-notice')).not.toContainText('add-on')
  })

  test('a chat that runs commands shows neither the chip nor the notice', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'sandbox', tools: 7, off: false }], commands: 'available', commands_provider: 'sandbox', commands_isolation: 'isolated' }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('switch', { name: 'sandbox' })).toBeChecked()
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

test.describe('A chat that switched commands off', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('shows the disabled notice and the off switch without an install link', async ({ page }) => {
    await stubChat(page, () => ({ addons: [], commands: 'switched_off' }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({
      json: {
        builtins: [{ group: 'shell', tools: ['bash'], off: true, usable: true, provider: 'sandbox', providers: { bash: 'sandbox' } }],
        addons: [], servers: [], connectors: [], off: [],
      },
    }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByRole('switch', { name: 'Run commands' })).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('chat-commands-notice')).toContainText('Running commands is switched off for this chat.')
    await expect(page.getByTestId('chat-commands-chip')).toBeVisible()
    await expect(page.getByTestId('chat-commands-notice-link')).toHaveCount(0)
  })
})

test.describe('A tool whose add-on cannot serve the chat', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the Tools menu says which add-on replaces it and whether the built-in serves', async ({ page }) => {
    await stubChat(page, () => ({ addons: [{ id: 'web', tools: 2, off: true }], commands: 'isolated' }))
    await page.route('**/api/chats/c1/tools', route => route.fulfill({
      json: {
        builtins: [{ group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin', providers: { web_search: 'builtin', web_fetch: 'builtin' } }],
        addons: [], servers: [], connectors: [], off: [],
        fallbacks: [
          { tool: 'web_search', addon: 'web', addon_title: 'Web search', builtin: true },
          { tool: 'bash', addon: 'sandbox', addon_title: 'Sandbox', builtin: false },
        ],
      },
    }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-tool-fallback-web_search')).toHaveText(
      'Web search replaces web_search but cannot serve this chat now (stopped, switched off here or not granted): the built-in web_search serves.')
    await expect(page.getByTestId('chat-tool-fallback-bash')).toHaveText(
      'Sandbox replaces bash but cannot serve this chat now, and the built-in bash is off: this chat has no bash.')
    await shot(page, 'tools-menu-fallbacks')
  })
})
