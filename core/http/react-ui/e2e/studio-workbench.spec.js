import { test, expect } from './coverage-fixtures.js'

// The generator workbenches (mock 5b/5c): the control column and the record of
// what the form actually sent.

test.describe('Studio workbench', () => {
  test('the control column is a hairline field stack, not a shadowed card', async ({ page }) => {
    await page.goto('/app/studio/images')
    const controls = page.locator('.media-controls')
    await expect(controls).toBeVisible()
    const style = await controls.evaluate(el => {
      const cs = getComputedStyle(el)
      return { shadow: cs.boxShadow, radius: cs.borderTopLeftRadius }
    })
    expect(style.shadow).toBe('none')
    expect(style.radius).toBe('0px')
  })

  test('fields are separated by a rule and labelled in caps', async ({ page }) => {
    await page.goto('/app/studio/images')
    const label = page.locator('.media-controls .form-label').first()
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
    await page.locator('.media-controls textarea').first().fill('a brass orrery')
    await page.getByRole('button', { name: /generate/i }).click()

    const panel = page.locator('.request-panel')
    await expect(panel).toBeVisible()
    await expect(panel).toContainText('/v1/images/generations')
    await expect(panel).toContainText('a brass orrery')
    await expect(panel.getByRole('button', { name: /curl/i })).toBeVisible()
  })
})
