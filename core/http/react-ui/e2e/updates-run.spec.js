import { test, expect } from './coverage-fixtures.js'
import { mockOperate, installedBackend } from './operate-fixtures.js'
import {
  DIGEST_A, DIGEST_B, addonPin, addonRecord, failedOp, galleryItem, minutesAgo, mockUpdateRuns, mockUpdates, modelItem, modelSnapshotOf, previewItem, runItem, runOf,
  snapshotOf, updateAvailable,
} from './updates-fixtures.js'

// The update run: the review the server plans, the run it owns, and what the
// page shows of it. The run API is stubbed (/api/updates/runs); the page, the
// hook and the words are the real ones.

const rowOf = (page, name) => page.locator(`[data-testid="update-row"][data-name="${name}"]`)
const reviewOf = (page) => page.getByTestId('update-review')
const reviewItem = (page, key) => reviewOf(page).locator(`[data-testid="update-review-item"][data-key="${key}"]`)
const runRow = (page, key) => page.locator(`[data-testid="run-item"][data-key="${key}"]`)

// The mocks' case: four backends (one on three nodes), two add-ons, two models.
async function fleet(page) {
  await mockOperate(page)
  return mockUpdates(page, {
    snapshot: snapshotOf([
      galleryItem('kokoro', '0.3.1', '0.4.0'),
      galleryItem('llama-cpp', '0.9.4', '0.9.7', { nodes: [{ nodeId: 'a' }, { nodeId: 'b' }, { nodeId: 'c' }], driftNodes: 0 }),
      galleryItem('piper', '1.2.0', '1.3.1'),
      galleryItem('sglang', '0.5.2', '0.5.4'),
    ]),
    installed: [installedBackend('kokoro', '0.3.1'), installedBackend('llama-cpp', '0.9.4'), installedBackend('piper', '1.2.0'), installedBackend('sglang', '0.5.2')],
    addons: [
      addonRecord('notes', 'Notes sync', updateAvailable('0.8.2'), { version: '0.8.1' }),
      addonRecord('tables', 'Tables', updateAvailable('2.4.0'), { version: '2.3.0' }),
    ],
    models: modelSnapshotOf([modelItem('qwen3-8b', 'update-available'), modelItem('llama-3.2-3b', 'update-available')]),
  })
}

// The plan the server answers for the fleet: the versions of the checks, an
// add-on with its title and pinned to its candidate digest, the risks the
// cluster knows (two models loaded with llama-cpp, on two of its three nodes),
// and the backends first.
const VERSIONS = {
  'gallery:kokoro': ['0.3.1', '0.4.0'], 'gallery:llama-cpp': ['0.9.4', '0.9.7'], 'gallery:piper': ['1.2.0', '1.3.1'], 'gallery:sglang': ['0.5.2', '0.5.4'],
  'addon:notes': ['0.8.1', '0.8.2'], 'addon:tables': ['2.3.0', '2.4.0'],
  'model:qwen3-8b': [DIGEST_A, DIGEST_B], 'model:llama-3.2-3b': [DIGEST_A, DIGEST_B],
}
const TITLES = { 'addon:notes': 'Notes sync', 'addon:tables': 'Tables' }
function plan(items) {
  const risk = {
    'gallery:llama-cpp': [{ code: 'stops_models', params: { count: '2', nodes: '2' } }],
    'gallery:sglang': [{ code: 'cluster', params: { nodes: '2' } }, { code: 'drift', params: { count: '1' } }],
    'model:llama-3.2-3b': [{ code: 'config_only', params: {} }],
    'addon:notes': [{ code: 'migrates_data', params: {} }],
    'addon:tables': [{ code: 'migrates_data', params: {} }],
  }
  const out = items.map((i) => {
    const key = `${i.kind}:${i.name}`
    const [from, to] = VERSIONS[key] || ['1.0.0', '1.1.0']
    return previewItem(key, from, to, { risk: risk[key] || [], title: TITLES[key] })
  })
  const rank = { gallery: 0, library: 0, model: 1, addon: 2 }
  const order = out.map(i => i.key).sort((a, b) => rank[a.split(':')[0]] - rank[b.split(':')[0]])
  return { items: out, order }
}

