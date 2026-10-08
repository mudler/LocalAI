import { test, expect } from './coverage-fixtures.js'
import { mockLedger, mockPlacement } from './ledger-fixtures.js'
import { mockLibrary } from './library-fixtures.js'

// Selected rows, status strips and empty states keep to the taste guide:
// no coloured left rail on a selectable or card-like element, and an empty
// state is a stacked column, not a row of three things run together.

const leftBorder = (locator) => locator.evaluate(el => parseFloat(getComputedStyle(el).borderLeftWidth))

test.describe('Model editor', () => {
  test('opens on the real config shapes with no error toast, and the current section is not marked by a rail', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page)
    await page.goto('/app/model-editor/qwen3-8b-instruct')
    const current = page.locator('.set-rail__item--on')
    await expect(current).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('editor-placement')).toBeVisible()
    expect(await leftBorder(current)).toBe(0)
    // Every section icon is the same quiet colour, whatever the section.
    const colours = await page.locator('.me-section-icon').evaluateAll(els => els.map(el => getComputedStyle(el).color))
    expect(colours.length).toBeGreaterThan(1)
    expect(new Set(colours).size).toBe(1)
    await expect(page.locator('.toast')).toHaveCount(0)
  })

  test('"No fields configured" stacks the icon, the title and the text', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page, { configs: { bare: '{}\n' } })
    await page.goto('/app/model-editor/bare')
    const empty = page.getByTestId('editor-no-fields')
    await expect(empty).toBeVisible({ timeout: 15_000 })
    await expect(empty.getByRole('heading', { name: 'No fields configured' })).toBeVisible()
    const icon = await empty.locator('.dk-empty-icon').boundingBox()
    const title = await empty.locator('.dk-empty-title').boundingBox()
    const text = await empty.locator('.dk-empty-text').boundingBox()
    expect(icon.y + icon.height).toBeLessThanOrEqual(title.y + 1)
    expect(title.y + title.height).toBeLessThanOrEqual(text.y + 1)
    await expect(page.locator('.toast')).toHaveCount(0)
  })
})

test('an install in progress is a plain strip with no status rail', async ({ page }) => {
  await mockLedger(page, {
    operations: [{
      id: 'model-a', name: 'model-a', fullName: 'model-a', jobID: 'job-a', progress: 40, taskType: 'installation',
      isDeletion: false, isBackend: false, isQueued: false, cancellable: true,
    }],
  })
  await page.goto('/app/models')
  const strip = page.locator('.operations-strip')
  await expect(strip).toBeVisible({ timeout: 15_000 })
  expect(await leftBorder(strip)).toBe(0)
})

test('the skill editor marks its current section without a left rail', async ({ page }) => {
  await mockLibrary(page)
  await page.goto('/app/skills/new')
  const current = page.locator('.skilledit-sidebar-item.active').first()
  await expect(current).toBeVisible({ timeout: 15_000 })
  expect(await leftBorder(current)).toBe(0)
})

test.describe('Add Model', () => {
  test('the templates are a list of rows, and choosing one moves on to its fields', async ({ page }) => {
    await mockLedger(page)
    await mockPlacement(page)
    await page.goto('/app/model-editor')
    const rows = page.locator('.lanes--templates .lane')
    await expect(rows.first()).toBeVisible({ timeout: 15_000 })
    expect(await rows.count()).toBeGreaterThan(5)
    // One column of rows, not a grid of cards: every row has the same left edge.
    const lefts = await rows.evaluateAll(els => els.map(el => Math.round(el.getBoundingClientRect().left)))
    expect(new Set(lefts).size).toBe(1)
    await rows.filter({ hasText: 'LLM' }).first().click()
    await expect(page.getByRole('heading', { name: 'Add Model' })).toBeVisible()
    await expect(page.locator('.lanes--templates')).toHaveCount(0)
  })
})

test.describe('Hub pages', () => {
  for (const [name, path, setup] of [
    ['Build', '/app/build', async page => { const { mockMachine } = await import('./tools-fixtures.js'); await mockMachine(page) }],
    ['Agents', '/app/agents', async page => { const { mockAgents } = await import('./agents-fixtures.js'); await mockAgents(page) }],
  ]) {
    test(`${name}: the page title starts at the same edge as the first tab`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 })
      await setup(page)
      await page.goto(path)
      const tab = page.locator('.dk-hubtab').first()
      await expect(tab).toBeVisible({ timeout: 15_000 })
      const bar = await page.locator('.dk-hubtabs').first().boundingBox()
      const title = await page.locator('.page .page-title, .page h1').first().boundingBox()
      expect(Math.abs(title.x - bar.x)).toBeLessThanOrEqual(2)
    })
  }
})

test('the Studio voice field shows its whole placeholder and stays inside a phone row', async ({ page }) => {
  const { mockStudio, stubMedia } = await import('./studio-fixtures.js')
  await mockStudio(page)
  await stubMedia(page)
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/app/studio/tts')
    const voice = page.getByTestId('ws-voice')
    await expect(voice).toBeVisible({ timeout: 15_000 })
    const box = await voice.boundingBox()
    const chip = await page.locator('.ws-chip--input').filter({ has: voice }).boundingBox()
    const viewport = page.viewportSize().width
    expect(chip.x + chip.width).toBeLessThanOrEqual(viewport)
    if (width === 1440) expect(box.width).toBeGreaterThan(200)
  }
})

test('on a phone the job schedule line wraps instead of ending in an ellipsis', async ({ page }) => {
  const { mockJobs } = await import('./jobs-fixtures.js')
  await page.setViewportSize({ width: 390, height: 844 })
  await mockJobs(page)
  await page.goto('/app/agent-jobs')
  const line = page.locator('.aj-phone-line').first()
  await expect(line).toBeVisible({ timeout: 15_000 })
  const style = await line.evaluate(el => getComputedStyle(el))
  expect(style.textOverflow).not.toBe('ellipsis')
  expect(style.whiteSpace).not.toBe('nowrap')
  expect(await line.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
})
