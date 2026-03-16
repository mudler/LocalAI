import { test, expect } from '@playwright/test'

test.describe('Traces Settings', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/traces')
    // Wait for settings panel to load
    await expect(page.locator('text=Tracing is')).toBeVisible({ timeout: 10_000 })
  })

  test('settings panel is visible on page load', async ({ page }) => {
    await expect(page.locator('text=Tracing is')).toBeVisible()
  })

  test('expand and collapse settings', async ({ page }) => {
    // The test server starts with tracing enabled, so the panel starts collapsed
    const settingsHeader = page.locator('button', { hasText: 'Tracing is' })

    // Click to expand
    await settingsHeader.click()
    await expect(page.locator('text=Enable Tracing')).toBeVisible()

    // Click to collapse
    await settingsHeader.click()
    await expect(page.locator('text=Enable Tracing')).not.toBeVisible()
  })

  test('toggle tracing on and off', async ({ page }) => {
    // Expand settings
    const settingsHeader = page.locator('button', { hasText: 'Tracing is' })
    await settingsHeader.click()
    await expect(page.locator('text=Enable Tracing')).toBeVisible()

    // The Toggle component is a <label> wrapping a hidden checkbox.
    // Target the checkbox within the settings panel.
    const checkbox = page.locator('input[type="checkbox"]')

    // Initially enabled (server starts with tracing on)
    await expect(checkbox).toBeChecked()

    // Click the label (parent) to toggle off
    await checkbox.locator('..').click()
    await expect(checkbox).not.toBeChecked()

    // Click again to re-enable
    await checkbox.locator('..').click()
    await expect(checkbox).toBeChecked()
  })

  test('set max items value', async ({ page }) => {
    // Expand settings
    await page.locator('button', { hasText: 'Tracing is' }).click()
    await expect(page.locator('text=Enable Tracing')).toBeVisible()

    const maxItemsInput = page.locator('input[type="number"]')
    await maxItemsInput.fill('500')
    await expect(maxItemsInput).toHaveValue('500')
  })

  test('save shows toast', async ({ page }) => {
    // Expand settings
    await page.locator('button', { hasText: 'Tracing is' }).click()

    // Click save
    await page.locator('button', { hasText: 'Save' }).click()

    // Verify toast appears
    await expect(page.locator('text=Tracing settings saved')).toBeVisible({ timeout: 5_000 })
  })

  test('panel collapses after save when tracing is enabled', async ({ page }) => {
    // Expand settings
    await page.locator('button', { hasText: 'Tracing is' }).click()
    await expect(page.locator('text=Enable Tracing')).toBeVisible()

    // Tracing is already enabled; save
    await page.locator('button', { hasText: 'Save' }).click()

    // Panel should collapse
    await expect(page.locator('text=Enable Tracing')).not.toBeVisible()
  })

  test('panel stays expanded after save when tracing is off', async ({ page }) => {
    // Expand settings
    await page.locator('button', { hasText: 'Tracing is' }).click()
    await expect(page.locator('text=Enable Tracing')).toBeVisible()

    // Toggle tracing off
    await page.locator('input[type="checkbox"]').locator('..').click()

    // Save
    await page.locator('button', { hasText: 'Save' }).click()

    // Panel should stay expanded since tracing is now disabled
    await expect(page.locator('text=Enable Tracing')).toBeVisible()
  })
})
