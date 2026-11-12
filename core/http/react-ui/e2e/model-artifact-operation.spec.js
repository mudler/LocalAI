import { test, expect } from './coverage-fixtures.js'
import { inboxButton, makeInbox, stubInbox } from './inbox-fixtures.js'

test('the Activity tab shows managed model acquisition phase and bytes', async ({ page }) => {
  await stubInbox(page, makeInbox(0))
  await page.route('**/api/operations', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      operations: [{
        id: 'qwen-asr',
        name: 'qwen-asr',
        fullName: 'qwen-asr',
        jobID: 'artifact-job-123',
        progress: 45,
        taskType: 'installation',
        isDeletion: false,
        isBackend: false,
        isQueued: false,
        cancellable: true,
        phase: 'downloading',
        currentBytes: 1073741824,
        totalBytes: 4294967296,
      }],
    }),
  }))

  await page.goto('/app/models')
  await inboxButton(page).click()
  const row = page.getByRole('dialog', { name: 'Notifications' }).getByTestId('inbox-op')
  await expect(row).toContainText('Installing qwen-asr')
  await expect(row).toContainText('Downloading')
  await expect(row).toContainText('1 GB / 4 GB')
  await expect(row).toContainText('45%')
  await expect(row.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '45')
  await expect(row.locator('.nx-op__fill')).toHaveAttribute('style', /--op-w: 45%/)
})
