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

// The row menu has the surface of the home model menu: the same radius and
// float shadow, rows with the small radius and the hover wash.
test('the action menu uses the shared menu surface', async ({ page }) => {
  await page.goto('/app/models?view=installed')
  await page.locator('[data-testid="installed-models-rail-item"]').first().click()
  const trigger = page.locator('button.action-menu__trigger').first()
  await trigger.click()
  const surface = page.locator('body > .popover')
  await expect(surface.locator('[role="menu"]')).toBeVisible()
  const look = await surface.evaluate((el) => {
    const cs = getComputedStyle(el)
    const item = getComputedStyle(el.querySelector('.action-menu__item'))
    const root = getComputedStyle(document.documentElement)
    return {
      radius: cs.borderTopLeftRadius,
      shadow: cs.boxShadow,
      itemRadius: item.borderTopLeftRadius,
      rLg: root.getPropertyValue('--r-lg').trim(),
      rSm: root.getPropertyValue('--r-sm').trim(),
    }
  })
  expect(look.radius).toBe(look.rLg)
  expect(look.itemRadius).toBe(look.rSm)
  expect(look.shadow).not.toBe('none')
  await page.keyboard.press('Escape')
  await expect(surface).toHaveCount(0)
  await expect(trigger).toBeFocused()
})
