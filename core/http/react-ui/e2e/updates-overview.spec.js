import { test, expect } from './coverage-fixtures.js'
import { mockOperate, installedBackend } from './operate-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'
import {
  addonRecord, failedOp, galleryItem, heldGrant, libraryItem, minutesAgo, mockUpdates, okSource, runningOp, snapshotOf,
  updateAvailable, upToDate,
} from './updates-fixtures.js'

// The Updates page: what is out of date, what is changing, and whether the
// answer can be trusted. The API is stubbed; the page, the shell provider and
// the rules are the real ones.

const rowOf = (page, name) => page.locator(`[data-testid="update-row"][data-name="${name}"]`)

// The design's mixed case: one failed, one held, one updating, two ready, three current.
async function mixed(page, over = {}) {
  await mockOperate(page)
  const state = await mockUpdates(page, {
    snapshot: snapshotOf(
      [
        galleryItem('whisper', '1.7.2', '1.8.0'),
        galleryItem('llama-cpp', '0.9.4', '0.9.7', { nodes: [{ nodeId: 'a' }, { nodeId: 'b' }, { nodeId: 'c' }], driftNodes: 2 }),
        libraryItem('piper', '1.2.0', '1.3.1'),
      ],
      { sources: { gallery: okSource(12), library: { state: 'failed', error: 'library not reachable', stale: true, checkedAt: minutesAgo(12), lastSuccessAt: minutesAgo(1100) } } },
    ),
    operations: [failedOp('whisper'), runningOp('piper')],
    installed: [installedBackend('llama-cpp', '0.9.4'), installedBackend('whisper', '1.7.2'), installedBackend('kokoro', '0.3.1'), installedBackend('rerankers', '1.0')],
    addons: [
      addonRecord('notes', 'Notes sync', updateAvailable('0.8.2'), { version: '0.8.1' }),
      addonRecord('tables', 'Tables', updateAvailable('2.4.0'), { version: '2.3.0' }),
      addonRecord('mail', 'Mail connector', upToDate()),
    ],
    grants: { revision: 1, addons: [heldGrant('tables', 'Tables', '2.4.0')] },
    ...over,
  })
  return state
}

