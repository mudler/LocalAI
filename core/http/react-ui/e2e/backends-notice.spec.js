import { test, expect } from './coverage-fixtures.js'

// A notice is a hairline with a coloured left edge, not a filled panel. A tint
// makes every notice shout at the weight of an error, which is how notices stop
// being read — and it is the same treatment the Operate overview uses for the
// rows that want a decision.

test('the backends notice is an edge, not a filled card', async ({ page }) => {
  // The upgrade banner is the notice worth pinning, so make one exist.
  await page.route('**/api/backends/upgrades', route => route.fulfill({
    json: { 'llama-cpp': { backend_name: 'llama-cpp', installed_version: '0.9.4', available_version: '0.9.7' } },
  }))
  await page.goto('/app/backends')
  const notice = page.locator('.bk-notice', { hasText: /update/i }).first()
  await expect(notice).toBeVisible()
  const s = await notice.evaluate(el => {
    const cs = getComputedStyle(el)
    return { bg: cs.backgroundColor, left: parseFloat(cs.borderLeftWidth), top: parseFloat(cs.borderTopWidth) }
  })
  expect(s.bg).toMatch(/rgba\(0, 0, 0, 0\)|transparent/)
  expect(s.left).toBeGreaterThanOrEqual(3)
  expect(s.top).toBeLessThanOrEqual(1)
})
