import { test, expect } from './coverage-fixtures.js'

// Pin the API-trace metadata surface (issues #10886 / #10887): the API
// traces table exposes the requesting user in a dedicated column, and the
// expanded row detail lists User, Client IP and User Agent so an operator
// can tell who/what issued each request.
test.describe('Traces - API request metadata', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/traces?*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: 'm1',
            request: { method: 'POST', path: '/v1/chat/completions' },
            response: { status: 200 },
            user_id: 'user-123',
            user_name: 'alice',
            client_ip: '203.0.113.7',
            user_agent: 'curl/8.4.0',
          },
        ]),
      })
    })
    await page.route('**/api/traces/m1', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'm1',
          request: { method: 'POST', path: '/v1/chat/completions' },
          response: { status: 200 },
          user_id: 'user-123',
          user_name: 'alice',
          client_ip: '203.0.113.7',
          user_agent: 'curl/8.4.0',
        }),
      })
    })
    await page.route('**/api/backend-traces?*', (route) => {
      route.fulfill({ contentType: 'application/json', body: '[]' })
    })
    await page.goto('/app/traces')
    await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
  })

  test('shows the User column value in the API traces table', async ({ page }) => {
    await expect(page.locator('th', { hasText: 'User' })).toBeVisible()
    await expect(page.locator('td', { hasText: 'alice' }).first()).toBeVisible()
  })

  test('opens the trace to show Client IP and User Agent', async ({ page }) => {
    await page.getByRole('link', { name: '/v1/chat/completions' }).click()
    await expect(page).toHaveURL(/\/app\/traces\/m1$/)

    await expect(page.locator('text=Client IP').first()).toBeVisible()
    await expect(page.locator('text=203.0.113.7').first()).toBeVisible()
    await expect(page.locator('text=User Agent').first()).toBeVisible()
    await expect(page.locator('text=curl/8.4.0').first()).toBeVisible()
  })
})
