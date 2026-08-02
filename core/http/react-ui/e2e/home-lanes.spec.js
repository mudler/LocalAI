import { test, expect } from './coverage-fixtures.js'

// Home's resident-model list and the app footer.

const SYS_INFO = {
  backends: ['llama-cpp'],
  loaded_models: [
    { id: 'qwen3-8b-instruct', backend: 'llama-cpp' },
    { id: 'parakeet-tdt-0.6b' },
  ],
}

async function mockLoaded(page) {
  await page.route('**/system', route => route.fulfill({ json: SYS_INFO }))
  await page.route('**/v1/models', route =>
    route.fulfill({ json: { data: [{ id: 'qwen3-8b-instruct' }, { id: 'parakeet-tdt-0.6b' }] } }))
}

test.describe('Home resident models', () => {
  test('resident models read as lanes, not status chips', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    const lanes = page.locator('.home-loaded .lane')
    await expect(lanes).toHaveCount(2)
    // Model ids are identifiers, so they are set in mono like every other
    // identifier in the app.
    const family = await lanes.first().locator('.lane__name').evaluate(
      el => getComputedStyle(el).fontFamily.toLowerCase())
    expect(family).toMatch(/mono|consol|menlo/)
  })

  test('each lane keeps its stop control', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    const lane = page.locator('.home-loaded .lane').first()
    await expect(lane.getByRole('button', { name: /stop/i })).toBeVisible()
  })

  test('the header reports how many are resident as a figure', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    const stat = page.locator('[data-testid="home-stat-loaded"]')
    await expect(stat).toBeVisible()
    await expect(stat).toContainText('2')
    // Digits that sit in a column need to line up.
    const numeric = await stat.locator('.home-stat__value').evaluate(
      el => getComputedStyle(el).fontVariantNumeric)
    expect(numeric).toContain('tabular-nums')
  })

  test('a resident model names the engine serving it', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    // Lanes are sorted by id, so target by content rather than position.
    const qwen = page.locator('.home-loaded .lane', { hasText: 'qwen3-8b-instruct' })
    await expect(qwen).toContainText('llama-cpp')
  })

  test('a model without a config shows no engine rather than a guess', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    // parakeet has no backend in the payload; the column stays blank.
    const parakeet = page.locator('.home-loaded .lane', { hasText: 'parakeet-tdt-0.6b' })
    await expect(parakeet).not.toContainText('llama-cpp')
  })

  test('jump-back-in offers the three places worth returning to', async ({ page }) => {
    await mockLoaded(page)
    await page.goto('/app')
    const lanes = page.locator('.lanes--jump .lane')
    await expect(lanes).toHaveCount(3)
    await expect(lanes.first()).toContainText('Discover')
  })

  test('nothing resident still says so', async ({ page }) => {
    await page.route('**/system', route =>
      route.fulfill({ json: { backends: ['llama-cpp'], loaded_models: [] } }))
    await page.route('**/v1/models', route => route.fulfill({ json: { data: [{ id: 'a-model' }] } }))
    await page.goto('/app')
    await expect(page.locator('.home-loaded-empty')).toBeVisible()
    await expect(page.locator('.home-loaded .lane')).toHaveCount(0)
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
