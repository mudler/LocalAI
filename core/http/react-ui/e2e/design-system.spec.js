import { test, expect } from './coverage-fixtures.js'

test.describe('Editorial design system', () => {
  test('page titles render in the Fraunces serif', async ({ page }) => {
    await page.goto('/app/settings')
    const title = page.locator('.page-title').first()
    await expect(title).toBeVisible({ timeout: 15_000 })
    const family = await title.evaluate(el => getComputedStyle(el).fontFamily)
    expect(family.toLowerCase()).toContain('fraunces')
  })

  test('active nav item shows a left accent rail (box-shadow), not just a tint', async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    const active = page.locator('.sidebar-nav .nav-item.active').first()
    await expect(active).toBeVisible()
    const shadow = await active.evaluate(el => getComputedStyle(el).boxShadow)
    expect(shadow).not.toBe('none')
  })

  test('page reveal animation is defined on .page-transition', async ({ page }) => {
    await page.goto('/app/settings')
    const pt = page.locator('.page-transition').first()
    await expect(pt).toBeVisible({ timeout: 15_000 })
    const name = await pt.evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('pageReveal')
  })
})

test.describe('reduced motion', () => {
  test('stagger animation-delay is neutralized under reduced motion', async ({ page }) => {
    // Emulate prefers-reduced-motion explicitly. (The fixture-option form
    // test.use({ reducedMotion }) does not propagate through our extended
    // coverage `page` fixture, so set it on the page directly.)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto('/app') // Home renders .reveal-stagger children
    // .home-status-line is staggerStyle(1) -> 60ms delay without the fix.
    const child = page.locator('.home-status-line').first()
    await expect(child).toBeVisible({ timeout: 15_000 })
    const delay = await child.evaluate(el => getComputedStyle(el).animationDelay)
    // Under reduced motion the per-child delay must be ~0 (not 60ms+).
    expect(parseFloat(delay)).toBeLessThan(0.05)
  })
})
