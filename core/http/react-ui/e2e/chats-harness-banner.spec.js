import { test, expect } from './coverage-fixtures.js'
import { stubServerChat } from './chat-settings-fixtures.js'
import { mockUser } from './home-fixtures.js'

// The banner of a chat whose model has a window that is too small for the chat
// agent. The server decides that (the `harness` block of the chat); the page
// shows one banner and, by role, the one thing the person can do about it.
// The API is stubbed.

const GIB = 1024 ** 3

const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'], context_size: 8192 },
  { id: 'gemma-3-12b', capabilities: ['FLAG_CHAT'], context_size: 32768 },
  { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'], context_size: 4096 },
]

const SMALL = { ok: false, window: 8192, minimum: 24576, recommended: 32768, can_fix: true, admin: true, source: 'model' }

// What the memory estimate answers for a window: 4 GiB of weights and 160 KiB
// of cache per token.
async function stubEstimate(page, { calls = [] } = {}) {
  await page.route('**/api/models/vram-estimate', route => {
    const { model, context_size: ctx } = route.request().postDataJSON()
    calls.push({ model, ctx })
    route.fulfill({ json: { size_bytes: 4 * GIB, vram_bytes: 4 * GIB + ctx * 160 * 1024, model_max_context: 131072 } })
  })
  return calls
}

test.describe('Chat window banner', () => {
  test('an administrator is told what fits and sets the window of the model with one button', async ({ page }) => {
    const estimates = await stubEstimate(page)
    const api = await stubServerChat(page, { harness: SMALL, models: MODELS })
    const configPatches = []
    await page.route('**/api/models/config-json/qwen3-8b', route => {
      // The chat page also reads the config of its model (a GET) for the model info.
      if (route.request().method() !== 'PATCH') return route.fulfill({ json: {} })
      configPatches.push(route.request().postDataJSON())
      api.chat.harness = { ...api.chat.harness, ok: true, window: configPatches[0].context_size }
      route.fulfill({ json: { message: 'ok' } })
    })
    await page.goto('/app/chats/c1')

    const banner = page.getByTestId('chat-harness')
    await expect(banner).toBeVisible()
    await expect(banner).toHaveAttribute('data-role', 'admin')
    await expect(banner).toContainText('8192 tokens')
    await expect(banner).toContainText('at least 24576')
    // 32768 tokens need about 9 GiB: they fit a 24 GiB card.
    await expect(page.getByTestId('chat-harness-memory')).toContainText('32768 tokens fit')
    expect(estimates[0]).toEqual({ model: 'qwen3-8b', ctx: 32768 })
    // Only the model's config changes, and only through the admin endpoint.
    await page.getByTestId('chat-harness-fix').click()
    await expect.poll(() => configPatches).toEqual([{ context_size: 32768 }])
    await expect(banner).toHaveCount(0)
  })

  test('an administrator is offered the largest window that fits a small machine', async ({ page }) => {
    // 8.4 GiB of memory: 32768 tokens need 9 GiB and do not fit, 24576 need
    // 7.75 GiB and do.
    await stubEstimate(page)
    await stubServerChat(page, { harness: SMALL, models: MODELS })
    await page.route('**/api/resources', route => route.fulfill({
      json: { type: 'gpu', available: true, gpus: [], ram: { total: 64 * GIB, available: 40 * GIB }, aggregate: { total_memory: 8.4 * GIB, gpu_count: 1 } },
    }))
    await page.goto('/app/chats/c1')
    const memory = page.getByTestId('chat-harness-memory')
    await expect(memory).toContainText('32768 tokens do not fit')
    await expect(memory).toContainText('24576 do')
    await expect(page.getByTestId('chat-harness-fix')).toContainText('24576')
  })

  test('an administrator is told to pick a smaller model when nothing fits', async ({ page }) => {
    await stubEstimate(page)
    await stubServerChat(page, { harness: SMALL, models: MODELS })
    await page.route('**/api/resources', route => route.fulfill({
      json: { type: 'gpu', available: true, gpus: [], ram: { total: 8 * GIB, available: 2 * GIB }, aggregate: { total_memory: 4 * GIB, gpu_count: 1 } },
    }))
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-harness-memory')).toContainText('Pick a smaller model')
    await expect(page.getByTestId('chat-harness-fix')).toHaveCount(0)
  })

  test('a regular user can only switch the chat to a model with a large enough window', async ({ page }) => {
    await mockUser(page, { chats: true })
    const api = await stubServerChat(page, {
      harness: { ok: false, window: 8192, minimum: 16384, recommended: 32768, can_fix: false, admin: false, source: 'model' },
      models: MODELS,
    })
    let touchedConfig = false
    await page.route('**/api/models/config-json/**', route => { touchedConfig = true; route.abort() })
    await page.goto('/app/chats/c1')

    const banner = page.getByTestId('chat-harness')
    await expect(banner).toBeVisible()
    await expect(banner).toHaveAttribute('data-role', 'user')
    await expect(banner).toContainText('at least 16384')
    // The model with a window below the minimum is not offered, and there is
    // no control for the settings of the model.
    await expect(page.getByTestId('chat-harness-switch-gemma-3-12b')).toBeVisible()
    await expect(page.getByTestId('chat-harness-switch-phi-4-mini')).toHaveCount(0)
    await expect(page.getByTestId('chat-harness-fix')).toHaveCount(0)

    await page.getByTestId('chat-harness-switch-gemma-3-12b').click()
    await expect.poll(() => api.patches).toEqual([{ model: 'gemma-3-12b' }])
    await expect(banner).toHaveCount(0)
    expect(touchedConfig).toBe(false)
  })

  test('a regular user is told to ask an administrator when no model has a large enough window', async ({ page }) => {
    await mockUser(page, { chats: true })
    await stubServerChat(page, {
      harness: { ok: false, window: 8192, minimum: 16384, recommended: 32768, can_fix: false, admin: false, source: 'model' },
      models: MODELS.filter(m => m.context_size < 16384),
    })
    await page.goto('/app/chats/c1')
    const banner = page.getByTestId('chat-harness')
    await expect(banner).toContainText('Ask an administrator')
    await expect(page.getByTestId('chat-harness-options')).toHaveCount(0)
  })

  test('shows nothing when the window is large enough', async ({ page }) => {
    await stubServerChat(page, { harness: { ok: true, window: 32768, minimum: 24576, recommended: 32768, can_fix: false, admin: true } })
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-input')).toBeVisible()
    await expect(page.getByTestId('chat-harness')).toHaveCount(0)
  })

  test('the owner can undo a chat setting that is below the minimum', async ({ page }) => {
    const api = await stubServerChat(page, {
      settings: { context_size: 8192, temperature: 0.4 },
      harness: { ok: false, window: 8192, minimum: 16384, recommended: 32768, can_fix: true, admin: false, source: 'chat' },
    })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-harness-reset').click()
    await expect.poll(() => api.patches).toEqual([{ settings: { temperature: 0.4 } }])
  })
})
