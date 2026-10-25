import { test, expect } from './coverage-fixtures.js'
import { mockLedger, GALLERY, PROFILES, withDisk, GB } from './ledger-fixtures.js'

// The Explore ledger: one dense table where every row carries its own fit,
// facets with counts, search, selection, density, the disk strip and the
// states around them. The gallery is the shared 41-model fixture, so the
// numbers below are computed from it rather than typed in.

const rows = (page) => page.locator('[data-testid="discover-rail-item"]')
const row = (page, name) => page.locator(`[data-entity="${name}"]`)
const pane = (page) => page.locator('[data-testid="discover-pane"]')
const strip = (page) => page.getByTestId('disk-strip')

async function open(page, options = {}, url = '/app/models') {
  const state = await mockLedger(page, options)
  await page.goto(url)
  await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
  return state
}

const entry = (name) => GALLERY.find((e) => e.name === name)
const collapsed = GALLERY.filter((e) => !e.variantOf)
const need8k = (name) => entry(name).estimate.estimates[8192].vramBytes
const gb1 = (bytes) => (bytes / GB).toFixed(1)

test.describe('Models ledger - fit on this machine', () => {
  test('every row carries its own fit: a bar, the memory needed and the headroom in words', async ({ page }) => {
    await open(page)
    const r = row(page, 'qwen3-32b-instruct')
    const fit = r.locator('[data-fit]')
    await expect(fit).toHaveAttribute('data-fit', 'fits')
    // 24 GB card, 95 percent usable, minus what the model needs at 8K.
    const headroom = gb1(24 * 0.95 * GB - need8k('qwen3-32b-instruct'))
    await expect(fit).toContainText(`${headroom} free`)
    await expect(fit).toContainText(`${gb1(need8k('qwen3-32b-instruct'))} GB`)
    // The bar is drawn, and solid: no gradient on the fill.
    const fill = fit.locator('.ledger-fit__fill')
    await expect(fill).toBeVisible()
    expect(await fill.evaluate((el) => getComputedStyle(el).backgroundImage)).toBe('none')
    expect((await fill.boundingBox()).width).toBeGreaterThan(3)
  })

  test('a model too big for the card spills to the CPU and says how much', async ({ page }) => {
    await open(page)
    const fit = row(page, 'llama-3.3-70b-instruct-iq2').locator('[data-fit]')
    await expect(fit).toHaveAttribute('data-fit', 'spill')
    const spill = gb1(need8k('llama-3.3-70b-instruct-iq2') - 24 * 0.95 * GB)
    await expect(fit).toContainText(`+${spill} on CPU`)
  })

  test('8 GB laptop: small models fit, mid ones spill, big ones are over', async ({ page }) => {
    await open(page, { profile: 'laptop' })
    // 6 GB of GPU memory, 10 GB of RAM to spill into.
    const limit = 6 * 0.95 * GB
    const small = row(page, 'qwen3-4b-instruct').locator('[data-fit]')
    await expect(small).toHaveAttribute('data-fit', 'fits')
    await expect(small).toContainText(`${gb1(limit - need8k('qwen3-4b-instruct'))} free`)

    const mid = row(page, 'qwen3-8b-instruct').locator('[data-fit]')
    await expect(mid).toHaveAttribute('data-fit', 'spill')
    await expect(mid).toContainText(`+${gb1(need8k('qwen3-8b-instruct') - limit)} on CPU`)

    const big = row(page, 'qwen3-14b-instruct').locator('[data-fit]')
    await expect(big).toHaveAttribute('data-fit', 'over')
    // Over GPU and RAM together: what is left after the RAM took its share.
    await expect(big).toContainText(`${gb1(need8k('qwen3-14b-instruct') - limit - 10 * GB)} over`)
  })

  test('no GPU: the budget is memory, nothing spills and the page says so', async ({ page }) => {
    await open(page, { profile: 'nogpu' })
    await expect(page.getByTestId('fit-basis')).toContainText('No GPU found')
    await expect(page.getByText('Fits in memory')).toBeVisible()
    await expect(page.getByText('Fits in GPU')).toHaveCount(0)
    await expect(row(page, 'qwen3-32b-instruct').locator('[data-fit]')).toHaveAttribute('data-fit', 'fits')
    const big = row(page, 'gpt-oss-120b').locator('[data-fit]')
    await expect(big).toHaveAttribute('data-fit', 'over')
    await expect(big).toContainText(`${gb1(need8k('gpt-oss-120b') - 32 * 0.95 * GB)} over`)
    await expect(page.locator('[data-fit="spill"]')).toHaveCount(0)
  })

  test('the context slider moves every fit and the column header with it', async ({ page }) => {
    await open(page)
    await expect(page.getByRole('columnheader', { name: /Fit at 8K/ })).toBeVisible()
    await expect(row(page, 'qwen3-32b-instruct').locator('[data-fit]')).toHaveAttribute('data-fit', 'fits')
    await page.locator('#models-context-size').fill('2')
    await expect(page.getByRole('columnheader', { name: /Fit at 32K/ })).toBeVisible()
    // At a 32K context the same model no longer fits in the card.
    await expect(row(page, 'qwen3-32b-instruct').locator('[data-fit]')).toHaveAttribute('data-fit', 'spill')
  })

  test('Fits in GPU hides the rows that do not fit and keeps the ones with no estimate', async ({ page }) => {
    await open(page)
    await expect(row(page, 'llama-3.3-70b-instruct-iq2')).toBeVisible()
    await page.locator('label.filter-bar-group__toggle', { hasText: 'Fits in GPU' }).locator('.toggle__track').click()
    await expect(row(page, 'llama-3.3-70b-instruct-iq2')).toHaveCount(0)
    await expect(row(page, 'qwen3-4b-instruct')).toBeVisible()
  })

  test('the inspector explains the fit and shows what an install leaves on disk', async ({ page }) => {
    await open(page)
    await row(page, 'qwen3-32b-instruct').click()
    const summary = pane(page).getByTestId('fit-summary')
    await expect(summary).toContainText('It fits')
    await expect(summary).toContainText('GPU')
    // 171 GB free, minus the 19.2 GB download.
    await expect(pane(page).getByTestId('leaves-free')).toContainText('leaves 152 GB free')
    await expect(pane(page).locator('.discover__chart')).toBeVisible()
  })

  test('a spilling model shows the GPU and the RAM bar in the inspector', async ({ page }) => {
    await open(page, {}, '/app/models?model=llama-3.3-70b-instruct-iq2')
    const summary = pane(page).getByTestId('fit-summary')
    await expect(summary).toContainText('on the CPU')
    await expect(summary.locator('.ledger-fitblock__row')).toHaveCount(2)
  })
})

