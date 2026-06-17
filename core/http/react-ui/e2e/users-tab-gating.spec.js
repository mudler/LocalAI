import { test, expect } from '@playwright/test'

// Two surfaces enforce single-user (no-auth) gating for the Users page:
//   1. Sidebar entry: hidden via the `authOnly: true` flag in Sidebar.jsx
//      (filterItem returns false when `!authEnabled`).
//   2. Direct URL navigation: RequireAuthEnabled wrapping the /app/users
//      route in router.jsx redirects to /app when authEnabled is false.
//
// Without (2), an old bookmark or pasted URL would land on a page rendered
// against admin-only `/api/auth/admin/users` data — which doesn't exist
// when auth is off — and the user sees a confusing empty/error state.
//
// These specs are the "prevent accidental removal" guarantee — if anyone
// drops the gating, /app/users stays open in single-user mode and the
// test fails on the redirect or the visible sidebar item.

test.describe('Users tab — single-user no-auth mode', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          authEnabled: false,
          staticApiKeyRequired: false,
          providers: [],
        }),
      })
    )
  })

  test('sidebar does not list Users entry', async ({ page }) => {
    // Users now lives in the admin-console rail (authOnly gate). With auth
    // off the rail must not list it. /app/models is the console default and
    // is reachable because no-auth ⇒ isAdmin.
    await page.goto('/app/models')
    const usersLink = page.locator('.admin-console-rail a.nav-item[href="/app/users"]')
    await expect(usersLink).toHaveCount(0)
  })

  test('direct navigation to /app/users redirects to /app', async ({ page }) => {
    await page.goto('/app/users')
    // RequireAuthEnabled performs the redirect synchronously, but the URL
    // change is async — wait for it before asserting.
    await page.waitForURL(/\/app(?!\/users)/, { timeout: 5000 })
    expect(page.url()).toMatch(/\/app(\/?$|\/(?!users))/)
  })
})

test.describe('Users tab — auth on', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          authEnabled: true,
          staticApiKeyRequired: false,
          providers: ['local'],
          // Mark the viewer as admin so the sidebar's adminOnly gate also
          // passes; the test then exercises the authOnly path in isolation.
          user: { id: 'admin-uuid', name: 'Admin', role: 'admin', provider: 'local' },
        }),
      })
    )
  })

  test('sidebar lists Users entry when auth is on', async ({ page }) => {
    // With auth on and an admin viewer the console rail lists Users.
    await page.goto('/app/models')
    const usersLink = page.locator('.admin-console-rail a.nav-item[href="/app/users"]')
    await expect(usersLink).toBeVisible()
  })
})