const RUNNING = () => runOf([
  runItem('gallery:piper', 'updated', { from: 'v1.2.0', to: 'v1.3.1' }),
  runItem('gallery:sglang', 'updated', { from: 'v0.5.2', to: 'v0.5.4' }),
  runItem('gallery:kokoro', 'running', { from: 'v0.3.1', to: 'v0.4.0', progress: 35, message: 'Downloading 35%' }),
  runItem('gallery:llama-cpp', 'running', {
    from: 'v0.9.4', to: 'v0.9.7', progress: 64, message: 'downloading 64%',
    nodes: [{ id: 'a', name: 'node-a', phase: 'updated', progress: 100 }, { id: 'b', name: 'node-b', phase: 'running', progress: 64, message: 'downloading 64%' }, { id: 'c', name: 'node-c', phase: 'queued' }],
  }),
  runItem('addon:notes', 'queued', { title: 'Notes sync', from: '0.8.1', to: '0.8.2', queued_reason: 'after_backends' }),
  runItem('addon:tables', 'queued', { title: 'Tables', from: '2.3.0', to: '2.4.0', queued_reason: 'one_addon_at_a_time' }),
])

const ENDED = ({ retryable = true } = {}) => runOf([
  runItem('gallery:piper', 'updated'),
  runItem('gallery:sglang', 'updated'),
  runItem('gallery:kokoro', 'updated'),
  runItem('gallery:llama-cpp', 'updated'),
  runItem('addon:tables', 'updated', { title: 'Tables', from: '2.3.0', to: '2.4.0' }),
  runItem('addon:notes', 'rolled_back', { title: 'Notes sync', from: '0.8.1', to: '0.8.2', error_key: 'start', error: 'the setting "sync.url" is missing', retryable }),
])

