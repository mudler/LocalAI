import { test, expect } from './coverage-fixtures.js'

test.describe('Editorial design system', () => {
  test('page titles render in the sans display font (no serif)', async ({ page }) => {
    await page.goto('/app/settings')
    const title = page.locator('.page-title').first()
    await expect(title).toBeVisible({ timeout: 15_000 })
    const family = await title.evaluate(el => getComputedStyle(el).fontFamily)
    // Editorial-grotesk direction: headings use the Geist sans family, no serif.
    expect(family.toLowerCase()).toContain('geist')
    expect(family.toLowerCase()).not.toContain('fraunces')
  })

  test('active nav item is highlighted with a tinted background (no rail)', async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    const active = page.locator('.sidebar-nav .nav-item.active').first()
    await expect(active).toBeVisible()
    const bg = await active.evaluate(el => getComputedStyle(el).backgroundColor)
    // Tint-only active treatment: a non-transparent tinted background.
    expect(bg).not.toBe('rgba(0, 0, 0, 0)')
    expect(bg).not.toBe('transparent')
  })

  test('the current sidebar row carries an accent dot, other rows do not', async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    const dot = (loc) => loc.evaluate((el) => {
      const cs = getComputedStyle(el, '::after')
      return { content: cs.content, width: cs.width, radius: cs.borderTopLeftRadius, bg: cs.backgroundColor }
    })
    const active = await dot(page.locator('.sidebar-nav .nav-item.active').first())
    expect(active.content).not.toBe('none')
    expect(active.width).toBe('6px')
    expect(active.bg).not.toBe('rgba(0, 0, 0, 0)')
    const idle = await dot(page.locator('.sidebar-nav .nav-item:not(.active)').first())
    expect(idle.content).toBe('none')
  })

  test('the collapsed sidebar shows the icon mark and hides the dot', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai_sidebar_collapsed', 'true'))
    await page.goto('/app')
    const sidebar = page.locator('.sidebar.collapsed')
    await expect(sidebar).toBeVisible({ timeout: 15_000 })
    expect((await sidebar.boundingBox()).width).toBe(64)
    await expect(page.locator('.sidebar-logo-icon-img')).toBeVisible()
    await expect(page.locator('.sidebar-logo-img')).toBeHidden()
    const display = await page.locator('.sidebar-nav .nav-item.active').first()
      .evaluate((el) => getComputedStyle(el, '::after').display)
    expect(display).toBe('none')
  })

  test('settings stacks its section rail above the form on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/app/settings')
    const label = page.locator('.set-content .form-row__label').first()
    await expect(label).toBeVisible({ timeout: 15_000 })
    // Beside the rail the label had a few characters of width.
    expect((await label.boundingBox()).width).toBeGreaterThan(200)
    const rail = await page.locator('.set-rail').boundingBox()
    const content = await page.locator('.set-content').boundingBox()
    expect(content.y).toBeGreaterThanOrEqual(rail.y + rail.height - 1)
  })

  test('the settings save button carries no icon class of its own', async ({ page }) => {
    await page.goto('/app/settings')
    const btn = page.locator('.set-head button.btn').first()
    await expect(btn).toBeVisible({ timeout: 15_000 })
    // An icon class on the button itself once put a missing glyph before the
    // label. The icon is a child svg.
    await expect(btn).not.toHaveClass(/\b(fas|far|fab|lai-icon)\b/)
    await expect(btn.locator('svg[data-icon]')).toHaveCount(1)
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

test.describe('Shared UI kit theme', () => {
  const canvas = {
    dark: 'rgb(11, 19, 18)',
    light: 'rgb(242, 245, 245)',
  }

  for (const mode of ['dark', 'light']) {
    test(`the ${mode} canvas reaches the legacy page variables`, async ({ page }) => {
      await page.addInitScript((m) => localStorage.setItem('localai-theme', m), mode)
      await page.goto('/app/settings')
      await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
      const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
      expect(bg).toBe(canvas[mode])
    })
  }

  test('the theme is set before the app bundle runs', async ({ page }) => {
    // Block every script chunk. Only the inline snippet in index.html can set
    // the attribute, so a pass means first paint already has the right theme.
    await page.route('**/assets/**/*.js', (route) => route.abort())
    await page.route('**/assets/*.js', (route) => route.abort())
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'light'))
    await page.goto('/app')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  })

  test('the page reveal is a 250ms fade', async ({ page }) => {
    await page.goto('/app/settings')
    const pt = page.locator('.page-transition').first()
    await expect(pt).toBeVisible({ timeout: 15_000 })
    const duration = await pt.evaluate((el) => getComputedStyle(el).animationDuration)
    expect(duration).toBe('0.25s')
  })

  test('the current sidebar row lifts onto a card', async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 15_000 })
    const active = page.locator('.sidebar-nav .nav-item.active').first()
    const shadow = await active.evaluate((el) => getComputedStyle(el).boxShadow)
    expect(shadow).not.toBe('none')
  })
})
