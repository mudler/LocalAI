import { test, expect } from './coverage-fixtures.js'
import { mockLedger, GALLERY, PROFILES, withDisk, GB } from './ledger-fixtures.js'

// The cleanup review: a sheet from the disk strip that ranks installed models
// by how safe they are to remove, from what the API really reports. The API
// records no last-used time or use count, so the sheet says so and ranks on
// structure only. Removal waits out an undo window in the browser; nothing is
// deleted before the window ends, and leaving the page deletes nothing.

const strip = (page) => page.getByTestId('disk-strip')
const sheet = (page) => page.getByTestId('cleanup-sheet')
const tier = (page, name) => page.getByTestId(`cleanup-tier-${name}`)
const sizeOf = (name) => GALLERY.find((e) => e.name === name).estimate.sizeBytes
const gb1 = (bytes) => (bytes / GB).toFixed(1)

async function openSheet(page, options = {}, { url = '/app/models', clock = false } = {}) {
  if (clock) await page.clock.install()
  const state = await mockLedger(page, options)
  await page.goto(url)
  await expect(strip(page)).toBeVisible({ timeout: 15_000 })
  await strip(page).click()
  await expect(sheet(page)).toBeVisible()
  await expect(sheet(page).locator('.dk-sheet-body')).toHaveAttribute('aria-busy', 'false', { timeout: 15_000 })
  return state
}

const selectSafe = (page) => tier(page, 'safe').getByRole('button', { name: 'Select all' }).click()
const removeButton = (page) => page.getByTestId('cleanup-remove')

// The one safe model in the fixture: the 4-bit build of a model whose 8-bit
// build is installed too, which the gallery would pick on this host.
const SAFE = 'qwen3-14b-instruct-q4'

test.describe('Cleanup sheet - dialog behaviour', () => {
  test('it is a modal dialog with a name, focus on Close, and Escape gives focus back', async ({ page }) => {
    await openSheet(page)
    const dialog = page.getByRole('dialog', { name: 'Disk and cleanup' })
    await expect(dialog).toHaveAttribute('aria-modal', 'true')
    await expect(page.getByRole('button', { name: 'Close' })).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(sheet(page)).toHaveCount(0)
    await expect(strip(page)).toBeFocused()
  })

  test('Tab stays inside the sheet', async ({ page }) => {
    await openSheet(page)
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press('Tab')
      const inside = await page.evaluate(() => !!document.activeElement?.closest('[data-testid="cleanup-sheet"]'))
      expect(inside).toBe(true)
    }
  })

  test('a click on the dimmed page closes it', async ({ page }) => {
    await openSheet(page)
    await page.mouse.click(40, 450)
    await expect(sheet(page)).toHaveCount(0)
  })

  test('the header states what the disk holds', async ({ page }) => {
    await openSheet(page)
    await expect(sheet(page).locator('.dk-sheet-desc')).toContainText('171 GB free of 931 GB. Models take 108 GB.')
    await expect(sheet(page).locator('.dk-meter')).toHaveAccessibleName(/652 GB other files, 108 GB models, 171 GB free/)
  })
})

