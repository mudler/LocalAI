import { test, expect } from './coverage-fixtures.js'

test.describe('Admin console', () => {
  test('admin pages render inside the grouped console rail', async ({ page }) => {
    await page.goto('/app/backends')
    const rail = page.locator('.console-rail')
    await expect(rail).toBeVisible()
    for (const group of ['Inference', 'Cluster', 'Observability', 'Access', 'System']) {
      await expect(rail.locator('.console-group-title', { hasText: group })).toBeVisible()
    }
  })

  test('console rail cross-navigates between admin pages', async ({ page }) => {
    await page.goto('/app/backends')
    const settings = page.locator('.console-rail a.nav-item[href="/app/settings"]')
    await expect(settings).toBeVisible()
    await settings.click()
    await expect(page).toHaveURL(/\/app\/settings/)
    // Rail persists across admin navigation (layout route, not per-page chrome)
    await expect(page.locator('.console-rail')).toBeVisible()
  })

  test('rail links to external API docs', async ({ page }) => {
    await page.goto('/app/settings')
    const api = page.locator('.console-rail a[href$="/swagger/index.html"]')
    await expect(api).toBeVisible()
  })
})