test.describe('Updates page: states', () => {
  test('shows every state of the design in its group, in order', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    await expect(page.getByRole('heading', { level: 1, name: 'Updates' })).toBeVisible()
    await expect(page.getByTestId('updates-headline')).toHaveText('5 updates available')
    await expect(page.getByTestId('updates-breakdown')).toContainText('2 ready, 1 updating, 2 need you')

    // Groups are h2, "Needs you" first so a failure is never below the fold.
    const headings = await page.locator('h2.uc-group__title, h2.uc-current__text').allInnerTexts()
    expect(headings.map(h => h.replace(/\s+/g, ' ').trim().replace(/ \d+$/, ''))).toEqual(['Needs you', 'Updating', 'Ready to update', expect.stringContaining('up to date')])

    const failed = rowOf(page, 'whisper')
    await expect(failed).toHaveAttribute('data-state', 'failed')
    await expect(failed.getByTestId('update-row-chip')).toContainText('Failed')
    await expect(failed).toContainText('download interrupted')
    await expect(failed.getByRole('button', { name: 'Retry whisper' })).toBeVisible()

    const held = rowOf(page, 'Tables')
    await expect(held).toHaveAttribute('data-state', 'held')
    await expect(held.getByTestId('update-row-chip')).toContainText('Held for approval')
    await expect(held.getByRole('button', { name: 'Review the update of Tables' })).toBeVisible()
    await expect(held.locator('input[type="checkbox"]')).toHaveCount(0)

    const updating = rowOf(page, 'piper')
    await expect(updating).toHaveAttribute('data-state', 'updating')
    await expect(updating.getByTestId('update-row-chip')).toContainText('Updating')
    await expect(updating.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '62')
    await expect(updating.getByRole('button', { name: 'Cancel the update of piper' })).toBeVisible()
    await expect(updating).toContainText('Library backend')

    const ready = rowOf(page, 'llama-cpp')
    await expect(ready).toHaveAttribute('data-state', 'ready')
    await expect(ready.getByTestId('update-row-chip')).toContainText('Update available')
    await expect(ready).toContainText('v0.9.4 to v0.9.7')
    await expect(ready).toContainText('3 nodes, 2 differ')
    await expect(ready.getByRole('button', { name: 'Update llama-cpp' })).toBeVisible()
    // No dead button: the per-node view ships later.
    await expect(page.getByRole('button', { name: /^Nodes$/ })).toHaveCount(0)
    await expect(page.getByRole('link', { name: /^Nodes$/ })).toHaveCount(0)

    const addon = rowOf(page, 'Notes sync')
    await expect(addon).toContainText('Add-on')
    await expect(addon).toContainText('Needs your confirmation in this browser')
  })

  test('the chip of an optional update is the accent tone, a failure is not', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    await expect(rowOf(page, 'llama-cpp').getByTestId('update-row-chip')).toHaveClass(/ui-chip--accent/)
    await expect(rowOf(page, 'Tables').getByTestId('update-row-chip')).toHaveClass(/ui-chip--warn/)
    await expect(rowOf(page, 'whisper').getByTestId('update-row-chip')).toHaveClass(/ui-chip--error/)
  })

  test('a failed source says what is missing and still lists the last good result', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    const banner = page.getByTestId('updates-source-failed-library')
    await expect(banner).toBeVisible()
    await expect(banner).toHaveAttribute('role', 'alert')
    await expect(banner).toContainText('library backends may have updates that are not shown')
    await expect(banner).toContainText('library not reachable')
    await expect(banner).toContainText('last good check')
    await expect(banner.getByRole('button', { name: 'Check again' })).toBeVisible()
    // The rows of the failed source stay, marked as old.
    await expect(rowOf(page, 'piper')).toContainText('from an earlier check')
    // The source line pairs the dot with words.
    const library = page.locator('[data-source="library"]')
    await expect(library).toContainText('Backend library')
    await expect(library).toContainText('check failed: library not reachable')
    await expect(page.locator('[data-source="gallery"]')).toContainText('checked')
    await expect(page.getByTestId('auto-backends')).toHaveText('off')
    await expect(page.getByRole('link', { name: 'Settings' }).last()).toHaveAttribute('href', '/app/settings')
  })

  test('never checked: no claim that everything is current, and Check now is the main action', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: { sources: { gallery: { state: 'never' }, library: { state: 'never' } }, autoUpdate: { backends: false }, items: [] },
      installed: [installedBackend('llama-cpp', '1.0')],
    })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('No check has run yet')
    await expect(page.getByTestId('updates-strip')).not.toContainText('up to date')
    await expect(page.getByTestId('updates-check-now')).toHaveClass(/dk-btn--primary/)
    await expect(page.locator('[data-source="gallery"]')).toContainText('not checked yet')
  })

  test('a failed check never reads as "no updates"', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: snapshotOf([], { sources: { gallery: okSource(), library: { state: 'failed', error: 'timeout', checkedAt: minutesAgo(1) } } }),
      installed: [installedBackend('llama-cpp', '1.0')],
    })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('No updates found, but the check failed for: Backend library')
    await expect(page.getByTestId('updates-strip')).not.toContainText('Everything is up to date')
    await expect(page.getByTestId('updates-source-failed-library')).toContainText('No check has worked yet')
    // Nothing is claimed about library backends: a backend of the gallery is current, so there is one line.
    await expect(page.locator('[data-group="current"]')).toContainText('1 up to date')
  })

  test('everything current: one sentence, the source line and one collapsed group', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      installed: [installedBackend('llama-cpp', '1.0'), installedBackend('whisper', '1.7')],
      addons: [addonRecord('mail', 'Mail connector', upToDate())],
      snapshot: snapshotOf([]),
    })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('Everything is up to date')
    await expect(page.locator('[data-group="current"]')).toContainText('3 up to date')
    await expect(page.locator('[data-group="current"]')).toContainText('llama-cpp')
    await expect(page.locator('[data-testid="update-row"]')).toHaveCount(0)
    await page.getByTestId('updates-current-toggle').click()
    await expect(page.locator('[data-testid="update-row"]')).toHaveCount(3)
    await expect(page.getByTestId('updates-update-ready')).toHaveCount(0)
  })

  test('nothing installed: the empty state points to Backends and Apps', async ({ page }) => {
    await mockOperate(page, { installed: [] })
    await mockUpdates(page, { installed: [], addons: [] })
    await page.goto('/app/updates')
    await expect(page.getByText('Nothing to update yet')).toBeVisible()
    await expect(page.getByRole('link', { name: 'Open Backends' })).toHaveAttribute('href', '/app/backends')
  })

  test('a load error is a load error, never an empty list', async ({ page }) => {
    await mockOperate(page)
    const state = await mockUpdates(page, { updatesStatus: 500 })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-load-error')).toBeVisible()
    await expect(page.getByTestId('updates-headline')).toHaveText('Could not load the list of updates')
    await expect(page.getByText('Everything is up to date')).toHaveCount(0)
    state.updatesStatus = 200
    await page.getByTestId('updates-load-error').getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('updates-load-error')).toHaveCount(0)
  })

  test('loading shows placeholders, not a claim', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { delayMs: 1500 })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-loading')).toBeVisible()
    await expect(page.getByTestId('updates-headline')).toHaveText('Loading updates')
    await expect(page.getByTestId('updates-loading')).toHaveCount(0)
  })

  test('installs disabled: backends are listed as "Cannot update" with the reason, and nothing can start', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { snapshot: snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')], { installsDisabled: true }) })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-installs-disabled')).toContainText('Backend updates are turned off on this server.')
    const row = rowOf(page, 'llama-cpp')
    await expect(row.getByTestId('update-row-chip')).toContainText('Cannot update')
    await expect(row.getByRole('button')).toHaveCount(0)
    await expect(page.getByTestId('updates-update-ready')).toHaveCount(0)
  })

  test('add-ons off on this server: the source says so and no add-on row exists', async ({ page }) => {
    await mockOperate(page)
    await page.route('**/api/features', route => route.fulfill({ json: { addons: false } }))
    await mockUpdates(page, { snapshot: snapshotOf([galleryItem('llama-cpp', '1', '2')]) })
    await page.goto('/app/updates')
    await expect(page.locator('[data-source="addons"]')).toContainText('not available on this server')
    await expect(page.locator('[data-kind="addon"]')).toHaveCount(0)
  })

  test('the filter narrows the groups, keeps its counts and lives in the address', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-filter-all')).toContainText('8')
    await page.getByTestId('updates-filter-needs-you').click()
    await expect(page).toHaveURL(/filter=needs-you/)
    await expect(page.locator('[data-testid="update-row"]')).toHaveCount(2)
    await expect(page.locator('[data-group="ready"]')).toHaveCount(0)
    await page.getByTestId('updates-filter-all').click()
    await expect(page).not.toHaveURL(/filter=/)
    // The inbox links here with a filter.
    await page.goto('/app/updates?filter=ready')
    await expect(page.getByTestId('updates-filter-ready')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.locator('[data-testid="update-row"]')).toHaveCount(2)
  })
})

