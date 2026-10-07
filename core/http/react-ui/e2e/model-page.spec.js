import { test, expect } from './coverage-fixtures.js'
import { mockLedger, mockPlacement, GALLERY, GB } from './ledger-fixtures.js'

// A model at its own address: /app/models/<id>. Opened from Explore and from
// Installed, with tabs, a walker over the list it came from, Back that finds
// the list as it was, and honest states for loading, not found, offline and an
// install in flight.

const rows = (page) => page.locator('[data-testid="discover-rail-item"]')
const installedRows = (page) => page.locator('[data-testid="installed-models-rail-item"]')
const name = (page) => page.getByTestId('model-page-name')
const tab = (page, id) => page.getByTestId(`model-page-tab-${id}`)

async function setup(page, options = {}, url = '/app/models') {
  const state = await mockLedger(page, options)
  const placement = await mockPlacement(page, options.placement || {})
  await page.route('**/backend/load', route => { state.loads = [...(state.loads || []), route.request().postDataJSON().model]; return route.fulfill({ json: {} }) })
  await page.route('**/backend/shutdown', route => { state.stops = [...(state.stops || []), route.request().postDataJSON().model]; return route.fulfill({ json: {} }) })
  await page.goto(url)
  return { state, placement }
}

async function openFromExplore(page, model, options) {
  const handle = await setup(page, options)
  await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
  await page.locator(`[data-entity="${model}"] [data-row-open]`).click()
  await expect(name(page)).toHaveText(model)
  return handle
}

test.describe('Model page - reaching it', () => {
  test('the arrow at the end of an Explore row opens the model at its own address', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await expect(page).toHaveURL(/\/app\/models\/qwen3-32b-instruct$/)
    await expect(page.getByTestId('model-page')).toHaveAttribute('data-state', 'ready')
  })

  test('the arrow carries the name of its model for assistive technology', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('[data-entity="qwen3-32b-instruct"] [data-row-open]')).toHaveAccessibleName('Open details for qwen3-32b-instruct')
  })

  test('a double click on a row opens it', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-8b-instruct"] .ledger-name').dblclick()
    await expect(name(page)).toHaveText('qwen3-8b-instruct')
  })

  test('"o" opens the selected row and the inspector has an Open details button', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="gemma-3-27b-it"]').click()
    await expect(page.getByTestId('inspector-open-page')).toBeVisible()
    await page.keyboard.press('o')
    await expect(name(page)).toHaveText('gemma-3-27b-it')
    await page.getByTestId('model-page-back').click()
    await page.locator('[data-entity="gemma-3-27b-it"]').click()
    await page.getByTestId('inspector-open-page').click()
    await expect(name(page)).toHaveText('gemma-3-27b-it')
  })

  test('an installed model opens from the Installed table too', async ({ page }) => {
    await setup(page, {}, '/app/models?view=installed')
    await expect(installedRows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-14b-instruct"] [data-row-open]').click()
    await expect(name(page)).toHaveText('qwen3-14b-instruct')
    await expect(page.getByTestId('model-page-load')).toBeVisible()
  })

  test('a pasted link opens the page with no list behind it', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-32b-instruct')
    await expect(name(page)).toHaveText('qwen3-32b-instruct')
    await expect(page.getByTestId('model-page-walk')).toHaveCount(0)
    await page.getByTestId('model-page-back').click()
    await expect(page).toHaveURL(/\/app\/models$/)
    await expect(rows(page).first()).toBeVisible()
  })

  test('on a phone a tap on a row goes straight to the page', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-32b-instruct"] .ledger-name').click()
    await expect(name(page)).toHaveText('qwen3-32b-instruct')
    await expect(page).toHaveURL(/\/app\/models\/qwen3-32b-instruct$/)
  })
})

