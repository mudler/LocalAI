import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// The incognito button of the composer's bottom bar on Home and on the chat
// page, with the API stubbed: it shows only when the server allows it, every
// request of the incognito chat carries the marker, a zero-retention chat
// takes no attachment, nothing of the chat reaches the browser storage, and
// leaving the chat ends it on the server.

const SENTINEL = 'INCOGNITO-SENTINEL-4711'

async function server(page, { policy = 'allow', maxLevel = 'zero-retention' } = {}) {
  const seen = { created: [], markers: [], deleted: [], sent: [] }
  const chat = { id: 'i1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-11-25T10:00:00Z', updated: '2026-11-25T10:00:00Z', running: false }
  const mark = (route) => seen.markers.push(route.request().headers()['x-localai-incognito'] || '')
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      mark(route)
      const level = route.request().headers()['x-localai-incognito'] || ''
      seen.created.push({ body: route.request().postDataJSON(), level })
      if (level) chat.incognito = level
      return route.fulfill({ status: 201, json: chat })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true, incognito: { policy, max_level: maxLevel } } })
  })
  await page.route(/\/api\/chats\/i1\/messages(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      mark(route)
      seen.sent.push(route.request().postDataJSON().text)
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: seen.sent.map((text, i) => ({ seq: i + 1, turn: i + 1, role: 'user', text, at: '2026-11-25T10:00:00Z' })) } })
  })
  await page.route('**/api/chats/i1/events', route => route.abort())
  await page.route('**/api/chats/i1', route => {
    if (route.request().method() === 'DELETE') {
      seen.deleted.push(route.request().headers()['x-localai-incognito'] || '')
      return route.fulfill({ json: { deleted: true } })
    }
    return route.fulfill({ json: { ...chat, pending: [] } })
  })
  await page.route(/\/api\/chats\/draft\/(tools|addons)(\?.*)?$/, route => route.fulfill({ json: { builtins: [], addons: [], servers: [], connectors: [], off: [] } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [] } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents: [] } }))
  return seen
}

const storageHas = (page, text) => page.evaluate((t) => {
  for (const s of [localStorage, sessionStorage]) {
    for (let i = 0; i < s.length; i++) if ((s.getItem(s.key(i)) || '').includes(t)) return true
  }
  return false
}, text)

test.describe('Incognito chats', () => {
  test.beforeEach(async ({ page }) => { await mockHome(page) })

  test('a zero-retention chat carries the marker, keeps nothing in the browser and ends when you leave it', async ({ page }) => {
    const seen = await server(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    const bar = page.getByTestId('chat-incognito')
    await expect(bar).toBeVisible()
    const chip = page.getByTestId('chat-incognito-button')
    const attach = page.getByRole('button', { name: 'Attach file' })
    // Off, it is an icon of the bottom bar, named by its state.
    await expect(page.locator('.home-cmd__foot').getByTestId('chat-incognito-button')).toBeVisible()
    await expect(chip).toHaveAttribute('aria-label', 'Incognito: off')
    await expect(chip).toHaveText('')
    await expect(attach).toBeVisible()
    await expect(page.getByTestId('chat-incognito-note')).toHaveCount(0)
    await chip.click()
    // The menu opens with the focus on the switch.
    await expect(page.getByTestId('chat-incognito-switch')).toBeFocused()
    await page.getByTestId('chat-incognito-switch').click()
    await expect(page.getByTestId('chat-incognito-desc')).toContainText('memory')
    await page.getByTestId('chat-incognito-level-zero-retention').click()
    await expect(bar).toHaveAttribute('data-level', 'zero-retention')
    await expect(page.getByTestId('chat-incognito-desc')).toContainText('only usage counts')
    // Closed, the button names the level and the line under the composer
    // says what it keeps. An incognito chat belongs to no project.
    await page.keyboard.press('Escape')
    await expect(chip).toBeFocused()
    await expect(chip).toHaveText('Zero retention')
    await expect(chip).toHaveAttribute('aria-label', 'Incognito: Zero retention')
    await expect(page.getByTestId('chat-incognito-note')).toContainText('only usage counts')
    await expect(page.getByTestId('project-picker-incognito')).toBeVisible()
    await expect(attach).toHaveCount(0)

    await page.getByTestId('home-input').fill(SENTINEL)
    // The box of an incognito chat is never written to the browser storage.
    await page.waitForTimeout(600)
    expect(await storageHas(page, SENTINEL)).toBe(false)

    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/i1/)
    expect(seen.created).toHaveLength(1)
    expect(seen.created[0].level).toBe('zero-retention')
    expect(seen.markers.every(m => m === 'zero-retention')).toBe(true)
    await expect(page.getByTestId('chat-incognito')).toHaveAttribute('data-level', 'zero-retention')
    await page.getByTestId('chat-input').fill('a second thought ' + SENTINEL)
    await page.waitForTimeout(600)
    expect(await storageHas(page, SENTINEL)).toBe(false)

    // The open chat keeps its level and takes no attachment: the menu offers
    // no level to pick.
    await expect(page.getByTestId('chat-incognito-note')).toContainText('only usage counts')
    await expect(attach).toHaveCount(0)
    // It has no files either: the header draws no Files button.
    await expect(page.getByTestId('chat-files-toggle')).toHaveCount(0)
    await expect(page.locator('#chat-files-pane')).toHaveCount(0)
    await expect(page.locator('.cx-dock .home-cmd__foot').getByTestId('chat-incognito-button')).toHaveText('Zero retention')
    await page.getByTestId('chat-incognito-button').click()
    await expect(page.getByTestId('chat-incognito-level')).toHaveCount(0)
    await expect(page.getByTestId('chat-incognito-level-name')).toHaveText('Zero retention')
    // Turning the switch off asks, then ends the chat on the server.
    await page.getByTestId('chat-incognito-switch').click()
    await page.getByRole('button', { name: 'End chat' }).click()
    await expect.poll(() => seen.deleted).toEqual(['zero-retention'])
    expect(await storageHas(page, SENTINEL)).toBe(false)
  })

  test('the button is hidden when the server forbids incognito', async ({ page }) => {
    await server(page, { policy: 'forbid' })
    await page.goto('/app')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    await expect(page.getByTestId('chat-incognito')).toHaveCount(0)
  })

  test('a memory-off chat keeps the Files button of the header', async ({ page }) => {
    const seen = await server(page)
    await page.route('**/api/chats/i1/files', route => route.fulfill({ json: { files: [] } }))
    await page.goto('/app')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    await page.getByTestId('chat-incognito-button').click()
    await page.getByTestId('chat-incognito-switch').click()
    await expect(page.getByTestId('chat-incognito')).toHaveAttribute('data-level', 'memory-off')
    await page.keyboard.press('Escape')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/i1/)
    expect(seen.created[0].level).toBe('memory-off')
    await expect(page.getByTestId('chat-incognito')).toHaveAttribute('data-level', 'memory-off')
    await expect(page.getByTestId('chat-files-toggle')).toBeVisible()
  })

  test('a server that offers memory-off only shows no level picker', async ({ page }) => {
    await server(page, { maxLevel: 'memory-off' })
    await page.goto('/app')
    await page.getByTestId('chat-incognito-button').click()
    await page.getByTestId('chat-incognito-switch').click()
    await expect(page.getByTestId('chat-incognito')).toHaveAttribute('data-level', 'memory-off')
    await expect(page.getByTestId('chat-incognito-level')).toHaveCount(0)
    await expect(page.getByTestId('chat-incognito-level-name')).toHaveText('Memory off')
  })
})
