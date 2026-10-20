import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'

// The add-on UI runtime: SDK, loader, scoped styles, registry and the shell
// route. The UI test server runs here with real authentication and a real
// add-on owner, and installs the fixture packages of tests/addons/fixture/ui
// AFTER the host build was made, from a catalog on loopback. Nothing about an
// add-on is known to the build: its module, style sheet and process are what
// the owner serves. Enabling, disabling and updating are the real lifecycle
// routes, sessions and keys are stored keys, and the server decides every
// asset and call.
//
// The specs change the owner's state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const shots = process.env.ADDONS_SHOTS

async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true })
}

// A browser session of one of the seeded accounts.
async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  return { context, page }
}

// An API client with a seeded key. `cookie` sends the session instead.
async function apiAs(who, { cookie = false, headers = {} } = {}) {
  const user = creds.users[who]
  return playwrightRequest.newContext({
    baseURL: BASE,
    extraHTTPHeaders: cookie ? { Cookie: `session=${user.session}`, ...headers } : { Authorization: `Bearer ${user.token}`, ...headers },
  })
}

async function listApps(api) {
  const response = await api.get('/api/addons/apps')
  expect(response.status()).toBe(200)
  return (await response.json()).apps
}

async function appOf(api, id) {
  return (await listApps(api)).find(a => a.id === id)
}

async function lifecycle(api, id, verb) {
  const app = await appOf(api, id)
  const response = await api.post(`/api/addons/apps/${id}/${verb}`, { data: { operationId: randomUUID(), generation: app.generation } })
  expect(response.status(), `${verb} ${id}`).toBe(200)
}

async function upgrade(api, id, version) {
  const app = await appOf(api, id)
  const response = await api.post('/api/addons/dispatch', {
    data: { operationId: randomUUID(), addonId: id, generation: app.generation, action: 'upgrade', arguments: { sourceId: 'fixture', version } },
  })
  expect(response.status(), await response.text()).toBe(200)
  await expect.poll(async () => (await appOf(api, id)).generation, { timeout: 20_000 }).toBeGreaterThan(app.generation)
}

// The host's own bundles live under /assets/ too; add-on code is under /api/addons.
const isAddonAsset = (url) => /\/api\/addons\/[^/]+\/assets\//.test(url)
const events = (page) => page.evaluate(() => (window.__uiFixture ? window.__uiFixture.events : []))
const surface = (page, id = 'uifixture') => page.locator(`[data-testid="addon-surface"][data-addon="${id}"]`)

test.describe.configure({ mode: 'serial' })

let admin
test.beforeAll(async () => { admin = await apiAs('admin') })
test.afterAll(async () => { await admin.dispose() })

