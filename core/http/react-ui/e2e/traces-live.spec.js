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

test('shows a running backend trace and exposes its logs immediately', async ({ page }) => {
  await page.route('**/api/traces?*', route => route.fulfill({ json: [] }))
  await page.route('**/api/backend-traces?*', route => route.fulfill({
    json: [{
      id: 'backend-running-1',
      timestamp: '2026-08-05T02:00:00Z',
      duration: 2_000_000_000,
      status: 'running',
      type: 'llm',
      model_name: 'slow-model',
      backend: 'llama-cpp',
      summary: 'generating a reply',
    }],
    headers: { 'X-Total-Count': '1' },
  }))
  await page.route('**/api/backend-traces/backend-running-1', route => route.fulfill({
    json: {
      id: 'backend-running-1', status: 'running', type: 'llm',
      timestamp: '2026-08-05T02:00:00Z', duration: 2_000_000_000,
      model_name: 'slow-model', backend: 'llama-cpp', summary: 'generating a reply',
    },
  }))

  await page.goto('/app/traces?tab=backend')
  await page.getByRole('button', { name: /Backend Traces/ }).click()
  const row = page.locator('tbody tr').filter({ hasText: 'generating a reply' })
  await expect(row.locator('[title="Running"]')).toBeVisible()
  await row.click()
  await expect(page.getByRole('link', { name: 'View backend logs' })).toHaveAttribute('href', /\/app\/backend-logs\/slow-model/)
  await expect(page.getByText(/running/, { exact: false })).toBeVisible()
})

// Regression for #11376: switching from Backend Traces back to API Traces
// used to crash the page. `traces` holds whichever list was fetched last, so
// right after `setActiveTab('api')` — before the refetch effect lands — the
// API table renders the previous tab's backend rows, which carry no
// `response` envelope. The status column must tolerate that instead of
// dereferencing `trace.response.status` and tearing down the React tree.
test('switching from backend to API traces with a response-less row does not crash', async ({ page }) => {
  const pageErrors = []
  page.on('pageerror', (e) => pageErrors.push(e.message))

  await page.route('**/api/traces?*', route => route.fulfill({
    json: [{
      id: 'api-1',
      timestamp: '2026-08-05T02:00:00Z',
      request: { method: 'POST', path: '/v1/chat/completions' },
      response: { status: 200 },
    }],
    headers: { 'X-Total-Count': '1' },
  }))
  await page.route('**/api/backend-traces?*', route => route.fulfill({
    json: [{
      id: 'backend-1',
      type: 'llm',
      timestamp: '2026-08-05T02:00:00Z',
      model_name: 'mock-model',
      summary: 'generated a reply',
    }],
    headers: { 'X-Total-Count': '1' },
  }))

  await page.goto('/app/traces')
  await expect(page.locator('tbody tr').filter({ hasText: '/v1/chat/completions' })).toBeVisible()

  await page.getByRole('button', { name: /Backend Traces/ }).click()
  await expect(page.locator('tbody tr').filter({ hasText: 'generated a reply' })).toBeVisible()

  await page.getByRole('button', { name: /API Traces/ }).click()
  // The stale backend row renders in the API table for one frame; the status
  // column falls back to a neutral placeholder rather than throwing.
  await expect(page.locator('tbody tr').filter({ hasText: '/v1/chat/completions' })).toBeVisible()
  expect(pageErrors).toEqual([])
})