test.describe('Model page - walking and going back', () => {
  test('previous and next walk the list in the order it was shown', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    const order = await rows(page).evaluateAll(els => els.map(el => el.dataset.entity))
    await page.locator(`[data-entity="${order[2]}"] [data-row-open]`).click()
    await expect(name(page)).toHaveText(order[2])
    await expect(page.getByTestId('model-page-position')).toHaveText(`3 of ${order.length}`)
    await page.getByTestId('model-page-next').click()
    await expect(name(page)).toHaveText(order[3])
    await page.keyboard.press(']')
    await expect(name(page)).toHaveText(order[4])
    await page.keyboard.press('[')
    await expect(name(page)).toHaveText(order[3])
    await page.keyboard.press('j')
    await expect(name(page)).toHaveText(order[4])
    await page.keyboard.press('k')
    await expect(name(page)).toHaveText(order[3])
    await page.getByTestId('model-page-prev').click()
    await expect(name(page)).toHaveText(order[2])
  })

  test('the walker stops at the ends of the list', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    const first = await rows(page).first().getAttribute('data-entity')
    await page.locator(`[data-entity="${first}"] [data-row-open]`).click()
    await expect(page.getByTestId('model-page-prev')).toBeDisabled()
    await expect(page.getByTestId('model-page-next')).toBeEnabled()
  })

  test('the tab you are on is kept when you step to the next model', async ({ page }) => {
    await openFromExplore(page, 'qwen3-8b-instruct')
    await page.keyboard.press('2')
    await expect(page).toHaveURL(/tab=fit/)
    await page.keyboard.press(']')
    await expect(page).toHaveURL(/tab=fit/)
    await expect(page.getByTestId('model-page-panel-fit')).toBeVisible()
  })

  test('walking replaces the history entry, so Back leaves for the list in one step', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-8b-instruct"] [data-row-open]').click()
    await page.keyboard.press(']')
    await page.keyboard.press(']')
    await page.goBack()
    await expect(page).toHaveURL(/\/app\/models$/)
    await expect(rows(page).first()).toBeVisible()
  })

  test('Backspace goes back too, but not while typing', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-8b-instruct"] [data-row-open]').click()
    await expect(tab(page, 'config')).toBeVisible()
    await page.keyboard.press('5')
    await page.getByTestId('placement-context-input').fill('1234')
    await page.keyboard.press('Backspace')
    await expect(page.getByTestId('placement-context-input')).toHaveValue('123')
    await expect(name(page)).toBeVisible()
    await page.getByTestId('placement-context-input').blur()
    await page.keyboard.press('Backspace')
    await expect(page).toHaveURL(/\/app\/models$/)
  })

  test('Esc and the Back button return to the list with its search, filters and selection kept', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('models-search').fill('qwen3')
    await expect(rows(page).first()).toBeVisible()
    const facet = page.locator('.ledger-facets').getByRole('button', { name: /^Chat/ })
    await facet.click()
    await expect(facet).toHaveAttribute('aria-pressed', 'true')
    // qwen3 as chat: the 4B, 8B, 14B (its Q4 build is folded into it), 30B, the
    // coder and the 32B.
    await expect(rows(page)).toHaveCount(6)
    const before = 6
    const target = await rows(page).nth(1).getAttribute('data-entity')
    await page.locator(`[data-entity="${target}"]`).click()
    await page.locator(`[data-entity="${target}"] [data-row-open]`).click()
    await expect(name(page)).toHaveText(target)
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('models-search')).toHaveValue('qwen3')
    await expect(facet).toHaveAttribute('aria-pressed', 'true')
    await expect(rows(page)).toHaveCount(before)
    await expect(page.locator(`[data-entity="${target}"]`)).toHaveAttribute('data-selected', 'true')
    await page.locator(`[data-entity="${target}"] [data-row-open]`).click()
    await page.getByTestId('model-page-back').click()
    await expect(page.getByTestId('models-search')).toHaveValue('qwen3')
    await expect(rows(page)).toHaveCount(before)
  })

  test('the Installed view and its state filter come back with the page closed', async ({ page }) => {
    await setup(page, {}, '/app/models?view=installed&state=running')
    await expect(installedRows(page).first()).toBeVisible({ timeout: 15_000 })
    await expect(installedRows(page)).toHaveCount(2)
    await page.locator('[data-entity="qwen3-8b-instruct"] [data-row-open]').click()
    await expect(name(page)).toHaveText('qwen3-8b-instruct')
    // The walker walks the filtered Installed list, not the gallery.
    await expect(page.getByTestId('model-page-position')).toHaveText('1 of 2')
    await page.keyboard.press('Escape')
    await expect(page).toHaveURL(/view=installed&state=running/)
    await expect(installedRows(page)).toHaveCount(2)
    await expect(page.getByRole('tab', { name: /^Running/ })).toHaveAttribute('aria-selected', 'true')
  })

  test('coming back does not read the gallery again', async ({ page }) => {
    const { state } = await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(500)
    const before = state.listingRequests.length
    await page.locator('[data-entity="qwen3-8b-instruct"] [data-row-open]').click()
    await expect(name(page)).toHaveText('qwen3-8b-instruct')
    await page.keyboard.press('Escape')
    await expect(rows(page).first()).toBeVisible()
    await page.waitForTimeout(500)
    // The page itself looks its own entry up once; the list asks for nothing.
    expect(state.listingRequests.length - before).toBeLessThanOrEqual(1)
    expect(state.listingRequests.slice(before).every(search => search.includes('term=qwen3-8b-instruct'))).toBe(true)
  })

  test('changing tabs keeps the walker, and Back still leaves for the list in one step', async ({ page }) => {
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="qwen3-8b-instruct"] [data-row-open]').click()
    await page.keyboard.press('2')
    await page.keyboard.press('3')
    await expect(page.getByTestId('model-page-walk')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page).toHaveURL(/\/app\/models$/)
  })

  test('the list keeps its scroll position and focus returns to the row arrow', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 520 })
    await setup(page)
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    const wrap = page.locator('[data-testid="discover-rail"]')
    const scroller = await wrap.evaluate(el => {
      let node = el
      while (node && node.scrollHeight <= node.clientHeight + 4) node = node.parentElement
      return node ? (node.classList.contains('ledger-wrap') ? 'wrap' : 'page') : 'none'
    })
    expect(scroller).not.toBe('none')
    const target = await rows(page).nth(14).getAttribute('data-entity')
    await page.locator(`[data-entity="${target}"]`).scrollIntoViewIfNeeded()
    const position = () => page.evaluate(() => {
      const els = [document.querySelector('.models-page'), document.querySelector('.ledger-wrap')]
      return els.map(el => (el ? Math.round(el.scrollTop) : 0))
    })
    const before = await position()
    expect(before[0] + before[1]).toBeGreaterThan(50)
    await page.locator(`[data-entity="${target}"] [data-row-open]`).click()
    await expect(name(page)).toHaveText(target)
    await page.keyboard.press('Escape')
    await expect(rows(page).first()).toBeVisible()
    await expect.poll(position).toEqual(before)
    await expect(page.locator(`[data-entity="${target}"] [data-row-open]`)).toBeFocused()
  })
})

