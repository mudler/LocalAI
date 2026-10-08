import { test, expect } from './coverage-fixtures.js'
import { mockLedger, storageSpec, GALLERY, GB } from './ledger-fixtures.js'

// What the models directory really holds, from GET /api/models/storage: a real
// Size column on the Installed table, the files of a model on its page, and
// the cleanup review counting only what removing a model really frees. A file
// two installed models use stays on disk when one of them goes.
//
// storageSpec() in the fixtures: qwen3-14b-instruct (15.7 GB + a 1.2 GB
// tokenizer) and qwen3-14b-instruct-q4 (9.0 GB + the same tokenizer) share the
// tokenizer; the two whisper models share a 0.2 GB filter file; my-finetune-q4
// names a projector file that is not on disk.

const rows = (page) => page.locator('[data-testid="installed-models-rail-item"]')
const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const pane = (page) => page.getByTestId('installed-models-pane')
const strip = (page) => page.getByTestId('disk-strip')
const sheet = (page) => page.getByTestId('cleanup-sheet')
const item = (page, name) => sheet(page).locator('li', { has: page.locator('.ledger-cr__name', { hasText: name }) })
const gb1 = (bytes) => (bytes / GB).toFixed(1)
const sizeOf = (name) => GALLERY.find((e) => e.name === name).estimate.sizeBytes

async function openInstalled(page, options = {}, url = '/app/models?view=installed') {
  const state = await mockLedger(page, { storage: storageSpec(), ...options })
  await page.goto(url)
  await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
  return state
}

async function openSheet(page, options = {}) {
  const state = await mockLedger(page, { storage: storageSpec(), ...options })
  await page.goto('/app/models')
  await expect(strip(page)).toBeVisible({ timeout: 15_000 })
  await strip(page).click()
  await expect(sheet(page)).toBeVisible()
  await expect(sheet(page).locator('.dk-sheet-body')).toHaveAttribute('aria-busy', 'false', { timeout: 15_000 })
  return state
}