test.describe('Add-on UI runtime', () => {
  test('esm: a fixture add-on installed after the host build loads through real authorization and draws in its own root', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    const requested = []
    const answers = []
    page.on('request', r => requested.push(r.url()))
    page.on('response', r => { if (isAddonAsset(r.url())) answers.push({ url: r.url(), status: r.status(), cache: r.headers()['cache-control'], type: r.headers()['content-type'], nosniff: r.headers()['x-content-type-options'] }) })
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    await expect(page.getByTestId('fx-title')).toHaveText('Hello fixture')
    await expect(page.getByTestId('fx-where')).toHaveText('page=home path=')
    await expect(page.locator('h1.page-title')).toHaveText('UI fixture')
    // Every file came from the owner through the asset route, under one digest.
    const js = answers.find(a => a.url.endsWith('/ui.js'))
    const css = answers.find(a => a.url.endsWith('/ui/style.css'))
    expect(js, 'the module was requested').toBeTruthy()
    expect(css, 'the style sheet was requested').toBeTruthy()
    for (const a of [js, css]) {
      expect(a.status).toBe(200)
      expect(a.cache).toBe('private, max-age=31536000, immutable')
      expect(a.nosniff).toBe('nosniff')
      expect(a.url).toMatch(/\/api\/addons\/uifixture\/assets\/[0-9a-f]{64}\//)
    }
    expect(js.type).toBe('text/javascript')
    expect(css.type).toBe('text/css')
    expect(new URL(js.url).pathname.split('/')[5]).toBe(new URL(css.url).pathname.split('/')[5])
    // Nothing left the origin: the style sheet's import never ran.
    for (const url of requested) expect(new URL(url).origin, url).toBe(new URL(BASE).origin)
    expect(requested.some(u => u.includes('evil.example'))).toBe(false)
    expect(await events(page)).toContain('create:1')
    // No credential in any asset URL.
    for (const a of answers) expect(a.url).not.toMatch(/token|api_key|session|key=/i)
    await shoot(page, 'esm-wide-dark')
    await context.close()
  })

  test('esm: hooks run on the host React and the add-on keeps its state across renders', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    const errors = []
    page.on('pageerror', e => errors.push(String(e)))
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()) })
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Increment' }).click()
    await page.getByRole('button', { name: 'Increment' }).click()
    await page.getByRole('button', { name: 'Increment' }).click()
    await expect(surface(page).getByText('hooks 3')).toBeVisible()
    await page.getByTestId('fx-draft').fill('typed')
    await expect(page.getByTestId('fx-draft')).toHaveValue('typed')
    // The host did not re-create the add-on tree while it re-rendered.
    expect((await events(page)).filter(e => e === 'home-mounted:1')).toHaveLength(1)
    expect(errors.filter(e => /Invalid hook call|Minified React error #321/.test(e))).toEqual([])
    await context.close()
  })

  test('esm: the add-on style sheet is scoped to its root and leaves the host and other add-ons alone', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    const probe = await page.evaluate(() => {
      const style = (el) => getComputedStyle(el)
      const root = document.querySelector('[data-addon="uifixture"]')
      return {
        sheet: document.querySelector('style[data-addon-style="uifixture"]')?.textContent || '',
        title: style(document.querySelector('[data-testid="fx-title"]')).color,
        hostTitle: style(document.querySelector('h1.page-title')).color,
        hostTitleSize: style(document.querySelector('h1.page-title')).fontSize,
        bodyBackground: style(document.body).backgroundColor,
        token: style(root).getPropertyValue('--fx-token').trim(),
        rootToken: style(document.documentElement).getPropertyValue('--fx-token').trim(),
        hostButtonOutline: style(document.querySelector('.sidebar-nav a, .sidebar-nav button')).outlineStyle,
      }
    })
    expect(probe.title).toBe('rgb(1, 2, 3)')
    expect(probe.hostTitle).not.toBe('rgb(255, 0, 0)')
    expect(probe.hostTitleSize).not.toBe('11px')
    expect(probe.bodyBackground).not.toBe('rgb(255, 0, 255)')
    expect(probe.token).toBe('1')
    expect(probe.rootToken).toBe('')
    expect(probe.sheet).toContain(':where([data-addon="uifixture"])')
    expect(probe.sheet).not.toMatch(/evil\.example|@import/)
    // A second add-on that uses the same class name is styled by its own sheet.
    await page.goto('/app/addons/cssb/home')
    await expect(surface(page, 'cssb')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByTestId('cssb-title')).toHaveCSS('color', 'rgb(0, 0, 255)')
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveCount(0)
    await context.close()
  })

  test('esm: a component that throws is contained by the error boundary and the shell keeps working', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/boom')
    const card = page.getByTestId('addon-error')
    await expect(card).toBeVisible({ timeout: 20_000 })
    await expect(card).toContainText('This add-on had a problem')
    await shoot(page, 'esm-error-boundary')
    // The page header, the sidebar and the other routes are untouched.
    await expect(page.locator('h1.page-title')).toHaveText('UI fixture')
    await expect(page.locator('.sidebar-nav')).toBeVisible()
    await page.goto('/app/addons/uifixture/home')
    await expect(page.getByTestId('fx-title')).toBeVisible({ timeout: 20_000 })
    await context.close()
  })

  test('esm: deep links, the bare add-on address and a proxy path prefix reach the same declared page', async ({ browser }) => {
    const { context: direct, page: deep } = await browserAs(browser, 'member')
    await deep.goto('/app/addons/uifixture/dialog/opened/two?from=deep')
    await expect(deep.getByTestId('fx-where')).toHaveText('page=dialog path=opened/two', { timeout: 20_000 })
    await deep.reload()
    await expect(deep.getByTestId('fx-where')).toHaveText('page=dialog path=opened/two', { timeout: 20_000 })
    // The address of the add-on alone is its main page.
    await deep.goto('/app/addons/uifixture')
    await expect(deep).toHaveURL(/\/app\/addons\/uifixture\/home$/)
    await expect(deep.getByTestId('fx-title')).toBeVisible({ timeout: 20_000 })
    // A page the add-on did not declare is a missing page, never its code.
    await deep.goto('/app/addons/uifixture/nope')
    await expect(deep.getByTestId('addon-missing-page')).toBeVisible({ timeout: 20_000 })
    await direct.close()

    // Behind a reverse proxy that mounts the app under /llm.
    const { context, page } = await browserAs(browser, 'member', { extraHTTPHeaders: { 'X-Forwarded-Prefix': '/llm' } })
    const assets = []
    page.on('request', r => { if (isAddonAsset(r.url())) assets.push(new URL(r.url()).pathname) })
    await page.goto('/llm/app/addons/uifixture/dialog/opened')
    await expect(page.getByTestId('fx-where')).toHaveText('page=dialog path=opened', { timeout: 20_000 })
    expect(assets.length).toBeGreaterThanOrEqual(2)
    for (const p of assets) expect(p).toMatch(/^\/llm\/api\/addons\/uifixture\/assets\/[0-9a-f]{64}\//)
    await page.getByRole('button', { name: 'Open dialog' }).click()
    await expect(page.getByRole('dialog', { name: 'Fixture dialog' })).toBeVisible()
    await context.close()
  })

  test('esm: a host that does not provide the add-on SDK range never imports its code and says so', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    // The server of this build lists an add-on built for a newer SDK, as a
    // stale cached host bundle would meet after a server upgrade.
    await page.route('**/api/addons/apps', async (route) => {
      // route.fetch is sent from outside the page, so it carries no fetch
      // metadata of its own: say what the browser would have said.
      const response = await route.fetch({ headers: { ...route.request().headers(), 'sec-fetch-site': 'same-origin' } })
      const body = await response.json()
      for (const a of body.apps) if (a.id === 'uifixture') a.ui.sdk = { min: 3, max: 3 }
      await route.fulfill({ response, json: body })
    })
    const assets = []
    page.on('request', r => { if (isAddonAsset(r.url())) assets.push(r.url()) })
    await page.goto('/app/addons/uifixture/home')
    const state = page.getByTestId('addon-unavailable')
    await expect(state).toBeVisible({ timeout: 20_000 })
    await expect(state).toHaveAttribute('data-reason', 'incompatible_sdk')
    await expect(state).toContainText('another version of the LocalAI interface')
    expect(assets).toEqual([])
    expect(await events(page)).toEqual([])
    await context.close()
  })

  test('esm: a module that imports React by a bare specifier fails to load and no second React is resolved', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/bare/home')
    const state = page.getByTestId('addon-unavailable')
    await expect(state).toBeVisible({ timeout: 20_000 })
    await expect(state).toHaveAttribute('data-reason', 'import_failed')
    await expect(page.locator('style[data-addon-style="bare"]')).toHaveCount(0)
    await expect(state.getByRole('link', { name: 'Back to add-ons' })).toBeVisible()
    await context.close()
  })

  test('esm: the server decides asset access: undeclared files, traversal, other add-ons, credentials in URLs and foreign sessions are denied', async () => {
    const apps = await listApps(admin)
    const ui = apps.find(a => a.id === 'uifixture').ui
    const digest = new URL(ui.entry, BASE).pathname.split('/')[5]
    const base = `/api/addons/uifixture/assets/${digest}/`
    const member = await apiAs('member')
    const outsider = await apiAs('outsider')
    const anonymous = await playwrightRequest.newContext({ baseURL: BASE })
    const status = async (api, path) => (await api.get(path, { maxRedirects: 0 })).status()
    expect(await status(member, ui.entry)).toBe(200)
    expect(await status(member, ui.css[0])).toBe(200)
    for (const p of ['manifest.json', 'bin/fixture', '..%2Fmanifest.json', 'ui%2F..%2F..%2Fmanifest.json', 'UI.JS']) {
      expect(await status(member, base + p), p).toBe(403)
    }
    // A directory is never listed. The app strips a trailing slash before it
    // routes, so these two are an unknown address rather than the 403.
    for (const p of ['ui/', '']) {
      expect([403, 404], p).toContain(await status(member, base + p))
    }
    expect(await status(member, `/api/addons/cssb/assets/${digest}/ui.js`), 'another add-on, this digest').toBe(403)
    expect(await status(member, `/api/addons/uifixture/assets/${'0'.repeat(64)}/ui.js`), 'unknown digest').toBe(403)
    expect(await status(outsider, ui.entry), 'no addons feature').toBe(403)
    expect(await status(anonymous, ui.entry), 'anonymous').toBe(401)
    const token = creds.users.member.token
    expect(await status(anonymous, `${ui.entry}?token=${token}`), 'a key in the URL').toBe(401)
    expect(await status(anonymous, `${ui.entry}?api_key=${token}`), 'a key in the URL').toBe(401)
    // A browser session is accepted from the page itself and from nowhere else.
    const direct = await apiAs('member', { cookie: true })
    expect(await status(direct, ui.entry), 'a cookie without fetch metadata').toBe(403)
    const sameOrigin = await apiAs('member', { cookie: true, headers: { 'Sec-Fetch-Site': 'same-origin' } })
    expect(await status(sameOrigin, ui.entry)).toBe(200)
    const crossSite = await apiAs('member', { cookie: true, headers: { 'Sec-Fetch-Site': 'cross-site' } })
    expect(await status(crossSite, ui.entry)).toBe(403)
    await Promise.all([member, outsider, anonymous, direct, sameOrigin, crossSite].map(c => c.dispose()))
  })

  test('esm: an add-on navigates only to its own declared pages and the host draws the add-on navigation', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Leave' }).click()
    await expect(page.getByTestId('fx-result')).toHaveText('refused:invalid_path')
    await expect(page).toHaveURL(/\/app\/addons\/uifixture\/home$/)
    await page.getByRole('button', { name: 'Go to dialog' }).click()
    await expect(page).toHaveURL(/\/app\/addons\/uifixture\/dialog\/opened\?from=home$/)
    await expect(page.getByTestId('fx-where')).toHaveText('page=dialog path=opened')
    // The Add-ons tab lists the pages that usable add-ons declared in navigation.
    await page.goto('/app/addons')
    const subnav = page.locator('.hub-subnav')
    await expect(subnav.getByRole('link', { name: 'UI fixture: home' })).toHaveAttribute('href', '/app/addons/uifixture/home')
    await expect(subnav.getByRole('link', { name: 'UI fixture: dialog' })).toBeVisible()
    await expect(subnav.getByRole('link', { name: 'CSS B' })).toHaveAttribute('href', '/app/addons/cssb/home')
    await expect(subnav.getByRole('link', { name: /bare/i })).toHaveCount(0)
    await context.close()
  })

  test('esm: dialogs an add-on opens are owned by the host: focus, Escape and the close button', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/dialog')
    const open = page.getByRole('button', { name: 'Open dialog' })
    await expect(open).toBeVisible({ timeout: 20_000 })
    await open.click()
    const dialog = page.getByRole('dialog', { name: 'Fixture dialog' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText('Opened by the add-on.')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(open).toBeFocused()
    await open.click()
    await dialog.getByRole('button', { name: 'Done' }).click()
    await expect(dialog).toHaveCount(0)
    await context.close()
  })

  test('esm: a dialog or panel whose render throws is contained and the host app keeps running', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/dialog')
    for (const [button, title] of [['Open broken dialog', 'Broken dialog'], ['Open broken panel', 'Broken panel']]) {
      await page.getByRole('button', { name: button }).click({ timeout: 20_000 })
      const layer = page.getByRole('dialog', { name: title })
      await expect(layer.getByTestId('addon-error')).toBeVisible()
      // The router error page never replaces the host.
      await expect(page.getByText('Unexpected Application Error')).toHaveCount(0)
      await expect(page.locator('.sidebar-nav')).toBeVisible()
      await expect(page.locator('h1.page-title')).toHaveText('UI fixture')
      await layer.getByRole('button', { name: 'Close' }).last().click()
      await expect(layer).toHaveCount(0)
    }
    await context.close()
  })

  test('esm: unsaved edits are guarded when leaving the page', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    await page.getByTestId('fx-draft').fill('half a thought')
    await page.getByRole('link', { name: 'Chat', exact: true }).first().click()
    const guard = page.getByRole('alertdialog')
    await expect(guard).toContainText('Leave this page?')
    await guard.getByRole('button', { name: 'Stay' }).click()
    await expect(page).toHaveURL(/\/app\/addons\/uifixture\/home$/)
    await expect(page.getByTestId('fx-draft')).toHaveValue('half a thought')
    // Saving clears the guard: leaving is then free.
    await page.getByRole('button', { name: 'Save draft' }).click()
    await page.getByRole('link', { name: 'Chat', exact: true }).first().click()
    await expect(page).toHaveURL(/\/app\/chat/)
    await context.close()
  })

  test('esm: the page keeps inside the viewport at 400 px and follows the light and dark themes', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member', { viewport: { width: 400, height: 800 } })
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await shoot(page, 'esm-narrow-dark')
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await shoot(page, 'esm-narrow-light')
    await page.setViewportSize({ width: 1280, height: 800 })
    await shoot(page, 'esm-wide-light')
    await context.close()
  })

  test('esm: a member without the add-ons feature gets no add-on page and no code', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'outsider')
    const assets = []
    page.on('request', r => { if (isAddonAsset(r.url())) assets.push(r.url()) })
    await page.goto('/app/addons/uifixture/home')
    await page.waitForLoadState('networkidle')
    await expect(page.locator('[data-testid="addon-surface"]')).toHaveCount(0)
    expect(assets).toEqual([])
    expect(await events(page)).toEqual([])
    await context.close()
  })

  test('esm: disabling an add-on unmounts it, aborts its signal, drops its styles and denies its code at once', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    const entry = (await appOf(admin, 'uifixture')).ui.entry
    const member = await apiAs('member')
    expect((await member.get(entry)).status()).toBe(200)
    await lifecycle(admin, 'uifixture', 'stop')
    // The code is denied by the server as soon as the owner decided.
    expect((await member.get(entry)).status()).toBe(403)
    const state = page.getByTestId('addon-unavailable')
    await expect(state).toBeVisible({ timeout: 25_000 })
    // A reader no longer sees a stopped app at all: the owner hides it.
    await expect(state).toHaveAttribute('data-reason', 'removed')
    await expect(surface(page)).toHaveCount(0)
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveCount(0)
    const log = await events(page)
    expect(log).toContain('abort:1')
    expect(log).toContain('dispose:1')
    expect(log.indexOf('abort:1')).toBeLessThan(log.indexOf('dispose:1'))
    await shoot(page, 'esm-unavailable')
    // Starting it again does not bring the page back by itself: Reload does.
    await lifecycle(admin, 'uifixture', 'start')
    await expect.poll(async () => (await appOf(admin, 'uifixture')).state, { timeout: 30_000 }).toBe('running')
    await page.getByTestId('addon-reload').click()
    await expect(page.getByTestId('fx-title')).toBeVisible({ timeout: 25_000 })
    expect((await events(page)).filter(e => e === 'create:1')).toHaveLength(2)
    await member.dispose()
    await context.close()
  })

  test('esm: a withdrawn add-on with unsaved edits stays on screen until the user decides, and never saves', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(surface(page)).toBeVisible({ timeout: 20_000 })
    await page.getByTestId('fx-draft').fill('my unsaved notes')
    const calls = []
    page.on('request', r => { if (r.url().endsWith('/api/addons/dispatch')) calls.push(r.postData()) })
    await lifecycle(admin, 'uifixture', 'stop')
    const stale = page.getByTestId('addon-stale')
    await expect(stale).toBeVisible({ timeout: 25_000 })
    await expect(stale).toContainText('Your edits are still on screen')
    await expect(page.getByTestId('fx-draft')).toHaveValue('my unsaved notes')
    const log = await events(page)
    expect(log).toContain('abort:1')
    expect(log.filter(e => e === 'dispose:1' || e === 'home-unmounted:1')).toEqual([])
    // The aborted SDK refuses: nothing is sent for the old generation.
    await page.getByRole('button', { name: 'Read' }).click()
    await expect(page.getByTestId('fx-result')).toHaveText(/^error:AddonSdkError/)
    expect(calls).toEqual([])
    // Reload asks first, and Keep editing keeps everything.
    await page.getByTestId('addon-reload').click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('Discard unsaved changes?')
    await confirm.getByRole('button', { name: 'Keep editing' }).click()
    await expect(page.getByTestId('fx-draft')).toHaveValue('my unsaved notes')
    await shoot(page, 'esm-stale-edits')
    await lifecycle(admin, 'uifixture', 'start')
    await expect.poll(async () => (await appOf(admin, 'uifixture')).state, { timeout: 30_000 }).toBe('running')
    await page.getByTestId('addon-reload').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Discard and reload' }).click()
    await expect(page.getByTestId('fx-title')).toBeVisible({ timeout: 25_000 })
    await expect(page.getByTestId('fx-draft')).toHaveValue('')
    await context.close()
  })

  test('esm: an update withdraws the old generation, denies its code and loads the new one only on an explicit reload', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await page.goto('/app/addons/uifixture/home')
    await expect(page.getByTestId('fx-generation')).toHaveText('generation 1 version 1', { timeout: 20_000 })
    const before = await appOf(admin, 'uifixture')
    await upgrade(admin, 'uifixture', '2.0.0')
    const member = await apiAs('member')
    expect((await member.get(before.ui.entry)).status(), 'the old package is withdrawn').toBe(403)
    const after = await appOf(admin, 'uifixture')
    expect(after.generation).toBe(before.generation + 1)
    expect(after.ui.entry).not.toBe(before.ui.entry)
    expect((await member.get(after.ui.entry)).status()).toBe(200)
    const state = page.getByTestId('addon-unavailable')
    await expect(state).toBeVisible({ timeout: 30_000 })
    await expect(state).toHaveAttribute('data-reason', 'updated')
    const log = await events(page)
    expect(log).toContain('abort:1')
    expect(log).toContain('dispose:1')
    // A call carrying the old generation is denied by the server, even from a
    // client that kept the old code in memory.
    const stale = await member.post('/api/addons/dispatch', { data: { operationId: randomUUID(), addonId: 'uifixture', generation: before.generation, action: 'read', arguments: {} } })
    expect(stale.status()).toBe(403)
    await page.getByTestId('addon-reload').click()
    await expect(page.getByTestId('fx-generation')).toHaveText(`generation ${after.generation} version 2`, { timeout: 25_000 })
    expect(await events(page)).toContain(`create-v2:${after.generation}`)
    // The scoped sheet of the new package is the only one in the document.
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveCount(1)
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveAttribute('data-generation', String(after.generation))
    await member.dispose()
    await context.close()
  })
})
