import { test, expect } from '@playwright/test'

test.describe('Manage Page - Backend Logs Link', () => {
  test('row action menu exposes Backend logs entry with terminal icon', async ({ page }) => {
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    // Row actions live behind the kebab (ActionMenu) — open the first row's menu.
    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()

    const logsItem = page.getByRole('menuitem', { name: 'Backend logs' })
    await expect(logsItem).toBeVisible()
    await expect(logsItem.locator('i.fa-terminal')).toBeVisible()
  })

  test('Backend logs menu item navigates to backend-logs page', async ({ page }) => {
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()

    const logsItem = page.getByRole('menuitem', { name: 'Backend logs' })
    await expect(logsItem).toBeVisible()
    await logsItem.click()

    await expect(page).toHaveURL(/\/app\/backend-logs\//)
  })
})
