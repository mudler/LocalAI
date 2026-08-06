import { test, expect } from './coverage-fixtures.js'

test.describe('Theme default', () => {
  test('a fresh install opens dark even when the OS prefers light', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'light' })
    await page.goto('/app')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })

  test('a stored choice still wins', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'light'))
    await page.goto('/app')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  })
})
