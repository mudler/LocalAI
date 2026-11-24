import { test, expect } from './coverage-fixtures.js'

// The files page against the real test server: the browser, the files API, the
// workspace store and its blob store. Nothing is stubbed, so a pass means a
// user can really keep, find, delete and restore files.

const page_ = (page) => page.getByTestId('workspace-page')
const rows = (page) => page.getByTestId('ws-row')
const rowOf = (page, name) => rows(page).filter({ hasText: name })

async function upload(page, name, content) {
  await page.getByTestId('ws-file-input').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(content) })
}

async function rowAction(page, name, action) {
  await rowOf(page, name).getByRole('button', { name: `More actions for ${name}` }).click()
  await page.getByRole('menuitem', { name: action }).click()
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
    const row = rowOf(page, name)
    await expect(row).toBeVisible()
    await expect(page.getByTestId('ws-upload-row').filter({ hasText: name })).toContainText('Uploaded')

    await row.getByRole('button', { name, exact: true }).click()
    await expect(page.getByTestId('file-preview')).toContainText('the quarterly budget is approved')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('file-preview')).toHaveCount(0)

    const renamed = `plan-${Date.now()}.txt`
    await rowAction(page, name, 'Rename')
    await page.getByRole('textbox', { name: 'Rename' }).fill(renamed)
    await page.getByRole('textbox', { name: 'Rename' }).press('Enter')
    await expect(rowOf(page, renamed)).toBeVisible()
    await expect(rowOf(page, name)).toHaveCount(0)
    await expect(page.getByTestId('ws-toast').filter({ hasText: `Renamed to ${renamed}.` })).toBeVisible()

    await page.getByRole('searchbox', { name: 'Search your files' }).fill('quarterly budget')
    await page.getByRole('searchbox', { name: 'Search your files' }).press('Enter')
    await expect(page.getByTestId('ws-hit').filter({ hasText: renamed }).first()).toBeVisible()
  })

  test('creates a folder, opens it, uploads into it and goes back up', async ({ page }) => {
    const folder = `Reports-${Date.now()}`
    await page.getByRole('button', { name: 'New folder' }).click()
    const box = page.getByRole('textbox', { name: 'Folder name' })
    await expect(box).toHaveValue(/^New folder/)
    await box.fill(folder)
    await box.press('Enter')
    const row = rowOf(page, folder)
    await expect(row).toBeVisible()
    await row.getByRole('button', { name: folder, exact: true }).click()
    await expect(page.getByTestId('ws-empty')).toContainText('This folder is empty')
    await expect(page).toHaveURL(new RegExp(`path=%2F${folder}`))
    await upload(page, 'inside.txt', 'x')
    await expect(rowOf(page, 'inside.txt')).toBeVisible()
    await page.getByTestId('ws-crumbs').getByRole('button', { name: 'Home' }).click()
    await expect(rowOf(page, folder)).toBeVisible()
    // Back returns to the folder.
    await page.goBack()
    await expect(rowOf(page, 'inside.txt')).toBeVisible()
  })

  test('a deleted file goes to the Trash, Undo brings it back, and so does Restore', async ({ page }) => {
    const name = `gone-${Date.now()}.txt`
    await upload(page, name, 'bye')
    await expect(rowOf(page, name)).toBeVisible()

    await rowAction(page, name, 'Move to Trash')
    await expect(rowOf(page, name)).toHaveCount(0)
    const toast = page.getByTestId('ws-toast').filter({ hasText: `${name} moved to the Trash.` })
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(rowOf(page, name)).toBeVisible()

    await rowAction(page, name, 'Move to Trash')
    await expect(rowOf(page, name)).toHaveCount(0)
    await page.getByRole('tab', { name: /Trash/ }).click()
    const trashed = page.getByTestId('ws-trash-row').filter({ hasText: name })
    await expect(trashed).toBeVisible()
    await trashed.getByRole('button', { name: 'Restore' }).click()
    await expect(trashed).toHaveCount(0)

    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(rowOf(page, name)).toBeVisible()
  })

  test('moves a file into a folder with the Move dialog, and Undo moves it back', async ({ page }) => {
    const stamp = Date.now()
    const folder = `Inbox-${stamp}`
    const name = `move-me-${stamp}.txt`
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('textbox', { name: 'Folder name' }).fill(folder)
    await page.getByRole('textbox', { name: 'Folder name' }).press('Enter')
    await upload(page, name, 'move')
    await expect(rowOf(page, name)).toBeVisible()

    await rowAction(page, name, 'Move to…')
    const dialog = page.getByTestId('ws-move')
    await dialog.getByRole('button', { name: folder }).click()
    await dialog.getByRole('button', { name: `Move to ${folder}` }).click()
    await expect(rowOf(page, name)).toHaveCount(0)
    await page.getByTestId('ws-toast').filter({ hasText: `${name} moved to ${folder}.` }).getByRole('button', { name: 'Undo' }).click()
    await expect(rowOf(page, name)).toBeVisible()
  })

  test('shows the progress of a slow upload, cancels it, and sends it again', async ({ page }) => {
    const cdp = await page.context().newCDPSession(page)
    await cdp.send('Network.enable')
    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: 256 * 1024 })
    const name = `big-${Date.now()}.bin`
    await page.getByTestId('ws-file-input').setInputFiles({ name, mimeType: 'application/octet-stream', buffer: Buffer.alloc(3 * 1024 * 1024, 1) })
    const up = page.getByTestId('ws-upload-row').filter({ hasText: name })
    await expect(page.getByTestId('ws-upload-tray')).toContainText('Uploading 1 file')
    await expect(up.getByTestId('ws-upload-line')).toContainText(/[0-9.]+ (KB|MB) of 3\.0 MB/)
    await expect.poll(async () => Number(await up.getByRole('progressbar').getAttribute('value')), { timeout: 10_000 }).toBeGreaterThan(0)

    await up.getByRole('button', { name: `Cancel the upload of ${name}` }).click()
    await expect(up).toContainText('Canceled')
    await expect(rowOf(page, name)).toHaveCount(0)

    await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 })
    await up.getByRole('button', { name: `Try ${name} again` }).click()
    await expect(up).toContainText('Uploaded', { timeout: 15_000 })
    await expect(rowOf(page, name)).toBeVisible()
    await expect(page.getByTestId('ws-upload-live')).toContainText('Upload done: 1 file.')
  })

  test('shows what changed in a folder and lets the user undo it', async ({ page }) => {
    const name = `undo-${Date.now()}.txt`
    await upload(page, name, 'first')
    await expect(rowOf(page, name)).toBeVisible()
    await page.getByRole('tab', { name: 'History' }).click()
    const entry = page.getByTestId('ws-history-row').filter({ hasText: `Upload ${name}` })
    await expect(entry).toBeVisible()
    await entry.getByRole('button', { name: 'Undo' }).click()
    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(rowOf(page, name)).toHaveCount(0)
  })
})
