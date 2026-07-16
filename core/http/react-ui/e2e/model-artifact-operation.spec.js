import { test, expect } from './coverage-fixtures.js'

test('operations bar shows managed model acquisition phase and bytes', async ({ page }) => {
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
        isCancelled: false,
        cancellable: true,
        phase: 'downloading',
        currentBytes: 1073741824,
        totalBytes: 4294967296,
      }],
    }),
  }))
  let cancelledPath = ''
  await page.route('**/api/operations/artifact-job-123/cancel', (route) => {
    cancelledPath = new URL(route.request().url()).pathname
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/models')
  const operation = page.locator('.operation-item').filter({ hasText: 'qwen-asr' })
  await expect(operation).toContainText('Downloading model files')
  await expect(operation).toContainText('1 GB / 4 GB')
  await expect(operation.locator('.operation-progress')).toHaveText('45%')
  await expect(operation.locator('.operation-bar')).toHaveAttribute('style', /width: 45%/)

  await operation.getByTitle('Cancel').click()
  expect(cancelledPath).toBe('/api/operations/artifact-job-123/cancel')
})
