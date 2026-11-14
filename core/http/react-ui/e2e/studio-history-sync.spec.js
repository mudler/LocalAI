import { test, expect } from './coverage-fixtures.js'
import { mockStudio, stubMedia } from './studio-fixtures.js'

// Server-side Studio history (see docs/content/features/studio-history.md).
// These specs drive the real Studio bundle in the browser against a stand-in
// for /api/studio/history: what a result does on the server when it is made,
// deleted or cleared, and what a shared browser shows to whom. The store and
// the routes have their own Go specs; the merge rules have unit tests in
// addons/studio/ui/test/studioHistorySync.test.mjs.

const IMAGE_KEY = 'localai_image_history'
const T0 = new Date('2026-10-30T12:00:00Z').getTime()

// A stand-in for /api/studio/history with the server's contract: one history
// per account, an opaque cacheKey per account, tombstones in the list, 410
// for a write of a deleted id. `who()` names the signed-in account and is read
// on every request, so a spec can switch accounts on one browser. `status`,
// when set, is the answer to every request (403: the feature is revoked).
function fakeStudioServer({ who }) {
  const accounts = new Map()
  const requests = []
  let status = null
  const of = (user) => {
    if (!accounts.has(user)) accounts.set(user, { live: new Map(), tombs: new Map() })
    return accounts.get(user)
  }
  const handler = async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const rest = url.pathname.slice(url.pathname.indexOf('/api/studio/history') + '/api/studio/history'.length)
    const id = rest.startsWith('/') ? decodeURIComponent(rest.slice(1)) : null
    const type = url.searchParams.get('type')
    const method = req.method()
    const body = method === 'PUT' || method === 'POST' ? req.postDataJSON() : null
    const user = who()
    requests.push({ user, method, id, type, body })
    if (status) return route.fulfill({ status, json: { error: 'unavailable' } })
    const { live, tombs } = of(user)
    const tomb = (e) => tombs.set(e.id, { id: e.id, type: e.type, version: (e.version || 0) + 1, deletedAt: T0 })

    if (method === 'GET' && !id) {
      return route.fulfill({ json: { entries: [...live.values()], deleted: [...tombs.values()], cacheKey: `ck-${user}` } })
    }
    if (method === 'POST' && id === 'import') {
      let count = 0
      for (const e of body.entries || []) {
        if (live.has(e.id) || tombs.has(e.id)) continue
        live.set(e.id, { ...e, version: 1 })
        count += 1
      }
      return route.fulfill({ json: { status: 'ok', count, skipped: [] } })
    }
    if (method === 'PUT' && id) {
      if (tombs.has(id)) return route.fulfill({ status: 410, json: { error: 'entry was deleted' } })
      const saved = { ...body, id, version: (live.get(id)?.version || 0) + 1 }
      live.set(id, saved)
      return route.fulfill({ json: saved })
    }
    if (method === 'DELETE' && id) {
      const e = live.get(id) || { id, type }
      live.delete(id)
      tomb(e)
      return route.fulfill({ json: { status: 'ok' } })
    }
    if (method === 'DELETE' && !id) {
      for (const e of [...live.values()]) {
        if (type && e.type !== type) continue
        live.delete(e.id)
        tomb(e)
      }
      return route.fulfill({ json: { status: 'ok' } })
    }
    return route.fulfill({ status: 404, json: { error: 'not found' } })
  }
  return {
    attach: (page) => page.route(/\/api\/studio\/history(\/[^?]*)?(\?.*)?$/, handler),
    setStatus: (value) => { status = value },
    prompts: (user) => [...of(user).live.values()].map(e => e.entry?.prompt).sort(),
    tombstoneCount: (user) => of(user).tombs.size,
    requests: (filter = {}) => requests.filter(r => Object.entries(filter).every(([k, v]) => r[k] === v)),
  }
}

async function openImages(page, server) {
  await mockStudio(page, { capabilityModels: [{ id: 'test-image-model', capabilities: ['FLAG_IMAGE'] }] })
  await stubMedia(page)
  let n = 0
  await page.route('**/v1/images/generations', route => {
    n += 1
    return route.fulfill({ json: { created: Math.floor(Date.now() / 1000), data: [{ url: `/generated-images/img${n}.png` }] } })
  })
  if (server) await server.attach(page)
  await page.goto('/app/studio/images')
  await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })
}

async function generate(page, prompt) {
  const before = await page.getByTestId('media-history-item').count()
  await page.locator('[data-testid="ws-compose"] textarea').first().fill(prompt)
  await page.locator('[data-testid="ws-compose"] button[type="submit"]').click()
  await expect(page.getByTestId('media-history-item')).toHaveCount(before + 1, { timeout: 10_000 })
}

const stored = (page, key) => page.evaluate(k => JSON.parse(localStorage.getItem(k) || 'null'), key)

