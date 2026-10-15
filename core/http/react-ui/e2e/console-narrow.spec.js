import { test, expect } from './coverage-fixtures.js'

test.describe('Operate hub on a narrow screen', () => {
  test('the tab bar scrolls sideways instead of widening the page', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/operate')

    const bar = page.locator('.dk-hubtabs')
    await expect(bar).toBeVisible()
    const box = await bar.boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    const scrolls = await bar.evaluate(el => el.scrollWidth > el.clientWidth)
    expect(scrolls).toBe(true)
    const pageOverflows = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
    expect(pageOverflows).toBe(false)
  })

  test('the current tab can be scrolled into view and the overview stays on screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/settings')

    const current = page.locator('.dk-hubtabs [data-hub-tab="settings"]')
    await current.scrollIntoViewIfNeeded()
    await expect(current).toBeInViewport()
    await expect(current).toHaveAttribute('aria-current', 'page')

    await page.goto('/app/operate')
    const heading = page.getByTestId('operate-headline')
    const box = await heading.boundingBox()
    expect(box).not.toBeNull()
    expect(box.y).toBeLessThan(800)
  })

  test('there is no second navigation rail', async ({ page }) => {
    await page.goto('/app/operate')
    await expect(page.locator('.console-rail')).toHaveCount(0)
    await expect(page.locator('.console-layout')).toHaveCount(0)
  })
})

test.describe('Operate status rows', () => {
  for (const width of [390, 768, 1024]) {
    test(`row names and the headline remain legible at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/app/operate')

      const names = page.locator('.op-row__name')
      await expect(names.first()).toBeVisible()
      await expect(names).toHaveCount(4)
      const clipped = await names.evaluateAll(els =>
        els.filter(el => el.scrollWidth > el.clientWidth + 1).map(el => el.textContent))
      expect(clipped).toEqual([])
      const headline = await page.getByTestId('operate-headline').evaluate(el => el.scrollWidth <= el.clientWidth + 1)
      expect(headline).toBe(true)
    })
  }

  test('values remain legible in dark theme', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'dark'))
    await page.setViewportSize({ width: 390, height: 950 })
    await page.goto('/app/operate')

    const text = page.locator('.op-row__summary, .op-row__name, .op-status__title')
    await expect(text.first()).toBeVisible()
    const invisible = await text.evaluateAll(els => els
      .map(el => ({ text: el.textContent, color: getComputedStyle(el).color }))
      .filter(value => value.color === 'rgb(0, 0, 0)'))
    expect(invisible).toEqual([])
  })
})
