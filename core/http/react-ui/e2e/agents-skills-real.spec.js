import { test, expect } from './coverage-fixtures.js'

// Skills against the real test server: write one on the page, find it in the
// list, and delete it.

test('a skill written on the page shows in the list and can be deleted', async ({ page }) => {
  const name = `checklist-${Date.now()}`
  await page.goto('/app/skills')
  test.skip(await page.getByTestId('files-off').isVisible(), 'files are off on this server build')

  await page.goto('/app/skills/new')
  await page.getByLabel(/^Name/).fill(name)
  await page.getByLabel(/^Description/).fill('Go through the release checklist')
  await page.locator('.skilledit-sidebar-item', { hasText: 'Content' }).click()
  await page.getByLabel('Skill content (markdown)').fill('Tick every box.')
  await page.getByRole('button', { name: 'Create skill' }).click()
  await expect(page).toHaveURL(/\/app\/skills$/)

  await page.getByTestId(`skill-row-${name}`).click()
  await expect(page.getByTestId('skill-title')).toHaveText(name)
  await page.getByRole('tab', { name: 'Instructions' }).click()
  await expect(page.getByTestId('skill-content')).toContainText('Tick every box.')

  await page.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByTestId(`skill-row-${name}`)).toHaveCount(0)
})
