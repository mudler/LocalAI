import { test, expect } from './coverage-fixtures.js'

// These specs stub /api/features and /api/auth/status per cell. The test server
// disables auth (isAdmin=true) and reports its own features, so we intercept
// before navigation to simulate each role x mode cell.

function stubFeatures(page, features) {
  return page.route('**/api/features', route =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(features) }))
}

function stubAuth(page, status) {
  return page.route('**/api/auth/status', route =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(status) }))
}

function stubNoP2P(page) {
  // P2P token endpoint returns empty -> p2pEnabled=false.
  return page.route('**/api/p2p/token', route =>
    route.fulfill({ contentType: 'text/plain', body: '' }))
}

test.describe('Adaptive landing (HomeRoute)', () => {
  test('admin + distributed redirects /app to Nodes', async ({ page }) => {
    await stubFeatures(page, { distributed: true })
    await stubNoP2P(page)
    await page.goto('/app')
    await expect(page).toHaveURL(/\/app\/nodes$/)
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
  })

  test('admin + single-node stays on Home', async ({ page }) => {
    await stubFeatures(page, { distributed: false })
    await stubNoP2P(page)
    await page.goto('/app')
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
  })
})
