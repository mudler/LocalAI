import { test, expect } from './coverage-fixtures.js'

test('operations strip shows managed model acquisition phase and bytes', async ({ page }) => {
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

  await page.goto('/app/models')
  const strip = page.locator('.operations-strip')
  await expect(strip.locator('.operations-strip__name')).toHaveText('qwen-asr')
  await expect(strip).toContainText('Downloading')
  await expect(strip).toContainText('1 GB / 4 GB')
  await expect(strip.locator('.operations-strip__pct')).toHaveText('45%')
  await expect(strip.locator('.operations-strip__fill')).toHaveAttribute('style', /width: 45%/)
})