test.describe('Models ledger - facets and search', () => {
  test('capability chips carry the server counts', async ({ page }) => {
    await open(page)
    const count = (key) => collapsed.filter((e) => e.facets.includes(key)).length
    await expect(page.getByTestId('facet-all')).toContainText(String(collapsed.length))
    await expect(page.getByTestId('facet-chat')).toContainText(String(count('chat')))
    await expect(page.getByTestId('facet-tts')).toContainText(String(count('tts')))
    // The count is read from the listing's own total, not tallied in the page.
    await expect(page.getByTestId('facet-tts')).toHaveAccessibleName(new RegExp(`TTS ${count('tts')}`))
  })

  test('a facet filters on the server and the chip says it is pressed', async ({ page }) => {
    const state = await open(page)
    await page.getByTestId('facet-tts').click()
    await expect(page.getByTestId('facet-tts')).toHaveAttribute('aria-pressed', 'true')
    const tts = collapsed.filter((e) => e.facets.includes('tts'))
    await expect(rows(page)).toHaveCount(tts.length)
    await expect(page.getByTestId('facet-all')).toHaveAttribute('aria-pressed', 'false')
    const listing = state.listingRequests.filter((q) => q.includes('items=30') && q.includes('tag=tts'))
    expect(listing.length).toBeGreaterThan(0)
  })

  test('a search narrows the counts and drops the facets nothing matches', async ({ page }) => {
    await open(page)
    await expect(page.getByTestId('facet-chat')).toBeVisible()
    await page.getByTestId('models-search').fill('whisper')
    await expect(page.getByTestId('facet-transcript')).toContainText('2', { timeout: 10_000 })
    await expect(page.getByTestId('facet-chat')).toHaveCount(0)
    await expect(page.getByTestId('facet-all')).toContainText('2')
  })

  test('"/" jumps to the search field and Escape leaves it', async ({ page }) => {
    await open(page)
    const search = page.getByTestId('models-search')
    await expect(search).not.toBeFocused()
    await page.keyboard.press('/')
    await expect(search).toBeFocused()
    // A slash typed into the field is text, not a second jump.
    await page.keyboard.type('a/b')
    await expect(search).toHaveValue('a/b')
    await page.keyboard.press('Escape')
    await expect(search).not.toBeFocused()
  })

  test('search keeps its meaning: the term goes to the server as typed', async ({ page }) => {
    const state = await open(page)
    await page.getByTestId('models-search').fill('kokoro')
    await expect(rows(page)).toHaveCount(1)
    await expect(row(page, 'kokoro-82m')).toBeVisible()
    await expect(page).toHaveURL(/[?&]q=kokoro/)
    // A sentence is not parsed into filters. It is a term like any other, and
    // the listing answers it (here with nothing).
    await page.getByTestId('models-search').fill('models that fit my gpu')
    await expect(page.getByTestId('gallery-empty')).toBeVisible()
    await expect.poll(() => state.listingRequests.some((q) => q.includes('term=models+that+fit+my+gpu') || q.includes('term=models%20that%20fit%20my%20gpu'))).toBe(true)
    // No tag was invented from the words.
    expect(state.listingRequests.filter((q) => q.includes('items=30') && q.includes('models+that')).every((q) => !q.includes('tag='))).toBe(true)
  })
})