test.describe('Installed table - size on disk', () => {
  test('the Size column holds what each model takes on disk, with the shared part under it', async ({ page }) => {
    await openInstalled(page)
    await expect(row(page, 'bge-m3').locator('.ledger-size')).toContainText('0.6 GB')
    // 15.7 GB of weights and the 1.2 GB tokenizer another build also uses.
    await expect(row(page, 'qwen3-14b-instruct').locator('.ledger-size')).toContainText('16.9 GB')
    await expect(row(page, 'qwen3-14b-instruct').locator('.ledger-size')).toContainText('1.2 GB shared')
    await expect(row(page, 'whisper-medium').locator('.ledger-size')).toContainText('0.2 GB shared')
    // A model that shares nothing has no second line.
    await expect(row(page, 'bge-m3').locator('.ledger-size__shared')).toHaveCount(0)
    // The exact figure is on the tooltip.
    await expect(row(page, 'bge-m3').locator('.ledger-size span').first()).toHaveAttribute('title', /on disk/)
  })

  test('a model that is not in the gallery still has a size, because the disk knows it', async ({ page }) => {
    await openInstalled(page)
    await expect(row(page, 'my-finetune-q4').locator('.ledger-size')).toContainText('4.6 GB')
  })

  test('a size read from disk is not marked as an estimate', async ({ page }) => {
    await openInstalled(page)
    const cell = row(page, 'sdxl-turbo').locator('.ledger-size')
    await expect(cell).toContainText('6.9 GB')
    await expect(cell.locator('span').first()).toHaveAttribute('title', /on disk/)
    await expect(cell.locator('[title^="Estimated"]')).toHaveCount(0)
  })

  test('a model the report has no files for falls back to the gallery estimate', async ({ page }) => {
    const spec = storageSpec()
    delete spec.models['kokoro-82m']
    await openInstalled(page, { storage: spec })
    const cell = row(page, 'kokoro-82m').locator('.ledger-size')
    await expect(cell).toContainText(`${gb1(sizeOf('kokoro-82m'))} GB`)
    await expect(cell.locator('span').first()).toHaveAttribute('title', /Estimated from the gallery/)
  })

  test('sorting by size uses the size on disk', async ({ page }) => {
    await openInstalled(page)
    const header = page.getByRole('columnheader', { name: /Size/ })
    await header.getByRole('button').click()
    let names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    expect(names[0]).toBe('kokoro-82m')
    expect(names[names.length - 1]).toBe('llama-3.3-70b-instruct-iq2')
    await header.getByRole('button').click()
    names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    expect(names[0]).toBe('llama-3.3-70b-instruct-iq2')
    expect(names[names.length - 1]).toBe('kokoro-82m')
  })

  test('the footer gives the total on disk, the shared part and the missing references', async ({ page }) => {
    await openInstalled(page)
    const summary = page.getByTestId('installed-models-storage-summary')
    await expect(summary).toContainText('on disk')
    await expect(summary).toContainText('shared across models')
    await expect(summary).toContainText('1 missing references')
  })

  test('a model whose config names a missing file is marked on its row', async ({ page }) => {
    await openInstalled(page)
    await expect(row(page, 'my-finetune-q4').getByRole('img', { name: '1 referenced file missing' })).toBeVisible()
    await expect(row(page, 'bge-m3').getByRole('img', { name: /missing/ })).toHaveCount(0)
  })

  for (const [label, status] of [['the call fails', 500], ['the user is not an admin', 403]]) {
    test(`when ${label}, sizes fall back to the gallery and nothing else breaks`, async ({ page }) => {
      await openInstalled(page, { storageStatus: status })
      const q4 = row(page, 'qwen3-14b-instruct-q4').locator('.ledger-size')
      await expect(q4).toContainText(`${gb1(sizeOf('qwen3-14b-instruct-q4'))} GB`)
      await expect(q4.locator('span').first()).toHaveAttribute('title', /Estimated from the gallery/)
      await expect(row(page, 'qwen3-14b-instruct-q4').locator('.ledger-size__shared')).toHaveCount(0)
      // A model the gallery does not list says its size is not known.
      const unknown = row(page, 'my-finetune-q4').locator('.ledger-size')
      await expect(unknown).toContainText('—')
      await expect(unknown.locator('span')).toHaveAttribute('title', /size on disk is not known/)
      await expect(page.getByTestId('installed-models-storage-summary')).toHaveCount(0)
      await expect(page.locator('[role="alert"]')).toHaveCount(0)
    })
  }
})

test.describe('Installed table - inspector', () => {
  test('the inspector shows the size and names the models a model shares files with', async ({ page }) => {
    await openInstalled(page)
    await row(page, 'qwen3-14b-instruct').click()
    await expect(pane(page)).toContainText('16.9 GB')
    const shared = pane(page).getByRole('button', { name: 'qwen3-14b-instruct-q4' })
    await expect(shared).toBeVisible()
    await shared.click()
    await expect(row(page, 'qwen3-14b-instruct-q4')).toHaveAttribute('data-selected', 'true')
  })

  test('a model with a missing file says so in the inspector', async ({ page }) => {
    await openInstalled(page)
    await row(page, 'my-finetune-q4').click()
    await expect(pane(page)).toContainText('Missing files')
    await expect(pane(page)).toContainText('1 referenced file missing')
  })
})

