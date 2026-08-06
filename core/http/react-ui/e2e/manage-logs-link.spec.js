import { test, expect } from './coverage-fixtures.js'

test.describe('Manage Page - Backend Logs Link', () => {
  test('the pane action menu exposes Backend logs with a terminal icon', async ({ page }) => {
    await page.goto('/app/manage')
    // Actions moved out of the row and into the pane, so reaching them is now a
    // selection followed by the pane's kebab.
    await page.locator('[data-testid="host-rail-item"]').first().click()
    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()

    const logsItem = page.getByRole('menuitem', { name: 'Backend logs' })
    await expect(logsItem).toBeVisible()
    await expect(logsItem.locator('i.fa-terminal')).toBeVisible()
  })

  test('Backend logs menu item navigates to backend-logs page', async ({ page }) => {
    await page.goto('/app/manage')
    await page.locator('[data-testid="host-rail-item"]').first().click()
    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()

    const logsItem = page.getByRole('menuitem', { name: 'Backend logs' })
    await expect(logsItem).toBeVisible()
    await logsItem.click()

    await expect(page).toHaveURL(/\/app\/backend-logs\//)
  })
})
