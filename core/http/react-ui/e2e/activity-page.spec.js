import { test, expect } from './coverage-fixtures.js'

const stub = (page, { operations = [], history = [] } = {}) => Promise.all([
  page.route('**/api/operations', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ operations }),
  })),
  page.route('**/api/operations/history', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ operations: history }),
  })),
])

test('lists live operations and cancels one from a labelled button', async ({ page }) => {
  await stub(page, {
    operations: [{
      id: 'gemma-3-27b-it',
      name: 'gemma-3-27b-it',
      jobID: 'job-gemma',
      progress: 22,
      taskType: 'installation',
      isBackend: false,
      isQueued: false,
      isDeletion: false,
      isCancelled: false,
      cancellable: true,
      phase: 'downloading',
    }],
  })

  let cancelledPath = ''
  await page.route('**/api/operations/job-gemma/cancel', (route) => {
    cancelledPath = new URL(route.request().url()).pathname
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/activity')

  const card = page.locator('.operation-card').filter({ hasText: 'gemma-3-27b-it' })
  await expect(card).toBeVisible()
  await expect(card).toContainText('22%')

  await card.locator('.operation-card__cancel').click()
  expect(cancelledPath).toBe('/api/operations/job-gemma/cancel')
})

test('separates an unacknowledged failure from the record', async ({ page }) => {
  await stub(page, {
    operations: [{
      id: 'sherpa-onnx',
      name: 'sherpa-onnx',
      jobID: 'job-sherpa',
      progress: 0,
      taskType: 'installation',
      isBackend: true,
      isQueued: false,
      isDeletion: false,
      isCancelled: false,
      cancellable: false,
      error: 'no space left on device',
    }],
    history: [{
      id: 'bark-cpp',
      name: 'bark-cpp',
      jobID: 'job-bark',
      isBackend: true,
      taskType: 'installation',
      outcome: 'failed',
      error: 'checksum mismatch',
      startedAt: '2026-07-28T13:40:00Z',
      finishedAt: '2026-07-28T13:41:00Z',
    }],
  })

  await page.goto('/app/activity')

  // Live and unacknowledged: a card that needs a decision.
  await expect(page.locator('.operation-card--error')).toContainText('sherpa-onnx')
  // Dismissed earlier: a row in the record.
  await expect(page.locator('.activity-row')).toContainText('bark-cpp')
})

test('a failure never appears in both In progress and Needs attention', async ({ page }) => {
  // Section membership has to be unambiguous: the same job showing twice makes
  // the two failure paths (retry / dismiss) impossible to reason about.
  await stub(page, {
    operations: [
      {
        id: 'model-a',
        name: 'model-a',
        jobID: 'job-a',
        progress: 40,
        taskType: 'installation',
        isBackend: false,
        isQueued: false,
        isDeletion: false,
        isCancelled: false,
        cancellable: true,
      },
      {
        id: 'sherpa-onnx',
        name: 'sherpa-onnx',
        jobID: 'job-sherpa',
        progress: 0,
        taskType: 'installation',
        isBackend: true,
        isQueued: false,
        isDeletion: false,
        isCancelled: false,
        cancellable: false,
        error: 'no space left on device',
      },
    ],
  })

  await page.goto('/app/activity')

  await expect(page.locator('.operation-card')).toHaveCount(2)
  await expect(page.locator('.operation-card').filter({ hasText: 'sherpa-onnx' })).toHaveCount(1)
})

test('retrying a failed backend install dismisses it before reinstalling', async ({ page }) => {
  // Order is load-bearing: a bare reinstall overwrites the opcache entry
  // without going through recordTerminal, so the failure would never reach the
  // record.
  await stub(page, {
    operations: [{
      id: 'sherpa-onnx',
      name: 'sherpa-onnx',
      fullName: 'localai@sherpa-onnx',
      jobID: 'job-sherpa',
      progress: 0,
      taskType: 'installation',
      isBackend: true,
      isQueued: false,
      isDeletion: false,
      isCancelled: false,
      cancellable: false,
      error: 'no space left on device',
    }],
  })

  const calls = []
  await page.route('**/api/operations/job-sherpa/dismiss', (route) => {
    calls.push('dismiss')
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })
  await page.route('**/api/backends/install/**', (route) => {
    calls.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/activity')

  await page.locator('.operation-card__retry').click()

  await expect.poll(() => calls).toEqual(['dismiss', '/api/backends/install/localai@sherpa-onnx'])
})

test('a failed removal offers no retry, because retry only means install', async ({ page }) => {
  await stub(page, {
    operations: [{
      id: 'model-a',
      name: 'model-a',
      fullName: 'model-a',
      jobID: 'job-a',
      progress: 0,
      taskType: 'deletion',
      isBackend: false,
      isQueued: false,
      isDeletion: true,
      isCancelled: false,
      cancellable: false,
      error: 'file is busy',
    }],
  })

  await page.goto('/app/activity')

  await expect(page.locator('.operation-card--error')).toBeVisible()
  await expect(page.locator('.operation-card__retry')).toHaveCount(0)
  // And it must not claim an install was attempted.
  await expect(page.locator('.operation-card--error')).not.toContainText('install')
})

test('filters the record down to backends', async ({ page }) => {
  await stub(page, {
    history: [
      {
        id: 'gemma-3-27b-it',
        name: 'gemma-3-27b-it',
        jobID: 'job-gemma',
        isBackend: false,
        taskType: 'installation',
        outcome: 'completed',
        startedAt: '2026-07-28T13:40:00Z',
        finishedAt: '2026-07-28T13:41:30Z',
      },
      {
        id: 'bark-cpp',
        name: 'bark-cpp',
        jobID: 'job-bark',
        isBackend: true,
        taskType: 'installation',
        outcome: 'completed',
        startedAt: '2026-07-28T13:40:00Z',
        finishedAt: '2026-07-28T13:40:20Z',
      },
    ],
  })

  await page.goto('/app/activity')
  await expect(page.locator('.activity-row')).toHaveCount(2)

  await page.locator('.activity-chip', { hasText: 'Backends' }).click()

  await expect(page.locator('.activity-row')).toHaveCount(1)
  await expect(page.locator('.activity-row')).toContainText('bark-cpp')
})

test('shows the empty state when nothing has run', async ({ page }) => {
  await stub(page)

  await page.goto('/app/activity')

  await expect(page.locator('.page-title')).toBeVisible()
  await expect(page.locator('.activity-empty')).toBeVisible()
})