test.describe('Model page - files on disk', () => {
  const usage = '/app/models/qwen3-14b-instruct?tab=usage'

  test('the Usage and history tab lists the files with their size and who shares them', async ({ page }) => {
    await mockLedger(page, { storage: storageSpec() })
    await page.goto(usage)
    const files = page.getByTestId('model-page-files')
    await expect(files.getByTestId('disk-file-row')).toHaveCount(2)
    await expect(files).toContainText('16.9 GB in 2 files')
    const weights = files.locator('[data-testid="disk-file-row"]', { hasText: 'qwen3-14b-instruct.gguf' })
    await expect(weights).toContainText('15.7 GB')
    const tokenizer = files.locator('[data-testid="disk-file-row"]', { hasText: 'qwen3-14b.tokenizer.json' })
    await expect(tokenizer).toContainText('1.2 GB')
    await expect(tokenizer).toContainText('Also used by qwen3-14b-instruct-q4')
    await expect(tokenizer.getByRole('link', { name: 'qwen3-14b-instruct-q4' })).toHaveAttribute('href', /\/app\/models\/qwen3-14b-instruct-q4$/)
    await expect(weights).not.toContainText('Also used by')
    await expect(page.getByTestId('usage-size')).toContainText('16.9 GB')
    await expect(page.getByTestId('usage-size')).toContainText('1.2 GB shared with other models')
    // The usage history is still not recorded.
    await expect(page.getByTestId('usage-empty')).toContainText('not recorded yet')
  })

  test('a file the config names that is not on disk is flagged', async ({ page }) => {
    await mockLedger(page, { storage: storageSpec() })
    await page.goto('/app/models/my-finetune-q4?tab=usage')
    const missing = page.locator('[data-testid="disk-file-row"][data-missing="true"]')
    await expect(missing).toHaveCount(1)
    await expect(missing).toContainText('my-finetune-q4.mmproj.gguf')
    await expect(missing).toContainText('missing')
    await expect(page.getByTestId('files-missing-banner')).toContainText('not on disk')
    // The files that exist come first.
    await expect(page.getByTestId('disk-file-row').first()).toHaveAttribute('data-missing', 'false')
  })

  test('the Overview shows the size on disk and a missing-files badge', async ({ page }) => {
    await mockLedger(page, { storage: storageSpec() })
    await page.goto('/app/models/my-finetune-q4')
    await expect(page.getByTestId('overview-size')).toContainText('4.6 GB')
    await expect(page.getByTestId('overview-missing')).toContainText('1 referenced file missing')
    await page.getByTestId('overview-size').getByRole('button', { name: 'See the files' }).click()
    await expect(page.getByTestId('model-page-files')).toBeVisible()
  })

  for (const status of [403, 500]) {
    test(`with the report unavailable (${status}), the tab says so instead of listing files`, async ({ page }) => {
      await mockLedger(page, { storage: storageSpec(), storageStatus: status })
      await page.goto(usage)
      await expect(page.getByTestId('files-on-disk-none')).toContainText('admins only')
      await expect(page.getByTestId('disk-file-row')).toHaveCount(0)
      await expect(page.getByTestId('usage-empty')).toBeVisible()
    })
  }
})

