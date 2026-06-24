import { test, expect } from './coverage-fixtures.js'

// Traces rows carry latency as a shape, not only as a number buried in the
// expanded detail (mock 6d).

const TRACES = [
  { id: '1', timestamp: new Date().toISOString(), duration: 4_200_000_000,
    request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 500 }, error: 'context length exceeded' },
  { id: '2', timestamp: new Date().toISOString(), duration: 980_000_000,
    request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 200 } },
  { id: '3', timestamp: new Date().toISOString(), duration: 186_000_000,
    request: { method: 'POST', path: '/v1/embeddings' }, response: { status: 200 } },
]

test.describe('Traces latency', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/traces**', route => route.fulfill({ json: TRACES }))
    await page.goto('/app/traces')
  })

  test('every row shows a latency bar and figure', async ({ page }) => {
    const cells = page.locator('.lat')
    await expect(cells).toHaveCount(3)
    await expect(cells.first()).toContainText('4.20s')
  })

  test('the bar is scaled against the slowest request in view', async ({ page }) => {
    // Wait for the rows: goto alone does not guarantee the fetch has painted.
    await expect(page.locator('.lat__bar i')).toHaveCount(3)
    const widths = await page.locator('.lat__bar i').evaluateAll(
      els => els.map(el => parseFloat(el.style.width)))
    // 4.2s is the slowest, so it is full; 186ms is a sliver of it.
    expect(widths[0]).toBe(100)
    expect(widths[2]).toBeLessThan(20)
  })

  test('a slow request is marked, not just long', async ({ page }) => {
    await expect(page.locator('.lat')).toHaveCount(3)
    // Colour carries the threshold; the figure carries the value.
    await expect(page.locator('.lat__bar--slow')).toHaveCount(1)
  })
})
