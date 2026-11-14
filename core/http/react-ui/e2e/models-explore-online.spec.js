import { test, expect } from './coverage-fixtures.js'
import { mockLedger, GB } from './ledger-fixtures.js'

// The online sources of the Explore tab: Hugging Face and Pirate Face next to
// the library chips. The server is mocked: the specs pin what the page asks
// for (one provider, the debounce, lazy estimates, the quantization of an
// install), and what it shows for sizes still loading, a gated model, a source
// that does not answer and an install that is running.

const CTX = [8192, 16384, 32768, 65536, 131072, 262144]

const HF = (id, extra = {}) => ({
  provider: 'huggingface', id, title: id, uri: `huggingface://${id}`, url: `https://huggingface.co/${id}`,
  downloads: 3_400_000, likes: 120, license: 'apache-2.0', tags: ['gguf', 'license:apache-2.0', 'chat'],
  pipeline_tag: 'text-generation', backend: 'llama-cpp', has_variants: true, ...extra,
})
const PF = (id, extra = {}) => ({
  provider: 'pirateface', id, title: id, uri: `pirateface://${id}`, url: `https://huggingface.co/${id}`,
  size_bytes: 1_500_000_000, seeders: 6, has_variants: true, ...extra,
})

const RESULTS = [
  HF('acme/Tiny-GGUF'),
  HF('acme/Big-GGUF', { downloads: 900, license: 'mit' }),
  HF('acme/Locked-GGUF', { gated: true, downloads: 5 }),
]

// vram of a file of `size` bytes at a context: the weights plus a term that
// grows with the window, so the context slider has something to move.
const vram = (size, ctx) => Math.round(size * 1.04 + 0.15e9 + ctx * 40_000)
const estimateFor = (sizes, rec) => {
  const variant = (q, size) => ({
    file: `m-${q.toLowerCase()}.gguf`, quantization: q, sizeBytes: size, sizeDisplay: `${(size / GB).toFixed(1)} GB`,
    estimates: Object.fromEntries(CTX.map(c => [c, { contextLength: c, vramBytes: vram(size, c) }])),
  })
  const vs = Object.entries(sizes).map(([q, s]) => variant(q, s))
  const head = vs.find(v => v.quantization === rec)
  return { sizeBytes: head.sizeBytes, sizeDisplay: head.sizeDisplay, estimates: head.estimates, blockCount: 32, modelMaxContext: 32768, file: head.file, quantization: rec, source: 'gguf-header', variants: vs }
}

const FILES = (id, extra = {}) => ({
  provider: 'huggingface', id, uri: `huggingface://${id}`, revision: 'abc', gated: false, license: 'apache-2.0',
  description: 'A small chat model used by the tests of this page.', backend: 'llama-cpp', size_known: true, installed: false,
  tags: ['gguf', 'chat'],
  files: [],
  variants: [
    { quantization: 'Q4_K_M', file: 'm-q4_k_m.gguf', files: ['m-q4_k_m.gguf'], size_bytes: 4 * GB, shards: 1, preference: 'q4_k_m', recommended: true },
    { quantization: 'Q5_K_M', file: 'm-q5_k_m.gguf', files: ['m-q5_k_m.gguf'], size_bytes: 5 * GB, shards: 1, preference: 'q5_k_m' },
    { quantization: 'Q8_0', file: 'm-q8_0.gguf', files: ['m-q8_0.gguf'], size_bytes: 8 * GB, shards: 1, preference: 'q8_0' },
  ],
  ...extra,
})