test.describe('Studio history on the server', () => {
  test('a new result is saved on the server and shown on another browser', async ({ page, browser }) => {
    const server = fakeStudioServer({ who: () => 'alice' })
    await openImages(page, server)
    await generate(page, 'a robot in the rain')

    await expect.poll(() => server.prompts('alice')).toEqual(['a robot in the rain'])
    const put = server.requests({ method: 'PUT' })[0]
    expect(put.body.type).toBe('image')
    expect(put.body.entry.serverVersion).toBeUndefined()
    // The browser keeps the account's copy under its own key, not the plain one.
    await expect.poll(async () => (await stored(page, `${IMAGE_KEY}:ck-alice`))?.length).toBe(1)
    expect(await stored(page, IMAGE_KEY)).toBeNull()

    // Same account, a browser that has never seen this work.
    const other = await browser.newContext()
    const second = await other.newPage()
    await openImages(second, server)
    await expect(second.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(second.getByTestId('media-history-item')).toContainText('a robot in the rain')
    await other.close()
  })

  test('a delete reaches the server and stays deleted after a reload', async ({ page }) => {
    const server = fakeStudioServer({ who: () => 'alice' })
    await openImages(page, server)
    await generate(page, 'keep me')
    await generate(page, 'delete me')
    await expect.poll(() => server.prompts('alice')).toEqual(['delete me', 'keep me'])

    await page.getByTestId('media-history-item').filter({ hasText: 'delete me' }).hover()
    await page.getByTestId('media-history-item').filter({ hasText: 'delete me' }).getByTestId('media-history-delete').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1)

    await expect.poll(() => server.prompts('alice')).toEqual(['keep me'])
    expect(server.requests({ method: 'DELETE' })[0].type).toBe('image')

    await page.waitForTimeout(600) // the debounced cache write
    await page.reload()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText('keep me')
  })

  test('Clear empties this workspace on the server', async ({ page }) => {
    const server = fakeStudioServer({ who: () => 'alice' })
    await openImages(page, server)
    await generate(page, 'first')
    await generate(page, 'second')
    await expect.poll(() => server.prompts('alice')).toHaveLength(2)

    await page.locator('.media-history-clear-btn').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)

    await expect.poll(() => server.prompts('alice')).toEqual([])
    const clear = server.requests({ method: 'DELETE', id: null })
    expect(clear).toHaveLength(1)
    expect(clear[0].type).toBe('image')
    expect(server.tombstoneCount('alice')).toBe(2)
  })

  test('two accounts on one browser each see only their own work', async ({ page }) => {
    let user = 'alice'
    const server = fakeStudioServer({ who: () => user })
    await openImages(page, server)
    await generate(page, "alice's robot")
    await expect.poll(() => server.prompts('alice')).toEqual(["alice's robot"])
    await page.waitForTimeout(600)

    user = 'bob'
    await page.reload()
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })
    await expect.poll(() => server.requests({ user: 'bob', method: 'GET' }).length).toBeGreaterThan(0)
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
    await generate(page, "bob's robot")
    await expect.poll(() => server.prompts('bob')).toEqual(["bob's robot"])
    expect(server.prompts('alice')).toEqual(["alice's robot"])
    await page.waitForTimeout(600)

    user = 'alice'
    await page.reload()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText("alice's robot")
  })

  test('a browser that held an account shows nothing when the server refuses', async ({ page }) => {
    const server = fakeStudioServer({ who: () => 'alice' })
    await openImages(page, server)
    await generate(page, 'private work')
    await expect.poll(() => server.prompts('alice')).toEqual(['private work'])
    await page.waitForTimeout(600)

    // Something left under the plain key must not show either.
    await page.evaluate(k => localStorage.setItem(k, JSON.stringify([{ id: 'old-1', prompt: 'old work', results: [], createdAt: 1 }])), IMAGE_KEY)
    server.setStatus(403)
    await page.reload()
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })
    await expect.poll(() => server.requests({ method: 'GET' }).length).toBeGreaterThan(1)
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
  })

  test('work kept in the browser before goes to the first account only', async ({ page }) => {
    let user = 'alice'
    const server = fakeStudioServer({ who: () => user })
    await page.addInitScript(k => {
      if (!sessionStorage.getItem('seeded')) {
        sessionStorage.setItem('seeded', '1')
        localStorage.setItem(k, JSON.stringify([{ id: 'legacy-1', prompt: 'made before accounts', results: [], createdAt: 1 }]))
      }
    }, IMAGE_KEY)
    await openImages(page, server)

    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText('made before accounts')
    expect(server.requests({ method: 'POST', id: 'import' })).toHaveLength(1)
    expect(server.prompts('alice')).toEqual(['made before accounts'])
    await expect.poll(() => stored(page, IMAGE_KEY)).toBeNull()
    await page.waitForTimeout(600)

    user = 'bob'
    await page.reload()
    await expect.poll(() => server.requests({ user: 'bob', method: 'GET' }).length).toBeGreaterThan(0)
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
    expect(server.requests({ method: 'POST', id: 'import' })).toHaveLength(1)
  })

  // The test server runs without accounts, so this one talks to the real
  // route: it answers the list with enabled false, never a 404 that the
  // browser would log as an error on every load.
  test('without accounts the history stays in the browser, with no error logged', async ({ page }) => {
    const errors = []
    page.on('pageerror', e => errors.push(String(e)))
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()) })
    const calls = []
    page.on('request', r => { if (r.url().includes('/api/studio/history')) calls.push(`${r.method()} ${new URL(r.url()).pathname}`) })
    const list = page.waitForResponse(r => new URL(r.url()).pathname === '/api/studio/history')

    await openImages(page)
    const answer = await list
    expect(answer.status()).toBe(200)
    expect(await answer.json()).toEqual({ enabled: false, entries: [], deleted: [] })
    await generate(page, 'local only')

    await expect.poll(async () => (await stored(page, IMAGE_KEY))?.length).toBe(1)
    expect(calls).toEqual(['GET /api/studio/history'])
    await page.reload()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    expect(errors).toEqual([])
  })
})
