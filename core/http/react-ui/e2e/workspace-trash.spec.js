import { test, expect } from './coverage-fixtures.js'

// Deleting for good from the Trash tab of the Files page, against the real
// server: the browser, the files API, the workspace store and its blob store.
// The confirmation is in the page (a dialog element), never a browser dialog.

const page_ = (page) => page.getByTestId('workspace-page')
const trashRows = (page) => page.getByTestId('ws-trash-row')

async function upload(page, name, content) {
  await page.getByTestId('ws-file-input').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(content) })
  await expect(page.getByTestId('ws-row').filter({ hasText: name })).toBeVisible()
}

async function trash(page, name) {
  await page.getByTestId('ws-row').filter({ hasText: name }).getByRole('button', { name: `More actions for ${name}` }).click()
  await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
  await expect(page.getByTestId('ws-row').filter({ hasText: name })).toHaveCount(0)
}

const openTrash = (page) => page.getByRole('tab', { name: /Trash/ }).click()

test.describe('Deleting from the Trash for good', () => {
  test.beforeEach(async ({ page }) => {
    // A native dialog would hang the test: fail it loudly instead.
    page.on('dialog', async (d) => { await d.dismiss(); throw new Error(`browser dialog: ${d.message()}`) })
    await page.goto('/app/files')
    await expect(page_(page)).toBeVisible()
    await expect(page.getByTestId('files-off')).toHaveCount(0)
  })

  test('deletes one item permanently after a confirmation that says what goes', async ({ page }) => {
    const gone = `purge-${Date.now()}.txt`
    const kept = `keep-${Date.now()}.txt`
    await upload(page, gone, '0123456789')
    await upload(page, kept, 'abc')
    await trash(page, gone)
    await trash(page, kept)
    await openTrash(page)

    const row = trashRows(page).filter({ hasText: gone })
    await expect(row).toBeVisible()
    await expect(row.getByTestId('ws-trash-size')).toHaveText('1 file, 10 B')

    await row.getByTestId('ws-purge').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete permanently?')
    await expect(dialog).toContainText('1 file, 10 B')
    await expect(dialog).toContainText('It cannot be undone')
    // Cancel keeps it.
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(row).toBeVisible()

    await row.getByTestId('ws-purge').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete permanently' }).click()
    await expect(row).toHaveCount(0)
    await expect(page.getByTestId('ws-trash-done')).toContainText('Deleted permanently: 1 file, 10 B.')
    // The other item is still there and still restores.
    const other = trashRows(page).filter({ hasText: kept })
    await other.getByRole('button', { name: 'Restore' }).click()
    await expect(other).toHaveCount(0)
    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(page.getByTestId('ws-row').filter({ hasText: kept })).toBeVisible()
  })

  test('empties the whole Trash, counts it first, and then disables the button', async ({ page }) => {
    const stamp = Date.now()
    for (const [i, body] of ['aa', 'bbbb'].entries()) {
      const name = `bulk-${stamp}-${i}.txt`
      await upload(page, name, body)
      await trash(page, name)
    }
    await openTrash(page)
    const empty = page.getByTestId('ws-empty-trash')
    await expect(empty).toBeEnabled()
    await expect(page.getByTestId('ws-trash-summary')).toContainText('In the Trash:')

    await empty.click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Empty the Trash?')
    await expect(dialog).toContainText(/\d+ files?/)
    await dialog.getByRole('button', { name: 'Empty Trash' }).click()

    await expect(trashRows(page)).toHaveCount(0)
    await expect(page.getByTestId('ws-trash-summary')).toHaveText('The Trash is empty.')
    await expect(empty).toBeDisabled()
    await expect(page.getByTestId('ws-trash-done')).toContainText('Deleted permanently:')
    // The count in the tab is gone too, and nothing comes back after a reload.
    await page.reload()
    await openTrash(page)
    await expect(page.getByTestId('ws-trash-summary')).toHaveText('The Trash is empty.')
    await expect(page.getByTestId('ws-empty-trash')).toBeDisabled()
  })

  test('a deleted folder counts its files and folders', async ({ page }) => {
    const folder = `Dir-${Date.now()}`
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('textbox', { name: 'Folder name' }).fill(folder)
    await page.getByRole('textbox', { name: 'Folder name' }).press('Enter')
    await page.getByTestId('ws-row').filter({ hasText: folder }).getByRole('button', { name: folder, exact: true }).click()
    // Upload only once the new folder is open, so the file lands inside it.
    await expect(page.getByTestId('ws-empty')).toContainText('This folder is empty')
    await upload(page, 'inside.txt', '12345')
    await page.getByTestId('ws-crumbs').getByRole('button', { name: 'Home' }).click()
    await trash(page, folder)
    await openTrash(page)
    await expect(trashRows(page).filter({ hasText: folder }).getByTestId('ws-trash-size')).toHaveText('1 file, 1 folder, 5 B')
  })
})
