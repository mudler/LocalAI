import { test, expect } from './coverage-fixtures.js'

// Login page (src/pages/Login.jsx). /login redirects to /app when auth is
// disabled, so the harness mocks /api/auth/status to enable auth with no
// signed-in user. With no password/OAuth providers configured, the page
// offers API-token login — the path exercised here.
test.describe('Login page', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          authEnabled: true,
          staticApiKeyRequired: false,
          user: null,
          hasUsers: true,
          providers: [],
          registrationMode: 'open',
          permissions: {},
        }),
      }),
    )
    await page.goto('/login')
  })

  test('renders the API token login option', async ({ page }) => {
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByRole('button', { name: /Login with API Token/i })).toBeVisible()
  })

  test('reveals and accepts an API token', async ({ page }) => {
    await page.getByRole('button', { name: /Login with API Token/i }).click()
    const tokenInput = page.locator('input').first()
    await expect(tokenInput).toBeVisible()
    await tokenInput.fill('sk-test-token')
    await expect(tokenInput).toHaveValue('sk-test-token')
  })
})
