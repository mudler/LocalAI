import { test, expect } from './coverage-fixtures.js'

test.describe('Scheduling page', () => {
  test('renders at /app/scheduling with rules from the API', async ({ page }) => {
    await page.route('**/api/nodes/scheduling', (route) => {
      route.fulfill({
        status: 200, contentType: 'application/json',
        body: JSON.stringify([{ model_name: 'llama-3.3', spread_all: true, min_replicas: 0, max_replicas: 0 }]),
      })
    })
    await page.goto('/app/scheduling')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    await expect(page).toHaveURL(/\/app\/scheduling$/)
    await expect(page.getByText('llama-3.3')).toBeVisible()
  })
})
