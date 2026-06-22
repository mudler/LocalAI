import { test, expect } from './coverage-fixtures.js'

// These specs stub /api/features and /api/auth/status per cell. The test server
// disables auth (isAdmin=true) and reports its own features, so we intercept
// before navigation to simulate each role x mode cell.

function stubFeatures(page, features) {
  return page.route('**/api/features', route =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(features) }))
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

test.describe('Adaptive sidebar', () => {
  test('distributed pins the Cluster group with Nodes at the top', async ({ page }) => {
    await stubFeatures(page, { distributed: true })
    await stubNoP2P(page)
    await page.goto('/app/chat') // any in-app page so the sidebar is mounted
    const pinned = page.locator('.sidebar-nav .sidebar-section-items').first()
    await expect(pinned.getByText('Nodes', { exact: false })).toBeVisible({ timeout: 15_000 })
  })

  test('single-node does not pin a Cluster group', async ({ page }) => {
    await stubFeatures(page, { distributed: false })
    await stubNoP2P(page)
    await page.goto('/app/chat')
    // Nodes is reachable only via the Operate rail, not pinned at the top.
    await expect(page.locator('.sidebar-nav')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.sidebar-nav .sidebar-section-items').first()
      .getByText('Nodes', { exact: false })).toHaveCount(0)
  })
})

test.describe('Top navbar', () => {
  test('admin sees the mode pill and settings cog', async ({ page }) => {
    await stubFeatures(page, { distributed: true })
    await stubNoP2P(page)
    await page.goto('/app/chat')
    await expect(page.locator('.top-navbar__mode')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.top-navbar__icon[aria-label]')).not.toHaveCount(0)
  })

  test('admin-via-chat jump shows when localai_assistant is enabled', async ({ page }) => {
    await stubFeatures(page, { distributed: false, localai_assistant: true })
    await stubNoP2P(page)
    await page.goto('/app/chat')
    await expect(page.locator('.top-navbar__assistant')).toBeVisible({ timeout: 15_000 })
  })

  test('admin-via-chat jump hidden when localai_assistant is off', async ({ page }) => {
    await stubFeatures(page, { distributed: false, localai_assistant: false })
    await stubNoP2P(page)
    await page.goto('/app/chat')
    await expect(page.locator('.top-navbar__assistant')).toHaveCount(0)
  })
})

test.describe('Token usage meter', () => {
  test('renders when admin usage has data', async ({ page }) => {
    await stubFeatures(page, { distributed: false })
    await stubNoP2P(page)
    await page.route('**/api/auth/admin/usage**', route =>
      route.fulfill({ contentType: 'application/json',
        body: JSON.stringify({ buckets: [{ total_tokens: 1234 }] }) }))
    await page.goto('/app/chat')
    await expect(page.locator('.top-navbar__meter')).toBeVisible({ timeout: 15_000 })
  })

  test('hidden when admin usage is empty (graceful degrade)', async ({ page }) => {
    await stubFeatures(page, { distributed: false })
    await stubNoP2P(page)
    await page.route('**/api/auth/admin/usage**', route =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ buckets: [] }) }))
    await page.goto('/app/chat')
    await expect(page.locator('.top-navbar')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.top-navbar__meter')).toHaveCount(0)
  })
})
