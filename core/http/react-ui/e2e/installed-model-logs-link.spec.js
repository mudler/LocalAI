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

  test('arrow navigation announces the active action through the focused menu', async ({ page }) => {
    await page.goto('/app/models?view=installed')
    await page.locator('[data-testid="installed-models-rail-item"]').first().click()
    const trigger = page.locator('button.action-menu__trigger').first()
    await trigger.focus()
    await trigger.press('Enter')

    const menu = page.getByRole('menu')
    await expect(menu).toBeFocused()
    const firstItem = menu.getByRole('menuitem').first()
    await expect(menu).toHaveAttribute('aria-activedescendant', await firstItem.getAttribute('id'))

    await menu.press('ArrowDown')
    const secondItem = menu.getByRole('menuitem').nth(1)
    await expect(menu).toHaveAttribute('aria-activedescendant', await secondItem.getAttribute('id'))
    await expect(menu).toBeFocused()
    await expect(firstItem).toHaveAttribute('tabindex', '-1')
    await expect(secondItem).toHaveAttribute('tabindex', '-1')
  })
})