// Stubs the three remote routes and the import route on top of mockLedger.
async function mockOnline(page, options = {}) {
  const {
    results = RESULTS, pf = [PF('acme/Peer'), PF('acme/Locked-P2P', { gated: true })],
    estimate = () => estimateFor({ Q4_K_M: 4 * GB, Q5_K_M: 5 * GB, Q8_0: 8 * GB }, 'Q4_K_M'),
    estimateDelay = 0, searchDelay = {}, filesFor = (id) => FILES(id), searchStatus = {}, jobProgress = [40, 100],
  } = options
  const seen = { search: [], files: [], estimate: [], imports: [], jobs: 0 }
  await page.route(/\/api\/models\/search\?/, async route => {
    const q = new URL(route.request().url()).searchParams
    seen.search.push(q)
    const provider = q.get('providers')
    if (searchDelay[provider]) await new Promise(r => setTimeout(r, searchDelay[provider]))
    if (searchStatus[provider]) {
      return route.fulfill({ json: { results: [], providers: [{ name: provider, ok: false, count: 0, error: searchStatus[provider] }] } })
    }
    const base = provider === 'pirateface' ? pf : results
    const limit = Number(q.get('limit') || 20)
    const list = base.slice(0, limit)
    return route.fulfill({ json: { results: list, providers: [{ name: provider, ok: true, count: list.length }] } })
  })
  await page.route(/\/api\/models\/remote\/files\?/, route => {
    const uri = new URL(route.request().url()).searchParams.get('uri')
    seen.files.push(uri)
    const id = uri.replace(/^[a-z]+:\/\//, '')
    return route.fulfill({ json: { ...filesFor(id), provider: uri.startsWith('pirateface') ? 'pirateface' : 'huggingface', uri, gated: id.includes('Locked') } })
  })
  await page.route(/\/api\/models\/remote\/estimate\?/, async route => {
    const q = new URL(route.request().url()).searchParams
    seen.estimate.push({ uri: q.get('uri'), contexts: q.get('contexts'), file: q.get('file') })
    if (estimateDelay) await new Promise(r => setTimeout(r, estimateDelay))
    const out = estimate(q.get('uri'))
    if (out === 'hang') return
    if (out === 429) return route.fulfill({ status: 429, headers: { 'retry-after': '60' }, json: { error: { message: 'rate limited' } } })
    return route.fulfill({ json: out })
  })
  await page.route('**/models/import-uri', route => {
    seen.imports.push(route.request().postDataJSON())
    return route.fulfill({ json: { uuid: 'job-uuid-1', status: '/models/jobs/job-uuid-1' } })
  })
  let polls = 0
  await page.route('**/models/jobs/*', route => {
    seen.jobs += 1
    const p = jobProgress[Math.min(polls++, jobProgress.length - 1)]
    return route.fulfill({ json: p >= 100 ? { processed: true, progress: 100, message: 'completed', gallery_element_name: 'm-q4_k_m' } : { processed: false, progress: p, message: 'downloading' } })
  })
  return seen
}

const rows = (page) => page.locator('[data-testid="discover-rail-item"]')
const row = (page, key) => page.locator(`[data-entity="${key}"]`)
const pane = (page) => page.getByTestId('discover-pane')
const chip = (page, id) => page.getByTestId(`source-chip-${id}`)

async function openSource(page, source, term, options) {
  await mockLedger(page, options?.ledger)
  const seen = await mockOnline(page, options)
  await page.goto('/app/models')
  await expect(page.locator('[data-testid="discover-rail-item"]').first()).toBeVisible({ timeout: 15_000 })
  // Under All the page already asked Hugging Face for its newest models; the
  // specs below count the requests the chip makes.
  await expect.poll(() => seen.search.length).toBeGreaterThan(0)
  seen.search.length = 0
  await chip(page, source).click()
  if (term) {
    await page.getByTestId('models-search').fill(term)
    if (options?.waitRows !== false) await expect(rows(page).first()).toBeVisible({ timeout: 10_000 })
  }
  return seen
}

test.describe('Explore: online sources', () => {
  test('the two source chips sit with the library chips and Pirate Face carries a p2p tag', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page)
    await page.goto('/app/models')
    await expect(chip(page, 'huggingface')).toHaveText('Hugging Face')
    await expect(chip(page, 'pirateface')).toContainText('Pirate Face')
    await expect(chip(page, 'pirateface')).toContainText('p2p')
    await expect(chip(page, 'huggingface')).toHaveAttribute('aria-pressed', 'false')
    await expect(page.getByRole('button', { name: 'All libraries' })).toHaveAttribute('aria-pressed', 'true')
  })

  test('selecting a source shows its newest models, then a term searches that one provider after the debounce', async ({ page }) => {
    const seen = await openSource(page, 'huggingface')
    await expect(page).toHaveURL(/source=huggingface/)
    await expect(page.getByTestId('discover-rail-heading')).toContainText('Latest on Hugging Face', { timeout: 10_000 })
    await expect(rows(page)).toHaveCount(3)
    expect(seen.search).toHaveLength(1)

    const input = page.getByTestId('models-search')
    await expect(input).toHaveAttribute('placeholder', 'Search Hugging Face')
    await input.pressSequentially('tiny', { delay: 30 })
    await expect(page.getByTestId('discover-rail-heading')).toContainText('From Hugging Face')
    // one request for the whole word, not one per key
    await expect.poll(() => seen.search.length).toBe(2)
    await page.waitForTimeout(600)
    expect(seen.search).toHaveLength(2)
    const q = seen.search[1]
    expect(q.get('q')).toBe('tiny')
    expect(q.get('providers')).toBe('huggingface')
    expect(q.get('limit')).toBe('20')
    expect(q.get('format')).toBe('gguf')
  })

  test('results use the Explore table under a section header, keyed by provider and repository', async ({ page }) => {
    await openSource(page, 'huggingface', 'tiny')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('From Hugging Face')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('3')
    const r = row(page, 'huggingface:acme/Tiny-GGUF')
    await expect(r).toBeVisible()
    await expect(r).toContainText('acme/Tiny-GGUF')
    // author, licence, downloads
    await expect(r.locator('.dk-table-sub')).toContainText('acme · apache-2.0 · 3.4M downloads')
    await expect(page.getByTestId('remote-count')).toContainText('3 results')
    // the gallery columns and no gallery-only controls
    await expect(page.getByRole('columnheader', { name: /Model Name/ })).toBeVisible()
    await expect(page.getByTestId('models-collapse-variants')).toHaveCount(0)
    await expect(page.getByTestId('remote-gguf-only')).toBeVisible()
    await expect(page.locator('.pagination')).toHaveCount(0)
    // a gated repository says so in the row
    await expect(row(page, 'huggingface:acme/Locked-GGUF').getByTestId('row-gated')).toBeVisible()
  })

  test('a row says sizing until its estimate arrives, then shows size and fit', async ({ page }) => {
    await openSource(page, 'huggingface', 'tiny', { estimateDelay: 1200 })
    const r = row(page, 'huggingface:acme/Tiny-GGUF')
    await expect(r.locator('.ledger-fit')).toContainText(/sizing/i)
    await expect(r.locator('.ledger-fit[data-fit="fits"]')).toBeVisible({ timeout: 10_000 })
    await expect(r.locator('.ledger-size')).toContainText('4.0')
  })

  test('estimates are asked per row, once, for every context at once', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF').locator('.ledger-fit[data-fit]')).toBeVisible({ timeout: 10_000 })
    await expect.poll(() => seen.estimate.length).toBe(3)
    expect(new Set(seen.estimate.map(e => e.uri)).size).toBe(3)
    expect(seen.estimate[0].contexts).toBe(CTX.join(','))
    expect(seen.estimate[0].file).toBeNull()
  })

  test('"Fits in memory" keeps a row whose estimate is still loading and hides only a known non-fit', async ({ page }) => {
    // 24 GB card: Big needs more than that, Locked never answers.
    const estimate = (uri) => {
      if (uri.includes('Big')) return estimateFor({ Q4_K_M: 40 * GB, Q8_0: 70 * GB }, 'Q4_K_M')
      if (uri.includes('Locked')) return 'hang'
      return estimateFor({ Q4_K_M: 4 * GB, Q8_0: 8 * GB }, 'Q4_K_M')
    }
    await openSource(page, 'huggingface', 'tiny', { estimate })
    await expect(row(page, 'huggingface:acme/Big-GGUF').locator('.ledger-fit[data-fit]')).toBeVisible({ timeout: 10_000 })
    await expect(row(page, 'huggingface:acme/Locked-GGUF').locator('.ledger-fit')).toContainText(/sizing/i)
    await page.getByText('Fits in GPU').click()
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible()
    await expect(row(page, 'huggingface:acme/Locked-GGUF')).toBeVisible()
    await expect(row(page, 'huggingface:acme/Big-GGUF')).toHaveCount(0)
  })

  test('a row opens the inspector with one line per quantization, its size and fit', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    await row(page, 'huggingface:acme/Tiny-GGUF').click()
    await expect(page).toHaveURL(/model=huggingface%3Aacme%2FTiny-GGUF/)
    await expect(pane(page)).toContainText('A small chat model used by the tests of this page.')
    const variants = pane(page).getByTestId('remote-variant')
    await expect(variants).toHaveCount(3)
    await expect(variants.nth(0)).toContainText('Q4_K_M')
    await expect(variants.nth(0)).toContainText(/4(\.0)? GB|4\.3 GB/)
    await expect(variants.nth(0)).toContainText('Recommended')
    await expect(variants.nth(2)).toContainText('Q8_0')
    // fit of each build, from the one estimate
    await expect(variants.nth(0)).not.toContainText('Too big')
    await expect(variants.nth(0)).toHaveAttribute('aria-pressed', 'true')
    expect(seen.files).toContain('huggingface://acme/Tiny-GGUF')
    // there is no model page to open
    await expect(pane(page).getByTestId('inspector-open-page')).toHaveCount(0)
    await expect(row(page, 'huggingface:acme/Tiny-GGUF').getByTestId('row-open')).toHaveCount(0)
  })

  test('choosing a build and installing sends its quantization and follows the job by its id', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    await row(page, 'huggingface:acme/Tiny-GGUF').click()
    const variants = pane(page).getByTestId('remote-variant')
    await expect(variants).toHaveCount(3)
    await variants.nth(2).click()
    await expect(variants.nth(2)).toHaveAttribute('aria-pressed', 'true')
    await expect(variants.nth(0)).toHaveAttribute('aria-pressed', 'false')
    // the size and memory in the stat grid are the chosen build's
    await expect(pane(page)).toContainText('8.0 GB')

    await pane(page).getByTestId('discover-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0]).toEqual({ uri: 'huggingface://acme/Tiny-GGUF', preferences: { quantizations: 'q8_0' } })
    // progress comes from the job route, by uuid; the row and the pane show it
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toContainText(/Installing/, { timeout: 10_000 })
    await expect(pane(page)).toContainText(/Installing.*40|40/, { timeout: 10_000 })
    // when the job is processed the row is installed
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toContainText('Installed', { timeout: 10_000 })
    expect(seen.jobs).toBeGreaterThan(1)
  })

  test('installing from the row uses the recommended build without opening the inspector', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    await row(page, 'huggingface:acme/Tiny-GGUF').getByTestId('discover-row-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].preferences).toEqual({ quantizations: 'q4_k_m' })
  })

  test('an install pressed while the file list is still loading waits for it instead of failing', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockOnline(page)
    // Registered after mockOnline, so it answers first, slowly.
    await page.route(/\/api\/models\/remote\/files\?/, async route => {
      const uri = new URL(route.request().url()).searchParams.get('uri')
      await new Promise(r => setTimeout(r, 1500))
      return route.fulfill({ json: { ...FILES('acme/Big-GGUF'), uri } })
    })
    await page.goto('/app/models')
    await chip(page, 'huggingface').click()
    await page.getByTestId('models-search').fill('big')
    await expect(rows(page).first()).toBeVisible({ timeout: 10_000 })
    await row(page, 'huggingface:acme/Big-GGUF').click()
    await pane(page).getByTestId('discover-install').click()
    await expect.poll(() => seen.imports.length, { timeout: 10_000 }).toBe(1)
    await expect(page.getByText('Could not read the file list')).toHaveCount(0)
  })

  test('a gated Hugging Face repository cannot be installed, from the row or from the inspector', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    const locked = row(page, 'huggingface:acme/Locked-GGUF')
    await expect(locked.getByTestId('row-gated')).toBeVisible()
    await expect(locked.getByTestId('discover-row-install')).toBeDisabled()
    // an ungated row next to it still installs
    await expect(row(page, 'huggingface:acme/Tiny-GGUF').getByTestId('discover-row-install')).toBeEnabled()

    await locked.click()
    await expect(page.getByTestId('remote-gated')).toContainText('needs approval on Hugging Face')
    await expect(pane(page).getByTestId('discover-install')).toBeDisabled()
    // Enter on the row does not start an install either
    await locked.focus()
    await page.keyboard.press('Enter')
    expect(seen.imports).toHaveLength(0)
  })

  test('a gated Pirate Face repository installs from the row and the inspector, and says the licence still applies', async ({ page }) => {
    const seen = await openSource(page, 'pirateface', 'peer')
    const locked = row(page, 'pirateface:acme/Locked-P2P')
    await expect(locked.getByTestId('row-gated')).toBeVisible()
    await expect(locked.getByTestId('discover-row-install')).toBeEnabled()

    await locked.click()
    await expect(page.getByTestId('remote-gated')).toContainText('licence terms')
    await expect(page.getByTestId('remote-gated')).toContainText('no token')
    await expect(pane(page).getByTestId('discover-install')).toBeEnabled()
    await pane(page).getByTestId('discover-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].uri).toBe('pirateface://acme/Locked-P2P')
  })

  test('the install button of a gated Pirate Face row starts the install', async ({ page }) => {
    const seen = await openSource(page, 'pirateface', 'peer')
    await row(page, 'pirateface:acme/Locked-P2P').getByTestId('discover-row-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].uri).toBe('pirateface://acme/Locked-P2P')
  })

  test('a Pirate Face row with one seeder says "1 seed", not "1 seeds"', async ({ page }) => {
    await openSource(page, 'pirateface', 'peer', { pf: [PF('acme/Peer', { seeders: 1 }), PF('acme/Crowd', { seeders: 6 })] })
    await expect(row(page, 'pirateface:acme/Peer').locator('.dk-table-sub')).toContainText(/1 seed(?!s)/)
    await expect(row(page, 'pirateface:acme/Peer').locator('.dk-table-sub')).not.toContainText('1 seeds')
    await expect(row(page, 'pirateface:acme/Crowd').locator('.dk-table-sub')).toContainText('6 seeds')
  })

  test('Pirate Face rows are searched through the same table and install by their own uri', async ({ page }) => {
    const seen = await openSource(page, 'pirateface', 'peer')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('From Pirate Face')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('p2p')
    expect(seen.search.at(-1).get('providers')).toBe('pirateface')
    await expect(row(page, 'pirateface:acme/Peer').locator('.dk-table-sub')).toContainText('6 seeds')
    await row(page, 'pirateface:acme/Peer').getByTestId('discover-row-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].uri).toBe('pirateface://acme/Peer')
  })

  test('a source that does not answer is said so, with a way to retry', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny', { waitRows: false, searchStatus: { huggingface: 'rate limited by Hugging Face (status 429)' } })
    await expect(page.getByTestId('remote-unavailable')).toContainText('Hugging Face did not answer')
    await expect(page.getByTestId('remote-unavailable')).toContainText('rate limited')
    expect(seen.search).toHaveLength(1)
    await page.getByTestId('remote-unavailable').getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => seen.search.length).toBe(2)
  })

  test('a search with nothing in it says so, and GGUF only can be turned off', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'zzz', { waitRows: false, results: [] })
    await expect(page.getByTestId('remote-empty')).toContainText('Nothing found on Hugging Face')
    await expect(page.getByTestId('remote-empty')).toContainText('GGUF')
    await page.getByTestId('remote-gguf-only').click()
    await expect.poll(() => seen.search.at(-1).get('format')).toBe('any')
  })

  test('"Load more" asks the provider for more results, and stops at the cap', async ({ page }) => {
    const many = Array.from({ length: 50 }, (_, i) => HF(`acme/Model-${i}`))
    const seen = await openSource(page, 'huggingface', 'model', { results: many })
    await expect(rows(page)).toHaveCount(20)
    await page.getByTestId('remote-load-more').click()
    await expect(rows(page)).toHaveCount(40)
    expect(seen.search.at(-1).get('limit')).toBe('40')
    await page.getByTestId('remote-load-more').click()
    await expect(rows(page)).toHaveCount(50)
    expect(seen.search.at(-1).get('limit')).toBe('50')
    await expect(page.getByTestId('remote-load-more')).toHaveCount(0)
  })

  test('an installed repository is shown as installed, from the search answer', async ({ page }) => {
    await openSource(page, 'huggingface', 'tiny', { results: [HF('acme/Tiny-GGUF', { installed: true, installed_name: 'tiny-q4' })] })
    const r = row(page, 'huggingface:acme/Tiny-GGUF')
    await expect(r).toContainText('Installed')
    await expect(r.getByTestId('discover-row-install')).toHaveCount(0)
  })

  test('keys keep working: / focuses the search, arrows move the selection, Enter installs', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', 'tiny')
    await page.getByTestId('models-search').blur()
    await page.keyboard.press('/')
    await expect(page.getByTestId('models-search')).toBeFocused()
    await page.keyboard.press('Escape')
    await row(page, 'huggingface:acme/Tiny-GGUF').focus()
    await page.keyboard.press('ArrowDown')
    await expect(row(page, 'huggingface:acme/Big-GGUF')).toBeFocused()
    await expect(row(page, 'huggingface:acme/Big-GGUF')).toHaveAttribute('data-selected', 'true')
    await page.keyboard.press('Enter')
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].uri).toBe('huggingface://acme/Big-GGUF')
    await page.keyboard.press('Escape')
    await expect(page).not.toHaveURL(/model=/)
  })

  test('picking a library chip leaves the online source and its rows', async ({ page }) => {
    await openSource(page, 'huggingface', 'tiny')
    await page.getByRole('button', { name: 'All libraries' }).click()
    await expect(page).not.toHaveURL(/source=/)
    await expect(page.getByTestId('discover-rail-heading')).toHaveCount(0)
    await expect(page.getByTestId('models-collapse-variants')).toBeVisible()
  })

  test('the address holds the source, the term and the row, so a reload comes back to it', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockOnline(page)
    await page.goto('/app/models?source=huggingface&q=tiny&model=huggingface%3Aacme%2FTiny-GGUF')
    await expect(rows(page)).toHaveCount(3, { timeout: 15_000 })
    await expect(chip(page, 'huggingface')).toHaveAttribute('aria-pressed', 'true')
    await expect(pane(page).getByTestId('remote-variant')).toHaveCount(3)
    expect(seen.search[0].get('q')).toBe('tiny')
  })

  test('a files lookup that fails says so in the inspector and offers a retry', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page)
    let fail = true
    await page.route(/\/api\/models\/remote\/files\?/, route => (fail
      ? route.fulfill({ status: 502, json: { error: { message: 'upstream down' } } })
      : route.fulfill({ json: FILES('acme/Tiny-GGUF') })))
    await page.goto('/app/models?source=huggingface&q=tiny&model=huggingface%3Aacme%2FTiny-GGUF')
    await expect(page.getByTestId('remote-files-error')).toContainText('Could not read the file list', { timeout: 15_000 })
    fail = false
    await page.getByTestId('remote-files-error').getByRole('button', { name: 'Retry' }).click()
    await expect(pane(page).getByTestId('remote-variant')).toHaveCount(3)
  })
})

