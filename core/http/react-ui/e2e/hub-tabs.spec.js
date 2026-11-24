import { test, expect } from './coverage-fixtures.js'

// What is left of the hubs after Administration got a sidebar of its own
// (admin-sidebar.spec.js): the pages the Build hub had are pages of their own
// with no tab bar, and the sidebar gates its items by feature and by role.

async function mockFeatures(page, features) {
  await page.route('**/api/features', route => route.fulfill({ json: features }))
}

async function mockAuth(page, user, authEnabled = true) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled, staticApiKeyRequired: false, providers: ['local'], user },
  }))
}

test.describe('Hub tabs: no tab bar on the pages the Build hub had', () => {
  for (const path of ['/app/collections', '/app/import-model']) {
    test(`${path} has no hub tab bar`, async ({ page }) => {
      await page.goto(path)
      await expect(page.locator('h1').first()).toBeVisible()
      await expect(page.locator('.dk-hubtabs')).toHaveCount(0)
    })
  }
})

test.describe('Hub tabs: gating', () => {
  test('feature flags hide the sidebar items they gate', async ({ page }) => {
    await mockFeatures(page, { collections: false, mcp: false, fine_tuning: false, quantization: false, distributed: false })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/collections"]')).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/agents"]')).toBeVisible()
  })

  test('a user without admin sees Knowledge with the collections permission and no Import', async ({ page }) => {
    await mockFeatures(page, { collections: true, mcp: true, fine_tuning: true, quantization: true })
    await mockAuth(page, { id: 'u1', name: 'Sam', role: 'user', permissions: { collections: true } })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/collections"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/import-model"]')).toHaveCount(0)
  })

  test('a non-admin user gets no Operate entry in the sidebar', async ({ page }) => {
    await mockAuth(page, { id: 'u1', name: 'Sam', role: 'user', permissions: { collections: true } })
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Administration' })).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })).toHaveCount(0)
  })
})
