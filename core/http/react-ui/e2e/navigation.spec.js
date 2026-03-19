import { test, expect } from '@playwright/test'

test.describe('Navigation', () => {
  test('/ redirects to /app', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/app/)
  })

  test('/app shows home page with LocalAI title', async ({ page }) => {
    await page.goto('/app')
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Start a conversation' })).toBeVisible()
  })

  test('sidebar traces link navigates to /app/traces', async ({ page }) => {
    await page.goto('/app')
    const tracesLink = page.locator('a.nav-item[href="/app/traces"]')
    await expect(tracesLink).toBeVisible()
    await tracesLink.click()
    await expect(page).toHaveURL(/\/app\/traces/)
    await expect(page.getByRole('heading', { name: 'Traces', exact: true })).toBeVisible()
  })
})
