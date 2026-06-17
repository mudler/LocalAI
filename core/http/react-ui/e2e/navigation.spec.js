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

  test('sidebar shows the three intent tiers', async ({ page }) => {
    await page.goto('/app')
    for (const title of ['Create', 'Recognition', 'Build']) {
      await expect(page.locator('.sidebar-section-title', { hasText: title })).toBeVisible()
    }
  })

  test('Recognition tier exposes Faces and Voices', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('a.nav-item[href="/app/face"]')).toBeVisible()
    await expect(page.locator('a.nav-item[href="/app/voice"]')).toBeVisible()
  })

  test('Build tier keeps Fine-tune and Quantize as distinct items', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('a.nav-item[href="/app/fine-tune"]')).toBeVisible()
    await expect(page.locator('a.nav-item[href="/app/quantize"]')).toBeVisible()
  })

  test('Operate is a single entry pointing at the admin console default', async ({ page }) => {
    await page.goto('/app')
    const operate = page.locator('a.nav-item[href="/app/models"]')
    await expect(operate).toBeVisible()
    await operate.click()
    await expect(page).toHaveURL(/\/app\/models/)
  })
})