test.describe('Cleanup sheet - what a removal really frees', () => {
  test('a model is sized by the files it does not share', async ({ page }) => {
    await openSheet(page)
    // 9.0 GB of its own, plus the 1.2 GB tokenizer the other build keeps.
    const q4 = item(page, 'qwen3-14b-instruct-q4')
    await expect(q4.locator('.ledger-cr__size')).toHaveText('9.0 GB')
    await expect(q4.getByTestId('cleanup-shared')).toContainText('Shares 1.2 GB with qwen3-14b-instruct.')
    await expect(q4.getByTestId('cleanup-shared')).toContainText('stay on disk')
  })

  test('selecting a model frees its own bytes only, not the shared ones', async ({ page }) => {
    await openSheet(page)
    await item(page, 'qwen3-14b-instruct-q4').getByRole('checkbox').check()
    const effect = page.getByTestId('cleanup-effect')
    await expect(effect).toContainText('Frees 9.0 GB.')
    await expect(effect).not.toContainText('10.2')
  })

  test('the review before removal lists the same bytes', async ({ page }) => {
    await openSheet(page)
    await item(page, 'qwen3-14b-instruct-q4').getByRole('checkbox').check()
    await page.getByTestId('cleanup-remove').click()
    const dry = page.getByTestId('cleanup-dry-run')
    await expect(dry).toContainText('qwen3-14b-instruct-q4')
    await expect(dry).toContainText('9.0 GB')
    await expect(page.getByRole('alertdialog')).toContainText('This frees 9.0 GB.')
  })

  test('two models that share a file give it back together, and neither does alone', async ({ page }) => {
    const spec = {
      files: { 'flux.gguf': 12 * GB, 'sdxl.safetensors': 7 * GB, 'vae.safetensors': 1 * GB },
      models: { 'flux.1-schnell': ['flux.gguf', 'vae.safetensors'], 'sdxl-turbo': ['sdxl.safetensors', 'vae.safetensors'] },
    }
    await openSheet(page, { storage: spec })
    const effect = page.getByTestId('cleanup-effect')
    await item(page, 'flux.1-schnell').getByRole('checkbox').check()
    await expect(effect).toContainText('Frees 12.0 GB.')
    await item(page, 'sdxl-turbo').getByRole('checkbox').check()
    await expect(effect).toContainText('Frees 20.0 GB.')
    await item(page, 'flux.1-schnell').getByRole('checkbox').uncheck()
    await expect(effect).toContainText('Frees 7.0 GB.')
  })

  test('a model whose files are all shared frees nothing alone, and says why', async ({ page }) => {
    const spec = {
      files: { 'flux.gguf': 12 * GB, 'sdxl.bin': 2 * GB },
      models: { 'flux.1-schnell': ['flux.gguf', 'sdxl.bin'], 'sdxl-turbo': ['sdxl.bin'] },
    }
    await openSheet(page, { storage: spec })
    const sdxl = item(page, 'sdxl-turbo')
    await expect(sdxl.locator('.ledger-cr__size')).toHaveText('0.0 GB')
    await expect(sdxl.getByTestId('cleanup-shared')).toContainText('Shares 2.0 GB with flux.1-schnell')
    await sdxl.getByRole('checkbox').check()
    await expect(page.getByTestId('cleanup-effect')).toContainText('Frees 0.0 GB.')
    await expect(page.getByTestId('cleanup-effect')).not.toContainText('size is unknown')
  })

  test('the sheet says sizes are read from the disk and leaves out shared files', async ({ page }) => {
    await openSheet(page)
    await expect(page.getByTestId('cleanup-sizes')).toContainText('what each model uses on disk')
    await expect(page.getByTestId('cleanup-sizes')).toContainText('another installed model also uses is left out')
    // The usage history is still not recorded, and the sheet still says so.
    await expect(page.getByTestId('cleanup-honesty')).toContainText('Usage history is not recorded yet.')
  })

  test('a model that names files that are not on disk is a finding', async ({ page }) => {
    await openSheet(page)
    await expect(page.getByTestId('cleanup-missing')).toContainText('Config points at files that are not on disk: my-finetune-q4')
    await expect(item(page, 'my-finetune-q4').getByTestId('cleanup-missing-item')).toContainText('Config points at 1 file that is not on disk.')
    await expect(item(page, 'bge-m3').getByTestId('cleanup-missing-item')).toHaveCount(0)
  })

  test('a protected model shares its files too, and the sheet says which', async ({ page }) => {
    await openSheet(page)
    const group = page.getByTestId('cleanup-protected')
    await group.getByRole('button').click()
    await expect(group.locator('li', { hasText: 'whisper-large-v3' })).toContainText('Shares 0.2 GB with whisper-medium')
  })

  for (const status of [500, 403]) {
    test(`when the report cannot be read (${status}), sizes are the gallery's and the sheet says so`, async ({ page }) => {
      await openSheet(page, { storageStatus: status })
      await expect(page.getByTestId('cleanup-sizes')).toContainText('could not be read')
      await expect(page.getByTestId('cleanup-shared')).toHaveCount(0)
      await expect(page.getByTestId('cleanup-missing')).toHaveCount(0)
      await expect(item(page, 'qwen3-14b-instruct-q4').locator('.ledger-cr__size')).toHaveText(`${gb1(sizeOf('qwen3-14b-instruct-q4'))} GB`)
      await expect(item(page, 'my-finetune-q4').locator('.ledger-cr__size')).toHaveText('size unknown')
      await expect(page.getByTestId('cleanup-honesty')).toContainText('Usage history is not recorded yet.')
    })
  }
})