test.describe('Cleanup sheet - what it knows and what it does not', () => {
  test('it says plainly that usage history is not recorded, and shows no usage numbers', async ({ page }) => {
    await openSheet(page)
    await expect(page.getByTestId('cleanup-honesty')).toContainText('Usage history is not recorded yet.')
    const text = await sheet(page).innerText()
    // Nothing the API cannot say: no last used, no times used, no days since.
    expect(text).not.toMatch(/(last used|times used)\s*[:\d]|\d+ uses|days? ago|never loaded/i)
  })

  test('models land in Safe, Probably safe and Your call from real facts only', async ({ page }) => {
    await openSheet(page)
    // Safe: another build of the same model is installed, and nothing uses this one.
    await expect(tier(page, 'safe').locator('li')).toHaveCount(1)
    await expect(tier(page, 'safe')).toContainText(SAFE)
    await expect(tier(page, 'safe')).toContainText('Another build is installed: qwen3-14b-instruct')
    // Probably safe: turned off, unused, and the gallery can download it again.
    await expect(tier(page, 'probably').locator('li')).toHaveCount(1)
    await expect(tier(page, 'probably')).toContainText('whisper-medium')
    await expect(tier(page, 'probably')).toContainText('Disabled and unused. Can be downloaded again.')
    // Your call: nothing uses them, and nothing more is known.
    const call = await tier(page, 'call').locator('.ledger-cr__name').evaluateAll((els) => els.map((e) => e.textContent.trim().split('\n')[0]))
    expect(call.length).toBe(7)
    await expect(tier(page, 'call')).toContainText('LocalAI cannot tell when you last used them')
    await expect(tier(page, 'call')).toContainText('Not in the gallery, so it cannot be downloaded again.')
  })

  test('each tier is ranked by size and a model with no known size goes last', async ({ page }) => {
    await openSheet(page)
    // Estimates arrive after the sheet becomes interactive and can reorder it.
    const names = tier(page, 'call').locator('.ledger-cr__name')
    await expect(names.first()).toContainText('llama-3.3-70b-instruct-iq2')
    await expect(names.last()).toContainText('my-finetune-q4')
    await expect(tier(page, 'call').locator('li').last()).toContainText('size unknown')
    await expect(tier(page, 'call').locator('li').first()).toContainText(`${gb1(sizeOf('llama-3.3-70b-instruct-iq2'))} GB`)
  })

  test('protected models are in their own group, with the reason, and cannot be picked', async ({ page }) => {
    await openSheet(page)
    const group = page.getByTestId('cleanup-protected')
    await expect(group).toContainText('Protected')
    await expect(group).toContainText('4 models')
    await expect(group).toContainText('never suggested')
    await group.getByRole('button').click()
    await expect(group).toContainText('Loaded now. Pinned. Used by alias "default-chat".')
    await expect(group).toContainText('whisper-large-v3')
    await expect(group).toContainText('Used by agent "research-helper".')
    await expect(group).toContainText('Used by task "nightly-digest".')
    await expect(group.getByRole('checkbox')).toHaveCount(0)
    // And none of them is a row in a suggestion tier.
    const suggested = []
    for (const name of ['safe', 'probably', 'call']) {
      suggested.push(...(await tier(page, name).locator('.ledger-cr__name').evaluateAll(
        (els) => els.map((e) => e.firstChild.textContent.trim()),
      )))
    }
    expect(suggested).toHaveLength(9)
    for (const name of ['qwen3-8b-instruct', 'whisper-large-v3', 'qwen3-14b-instruct', 'gemma-3-12b-it']) {
      expect(suggested).not.toContain(name)
    }
  })

  test('a failover chain protects its targets', async ({ page }) => {
    await openSheet(page, { chains: [{ name: 'chat-chain', targets: [{ model: 'mistral-small-3.2-24b' }] }] })
    await page.getByTestId('cleanup-protected').getByRole('button').click()
    await expect(page.getByTestId('cleanup-protected')).toContainText('failover chain "chat-chain"')
    await expect(tier(page, 'call')).not.toContainText('mistral-small-3.2-24b')
  })

  test('when agents cannot be read nothing is called safe, and the sheet says why', async ({ page }) => {
    await openSheet(page, { agentsStatus: 500 })
    await expect(page.getByTestId('cleanup-unverified')).toContainText('could not be read')
    await expect(tier(page, 'safe')).toHaveCount(0)
    await expect(tier(page, 'probably')).toHaveCount(0)
    await expect(tier(page, 'call')).toContainText(SAFE)
    await expect(tier(page, 'call')).toContainText('Agents or tasks could not be checked.')
  })

  test('with nothing to suggest the sheet says so', async ({ page }) => {
    await openSheet(page, { installed: [{ id: 'only-model', backend: 'llama-cpp', capabilities: [], pinned: true }] })
    await expect(sheet(page)).toContainText('Nothing to free up')
    await expect(removeButton(page)).toBeDisabled()
  })

  test('the first look shows skeleton rows while the facts are read', async ({ page }) => {
    await mockLedger(page)
    await page.route('**/api/agents*', async (route) => {
      await new Promise((r) => setTimeout(r, 1500))
      return route.fulfill({ json: { agents: [] } })
    })
    await page.goto('/app/models')
    await strip(page).click()
    await expect(page.getByTestId('cleanup-loading')).toBeVisible()
    await expect(sheet(page).locator('.dk-sheet-body')).toHaveAttribute('aria-busy', 'true')
    await expect(tier(page, 'safe')).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('Cleanup sheet - the effect of a choice', () => {
  test('the bar says nothing is selected, and what could be freed', async ({ page }) => {
    await openSheet(page)
    const bar = page.getByTestId('cleanup-effect')
    await expect(bar).toContainText('Nothing selected')
    await expect(bar).toContainText('9 models could free up to')
    await expect(removeButton(page)).toBeDisabled()
  })

  test('selecting rows updates the effect: what is freed and what is free after', async ({ page }) => {
    await openSheet(page)
    await selectSafe(page)
    const bar = page.getByTestId('cleanup-effect')
    const freed = sizeOf(SAFE)
    await expect(bar).toContainText('1 model selected')
    await expect(bar).toContainText(`Frees ${gb1(freed)} GB.`)
    await expect(bar).toContainText(`${Math.round(171 + freed / GB)} GB free after, up from 171 GB`)
    await expect(removeButton(page)).toBeEnabled()
    await expect(removeButton(page)).toContainText('Remove 1 model')

    // A second pick adds its size, and a model of unknown size is said, not guessed.
    await tier(page, 'call').getByRole('checkbox', { name: 'Select my-finetune-q4' }).check()
    await expect(bar).toContainText('2 models selected')
    await expect(bar).toContainText('1 size is unknown and is not counted.')
    await expect(bar).toContainText(`Frees ${gb1(freed)} GB.`)

    await bar.getByRole('button', { name: 'Clear' }).click()
    await expect(bar).toContainText('Nothing selected')
  })

  test('the disk meter shows the selection as its own segment', async ({ page }) => {
    await openSheet(page)
    await expect(sheet(page).locator('.ledger-space__freed')).toHaveCount(0)
    await selectSafe(page)
    await expect(sheet(page).locator('.ledger-space__freed')).toHaveCount(1)
    await expect(sheet(page).locator('.dk-meter-legend')).toContainText(`Frees ${gb1(sizeOf(SAFE))} GB`)
  })

  test('"Select the safe one" picks the safe tier from the bar in one press', async ({ page }) => {
    await openSheet(page)
    await page.getByTestId('cleanup-pick-safe').click()
    await expect(page.getByTestId('cleanup-effect')).toContainText('1 model selected')
    await expect(tier(page, 'safe').getByRole('checkbox', { checked: true })).toHaveCount(1)
    await expect(page.getByTestId('cleanup-pick-safe')).toHaveCount(0)
  })

  test('Select all picks a tier, and a second press clears it', async ({ page }) => {
    await openSheet(page)
    await tier(page, 'call').getByRole('button', { name: 'Select all' }).click()
    await expect(tier(page, 'call').getByRole('checkbox', { checked: true })).toHaveCount(7)
    await tier(page, 'call').getByRole('button', { name: 'Clear' }).click()
    await expect(tier(page, 'call').getByRole('checkbox', { checked: true })).toHaveCount(0)
  })
})

test.describe('Cleanup sheet - confirm and dry run', () => {
  test('a dry run lists what goes, why, and what it frees, and cancelling deletes nothing', async ({ page }) => {
    const state = await openSheet(page)
    await selectSafe(page)
    await removeButton(page).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Remove 1 model?')
    await expect(dialog).toContainText(`This frees ${gb1(sizeOf(SAFE))} GB.`)
    const dry = dialog.getByTestId('cleanup-dry-run')
    await expect(dry).toContainText(SAFE)
    await expect(dry).toContainText('Another build is installed: qwen3-14b-instruct')
    await expect(dry).toContainText(`${gb1(sizeOf(SAFE))} GB`)
    await expect(dialog).toContainText('Checked just now.')
    await expect(dialog).toContainText('Nothing is deleted yet.')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    expect(state.deletes).toHaveLength(0)
    await expect(page.getByTestId('removal-undo-toast')).toHaveCount(0)
    // The sheet is still there, with the selection intact.
    await expect(sheet(page)).toBeVisible()
    await expect(page.getByTestId('cleanup-effect')).toContainText('1 model selected')
  })

  test('the dry run looks again: a model that became used since is left out', async ({ page }) => {
    const state = await openSheet(page)
    await selectSafe(page)
    // Between opening the sheet and confirming, a task starts using the model.
    state.tasks.push({ id: 't2', name: 'new-job', model: SAFE, enabled: true })
    await removeButton(page).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Nothing left to remove')
    await expect(dialog).toContainText('Left out, because they are now in use')
    await expect(dialog).toContainText(SAFE)
    // There is nothing to confirm, so the only button closes the dialog.
    await expect(dialog.getByRole('button', { name: /^Remove/ })).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Close' }).click()
    await expect(page.getByTestId('removal-undo-toast')).toHaveCount(0)
    expect(state.deletes).toHaveLength(0)
  })
})

test.describe('Cleanup sheet - removal with an undo window', () => {
  async function removeSafe(page, options = {}) {
    const state = await openSheet(page, options, { clock: true })
    await selectSafe(page)
    await removeButton(page).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Remove 1 model' }).click()
    await expect(page.getByTestId('removal-undo-toast')).toBeVisible()
    return state
  }

  test('removing starts a window: the toast offers Undo and nothing is sent yet', async ({ page }) => {
    const state = await removeSafe(page)
    const toast = page.getByTestId('removal-undo-toast')
    await expect(toast).toContainText('Removed 1 model.')
    await expect(toast).toContainText(`${gb1(sizeOf(SAFE))} GB is freed when the undo time ends in 30 s.`)
    await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible()
    // No close button: closing must not finish the removal early.
    await expect(toast.getByRole('button', { name: /dismiss/i })).toHaveCount(0)
    await page.clock.fastForward(25_000)
    expect(state.deletes).toHaveLength(0)
    // The model is out of the sheet meanwhile, and a second removal waits.
    await expect(tier(page, 'safe')).toHaveCount(0)
    await expect(page.getByTestId('cleanup-effect')).toContainText('A removal is waiting.')
  })

  test('Undo brings the model back and nothing is ever sent', async ({ page }) => {
    const state = await removeSafe(page)
    await page.clock.fastForward(10_000)
    await page.getByTestId('removal-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('removal-undo-toast')).toHaveCount(0)
    await expect(tier(page, 'safe')).toContainText(SAFE)
    await page.clock.fastForward(120_000)
    expect(state.deletes).toHaveLength(0)
    await expect(removeButton(page)).toBeDisabled()
  })

  test('when the window ends each model goes through the existing delete call', async ({ page }) => {
    const state = await removeSafe(page)
    await page.clock.fastForward(31_000)
    await expect.poll(() => state.deletes).toEqual([SAFE])
    await expect(page.getByTestId('removal-undo-toast')).toHaveCount(0)
    await expect(page.getByText('Deleted 1 model.')).toBeVisible()
    await expect(tier(page, 'safe')).toHaveCount(0)
  })

  test('the hidden model is gone from the Installed table during the window and back after Undo', async ({ page }) => {
    await removeSafe(page)
    await page.keyboard.press('Escape')
    await page.getByRole('link', { name: /^Installed/ }).click()
    await expect(page.locator('[data-testid="installed-models-rail-item"]').first()).toBeVisible()
    await expect(page.locator(`[data-entity="${SAFE}"]`)).toHaveCount(0)
    await page.getByTestId('removal-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.locator(`[data-entity="${SAFE}"]`)).toBeVisible()
  })

  test('leaving the page during the window deletes nothing and says so', async ({ page }) => {
    const state = await removeSafe(page)
    await page.keyboard.press('Escape')
    await page.getByRole('link', { name: 'Chat', exact: true }).first().click()
    await expect(page).toHaveURL(/\/app\/chat/)
    await expect(page.getByText('You left the page, so 1 model was not deleted.')).toBeVisible()
    await page.clock.fastForward(120_000)
    expect(state.deletes).toHaveLength(0)
  })

  test('closing the page during the window deletes nothing', async ({ page }) => {
    const state = await removeSafe(page)
    await page.close()
    await new Promise((r) => setTimeout(r, 400))
    expect(state.deletes).toHaveLength(0)
  })

  test('a delete that fails is reported and the model comes back', async ({ page }) => {
    const state = await removeSafe(page, { deleteFails: [SAFE] })
    await page.clock.fastForward(31_000)
    await expect(page.getByText(`Could not delete ${SAFE}: model is busy`)).toBeVisible()
    expect(state.deletes).toHaveLength(0)
    await expect(tier(page, 'safe')).toContainText(SAFE)
  })

  test('a model that was loaded in the meantime is skipped, not deleted', async ({ page }) => {
    const state = await removeSafe(page)
    state.loaded.push(SAFE)
    await page.clock.fastForward(31_000)
    await expect(page.getByText(`Not deleted, because they were loaded in the meantime: ${SAFE}`)).toBeVisible()
    expect(state.deletes).toHaveLength(0)
  })
})

test.describe('Cleanup sheet - where the disk strip is not offered', () => {
  test('without a disk reading there is no strip, and no Free up space button either', async ({ page }) => {
    const { disk, ...noDisk } = PROFILES.gpu24
    void disk
    await mockLedger(page, { resources: noDisk })
    await page.goto('/app/models?view=installed')
    await expect(page.locator('[data-testid="installed-models-rail-item"]').first()).toBeVisible({ timeout: 15_000 })
    await expect(strip(page)).toHaveCount(0)
    await expect(page.getByTestId('installed-cleanup')).toHaveCount(0)
  })
})

test.describe('Cleanup sheet - low disk', () => {
  test('the strip is amber and the sheet opens on the same figures', async ({ page }) => {
    await openSheet(page, { resources: withDisk(PROFILES.gpu24, { total: 931 * GB, used: 908 * GB, available: 23 * GB }) })
    await expect(sheet(page).locator('.dk-sheet-desc')).toContainText('23.0 GB free of 931 GB')
  })
})

test.describe('Cleanup sheet - phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('it is a bottom sheet with a grip, and the effect bar stays on screen', async ({ page }) => {
    await openSheet(page)
    // The sheet rises into place, so read it once it has settled.
    await expect.poll(async () => {
      const b = await sheet(page).boundingBox()
      return Math.round(b.y + b.height)
    }).toBeLessThanOrEqual(845)
    const box = await sheet(page).boundingBox()
    expect(Math.round(box.width)).toBe(390)
    expect(box.y).toBeGreaterThan(0)
    await expect(sheet(page).locator('.dk-sheet-grip')).toBeVisible()
    const bar = await page.getByTestId('cleanup-effect').boundingBox()
    expect(bar.y + bar.height).toBeLessThanOrEqual(844 + 1)
    await selectSafe(page)
    await expect(removeButton(page)).toBeVisible()
    await expect(removeButton(page)).toBeInViewport()
  })
})

test.describe('Cleanup sheet - reduced motion', () => {
  test('it still opens, and the sheet does not animate in', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await openSheet(page)
    const duration = await sheet(page).evaluate((el) => getComputedStyle(el).animationDuration)
    expect(Math.max(...duration.split(',').map((d) => parseFloat(d)))).toBeLessThan(0.01)
  })
})