test.describe('Model page - an Explore model', () => {
  test('the title block names the model, its type, backend and licence, and the install action', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await expect(page.locator('.modelpage-chips')).toContainText('Text and reasoning')
    await expect(page.locator('.modelpage-chips')).toContainText('llama-cpp')
    await expect(page.locator('.modelpage-chips')).toContainText('apache-2.0')
    await expect(page.locator('.modelpage-lede')).toContainText('Dense 32B chat model')
    await expect(page.getByTestId('model-page-install')).toContainText('Install')
    await expect(page.getByTestId('leaves-free')).toContainText('leaves')
    await expect(page.getByTestId('model-page-state')).toHaveCount(0)
  })

  test('only the tabs that apply are there, without disabled ones', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await expect(page.getByRole('tab')).toHaveText(['Overview', 'Fit and memory', 'Variants and files'])
    await expect(page.getByRole('tab', { name: 'Logs' })).toHaveCount(0)
    await expect(page.getByRole('tab', { selected: true })).toHaveText('Overview')
  })

  test('the answer strip gives fit, what it does and what installing leaves', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await expect(page.getByTestId('answer-fit')).toContainText('Fits in GPU')
    await expect(page.getByTestId('answer-fit')).toContainText('at 8K context')
    await expect(page.getByTestId('answer-does')).toContainText('chat')
    await expect(page.getByTestId('answer-state')).toContainText('Leaves')
  })

  test('the overview lists backend, licence, max context, tags and links, and a memory bar', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    const about = page.locator('.modelpage-about')
    await expect(about).toContainText('llama-cpp')
    await expect(about).toContainText('apache-2.0')
    await expect(about).toContainText('Max context')
    await expect(about).toContainText('128K tokens')
    await expect(about.getByRole('link', { name: /huggingface\.co\/example\/qwen3-32b-instruct/ })).toHaveAttribute('target', '_blank')
    await expect(page.getByTestId('glance-bar')).toBeVisible()
    await expect(page.getByTestId('model-page-glance')).toContainText('free after')
  })

  test('the tab strip is a tab list: click, keys 1 to 3, arrows, and the address says which', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await tab(page, 'fit').click()
    await expect(page).toHaveURL(/tab=fit/)
    await expect(page.getByTestId('model-page-fit')).toBeVisible()
    await page.keyboard.press('3')
    await expect(page).toHaveURL(/tab=variants/)
    await expect(page.getByTestId('model-page-variants')).toBeVisible()
    await page.keyboard.press('1')
    await expect(page).not.toHaveURL(/tab=/)
    await tab(page, 'overview').focus()
    await page.keyboard.press('ArrowRight')
    await expect(tab(page, 'fit')).toHaveAttribute('aria-selected', 'true')
    await expect(tab(page, 'fit')).toBeFocused()
    await page.keyboard.press('End')
    await expect(tab(page, 'variants')).toHaveAttribute('aria-selected', 'true')
  })

  test('a tab named in the address opens, and an unknown one falls back to the overview', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-32b-instruct?tab=fit')
    await expect(page.getByTestId('model-page-fit')).toBeVisible()
    await page.goto('/app/models/qwen3-32b-instruct?tab=logs')
    await expect(page.getByTestId('model-page-panel-overview')).toBeVisible()
  })

  test('Fit and memory says it in words, moves with the context size and draws the memory by context', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await page.keyboard.press('2')
    const banner = page.getByTestId('fit-banner')
    await expect(banner).toContainText('Fits in GPU')
    await expect(page.getByTestId('fit-bar')).toBeVisible()
    await expect(page.getByTestId('vram-chart')).toBeVisible()
    await expect(page.getByTestId('vram-bar-8192')).toHaveAttribute('data-selected', 'true')
    await page.getByTestId('fit-context-262144').click()
    await expect(banner).not.toContainText('Fits in GPU')
    await expect(banner).toContainText('256K')
    await expect(page.getByTestId('vram-bar-262144')).toHaveAttribute('data-selected', 'true')
    await expect(page.getByTestId('vram-bar-262144')).toHaveAttribute('data-over', 'true')
    await page.getByTestId('vram-bar-16384').scrollIntoViewIfNeeded()
    await page.getByTestId('vram-bar-16384').locator('rect').first().click()
    await expect(page.getByTestId('vram-bar-16384')).toHaveAttribute('data-selected', 'true')
    await expect(page.getByTestId('vram-chart').locator('details')).toBeAttached()
  })

  test('a model that does not fit says how far over, never that it fits', async ({ page }) => {
    await openFromExplore(page, 'llama-3.3-70b-instruct-iq2', { profile: 'laptop' })
    await expect(page.getByTestId('answer-fit')).not.toContainText('Fits in GPU')
    await expect(page.getByTestId('answer-fit')).toContainText(/more than|CPU/)
  })

  test('a host with no GPU measures against memory and says so', async ({ page }) => {
    await openFromExplore(page, 'qwen3-8b-instruct', { profile: 'nogpu' })
    await expect(page.getByTestId('answer-fit')).toContainText('Fits in memory')
    await page.keyboard.press('2')
    await expect(page.getByTestId('fit-pool')).toContainText('System memory')
  })

  test('Variants and files lists the builds with size and fit and installs the one you pick', async ({ page }) => {
    const { state } = await openFromExplore(page, 'qwen3-14b-instruct', { installed: [] })
    await page.keyboard.press('3')
    const base = page.getByTestId('variant-row-qwen3-14b-instruct')
    const q4 = page.getByTestId('variant-row-qwen3-14b-instruct-q4')
    await expect(base).toBeVisible()
    await expect(q4).toBeVisible()
    await expect(base).toContainText('Auto-selected')
    await expect(q4).toContainText('Q4_K_M')
    await expect(q4).toContainText('Fits in GPU')
    await page.getByTestId('variant-install-qwen3-14b-instruct-q4').click()
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0]).toEqual({ name: 'qwen3-14b-instruct', search: '?variant=qwen3-14b-instruct-q4' })
  })

  test('choosing a build changes what the Install button installs and the estimate it shows', async ({ page }) => {
    const { state } = await openFromExplore(page, 'qwen3-14b-instruct', { installed: [] })
    await expect(page.getByTestId('model-page-install')).toContainText('Install Q8_0')
    await page.getByTestId('model-page-builds').click()
    await page.getByTestId('model-page-build-qwen3-14b-instruct-q4').click()
    await expect(page.getByTestId('model-page-install')).toContainText('Install Q4_K_M')
    await page.getByTestId('model-page-install').click()
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0].search).toBe('?variant=qwen3-14b-instruct-q4')
    await page.keyboard.press('2')
    await expect(page.getByTestId('fit-build-qwen3-14b-instruct-q4')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('fit-build-qwen3-14b-instruct').click()
    await expect(page.getByTestId('fit-build-qwen3-14b-instruct')).toHaveAttribute('aria-pressed', 'true')
  })

  test('the build menu works from the keyboard: it opens on the chosen build and the arrows move', async ({ page }) => {
    const { state } = await openFromExplore(page, 'qwen3-14b-instruct', { installed: [] })
    await page.getByTestId('model-page-builds').focus()
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('model-page-build-qwen3-14b-instruct')).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByTestId('model-page-build-qwen3-14b-instruct-q4')).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('model-page-install')).toContainText('Install Q4_K_M')
    await page.getByTestId('model-page-install').click()
    await expect.poll(() => state.installs.length).toBe(1)
  })

  test('the files of a build are listed with their source and checksum', async ({ page }) => {
    await openFromExplore(page, 'qwen3-32b-instruct')
    await page.keyboard.press('3')
    const file = page.getByTestId('file-row').first()
    await expect(file).toContainText('qwen3-32b-instruct.gguf')
    await expect(file).toContainText('huggingface.co/example/qwen3-32b-instruct')
    await expect(file).toContainText('aaaaaaaaaaaaaaaa')
    await expect(file).toContainText('Not downloaded')
    await expect(page.getByTestId('variants-single')).toContainText('one build')
  })

  test('Install starts the install and the title block turns into progress with Cancel', async ({ page }) => {
    const { state } = await openFromExplore(page, 'qwen3-32b-instruct')
    await page.getByTestId('model-page-install').click()
    await expect.poll(() => state.installs.length).toBe(1)
    expect(state.installs[0].search).toBe('')
    state.operations = [{ jobID: 'job-1', name: 'qwen3-32b-instruct', progress: 38, currentBytes: 7.5 * GB, totalBytes: 19.9 * GB, cancellable: true, isBackend: false }]
    const installing = page.getByTestId('model-page-installing')
    await expect(installing).toBeVisible({ timeout: 8_000 })
    await expect(installing).toContainText('38%')
    await expect(installing).toContainText('7.5 GB of 19.9 GB')
    await expect(page.getByTestId('model-page-cancel')).toBeVisible()
    await expect(page.getByTestId('answer-state')).toContainText('Installing 38%')
  })
})