// Under All a term searches the libraries and both online sources, each one
// a section of the same table that answers on its own.
const section = (page, id) => page.locator(`[data-testid="discover-section-heading"][data-section="${id}"]`)
const sectionBox = (page, id, testId) => page.locator(`[data-testid="${testId}"][data-section="${id}"]`)

async function searchAll(page, term, options) {
  await mockLedger(page, options?.ledger)
  const seen = await mockOnline(page, options)
  await page.goto('/app/models')
  await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('models-search').fill(term)
  return seen
}

test.describe('Explore: All covers the online sources', () => {
  test('a term under All shows the libraries, then Hugging Face, then Pirate Face, in one table', async ({ page }) => {
    const seen = await searchAll(page, 'qwen')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    await expect(row(page, 'pirateface:acme/Peer')).toBeVisible()
    await expect(row(page, 'qwen3-8b-instruct')).toBeVisible()

    await expect(page.getByRole('button', { name: 'All libraries' })).toHaveAttribute('aria-pressed', 'true')
    await expect(page).not.toHaveURL(/source=/)
    // One table, in this order: library rows, the two headings with their rows.
    const order = await page.locator('[data-testid="discover-rail"] tbody tr').evaluateAll(trs => trs.map(tr => (
      tr.getAttribute('data-section') && tr.getAttribute('data-testid') === 'discover-section-heading' ? `#${tr.getAttribute('data-section')}` : (tr.getAttribute('data-entity') || '')
    )).filter(Boolean))
    const hf = order.indexOf('#huggingface')
    const pf = order.indexOf('#pirateface')
    expect(order.indexOf('qwen3-8b-instruct')).toBeLessThan(hf)
    expect(hf).toBeLessThan(order.indexOf('huggingface:acme/Tiny-GGUF'))
    expect(order.indexOf('huggingface:acme/Locked-GGUF')).toBeLessThan(pf)
    expect(pf).toBeLessThan(order.indexOf('pirateface:acme/Peer'))
    await expect(section(page, 'huggingface')).toContainText('From Hugging Face')
    await expect(section(page, 'pirateface')).toContainText('From Pirate Face')

    // One request per source, a short page each.
    const asked = Object.fromEntries(seen.search.map(q => [q.get('providers'), q]))
    expect(Object.keys(asked).sort()).toEqual(['huggingface', 'pirateface'])
    expect(asked.huggingface.get('q')).toBe('qwen')
    expect(asked.huggingface.get('limit')).toBe('10')
    expect(asked.huggingface.get('format')).toBe('gguf')
  })

  test('each row type keeps its own actions: library install, online install, gated, inspector', async ({ page }) => {
    const seen = await searchAll(page, 'qwen')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    // A gated Hugging Face row cannot be installed; a gated Pirate Face one can.
    await expect(row(page, 'huggingface:acme/Locked-GGUF').getByTestId('discover-row-install')).toBeDisabled()
    await expect(row(page, 'pirateface:acme/Locked-P2P').getByTestId('discover-row-install')).toBeEnabled()
    // No "open page" arrow on an online row; a library row still has one.
    await expect(row(page, 'huggingface:acme/Tiny-GGUF').getByTestId('row-open')).toHaveCount(0)
    await expect(row(page, 'qwen3-8b-instruct').getByTestId('row-open')).toHaveCount(1)

    // The inspector of an online row reads the online listing.
    await row(page, 'huggingface:acme/Tiny-GGUF').click()
    await expect(pane(page).getByTestId('remote-variant')).toHaveCount(3, { timeout: 10_000 })

    // Install goes through the import route, with a job that finishes.
    await row(page, 'huggingface:acme/Tiny-GGUF').getByTestId('discover-row-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    expect(seen.imports[0].uri).toBe('huggingface://acme/Tiny-GGUF')
  })

  test('a library row installs through the gallery under All, next to online rows', async ({ page }) => {
    const state = await mockLedger(page)
    await mockOnline(page)
    await page.goto('/app/models')
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('models-search').fill('qwen3-4b')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    await row(page, 'qwen3-4b-instruct').getByTestId('discover-row-install').click()
    await expect.poll(() => state.installs.length).toBeGreaterThan(0)
    expect(state.installs.at(-1).name).toBe('qwen3-4b-instruct')
  })

  test('sections answer on their own: a slow source does not hold the others, a failing one says so', async ({ page }) => {
    const seen = await searchAll(page, 'qwen', {
      searchDelay: { huggingface: 2500 },
      searchStatus: { pirateface: 'upstream timeout' },
    })
    // The library rows and the failing section show while Hugging Face is still loading.
    await expect(row(page, 'qwen3-8b-instruct')).toBeVisible({ timeout: 10_000 })
    await expect(sectionBox(page, 'huggingface', 'discover-section-loading').first()).toBeAttached()
    await expect(sectionBox(page, 'pirateface', 'discover-section-error')).toContainText('Pirate Face did not answer')
    await expect(sectionBox(page, 'pirateface', 'discover-section-error')).toContainText('upstream timeout')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    await expect(sectionBox(page, 'huggingface', 'discover-section-loading')).toHaveCount(0)
    // The failure is the section's alone.
    await expect(sectionBox(page, 'huggingface', 'discover-section-error')).toHaveCount(0)

    // Retry asks Pirate Face again, and only it.
    const before = seen.search.filter(q => q.get('providers') === 'huggingface').length
    await sectionBox(page, 'pirateface', 'discover-section-error').getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => seen.search.filter(q => q.get('providers') === 'pirateface').length).toBe(2)
    expect(seen.search.filter(q => q.get('providers') === 'huggingface')).toHaveLength(before)
  })

  test('a source with nothing for the term says so under its heading, the others keep their rows', async ({ page }) => {
    await searchAll(page, 'qwen', { pf: [] })
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    await expect(sectionBox(page, 'pirateface', 'discover-section-empty')).toContainText('Nothing found on Pirate Face')
  })

  test('a section grows by ten rows with its own Load more, and the other one is not asked again', async ({ page }) => {
    const many = Array.from({ length: 30 }, (_, i) => HF(`acme/Model-${i}`))
    const seen = await searchAll(page, 'qwen', { results: many })
    await expect(row(page, 'huggingface:acme/Model-0')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('[data-entity^="huggingface:"]')).toHaveCount(10)
    const pfAsked = seen.search.filter(q => q.get('providers') === 'pirateface').length
    await page.locator('tr[data-section="huggingface"]').getByTestId('discover-section-more').click()
    await expect(page.locator('[data-entity^="huggingface:"]')).toHaveCount(20)
    expect(seen.search.at(-1).get('limit')).toBe('20')
    expect(seen.search.filter(q => q.get('providers') === 'pirateface')).toHaveLength(pfAsked)
    // Pirate Face filled no page, so it has no button.
    await expect(page.locator('tr[data-section="pirateface"]').getByTestId('discover-section-more')).toHaveCount(0)
  })

  test('a source chip narrows to that source, and All brings the sections back', async ({ page }) => {
    const seen = await searchAll(page, 'qwen')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 10_000 })
    await chip(page, 'huggingface').click()
    await expect(page).toHaveURL(/source=huggingface/)
    await expect(row(page, 'qwen3-8b-instruct')).toHaveCount(0)
    await expect(row(page, 'pirateface:acme/Peer')).toHaveCount(0)
    await expect(page.getByTestId('discover-rail-heading')).toContainText('From Hugging Face')
    expect(seen.search.at(-1).get('limit')).toBe('20')

    await page.getByRole('button', { name: 'All libraries' }).click()
    await expect(page).not.toHaveURL(/source=/)
    await expect(row(page, 'qwen3-8b-instruct')).toBeVisible()
    await expect(row(page, 'pirateface:acme/Peer')).toBeVisible()
    await expect(section(page, 'huggingface')).toBeVisible()
  })

  test('a library chip keeps the online sections out, and the address keeps the term under All', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page)
    await page.route('**/api/library/sources', route => (route.request().method() === 'GET'
      ? route.fulfill({ json: { officialLibraries: true, sources: LIBRARY_SOURCES } })
      : route.fallback()))
    await page.goto('/app/models?q=qwen')
    await expect(row(page, 'huggingface:acme/Tiny-GGUF')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Official model library' }).click()
    await expect(section(page, 'huggingface')).toHaveCount(0)
    await expect(section(page, 'pirateface')).toHaveCount(0)
    await page.getByRole('button', { name: 'All libraries' }).click()
    await expect(section(page, 'huggingface')).toBeVisible()
  })
})

