import { test, expect } from './coverage-fixtures.js'
import { mockLedger } from './ledger-fixtures.js'

// The sources of the online model search. Hugging Face is built in; another
// source comes from an add-on and declares typed filters that the page draws.
// The server is mocked. With SHOTS_DIR set, the specs also save a screenshot of
// each state, for a visual check.

const HF_SOURCE = { id: 'huggingface', title: 'Hugging Face', kind: 'builtin', lists_latest: true, running: true }
const PEER_SOURCE = {
  id: 'peer', title: 'Peer Index', kind: 'addon', running: true,
  filters: [
    { id: 'kind', type: 'enum', label: 'Format', values: ['gguf', 'any'], default: 'gguf' },
    { id: 'fresh', type: 'bool', label: 'Recent only' },
    { id: 'seeds', type: 'range', label: 'Minimum seeds', min: 0, max: 50, step: 1, unit: 'seeds' },
  ],
  i18n: { it: { title: 'Indice Peer', filters: { kind: { label: 'Formato' } } } },
}

const HF = (id) => ({
  provider: 'huggingface', id, title: id, uri: `huggingface://${id}`, url: `https://huggingface.co/${id}`,
  downloads: 3_400_000, likes: 120, license: 'apache-2.0', tags: ['gguf', 'chat'], pipeline_tag: 'text-generation', backend: 'llama-cpp', has_variants: true,
})
const PEER = (id, seeders) => ({
  provider: 'peer', id, title: id, uri: `pirateface://${id}`, url: `https://huggingface.co/${id}`,
  size_bytes: 1_500_000_000, seeders, has_variants: true,
})

async function mockSources(page, { sources, enabled, missing = [], peerError = '' }) {
  const seen = { search: [], put: [] }
  let current = enabled
  await page.route(/\/api\/models\/search\/sources$/, route => {
    if (route.request().method() === 'PUT') {
      current = route.request().postDataJSON().enabled
      seen.put.push(current)
    }
    return route.fulfill({ json: { sources, enabled: current, missing } })
  })
  await page.route(/\/api\/models\/search\?/, route => {
    const q = new URL(route.request().url()).searchParams
    seen.search.push(q)
    const provider = q.get('providers')
    if (provider === 'peer') {
      if (peerError) return route.fulfill({ json: { results: [], providers: [{ name: 'peer', title: 'Peer Index', ok: false, count: 0, error: peerError }] } })
      return route.fulfill({ json: { results: [PEER('acme/Peer-7B', 6), PEER('acme/Peer-13B', 2)], providers: [{ name: 'peer', title: 'Peer Index', ok: true, count: 2, dropped: 1 }] } })
    }
    return route.fulfill({ json: { results: [HF('acme/Tiny-GGUF'), HF('acme/Big-GGUF')], providers: [{ name: 'huggingface', title: 'Hugging Face', ok: true, count: 2 }] } })
  })
  return seen
}

const shot = async (page, name) => {
  if (process.env.SHOTS_DIR) await page.waitForTimeout(500)
  if (process.env.SHOTS_DIR) await page.screenshot({ path: `${process.env.SHOTS_DIR}/${name}.png`, fullPage: false })
}
const chip = (page, id) => page.getByTestId(`source-chip-${id}`)