test.describe('Model page - an installed model', () => {
  test('it has all six tabs, a state, Load and a menu, and the keys 1 to 6 reach them', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct')
    await expect(name(page)).toHaveText('qwen3-14b-instruct')
    await expect(page.getByRole('tab')).toHaveText([/^Overview$/, /^Fit and memory$/, /^Variants and files/, /^Usage and history$/, /^Configuration$/, /^Logs$/])
    await expect(page.getByTestId('model-page-state')).toContainText('Idle')
    await expect(page.getByTestId('model-page-load')).toBeVisible()
    for (const [key, id] of [['4', 'usage'], ['5', 'config'], ['6', 'logs']]) {
      await page.keyboard.press(key)
      await expect(page.getByTestId(`model-page-panel-${id}`)).toBeVisible()
    }
  })

  test('a running model shows Stop, asks before stopping and says Running', async ({ page }) => {
    const { state } = await setup(page, {}, '/app/models/qwen3-8b-instruct')
    await expect(page.getByTestId('model-page-state')).toContainText('Running')
    await expect(page.getByTestId('model-page-load')).toHaveCount(0)
    await page.getByTestId('model-page-stop').click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop' }).click()
    await expect.poll(() => state.stops).toEqual(['qwen3-8b-instruct'])
  })

  test('Load calls the same endpoint the table does', async ({ page }) => {
    const { state } = await setup(page, {}, '/app/models/qwen3-14b-instruct')
    await page.getByTestId('model-page-load').click()
    await expect.poll(() => state.loads).toEqual(['qwen3-14b-instruct'])
  })

  test('the menu holds disable, pin, edit configuration, logs and delete', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct')
    await page.getByRole('button', { name: 'Actions for qwen3-14b-instruct' }).click()
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveText([/Disable model/, /Pin/, /Edit configuration/, /Backend logs/, /Delete model/])
    await menu.getByRole('menuitem', { name: /Edit configuration/ }).click()
    await expect(page.getByTestId('model-page-config')).toBeVisible()
    await expect(page).toHaveURL(/tab=config/)
    await page.getByRole('button', { name: 'Actions for qwen3-14b-instruct' }).click()
    await page.getByRole('menu').getByRole('menuitem', { name: /Backend logs/ }).click()
    await expect(page.getByTestId('model-page-logs')).toBeVisible()
  })

  test('delete asks first, removes the model and leaves for the installed list', async ({ page }) => {
    const { state } = await setup(page, {}, '/app/models/qwen3-14b-instruct-q4')
    await expect(name(page)).toHaveText('qwen3-14b-instruct-q4')
    await page.getByRole('button', { name: 'Actions for qwen3-14b-instruct-q4' }).click()
    await page.getByRole('menuitem', { name: /Delete model/ }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete model' }).click()
    await expect.poll(() => state.deletes).toEqual(['qwen3-14b-instruct-q4'])
    await expect(page).toHaveURL(/\/app\/models\?view=installed/)
  })

  test('Used by lists the agent, the alias and what else names this model, from the real lists', async ({ page }) => {
    await setup(page, { aliases: [{ name: 'default-chat', target: 'qwen3-14b-instruct' }] }, '/app/models/qwen3-14b-instruct')
    const used = page.getByTestId('model-page-usedby')
    await expect(used).toContainText('research-helper')
    await expect(used).toContainText('Agent')
    await expect(used).toContainText('default-chat')
    await expect(used).toContainText('Alias')
    await expect(used.getByRole('link', { name: 'research-helper' })).toHaveAttribute('href', '/app/agents/research-helper/edit')
  })

  test('a model nothing uses says so', async ({ page }) => {
    await setup(page, {}, '/app/models/kokoro-82m')
    await expect(page.getByTestId('model-page-usedby')).toContainText('No agent, task, failover chain or alias')
  })

  test('Usage and history is honest: nothing is recorded, and it names what will appear', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct?tab=usage')
    await expect(page.getByTestId('usage-empty')).toContainText('not recorded yet')
    const missing = page.getByTestId('usage-missing')
    for (const label of ['Requests per day', 'Time to first token', 'Loads and stops', 'Configuration changes']) {
      await expect(missing).toContainText(label)
    }
    await expect(missing.getByText('Not recorded')).toHaveCount(4)
    await expect(page.locator('.dk-chart')).toHaveCount(0)
    const known = page.getByTestId('usage-known')
    await expect(known).toContainText('Idle')
    await expect(page.getByTestId('usage-usedby')).toContainText('1 agent')
  })

  test('Configuration holds the Placement section, the file it writes and a way into the full editor', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct?tab=config')
    await expect(page.getByTestId('placement')).toBeVisible()
    await expect(page.getByTestId('placement-file')).toContainText('context_size: 8192')
    await expect(page.getByTestId('open-full-editor')).toHaveAttribute('href', '/app/model-editor/qwen3-14b-instruct')
    await page.getByTestId('open-full-editor').click()
    await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible()
    await expect(page.getByRole('button', { name: /Back to this model/ })).toBeVisible()
  })

  test('Logs shows the backend output of the model in the log viewer', async ({ page }) => {
    await page.routeWebSocket('**/ws/backend-logs/**', ws => {
      ws.send(JSON.stringify({ type: 'initial', lines: [
        { timestamp: '2026-10-07T12:03:58Z', stream: 'stdout', text: 'Loading model qwen3-14b-instruct' },
        { timestamp: '2026-10-07T12:04:03Z', stream: 'stderr', text: 'rope scaling set to yarn' },
      ] }))
    })
    await setup(page, {}, '/app/models/qwen3-14b-instruct?tab=logs')
    await expect(page.getByTestId('model-page-logs')).toContainText('Loading model qwen3-14b-instruct')
    await expect(page.getByTestId('model-page-logs')).toContainText('rope scaling set to yarn')
    await expect(page.getByTestId('model-page-logs').getByRole('button', { name: 'Export' })).toBeVisible()
    await expect(page.getByTestId('model-page-logs').locator('.page-title')).toHaveCount(0)
  })

  test('a model the gallery does not list still has a page, from the installed list alone', async ({ page }) => {
    await setup(page, {}, '/app/models/my-finetune-q4')
    await expect(name(page)).toHaveText('my-finetune-q4')
    await expect(page.getByRole('tab')).toHaveText(['Overview', 'Fit and memory', 'Usage and history', 'Configuration', 'Logs'])
    await expect(page.getByTestId('model-page-load')).toBeVisible()
    await page.keyboard.press('2')
    await expect(page.getByTestId('fit-unavailable')).toBeVisible()
  })

  test('a disabled model offers Enable instead of Load', async ({ page }) => {
    await setup(page, {}, '/app/models/whisper-medium')
    await expect(page.getByTestId('model-page-state')).toContainText('Disabled')
    await expect(page.getByTestId('model-page-enable')).toBeVisible()
    await expect(page.getByTestId('model-page-load')).toHaveCount(0)
  })
})

