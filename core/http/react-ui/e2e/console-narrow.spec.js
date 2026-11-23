import { test, expect } from './coverage-fixtures.js'

test.describe('Administration on a narrow screen', () => {
  test('the page does not widen the screen and the sidebar lives in the drawer', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/operate')
    await expect(page.locator('.dk-hubtabs')).toHaveCount(0)
    const pageOverflows = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
    expect(pageOverflows).toBe(false)
    await page.getByRole('button', { name: 'Open menu' }).click()
    await expect(page.locator('[data-testid="admin-nav"]')).toBeVisible()
  })

  test('a page of the last group opens from the drawer and the overview stays on screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await page.goto('/app/operate')
    await page.getByRole('button', { name: 'Open menu' }).click()
    const settings = page.locator('[data-testid="admin-nav"] a[data-admin-item="/app/settings"]')
    await settings.scrollIntoViewIfNeeded()
    await settings.click()
    await expect(page).toHaveURL(/\/app\/settings$/)

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

test.describe('Overview cards on a narrow screen', () => {
  for (const width of [390, 768, 1024]) {
    test(`the headline and the cards stay inside the screen at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/app/operate')
      const headline = page.getByTestId('operate-headline')
      await expect(headline).toBeVisible()
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(over).toBeLessThanOrEqual(0)
      const box = await page.getByTestId('overview-traffic').boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(width)
    })
  }
})