// With no term an online source shows its newest models: Hugging Face from
// the models API sorted by creation date, Pirate Face a note, because its
// public pages have no list of new models outside /api.
test.describe('Explore: the latest models with no search', () => {
  const FRESH = [HF('acme/Fresh-GGUF', { downloads: 0 }), HF('acme/Newer-GGUF', { downloads: 0 })]

  test('a source chip with no term lists the newest Hugging Face models, with the same filters as a search', async ({ page }) => {
    const seen = await openSource(page, 'huggingface', '', { results: FRESH })
    await expect(rows(page)).toHaveCount(2, { timeout: 10_000 })
    await expect(page.getByTestId('discover-rail-heading')).toContainText('Latest on Hugging Face')
    const q = seen.search[0]
    expect(q.has('q')).toBe(false)
    expect(q.get('sort')).toBe('latest')
    expect(q.get('providers')).toBe('huggingface')
    expect(q.get('limit')).toBe('20')
    expect(q.get('format')).toBe('gguf')

    // GGUF only off widens the list, still newest first.
    await page.getByTestId('remote-gguf-only').click()
    await expect.poll(() => seen.search.at(-1).get('format')).toBe('any')
    expect(seen.search.at(-1).get('sort')).toBe('latest')

    // A term turns the list into a search, and clearing it brings the newest back.
    await page.getByTestId('models-search').fill('tiny')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('From Hugging Face')
    expect(seen.search.at(-1).get('q')).toBe('tiny')
    expect(seen.search.at(-1).has('sort')).toBe(false)
    await page.getByTestId('models-search').fill('')
    await expect(page.getByTestId('discover-rail-heading')).toContainText('Latest on Hugging Face')
    expect(seen.search.at(-1).get('sort')).toBe('latest')
  })

  test('Pirate Face has no list of new models: it asks nothing and says so', async ({ page }) => {
    const seen = await openSource(page, 'pirateface')
    await expect(page.getByTestId('remote-hint')).toContainText('Search Pirate Face')
    await expect(page.getByTestId('remote-hint')).toContainText('does not list its newest models')
    await expect(rows(page)).toHaveCount(0)
    await page.waitForTimeout(600)
    expect(seen.search).toHaveLength(0)
    // A term searches it as before.
    await page.getByTestId('models-search').fill('peer')
    await expect(row(page, 'pirateface:acme/Peer')).toBeVisible({ timeout: 10_000 })
    expect(seen.search.at(-1).get('q')).toBe('peer')
  })

  test('"Load more" on the newest list asks for a longer list, still newest first', async ({ page }) => {
    const many = Array.from({ length: 50 }, (_, i) => HF(`acme/New-${i}`))
    const seen = await openSource(page, 'huggingface', '', { results: many })
    await expect(rows(page)).toHaveCount(20, { timeout: 10_000 })
    await page.getByTestId('remote-load-more').click()
    await expect(rows(page)).toHaveCount(40)
    expect(seen.search.at(-1).get('limit')).toBe('40')
    expect(seen.search.at(-1).get('sort')).toBe('latest')
  })

  test('under All with no term: the library listing stays, the newest Hugging Face models follow, Pirate Face says it has no list', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockOnline(page, { results: Array.from({ length: 30 }, (_, i) => HF(`acme/Fresh-${i}`)) })
    await page.goto('/app/models')
    await expect(row(page, 'qwen3-4b-instruct')).toBeVisible({ timeout: 15_000 })
    await expect(section(page, 'huggingface')).toContainText('Latest on Hugging Face')
    await expect(page.locator('[data-entity^="huggingface:"]')).toHaveCount(10, { timeout: 10_000 })
    await expect(section(page, 'pirateface')).toContainText('Latest on Pirate Face')
    await expect(sectionBox(page, 'pirateface', 'discover-section-empty')).toContainText('does not list its newest models')
    // The library keeps its grouped listing, and nothing was asked of Pirate Face.
    await expect(page.getByTestId('discover-rail-group-text')).toBeVisible()
    expect(seen.search).toHaveLength(1)
    const q = seen.search[0]
    expect(q.get('providers')).toBe('huggingface')
    expect(q.get('sort')).toBe('latest')
    expect(q.get('limit')).toBe('10')
    expect(q.has('q')).toBe(false)
    // The newest rows install like any online row.
    await row(page, 'huggingface:acme/Fresh-0').getByTestId('discover-row-install').click()
    await expect.poll(() => seen.imports.length).toBe(1)
    // Its own button asks for ten more, and only for Hugging Face.
    await page.locator('tr[data-section="huggingface"]').getByTestId('discover-section-more').click()
    await expect(page.locator('[data-entity^="huggingface:"]')).toHaveCount(20)
    expect(seen.search.at(-1).get('limit')).toBe('20')
    expect(seen.search.at(-1).get('sort')).toBe('latest')
    expect(seen.search.every(x => x.get('providers') === 'huggingface')).toBe(true)
  })

  test('under All with no term, a failing newest list says so in its own section', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page, { searchStatus: { huggingface: 'rate limited by Hugging Face (status 429)' } })
    await page.goto('/app/models')
    await expect(row(page, 'qwen3-4b-instruct')).toBeVisible({ timeout: 15_000 })
    await expect(sectionBox(page, 'huggingface', 'discover-section-error')).toContainText('Hugging Face did not answer', { timeout: 10_000 })
    await expect(rows(page).first()).toBeVisible()
  })
})

