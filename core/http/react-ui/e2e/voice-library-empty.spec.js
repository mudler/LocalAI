import { test, expect } from './coverage-fixtures.js'

// The empty voice library must offer its action, visibly, inside the panel.

test.describe('Voice library empty state', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/voice-profiles', route => route.fulfill({ json: [] }))
    await page.goto('/app/voice-library')
  })

  test('the create action is visible and a normal size', async ({ page }) => {
    const action = page.locator('.empty-state__actions a.btn').first()
    await expect(action).toBeVisible()
    const box = await action.boundingBox()
    // It had been carrying the panel's own min-height:430px, which made it an
    // invisible box that pushed itself out of view.
    expect(box.height).toBeLessThan(80)
  })

  test('the action sits inside the panel, not past its edge', async ({ page }) => {
    const panel = page.locator('.empty-state').first()
    const action = page.locator('.empty-state__actions a.btn').first()
    const [p, a] = [await panel.boundingBox(), await action.boundingBox()]
    expect(a.y + a.height).toBeLessThanOrEqual(p.y + p.height + 1)
  })

  test('the panel renders its icon', async ({ page }) => {
    const icon = page.locator('.empty-state-icon').first()
    await expect(icon).toBeVisible()
    const box = await icon.boundingBox()
    expect(box.width).toBeGreaterThan(0)
  })
})
