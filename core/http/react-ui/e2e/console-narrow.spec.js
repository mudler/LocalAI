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

test.describe('Dashboard stat cards', () => {
  // Two components both claimed `.stat-grid`: the dashboard card strip and the
  // detail-pane StatGrid added with the split views. The later rule won, so the
  // cards were laid out on 120px columns with a 1px gap meant for something
  // else, and their labels were clipped.
  for (const width of [768, 1024]) {
    test(`labels are not clipped at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/app/manage')
      const labels = page.locator('.stat-card__label')
      await expect(labels.first()).toBeVisible()

      const clipped = await labels.evaluateAll(els =>
        els.filter(el => el.scrollWidth > el.clientWidth + 1).map(el => el.textContent))
      expect(clipped).toEqual([])
    })
  }

  test('cards keep the card gap, not the hairline gap of the detail pane grid', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 1000 })
    await page.goto('/app/manage')
    const strip = page.locator('.manage-summary')
    await expect(strip).toBeVisible()
    const gap = await strip.evaluate(el => parseFloat(getComputedStyle(el).columnGap))
    // 1px is the detail-pane StatGrid's hairline; the card strip wants real space.
    expect(gap).toBeGreaterThan(4)
  })
})