// The two shipped records of the official models: the model library and the
// older gallery index. Only one is on in a build; the other one stays listed in
// Manage libraries, but it lists no models, so it gets no chip.
const LIBRARY_SOURCES = [
  { kind: 'model', id: 'official-models', name: 'Official model library', url: 'https://example.org/models/index.yaml', enabled: true, default: true, removed: false, suspended: false },
  { kind: 'model', id: 'localai', name: 'Official models', url: 'https://example.org/gallery/index.yaml', enabled: false, default: true, removed: false, suspended: false, format: 'gallery' },
  { kind: 'model', id: 'team', name: 'Team models', url: 'https://example.org/team.yaml', enabled: true, default: false, removed: false, suspended: false },
  { kind: 'model', id: 'old', name: 'Switched off', url: 'https://example.org/old.yaml', enabled: false, default: false, removed: false, suspended: false },
  { kind: 'model', id: 'gone', name: 'Removed default', url: 'https://example.org/gone.yaml', enabled: false, default: true, removed: true, suspended: false },
  { kind: 'backend', id: 'localai-backends', name: 'Official backends', url: 'https://example.org/backends.yaml', enabled: true, default: true, removed: false, suspended: false },
]

test.describe('Explore: library chips', () => {
  test('one chip per library that is on, then the two online sources', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page)
    await page.route('**/api/library/sources', route => (route.request().method() === 'GET'
      ? route.fulfill({ json: { officialLibraries: true, sources: LIBRARY_SOURCES } })
      : route.fallback()))
    await page.goto('/app/models')
    const group = page.getByRole('group', { name: 'Libraries' })
    await expect(group.getByRole('button')).toHaveText(['All libraries', 'Official model library', 'Team models', 'Hugging Face', /^Pirate Face/])
    await expect(page.getByRole('button', { name: 'Official models', exact: true })).toHaveCount(0)

    // Manage libraries keeps every record, the switched-off one included.
    await page.locator('[data-action="manage-libraries"]').click()
    const panel = page.getByTestId('kind-libraries-panel')
    await expect(panel.getByRole('heading', { name: /^Official models localai/ })).toBeVisible()
    await expect(panel.getByRole('heading', { name: /^Official model library official-models/ })).toBeVisible()
    await expect(panel.getByRole('heading', { name: /^Switched off old/ })).toBeVisible()
  })

  test('a library chip filters the table to that library, and All brings everything back', async ({ page }) => {
    await mockLedger(page)
    await mockOnline(page)
    await page.route('**/api/library/sources', route => (route.request().method() === 'GET'
      ? route.fulfill({ json: { officialLibraries: true, sources: LIBRARY_SOURCES } })
      : route.fallback()))
    await page.goto('/app/models')
    await expect(rows(page).first()).toBeVisible({ timeout: 15_000 })
    const all = await rows(page).count()
    await page.getByRole('button', { name: 'Team models' }).click()
    await expect(page.getByRole('button', { name: 'Team models' })).toHaveAttribute('aria-pressed', 'true')
    await page.getByRole('button', { name: 'All libraries' }).click()
    await expect(rows(page)).toHaveCount(all)
  })
})

