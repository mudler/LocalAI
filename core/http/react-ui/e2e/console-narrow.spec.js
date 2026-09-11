import { test, expect } from './coverage-fixtures.js'

test.describe('Operate console on a narrow screen', () => {
  test('expanding the rail leaves the overview on screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/operate')

    const toggle = page.locator('.console-rail-toggle')
    await expect(toggle).toBeVisible()
    await toggle.click()
    await expect(page.locator('.console-rail-groups')).toBeVisible()

    const heading = page.getByRole('heading', { name: 'Overview', exact: true })
    const box = await heading.boundingBox()
    expect(box).not.toBeNull()
    expect(box.y).toBeLessThan(800)
  })

  test('the rail scrolls internally rather than growing without bound', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/operate')
    await page.locator('.console-rail-toggle').click()

    const groups = page.locator('.console-rail-groups')
    await expect(groups).toBeVisible()
    const height = await groups.evaluate(el => el.getBoundingClientRect().height)
    expect(height).toBeLessThan(800)
  })
})

test.describe('Operate headline figures', () => {
  for (const width of [390, 768, 1024]) {
    test(`labels remain legible at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/app/operate')

      const labels = page.locator('.operate-headline dt')
      await expect(labels.first()).toBeVisible()
      const clipped = await labels.evaluateAll(els =>
        els.filter(el => el.scrollWidth > el.clientWidth + 1).map(el => el.textContent))
      expect(clipped).toEqual([])
    })
  }

  test('values remain legible in dark theme', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 950 })
    await page.goto('/app/operate')

    const invisible = await page.locator('.operate-headline__value').evaluateAll(els => els
      .map(el => ({ text: el.textContent, color: getComputedStyle(el).color }))
      .filter(value => value.color === 'rgb(0, 0, 0)'))
    expect(invisible).toEqual([])
  })
})
