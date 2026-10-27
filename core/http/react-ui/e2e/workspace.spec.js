import { test, expect } from './coverage-fixtures.js'

// The files page against the real test server: the browser, the files API, the
// workspace store and its blob store. Nothing is stubbed, so a pass means a
// user can really keep, find, delete and restore files.

const page_ = (page) => page.getByTestId('workspace-page')
const rows = (page) => page.getByTestId('ws-row')

async function upload(page, name, content) {
  await page.getByTestId('ws-file-input').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(content) })
}

test.describe('Files against the real server', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/files')
    await expect(page_(page)).toBeVisible()
    // Every build has a workspace: a database, or a JSON index when there is none.
    await expect(page.getByTestId('files-off')).toHaveCount(0)
  })

  test('uploads a file, previews it, renames it, and finds it by its text', async ({ page }) => {
    const name = `notes-${Date.now()}.txt`
    await upload(page, name, 'the quarterly budget is approved')
    const row = rows(page).filter({ hasText: name })
    await expect(row).toBeVisible()

    await row.getByRole('button', { name }).click()
    await expect(page.getByRole('complementary', { name: 'Preview' })).toContainText('the quarterly budget is approved')

    const renamed = `plan-${Date.now()}.txt`
    await row.getByRole('button', { name: 'Rename' }).click()
    await page.getByRole('textbox', { name: 'Rename' }).fill(renamed)
    await page.getByRole('textbox', { name: 'Rename' }).press('Enter')
    await expect(rows(page).filter({ hasText: renamed })).toBeVisible()
    await expect(rows(page).filter({ hasText: name })).toHaveCount(0)

    await page.getByRole('searchbox', { name: 'Search your files' }).fill('quarterly budget')
    await page.getByRole('searchbox', { name: 'Search your files' }).press('Enter')
    await expect(page.getByTestId('ws-hit').filter({ hasText: renamed }).first()).toBeVisible()
  })

  test('creates a folder, opens it and goes back up', async ({ page }) => {
    const folder = `Reports-${Date.now()}`
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('textbox', { name: 'Folder name' }).fill(folder)
    await page.getByRole('textbox', { name: 'Folder name' }).press('Enter')
    const row = rows(page).filter({ hasText: folder })
    await expect(row).toBeVisible()
    await row.getByRole('button', { name: `${folder}/` }).click()
    await expect(page.getByText('This folder is empty')).toBeVisible()
    await upload(page, 'inside.txt', 'x')
    await expect(rows(page).filter({ hasText: 'inside.txt' })).toBeVisible()
    await page.getByRole('button', { name: 'Up one folder' }).click()
    await expect(rows(page).filter({ hasText: folder })).toBeVisible()
  })

  test('a deleted file waits in the Trash and comes back when restored', async ({ page }) => {
    const name = `gone-${Date.now()}.txt`
    await upload(page, name, 'bye')
    const row = rows(page).filter({ hasText: name })
    await expect(row).toBeVisible()
    await row.getByRole('button', { name: 'Move to Trash' }).click()
    await expect(rows(page).filter({ hasText: name })).toHaveCount(0)

    await page.getByRole('tab', { name: /Trash/ }).click()
    const trashed = page.getByTestId('ws-trash-row').filter({ hasText: name })
    await expect(trashed).toBeVisible()
    await trashed.getByRole('button', { name: 'Restore' }).click()
    await expect(trashed).toHaveCount(0)

    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(rows(page).filter({ hasText: name })).toBeVisible()
  })

  test('shows what changed in a folder and lets the user undo it', async ({ page }) => {
    const name = `undo-${Date.now()}.txt`
    await upload(page, name, 'first')
    await expect(rows(page).filter({ hasText: name })).toBeVisible()
    await page.getByRole('tab', { name: 'History' }).click()
    const entry = page.getByTestId('ws-history-row').filter({ hasText: `Upload ${name}` })
    await expect(entry).toBeVisible()
    await entry.getByRole('button', { name: 'Undo' }).click()
    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(rows(page).filter({ hasText: name })).toHaveCount(0)
  })
})
