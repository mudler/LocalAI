import { test, expect } from '@playwright/test'

test.describe('Backend Logs', () => {
  test('model detail page shows title', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('.page-title')).toContainText('mock-model')
  })

  test('no back arrow link on detail page', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('a[href="/app/backend-logs"]')).not.toBeVisible()
  })

  test('filter buttons are visible', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('button', { hasText: 'All' })).toBeVisible()
    await expect(page.locator('button', { hasText: 'stdout' })).toBeVisible()
    await expect(page.locator('button', { hasText: 'stderr' })).toBeVisible()
  })

  test('filter buttons toggle active state', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')

    const allBtn = page.locator('button', { hasText: 'All' })
    const stdoutBtn = page.locator('button', { hasText: 'stdout' })

    // All is active by default
    await expect(allBtn).toHaveClass(/btn-primary/)

    // Click stdout
    await stdoutBtn.click()
    await expect(stdoutBtn).toHaveClass(/btn-primary/)
    await expect(allBtn).not.toHaveClass(/btn-primary/)
  })

  test('export button is present', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('button', { hasText: 'Export' })).toBeVisible()
  })

  test('auto-scroll checkbox is present', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('text=Auto-scroll')).toBeVisible()
  })

  test('clear button is present', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')
    await expect(page.locator('button', { hasText: 'Clear' })).toBeVisible()
  })

  test('details toggle button is present and toggles', async ({ page }) => {
    await page.goto('/app/backend-logs/mock-model')

    // "Text only" button visible by default (details are shown)
    const toggleBtn = page.locator('button', { hasText: 'Text only' })
    await expect(toggleBtn).toBeVisible()

    // Click to hide details
    await toggleBtn.click()

    // Button label changes to "Show details"
    await expect(page.locator('button', { hasText: 'Show details' })).toBeVisible()
  })
})
