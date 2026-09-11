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

test('pauses a model download without invoking destructive cancel', async ({ page }) => {
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
      cancellable: true,
      phase: 'downloading',
    }],
  })

  const requests = []
  await page.route('**/api/operations/job-gemma/pause', (route) => {
    requests.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })
  await page.route('**/api/operations/job-gemma/cancel', (route) => {
    requests.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/activity')

  const card = page.locator('.operation-card').filter({ hasText: 'gemma-3-27b-it' })
  await card.locator('.operation-card__pause').click()

  await expect.poll(() => requests).toEqual(['/api/operations/job-gemma/pause'])
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

test('retry dismisses the job it was pressed on, not another sharing its id', async ({ page }) => {
  // /api/operations strips the "node:<id>:" prefix, so a local install and a
  // node-scoped install of one backend arrive with the same id and different
  // jobIDs. Dismissing by id retired whichever came first, which both left the
  // acted-on failure live and silently retired an unrelated one.
  const failed = (over) => ({
    id: 'sherpa-onnx',
    name: 'sherpa-onnx',
    fullName: 'sherpa-onnx',
    progress: 0,
    taskType: 'installation',
    isBackend: true,
    isQueued: false,
    isDeletion: false,
    cancellable: false,
    error: 'no space left on device',
    ...over,
  })
  await stub(page, {
    operations: [
      failed({ jobID: 'job-local' }),
      failed({ jobID: 'job-node', nodeID: 'node-1' }),
    ],
  })

  const calls = []
  await page.route('**/api/operations/*/dismiss', (route) => {
    calls.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })
  await page.route('**/api/nodes/*/backends/install', (route) => {
    calls.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/activity')

  // Nothing on screen tells the two cards apart, which is the point: they
  // share a name and an id, and only the jobID behind each one differs. The
  // node-scoped job is second in the payload, so it is the second card.
  await expect(page.locator('.operation-card')).toHaveCount(2)
  await page.locator('.operation-card').nth(1).locator('.operation-card__retry').click()

  await expect.poll(() => calls).toEqual([
    '/api/operations/job-node/dismiss',
    '/api/nodes/node-1/backends/install',
  ])
})

test('the dismiss control also acts on the job it belongs to', async ({ page }) => {
  // Same hazard as retry: the card's X passed the display id too.
  const failed = (over) => ({
    id: 'sherpa-onnx',
    name: 'sherpa-onnx',
    fullName: 'sherpa-onnx',
    progress: 0,
    taskType: 'installation',
    isBackend: true,
    isQueued: false,
    isDeletion: false,
    cancellable: false,
    error: 'no space left on device',
    ...over,
  })
  await stub(page, {
    operations: [
      failed({ jobID: 'job-local' }),
      failed({ jobID: 'job-node', nodeID: 'node-1' }),
    ],
  })

  const dismissed = []
  await page.route('**/api/operations/*/dismiss', (route) => {
    dismissed.push(new URL(route.request().url()).pathname)
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/activity')

  await expect(page.locator('.operation-card')).toHaveCount(2)
  await page.locator('.operation-card').nth(1).locator('.operation-card__hide').click()

  await expect.poll(() => dismissed).toEqual(['/api/operations/job-node/dismiss'])
})

test('a filter matching nothing does not claim the instance is empty', async ({ page }) => {
  // Three model records on file: telling the user nothing has ever run, while
  // the header counts those same three, is simply false.
  await stub(page, {
    history: [1, 2, 3].map((n) => ({
      id: `model-${n}`,
      name: `model-${n}`,
      jobID: `job-${n}`,
      isBackend: false,
      taskType: 'installation',
      outcome: 'completed',
      startedAt: '2026-07-28T13:40:00Z',
      finishedAt: '2026-07-28T13:40:20Z',
    })),
  })

  await page.goto('/app/activity')
  await expect(page.locator('.activity-row')).toHaveCount(3)

  await page.locator('.activity-chip', { hasText: 'Backends' }).click()

  await expect(page.locator('.activity-empty--filtered')).toBeVisible()
  await expect(page.locator('.activity-empty')).not.toContainText('No operations since startup')
  // And the way back out is on screen.
  await page.locator('.activity-empty--filtered button').click()
  await expect(page.locator('.activity-row')).toHaveCount(3)
})

test('the summary drops a zero count instead of reporting it', async ({ page }) => {
  await stub(page, {
    operations: [{
      id: 'model-a',
      name: 'model-a',
      jobID: 'job-a',
      progress: 40,
      taskType: 'installation',
      isBackend: false,
      isQueued: false,
      isDeletion: false,
      cancellable: true,
    }],
  })

  await page.goto('/app/activity')

  const supporting = page.locator('.page-header__supporting')
  await expect(supporting).toHaveText('1 operation running.')
  await expect(supporting).not.toContainText('0')
})

test('a cancelled deletion reports the cancellation, not a removal', async ({ page }) => {
  await stub(page, {
    history: [{
      id: 'model-a',
      name: 'model-a',
      jobID: 'job-a',
      isBackend: false,
      taskType: 'deletion',
      outcome: 'cancelled',
      startedAt: '2026-07-28T13:40:00Z',
      finishedAt: '2026-07-28T13:40:02Z',
    }],
  })

  await page.goto('/app/activity')

  await expect(page.locator('.activity-row')).toContainText('cancelled')
  await expect(page.locator('.activity-row')).not.toContainText('removed')
})

test('an implausible or zero duration never reaches the row', async ({ page }) => {
  await stub(page, {
    history: [
      {
        id: 'zero-span',
        name: 'zero-span',
        jobID: 'job-zero',
        isBackend: false,
        taskType: 'installation',
        outcome: 'completed',
        // recordTerminal seeds StartedAt = FinishedAt and only overwrites it
        // with a real stamp, so this is an ordinary arrival.
        startedAt: '2026-07-28T13:40:00Z',
        finishedAt: '2026-07-28T13:40:00Z',
      },
      {
        id: 'zero-stamp',
        name: 'zero-stamp',
        jobID: 'job-stamp',
        isBackend: false,
        taskType: 'installation',
        outcome: 'completed',
        startedAt: '0001-01-01T00:00:00Z',
        finishedAt: '2026-07-28T13:41:00Z',
      },
    ],
  })

  await page.goto('/app/activity')

  const zeroSpan = page.locator('.activity-row').filter({ hasText: 'zero-span' })
  await expect(zeroSpan).toContainText('installed in < 1s')

  // A zero-value Go stamp is not a duration. The row says what happened and
  // stops, rather than stating a span of millennia as fact.
  const zeroStamp = page.locator('.activity-row').filter({ hasText: 'zero-stamp' })
  await expect(zeroStamp).toContainText('installed')
  await expect(zeroStamp).not.toContainText('installed in')
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