test.describe('Updates: the review', () => {
  test('Update ready opens the review grouped by kind, models unselected, with the risks the server named', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { preview: items => plan(items), polls: [RUNNING()] })
    await page.goto('/app/updates')
    const button = page.getByTestId('updates-update-ready')
    await expect(button).toContainText('Update 6 ready')
    await button.click()
    const review = reviewOf(page)
    await expect(review).toBeVisible()
    await expect(review.getByRole('heading', { name: 'Review 8 updates' })).toBeVisible()
    await expect(review).toContainText('Nothing changes until you start.')
    // Every ready row was asked about, models included, by key.
    expect(runs.previews[0].items).toEqual(expect.arrayContaining([
      { kind: 'gallery', name: 'llama-cpp' }, { kind: 'addon', name: 'notes' }, { kind: 'model', name: 'qwen3-8b' },
    ]))
    const groups = await review.locator('.up-group__title').allInnerTexts()
    expect(groups.map(g => g.toUpperCase())).toEqual(['BACKENDS (4)', 'ADD-ONS (2)', 'MODELS (2)'])
    await expect(page.getByTestId('update-review-count')).toHaveText('6 of 8 selected')
    await expect(reviewItem(page, 'model:qwen3-8b').getByRole('checkbox')).not.toBeChecked()
    await expect(reviewItem(page, 'model:qwen3-8b')).toContainText('Not selected by default')
    await expect(reviewItem(page, 'gallery:kokoro').getByRole('checkbox')).toBeChecked()
    await expect(reviewItem(page, 'gallery:llama-cpp').getByTestId('update-review-risk')).toHaveText('Stops 2 loaded models on 2 nodes while it switches')
    await expect(reviewItem(page, 'gallery:kokoro').getByTestId('update-review-risk')).toHaveCount(0)
    // Every risk code the server sends has words; none is dropped.
    await expect(reviewItem(page, 'gallery:sglang').getByTestId('update-review-risk')).toHaveText([
      'Updates on 2 nodes', '1 node runs a different build now. The update moves every node to the new one.',
    ])
    await expect(reviewItem(page, 'model:llama-3.2-3b').getByTestId('update-review-risk').first()).toHaveText("Changes only the model's settings. Nothing is downloaded.")
    await expect(reviewItem(page, 'addon:notes')).toContainText('Your data is migrated. A verified backup is taken first.')
    await expect(reviewItem(page, 'addon:notes').getByTestId('update-review-backup')).toHaveText('Backs up your data')
    // The page name of an add-on, not its id.
    await expect(reviewItem(page, 'addon:notes')).toContainText('Notes sync')
    await expect(page.getByTestId('update-review-note')).toContainText('Backends go first, then models, then add-ons one at a time.')
    await expect(page.getByTestId('update-review-start')).toHaveText('Update 6 items')
  })

  test('Select all, Select none and a click change the count and the start button', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { preview: items => plan(items), polls: [RUNNING()] })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    const count = page.getByTestId('update-review-count')
    const start = page.getByTestId('update-review-start')
    await page.getByTestId('update-review-all').click()
    await expect(count).toHaveText('8 of 8 selected')
    await expect(start).toHaveText('Update 8 items')
    await page.getByTestId('update-review-none').click()
    await expect(count).toHaveText('0 of 8 selected')
    await expect(start).toBeDisabled()
    await reviewItem(page, 'gallery:kokoro').getByRole('checkbox').check()
    await reviewItem(page, 'addon:notes').getByRole('checkbox').check()
    await expect(count).toHaveText('2 of 8 selected')
    await expect(start).toHaveText('Update 2 items')
    await start.click()
    // The plan's order, each item pinned to the version the review showed.
    await expect.poll(() => runs.starts.length).toBe(1)
    // An add-on is pinned to the digest of its candidate, not to the version
    // the review shows: the server refuses a version as stale.
    expect(runs.starts[0]).toEqual({ items: [{ kind: 'gallery', name: 'kokoro', expected_to: '0.4.0' }, { kind: 'addon', name: 'notes', expected_to: addonPin('0.8.2') }], source: 'manual' })
    // The confirmation of the administrator's browser: without it the server skips add-ons.
    expect(runs.startHeaders[0]['x-localai-confirm']).toBe('upgrade-run')
    await expect(reviewOf(page)).toHaveCount(0)
    await expect(page).toHaveURL(/[?&]run=run-1/)
  })

  test('an item that cannot start says why in words, never the code of the plan', async ({ page }) => {
    await fleet(page)
    // The reasons the planner sends (core/services/upgraderun/planner.go).
    const blocked = {
      'gallery:kokoro': 'installs_disabled', 'gallery:piper': 'in_run', 'model:qwen3-8b': 'edited_keys', 'model:llama-3.2-3b': 'held',
    }
    await mockUpdateRuns(page, {
      preview: items => {
        const out = plan(items)
        out.items = out.items.map(i => (blocked[i.key] ? { ...i, startable: false, selected: false, default_selected: false, reason: blocked[i.key] } : i))
        return out
      },
    })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    const why = (key) => reviewItem(page, key).getByTestId('update-review-why')
    await expect(why('gallery:kokoro')).toHaveText('Cannot start: Installing backends is turned off on this server')
    await expect(why('gallery:piper')).toHaveText('Cannot start: It is already in the update run in progress')
    await expect(why('model:qwen3-8b')).toHaveText('Cannot start: You edited settings of this model. Use Upgrade on its row to choose what to keep')
    await expect(why('model:llama-3.2-3b')).toHaveText('Cannot start: The update waits for your approval')
    for (const code of Object.values(blocked)) await expect(reviewOf(page)).not.toContainText(code)
  })

  test('Cancel closes the review and nothing starts', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { preview: items => plan(items) })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await reviewOf(page).getByRole('button', { name: 'Cancel' }).last().click()
    await expect(reviewOf(page)).toHaveCount(0)
    expect(runs.starts).toHaveLength(0)
  })

  test('a review that cannot load says so once', async ({ page }) => {
    await fleet(page)
    await mockUpdateRuns(page, { preview: items => plan(items) })
    await page.route('**/api/updates/runs/preview', route => route.fulfill({ status: 409, json: { code: 'source_unchecked', message: 'the check did not answer' } }))
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await expect(page.getByTestId('update-review-error')).toBeVisible()
    await expect(page.getByTestId('update-review-refused')).toHaveCount(0)
  })

  test('a start the server refuses keeps the review open with its words', async ({ page }) => {
    await fleet(page)
    await mockUpdateRuns(page, { preview: items => plan(items), startReply: () => ({ status: 409, json: { code: 'stale', error: 'stale' } }) })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await page.getByTestId('update-review-start').click()
    await expect(page.getByTestId('update-review-refused')).toContainText('A newer version came out after the review.')
    await expect(reviewOf(page)).toBeVisible()
  })

  test('the Update of one row asks about that row only, in the short dialog', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, {
      preview: items => ({ items: items.map(i => previewItem(`${i.kind}:${i.name}`, '0.8.1', '0.8.2', { title: 'Notes sync', risk: [{ code: 'migrates_data' }] })) }),
      startReply: () => runOf([runItem('addon:notes', 'running', { title: 'Notes sync', from: '0.8.1', to: '0.8.2', progress: 10 })]),
    })
    await page.goto('/app/updates')
    await rowOf(page, 'Notes sync').getByRole('button', { name: 'Update Notes sync' }).click()
    const review = reviewOf(page)
    await expect(review.getByRole('heading', { name: 'Update Notes sync?' })).toBeVisible()
    await expect(review).toContainText('0.8.1 to 0.8.2 · Library: official')
    await expect(page.getByTestId('update-review-note')).toContainText('Your data is migrated. A verified backup is taken first. The old version stays until the new one is running.')
    expect(runs.previews[0].items).toEqual([{ kind: 'addon', name: 'notes' }])
    await review.getByTestId('update-review-start').click()
    await expect.poll(() => runs.starts.length).toBe(1)
    expect(runs.starts[0].items).toEqual([{ kind: 'addon', name: 'notes', expected_to: addonPin('0.8.2') }])
    await expect(runRow(page, 'addon:notes')).toBeVisible()
  })

  test('a model in the review of its own row starts selected', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { startReply: () => runOf([runItem('model:qwen3-8b', 'queued')]) })
    await page.goto('/app/updates')
    // The model row keeps its own Upgrade (files, edited keys); a failed
    // backend's Retry is a review of that row.
    await expect(rowOf(page, 'qwen3-8b').getByTestId('model-upgrade')).toBeVisible()
    await rowOf(page, 'kokoro').getByRole('button', { name: 'Update kokoro' }).click()
    await expect(reviewOf(page).getByRole('heading', { name: 'Update kokoro?' })).toBeVisible()
    await expect(page.getByTestId('update-review-start')).toBeEnabled()
    expect(runs.previews[0].items).toEqual([{ kind: 'gallery', name: 'kokoro' }])
  })
})

