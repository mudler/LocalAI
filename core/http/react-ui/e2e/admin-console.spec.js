import { test, expect } from './coverage-fixtures.js'

test.describe('Admin console', () => {
  test('admin pages render inside the grouped console rail', async ({ page }) => {
    await page.goto('/app/models')
    const rail = page.locator('.admin-console-rail')
    await expect(rail).toBeVisible()
    for (const group of ['Inference', 'Cluster', 'Observability', 'Access', 'System']) {
      await expect(rail.locator('.admin-console-group', { hasText: group })).toBeVisible()
    }
  })

  test('console rail cross-navigates between admin pages', async ({ page }) => {
    await page.goto('/app/models')
    const backends = page.locator('.admin-console-rail a.nav-item[href="/app/backends"]')
    await expect(backends).toBeVisible()
    await backends.click()
    await expect(page).toHaveURL(/\/app\/backends/)
    // Rail persists across admin navigation (layout route, not per-page chrome)
    await expect(page.locator('.admin-console-rail')).toBeVisible()
  })

  test('rail links to external API docs', async ({ page }) => {
    await page.goto('/app/settings')
    const api = page.locator('.admin-console-rail a[href$="/swagger/index.html"]')
    await expect(api).toBeVisible()
  })
})
