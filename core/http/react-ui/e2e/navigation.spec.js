import { test, expect } from './coverage-fixtures.js'

test.describe('Navigation', () => {
  test('/ redirects to /app', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/app/)
  })

  test('/app shows the home page', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.locator('.home-page')).toBeVisible()
  })

  test('top menu exposes Home and Models', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app"]')).toBeVisible()
    const models = page.locator('.sidebar-nav a.nav-item[href="/app/models"]')
    await expect(models).toBeVisible()
    await expect(models.locator('.nav-label')).toHaveText('Models')
  })

  test('Create stays an inline tier with Chat, Studio and Talk', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/chat"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/studio"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/talk"]')).toBeVisible()
  })

  test('Build is a single entry that opens the Build console', async ({ page }) => {
    await page.goto('/app')
    const build = page.locator('.sidebar-nav a.nav-item', { hasText: 'Build' })
    await expect(build).toBeVisible()
    await build.click()
    await expect(page.locator('.console-rail .console-rail-header', { hasText: 'Build' })).toBeVisible()
  })

  test('Operate is a single entry that opens the admin console', async ({ page }) => {
    await page.goto('/app')
    const operate = page.locator('.sidebar-nav a.nav-item', { hasText: 'Operate' })
    await expect(operate).toBeVisible()
    await operate.click()
    await expect(page.locator('.console-rail .console-rail-header', { hasText: 'Operate' })).toBeVisible()
  })

  test('Build console groups Automation, Training and Recognition', async ({ page }) => {
    await page.goto('/app/agents')
    const rail = page.locator('.console-rail')
    await expect(rail).toBeVisible()
    for (const group of ['Automation', 'Training', 'Recognition']) {
      await expect(rail.locator('.console-group-title', { hasText: group })).toBeVisible()
    }
    // Recognition (Faces/Voices) and Training (Fine-tune/Quantize) live here now.
    await expect(rail.locator('a.nav-item[href="/app/fine-tune"]')).toBeVisible()
    await expect(rail.locator('a.nav-item[href="/app/face"]')).toBeVisible()
  })

  test('desktop console rail collapses to accessible icons and persists globally', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto('/app/backends')

    const rail = page.locator('.console-rail')
    const collapse = rail.getByRole('button', { name: 'Collapse Operate navigation' })
    await expect(collapse).toBeVisible()
    await collapse.click()
    await expect(rail).toHaveClass(/console-rail--collapsed/)
    await expect(rail).toHaveCSS('width', '60px')
    await expect(rail.getByRole('link', { name: 'Backends', exact: true })).toHaveClass(/active/)
    await expect(rail.getByRole('link', { name: 'Overview', exact: true })).toHaveAttribute('title', 'Overview')
    await expect.poll(() => page.evaluate(() => localStorage.getItem('localai_console_rail_collapsed'))).toBe('true')

    await page.goto('/app/agents')
    const buildRail = page.locator('.console-rail')
    await expect(buildRail).toHaveClass(/console-rail--collapsed/)
    await expect(buildRail.getByRole('button', { name: 'Expand Build navigation' })).toBeVisible()
    await page.reload()
    await expect(page.locator('.console-rail')).toHaveClass(/console-rail--collapsed/)
  })
})