test.describe('Updates page: check now', () => {
  test('is a real check that waits, reports its result and then waits a moment before the next', async ({ page }) => {
    await mockOperate(page)
    const state = await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')]),
      addons: [addonRecord('mail', 'Mail connector', upToDate())],
      onCheck: () => snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7'), galleryItem('whisper', '1.7', '1.8')]),
    })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('1 update available')
    await page.getByTestId('updates-check-now').click()
    await expect(page.getByTestId('updates-headline')).toHaveText('2 updates available')
    expect(state.checks).toBe(1)
    await expect(page.getByTestId('updates-check-result')).toContainText('Check finished: 2 updates.')
    await expect(page.getByTestId('updates-check-now')).toBeDisabled()
    await expect(page.getByTestId('updates-check-now')).toContainText('Checked just now')
  })

  test('says so when the answer is seconds old, and when a source failed in the check', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: snapshotOf([]),
      onCheck: () => snapshotOf([], { throttled: true, sources: { gallery: okSource(0), library: { state: 'failed', error: 'timeout', checkedAt: minutesAgo(0) } } }),
    })
    await page.goto('/app/updates')
    await page.getByTestId('updates-check-now').click()
    await expect(page.getByTestId('updates-check-result')).toContainText('1 source failed.')
    await expect(page.getByTestId('updates-check-result')).toContainText('A check ran moments ago.')
    await expect(page.getByTestId('updates-headline')).toContainText('the check failed for: Backend library')
  })
})