test.describe('Models ledger - selection and keys', () => {
  test('selection is a surface step and a check, with no rail on the edge', async ({ page }) => {
    await open(page)
    const r = row(page, 'qwen3-4b-instruct')
    const cell = r.locator('td').nth(1)
    const before = await cell.evaluate((el) => getComputedStyle(el).backgroundColor)
    await r.click()
    await expect(r).toHaveAttribute('data-selected', 'true')
    await expect(r).toHaveAttribute('aria-current', 'true')
    const after = await cell.evaluate((el) => {
      const cs = getComputedStyle(el)
      return { bg: cs.backgroundColor, shadow: cs.boxShadow, left: cs.borderLeftWidth }
    })
    expect(after.bg).not.toBe(before)
    expect(after.shadow).not.toContain('inset')
    expect(after.left).toBe('0px')
    // The check mark fills in.
    const mark = r.locator('.ledger-mark')
    expect(await mark.evaluate((el) => getComputedStyle(el).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
    // And there is no rail beside the table at all.
    await expect(page.locator('.entity-rail, .split-view__rail-col')).toHaveCount(0)
    await expect(pane(page).getByRole('heading', { name: 'qwen3-4b-instruct' })).toBeVisible()
  })

  test('arrow keys move the selection and the focus together', async ({ page }) => {
    await open(page)
    const names = await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-entity')))
    await row(page, names[0]).click()
    await page.keyboard.press('ArrowDown')
    await expect(row(page, names[1])).toBeFocused()
    await expect(row(page, names[1])).toHaveAttribute('data-selected', 'true')
    await expect(page).toHaveURL(new RegExp(`model=${names[1]}`))
    await page.keyboard.press('ArrowUp')
    await expect(row(page, names[0])).toBeFocused()
    // Moving by key replaces the history entry: Back leaves the page, it does
    // not walk back through every row that was passed: one step out of the
    // inspector, to the table as it was before the first click.
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('ArrowDown')
    await page.goBack()
    await expect(page).not.toHaveURL(/model=/)
  })

  test('Enter installs the focused row, and does nothing on one that is installed', async ({ page }) => {
    const state = await open(page)
    await row(page, 'qwen3-4b-instruct').click()
    await page.keyboard.press('Enter')
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0].name).toBe('qwen3-4b-instruct')
    // Plain Install asks for no variant: the server picks the build.
    expect(state.installs[0].search).toBe('')

    await row(page, 'qwen3-8b-instruct').click()
    await expect(row(page, 'qwen3-8b-instruct')).toContainText('Installed')
    await page.keyboard.press('Enter')
    await page.waitForTimeout(300)
    expect(state.installs).toHaveLength(1)
  })

  test('the Install button in a row installs without moving the selection', async ({ page }) => {
    const state = await open(page)
    await row(page, 'qwen3-4b-instruct').click()
    await row(page, 'qwen3-30b-a3b').getByTestId('discover-row-install').click()
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0].name).toBe('qwen3-30b-a3b')
    await expect(row(page, 'qwen3-4b-instruct')).toHaveAttribute('data-selected', 'true')
  })

  test('Escape closes the inspector and puts focus back on the row', async ({ page }) => {
    await open(page)
    await row(page, 'qwen3-4b-instruct').click()
    await expect(pane(page).getByTestId('discover-back')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page).not.toHaveURL(/model=/)
    await expect(pane(page).getByTestId('discover-back')).toHaveCount(0)
    await expect(row(page, 'qwen3-4b-instruct')).toBeFocused()
  })

  test('there is no compare key: "c" does nothing', async ({ page }) => {
    await open(page)
    await row(page, 'qwen3-4b-instruct').click()
    await page.keyboard.press('c')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page).toHaveURL(/model=qwen3-4b-instruct/)
  })
})