test.describe('Updates: the run', () => {
  test('the strip and the run group show each item in run order with its state', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { active: [RUNNING()], polls: [RUNNING()] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
    await expect(page.getByTestId('updates-breakdown')).toContainText('2 updated, 2 running, 2 waiting. Started')
    await expect(page.getByTestId('updates-run-progress')).toHaveAttribute('aria-valuenow', String(Math.round((100 + 100 + 35 + 64) / 6)))
    const keys = await page.locator('[data-testid="run-item"]').evaluateAll(els => els.map(el => el.dataset.key))
    expect(keys).toEqual(['gallery:piper', 'gallery:sglang', 'gallery:kokoro', 'gallery:llama-cpp', 'addon:notes', 'addon:tables'])
    await expect(page.getByRole('heading', { name: /This update run/ })).toBeVisible()
    await expect(runRow(page, 'gallery:piper').getByTestId('run-item-chip')).toHaveText('Updated')
    await expect(runRow(page, 'gallery:kokoro').getByTestId('run-item-chip')).toHaveText('Updating')
    await expect(runRow(page, 'gallery:kokoro').getByRole('progressbar')).toHaveAttribute('aria-valuenow', '35')
    await expect(runRow(page, 'gallery:kokoro')).toContainText('Downloading 35%')
    await expect(runRow(page, 'gallery:llama-cpp')).toContainText('Node 2 of 3: downloading 64%')
    await expect(runRow(page, 'addon:notes').getByTestId('run-item-chip')).toHaveText('Queued')
    await expect(runRow(page, 'addon:notes')).toContainText('Waiting for the backends. Add-ons update one at a time.')
    await expect(runRow(page, 'addon:tables')).toContainText('Waiting for Notes sync.')
    // An item of the run is not listed a second time in the other groups.
    await expect(rowOf(page, 'kokoro')).toHaveCount(0)
    await expect(rowOf(page, 'qwen3-8b')).toHaveCount(1)
    // A queued item can be cancelled: the server drops it. The server marks
    // only a running operation cancellable, so the page offers Cancel for
    // every queued item and for a running one the server says can stop.
    await expect(runRow(page, 'addon:notes').getByTestId('run-item-cancel')).toBeVisible()
    await expect(runRow(page, 'gallery:kokoro').getByTestId('run-item-cancel')).toBeVisible()
    await expect(runRow(page, 'gallery:piper').getByTestId('run-item-cancel')).toHaveCount(0)
    // The cancel of one item and of the rest.
    await runRow(page, 'addon:tables').getByTestId('run-item-cancel').click()
    await expect.poll(() => runs.itemCancels).toEqual(['addon:tables'])
    await page.getByTestId('updates-cancel-run').click()
    await expect.poll(() => runs.cancels.length).toBe(1)
  })

  test('the page follows the run as the server moves it, to a rolled-back item and the result', async ({ page }) => {
    await fleet(page)
    const queued = runOf([runItem('gallery:kokoro', 'queued', { queued_reason: '' }), runItem('addon:notes', 'queued', { queued_reason: 'after_backends' })])
    const running = runOf([runItem('gallery:kokoro', 'running', { progress: 50, message: 'Installing' }), runItem('addon:notes', 'queued', { queued_reason: 'after_backends' })])
    const addon = runOf([runItem('gallery:kokoro', 'updated'), runItem('addon:notes', 'running', { progress: 20, message: 'Backing up' })])
    const ended = runOf([runItem('gallery:kokoro', 'updated'), runItem('addon:notes', 'rolled_back', { title: 'Notes sync', from: '0.8.1', to: '0.8.2', error_key: 'start', retryable: true })])
    await mockUpdateRuns(page, { active: [queued], polls: [queued, running, addon, ended] })
    await page.goto('/app/updates')
    await expect(runRow(page, 'gallery:kokoro')).toContainText('Waiting to start.')
    await expect(runRow(page, 'gallery:kokoro')).toContainText('Installing', { timeout: 10_000 })
    await expect(runRow(page, 'addon:notes')).toContainText('Backing up', { timeout: 10_000 })
    await expect(runRow(page, 'addon:notes').getByTestId('run-item-chip')).toHaveText('Rolled back', { timeout: 10_000 })
    await expect(runRow(page, 'addon:notes')).toContainText('The new version did not start. Notes sync is still on 0.8.1 and your data is unchanged.')
    await expect(page.getByTestId('updates-headline')).toHaveText('1 updated, 1 needs attention')
    await expect(page.getByTestId('updates-breakdown')).toContainText('Nothing was left half-updated. Finished')
  })

  test('a reload during a run shows the same run again', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { active: [RUNNING()], polls: [RUNNING()] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
    await page.reload()
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
    await expect(runRow(page, 'gallery:kokoro')).toBeVisible()
    // The address of the run (the link of its notification) opens it too.
    runs.active = []
    await page.goto('/app/updates?run=run-1')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
  })

  test('a failure says what happened in plain words; Retry only when the server allows it, Details always', async ({ page }) => {
    await fleet(page)
    // A retry is a new run, as on the server; the page then polls that run.
    const next = { ...RUNNING(), id: 'run-2' }
    const runs = await mockUpdateRuns(page, {
      active: [ENDED()],
      polls: [ENDED()],
      onRetry: (state) => { state.polls = [next]; state.reads = 0; return next },
    })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('5 updated, 1 needs attention')
    const failed = runRow(page, 'addon:notes')
    await expect(failed.getByTestId('run-item-detail')).toHaveText('The new version did not start: the setting "sync.url" is missing. Notes sync is still on 0.8.1 and your data is unchanged.')
    await expect(failed.getByTestId('run-item-retry')).toBeVisible()
    await failed.getByTestId('run-item-details-toggle').click()
    await expect(failed.getByTestId('run-item-details')).toContainText('Code: start')
    await page.getByTestId('updates-retry-failed').click()
    await expect.poll(() => runs.retries.length).toBe(1)
    expect(runs.retryHeaders[0]['x-localai-confirm']).toBe('upgrade-run')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
  })

  test('without retryable failures there is no Retry, and Dismiss puts the run away', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, { active: [ENDED({ retryable: false })], polls: [ENDED({ retryable: false })] })
    await page.goto('/app/updates')
    const failed = runRow(page, 'addon:notes')
    await expect(failed.getByTestId('run-item-details-toggle')).toBeVisible()
    await expect(failed.getByTestId('run-item-retry')).toHaveCount(0)
    await expect(page.getByTestId('updates-retry-failed')).toHaveCount(0)
    await page.getByTestId('updates-dismiss-run').click()
    await expect.poll(() => runs.dismissed).toEqual(['run-1'])
    await expect(page.getByTestId('run-item')).toHaveCount(0)
    await expect(page.getByTestId('updates-headline')).toHaveText(/updates available/)
  })

  test('an unconfirmed item the server still checks is in progress; one no check could answer asks for a new check', async ({ page }) => {
    await fleet(page)
    const checking = runOf([runItem('gallery:kokoro', 'updated'), runItem('gallery:piper', 'unconfirmed', { verifying: true, progress: 100, message: 'checking that the update applied' })], { state: 'running' })
    const settled = runOf([runItem('gallery:kokoro', 'updated'), runItem('gallery:piper', 'unconfirmed', { retryable: true, message: 'no check could confirm the update' })], { finished_at: minutesAgo(0) })
    await mockUpdateRuns(page, { active: [checking], polls: [checking, checking, settled] })
    await page.goto('/app/updates')
    const piper = runRow(page, 'gallery:piper')
    await expect(piper.getByTestId('run-item-chip')).toHaveText('Updating')
    await expect(piper).toContainText('checking that the update applied')
    await expect(page.getByTestId('updates-breakdown')).toContainText('1 updated, 1 running')
    await expect(piper.getByTestId('run-item-chip')).toHaveText('Not confirmed', { timeout: 10_000 })
    await expect(piper).toContainText('Could not confirm. Check again to see which version piper is on.')
    await expect(page.getByTestId('updates-breakdown')).toContainText('1 result is not confirmed yet')
    await expect(page.getByTestId('updates-headline')).toHaveText('1 updated, 1 needs attention')
  })

  test('an unconfirmed item no check could answer needs a look while the run still goes on', async ({ page }) => {
    await fleet(page)
    const run = runOf([
      runItem('gallery:piper', 'unconfirmed', { retryable: true, message: 'no check could confirm the update' }),
      runItem('gallery:kokoro', 'running', { progress: 40, message: 'Downloading 40%' }),
    ], { state: 'running' })
    await mockUpdateRuns(page, { active: [run], polls: [run] })
    await page.goto('/app/updates')
    const piper = runRow(page, 'gallery:piper')
    await expect(piper.getByTestId('run-item-chip')).toHaveText('Not confirmed')
    await expect(piper).toContainText('Could not confirm.')
    await expect(page.getByTestId('updates-breakdown')).toContainText('1 running')
    await expect(page.getByTestId('updates-breakdown')).toContainText('1 needs attention')
  })

  test('a retry is a new run: the page follows it', async ({ page }) => {
    await fleet(page)
    const next = { ...RUNNING(), id: 'run-2' }
    const runs = await mockUpdateRuns(page, {
      active: [ENDED()],
      polls: [ENDED()],
      onRetry: (state) => { state.polls = [next]; state.reads = 0; return next },
    })
    await page.goto('/app/updates?run=run-1')
    await runRow(page, 'addon:notes').getByTestId('run-item-retry').click()
    await expect.poll(() => runs.retries.length).toBe(1)
    await expect(page).toHaveURL(/[?&]run=run-2/)
    await expect(page.getByTestId('updates-strip')).toHaveAttribute('data-run', 'run-2')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
  })

  test('a start while another run is active says so plainly and shows that run', async ({ page }) => {
    await fleet(page)
    const runs = await mockUpdateRuns(page, {
      preview: items => plan(items),
      polls: [RUNNING()],
      startReply: (body, state) => { state.active = [RUNNING()]; return { status: 409, json: { code: 'run_active', key: '', message: 'a run is active' } } },
    })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await page.getByTestId('update-review-start').click()
    await expect(page.getByTestId('update-review-refused')).toContainText('An update run is in progress.')
    await page.getByTestId('update-review').getByRole('button', { name: 'Cancel' }).last().click()
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
    // While a run is active, no row starts another one.
    await expect(rowOf(page, 'qwen3-8b').getByTestId('model-upgrade')).toBeDisabled()
    expect(runs.starts).toHaveLength(1)
  })

  test('an add-on that left the lists keeps its title and versions in the run, never its id and digest', async ({ page }) => {
    await mockOperate(page)
    // The add-on was updated: the lists no longer have its row.
    await mockUpdates(page, { snapshot: snapshotOf([]), installed: [installedBackend('kokoro', '0.4.0')] })
    const run = runOf([runItem('gallery:kokoro', 'updated', { from: '0.3.1', to: '0.4.0' }), runItem('addon:notes', 'rolled_back', { title: 'Notes sync', from: '0.8.1', to: '0.8.2', error_key: 'start', retryable: true })])
    await mockUpdateRuns(page, { active: [run], polls: [run] })
    await page.goto('/app/updates')
    const notes = runRow(page, 'addon:notes')
    await expect(notes.locator('.uc-row__title')).toHaveText('Notes sync')
    await expect(notes).toContainText('0.8.1 to 0.8.2')
    await expect(notes).toContainText('Notes sync is still on 0.8.1')
    await expect(notes).not.toContainText('sha256')
  })

  test('a skipped add-on says why in words, from its key', async ({ page }) => {
    await fleet(page)
    const run = runOf([runItem('gallery:kokoro', 'updated'), runItem('addon:notes', 'skipped', { error_key: 'not_human_surface', error: 'not a human surface' })])
    await mockUpdateRuns(page, { active: [run], polls: [run] })
    await page.goto('/app/updates')
    await expect(runRow(page, 'addon:notes')).toContainText('Skipped: This update needs you to confirm it in the browser.')
    // A skip with a reason needs a look, as the end notification counts it.
    await expect(page.getByTestId('updates-headline')).toHaveText('1 updated, 1 needs attention')
  })

  test('a cancelled item and a skip with no reason do not need attention', async ({ page }) => {
    await fleet(page)
    const run = runOf([runItem('gallery:kokoro', 'updated'), runItem('gallery:piper', 'cancelled'), runItem('model:qwen3-8b', 'skipped')], { state: 'cancelled' })
    await mockUpdateRuns(page, { active: [run], polls: [run] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('Update run cancelled, 1 updated')
  })

  test('a finished run stays on the page after a reload until it is dismissed', async ({ page }) => {
    await fleet(page)
    // The server lists the finished run (not dismissed); ?active=1 lists none.
    const runs = await mockUpdateRuns(page, { active: [ENDED({ retryable: false })], polls: [ENDED({ retryable: false })] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('5 updated, 1 needs attention')
    await page.reload()
    await expect(page.getByTestId('updates-headline')).toHaveText('5 updated, 1 needs attention')
    await expect(runRow(page, 'addon:notes')).toBeVisible()
    await page.getByTestId('updates-dismiss-run').click()
    await expect.poll(() => runs.dismissed).toEqual(['run-1'])
    await page.reload()
    await expect(page.getByTestId('updates-headline')).toHaveText(/updates available/)
    await expect(page.getByTestId('run-item')).toHaveCount(0)
  })

  test('an automatic run says so in its banner', async ({ page }) => {
    await fleet(page)
    const run = { ...RUNNING(), source: 'auto' }
    await mockUpdateRuns(page, { active: [run], polls: [run] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-run-auto')).toHaveText('Automatic update')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
  })

  test('a manual run has no automatic label', async ({ page }) => {
    await fleet(page)
    await mockUpdateRuns(page, { active: [RUNNING()], polls: [RUNNING()] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
    await expect(page.getByTestId('updates-run-auto')).toHaveCount(0)
  })

  test('the filter counts the items of the run: running and waiting as updating, rolled back as needing you', async ({ page }) => {
    await fleet(page)
    const live = runOf([
      runItem('gallery:kokoro', 'rolled_back', { error_key: 'start', error: 'the backend process exited before it answered a health check (exit code 3)', retryable: true }),
      runItem('gallery:piper', 'updated'),
      runItem('gallery:sglang', 'running', { progress: 30, message: 'Downloading 30%' }),
      runItem('gallery:llama-cpp', 'queued', { queued_reason: 'backend_limit' }),
    ])
    await mockUpdateRuns(page, { active: [live], polls: [live] })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-filter-updating').locator('.ui-seg__count')).toHaveText('2')
    await expect(page.getByTestId('updates-filter-needs-you').locator('.ui-seg__count')).toHaveText('1')
    // The rolled-back item reads the same word as the chip of the run.
    await expect(runRow(page, 'gallery:kokoro').getByTestId('run-item-chip')).toHaveText('Rolled back')
    // The sentence says once that the new version did not start.
    await expect(runRow(page, 'gallery:kokoro').getByTestId('run-item-detail')).toHaveText(
      'The new version did not start: the backend process exited before it answered a health check (exit code 3). kokoro is still on v1.0.0 and your data is unchanged.')
    // The filter shows the items of the run it counts.
    await page.getByTestId('updates-filter-needs-you').click()
    await expect(page.getByTestId('run-item')).toHaveCount(1)
    await expect(runRow(page, 'gallery:kokoro')).toBeVisible()
    await page.getByTestId('updates-filter-updating').click()
    await expect(page.getByTestId('run-item')).toHaveCount(2)
  })

  test('after the run, a backend whose new build did not start reads Rolled back, not Failed', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('e2e-bad', '1.0.0', '2.0.0')]),
      installed: [installedBackend('e2e-bad', '1.0.0')],
      operations: [failedOp('e2e-bad', 'the backend process exited before it answered a health check (exit code 3)', { rolledBack: true, errorKey: 'start' })],
    })
    await page.goto('/app/updates')
    const row = rowOf(page, 'e2e-bad')
    await expect(row.getByTestId('update-row-chip')).toHaveText('Rolled back')
    await expect(row).toContainText('The new version did not start: the backend process exited before it answered a health check (exit code 3). e2e-bad is still on v1.0.0')
    await expect(page.getByTestId('updates-filter-needs-you').locator('.ui-seg__count')).toHaveText('1')
  })

  test('a release the automatic update holds back says why, and can still be updated by hand', async ({ page }) => {
    await mockOperate(page)
    await mockUpdates(page, {
      snapshot: snapshotOf([galleryItem('e2e-bad', '1.0.0', '2.0.0', { heldBack: true })]),
      installed: [installedBackend('e2e-bad', '1.0.0')],
    })
    await page.goto('/app/updates')
    const row = rowOf(page, 'e2e-bad')
    await expect(row).toContainText('Held back: this release did not start.')
    await expect(row.getByRole('button', { name: 'Update e2e-bad' })).toBeEnabled()
  })

  test('a model row\'s own Upgrade starts a run of one, and the page shows it at once', async ({ page }) => {
    const state = await fleet(page)
    const runs = await mockUpdateRuns(page, {})
    const one = runOf([runItem('model:qwen3-8b', 'running', { from: DIGEST_A, to: DIGEST_B, progress: 5, message: 'downloading' })], { id: 'run-9' })
    // The server answers the upgrade and lists the run of one it started.
    state.onModelAction = (action) => { if (action === 'upgrade') { runs.active = [one]; runs.polls = [one] } }
    await page.goto('/app/updates')
    await rowOf(page, 'qwen3-8b').getByRole('button', { name: 'Upgrade qwen3-8b' }).click()
    await page.getByTestId('model-upgrade-dialog').getByTestId('model-upgrade-confirm').click()
    // Well before the idle look for runs (every 15 seconds).
    await expect(page.getByTestId('updates-strip')).toHaveAttribute('data-run', 'run-9', { timeout: 4000 })
    await expect(runRow(page, 'model:qwen3-8b')).toBeVisible()
  })
})

test.describe('Updates: no runs on this server', () => {
  test('a server that keeps no update runs says so and nothing can start', async ({ page }) => {
    await fleet(page)
    await page.route(/\/api\/updates\/runs(\?.*)?$/, route => route.fulfill({ status: 503, json: { code: 'unavailable', key: '', message: 'update runs are not available' } }))
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-runs-unavailable')).toHaveText('Update runs are not available on this server.')
    await expect(page.getByTestId('updates-update-ready')).toBeDisabled()
    await expect(rowOf(page, 'kokoro').getByRole('button', { name: 'Update kokoro' })).toBeDisabled()
  })

  test('a review that finds a run in progress offers it instead of a start', async ({ page }) => {
    await fleet(page)
    await mockUpdateRuns(page, { preview: items => ({ ...plan(items), active_run_id: 'run-1' }), polls: [RUNNING()] })
    await page.goto('/app/updates')
    await page.getByTestId('updates-update-ready').click()
    await expect(page.getByTestId('update-review-active')).toContainText('An update run is in progress.')
    await expect(page.getByTestId('update-review-start')).toBeDisabled()
    await page.getByTestId('update-review-follow').click()
    await expect(page).toHaveURL(/[?&]run=run-1/)
    await expect(page.getByTestId('updates-headline')).toHaveText('Updating 6 items')
  })
})