test.describe('Updates page: one update, done only when it is done', () => {
  // The operation is listed for a few polls, then leaves the list; the check
  // after it answers according to `after`.
  async function singleBackend(page, { after }) {
    await mockOperate(page)
    const state = await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')]),
      installed: [installedBackend('llama-cpp', '0.9.4')],
      onUpgrade: (name, jobID, s) => {
        s.operations = [runningOp(name, { jobID })]
        setTimeout(() => { s.operations = [] }, 2200)
      },
      onCheck: (s) => after(s),
    })
    return state
  }

  test('the row is updating until the operation ended and the fresh check no longer lists it', async ({ page }) => {
    const state = await singleBackend(page, { after: () => snapshotOf([]) })
    await page.goto('/app/updates')
    await rowOf(page, 'llama-cpp').getByRole('button', { name: 'Update llama-cpp' }).click()
    await expect(rowOf(page, 'llama-cpp')).toHaveAttribute('data-state', 'updating')
    // Dispatch is not completion: nothing says "updated" while the operation runs.
    await expect(page.getByTestId('updates-check-result')).toHaveText('')
    expect(state.checks).toBe(0)
    await expect(page.getByTestId('updates-check-result')).toHaveText('llama-cpp was updated.', { timeout: 15_000 })
    expect(state.checks).toBe(1)
    await expect(rowOf(page, 'llama-cpp')).toHaveCount(0)
    await expect(page.getByTestId('updates-headline')).toHaveText('Everything is up to date')
  })

  test('an operation that ended with the update still listed is a failed update', async ({ page }) => {
    await singleBackend(page, { after: () => snapshotOf([galleryItem('llama-cpp', '0.9.4', '0.9.7')]) })
    await page.goto('/app/updates')
    await rowOf(page, 'llama-cpp').getByRole('button', { name: 'Update llama-cpp' }).click()
    const row = rowOf(page, 'llama-cpp')
    await expect(row).toHaveAttribute('data-state', 'failed', { timeout: 15_000 })
    await expect(row).toContainText('The update ended but the version did not change.')
    await expect(row.getByRole('button', { name: 'Retry llama-cpp' })).toBeVisible()
    await expect(page.getByTestId('updates-check-result')).toHaveText('llama-cpp was not updated.')
  })

  test('a failed operation shows its reason, and Retry starts a new update', async ({ page }) => {
    await mockOperate(page)
    const state = await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('whisper', '1.7.2', '1.8.0')]),
      operations: [failedOp('whisper')],
      installed: [installedBackend('whisper', '1.7.2')],
      onUpgrade: (name, jobID, s) => { s.operations = [runningOp(name, { jobID })] },
    })
    await page.route('**/api/operations/*/dismiss', route => { state.operations = []; return route.fulfill({ json: {} }) })
    await page.goto('/app/updates')
    const row = rowOf(page, 'whisper')
    await expect(row).toContainText('Update failed: download interrupted')
    await row.getByRole('button', { name: 'Retry whisper' }).click()
    await expect(row).toHaveAttribute('data-state', 'updating')
    expect(state.upgrades).toEqual(['whisper'])
  })

  test('a request the server refuses is a failed update with its words', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { snapshot: snapshotOf([galleryItem('llama-cpp', '1', '2')]) })
    await page.route('**/api/backends/upgrade/*', route => route.fulfill({ status: 403, json: { error: 'backend_install_disabled' } }))
    await page.goto('/app/updates')
    await rowOf(page, 'llama-cpp').getByRole('button', { name: 'Update llama-cpp' }).click()
    await expect(rowOf(page, 'llama-cpp')).toHaveAttribute('data-state', 'failed')
    await expect(rowOf(page, 'llama-cpp')).toContainText('The update could not start')
  })

  test('Cancel on an updating row cancels its operation', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { snapshot: snapshotOf([galleryItem('piper', '1', '2')]), operations: [runningOp('piper')] })
    const cancelled = []
    await page.route('**/api/operations/*/cancel', route => { cancelled.push(route.request().url()); return route.fulfill({ json: {} }) })
    await page.goto('/app/updates')
    await rowOf(page, 'piper').getByRole('button', { name: 'Cancel the update of piper' }).click()
    await expect.poll(() => cancelled.length).toBe(1)
    expect(cancelled[0]).toContain('job-piper')
  })
})

