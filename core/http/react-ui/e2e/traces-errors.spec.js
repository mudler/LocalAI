import { test, expect } from '@playwright/test'

test.describe('Traces - Error Display', () => {
  test.beforeEach(async ({ page }) => {
    // Mock API traces with sample data so the table renders
    await page.route('**/api/traces', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify([
          {
            request: { method: 'POST', path: '/v1/chat/completions' },
            response: { status: 200 },
            error: null,
          },
        ]),
      })
    })
    // Mock backend traces with sample data
    await page.route('**/api/backend-traces', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify([
          {
            type: 'model_load',
            timestamp: Date.now() * 1_000_000,
            model_name: 'mock-model',
            summary: 'Loaded model',
            duration: 500_000_000,
            error: null,
          },
        ]),
      })
    })
    await page.goto('/app/traces')
    await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
  })

  test('API traces tab has Result column header', async ({ page }) => {
    // API tab is active by default
    await expect(page.locator('th', { hasText: 'Result' })).toBeVisible()
  })

  test('backend traces tab shows model_load type if present', async ({ page }) => {
    // Switch to backend traces tab
    await page.locator('button', { hasText: 'Backend Traces' }).click()

    // The table should be visible with Type column
    await expect(page.locator('th', { hasText: 'Type' })).toBeVisible()
  })
})
