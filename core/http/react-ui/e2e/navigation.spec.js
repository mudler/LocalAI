import { test, expect } from './coverage-fixtures.js'

test.describe('Navigation', () => {
  test('/ redirects to /app', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/app/)
  })

  test('/app shows home page with LocalAI title', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.locator('.home-page')).toBeVisible()
  })

  test('sidebar traces link navigates to /app/traces', async ({ page }) => {
    await page.goto('/app')
    // Expand the "System" collapsible section so the traces link is visible
    const systemSection = page.locator('button.sidebar-section-toggle', { hasText: 'System' })
    await systemSection.click()
    const tracesLink = page.locator('a.nav-item[href="/app/traces"]')
    await expect(tracesLink).toBeVisible()
    await tracesLink.click()
    await expect(page).toHaveURL(/\/app\/traces/)
    await expect(page.getByRole('heading', { name: 'Traces', exact: true })).toBeVisible()
  })

  test('old cluster routes redirect to /app/cluster', async ({ page }) => {
    await page.goto('/app/p2p')
    await expect(page).toHaveURL(/\/app\/cluster$/)
    await page.goto('/app/nodes')
    await expect(page).toHaveURL(/\/app\/cluster$/)
  })
})
