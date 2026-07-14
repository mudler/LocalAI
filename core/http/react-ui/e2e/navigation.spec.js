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

  test('top menu exposes Home and Install Models', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app"]')).toBeVisible()
    await expect(page.locator('.sidebar-nav a.nav-item[href="/app/models"]')).toBeVisible()
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
})
