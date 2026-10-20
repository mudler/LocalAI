import { test, expect } from '@playwright/test'
import fs from 'node:fs'

// The add-ons foundation, seen from the browser through real servers. The
// runner (scripts/addons/foundation.sh) starts three UI test servers: the
// add-on owner and two frontends that hold no add-on state and forward to the
// owner over the pinned mTLS carrier. They share one account store, so a
// session is a stored key that every server decides about. The UI package was
// installed from a catalog after the host build and enabled through a
// frontend. Nothing here mocks a route: every refusal below is the server's.
//
// The specs end sessions and change the add-on's state, so they run in order.

const ready = JSON.parse(fs.readFileSync(process.env.ADDONS_FOUNDATION, 'utf8'))
const URLS = ready.urls
const ID = 'found-ui'

async function browserAs(browser, node, who, extra = {}) {
  const base = URLS[node]
  const context = await browser.newContext({ baseURL: base, ...extra })
  await context.addCookies([{ name: 'session', value: ready.users[who].session, url: base }])
  const page = await context.newPage()
  return { context, page, base }
}

// A same-origin fetch from the page, so the server sees the metadata a real
// browser sends. It bypasses the HTTP cache: add-on assets are cacheable for a
// year by design, and what these specs ask is what the server answers.
const call = (page, method, path, body) => page.evaluate(async ({ method, path, body }) => {
  const response = await fetch(path, { method, cache: 'no-store', headers: { 'content-type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: response.status, text: await response.text() }
}, { method, path, body })

const isAddonAsset = (url) => /\/api\/addons\/[^/]+\/assets\//.test(url)
// The drawer draws a tile for the add-on; its start and stop controls are in
// the card of the detail sheet (/app/addons?detail=ID).
const card = (page) => page.locator(`[data-testid="addon-tile"][data-app="${ID}"]`)
const sheetCard = (page) => page.getByTestId('addon-detail').locator(`[data-app="${ID}"]`)

test.describe.configure({ mode: 'serial' })

test.describe('Add-ons foundation through real servers', () => {
  test('foundation: a member opens the add-on through each non-owner frontend and its module comes from the owner by digest', async ({ browser }) => {
    for (const node of ['front-a', 'front-b']) {
      const { context, page, base } = await browserAs(browser, node, 'member')
      const answers = []
      page.on('response', r => { if (isAddonAsset(r.url())) answers.push({ url: r.url(), status: r.status() }) })
      await page.goto('/app/addons')
      await expect(card(page)).toBeVisible({ timeout: 30_000 })
      await card(page).getByRole('link').click()
      await expect(page).toHaveURL(/\/app\/addons\/found-ui\/home$/)
      await expect(page.getByTestId('fnd-title')).toHaveText('Foundation fixture', { timeout: 30_000 })
      expect(answers.length).toBeGreaterThanOrEqual(1)
      for (const a of answers) {
        expect(a.status).toBe(200)
        expect(new URL(a.url).origin).toBe(new URL(base).origin)
        expect(new URL(a.url).pathname).toMatch(/^\/api\/addons\/found-ui\/assets\/[0-9a-f]{64}\//)
      }
      await context.close()
    }
  })

  test('foundation: the owner and both frontends list the same add-on to the same account', async ({ browser }) => {
    for (const node of ['owner', 'front-a', 'front-b']) {
      const { context, page } = await browserAs(browser, node, 'member')
      await page.goto('/app/addons')
      await expect(card(page)).toBeVisible({ timeout: 30_000 })
      await expect(card(page).locator('.ad-tile__state')).toHaveText('Running')
      await context.close()
    }
  })

  test('foundation: a member sees the Apps list only and the server refuses every management request', async ({ browser }) => {
    for (const node of ['front-a', 'front-b', 'owner']) {
      const { context, page } = await browserAs(browser, node, 'member')
      await page.goto('/app/addons')
      await expect(card(page)).toBeVisible({ timeout: 30_000 })
      await expect(page.getByRole('tablist')).toHaveCount(0)
      await expect(page.getByRole('button', { name: /^(Start|Stop)$/ })).toHaveCount(0)
      await expect(page.getByRole('link', { name: 'Manage' })).toHaveCount(0)
      // The detail sheet hides them as well.
      await page.goto(`/app/addons?detail=${ID}`)
      await expect(sheetCard(page)).toBeVisible({ timeout: 30_000 })
      await expect(page.getByRole('button', { name: /^(Start|Stop)$/ })).toHaveCount(0)
      await expect(page.getByRole('link', { name: 'Manage' })).toHaveCount(0)
      // The page hides the controls; the server is what refuses.
      for (const action of ['install', 'upgrade', 'enable', 'disable', 'uninstall', 'catalog-sources', 'addons-installed']) {
        const r = await call(page, 'POST', '/api/addons/dispatch', { operationId: `m-${node}-${action}`, addonId: ID, generation: 1, action, arguments: {} })
        expect(r.status, `${node} ${action}`).toBe(403)
      }
      for (const verb of ['start', 'stop']) {
        const r = await call(page, 'POST', `/api/addons/apps/${ID}/${verb}`, { operationId: `m-${node}-${verb}`, generation: 1 })
        expect(r.status, `${node} ${verb}`).toBe(403)
      }
      await context.close()
    }
  })

  test('foundation: an account without the add-ons feature gets no apps and no code on any node', async ({ browser }) => {
    for (const node of ['front-a', 'front-b', 'owner']) {
      const { context, page } = await browserAs(browser, node, 'outsider')
      await page.goto('/app')
      const apps = await call(page, 'GET', '/api/addons/apps')
      expect(apps.status, node).toBe(403)
      const asset = await call(page, 'GET', `/api/addons/${ID}/assets/${'0'.repeat(64)}/ui.js`)
      expect(asset.status, node).toBe(403)
      await page.goto(`/app/addons/${ID}/home`)
      await expect(page.getByTestId('fnd-title')).toHaveCount(0)
      await context.close()
    }
  })

  test('foundation: an administrator manages through a frontend and the change is the owner\'s', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-b', 'admin')
    await page.goto('/app/addons')
    await expect(card(page)).toBeVisible({ timeout: 30_000 })
    await expect(page.getByRole('tablist')).toBeVisible()
    await page.goto(`/app/addons?detail=${ID}`)
    await sheetCard(page).getByRole('button', { name: 'Stop' }).click()
    await expect(sheetCard(page).getByRole('img', { name: 'State: Stopped' })).toBeVisible({ timeout: 30_000 })
    // The other frontend and the owner see the same stopped add-on: the
    // administrator in the list with its state, a member not at all, because
    // the owner lists a disabled add-on to administrators only.
    for (const node of ['front-a', 'owner']) {
      const other = await browserAs(browser, node, 'admin')
      await other.page.goto(`/app/addons?detail=${ID}`)
      await expect(sheetCard(other.page).getByRole('img', { name: 'State: Stopped' })).toBeVisible({ timeout: 30_000 })
      await other.context.close()
      const member = await browserAs(browser, node, 'member')
      await member.page.goto('/app/addons')
      await expect(member.page.getByTestId('addons')).toBeVisible({ timeout: 30_000 })
      await expect(card(member.page)).toHaveCount(0)
      await member.context.close()
    }
    await sheetCard(page).getByRole('button', { name: 'Start' }).click()
    await expect(sheetCard(page).getByRole('img', { name: 'State: Running' })).toBeVisible({ timeout: 30_000 })
    await context.close()
  })

  test('foundation: deep links of the add-on page work behind a proxy path prefix on a non-owner frontend', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'front-a', 'member', { extraHTTPHeaders: { 'X-Forwarded-Prefix': '/llm' } })
    const assets = []
    page.on('request', r => { if (isAddonAsset(r.url())) assets.push(new URL(r.url()).pathname) })
    await page.goto('/llm/app/addons/found-ui/home')
    await expect(page.getByTestId('fnd-title')).toBeVisible({ timeout: 30_000 })
    expect(assets.length).toBeGreaterThanOrEqual(1)
    for (const p of assets) expect(p).toMatch(/^\/llm\/api\/addons\/found-ui\/assets\/[0-9a-f]{64}\//)
    // A reload of the deep link keeps the prefix and the page.
    await page.reload()
    await expect(page.getByTestId('fnd-title')).toBeVisible({ timeout: 30_000 })
    await expect(page).toHaveURL(/\/llm\/app\/addons\/found-ui\/home$/)
    await context.close()
  })

  test('foundation: a session ended through one frontend is refused for add-on assets by the other servers', async ({ browser }) => {
    const a = await browserAs(browser, 'front-a', 'friend')
    await a.page.goto(`/app/addons/${ID}/home`)
    await expect(a.page.getByTestId('fnd-title')).toBeVisible({ timeout: 30_000 })
    const entry = (await call(a.page, 'GET', '/api/addons/apps').then(r => JSON.parse(r.text))).apps.find(x => x.id === ID).ui.entry
    expect((await call(a.page, 'GET', entry)).status).toBe(200)

    const b = await browserAs(browser, 'front-b', 'friend')
    await b.page.goto('/app')
    expect((await call(b.page, 'GET', entry)).status).toBe(200)

    const out = await call(a.page, 'POST', '/api/auth/logout')
    expect(out.status).toBe(200)
    for (const [label, page] of [['front-a', a.page], ['front-b', b.page]]) {
      const r = await call(page, 'GET', entry)
      expect(r.status, label).toBe(401)
    }
    await b.page.goto(`/app/addons/${ID}/home`)
    await expect(b.page.getByTestId('fnd-title')).toHaveCount(0)
    await a.context.close()
    await b.context.close()
  })
})
