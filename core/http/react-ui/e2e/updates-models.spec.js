import { test, expect } from './coverage-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { adminItem } from './admin-nav.js'
import {
  DIGEST_B, galleryItem, minutesAgo, mockUpdates, modelFiles, modelItem, modelOp, modelSnapshotOf, snapshotOf,
} from './updates-fixtures.js'

// The model rows of the Updates page: every state of the model read model,
// the upgrade confirmation with the edited keys, a running upgrade, a failed
// one and its retry, the removal of the old version and the hold. The API is
// stubbed; the page, the shell provider and the rules are the real ones.

const rowOf = (page, name) => page.locator(`[data-testid="update-row"][data-name="${name}"]`)
const dialog = (page) => page.getByTestId('model-upgrade-dialog')

const EDITED = [
  { key: 'context_size', installed: 4096, mine: 16384, gallery: 8192, conflict: true },
  { key: 'parameters.temperature', installed: 0.7, mine: 0.2, gallery: 0.6, conflict: true },
  { key: 'f16', installed: false, mine: true },
]

async function setup(page, items, over = {}) {
  await mockOperate(page)
  return mockUpdates(page, {
    snapshot: snapshotOf([]),
    models: modelSnapshotOf(items),
    ...over,
  })
}

test.describe('Updates page: models', () => {
  test('each model state has its group, chip, words and next step', async ({ page }) => {
    await setup(page, [
      modelItem('qwen3-8b', 'update-available', { changedFiles: modelFiles() }),
      modelItem('llama-3.2-3b', 'edited', { editedKeys: EDITED, changedFiles: modelFiles() }),
      modelItem('whisper-base', 'failed', { upgrade: { phase: 'rolled-back', error: 'the model did not load', attempts: 1 } }),
      modelItem('phi-4', 'held', { held: true }),
      modelItem('old-model', 'unknown', { reason: 'legacy-sidecar', availableDigest: '' }),
      modelItem('retired', 'gone'),
      modelItem('gemma-3', 'up-to-date', { upgrade: { phase: 'confirmed', removableOld: true, retainedFiles: ['gemma-3-old.gguf', 'gemma-3-old.tmpl'] } }),
      modelItem('kokoro', 'up-to-date'),
    ])
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('3 updates available')
    await expect(page.locator('[data-source="models"]')).toContainText('Model gallery')

    const ready = rowOf(page, 'qwen3-8b')
    await expect(ready).toHaveAttribute('data-state', 'ready')
    await expect(ready).toHaveAttribute('data-kind', 'model')
    await expect(ready).toContainText('Model')
    await expect(ready).toContainText('Gallery localai')
    await expect(ready).toContainText('sha256:1a2b3c4d5e6f to sha256:9f8e7d6c5b4a')
    await expect(ready).toContainText('3 files change.')
    await expect(ready.getByTestId('update-row-chip')).toContainText('Update available')
    await expect(ready.getByRole('button', { name: 'Upgrade qwen3-8b' })).toBeVisible()
    // A model row has no checkbox: it is picked in the review.
    await expect(ready.locator('input[type="checkbox"]')).toHaveCount(0)

    const edited = rowOf(page, 'llama-3.2-3b')
    await expect(edited.getByTestId('update-row-chip')).toContainText('Edited')
    await expect(edited).toContainText('You edited 2 keys that this update also changes')

    const failed = rowOf(page, 'whisper-base')
    await expect(failed).toHaveAttribute('data-state', 'failed')
    await expect(failed).toContainText('the old version was put back: the model did not load')
    await expect(failed).toContainText('The old version keeps serving.')
    await expect(failed.getByRole('button', { name: 'Retry whisper-base' })).toBeVisible()

    const other = page.locator('[data-group="other"]')
    await expect(other.getByRole('heading', { name: /Other models/ })).toBeVisible()
    await expect(rowOf(page, 'phi-4').getByTestId('update-row-chip')).toContainText('Held')
    await expect(rowOf(page, 'phi-4')).toContainText('Held at this version. The gallery has a newer one.')
    await expect(rowOf(page, 'phi-4').getByRole('button', { name: 'Release the hold on phi-4' })).toBeVisible()
    await expect(rowOf(page, 'old-model')).toContainText('Adopt it to get updates')
    await expect(rowOf(page, 'old-model').getByTestId('update-row-chip')).toContainText('Not known')
    await expect(rowOf(page, 'retired').getByTestId('update-row-chip')).toContainText('Not in the gallery')
    await expect(rowOf(page, 'gemma-3')).toContainText('The old version stays on disk (2 files)')
    await expect(rowOf(page, 'gemma-3').getByRole('button', { name: 'Remove the old version of gemma-3' })).toBeVisible()
    // Up to date is the collapsed line.
    await expect(page.locator('[data-group="current"]')).toContainText('kokoro')

    // The badge counts the update, the edited update and the failure, never the hold.
    await expect(adminItem(page, '/app/updates').getByTestId('admin-signal-updates')).toContainText('3')
  })

  test('the confirmation lists the files, says the loaded model stops, and asks a choice for each edited key', async ({ page }) => {
    const state = await setup(page, [modelItem('llama-3.2-3b', 'edited', { editedKeys: EDITED, changedFiles: modelFiles() })], {
      loaded: [{ id: 'llama-3.2-3b' }],
    })
    await page.goto('/app/updates')
    await rowOf(page, 'llama-3.2-3b').getByRole('button', { name: 'Upgrade llama-3.2-3b' }).click()
    const d = dialog(page)
    await expect(d.getByRole('heading', { name: 'Upgrade llama-3.2-3b?' })).toBeVisible()
    await expect(d.getByTestId('model-upgrade-loaded')).toContainText('llama-3.2-3b is loaded now.')
    await expect(d.getByTestId('model-upgrade-loaded')).toContainText('stops it')
    const files = d.getByTestId('model-upgrade-files')
    await expect(files).toContainText('qwen3-8b-Q4_K_M.gguf')
    await expect(files).toContainText('changed · weights')
    await expect(files).toContainText('new · vision projector')
    await expect(d.getByTestId('model-upgrade-disk')).toContainText('room for both')
    await expect(d.getByTestId('model-upgrade-key')).toHaveCount(2)
    await expect(d.getByTestId('model-upgrade-kept')).toContainText('f16')

    const confirm = d.getByTestId('model-upgrade-confirm')
    await expect(confirm).toHaveText('Stop and upgrade')
    await expect(confirm).toBeDisabled()
    await expect(d.getByTestId('model-upgrade-missing')).toHaveText('Choose a value for 2 keys')

    const ctx = d.locator('[data-key="context_size"]')
    await expect(ctx).toContainText('16384')
    await expect(ctx).toContainText('8192')
    await ctx.getByRole('radio', { name: /Keep mine/ }).check()
    await expect(confirm).toBeDisabled()
    await d.locator('[data-key="parameters.temperature"]').getByRole('radio', { name: /Take the gallery's/ }).check()
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect(d).toHaveCount(0)
    expect(state.modelCalls).toEqual([{ action: 'upgrade', name: 'llama-3.2-3b', body: { choices: { context_size: 'mine', 'parameters.temperature': 'gallery' } } }])
    await expect(page.getByTestId('updates-check-result')).toHaveText('The upgrade of llama-3.2-3b started.')
  })

  test('a refusal that names more keys keeps the dialog open and asks for them', async ({ page }) => {
    const state = await setup(page, [modelItem('qwen3-8b', 'update-available', { changedFiles: modelFiles() })], {
      loaded: [],
    })
    let refused = false
    state.modelReply = (action) => {
      if (action !== 'upgrade' || refused) return null
      refused = true
      return { status: 409, json: { error: 'the update changes keys that were edited; choose mine or gallery for each', keys: ['top_k'] } }
    }
    await page.goto('/app/updates')
    await rowOf(page, 'qwen3-8b').getByRole('button', { name: 'Upgrade qwen3-8b' }).click()
    const d = dialog(page)
    await expect(d.getByTestId('model-upgrade-idle')).toContainText('qwen3-8b is not loaded now')
    await expect(d.getByTestId('model-upgrade-confirm')).toHaveText('Upgrade')
    await d.getByTestId('model-upgrade-confirm').click()
    await expect(d.getByTestId('model-upgrade-error')).toContainText('asks for a choice on more keys')
    await expect(d.locator('[data-key="top_k"]')).toBeVisible()
    await expect(d.getByTestId('model-upgrade-confirm')).toBeDisabled()
    await d.locator('[data-key="top_k"]').getByRole('radio', { name: /Keep mine/ }).check()
    await d.getByTestId('model-upgrade-confirm').click()
    await expect(d).toHaveCount(0)
    expect(state.modelCalls.map(c => c.body)).toEqual([{}, { choices: { top_k: 'mine' } }])
  })

  test('a config-only update downloads nothing and sends config_only', async ({ page }) => {
    const state = await setup(page, [modelItem('qwen3-8b', 'update-available', {
      configOnly: true,
      changedFiles: [{ path: 'qwen3-8b.tmpl', role: 'template', change: 'changed', checksum: false }],
    })])
    await page.goto('/app/updates')
    await expect(rowOf(page, 'qwen3-8b')).toContainText('Only the config or the prompt templates change')
    await rowOf(page, 'qwen3-8b').getByRole('button', { name: 'Upgrade qwen3-8b' }).click()
    const d = dialog(page)
    await expect(d.getByTestId('model-upgrade-disk')).toHaveText('Nothing downloads.')
    await expect(d.getByTestId('model-upgrade-unchecked')).toHaveCount(0)
    const box = d.getByTestId('model-upgrade-config-only').getByRole('checkbox')
    await expect(box).toBeChecked()
    await d.getByTestId('model-upgrade-confirm').click()
    await expect(d).toHaveCount(0)
    expect(state.modelCalls[0].body).toEqual({ config_only: true })
  })

  test('a running upgrade shows its phase and can be cancelled only before the model stops', async ({ page }) => {
    const state = await setup(page, [modelItem('qwen3-8b', 'update-available', { changedFiles: modelFiles() })], {
      operations: [modelOp('qwen3-8b', 'staging')],
    })
    let cancelled = ''
    await page.route('**/api/operations/*/cancel', (route) => { cancelled = route.request().url(); return route.fulfill({ json: {} }) })
    await page.goto('/app/updates')
    const row = rowOf(page, 'qwen3-8b')
    await expect(row).toHaveAttribute('data-state', 'updating')
    await expect(row.getByTestId('update-row-chip')).toContainText('Updating')
    await expect(row).toContainText('Upgrading: downloading the new files')
    await expect(row.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '35')
    await expect(adminItem(page, '/app/updates').getByTestId('admin-signal-updates')).toHaveCount(0)
    await row.getByRole('button', { name: 'Cancel the update of qwen3-8b' }).click()
    await expect.poll(() => cancelled).toContain('/api/operations/job-qwen3-8b/cancel')

    state.operations = [modelOp('qwen3-8b', 'unloading')]
    await page.reload()
    await expect(row).toContainText('Upgrading: stopping the model')
    await expect(row.getByRole('button', { name: /Cancel/ })).toHaveCount(0)
  })

  test('a failed upgrade says the old version serves, and Retry runs it again', async ({ page }) => {
    const state = await setup(page, [modelItem('whisper-base', 'failed', { upgrade: { phase: 'failed', error: 'not enough disk space: 3 GB needed' } })])
    await page.goto('/app/updates')
    const row = rowOf(page, 'whisper-base')
    await expect(row).toContainText('The upgrade failed: not enough disk space: 3 GB needed. The old version keeps serving.')
    await expect(row.getByTestId('update-row-chip')).toHaveClass(/ui-chip--error/)
    // The server changes when it takes the retry. Changing it before the click
    // lets a refresh in between redraw the row, and the click finds it gone.
    state.onModelAction = (action, _name, _body, s) => {
      if (action !== 'retry') return
      s.models = modelSnapshotOf([modelItem('whisper-base', 'update-available')])
      s.operations = [modelOp('whisper-base', 'preflight')]
    }
    await row.getByRole('button', { name: 'Retry whisper-base' }).click()
    await expect.poll(() => state.modelCalls.map(c => `${c.action}:${c.name}`)).toEqual(['retry:whisper-base'])
    await expect(row).toHaveAttribute('data-state', 'updating')
    await expect(row).toContainText('checking the disk, the backend and the files')
  })

  test('the old version is removed only after a confirmation', async ({ page }) => {
    const state = await setup(page, [modelItem('gemma-3', 'up-to-date', { upgrade: { phase: 'confirmed', removableOld: true, retainedFiles: ['a.gguf', 'b.gguf'] } })])
    await page.goto('/app/updates')
    await rowOf(page, 'gemma-3').getByRole('button', { name: 'Remove the old version of gemma-3' }).click()
    const confirm = page.getByRole('alertdialog').filter({ hasText: 'Remove the old version of gemma-3?' })
    await expect(confirm).toContainText('This deletes 2 old files that the upgrade kept')
    expect(state.modelCalls).toEqual([])
    state.models = modelSnapshotOf([modelItem('gemma-3', 'up-to-date')])
    await confirm.getByRole('button', { name: 'Remove old version' }).click()
    await expect.poll(() => state.modelCalls.map(c => c.action)).toEqual(['remove-old'])
    await expect(page.getByTestId('updates-check-result')).toHaveText('gemma-3: 2 old files removed.')
    await expect(rowOf(page, 'gemma-3')).toHaveCount(0)
  })

  test('Hold this version takes a model out of the count, and Release hold puts it back', async ({ page }) => {
    const state = await setup(page, [modelItem('qwen3-8b', 'update-available', { changedFiles: modelFiles() })])
    await page.goto('/app/updates')
    await expect(adminItem(page, '/app/updates').getByTestId('admin-signal-updates')).toContainText('1')
    const row = rowOf(page, 'qwen3-8b')
    await row.getByRole('button', { name: 'More actions for qwen3-8b' }).click()
    state.onModelAction = (_action, _name, _body, s) => { s.models = modelSnapshotOf([modelItem('qwen3-8b', 'held', { held: true })]) }
    await page.getByRole('menuitem', { name: 'Hold this version' }).click()
    await expect.poll(() => state.modelCalls).toEqual([{ action: 'hold', name: 'qwen3-8b', body: { hold: true } }])
    await expect(row).toHaveAttribute('data-state', 'on-hold')
    await expect(adminItem(page, '/app/updates').getByTestId('admin-signal-updates')).toHaveCount(0)
    await expect(page.getByTestId('updates-check-result')).toHaveText('qwen3-8b is held at this version.')

    state.onModelAction = (_action, _name, _body, s) => { s.models = modelSnapshotOf([modelItem('qwen3-8b', 'update-available', { availableDigest: DIGEST_B })]) }
    await row.getByRole('button', { name: 'Release the hold on qwen3-8b' }).click()
    await expect.poll(() => state.modelCalls.length).toBe(2)
    expect(state.modelCalls[1]).toEqual({ action: 'hold', name: 'qwen3-8b', body: { hold: false } })
    await expect(row).toHaveAttribute('data-state', 'ready')
  })

  test('Check now checks the model gallery too, and backend and model rows sit together', async ({ page }) => {
    const state = await setup(page, [modelItem('kokoro', 'up-to-date')], {
      snapshot: snapshotOf([galleryItem('whisper', '1.7.2', '1.8.0')]),
    })
    state.onModelCheck = () => modelSnapshotOf([modelItem('kokoro', 'update-available')], { checkedAt: minutesAgo(0) })
    await page.goto('/app/updates')
    await expect(page.getByTestId('updates-headline')).toHaveText('1 update available')
    await page.getByTestId('updates-check-now').click()
    await expect(page.getByTestId('updates-headline')).toHaveText('2 updates available')
    await expect(page.locator('[data-group="ready"] [data-testid="update-row"]')).toHaveCount(2)
    expect(state.checks).toBe(1)
    expect(state.modelChecks).toBe(1)
    await expect(rowOf(page, 'kokoro')).toHaveAttribute('data-kind', 'model')
  })
})
