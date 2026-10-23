import { test, expect } from './coverage-fixtures.js'
import { installStudioAddons } from './studio-addon.js'

test.describe('Studio - Transform', () => {
  test.beforeEach(async ({ page }) => { await installStudioAddons(page, { ids: ['studio'] }) })

  test('Studio exposes a Transform tab that renders Audio Transform', async ({ page }) => {
    await page.goto('/app/studio/transform')
    await expect(page.locator('.studio-tab', { hasText: 'Transform' })).toBeVisible()
    await expect(page.locator('h1.page-title', { hasText: 'Audio Transform' })).toBeVisible({ timeout: 15_000 })
  })
})
