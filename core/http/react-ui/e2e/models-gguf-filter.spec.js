import { test, expect } from '@playwright/test'

// The "GGUF only" switch of the Explore tab. Each source answers by the format
// the page sends, as the host does, so the specs check that the switch asks
// again and that the list follows the answer, not only that a parameter is sent.

const row = (id, provider, gguf) => ({
  id, title: id, provider, uri: `${provider}://${id}`, downloads: 10, tags: gguf ? ['gguf'] : ['safetensors'],
})
const BY_SOURCE = {
  huggingface: [row('acme/Tiny-GGUF', 'huggingface', true), row('acme/Tiny', 'huggingface', false)],
  pirateface: [row('acme/Peer-GGUF', 'pirateface', true), row('acme/Peer', 'pirateface', false)],
}

async function mockSources(page) {
  const seen = []
  await page.route(/\/api\/models\/search\/sources$/, route => route.fulfill({
    json: {
      sources: [
        { id: 'huggingface', title: 'Hugging Face', kind: 'builtin', lists_latest: true, running: true },
        { id: 'pirateface', title: 'Pirate Face', kind: 'addon', running: true, p2p: true },
      ],
      enabled: ['huggingface', 'pirateface'],
      missing: [],
    },
  }))
  await page.route(/\/api\/models\/search\?/, route => {
    const q = new URL(route.request().url()).searchParams
    seen.push(q)
    const provider = q.get('providers')
    const all = BY_SOURCE[provider] || []
    const list = q.get('format') === 'any' ? all : all.filter(r => r.tags.includes('gguf'))
    return route.fulfill({ json: { results: list, providers: [{ name: provider, ok: true, count: list.length }] } })
  })
  return seen
}

// A remote row is keyed provider:id.
const names = (page) => page.locator('[data-testid="discover-rail-item"]').evaluateAll(els => els.map(el => (el.closest('[data-entity]') || el).getAttribute('data-entity') || '').map(k => k.slice(k.indexOf(':') + 1)))

for (const [source, gguf, other] of [['huggingface', 'acme/Tiny-GGUF', 'acme/Tiny'], ['pirateface', 'acme/Peer-GGUF', 'acme/Peer']]) {
  test(`GGUF only filters the ${source} list and widens it when turned off`, async ({ page }) => {
    const seen = await mockSources(page)
    await page.goto('/app/models')
    await page.getByTestId(`source-chip-${source}`).click()
    await page.getByTestId('models-search').fill('tiny')
    await expect.poll(() => names(page)).toEqual([gguf])

    await page.getByTestId('remote-gguf-only').click()
    await expect.poll(() => names(page)).toEqual([gguf, other])
    expect(seen.at(-1).get('format')).toBe('any')

    await page.getByTestId('remote-gguf-only').click()
    await expect.poll(() => names(page)).toEqual([gguf])
    expect(seen.at(-1).get('format')).toBe('gguf')
  })
}

test('GGUF only under All asks every source again and follows their answers', async ({ page }) => {
  const seen = await mockSources(page)
  await page.goto('/app/models')
  await page.getByTestId('models-search').fill('tiny')
  await expect.poll(() => names(page)).toEqual(expect.arrayContaining(['acme/Tiny-GGUF', 'acme/Peer-GGUF']))
  expect(await names(page)).not.toContain('acme/Tiny')
  seen.length = 0

  await page.getByTestId('remote-gguf-only').click()
  await expect.poll(() => names(page)).toEqual(expect.arrayContaining(['acme/Tiny', 'acme/Peer']))
  expect(seen.map(q => q.get('providers')).sort()).toEqual(['huggingface', 'pirateface'])
  expect(seen.every(q => q.get('format') === 'any')).toBe(true)
})