test.describe('Updates page: update ready', () => {
  test('runs the rows one after another and counts each as done only when it is', async ({ page }) => {
    await mockOperate(page)
    const state = await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('alpha', '1', '2'), galleryItem('beta', '1', '2')]),
      installed: [installedBackend('alpha', '1'), installedBackend('beta', '1')],
      onUpgrade: (name, jobID, s) => {
        s.operations = [runningOp(name, { jobID })]
        setTimeout(() => { s.operations = []; s.finished = [...(s.finished || []), { name, at: Date.now() }] }, 1800)
      },
      onCheck: (s) => snapshotOf(s.snapshot.items.filter(i => !(s.finished || []).some(f => f.name === i.name))),
    })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 2 ready')
    await button.click()
    await expect(button).toContainText('Updating 1 of 2')
    await expect(page.getByTestId('updates-check-result')).toContainText('Updating 1 of 2')
    await expect(page.getByTestId('updates-check-result')).toContainText('2 updated, 0 failed, 0 skipped', { timeout: 30_000 })
    expect(state.upgrades).toEqual(['alpha', 'beta'])
    // The second update starts only after the first one really ended.
    const firstEnd = state.finished.find(f => f.name === 'alpha').at
    expect(state.upgradeTimes[1]).toBeGreaterThanOrEqual(firstEnd)
    await expect(page.getByTestId('updates-headline')).toHaveText('Everything is up to date')
  })

  test('a failure in the middle does not stop the rest, and Retry failed runs only the failed one', async ({ page }) => {
    await mockOperate(page)
    let betaTries = 0
    const state = await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('alpha', '1', '2'), galleryItem('beta', '1', '2'), galleryItem('gamma', '1', '2')]),
      installed: [],
      onUpgrade: (name, jobID, s) => {
        if (name === 'beta' && betaTries++ === 0) {
          s.operations = [failedOp('beta', 'disk full', { jobID })]
          return
        }
        s.operations = [runningOp(name, { jobID })]
        setTimeout(() => { s.operations = []; s.finished = [...(s.finished || []), { name }] }, 1500)
      },
      onCheck: (s) => snapshotOf(s.snapshot.items.filter(i => !(s.finished || []).some(f => f.name === i.name))),
    })
    await page.route('**/api/operations/*/dismiss', route => route.fulfill({ json: {} }))
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await expect(page.getByTestId('updates-check-result')).toContainText('2 updated, 1 failed, 0 skipped', { timeout: 40_000 })
    expect(state.upgrades).toEqual(['alpha', 'beta', 'gamma'])
    const retry = page.getByTestId('updates-retry-failed')
    await expect(retry).toContainText('Retry failed (1)')
    // beta is the only row left, failed by its operation.
    state.operations = []
    await retry.click()
    await expect(page.getByTestId('updates-check-result')).toContainText('1 updated, 0 failed', { timeout: 30_000 })
    expect(state.upgrades).toEqual(['alpha', 'beta', 'gamma', 'beta'])
  })

  test('held and blocked rows are never part of the run, and a row can be left out', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    // Two rows can start: llama-cpp and Notes sync. Tables is held.
    await expect(page.getByTestId('updates-update-ready')).toContainText('Update 2 ready')
    await rowOf(page, 'Notes sync').getByLabel('Select Notes sync').uncheck()
    await expect(page.getByTestId('updates-update-ready')).toContainText('Update 1 ready')
    await expect(rowOf(page, 'Tables').getByRole('checkbox')).toHaveCount(0)
    await page.getByTestId('updates-select-all').click()
    await expect(page.getByTestId('updates-update-ready')).toContainText('Update 2 ready')
  })
})

test.describe('Updates page: add-ons', () => {
  test('Update asks for the add-on confirmation before anything starts', async ({ page }) => {
    await mockOperate(page)
    const posts = []
    await mockUpdates(page, { addons: [addonRecord('notes', 'Notes sync', updateAvailable('0.8.2'), { version: '0.8.1' })] })
    await page.route(/\/api\/addons\/notes\/upgrade$/, route => { posts.push(1); return route.fulfill({ status: 500, json: {} }) })
    await page.goto('/app/updates')
    await rowOf(page, 'Notes sync').getByRole('button', { name: 'Update Notes sync' }).click()
    const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog')).first()
    await expect(dialog).toContainText('Notes sync')
    await expect(dialog).toContainText('0.8.2')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(posts).toHaveLength(0)
  })

  test('an add-on with an update that has no build to pin offers a check, not a dead Update', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { addons: [addonRecord('notes', 'Notes sync', { state: 'update-available', version: '0.8.2', available: true })] })
    await page.goto('/app/updates')
    const row = rowOf(page, 'Notes sync')
    await expect(row.getByRole('button', { name: 'Update Notes sync' })).toHaveCount(0)
    await expect(row.getByRole('button', { name: 'Check Notes sync for an update' })).toBeVisible()
    await expect(row.getByRole('checkbox')).toHaveCount(0)
  })

  test('Review opens the approval dialog of the held upgrade', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      addons: [addonRecord('tables', 'Tables', updateAvailable('2.4.0'), { version: '2.3.0' })],
      grants: { revision: 1, addons: [heldGrant('tables', 'Tables', '2.4.0', ['read_files', 'write_files'])] },
    })
    await page.route(/\/api\/addons\/host-grants\/preview/, route => route.fulfill({ status: 503, json: {} }))
    await page.goto('/app/updates')
    await expect(rowOf(page, 'Tables')).toContainText('Held for approval. v2.4.0 asks for access that you have not approved (2 new).')
    await rowOf(page, 'Tables').getByRole('button', { name: 'Review the update of Tables' }).click()
    await expect(page.getByTestId('approve-dialog')).toBeVisible()
  })

  test('blocked add-ons say why in words', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      addons: [
        addonRecord('old', 'Old thing', { state: 'incompatible', version: '9.0.0', digest: 'sha256:cc', reasons: ['platform'] }),
        addonRecord('odd', 'Odd thing', { state: 'integrity-error' }),
      ],
    })
    await page.goto('/app/updates')
    await expect(rowOf(page, 'Old thing').getByTestId('update-row-chip')).toContainText('Cannot update')
    await expect(rowOf(page, 'Old thing')).toContainText('Version v9.0.0 cannot run on this host.')
    await expect(rowOf(page, 'Odd thing')).toContainText('different contents')
  })

  test('an add-on that failed to check puts the add-on source in the failed state', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, { addons: [addonRecord('mail', 'Mail', { state: 'check-failed', error: 'source-unavailable', checkedAt: minutesAgo(5) })] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-source-failed-addons')).toContainText('add-ons may have updates that are not shown')
    await expect(page.getByTestId('updates-headline')).toContainText('the check failed for: Add-ons')
  })
})