test.describe('Explore: online sources on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('a tap on a row opens the inspector, not a model page, and nothing scrolls sideways', async ({ page }) => {
    await openSource(page, 'huggingface', 'tiny')
    await row(page, 'huggingface:acme/Tiny-GGUF').click()
    await expect(page).toHaveURL(/\/app\/models\?/)
    await expect(pane(page).getByTestId('remote-variant')).toHaveCount(3, { timeout: 10_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  test('the "Spills to CPU" badge of a build is not clipped at the right edge', async ({ page }) => {
    // 23 GB of weights do not fit the 24 GB card with a context, so the build
    // carries the spill badge; the others fit.
    const sizes = { Q4_K_M: 23 * GB, Q5_K_M: 5 * GB, Q8_0: 8 * GB }
    await openSource(page, 'huggingface', 'tiny', {
      estimate: () => estimateFor(sizes, 'Q4_K_M'),
      filesFor: (id) => FILES(id),
    })
    await row(page, 'huggingface:acme/Tiny-GGUF').click()
    const badge = pane(page).getByTestId('remote-variant').getByText('Spills to CPU').first()
    await expect(badge).toBeVisible({ timeout: 10_000 })
    const box = await badge.boundingBox()
    const viewport = page.viewportSize().width
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(viewport)
    const paneBox = await pane(page).boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(paneBox.x + paneBox.width)
    // and not cut by an ancestor that hides its overflow
    const clipped = await badge.evaluate(el => {
      const b = el.getBoundingClientRect()
      for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
        const s = getComputedStyle(a)
        if (s.overflowX === 'visible') continue
        const r = a.getBoundingClientRect()
        if (b.right > r.right + 0.5 || b.left < r.left - 0.5) return a.className || a.tagName
      }
      return ''
    })
    expect(clipped).toBe('')
  })
})
