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

  test('shows an error from a failed call and lets the user dismiss it', async ({ page }) => {
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: { path: '/', entries: [] } })
      return route.fulfill({ status: 500, json: { error: { message: 'boom', code: 500 } } })
    })
    await page.route('**/api/chats/files/content*', route => route.fulfill({ status: 500, json: { error: { message: 'upload failed', code: 500 } } }))
    await page.goto('/app/files')
    await page.getByTestId('ws-file-input').setInputFiles({ name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('x') })
    await expect(page.getByRole('alert')).toContainText('upload failed')
    await page.getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByRole('alert')).toHaveCount(0)
  })
})
