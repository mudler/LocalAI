import { test, expect } from './coverage-fixtures.js'

// The backend-logging, download and gallery settings, now under their intent
// groups. The page waits for edits to be applied, so saving is Apply.
test.describe('Settings - Backend Logging', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible({ timeout: 10_000 })
    await page.getByRole('button', { name: /^Debugging and traces/ }).click()
  })

  test('backend logging toggle is visible in the debugging group', async ({ page }) => {
    await expect(page.locator('text=Enable Backend Logging')).toBeVisible()
  })

  test('artifact download concurrency is configurable', async ({ page }) => {
    await page.getByRole('button', { name: /^Speed and defaults/ }).click()
    const input = page.getByLabel('Artifact Download Concurrency')
    await expect(input).toBeVisible()
    await input.fill('4')
    await expect(input).toHaveValue('4')
  })

  test('persistent VRAM cache can be toggled', async ({ page }) => {
    await page.getByRole('button', { name: /^Backends and galleries/ }).click()
    const toggle = page.locator('.st-row', { hasText: 'Persist remote VRAM estimates' }).getByRole('switch')
    await expect(toggle).toBeVisible()
    const before = await toggle.getAttribute('aria-checked')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', before === 'true' ? 'false' : 'true')
  })

  test('gallery startup loading and pre-warming can be toggled together', async ({ page }) => {
    await page.getByRole('button', { name: /^Backends and galleries/ }).click()
    const toggle = page.locator('.st-row', { hasText: 'Load and pre-warm galleries on boot' }).getByRole('switch')
    await expect(toggle).toBeVisible()
    const before = await toggle.getAttribute('aria-checked')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', before === 'true' ? 'false' : 'true')
  })

  test('backend logging toggle can be toggled', async ({ page }) => {
    const toggle = page.locator('.st-row', { hasText: 'Enable Backend Logging' }).getByRole('switch')
    const before = await toggle.getAttribute('aria-checked')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', before === 'true' ? 'false' : 'true')
  })

  test('apply shows toast', async ({ page }) => {
    // The bar appears with the first edit and its Apply button saves.
    await page.locator('.st-row', { hasText: 'Enable Backend Logging' }).getByRole('switch').click()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.locator('text=Settings saved successfully')).toBeVisible({ timeout: 5_000 })
  })
})
