import { test, expect } from './coverage-fixtures.js'

test.describe('Home editorial redesign', () => {
  test('renders the serif greeting header', async ({ page }) => {
    await page.goto('/app')
    const greeting = page.locator('.home-greeting')
    await expect(greeting).toBeVisible({ timeout: 15_000 })
    const family = await greeting.evaluate(el => getComputedStyle(el).fontFamily)
    expect(family.toLowerCase()).toContain('fraunces')
  })

  test('quick links expose a single primary action', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.home-greeting, .empty-state-title').first()).toBeVisible({ timeout: 15_000 })
    const primaries = page.locator('.home-quick-links .btn-primary')
    // At most one primary CTA in the quick-links row.
    expect(await primaries.count()).toBeLessThanOrEqual(1)
  })

  test('loaded-models block uses an editorial section heading', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.home-greeting').first()).toBeVisible({ timeout: 15_000 })
    // The refined loaded-models block introduces a SectionHeading; the legacy
    // inline ".home-loaded-text" label is gone.
    const heading = page.locator('.home-loaded .section-heading')
    await expect(heading).toBeVisible({ timeout: 15_000 })
    await expect(heading).toHaveText(/active models/i)
  })
})
