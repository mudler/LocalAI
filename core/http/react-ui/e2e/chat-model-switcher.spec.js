import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat, storedChats, RESOURCES_24GB } from './chat-fixtures.js'

// The model chip lists the chat models: loaded ones first, a dot for warm or
// not loaded, an eye for vision, and, where the server can estimate, whether
// the model fits this machine. Nothing is shown that the API does not report.

const GB = 1024 * 1024 * 1024
const NEED_GB = { 'qwen3-8b': 5.1, 'gemma-3-12b': 7.6, 'phi-4-mini': 2.5, 'llama-70b': 42.5, 'deepseek-32b': 19.5 }

const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] },
  { id: 'gemma-3-12b', capabilities: ['FLAG_CHAT', 'FLAG_VISION'] },
  { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'] },
  { id: 'deepseek-32b', capabilities: ['FLAG_CHAT'] },
  { id: 'llama-70b', capabilities: ['FLAG_CHAT'] },
  { id: 'mystery-model', capabilities: ['FLAG_CHAT'] },
]

async function stubEstimates(page) {
  const asked = []
  await page.route('**/api/models/vram-estimate', (route) => {
    const body = route.request().postDataJSON()
    asked.push(body)
    const need = NEED_GB[body.model]
    // A model the server cannot read answers 200 with no figure.
    if (!need) return route.fulfill({ json: { message: 'no estimate for this format' } })
    return route.fulfill({ json: { vram_bytes: need * GB, size_bytes: need * 0.9 * GB } })
  })
  return asked
}

const chip = (page) => page.getByTestId('home-model-chip')
const row = (page, name) => page.getByRole('option').filter({ has: page.locator('code', { hasText: new RegExp(`^${name}$`) }) })

test.describe('Chat model switcher', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page, { models: MODELS, loaded: ['qwen3-8b', 'phi-4-mini'], resources: RESOURCES_24GB })
  })

  test('groups loaded and installed models and says warm or not loaded', async ({ page }) => {
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', pair('hi', 'hello'), { contextSize: 8192 })])
    await chip(page).click()
    const heads = page.locator('.home-menu__head')
    await expect(heads).toHaveText(['Loaded now', 'Installed'])
    await expect(row(page, 'qwen3-8b')).toContainText('warm')
    await expect(row(page, 'phi-4-mini')).toContainText('warm')
    await expect(row(page, 'gemma-3-12b')).toContainText('not loaded')
    // Loaded models come first.
    const names = await page.getByRole('option').locator('code').allTextContents()
    expect(names.slice(0, 2).sort()).toEqual(['phi-4-mini', 'qwen3-8b'])
    // The chip's dot follows the loaded state.
    await expect(chip(page).locator('.home-dot')).not.toHaveClass(/home-dot--cold/)
  })

  test('marks the models that understand images', async ({ page }) => {
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', [])])
    await chip(page).click()
    await expect(row(page, 'gemma-3-12b').getByRole('img', { name: 'Understands images' })).toBeVisible()
    await expect(row(page, 'phi-4-mini').getByRole('img', { name: 'Understands images' })).toHaveCount(0)
  })

  test('reads memory and estimates once the list opens, then shows what fits', async ({ page }) => {
    const asked = await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', [], { contextSize: 16384 })])
    // Nothing is asked until the list opens.
    await page.waitForTimeout(300)
    expect(asked).toHaveLength(0)

    await chip(page).click()
    // Warm models say they are ready; a cold one that fits says how much room is left.
    await expect(row(page, 'qwen3-8b')).toContainText('ready now')
    await expect(row(page, 'gemma-3-12b')).toContainText('GB free')
    await expect(row(page, 'gemma-3-12b')).toContainText('7.6 GB')
    // 19.5 GB on a 24 GB card with a 22.8 GB limit fits with 3.3 left.
    await expect(row(page, 'deepseek-32b')).toContainText('3.3 GB free')
    // 42.5 GB does not fit the card; the rest would run on the CPU (RAM is 40 GB).
    await expect(row(page, 'llama-70b')).toContainText('runs on the CPU')
    // The estimates use this chat's context size.
    expect(asked.every(b => b.context_size === 16384)).toBe(true)
    // The memory bar closes the list.
    await expect(page.getByTestId('chat-model-memory')).toContainText('7.6 of 24 GB used')
  })

  test('says how far over the machine a model is when it cannot run at all', async ({ page }) => {
    await page.route('**/api/resources', route => route.fulfill({ json: {
      ...RESOURCES_24GB, ram: { total: 16 * GB, used: 10 * GB, available: 6 * GB },
    } }))
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', [])])
    await chip(page).click()
    await expect(row(page, 'llama-70b')).toContainText('more than this machine has')
  })

  test('a model with no estimate shows no fit text and no invented numbers', async ({ page }) => {
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', [])])
    await chip(page).click()
    await expect(row(page, 'deepseek-32b')).toContainText('GB free')
    const mystery = row(page, 'mystery-model')
    await expect(mystery).toContainText('not loaded')
    await expect(mystery).not.toContainText('GB')
    await expect(mystery).not.toContainText('free')
  })

  test('picking a model changes this chat and remembers it', async ({ page }) => {
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', [])])
    await chip(page).click()
    await row(page, 'gemma-3-12b').click()
    await expect(chip(page)).toContainText('gemma-3-12b')
    await expect(page.getByRole('option')).toHaveCount(0)
    await expect.poll(async () => (await storedChats(page)).chats[0].model).toBe('gemma-3-12b')
    // Cold now: the dot is hollow.
    await expect(chip(page).locator('.home-dot')).toHaveClass(/home-dot--cold/)
  })

  test('/model opens the list and Esc closes it', async ({ page }) => {
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', pair('hi', 'hello'))])
    await page.getByTestId('chat-input').fill('/model')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByRole('listbox', { name: 'Model' })).toBeVisible()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Escape')
    await expect(page.getByRole('listbox', { name: 'Model' })).toHaveCount(0)
    await expect(chip(page)).toBeFocused()
  })

  test('on a phone the list is a sheet from the bottom', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubEstimates(page)
    await openChat(page, [chatOf('c1', 'Models', 'qwen3-8b', pair('hi', 'hello'))])
    await chip(page).click()
    const menu = page.locator('.home-menu--models')
    await expect(menu).toBeVisible()
    const box = await menu.boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeLessThanOrEqual(844)
    expect(await menu.evaluate(el => getComputedStyle(el).position)).toBe('fixed')
  })
})
