import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { mockAddons } from './addons-fixtures.js'

// Both switches that update things by themselves read "Update ... automatically".
// The owner's add-on switch is translated; it was English text before.

test.describe('Settings: the two automatic update switches', () => {
  test('the backend switch is reworded', async ({ page }) => {
    await mockSettings(page)
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /^Backends and galleries/ }).click()
    await expect(page.locator('[data-field="auto_upgrade_backends"]')).toContainText('Update backends automatically')
    await expect(page.locator('[data-field="auto_upgrade_backends"]')).not.toContainText('Upgrade')
    await expect(page.locator('[data-field="auto_upgrade_addons"]')).toContainText('Update add-ons automatically')
  })

  test('the add-on switch of the owner is reworded and translated', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-language', 'de'))
    await mockSettings(page, { addon_host_settings_authority: 'owner_required' })
    await mockAddons(page)
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /^Backends und Galerien|^Backends/ }).first().click()
    const box = page.getByTestId('owner-upgrade-settings')
    await expect(box).toContainText('Add-ons automatisch aktualisieren')
    await expect(box).toContainText('Eigentümer-Richtlinie')
    await expect(box.getByRole('button', { name: 'Aktualisierungseinstellung anwenden' })).toBeDisabled()
    await expect(box).not.toContainText('Upgrade')
  })

  test('in English the owner switch reads "Update add-ons automatically"', async ({ page }) => {
    await mockSettings(page, { addon_host_settings_authority: 'owner_required' })
    await mockAddons(page)
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /^Backends and galleries/ }).click()
    const box = page.getByTestId('owner-upgrade-settings')
    await expect(box).toContainText('Update add-ons automatically')
    await expect(box.getByRole('button', { name: 'Apply update setting' })).toBeDisabled()
  })
})
