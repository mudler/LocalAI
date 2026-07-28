import { test, expect } from './coverage-fixtures.js'

const op = (over = {}) => ({
  id: 'model-a',
  name: 'model-a',
  fullName: 'model-a',
  jobID: 'job-a',
  progress: 40,
  taskType: 'installation',
  isDeletion: false,
  isBackend: false,
  isQueued: false,
  isCancelled: false,
  cancellable: true,
  ...over,
})

const stubOperations = (page, operations) =>
  page.route('**/api/operations', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ operations }),
  }))

test('renders exactly one row for four concurrent operations', async ({ page }) => {
  await stubOperations(page, [
    op({ id: 'model-a', name: 'model-a', jobID: 'job-a', progress: 10 }),
    op({ id: 'model-b', name: 'model-b', jobID: 'job-b', progress: 40 }),
    op({ id: 'model-c', name: 'model-c', jobID: 'job-c', progress: 70 }),
    op({ id: 'model-d', name: 'model-d', jobID: 'job-d', progress: 90 }),
  ])

  await page.goto('/app/models')

  // One row, never four. The stacked bar is what this replaces.
  await expect(page.locator('.operations-strip')).toHaveCount(1)
  // The API sorts by progress ascending, so the least advanced op leads.
  await expect(page.locator('.operations-strip__name')).toHaveText('model-a')
  await expect(page.locator('.operations-strip__more')).toContainText('3')
  await expect(page.locator('.operations-strip__more')).toHaveAttribute('href', /\/app\/activity$/)
})

test('a failure takes the strip over a running install', async ({ page }) => {
  await stubOperations(page, [
    op({ id: 'model-a', name: 'model-a', jobID: 'job-a', progress: 10 }),
    op({ id: 'sherpa-onnx', name: 'sherpa-onnx', jobID: 'job-f', isBackend: true, error: 'no space left on device' }),
  ])

  await page.goto('/app/models')

  await expect(page.locator('.operations-strip__name')).toHaveText('sherpa-onnx')
  await expect(page.locator('.operations-strip')).toContainText('no space left on device')
})

test('a hidden strip comes back when a different operation becomes primary', async ({ page }) => {
  // Hiding must not be able to silence a later failure, so the hidden state is
  // keyed by job rather than being a blanket mute.
  let poll = 0
  await page.route('**/api/operations', (route) => {
    poll += 1
    const operations = poll <= 2
      ? [op({ id: 'model-a', name: 'model-a', jobID: 'job-a' })]
      : [op({ id: 'sherpa-onnx', name: 'sherpa-onnx', jobID: 'job-f', error: 'no space left on device' })]
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ operations }) })
  })

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip__name')).toHaveText('model-a')

  await page.locator('.operations-strip__hide').click()
  await expect(page.locator('.operations-strip')).toHaveCount(0)

  // The poller swaps in a different job, which must re-render the strip.
  await expect(page.locator('.operations-strip__name')).toHaveText('sherpa-onnx', { timeout: 10_000 })
})

test('the hide button hides the strip without cancelling', async ({ page }) => {
  await stubOperations(page, [op()])

  let cancelCalled = false
  await page.route('**/api/operations/*/cancel', (route) => {
    cancelCalled = true
    return route.fulfill({ contentType: 'application/json', body: '{}' })
  })

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip')).toBeVisible()

  await page.locator('.operations-strip__hide').click()

  await expect(page.locator('.operations-strip')).toHaveCount(0)
  expect(cancelCalled).toBe(false)
})
