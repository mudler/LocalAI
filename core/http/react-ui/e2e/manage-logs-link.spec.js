import { test, expect } from '@playwright/test'

test.describe('Manage Page - Backend Logs Link', () => {
  test('models table shows terminal icon for logs', async ({ page }) => {
    await page.goto('/app/manage')
    // Wait for models to load
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    // Check for terminal icon (backend logs link)
    const terminalIcon = page.locator('a[title="Backend logs"] i.fa-terminal')
    await expect(terminalIcon.first()).toBeVisible()
  })

  test('terminal icon links to backend-logs page', async ({ page }) => {
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    const logsLink = page.locator('a[title="Backend logs"]').first()
    await expect(logsLink).toBeVisible()

    // Link uses href="#" with onClick for navigation
    const href = await logsLink.getAttribute('href')
    expect(href).toBe('#')

    // Click and verify navigation
    await logsLink.click()
    await expect(page).toHaveURL(/\/app\/backend-logs\//)
  })
})
