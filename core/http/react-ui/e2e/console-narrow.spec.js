import { test, expect } from './coverage-fixtures.js'

// Small-screen behaviour of the Operate console and the dashboard stat cards.
//
// Both defects here are about a narrow viewport but neither is only a narrow
// viewport problem: the stat cards were being laid out by the wrong rule at
// every width, and the rail's height was never bounded.

test.describe('Operate console on a narrow screen', () => {
  test('expanding the rail leaves the page still on screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/manage')

    const toggle = page.locator('.console-rail-toggle')
    await expect(toggle).toBeVisible()
    await toggle.click()
    await expect(page.locator('.console-rail-groups')).toBeVisible()

    // Thirteen destinations in one column is taller than a phone. If opening
    // the menu pushes the page's own heading past the fold, the menu has
    // replaced the page instead of annotating it.
    // Manage titles itself with .view-bar__title rather than .page-title.
    const heading = page.locator('.page-title, .view-bar__title').first()
    const box = await heading.boundingBox()
    expect(box).not.toBeNull()
    expect(box.y).toBeLessThan(800)
  })

  test('the rail scrolls internally rather than growing without bound', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/manage')
    await page.locator('.console-rail-toggle').click()

    const groups = page.locator('.console-rail-groups')
    await expect(groups).toBeVisible()
    const height = await groups.evaluate(el => el.getBoundingClientRect().height)
    expect(height).toBeLessThan(800)
  })
})

test.describe('Headline figures', () => {
  // Host used shadowed StatCards; it now shares the Operate overview's hairline
  // figure strip, so the guard is that its labels stay legible, not that it
  // keeps a card gap.
  for (const width of [768, 1024]) {
    test(`Host figure labels are not clipped at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/app/manage')
      const labels = page.locator('.stat-strip__label')
      await expect(labels.first()).toBeVisible()
      const clipped = await labels.evaluateAll(els =>
        els.filter(el => el.scrollWidth > el.clientWidth + 1).map(el => el.textContent))
      expect(clipped).toEqual([])
    })
  }

  test('a Host figure routes into the thing it counts', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1000 })
    await page.goto('/app/manage')
    const cell = page.locator('.stat-strip__cell').first()
    await expect(cell).toBeVisible()
    // A count is worth more when it is also the way to what it counted.
    await expect(cell).toHaveJSProperty('tagName', 'BUTTON')
  })

  test('the figure strip keeps its height inside the flex column', async ({ page }) => {
    // .page--app is a flex column whose split view takes flex:1, so a child
    // with no intrinsic minimum gets shrunk to nothing. This strip did exactly
    // that and rendered 2px tall with four invisible cells.
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/app/manage')
    const strip = page.locator('.manage-summary')
    await expect(strip).toBeVisible()
    const h = await strip.evaluate(el => el.getBoundingClientRect().height)
    expect(h).toBeGreaterThan(40)
  })
})

test.describe('Headline figure contrast', () => {
  test('every figure is legible against the cell it sits on', async ({ page }) => {
    // A <button> does not inherit colour, so a value with no tone rule fell
    // back to the UA's `buttontext` — pure black on the dark ground, invisible.
    await page.setViewportSize({ width: 1440, height: 950 })
    await page.goto('/app/manage')
    const bad = await page.locator('.stat-strip__value').evaluateAll(els => els
      .map(el => ({ text: el.textContent, color: getComputedStyle(el).color }))
      .filter(v => v.color === 'rgb(0, 0, 0)'))
    expect(bad).toEqual([])
  })

  test('the strip keeps its top margin against the shared shorthand', async ({ page }) => {
    // `.stat-strip` declares `margin: 0 0 ...` later in the file, which was
    // silently resetting this element's top margin and leaving it flush
    // against the resources panel above it.
    await page.setViewportSize({ width: 1440, height: 950 })
    await page.goto('/app/manage')
    const top = await page.locator('.manage-summary')
      .evaluate(el => parseFloat(getComputedStyle(el).marginTop))
    expect(top).toBeGreaterThan(12)
  })
})