test.describe('Models ledger - density', () => {
  test('"d" flips between comfortable and compact rows, and the choice is kept', async ({ page }) => {
    await open(page)
    const table = page.locator('table.ledger-table')
    await expect(table).not.toHaveClass(/dk-table--compact/)
    const tall = (await row(page, 'qwen3-4b-instruct').boundingBox()).height
    await page.keyboard.press('d')
    await expect(table).toHaveClass(/dk-table--compact/)
    await expect(page.getByTestId('density-compact')).toHaveAttribute('aria-pressed', 'true')
    const short = (await row(page, 'qwen3-4b-instruct').boundingBox()).height
    expect(short).toBeLessThan(tall)

    await page.reload()
    await expect(rows(page).first()).toBeVisible()
    await expect(page.locator('table.ledger-table')).toHaveClass(/dk-table--compact/)
    await page.getByTestId('density-comfortable').click()
    await expect(page.locator('table.ledger-table')).not.toHaveClass(/dk-table--compact/)
  })

  test('"d" is text, not a switch, while typing in the search field', async ({ page }) => {
    await open(page)
    await page.getByTestId('models-search').click()
    await page.keyboard.type('mod')
    await expect(page.getByTestId('models-search')).toHaveValue('mod')
    await expect(page.locator('table.ledger-table')).not.toHaveClass(/dk-table--compact/)
  })
})

