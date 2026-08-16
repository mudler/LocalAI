import { test, expect } from './coverage-fixtures.js'

// Regression: opening the installed model detail menu must not move the page
// or detach the fixed-position menu from its trigger.
test('the installed model action menu stays beside its trigger', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 500 })
  await page.goto('/app/models?view=installed')
  await page.locator('[data-testid="installed-models-rail-item"]').first().click()

  const trigger = page.locator('button.action-menu__trigger').first()
  await expect(trigger).toBeVisible()
  await trigger.scrollIntoViewIfNeeded()
  const scrollBefore = await page.evaluate(() => window.scrollY)
  await trigger.click()

  const menu = page.locator('[role="menu"]')
  await expect(menu).toBeVisible()
  expect(await page.evaluate(() => window.scrollY)).toBe(scrollBefore)

  const triggerBox = await trigger.boundingBox()
  const menuBox = await menu.boundingBox()
  expect(triggerBox).not.toBeNull()
  expect(menuBox).not.toBeNull()
  const tracksTrigger =
    Math.abs(menuBox.y - (triggerBox.y + triggerBox.height)) < 24 ||
    Math.abs((menuBox.y + menuBox.height) - triggerBox.y) < 24
  expect(tracksTrigger).toBe(true)
  await expect(page.locator('body > .popover')).toHaveCount(1)
})
