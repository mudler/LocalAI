import { test, expect } from './coverage-fixtures.js'

test('marks an API trace with no response status as in progress', async ({ page }) => {
  await page.route('**/api/traces?*', route => route.fulfill({
    json: [{
      id: 'running-1',
      timestamp: '2026-08-05T02:00:00Z',
      duration: 2_000_000_000,
      request: { method: 'POST', path: '/v1/chat/completions' },
      response: { status: 0 },
    }],
    headers: { 'X-Total-Count': '1' },
  }))
  await page.route('**/api/backend-traces?*', route => route.fulfill({ json: [] }))

  await page.goto('/app/traces')

  const row = page.locator('tbody tr').filter({ hasText: '/v1/chat/completions' })
  await expect(row.getByText('Running', { exact: true })).toBeVisible()
  await expect(row.locator('[title="In progress"]')).toBeVisible()
  await expect(row.locator('.fa-check-circle')).toHaveCount(0)
})