test.describe('Models ledger - disk strip', () => {
  test('it shows what is free on the models disk and stays neutral', async ({ page }) => {
    await open(page)
    await expect(strip(page)).toContainText('171 GB free')
    await expect(strip(page)).toHaveAttribute('data-low', 'false')
    await expect(strip(page)).not.toContainText('Low disk')
    await expect(strip(page)).toHaveAccessibleName(/171 GB free of 931 GB/)
  })

  test('it turns amber and says so when the disk is low', async ({ page }) => {
    await open(page, { resources: withDisk(PROFILES.gpu24, { total: 931 * GB, used: 908 * GB, available: 23 * GB }) })
    await expect(strip(page)).toHaveAttribute('data-low', 'true')
    await expect(strip(page)).toContainText('Low disk')
    const border = await strip(page).evaluate((el) => getComputedStyle(el).borderTopColor)
    const warn = await page.evaluate(() => {
      const probe = document.createElement('span')
      probe.style.color = 'var(--dk-warn)'
      document.body.appendChild(probe)
      const color = getComputedStyle(probe).color
      probe.remove()
      return color
    })
    expect(border).toBe(warn)
    await expect(strip(page)).toHaveAccessibleName(/Low disk/)
  })

  test('low means under 10 percent free, or under 20 GB free', async ({ page }) => {
    const at = (total, free) => withDisk(PROFILES.gpu24, { total: total * GB, used: (total - free) * GB, available: free * GB })
    await open(page, { resources: at(4000, 450) })
    await expect(strip(page)).toHaveAttribute('data-low', 'false')
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await open(page, { resources: at(4000, 390) })
    await expect(strip(page)).toHaveAttribute('data-low', 'true')
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await open(page, { resources: at(100, 19) })
    await expect(strip(page)).toHaveAttribute('data-low', 'true')
  })

  test('it is hidden when the server reports no disk', async ({ page }) => {
    const { disk, ...noDisk } = PROFILES.gpu24
    void disk
    await open(page, { resources: noDisk })
    await expect(strip(page)).toHaveCount(0)
    // The rest of the page does not care.
    await expect(row(page, 'qwen3-4b-instruct')).toBeVisible()
  })

  test('it is hidden on a cluster controller, whose own disk says nothing about the workers', async ({ page }) => {
    await open(page, {
      resources: { ...PROFILES.gpu24, cluster: { enabled: true, node_name: 'w1', node_count: 2, total_memory: 80 * GB, is_gpu: true } },
    })
    await expect(strip(page)).toHaveCount(0)
  })

  test('an install that does not fit on the disk says so in words', async ({ page }) => {
    await open(page, {
      resources: withDisk(PROFILES.gpu24, { total: 931 * GB, used: 926 * GB, available: 5 * GB }),
    }, '/app/models?model=qwen3-32b-instruct')
    await expect(pane(page).getByTestId('leaves-free')).toContainText('less than that free')
  })

  test('it opens the cleanup review', async ({ page }) => {
    await open(page)
    await strip(page).click()
    await expect(page.getByRole('dialog', { name: 'Disk and cleanup' })).toBeVisible()
  })
})

test.describe('Models ledger - states', () => {
  test('the first load draws skeleton rows in the table', async ({ page }) => {
    await mockLedger(page, { listingDelay: 1500 })
    await page.goto('/app/models')
    await expect(page.getByTestId('gallery-loader')).toBeVisible({ timeout: 5_000 })
    await expect(page.locator('table.ledger-table')).toHaveAttribute('aria-busy', 'true')
    await expect(rows(page).first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('gallery-loader')).toHaveCount(0)
  })

  test('an empty gallery says so and offers no filter to clear', async ({ page }) => {
    await mockLedger(page, { gallery: [] })
    await page.goto('/app/models')
    const empty = page.getByTestId('gallery-empty')
    await expect(empty).toBeVisible({ timeout: 10_000 })
    await expect(empty).toContainText('The model gallery is empty.')
    await expect(empty.getByRole('button', { name: 'Clear filters' })).toHaveCount(0)
  })

  test('offline: a banner with Retry, and Retry brings the list back', async ({ page }) => {
    const state = await mockLedger(page, { listingStatus: 500 })
    await page.goto('/app/models')
    const banner = page.getByTestId('gallery-error')
    await expect(banner).toBeVisible({ timeout: 10_000 })
    await expect(banner).toContainText('Cannot reach the gallery')
    await expect(rows(page)).toHaveCount(0)
    state.listingStatus = 200
    await banner.getByRole('button', { name: 'Retry' }).click()
    await expect(rows(page).first()).toBeVisible()
    await expect(page.getByTestId('gallery-error')).toHaveCount(0)
  })

  test('install failed: the row names the error and offers Retry, which reinstalls', async ({ page }) => {
    const state = await open(page, {
      operations: [{ name: 'qwen3-32b-instruct', jobID: 'job-9', error: 'Connection reset at 62%', progress: 62 }],
    })
    const r = row(page, 'qwen3-32b-instruct')
    await expect(r).toContainText('Connection reset at 62%')
    const retry = r.getByTestId('discover-row-retry')
    await expect(retry).toBeVisible()
    await expect(r.getByTestId('discover-row-install')).toHaveCount(0)
    await retry.click()
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0].name).toBe('qwen3-32b-instruct')
    // The old failure is dismissed so it does not sit beside the new attempt.
    expect(state.dismissed).toContain('job-9')
  })

  test('install failed: the inspector says what happened and the next step', async ({ page }) => {
    await open(page, {
      operations: [{ name: 'qwen3-32b-instruct', jobID: 'job-9', error: 'Connection reset at 62%' }],
    }, '/app/models?model=qwen3-32b-instruct')
    await expect(pane(page).getByRole('alert')).toContainText('Install failed: Connection reset at 62%')
    await expect(pane(page).getByTestId('discover-install')).toContainText('Retry')
  })

  test('an install in flight shows its progress in the row', async ({ page }) => {
    await open(page, { operations: [{ name: 'qwen3-32b-instruct', jobID: 'job-2', progress: 38 }] })
    const r = row(page, 'qwen3-32b-instruct')
    await expect(r).toContainText('Installing')
    await expect(r).toContainText('38%')
    await expect(r.getByTestId('discover-row-install')).toHaveCount(0)
  })

  test('the Installed tab carries the count of installed models', async ({ page }) => {
    await open(page)
    await expect(page.getByRole('link', { name: /^Installed/ })).toContainText('13')
  })

  test('reduced motion removes the transitions', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await open(page)
    const duration = await row(page, 'qwen3-4b-instruct').locator('td').first().evaluate((el) => getComputedStyle(el).transitionDuration)
    expect(Math.max(...duration.split(',').map((d) => parseFloat(d)))).toBeLessThan(0.01)
  })
})

