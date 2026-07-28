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
  //
  // The swap is driven by the test rather than by a poll count: a count would
  // race the click, and a click landing on the failure would take the dismiss
  // path and fail this test for an unrelated reason.
  let swapped = false
  await page.route('**/api/operations', (route) => {
    const operations = swapped
      ? [op({ id: 'sherpa-onnx', name: 'sherpa-onnx', jobID: 'job-f', error: 'no space left on device' })]
      : [op({ id: 'model-a', name: 'model-a', jobID: 'job-a' })]
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ operations }) })
  })

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip__name')).toHaveText('model-a')

  await page.locator('.operations-strip__hide').click()
  await expect(page.locator('.operations-strip')).toHaveCount(0)

  // The poller swaps in a different job, which must re-render the strip.
  swapped = true
  await expect(page.locator('.operations-strip__name')).toHaveText('sherpa-onnx', { timeout: 10_000 })
})

test('hiding a running operation does not silence that same job failing', async ({ page }) => {
  // A job keeps its jobID when it fails, so keying the hidden state on the job
  // alone would let a user mute the very failure they need to see.
  let failing = false
  await page.route('**/api/operations', (route) => {
    const operations = failing
      ? [op({ error: 'no space left on device' })]
      : [op()]
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ operations }) })
  })

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip__name')).toHaveText('model-a')

  await page.locator('.operations-strip__hide').click()
  await expect(page.locator('.operations-strip')).toHaveCount(0)

  failing = true
  await expect(page.locator('.operations-strip')).toContainText('no space left on device', { timeout: 10_000 })
})

test('a long error message does not widen the page', async ({ page }) => {
  // An install error is arbitrarily long text, and the strip sits above every
  // page: if it cannot shrink, every page under it gets a horizontal scrollbar.
  await stubOperations(page, [op({ error: `disk write failed: ${'x'.repeat(180)}` })])

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/app/models')
  await expect(page.locator('.operations-strip')).toBeVisible()

  const widths = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }))
  expect(widths.scroll).toBeLessThanOrEqual(widths.client)
})

test('progress is exposed to assistive tech as a named progressbar', async ({ page }) => {
  // The percentage text is aria-hidden so the live region stops re-announcing
  // the strip once a second; the value has to reach assistive tech some other
  // way, and a progressbar is read on demand rather than announced.
  await stubOperations(page, [op({ progress: 45 })])

  await page.goto('/app/models')
  const bar = page.locator('.operations-strip__track')
  await expect(bar).toHaveAttribute('role', 'progressbar')
  await expect(bar).toHaveAttribute('aria-valuenow', '45')
  await expect(bar).toHaveAttribute('aria-valuemin', '0')
  await expect(bar).toHaveAttribute('aria-valuemax', '100')
  await expect(bar).toHaveAttribute('aria-label', /model-a/)
})

test('an operation waiting for the worker says queued, not installing', async ({ page }) => {
  // The real payload for an admitted-but-unstarted op: phase "queued", no
  // progress. It used to arrive with isQueued false, so the one state the
  // strip has a clock icon for never appeared and a queued install claimed to
  // be running.
  await stubOperations(page, [op({ isQueued: true, phase: 'queued', progress: 0 })])

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip')).toContainText('Queued')
  await expect(page.locator('.operations-strip')).not.toContainText('Installing')
  await expect(page.locator('.operations-strip__pct')).toHaveCount(0)
})

test('cancelling the last operation does not announce it as installed', async ({ page }) => {
  // Cancelling deletes the operation server side, so the strip sees exactly
  // what it sees when an install finishes: the operation stops being listed.
  // The completion hold used to take that for success and put a green
  // "Installed model model-a" on screen for four seconds after the user
  // called it off.
  let cancelled = false
  await page.route('**/api/operations', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ operations: cancelled ? [] : [op()] }),
  }))
  await page.route('**/api/operations/history', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ operations: [] }),
  }))
  await page.route('**/api/operations/job-a/cancel', (route) => {
    cancelled = true
    return route.fulfill({ contentType: 'application/json', body: '{"success":true}' })
  })

  // Cancel is on the card, never on the strip, so the page is where the user
  // does this. The strip sits above it the whole time.
  await page.goto('/app/activity')
  await expect(page.locator('.operations-strip')).toContainText('Installing model')

  await page.locator('.operation-card__cancel').click()

  // The poll that empties the page is the same render that would raise the
  // completion hold, so the strip has to be counted the instant the card
  // goes. A retrying assertion would simply wait the four-second hold out and
  // pass on the regression it is here to catch.
  await expect(page.locator('.operation-card')).toHaveCount(0)
  expect(await page.locator('.operations-strip').count()).toBe(0)

  // And it must not turn up a moment later either.
  await page.waitForTimeout(1500)
  expect(await page.locator('.operations-strip').count()).toBe(0)
})

test('a completed removal does not announce an install', async ({ page }) => {
  // The API drops an operation the instant it finishes, so the completion
  // phrase is rendered from the operation the strip was already holding.
  let removed = false
  await page.route('**/api/operations', (route) => {
    const operations = removed ? [] : [op({ isDeletion: true, progress: 0 })]
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ operations }) })
  })

  await page.goto('/app/models')
  await expect(page.locator('.operations-strip')).toContainText('Removing model')

  removed = true
  await expect(page.locator('.operations-strip')).toContainText('Removed model', { timeout: 10_000 })
  await expect(page.locator('.operations-strip')).not.toContainText('Installed')
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
