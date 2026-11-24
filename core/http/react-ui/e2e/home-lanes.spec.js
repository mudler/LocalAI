import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// Home's resident models (inside the memory strip) and the app footer.

const LOADED = [
  { id: 'qwen3-8b-instruct', backend: 'llama-cpp' },
  { id: 'parakeet-tdt-0.6b' },
]

async function mockLoaded(page) {
  await mockHome(page, { loaded: LOADED, models: ['qwen3-8b-instruct', 'parakeet-tdt-0.6b'], chatModels: ['qwen3-8b-instruct'] })
}

async function openStrip(page) {
  await page.locator('.home-strip__head').click()
  await expect(page.locator('.home-loaded')).toBeVisible()
}

test.describe('Home resident models', () => {
  test('resident models read as rows, not status chips', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    await openStrip(page)
    const rows = page.locator('.home-loaded__row')
    await expect(rows).toHaveCount(2)
    // Model ids are identifiers, so they are set in mono like every other
    // identifier in the app.
    const family = await rows.first().locator('code').evaluate(
      el => getComputedStyle(el).fontFamily.toLowerCase())
    expect(family).toMatch(/mono|consol|menlo/)
  })

  test('each row keeps its stop control', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    await openStrip(page)
    const row = page.locator('.home-loaded__row').first()
    await expect(row.getByRole('button', { name: /stop/i })).toBeAttached()
    // Hidden until the row is hovered or focused, but reachable by keyboard.
    await row.hover()
    await expect(row.getByRole('button', { name: /stop/i })).toBeVisible()
  })

  test('the strip reports how many are resident as a figure', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    const stat = page.locator('[data-testid="home-stat-loaded"]')
    await expect(stat).toBeVisible()
    await expect(stat).toContainText('2')
    // The count sits in a line of figures; digits that sit in a column need to
    // line up.
    const numeric = await page.locator('.home-strip__head').evaluate(
      el => getComputedStyle(el.querySelector('.home-fig') || el).fontVariantNumeric)
    expect(numeric).toContain('tabular-nums')
  })

  test('a resident model names the engine serving it', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    await openStrip(page)
    const qwen = page.locator('.home-loaded__row', { hasText: 'qwen3-8b-instruct' })
    await expect(qwen).toContainText('llama-cpp')
  })

  test('a model without a config shows no engine rather than a guess', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    await openStrip(page)
    // parakeet has no backend in the payload; no engine is written.
    const parakeet = page.locator('.home-loaded__row', { hasText: 'parakeet-tdt-0.6b' })
    await expect(parakeet).not.toContainText('llama-cpp')
    await expect(parakeet.locator('small')).toHaveCount(0)
  })

  test('no per-model size is invented: the API reports none', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    await openStrip(page)
    await expect(page.locator('.home-loaded')).not.toContainText(/\bGB\b/)
  })

  test('nothing resident still says so', async ({ page }) => {
    await mockHome(page, { loaded: [], models: ['a-model'], chatModels: ['a-model'] })
    await page.goto('/app')
    await expect(page.locator('[data-testid="home-stat-loaded-none"]')).toBeVisible()
    await page.locator('.home-strip__head').click()
    await expect(page.locator('.home-loaded__empty')).toBeVisible()
    await expect(page.locator('.home-loaded__row')).toHaveCount(0)
  })
})

test.describe('App footer', () => {
  test('is one line, not three stacked rows', async ({ page }) => {
    await page.goto('/app')
    const footer = page.locator('.app-footer')
    await expect(footer).toBeVisible()
    // Three centred rows of chrome cost more vertical space than the content
    // they sit under is usually worth.
    const height = await footer.evaluate(el => el.getBoundingClientRect().height)
    expect(height).toBeLessThan(56)
  })

  test('keeps every link it had', async ({ page }) => {
    await page.goto('/app')
    const footer = page.locator('.app-footer')
    for (const name of [/github/i, /documentation/i, /author/i]) {
      await expect(footer.getByRole('link', { name })).toBeVisible()
    }
  })
})
