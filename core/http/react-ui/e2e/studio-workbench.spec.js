import { test, expect } from './coverage-fixtures.js'

// The generator workbenches (mock 5b/5c): the control column and the record of
// what the form actually sent.

test.describe('Studio workbench', () => {
  test('the compose card is a flat hairline card, not a shadowed one', async ({ page }) => {
    await page.goto('/app/studio/images')
    const card = page.locator('[data-testid="ws-compose"]')
    await expect(card).toBeVisible()
    const shadow = await card.evaluate(el => getComputedStyle(el).boxShadow)
    // Only the one-pixel inset edge: no drop shadow.
    expect(shadow).toMatch(/inset/)
    expect(shadow.replace(/\([^)]*\)/g, '')).not.toContain(',')
  })

  test('fields are labelled in caps', async ({ page }) => {
    await page.goto('/app/studio/images')
    await page.getByRole('button', { name: /Advanced Settings/ }).click()
    const label = page.locator('[data-testid="ws-compose"] .ws-label').first()
    await expect(label).toBeVisible()
    const cs = await label.evaluate(el => getComputedStyle(el).textTransform)
    expect(cs).toBe('uppercase')
  })

  test('no request is shown before one has been made', async ({ page }) => {
    // A panel describing a request nobody sent is a tutorial, not a record.
    await page.goto('/app/studio/images')
    await expect(page.locator('.request-panel')).toHaveCount(0)
  })

  test('generating records the request that was actually sent', async ({ page }) => {
    await page.route('**/api/models/capabilities', route =>
      route.fulfill({ json: { data: [{ id: 'flux-mock', capabilities: ['FLAG_IMAGE'] }] } }))
    await page.route('**/v1/images/generations', route =>
      route.fulfill({ json: { data: [{ url: 'https://example.invalid/a.png' }] } }))

    await page.goto('/app/studio/images')
    await page.locator('[data-testid="ws-compose"] textarea').first().fill('a brass orrery')
    await page.getByRole('button', { name: /generate/i }).click()

    const panel = page.locator('.request-panel')
    await expect(panel).toBeVisible()
    await expect(panel).toContainText('/v1/images/generations')
    await expect(panel).toContainText('a brass orrery')
    await expect(panel.getByRole('button', { name: /curl/i })).toBeVisible()
  })
})