test.describe('Models ledger - phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('rows collapse to name, fit and action, and the page does not scroll sideways', async ({ page }) => {
    await open(page)
    const r = row(page, 'qwen3-4b-instruct')
    expect(await r.evaluate((el) => getComputedStyle(el).display)).toBe('grid')
    await expect(page.getByRole('columnheader', { name: 'Size' })).toBeHidden()
    const name = await r.locator('.ledger-name').boundingBox()
    const fit = await r.locator('[data-fit]').boundingBox()
    expect(fit.y).toBeGreaterThan(name.y)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    // The action stays reachable and large enough to tap.
    const button = await r.getByTestId('discover-row-install').boundingBox()
    expect(button.height).toBeGreaterThanOrEqual(30)
  })

  // On a phone there is no inspector beside the table, so a tap on a row opens
  // the model's own page. Back gives the table back, with focus on the arrow of
  // the row that was opened.
  test('a tapped row is the page, and Back gives the table back', async ({ page }) => {
    await open(page)
    const r = row(page, 'qwen3-4b-instruct')
    await r.locator('.ledger-name').click()
    await expect(page.getByTestId('model-page')).toBeVisible()
    await expect(page.getByTestId('model-page-name')).toHaveText('qwen3-4b-instruct')
    await expect(r).toBeHidden()
    await page.getByTestId('model-page-back').click()
    await expect(r).toBeVisible()
    await expect(r.locator('[data-row-open]')).toBeFocused()
  })

  // Between a phone and a desk the inspector still takes the page when a row is
  // selected, and its close button gives the table back.
  test('on a narrow window a selected row is the inspector page, and the close button gives the table back', async ({ page }) => {
    await page.setViewportSize({ width: 800, height: 900 })
    await open(page)
    const r = row(page, 'qwen3-4b-instruct')
    await r.click()
    await expect(pane(page)).toBeVisible()
    await expect(r).toBeHidden()
    await pane(page).getByTestId('discover-back').click()
    await expect(r).toBeVisible()
    await expect(r).toBeFocused()
  })

  test('the disk strip spans the width and is a tap target', async ({ page }) => {
    await open(page)
    const box = await strip(page).boundingBox()
    expect(box.width).toBeGreaterThan(300)
    expect(box.height).toBeGreaterThanOrEqual(40)
  })
})