test.describe('model search sources', () => {
  test('with only Hugging Face on, the panel has its chip and no source controls', async ({ page }) => {
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE], enabled: ['huggingface'] })
    await page.goto('/app/models')
    await expect(chip(page, 'huggingface')).toContainText('Hugging Face')
    await expect(page.getByTestId('model-sources-open')).toHaveCount(0)
    await expect(page.getByTestId('source-hint')).toHaveCount(0)
    await chip(page, 'huggingface').click()
    await page.getByTestId('models-search').fill('qwen')
    await expect(page.locator('[data-entity="huggingface:acme/Tiny-GGUF"]')).toBeVisible()
    await expect(page.getByTestId('source-filters')).toHaveCount(0)
    await shot(page, 'hf-only')
  })

  test('two sources on: the chip of the add-on source draws its filters and sends them', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await expect(chip(page, 'peer')).toContainText('Peer Index')
    await chip(page, 'peer').click()
    await page.getByTestId('models-search').fill('peer')
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"]')).toBeVisible()
    for (const id of ['kind', 'fresh', 'seeds']) await expect(page.getByTestId(`source-filter-peer-${id}`)).toBeVisible()
    await expect(page.getByTestId('remote-dropped')).toContainText('1 result skipped')
    await shot(page, 'two-sources-filter')

    seen.search.length = 0
    await page.getByTestId('source-filter-peer-kind-any').click()
    await expect.poll(() => seen.search.at(-1)?.get('filters.peer.kind')).toBe('any')
    await page.getByTestId('source-filter-peer-fresh').getByRole('switch').click()
    await expect.poll(() => seen.search.at(-1)?.get('filters.peer.fresh')).toBe('true')
    // A filter left alone is not sent: the source applies its own default.
    expect(seen.search.at(-1).has('filters.peer.seeds')).toBe(false)
  })

  test('a source that fails shows its own error and the other source still answers', async ({ page }) => {
    // Tall enough to see the Hugging Face rows above the failed section.
    await page.setViewportSize({ width: 1280, height: 1500 })
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'], peerError: 'upstream timeout' })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('qwen')
    await expect(page.locator('[data-entity="huggingface:acme/Tiny-GGUF"]')).toBeVisible()
    const section = page.getByTestId('discover-section-error')
    await expect(section).toContainText('Peer Index did not answer')
    await expect(section).toContainText('upstream timeout')
    await expect(section).toBeInViewport()
    await shot(page, 'failed-source')
  })

  test('no gallery match but online results: one results view, no empty state', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1500 })
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('zzz-no-such-gallery-model')
    await expect(page.locator('[data-entity="huggingface:acme/Tiny-GGUF"]')).toBeVisible()
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"]')).toBeVisible()
    await expect(page.getByTestId('gallery-empty')).toHaveCount(0)
    // One list: no per-source heading rows, each row names its origin.
    await expect(page.getByTestId('discover-section-heading')).toHaveCount(0)
    await expect(page.getByTestId('discover-rail')).toHaveCount(1)
    await expect(page.locator('[data-entity="huggingface:acme/Tiny-GGUF"] [data-testid="row-origin"]')).toHaveText('Hugging Face')
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"] [data-testid="row-origin"]')).toHaveText('Peer Index')
    // Arrow keys walk the mixed list from one source into the next.
    await page.locator('[data-entity="huggingface:acme/Big-GGUF"]').focus()
    await page.keyboard.press('ArrowDown')
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"]')).toBeFocused()
    await shot(page, 'no-gallery-match-online-results')
  })

  test('gallery matches come first in the same list, marked as gallery rows', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1500 })
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('qwen')
    await expect(page.locator('[data-entity="huggingface:acme/Tiny-GGUF"]')).toBeVisible()
    await expect(page.getByTestId('discover-section-heading')).toHaveCount(0)
    const origins = await page.getByTestId('row-origin').evaluateAll(els => els.map(e => e.getAttribute('data-origin')))
    expect(origins).toContain('gallery')
    expect(origins.indexOf('online')).toBeGreaterThan(origins.lastIndexOf('gallery'))
    await shot(page, 'gallery-and-online-mixed')
  })

  test('online-only results: the All chip and the counter count the rows of the list', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1500 })
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('zzz-no-such-gallery-model')
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"]')).toBeVisible()
    // Two rows from each online source, none from the gallery.
    await expect(page.getByTestId('row-origin')).toHaveCount(4)
    await expect(page.getByTestId('facet-all').locator('.ledger-facet__count')).toHaveText('4')
    await expect(page.locator('.models-tabsrow__count')).toHaveText('4 of 4')
    await shot(page, 'counters-online-only')
  })

  test('mixed results: the counters add the online rows to the gallery matches', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1500 })
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('qwen')
    await expect(page.locator('[data-entity="peer:acme/Peer-7B"]')).toBeVisible()
    const rows = await page.getByTestId('row-origin').count()
    const gallery = await page.locator('[data-testid="row-origin"][data-origin="gallery"]').count()
    expect(gallery).toBeGreaterThan(0)
    // Every gallery match is on the first page here, so the list length is the count.
    await expect(page.getByTestId('facet-all').locator('.ledger-facet__count')).toHaveText(String(rows))
    await expect(page.locator('.models-tabsrow__count')).toHaveText(`${rows} of ${rows}`)
  })

  test('the counters do not count online rows that are still loading', async ({ page }) => {
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE], enabled: ['huggingface'] })
    await page.route(/\/api\/models\/search\?/, async route => {
      await new Promise(r => setTimeout(r, 1500))
      return route.fulfill({ json: { results: [HF('acme/Tiny-GGUF')], providers: [{ name: 'huggingface', title: 'Hugging Face', ok: true, count: 1 }] } })
    })
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('zzz-no-such-gallery-model')
    await expect(page.locator('.models-tabsrow__count')).toHaveText('0 of 0')
    await expect(page.locator('.models-tabsrow__count')).toHaveText('1 of 1')
    await expect(page.getByTestId('facet-all').locator('.ledger-facet__count')).toHaveText('1')
  })

  test('no match anywhere: the empty state shows and the table is gone', async ({ page }) => {
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE], enabled: ['huggingface'] })
    await page.route(/\/api\/models\/search\?/, route => route.fulfill({ json: { results: [], providers: [{ name: 'huggingface', title: 'Hugging Face', ok: true, count: 0 }] } }))
    await page.goto('/app/models')
    await page.getByTestId('models-search').fill('zzz-no-such-gallery-model')
    await expect(page.getByTestId('gallery-empty')).toBeVisible()
    await shot(page, 'no-match-anywhere')
  })

  test('an installed add-on source is on with no action, and the panel shows it on', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await expect(chip(page, 'huggingface')).toBeVisible()
    await expect(chip(page, 'peer')).toBeVisible()
    await expect(page.getByTestId('source-hint')).toHaveCount(0)
    await page.getByTestId('model-sources-open').click()
    await expect(page.getByTestId('source-switch-huggingface')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('source-switch-peer')).toHaveAttribute('aria-checked', 'true')
    expect(seen.put).toEqual([])
    await shot(page, 'sources-default-on')
  })

  test('the sources panel turns a source off and on again', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'peer'] })
    await page.goto('/app/models')
    await page.getByTestId('model-sources-open').click()
    const peerSwitch = page.getByTestId('source-switch-peer')
    // Off: the chip and the filters of a source that is not on are gone.
    await peerSwitch.click()
    await expect.poll(() => seen.put.length).toBe(1)
    expect(seen.put[0]).toEqual(['huggingface'])
    await expect(peerSwitch).toHaveAttribute('aria-checked', 'false')
    await expect(chip(page, 'peer')).toHaveCount(0)
    await expect(page.getByTestId('source-filter-peer-kind')).toHaveCount(0)
    await shot(page, 'sources-panel-off')

    // On again: the chip and the filters of the source are back.
    await peerSwitch.click()
    await expect.poll(() => seen.put.length).toBe(2)
    expect(seen.put[1]).toEqual(['huggingface', 'peer'])
    await expect(peerSwitch).toHaveAttribute('aria-checked', 'true')
    await expect(chip(page, 'peer')).toBeVisible()
    await expect(page.getByTestId('source-filter-peer-kind')).toBeVisible()
    await shot(page, 'sources-panel')
  })

  test('a server that still sends missing IDs gets the hint and keeps the ID in the list it sends back', async ({ page }) => {
    await mockLedger(page)
    const seen = await mockSources(page, { sources: [HF_SOURCE, PEER_SOURCE], enabled: ['huggingface', 'ghost'], missing: ['ghost'] })
    await page.goto('/app/models')
    await expect(page.getByTestId('source-hint')).toContainText('ghost')
    await page.getByTestId('model-sources-open').click()
    await expect(page.getByTestId('source-missing-ghost')).toBeVisible()
    await page.getByTestId('source-switch-peer').click()
    await expect.poll(() => seen.put.length).toBe(1)
    expect(seen.put[0]).toEqual(['huggingface', 'ghost', 'peer'])
    await expect(page.getByTestId('source-missing-ghost')).toBeVisible()
  })

  test('an empty set says nothing is searched', async ({ page }) => {
    await mockLedger(page)
    await mockSources(page, { sources: [HF_SOURCE], enabled: [] })
    await page.goto('/app/models')
    await expect(page.getByTestId('source-hint')).toContainText('No source is turned on')
    await expect(chip(page, 'huggingface')).toHaveCount(0)
  })
})
