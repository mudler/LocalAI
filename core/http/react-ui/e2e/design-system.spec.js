import { test, expect } from './coverage-fixtures.js'

test.describe('Editorial design system', () => {
  test('page titles render in the Fraunces serif', async ({ page }) => {
    await page.goto('/app/settings')
    const title = page.locator('.page-title').first()
    await expect(title).toBeVisible({ timeout: 15_000 })
    const family = await title.evaluate(el => getComputedStyle(el).fontFamily)
    expect(family.toLowerCase()).toContain('fraunces')
  })
})
