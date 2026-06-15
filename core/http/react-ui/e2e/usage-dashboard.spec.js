import { test, expect } from '@playwright/test'

// Mock usage payload as the new /api/usage endpoint returns it.
const MOCK_USAGE = {
  viewer: { id: 'local-uuid', name: 'local', role: 'admin', provider: 'local' },
  totals: {
    prompt_tokens: 1234,
    completion_tokens: 567,
    total_tokens: 1801,
    request_count: 42,
  },
  usage: [
    {
      bucket: '2026-05-05',
      model: 'qwen-7b',
      user_id: 'local-uuid',
      user_name: 'local',
      prompt_tokens: 1234,
      completion_tokens: 567,
      total_tokens: 1801,
      request_count: 42,
    },
  ],
}

const MOCK_USAGE_AUTH_USER = {
  ...MOCK_USAGE,
  viewer: { id: 'alice-uuid', name: 'Alice', role: 'user', provider: 'local' },
}

// Two scenarios:
//   1. No-auth single-user box: /api/auth/status returns authEnabled:false
//      and the page must call /api/usage and render the local user's data.
//   2. Auth-on regular user: status returns authEnabled:true and the page
//      keeps using /api/auth/usage as before.
//
// The point of these specs is the "prevent accidental removal" guarantee
// the user asked for: if anyone gates the Usage page behind auth again,
// scenario 1 fails immediately.

test.describe('Usage page — single-user no-auth mode', () => {
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

    // The new no-auth code path. If anyone reverts Usage.jsx to
    // /api/auth/usage in single-user mode, this route is never hit and
    // the test fails because no usage data renders.
    let usageHits = 0
    await page.route('**/api/usage?**', (route) => {
      usageHits++
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_USAGE),
      })
    })
    // The synthetic local user has admin role, so Usage.jsx also pulls
    // the cluster-wide view from /api/usage/all to populate displayTotals.
    await page.route('**/api/usage/all?**', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_USAGE),
      })
    )
    page.usageHits = () => usageHits
  })

  test('Usage entry is visible in sidebar without auth', async ({ page }) => {
    await page.goto('/app')
    const systemSection = page.locator('button.sidebar-section-toggle', { hasText: 'System' })
    await systemSection.click()
    const usageLink = page.locator('a.nav-item[href="/app/usage"]')
    await expect(usageLink).toBeVisible()
  })

  test('navigating to /app/usage renders the dashboard with local-user data', async ({ page }) => {
    await page.goto('/app/usage')

    // The page used to bail with "Usage tracking unavailable" when authEnabled=false.
    // We assert the *opposite*: data is rendered and the empty-state text is absent.
    await expect(page.getByText('Usage tracking unavailable')).toHaveCount(0)

    // The total-tokens stat card is one of the first things rendered after
    // a successful /api/usage call. We assert the formatted number "1.8K"
    // is present (formatNumber in Usage.jsx renders 1801 as "1.8K").
    await expect(page.getByText('1.8K').first()).toBeVisible()
  })
})

test.describe('Usage page — auth on', () => {
  test.beforeEach(async ({ page }) => {
    // RequireAuth redirects to /login when user is null, so the status
    // response must include a resolved user for auth-on specs to reach
    // the Usage page at all.
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          authEnabled: true,
          staticApiKeyRequired: false,
          providers: ['local'],
          user: { id: 'alice-uuid', name: 'Alice', role: 'user', provider: 'local' },
        }),
      })
    )
    await page.route('**/api/auth/me', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          user: { id: 'alice-uuid', name: 'Alice', role: 'user', provider: 'local' },
          permissions: {},
        }),
      })
    )
    await page.route('**/api/auth/usage?**', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_USAGE_AUTH_USER),
      })
    )
    await page.route('**/api/auth/quota', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ quotas: [] }) })
    )
  })

  test('Usage page calls /api/auth/usage when auth is on', async ({ page }) => {
    let authUsageHit = false
    await page.route('**/api/auth/usage?**', (route) => {
      authUsageHit = true
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_USAGE_AUTH_USER),
      })
    })

    await page.goto('/app/usage')
    await expect(page.getByText('1.8K').first()).toBeVisible()
    expect(authUsageHit).toBe(true)
  })
})
