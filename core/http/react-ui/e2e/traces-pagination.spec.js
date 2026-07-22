import { test, expect } from './coverage-fixtures.js'

// The trace list endpoints return a bounded page with the heavy request /
// response bodies stripped; the full record is fetched per trace when a row
// is expanded. Without this the admin UI polled a multi-megabyte JSON blob
// every few seconds. These specs pin both halves of the contract from the
// browser's side: the page asks for a bounded list, and expanding a row
// issues the per-trace detail request whose payload is what gets rendered.

const LIST_BODY = [
  {
    id: '7',
    request: { method: 'POST', path: '/v1/chat/completions', body: null },
    response: { status: 200, body: null, body_bytes: 65536 },
  },
]

const DETAIL_BODY = {
  id: '7',
  request: {
    method: 'POST',
    path: '/v1/chat/completions',
    // "hello from the request body" base64-encoded, matching the wire shape.
    body: Buffer.from('hello from the request body').toString('base64'),
  },
  response: {
    status: 200,
    body: Buffer.from('hello from the response body').toString('base64'),
    body_bytes: 65536,
  },
  client_ip: '203.0.113.9',
}

test.describe('Traces - bounded list and on-demand detail', () => {
  let listUrls = []

  test.beforeEach(async ({ page }) => {
    listUrls = []
    await page.route('**/api/traces?*', (route) => {
      listUrls.push(route.request().url())
      route.fulfill({
        contentType: 'application/json',
        headers: { 'X-Total-Count': '842' },
        body: JSON.stringify(LIST_BODY),
      })
    })
    await page.route('**/api/traces/7', (route) => {
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(DETAIL_BODY) })
    })
    await page.route('**/api/backend-traces?*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        headers: { 'X-Total-Count': '0' },
        body: '[]',
      })
    })
    await page.goto('/app/traces')
    await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
  })

  test('requests a bounded page rather than the whole buffer', async () => {
    expect(listUrls.length).toBeGreaterThan(0)
    expect(listUrls[0]).toContain('limit=')
    expect(listUrls[0]).not.toContain('limit=0')
  })

  test('reports the server-side total, not the page length', async ({ page }) => {
    // The tab counter reflects X-Total-Count (842 buffered) even though only
    // one entry was returned in the page.
    await expect(page.locator('button', { hasText: 'API Traces' })).toContainText('842')
  })

  test('fetches the full record when a row is expanded', async ({ page }) => {
    await page.locator('tr', { hasText: '/v1/chat/completions' }).first().click()

    // The bodies live only in the detail response, so seeing them proves the
    // per-trace fetch happened and its payload is what gets rendered.
    await expect(page.locator('text=hello from the request body')).toBeVisible()
    await expect(page.locator('text=hello from the response body')).toBeVisible()
    await expect(page.locator('text=203.0.113.9').first()).toBeVisible()
  })
})
