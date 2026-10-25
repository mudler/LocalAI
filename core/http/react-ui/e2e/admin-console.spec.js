import { test, expect } from './coverage-fixtures.js'

test.describe('Admin console', () => {
  test('admin pages render under the Operate tab bar', async ({ page }) => {
    await page.goto('/app/backends')
    const bar = page.locator('.dk-hubtabs')
    await expect(bar).toBeVisible()
    // Six tabs for the single-node case; Swarm only appears with distributed
    // mode on. The tab that owns the current page carries aria-current.
    for (const name of ['Status', 'This machine', 'Runtime', 'Traffic', 'Settings']) {
      await expect(bar.getByRole('link', { name, exact: false })).toBeVisible()
    }
    await expect(bar.locator('[data-hub-tab="runtime"]')).toHaveAttribute('aria-current', 'page')
  })

  test('the tab bar cross-navigates between admin pages', async ({ page }) => {
    await page.goto('/app/backends')
    const settings = page.locator('.dk-hubtabs a[href="/app/settings"]')
    await expect(settings).toBeVisible()
    await settings.click()
    await expect(page).toHaveURL(/\/app\/settings/)
    // The bar persists across admin navigation (layout route, not per-page chrome)
    await expect(page.locator('.dk-hubtabs')).toBeVisible()
    await expect(page.locator('.dk-hubtabs [data-hub-tab="settings"]')).toHaveAttribute('aria-current', 'page')
  })

  test('the bar links to the external API docs', async ({ page }) => {
    await page.goto('/app/settings')
    const api = page.locator('.dk-hubtabs a[href$="/swagger/index.html"]')
    await expect(api).toBeVisible()
    await expect(api).toHaveAttribute('target', '_blank')
  })
})
