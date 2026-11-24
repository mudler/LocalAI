import { test, expect } from './coverage-fixtures.js'
import { adminItem, adminCurrent, ADMIN_NAV } from './admin-nav.js'

test.describe('Admin console', () => {
  test('admin pages render with the Administration sidebar', async ({ page }) => {
    await page.goto('/app/backends')
    const nav = page.locator(ADMIN_NAV)
    await expect(nav).toBeVisible()
    // The pages of the single-node case; the Cluster group only appears with
    // distributed mode (or P2P) on. The item of the current page carries
    // aria-current.
    for (const name of ['Overview', 'This machine', 'Backends', 'Activity', 'Updates', 'Settings']) {
      await expect(nav.getByRole('link', { name })).toBeVisible()
    }
    await expect(nav.getByRole('button', { name: /Traffic/ })).toBeVisible()
    await expect(adminCurrent(page)).toHaveAttribute('data-admin-item', '/app/backends')
  })

  test('the sidebar cross-navigates between admin pages', async ({ page }) => {
    await page.goto('/app/backends')
    const settings = adminItem(page, '/app/settings')
    await expect(settings).toBeVisible()
    await settings.click()
    await expect(page).toHaveURL(/\/app\/settings/)
    // The sidebar persists across admin navigation (layout route, not per-page chrome)
    await expect(page.locator(ADMIN_NAV)).toBeVisible()
    await expect(settings).toHaveAttribute('aria-current', 'page')
  })

  test('the sidebar links to the external API docs', async ({ page }) => {
    await page.goto('/app/settings')
    const api = page.locator(`${ADMIN_NAV} a[href$="/swagger/index.html"]`)
    await expect(api).toBeVisible()
    await expect(api).toHaveAttribute('target', '_blank')
  })
})
