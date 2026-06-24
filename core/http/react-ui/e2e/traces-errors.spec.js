import { test, expect } from './coverage-fixtures.js'

test.describe('Traces - Error Display', () => {
  test.beforeEach(async ({ page }) => {
    // Mock API traces with sample data so the table renders
    await page.route('**/api/traces?*', (route) => {
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
    await page.route('**/api/backend-traces?*', (route) => {
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

// Pin the BackendTraceDetail expansion path for a vector_store trace —
// the type that surfaces the router's embedding-cache plumbing. The
// row click triggers the detail render, which exercises typeBadgeStyle
// (with the new vector_store badge color), the DataFields component
// (op / outcome / vector_dim / similarity), and the "View backend
// logs" link that resolves to the store namespace. Without this spec
// the new color entry plus the data-field render branches stay
// uncovered, dragging UI line coverage below the regression gate.
test.describe('Traces - vector_store backend trace detail', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/traces?*', (route) => {
      route.fulfill({ contentType: 'application/json', body: '[]' })
    })
    await page.route('**/api/backend-traces?*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify([
          {
            type: 'vector_store',
            timestamp: '2026-05-28T13:56:25.558Z',
            model_name: 'router-cache-smart-router',
            backend: 'local-store',
            summary: 'search hit (sim=0.989)',
            duration: 160_000_000,
            error: '',
            data: {
              op: 'search',
              outcome: 'hit',
              vector_dim: 768,
              similarity: 0.9899752140045166,
            },
          },
          {
            type: 'vector_store',
            timestamp: '2026-05-28T13:49:07.545Z',
            model_name: 'router-cache-smart-router',
            backend: 'local-store',
            summary: 'search miss',
            duration: 100_000_000,
            error: '',
            data: {
              op: 'search',
              outcome: 'miss',
              vector_dim: 768,
            },
          },
        ]),
      })
    })
    await page.goto('/app/traces')
    await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
    await page.locator('button', { hasText: 'Backend Traces' }).click()
  })

  test('renders type badge and expands data fields on row click', async ({ page }) => {
    // The vector_store badge appears in the type column.
    await expect(page.locator('td span', { hasText: 'vector_store' }).first()).toBeVisible()

    // Clicking the first row expands BackendTraceDetail, which renders
    // the four data fields. Use the first row's "search hit" summary
    // as the anchor to disambiguate from the miss row below.
    await page.locator('tr', { hasText: 'search hit' }).first().click()

    // DataFields renders op/outcome/vector_dim/similarity as label/value pairs.
    // 'hit' appears as the rendered outcome value.
    await expect(page.locator('text=outcome').first()).toBeVisible()
    await expect(page.locator('text=hit').first()).toBeVisible()

    // The model_name → /app/backend-logs link is the BackendTraceDetail
    // affordance for jumping to logs for the store namespace.
    await expect(page.locator('a', { hasText: 'View backend logs' })).toBeVisible()
  })
})
