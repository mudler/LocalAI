import { test, expect } from './coverage-fixtures.js'
import { mockLedger } from './ledger-fixtures.js'
import { modelItem, modelSnapshotOf } from './updates-fixtures.js'

// The Models page and the model update center: a mark on a model with an
// update or a hold, the update state in the inspector with the "Hold this
// version" switch, and the button that reloads the configs from disk, which
// is called Reload so that "Update" means an upgrade.

const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const pane = (page) => page.locator('[data-testid="installed-models-pane"]')

async function open(page, items) {
  const state = await mockLedger(page)
  const models = { snapshot: modelSnapshotOf(items), calls: [] }
  await page.route('**/api/models/updates', route => route.fulfill({ json: models.snapshot }))
  await page.route(/\/api\/models\/[^/]+\/hold$/, (route) => {
    const body = route.request().postDataJSON()
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3])
    models.calls.push({ name, body })
    models.snapshot = modelSnapshotOf(items.map(it => (it.name === name
      ? { ...it, state: body.hold ? 'held' : 'update-available', held: body.hold, needsPerson: !body.hold }
      : it)))
    return route.fulfill({ json: { name, hold: body.hold } })
  })
  await page.goto('/app/models?view=installed')
  await expect(page.locator('[data-testid="installed-models-rail-item"]').first()).toBeVisible({ timeout: 15_000 })
  return { state, models }
}

test.describe('Installed models: updates', () => {
  test('a model with an update or a hold carries a mark, and the reload button says Reload', async ({ page }) => {
    await open(page, [modelItem('kokoro-82m', 'update-available'), modelItem('flux.1-schnell', 'held', { held: true })])
    await expect(row(page, 'kokoro-82m').getByRole('img', { name: 'Update available' })).toBeVisible()
    await expect(row(page, 'flux.1-schnell').getByRole('img', { name: 'Held at this version' })).toBeVisible()
    await expect(row(page, 'whisper-large-v3').getByRole('img', { name: /Update available|Held/ })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Update', exact: true })).toHaveCount(0)
  })

  test('the inspector shows the update and holds the version with a switch that applies at once', async ({ page }) => {
    const { models } = await open(page, [modelItem('kokoro-82m', 'update-available')])
    await row(page, 'kokoro-82m').click()
    const card = pane(page).getByTestId('model-update-card')
    await expect(card).toHaveAttribute('data-state', 'ready')
    await expect(card.getByTestId('model-update-chip')).toContainText('Update available')
    await expect(card.getByRole('button', { name: 'Upgrade' })).toBeVisible()
    const hold = card.getByRole('switch', { name: 'Hold this version' })
    await expect(hold).toHaveAttribute('aria-checked', 'false')
    await hold.click()
    await expect.poll(() => models.calls).toEqual([{ name: 'kokoro-82m', body: { hold: true } }])
    await expect(card).toHaveAttribute('data-state', 'on-hold')
    await expect(hold).toHaveAttribute('aria-checked', 'true')
    await expect(row(page, 'kokoro-82m').getByRole('img', { name: 'Held at this version' })).toBeVisible()
    await hold.click()
    await expect.poll(() => models.calls.length).toBe(2)
    expect(models.calls[1]).toEqual({ name: 'kokoro-82m', body: { hold: false } })
    await expect(card).toHaveAttribute('data-state', 'ready')
  })

  test('Upgrade in the inspector opens the same confirmation as the Updates page', async ({ page }) => {
    await open(page, [modelItem('kokoro-82m', 'update-available', {
      changedFiles: [{ path: 'kokoro.onnx', role: 'weights', change: 'changed', checksum: true }],
    })])
    await row(page, 'kokoro-82m').click()
    await pane(page).getByTestId('model-card-upgrade').click()
    const dialog = page.getByTestId('model-upgrade-dialog')
    await expect(dialog.getByRole('heading', { name: 'Upgrade kokoro-82m?' })).toBeVisible()
    await expect(dialog).toContainText('kokoro.onnx')
    await dialog.getByTestId('model-upgrade-cancel').click()
    await expect(dialog).toHaveCount(0)
  })

  test('a model that is not from a gallery has no update card', async ({ page }) => {
    await open(page, [])
    await row(page, 'kokoro-82m').click()
    await expect(pane(page).getByRole('heading', { name: 'kokoro-82m' })).toBeVisible()
    await expect(pane(page).getByTestId('model-update-card')).toHaveCount(0)
  })
})
