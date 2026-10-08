import { test, expect } from './coverage-fixtures.js'
import { mockLedger, GALLERY, GB } from './ledger-fixtures.js'

// The Installed tab in the ledger's own vocabulary: the same table, state
// filters with counts, a state per row, Load or Stop on the row, the row menu,
// and sizes only where the gallery can state one. The fixture installs 13
// models, loads two, pins one and disables one.

const rows = (page) => page.locator('[data-testid="installed-models-rail-item"]')
const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const pane = (page) => page.locator('[data-testid="installed-models-pane"]')

async function open(page, options = {}, url = '/app/models?view=installed') {
  const state = await mockLedger(page, options)
  await page.goto(url)
  await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
  return state
}

const sizeOf = (name) => GALLERY.find((e) => e.name === name).estimate.sizeBytes

test.describe('Installed ledger - table', () => {
  test('every installed model is a row, with state filters that carry counts', async ({ page }) => {
    await open(page)
    await expect(rows(page)).toHaveCount(13)
    const tab = (name) => page.getByRole('tab', { name: new RegExp(`^${name}`) })
    await expect(tab('All')).toContainText('13')
    await expect(tab('Running')).toContainText('2')
    await expect(tab('Idle')).toContainText('10')
    await expect(tab('Disabled')).toContainText('1')
    await expect(tab('Pinned')).toContainText('1')
    await expect(tab('Distributed')).toContainText('0')
    await tab('Running').click()
    await expect(page).toHaveURL(/[?&]state=running/)
    await expect(rows(page)).toHaveCount(2)
    await expect(tab('Running')).toHaveAttribute('aria-selected', 'true')
  })

  test('each row says what state the model is in', async ({ page }) => {
    await open(page)
    await expect(row(page, 'qwen3-8b-instruct').locator('.ledger-state')).toHaveAttribute('data-state', 'running')
    await expect(row(page, 'qwen3-8b-instruct').locator('.ledger-state')).toContainText('Running')
    await expect(row(page, 'kokoro-82m').locator('.ledger-state')).toContainText('Idle')
    await expect(row(page, 'whisper-medium').locator('.ledger-state')).toHaveAttribute('data-state', 'disabled')
    await expect(row(page, 'whisper-medium').locator('.ledger-state')).toContainText('Disabled')
  })

  test('the row shows its backend and marks a pinned model', async ({ page }) => {
    await open(page)
    await expect(row(page, 'flux.1-schnell')).toContainText('stablediffusion-ggml')
    await expect(row(page, 'whisper-large-v3')).toContainText('whisper')
    await expect(row(page, 'qwen3-8b-instruct').getByRole('img', { name: 'Pinned' })).toBeVisible()
    await expect(row(page, 'kokoro-82m').getByRole('img')).toHaveCount(0)
  })

  test('Load, Stop and Enable sit on the row and call the same endpoints as the inspector', async ({ page }) => {
    const calls = []
    await mockLedger(page)
    await page.route('**/backend/load', (route) => { calls.push(['load', route.request().postDataJSON().model]); return route.fulfill({ json: {} }) })
    await page.route('**/backend/shutdown', (route) => { calls.push(['stop', route.request().postDataJSON().model]); return route.fulfill({ json: {} }) })
    await page.route('**/models/toggle-state/**', (route) => { calls.push(['toggle', new URL(route.request().url()).pathname]); return route.fulfill({ json: {} }) })
    await page.goto('/app/models?view=installed')
    await expect(rows(page).first()).toBeVisible()

    await row(page, 'kokoro-82m').getByRole('button', { name: 'Load', exact: true }).click()
    await expect.poll(() => calls.length).toBe(1)
    expect(calls[0]).toEqual(['load', 'kokoro-82m'])

    await row(page, 'whisper-medium').getByRole('button', { name: 'Enable model' }).click()
    await expect.poll(() => calls.length).toBe(2)
    expect(calls[1][1]).toContain('/models/toggle-state/whisper-medium/enable')

    // Stopping asks first, as it always has.
    await row(page, 'qwen3-8b-instruct').getByRole('button', { name: 'Stop', exact: true }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Stop model qwen3-8b-instruct?')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop', exact: true }).click()
    await expect.poll(() => calls.length).toBe(3)
    expect(calls[2]).toEqual(['stop', 'qwen3-8b-instruct'])
  })

  test('the row menu offers disable, pin, edit, logs and delete, and delete asks first', async ({ page }) => {
    const state = await open(page)
    await row(page, 'kokoro-82m').getByRole('button', { name: 'Actions for kokoro-82m' }).click()
    const menu = page.getByRole('menu')
    for (const name of ['Disable model', 'Pin (prevent idle unload)', 'Edit configuration', 'Backend logs', 'Delete model']) {
      await expect(menu.getByRole('menuitem', { name })).toBeVisible()
    }
    await menu.getByRole('menuitem', { name: 'Delete model' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete model kokoro-82m?')
    // Nothing is sent until the question is answered.
    expect(state.deletes).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Delete model' }).click()
    await expect.poll(() => state.deletes).toEqual(['kokoro-82m'])
    await expect(row(page, 'kokoro-82m')).toHaveCount(0)
  })

  test('a menu on a row opens that row, not the one selected', async ({ page }) => {
    await open(page)
    await row(page, 'bge-m3').click()
    await row(page, 'kokoro-82m').getByRole('button', { name: 'Actions for kokoro-82m' }).click()
    await page.getByRole('menuitem', { name: 'Backend logs' }).click()
    await expect(page).toHaveURL(/\/app\/backend-logs\/kokoro-82m/)
  })
})

test.describe('Installed ledger - sizes', () => {
  test('a size appears where the gallery lists the model, and a dash where it does not', async ({ page }) => {
    await open(page)
    await expect(row(page, 'qwen3-14b-instruct-q4')).toContainText(`${(sizeOf('qwen3-14b-instruct-q4') / GB).toFixed(1)} GB`)
    const unknown = row(page, 'my-finetune-q4').locator('.ledger-size')
    await expect(unknown).toContainText('—')
    await expect(unknown.locator('span')).toHaveAttribute('title', /does not list this model/)
  })

  test('sorting by size puts models with no size last in both directions', async ({ page }) => {
    await open(page)
    // Sizes arrive in the background; wait for the last one to land.
    await expect(row(page, 'sdxl-turbo')).toContainText('GB')
    const header = page.getByRole('columnheader', { name: /Size/ })
    await header.getByRole('button').click()
    await expect(header).toHaveAttribute('aria-sort', 'ascending')
    let names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    expect(names[0]).toBe('kokoro-82m')
    expect(names[names.length - 1]).toBe('my-finetune-q4')
    await header.getByRole('button').click()
    await expect(header).toHaveAttribute('aria-sort', 'descending')
    names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    expect(names[0]).toBe('llama-3.3-70b-instruct-iq2')
    expect(names[names.length - 1]).toBe('my-finetune-q4')
  })
})

test.describe('Installed ledger - inspector and keys', () => {
  test('selecting a row opens the inspector with the same actions', async ({ page }) => {
    await open(page)
    await row(page, 'kokoro-82m').click()
    await expect(row(page, 'kokoro-82m')).toHaveAttribute('data-selected', 'true')
    await expect(pane(page).getByRole('heading', { name: 'kokoro-82m' })).toBeVisible()
    await expect(pane(page).getByRole('button', { name: 'Load', exact: true })).toBeVisible()
    await expect(pane(page).getByRole('button', { name: 'Actions for kokoro-82m' })).toBeVisible()
    await expect(page).toHaveURL(/model=kokoro-82m/)
  })

  test('arrows move, Escape closes, "/" searches and "d" changes density', async ({ page }) => {
    await open(page)
    const names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    await row(page, names[0]).click()
    await page.keyboard.press('ArrowDown')
    await expect(row(page, names[1])).toBeFocused()
    await expect(row(page, names[1])).toHaveAttribute('data-selected', 'true')
    await page.keyboard.press('Escape')
    await expect(page).not.toHaveURL(/model=/)
    await expect(row(page, names[1])).toBeFocused()

    await page.keyboard.press('d')
    await expect(page.locator('table.ledger-table')).toHaveClass(/dk-table--compact/)
    await page.keyboard.press('/')
    await expect(page.getByRole('textbox', { name: 'Search installed models' })).toBeFocused()
  })

  test('Free up space opens the cleanup review', async ({ page }) => {
    await open(page)
    await page.getByTestId('installed-cleanup').click()
    await expect(page.getByRole('dialog', { name: 'Disk and cleanup' })).toBeVisible()
  })
})

test.describe('Installed ledger - states', () => {
  test('nothing installed: the next step is named', async ({ page }) => {
    await mockLedger(page, { installed: [] })
    await page.goto('/app/models?view=installed')
    const empty = page.getByTestId('installed-empty')
    await expect(empty).toBeVisible({ timeout: 10_000 })
    await expect(empty).toContainText('No models installed yet')
    await expect(empty.getByRole('button', { name: 'Explore models' })).toBeVisible()
    await expect(empty.getByRole('button', { name: 'Import model' })).toBeVisible()
  })

  test('the first load draws skeleton rows', async ({ page }) => {
    await mockLedger(page)
    await page.route('**/api/models/capabilities', async (route) => {
      await new Promise((r) => setTimeout(r, 1500))
      return route.fulfill({ json: { data: [{ id: 'only', backend: 'llama-cpp', capabilities: [] }] } })
    })
    await page.goto('/app/models?view=installed')
    await expect(page.getByTestId('gallery-loader')).toBeVisible({ timeout: 5_000 })
    await expect(rows(page).first()).toBeVisible({ timeout: 10_000 })
  })

  test('a failed list is an inline alert with Retry', async ({ page }) => {
    await mockLedger(page)
    let fail = true
    await page.route('**/api/models/capabilities', (route) => (fail
      ? route.fulfill({ status: 500, json: { error: 'down' } })
      : route.fulfill({ json: { data: [{ id: 'back-again', backend: 'llama-cpp', capabilities: [] }] } })))
    await page.route('**/v1/models', (route) => route.fulfill({ status: 500, json: { error: 'down' } }))
    await page.goto('/app/models?view=installed')
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load installed models' })
    await expect(alert).toBeVisible({ timeout: 10_000 })
    fail = false
    await alert.getByRole('button', { name: 'Retry' }).click()
    await expect(row(page, 'back-again')).toBeVisible()
  })

  test('a filter that matches nothing offers to clear', async ({ page }) => {
    await open(page)
    await page.getByRole('textbox', { name: 'Search installed models' }).fill('zzz-nothing')
    await expect(page.getByText('No installed models match these filters.')).toBeVisible()
    await page.getByRole('button', { name: 'Clear filters' }).click()
    await expect(rows(page)).toHaveCount(13)
    await expect(page).not.toHaveURL(/[?&]q=/)
  })
})

test.describe('Installed ledger - phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('rows are name, state and action, and the page stays in its width', async ({ page }) => {
    await open(page)
    const r = row(page, 'kokoro-82m')
    expect(await r.evaluate((el) => getComputedStyle(el).display)).toBe('grid')
    await expect(r.getByRole('button', { name: 'Load', exact: true })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
