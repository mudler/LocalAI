import { test, expect } from './coverage-fixtures.js'

test.describe('Installed model backend logs link', () => {
  test('the detail action menu exposes Backend logs with a terminal icon', async ({ page }) => {
    await page.goto('/app/models?view=installed')
    await page.locator('[data-testid="installed-models-rail-item"]').first().click()
    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()

    const logsItem = page.getByRole('menuitem', { name: 'Backend logs' })
    await expect(logsItem).toBeVisible()
    await expect(logsItem.locator('i.fa-terminal')).toBeVisible()
  })

  test('Backend logs navigates to the selected model logs', async ({ page }) => {
    await page.goto('/app/models?view=installed')
    await page.locator('[data-testid="installed-models-rail-item"]').first().click()
    await page.locator('button.action-menu__trigger').first().click()
    await page.getByRole('menuitem', { name: 'Backend logs' }).click()

    await expect(page).toHaveURL(/\/app\/backend-logs\//)
  })
})