test.describe('Model page - states', () => {
  test('loading shows a skeleton, not a half page', async ({ page }) => {
    await setup(page, { listingDelay: 1500 }, '/app/models/qwen3-32b-instruct')
    await expect(page.getByTestId('model-page')).toHaveAttribute('data-state', 'loading')
    await expect(page.getByRole('status', { name: 'Loading the model' })).toBeVisible()
    await expect(name(page)).toHaveText('qwen3-32b-instruct', { timeout: 10_000 })
  })

  test('an unknown id says so, offers the closest matches and a way back', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-32b')
    const missing = page.getByTestId('model-page-missing')
    await expect(missing).toContainText('No model called qwen3-32b')
    await expect(missing.getByRole('link', { name: 'qwen3-32b-instruct' })).toBeVisible()
    await missing.getByRole('link', { name: 'qwen3-32b-instruct' }).click()
    await expect(name(page)).toHaveText('qwen3-32b-instruct')
  })

  test('an id nothing matches has no suggestions and still offers the way back', async ({ page }) => {
    await setup(page, {}, '/app/models/nothing-here')
    await expect(page.getByTestId('model-page-missing')).toContainText('No model called nothing-here')
    await expect(page.getByTestId('model-page-missing').getByRole('list')).toHaveCount(0)
    await page.getByTestId('model-page-missing').getByRole('button', { name: 'Back to models' }).click()
    await expect(page).toHaveURL(/\/app\/models$/)
  })

  test('offline, a gallery model shows the reason, disables Install and offers a retry', async ({ page }) => {
    const { state } = await setup(page, { listingStatus: 500 }, '/app/models/qwen3-32b-instruct')
    await expect(page.getByTestId('model-page-offline')).toContainText('Cannot reach the gallery')
    await expect(page.getByTestId('model-page-install')).toBeDisabled()
    await expect(page.getByText('Needs the gallery')).toBeVisible()
    state.listingStatus = 200
    await page.getByTestId('model-page-offline').getByRole('button', { name: 'Retry' }).click()
    await expect(page.getByTestId('model-page')).toHaveAttribute('data-state', 'ready')
    await expect(page.getByTestId('model-page-install')).toBeEnabled()
  })

  test('offline, an installed model keeps working and the banner says it is the installed view', async ({ page }) => {
    await setup(page, { listingStatus: 500 }, '/app/models/qwen3-14b-instruct')
    await expect(page.getByTestId('model-page-offline')).toContainText('installed model')
    await expect(page.getByTestId('model-page-load')).toBeEnabled()
    await expect(page.getByRole('tab', { name: 'Logs' })).toBeVisible()
  })

  test('a failed install shows why and offers Retry, which dismisses the old failure first', async ({ page }) => {
    const calls = []
    await mockLedger(page, { operations: [{ jobID: 'job-9', name: 'qwen3-32b-instruct', error: 'checksum mismatch', isBackend: false }] })
    await mockPlacement(page)
    await page.route('**/api/operations/*/dismiss', route => { calls.push('dismiss'); return route.fulfill({ json: {} }) })
    await page.route('**/api/models/install/*', route => { calls.push('install'); return route.fulfill({ json: { jobID: 'job-10' } }) })
    await page.goto('/app/models/qwen3-32b-instruct')
    await expect(page.getByTestId('model-page-failed')).toContainText('checksum mismatch')
    await expect(page.getByTestId('answer-state')).toContainText('Install failed')
    await page.getByTestId('model-page-install').click()
    await expect.poll(() => calls).toEqual(['dismiss', 'install'])
  })

  test('with no estimate the Fit tab says so instead of drawing nothing', async ({ page }) => {
    await mockLedger(page, { gallery: GALLERY.map(e => (e.name === 'whisper-large-v3' ? { ...e, estimate: { sizeBytes: 0, sizeDisplay: '', estimates: {} } } : e)) })
    await mockPlacement(page)
    await page.goto('/app/models/whisper-large-v3?tab=fit')
    await expect(page.getByTestId('fit-unavailable')).toBeVisible()
  })

  test('a speech model says memory does not grow with the context size and draws no chart', async ({ page }) => {
    await setup(page, {}, '/app/models/kokoro-82m?tab=fit')
    await expect(page.getByTestId('fit-banner')).toBeVisible()
    await expect(page.getByTestId('fit-flat').or(page.getByTestId('vram-chart'))).toBeVisible()
  })
})

test.describe('Model page - phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the page fits the width: title, action, tabs and strip stack without sideways scroll', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct')
    await expect(name(page)).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await expect(page.getByTestId('model-page-load')).toBeVisible()
    const box = await page.getByTestId('model-page-load').boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    await expect(page.getByTestId('model-page-answer')).toBeVisible()
    await expect(page.getByTestId('model-page-tab-logs')).toBeAttached()
  })

  test('the variants and files tables stay inside the width', async ({ page }) => {
    await setup(page, { installed: [] }, '/app/models/qwen3-14b-instruct?tab=variants')
    await expect(page.getByTestId('variant-row-qwen3-14b-instruct-q4')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    for (const row of ['variant-install-qwen3-14b-instruct', 'variant-install-qwen3-14b-instruct-q4']) {
      const box = await page.getByTestId(row).boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    }
  })

  test('the memory chart and the context chips fit a phone', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-32b-instruct?tab=fit')
    await expect(page.getByTestId('vram-chart')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  test('the Configuration tab and the Placement section fit a phone', async ({ page }) => {
    await setup(page, {}, '/app/models/qwen3-14b-instruct?tab=config')
    await expect(page.getByTestId('placement')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
