import { test, expect } from './coverage-fixtures.js'

test.describe('Editorial design system', () => {
  test('page titles render in the Fraunces serif', async ({ page }) => {
    await page.goto('/app/settings')
    const title = page.locator('.page-title').first()
    await expect(title).toBeVisible({ timeout: 15_000 })
    const family = await title.evaluate(el => getComputedStyle(el).fontFamily)
    expect(family.toLowerCase()).toContain('fraunces')
  })

  test('active nav item shows a left accent rail (box-shadow), not just a tint', async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    const active = page.locator('.sidebar-nav .nav-item.active').first()
    await expect(active).toBeVisible()
    const shadow = await active.evaluate(el => getComputedStyle(el).boxShadow)
    expect(shadow).not.toBe('none')
  })
})
