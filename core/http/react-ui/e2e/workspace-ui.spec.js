import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The files page with the API stubbed, for the states a real server does not
// produce on demand: files off, an empty folder, a failed call.

test.describe('Files page with a stubbed API', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('says why files are off, and offers nothing to do', async ({ page }) => {
    await page.route(/\/api\/chats\/files(\/trash)?(\?.*)?$/, route => route.fulfill({
      status: 409, json: { error: { type: 'files_off', message: 'There is no data path, so files are off.', code: 409 } },
    }))
    await page.goto('/app/files')
    await expect(page.getByTestId('files-off')).toContainText('There is no data path')
    await expect(page.getByRole('button', { name: 'Upload' })).toHaveCount(0)
  })

  test('an upload that fails says why in the tray, and Try again sends it again', async ({ page }) => {
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => route.fulfill({ json: { path: '/', entries: [] } }))
    let puts = 0
    await page.route('**/api/chats/files/content*', route => {
      puts++
      if (puts === 1) return route.fulfill({ status: 500, json: { error: { message: 'disk full', code: 500 } } })
      return route.fulfill({ json: { path: '/a.txt', name: 'a.txt', kind: 'file', size: 1, version: 1, rev: 2 } })
    })
    await page.goto('/app/files')
    await page.getByTestId('ws-file-input').setInputFiles({ name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('x') })
    const row = page.getByTestId('ws-upload-row').filter({ hasText: 'a.txt' })
    await expect(row).toContainText('The server could not save it. The server said: disk full.')
    await expect(page.getByTestId('ws-upload-tray')).toContainText('1 upload failed')
    await expect(page.getByTestId('ws-upload-live')).toContainText('Could not upload a.txt.')
    await row.getByRole('button', { name: 'Try a.txt again' }).click()
    await expect(row).toContainText('Uploaded')
    expect(puts).toBe(2)
  })

  test('a failed action shows its error until it is dismissed', async ({ page }) => {
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: { path: '/', entries: [{ path: '/a.txt', name: 'a.txt', kind: 'file', size: 1, version: 1 }] } })
      return route.fulfill({ status: 500, json: { error: { message: 'boom', code: 500 } } })
    })
    await page.goto('/app/files')
    await page.getByTestId('ws-row').filter({ hasText: 'a.txt' }).getByRole('button', { name: 'More actions for a.txt' }).click()
    await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
    await expect(page.getByRole('alert')).toContainText('boom')
    await page.getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('a folder that cannot be read says so and offers Try again', async ({ page }) => {
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    let lists = 0
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
      lists++
      if (lists === 1) return route.fulfill({ status: 500, json: { error: { message: 'database locked', code: 500 } } })
      return route.fulfill({ json: { path: '/', entries: [{ path: '/a.txt', name: 'a.txt', kind: 'file', size: 1, version: 1 }] } })
    })
    await page.goto('/app/files')
    const error = page.getByTestId('ws-load-error')
    await expect(error).toContainText('Could not open Home.')
    await expect(error).toContainText('database locked')
    await error.getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('ws-row').filter({ hasText: 'a.txt' })).toBeVisible()
  })

  test('shows the host preview of a file no add-on previews, and no add-on frame', async ({ page }) => {
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => route.fulfill({ json: { path: '/', entries: [
      { path: '/plan.docx', name: 'plan.docx', kind: 'file', size: 2048, version: 1 },
    ] } }))
    await page.goto('/app/files')
    await page.getByTestId('ws-row').filter({ hasText: 'plan.docx' }).getByRole('button', { name: 'plan.docx', exact: true }).click()
    const preview = page.getByTestId('file-preview')
    await expect(preview).toContainText('plan.docx')
    await expect(preview.getByTestId('ws-preview-meta')).toContainText('Document · 2.0 KB')
    await expect(page.getByTestId('addon-preview')).toHaveCount(0)
    await expect(preview.getByRole('link', { name: 'Download' })).toBeVisible()
    await preview.getByRole('button', { name: 'Close' }).click()
    await expect(preview).toHaveCount(0)
  })
})
