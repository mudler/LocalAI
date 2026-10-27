import { test, expect } from './coverage-fixtures.js'

// Skills against the real test server: write one on the page, see it in a
// chat, switch it off, and find it still off after a reload.

const composer = (page) => page.getByRole('textbox', { name: 'Message' })

async function offerMockModel(page) {
  await page.route('**/api/models/capabilities', route =>
    route.fulfill({ json: { data: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }] } }))
}

test('a skill written on the page shows in a chat, and its switch is remembered', async ({ page }) => {
  const name = `checklist-${Date.now()}`
  await page.goto('/app/chats/skills')
  test.skip(await page.getByTestId('files-off').isVisible(), 'files are off on this server build')

  await page.getByRole('button', { name: 'New skill' }).click()
  await page.getByRole('textbox', { name: 'Name' }).fill(name)
  await page.getByRole('textbox', { name: 'Description' }).fill('Go through the release checklist')
  await page.getByRole('textbox', { name: 'Instructions' }).fill('Tick every box.')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByTestId('skill-row').filter({ hasText: name })).toBeVisible()

  await offerMockModel(page)
  await page.goto('/app/chats')
  await composer(page).fill('hello')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
  await page.getByText('Skills (', { exact: false }).first().click()
  const box = page.getByRole('checkbox', { name })
  await expect(box).toBeChecked()
  await box.uncheck()
  await expect(box).not.toBeChecked()

  await page.reload()
  await page.getByText('Skills (', { exact: false }).first().click()
  await expect(page.getByRole('checkbox', { name })).not.toBeChecked()

  // The skill can be deleted from its page, and goes to the Trash.
  await page.goto('/app/chats/skills')
  // Deleting asks first: Cancel keeps the skill.
  const row = page.getByTestId('skill-row').filter({ hasText: name })
  await row.getByRole('button', { name: 'Delete' }).click()
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toContainText(`Delete ${name}?`)
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(row).toHaveCount(1)
  await row.getByRole('button', { name: 'Delete' }).click()
  await dialog.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)
})

test('a skill delete the server refuses shows its reason and keeps the skill', async ({ page }) => {
  await page.route('**/api/chats/skills*', route => (route.request().method() === 'DELETE'
    ? route.fulfill({ status: 500, json: { error: { message: 'the skill folder is read-only' } } })
    : route.fulfill({ json: { skills: [{ name: 'review', layer: 'personal', description: 'Check a change', path: '/a', enabled: true, editable: true }], notes: [] } })))
  await page.route(/\/api\/chats\/skills\/[^?]+/, route => route.fulfill({ status: 500, json: { error: { message: 'the skill folder is read-only' } } }))
  await page.goto('/app/chats/skills')
  const row = page.getByTestId('skill-row').filter({ hasText: 'review' })
  await row.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByRole('alert')).toContainText('the skill folder is read-only')
  await expect(row).toHaveCount(1)
})
