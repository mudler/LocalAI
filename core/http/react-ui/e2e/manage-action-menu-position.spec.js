import { test, expect } from './coverage-fixtures.js'

// Regression: opening a row's kebab (ActionMenu) on /app/manage used to snap
// the page scroll to the top and render the menu detached from its trigger,
// making it impossible to operate. Two causes: the menu auto-focus scrolled
// the page (no preventScroll), and the position:fixed popover was rendered
// inside a row whose hover `transform` re-anchored it. Fix portals the popover
// to document.body, positions it before paint, and focuses without scrolling.
test.describe('Manage Page - Action menu positioning', () => {
  test('opening a row menu keeps scroll stable and places the menu by its trigger', async ({ page }) => {
    // Small viewport so the page is scrollable and a scroll jump is observable.
    await page.setViewportSize({ width: 1024, height: 500 })
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()

    // Bring the trigger into view ourselves first, so the only scroll we then
    // measure is the one the menu would (wrongly) cause - not Playwright's own
    // scroll-into-view before the click.
    await trigger.scrollIntoViewIfNeeded()
    const scrollBefore = await page.evaluate(() => window.scrollY)
    await trigger.click()

    const menu = page.locator('[role="menu"]')
    await expect(menu).toBeVisible()

    // Behavioural symptom 1: focusing the menu must not yank the page scroll.
    const scrollAfter = await page.evaluate(() => window.scrollY)
    expect(scrollAfter).toBe(scrollBefore)

    // Behavioural symptom 2: the menu must sit next to its trigger, not float
    // at the top of the window where it can't be operated.
    const triggerBox = await trigger.boundingBox()
    const menuBox = await menu.boundingBox()
    expect(triggerBox).not.toBeNull()
    expect(menuBox).not.toBeNull()
    // Menu top is within ~24px of the trigger's bottom (below) or above it
    // (flipped) — in all cases it tracks the trigger, never floating at y≈0.
    const tracksTrigger =
      Math.abs(menuBox.y - (triggerBox.y + triggerBox.height)) < 24 ||
      Math.abs((menuBox.y + menuBox.height) - triggerBox.y) < 24
    expect(tracksTrigger).toBe(true)

    // Mechanism: the popover must be portaled to document.body so position:fixed
    // resolves against the viewport, not a transformed ancestor row.
    await expect(page.locator('body > .popover')).toHaveCount(1)
  })
})
