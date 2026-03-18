import { test, expect } from '@playwright/test'

test.describe('Settings - Backend Logging', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/settings')
    // Wait for settings to load
    await expect(page.locator('h3', { hasText: 'Tracing' })).toBeVisible({ timeout: 10_000 })
  })

  test('backend logging toggle is visible in tracing section', async ({ page }) => {
    await expect(page.locator('text=Enable Backend Logging')).toBeVisible()
  })

  test('backend logging toggle can be toggled', async ({ page }) => {
    // Find the checkbox associated with backend logging
    const section = page.locator('div', { has: page.locator('text=Enable Backend Logging') })
    const checkbox = section.locator('input[type="checkbox"]').last()

    // Toggle on
    const wasChecked = await checkbox.isChecked()
    await checkbox.locator('..').click()
    if (wasChecked) {
      await expect(checkbox).not.toBeChecked()
    } else {
      await expect(checkbox).toBeChecked()
    }
  })

  test('save shows toast', async ({ page }) => {
    // Click save button
    await page.locator('button', { hasText: 'Save' }).click()

    // Verify toast appears
    await expect(page.locator('text=Settings saved')).toBeVisible({ timeout: 5_000 })
  })
})