test.describe('Updates page: access and layout', () => {
  test('a person who is not an administrator has no tab, makes no update request and lands on Home', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true } } }) }))
    const seen = []
    page.on('request', req => { if (/\/api\/(backends\/updates|addons\/host-grants)/.test(req.url())) seen.push(req.url()) })
    await page.goto('/app/updates')
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.locator('[data-hub-tab="updates"]')).toHaveCount(0)
    await expect(page.getByTestId('operate-updates-badge')).toHaveCount(0)
    expect(seen).toEqual([])
  })

  test('the tab sits between Runtime and Traffic and has no second accessible name', async ({ page }) => {
    await mixed(page)
    await page.goto('/app/updates')
    await expect(page.locator('[data-hub-tab="updates"]')).toBeVisible()
    const tabs = await page.locator('.dk-hubtabs [data-hub-tab]').evaluateAll(els => els.map(el => el.getAttribute('data-hub-tab')))
    expect(tabs.indexOf('updates')).toBe(tabs.indexOf('runtime') + 1)
    expect(tabs.indexOf('traffic')).toBe(tabs.indexOf('updates') + 1)
    await expect(page.locator('[data-hub-tab="updates"]')).toHaveAttribute('aria-current', 'page')
  })

  test('keyboard: every action is a named button reached with Tab, and Enter starts it', async ({ page }) => {
    await mockOperate(page)
    const state = await mockUpdates(page, { snapshot: snapshotOf([galleryItem('llama-cpp', '1', '2')]) })
    await page.goto('/app/updates')
    const update = page.getByRole('button', { name: 'Update llama-cpp' })
    await update.focus()
    await expect(update).toBeFocused()
    await page.keyboard.press('Enter')
    await expect.poll(() => state.upgrades.length).toBe(1)
    // Landmarks and names.
    await expect(page.getByRole('list', { name: 'Where updates come from' })).toBeVisible()
    await expect(page.getByRole('group', { name: 'Filter updates' })).toBeVisible()
  })

  test('narrow: no sideways scroll, the main action stays in reach and row buttons are touch size', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mixed(page)
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const sticky = page.getByTestId('updates-sticky')
    await expect(sticky).toBeVisible()
    await expect(sticky.getByTestId('updates-update-ready')).toHaveCount(1)
    await expect(page.getByTestId('updates-strip').getByTestId('updates-update-ready')).toHaveCount(0)
    const button = rowOf(page, 'llama-cpp').getByRole('button', { name: 'Update llama-cpp' })
    const box = await button.boundingBox()
    expect(box.height).toBeGreaterThanOrEqual(40)
    // The row's checkbox is left out on a phone; the run takes every row that can start.
    await expect(rowOf(page, 'llama-cpp').locator('input[type="checkbox"]')).toBeHidden()
    const rowBox = await rowOf(page, 'llama-cpp').boundingBox()
    expect(rowBox.width).toBeLessThanOrEqual(390)
  })
})
