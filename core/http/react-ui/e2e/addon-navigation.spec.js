import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { ADMIN, MEMBER, app, sessionFor, mockAddons } from './addons-fixtures.js'
import { buildAddonBundles, installStudioAddons, packagedApp } from './studio-addon.js'
import { FACE_MODEL, clipFile, faces, match, mockIdentity, people, photoFile } from './identity-fixtures.js'

// Studio and Talk are packaged add-ons, not built-in tiles: the specs install
// the real packages. No page is built in today, so `builtin` is empty unless a
// spec makes one up to check the built-in rules.
async function setup(page, { apps = [], pins = ['studio', 'talk'], addons = true, builtin = [], studio = true, user = MEMBER } = {}) {
  let saved = pins
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, { addons }) }))
  await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: true } }))
  await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin } }))
  // mockAddons installs the Apps routes with the real response shape ({ apps }).
  // The packages are served from the build output, so a page that opens one runs it.
  if (studio) await installStudioAddons(page, { apps: [packagedApp('studio'), packagedApp('talk'), ...apps] })
  else await mockAddons(page, { apps })
  await page.route('**/api/addons/pins', async route => {
    if (route.request().method() === 'PUT') saved = route.request().postDataJSON()
    await route.fulfill({ json: saved })
  })
  return { saved: () => saved }
}

test.describe('Add-on navigation', () => {
  test('the sidebar lists the pinned apps under Apps, in pin order, and no Show all link', async ({ page }) => {
    await setup(page)
    await page.goto('/app')
    const nav = page.getByRole('navigation')
    await expect(nav.getByRole('link', { name: 'Chat' })).toBeVisible()
    // Apps, then the pinned apps in pin order; Apps is the only add-on entry.
    await expect(nav.getByRole('link', { name: 'Apps', exact: true })).toHaveCount(1)
    const pinned = nav.getByTestId('apps-pinned-link')
    await expect(pinned).toHaveCount(2)
    await expect(pinned.nth(0)).toHaveAttribute('data-app', 'studio')
    await expect(pinned.nth(0)).toHaveText('Studio')
    await expect(pinned.nth(1)).toHaveAttribute('data-app', 'talk')
    await expect(pinned.nth(1)).toHaveText('Talk')
    await expect(nav.getByTestId('apps-show-all')).toHaveCount(0)
    await expect(nav.getByText('Show all')).toHaveCount(0)
    const group = await nav.locator('a.nav-item').evaluateAll(els => els.map(e => e.textContent.trim()))
    const at = group.indexOf('Apps')
    expect(group.slice(at, at + 4)).toEqual(['Apps', 'Studio', 'Talk'])
    await expect(pinned.nth(0)).toHaveAttribute('href', /\/app\/addons\/studio\//)
    // There is no Pinned section.
    await expect(page.getByTestId('pinned-nav')).toHaveCount(0)
    await expect(page.locator('.sidebar-section-title', { hasText: 'Pinned' })).toHaveCount(0)
  })

  test('the sidebar follows the pin order and shows no pinned app when none is pinned', async ({ page }) => {
    await setup(page, { pins: ['talk', 'studio', 'missing'] })
    await page.goto('/app')
    const nav = page.getByRole('navigation')
    await expect(nav.getByTestId('apps-pinned-link')).toHaveText(['Talk', 'Studio'])
    await expect(nav.getByTestId('apps-show-all')).toHaveCount(0)
    await page.unroute('**/api/addons/pins')
    await page.route('**/api/addons/pins', route => route.fulfill({ json: [] }))
    await page.goto('/app')
    await expect(nav.getByRole('link', { name: 'Apps', exact: true })).toHaveCount(1)
    await expect(nav.getByTestId('apps-show-all')).toHaveCount(0)
    await expect(nav.getByTestId('apps-pinned-link')).toHaveCount(0)
  })

  test('the icon rail shows Apps only, centred, with no pinned app', async ({ page }) => {
    await setup(page)
    for (const size of [{ width: 1280, height: 800, collapsed: true }, { width: 1000, height: 800, collapsed: false }]) {
      await page.setViewportSize({ width: size.width, height: size.height })
      await page.addInitScript(collapsed => {
        try { localStorage.setItem('localai_sidebar_collapsed', String(collapsed)) } catch (_) { /* ignore */ }
      }, size.collapsed)
      await page.goto('/app')
      const nav = page.getByRole('navigation')
      await expect(nav.locator('a.nav-item[href="/app/addons"]')).toBeVisible()
      await expect(nav.getByTestId('apps-pinned-link').filter({ visible: true })).toHaveCount(0)
      const rail = await page.locator('.sidebar').boundingBox()
      const icons = await nav.locator('a.nav-item:visible .nav-icon').evaluateAll(els => els.map(e => {
        const r = e.getBoundingClientRect()
        return (r.left + r.right) / 2
      }))
      expect(icons.length).toBeGreaterThan(0)
      for (const x of icons) expect(Math.abs(x - (rail.x + rail.width / 2))).toBeLessThan(2)
    }
  })

  test('the Apps entry opens the drawer, where pinned add-ons are shown and their order can be changed', async ({ page }) => {
    const server = await setup(page, { pins: ['studio', 'talk'] })
    await page.goto('/app')
    await page.getByRole('navigation').getByRole('link', { name: 'Apps' }).click()
    await expect(page).toHaveURL(/\/app\/addons$/)
    await expect(page.getByTestId('addon-tile')).toHaveCount(2)
    await expect(page.getByTestId('pin-toggle')).toHaveCount(2)
    for (const toggle of await page.getByTestId('pin-toggle').all()) {
      await expect(toggle).toHaveAttribute('aria-pressed', 'true')
      await expect(toggle).toHaveText('Pinned')
    }
    await page.locator('[data-testid="addon-tile"][data-app="studio"]').getByTestId('pin-later').click()
    await expect.poll(() => server.saved()).toEqual(['talk', 'studio'])
  })

  test('the drawer lists tiles, searches them and pins from the keyboard', async ({ page }) => {
    const server = await setup(page, { pins: [], apps: [app({ id: 'notes', title: 'Notes', state: 'running', mainPage: '/app/addons/notes/home' })] })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(3)
    await page.getByTestId('addon-search').fill('note')
    await expect(page.getByTestId('addon-tile')).toHaveCount(1)
    const toggle = page.getByTestId('pin-toggle').first()
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
    await expect(toggle).toHaveAccessibleName(/Pin Notes/)
    await expect(toggle).toHaveText('Pin')
    await toggle.focus()
    await page.keyboard.press('Enter')
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await expect(toggle).toHaveText('Pinned')
    expect(server.saved()).toEqual(['notes'])
  })

  test('pinned apps come first in the grid, in pin order, and the detail view has the same pin control', async ({ page }) => {
    const server = await setup(page, {
      pins: ['notes'],
      apps: [app({ id: 'notes', title: 'Notes', state: 'running', mainPage: '/app/addons/notes/home' }), app({ id: 'zeta', title: 'Zeta', enabled: false, state: 'stopped' })],
    })
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/app/addons')
    const tiles = page.getByTestId('addon-tile')
    await expect(tiles.first()).toHaveAttribute('data-app', 'notes')
    await expect(tiles.first().getByTestId('pin-toggle')).toHaveText('Pinned')
    await expect(tiles.nth(1).getByTestId('pin-toggle')).toHaveText('Pin')
    // The tile pin and the detail pin are one state.
    await page.goto('/app/addons?detail=zeta')
    const detail = page.getByTestId('addon-detail')
    const head = detail.getByTestId('pin-toggle')
    await expect(head).toHaveText('Pin')
    await head.click()
    await expect(head).toHaveText('Pinned')
    await expect(head).toHaveAttribute('aria-pressed', 'true')
    await expect.poll(() => server.saved()).toEqual(['notes', 'zeta'])
    await head.click()
    await expect(head).toHaveText('Pin')
    await expect.poll(() => server.saved()).toEqual(['notes'])
  })

  test('the first-run card shows to an administrator with nothing installed and leads to Browse', async ({ page }) => {
    await setup(page, { user: ADMIN, studio: false, pins: [] })
    await page.goto('/app/addons')
    const card = page.getByTestId('addons-first-run')
    await expect(card).toBeVisible()
    await expect(card.getByRole('listitem')).toHaveCount(3)
    await expect(card).toContainText('official library is registered by default')
    await expect(page.getByTestId('addon-tile')).toHaveCount(0)
    await card.getByTestId('addons-first-run-browse').click()
    await expect(page).toHaveURL(/tab=browse/)
    await expect(page.getByTestId('addons-first-run')).toHaveCount(0)
  })

  test('the first-run card is hidden once an add-on is installed', async ({ page }) => {
    await setup(page, { user: ADMIN, pins: [] })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile').first()).toBeVisible()
    await expect(page.getByTestId('addons-first-run')).toHaveCount(0)
  })

  test('the first-run card is hidden from a caller who cannot install add-ons', async ({ page }) => {
    await setup(page, { studio: false, pins: [] })
    await page.goto('/app/addons')
    await expect(page.getByRole('heading', { name: 'No apps yet' })).toBeVisible()
    await expect(page.getByTestId('addons-first-run')).toHaveCount(0)
  })

  test('a stopped tile opens the detail view and never the main page', async ({ page }) => {
    await setup(page, { apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped', mainPage: undefined })] })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'Notes' }).getByRole('button', { name: /^Notes,/ }).click()
    await expect(page.getByTestId('addon-detail')).toBeVisible()
    expect(page.url()).toContain('/app/addons?detail=notes')
  })

  test('a pinned tile can be moved later and the new order is saved', async ({ page }) => {
    const server = await setup(page, { pins: ['studio', 'talk'] })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'Studio' }).getByTestId('pin-later').click()
    await expect.poll(() => server.saved()).toEqual(['talk', 'studio'])
  })

  test('the ninth pin asks the user to unpin one', async ({ page }) => {
    const nine = Array.from({ length: 9 }, (_, i) => app({ id: `a${i}`, title: `App ${i}`, state: 'running', mainPage: `/app/addons/a${i}/home` }))
    const eight = nine.slice(0, 8).map(a => a.id)
    await setup(page, { apps: nine, pins: eight })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'App 8' }).getByTestId('pin-toggle').click()
    await expect(page.getByRole('alertdialog').or(page.getByRole('dialog'))).toContainText(/unpin/i)
  })

  test('member without the add-ons feature sees the built-in tiles and no error', async ({ page }) => {
    let appsCalled = false
    // No page is built in today, so the spec makes one up.
    await setup(page, { addons: false, builtin: [{ id: 'guide', kind: 'page', titleKey: 'items.chat', icon: 'chat', mainPath: '/app/chat' }] })
    // Registered after setup, so it wins over the mocked Apps route.
    await page.route(/\/api\/addons\/apps$/, route => { appsCalled = true; return route.fulfill({ status: 403, json: {} }) })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(1)
    await expect(page.getByTestId('addons-browse')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(appsCalled).toBe(false)
  })

  // The icon of a built-in tile sat in the top-left corner of its 40px box, at the
  // size of the page text, because only the empty-icon box was laid out as a grid.
  test('a built-in tile draws its icon centred in the icon box', async ({ page }) => {
    await setup(page, { builtin: [{ id: 'guide', kind: 'page', titleKey: 'items.chat', icon: 'palette', mainPath: '/app/chat' }], studio: false })
    await page.goto('/app/addons')
    const box = page.locator('.ad-icon').first()
    await expect(box.locator('svg[data-icon="palette"]')).toBeVisible()
    const offsets = await box.evaluate(el => {
      const outer = el.getBoundingClientRect()
      const inner = el.querySelector('svg').getBoundingClientRect()
      return { dx: (inner.left + inner.width / 2) - (outer.left + outer.width / 2), dy: (inner.top + inner.height / 2) - (outer.top + outer.height / 2), width: inner.width, box: outer.width }
    })
    expect(Math.abs(offsets.dx)).toBeLessThan(1)
    expect(Math.abs(offsets.dy)).toBeLessThan(1)
    expect(offsets.width).toBeGreaterThan(16)
    expect(offsets.box).toBe(40)
  })

  test('member without the add-ons feature and with no built-in tile goes home, and Studio and Talk are not built in', async ({ page }) => {
    await setup(page, { addons: false })
    await page.goto('/app/addons')
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.getByRole('navigation').locator('a.nav-item[href="/app/addons"]')).toHaveCount(0)
  })

  test('the legacy flag brings the old sidebar back', async ({ page }) => {
    await setup(page)
    await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: false } }))
    await page.goto('/app')
    await expect(page.getByTestId('apps-show-all')).toHaveCount(0)
    // Home, Models and Chat head the old sidebar; Studio and Talk are add-ons and are not in it.
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Chat' })).toBeVisible()
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Studio' })).toHaveCount(0)
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toHaveCount(0)
  })

  test('the layout holds at phone width', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 700 })
    await setup(page)
    await page.goto('/app/addons')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(overflow).toBe(false)
  })

  test("the server's admin block is what shows a Start action for a stopped add-on in the detail view", async ({ page }) => {
    await setup(page, { apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped', admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=notes' } })] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor({ ...MEMBER, role: 'admin' }, { addons: true }) }))
    await page.goto('/app/addons?detail=notes')
    await expect(page.getByTestId('addon-detail').getByRole('button', { name: 'Start' })).toBeVisible()
  })

  test('the pin control has a visible focus state and a name', async ({ page }) => {
    await setup(page)
    await page.goto('/app/addons')
    const toggle = page.getByTestId('pin-toggle').first()
    await toggle.focus()
    const outline = await toggle.evaluate(el => getComputedStyle(el).outlineStyle)
    expect(outline).toBe('solid')
    const width = await toggle.evaluate(el => parseFloat(getComputedStyle(el).outlineWidth))
    expect(width).toBeGreaterThan(0)
    await expect(toggle).toHaveAccessibleName(/(Pin|Unpin) /)
  })

  test('a pin of an add-on that is no longer listed is not drawn in the drawer and shows no error', async ({ page }) => {
    await setup(page, { pins: ['studio', 'removed-addon'] })
    // The Home page raises its own alert when the model list or a staging
    // operation fails. Both come from the shared test server, so a slow answer
    // or a failed install left there would count as an alert about the pin.
    // Fix their answers so the alert count tells only about the pin.
    await page.route('**/v1/models', route => route.fulfill({ json: { object: 'list', data: [] } }))
    await page.route('**/api/operations', route => route.fulfill({ json: { operations: [] } }))
    // Hold the Apps list back so the assertions run after it has arrived and
    // been drawn from, not before, when a missing pin proves nothing.
    await page.route(/\/api\/addons\/apps$/, async route => {
      await new Promise(resolve => setTimeout(resolve, 4000))
      await route.fulfill({ json: { apps: [packagedApp('studio')] } })
    })
    const listed = page.waitForResponse(res => /\/api\/addons\/apps$/.test(res.url()))
    await page.goto('/app/addons')
    await listed
    // Two frames: the list reaches React and the drawer redraws from it.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    await expect(page.getByTestId('addon-tile')).toHaveCount(1)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('a stopped pinned add-on in the drawer opens the detail view', async ({ page }) => {
    await setup(page, { pins: ['notes'], apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped' })] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor({ ...MEMBER, role: 'admin' }, { addons: true }) }))
    await page.goto('/app/addons')
    const tile = page.locator('[data-testid="addon-tile"][data-app="notes"]')
    await expect(tile).toHaveAttribute('data-state', 'stopped')
    await tile.locator('.ad-tile__open').click()
    await expect(page).toHaveURL(/\/app\/addons\?detail=notes$/)
    await expect(page.getByTestId('addon-detail')).toBeVisible()
  })

  test('core pages that share a primitive with the SDK draw the same header', async ({ page }) => {
    await setup(page)
    await page.goto('/app/addons')
    const header = page.locator('header.page-header').first()
    await expect(header.locator('h1.page-title')).toHaveText('Add-ons')
    expect(await header.getAttribute('class')).toBe('page-header page-header--editorial')
  })
})

test.describe('Add-on navigation: the Add-ons hub tab and the route in both layouts', () => {
  test('the new layout has no Add-ons hub tab and serves the drawer at /app/addons', async ({ page }) => {
    await setup(page)
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="addons"]')).toHaveCount(0)
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(2)
    await expect(page.locator('.dk-hubtabs')).toHaveCount(0)
  })

  test('the legacy flag brings the Add-ons hub tab back and /app/addons opens under it', async ({ page }) => {
    await setup(page, { apps: [app({ id: 'notes', title: 'Notes', state: 'running', mainPage: '/app/addons/notes/home' })] })
    await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: false } }))
    await page.goto('/app/build')
    const hubTab = page.locator('.dk-hubtabs [data-hub-tab="addons"]')
    await expect(hubTab).toHaveAttribute('href', '/app/addons')
    await hubTab.click()
    await expect(page).toHaveURL(/\/app\/addons$/)
    await expect(hubTab).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('addon-tile').filter({ hasText: 'Notes' })).toBeVisible()
    await page.goto('/app/addons')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="addons"]')).toHaveAttribute('aria-current', 'page')
  })

  test('an old generator link keeps its model, query and hash', async ({ page }) => {
    await setup(page)
    await page.goto('/app/image/stable%2Fdiffusion?x=1#top')
    await expect(page).toHaveURL(/\/app\/addons\/studio\/studio\/images\?x=1&model=stable%2Fdiffusion#top$/)
  })

  test('the legacy ?tab= link of Studio still works', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio?tab=tts')
    await expect(page).toHaveURL(/\/app\/addons\/studio\/studio\/tts$/)
  })

  test('a viewer without the 3d feature still ends on the app home', async ({ page }) => {
    await setup(page)
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, '3d': false }) }))
    await page.goto('/app/3d')
    await expect(page).toHaveURL(/\/app$/)
  })
})

// The built Voice module, served the way the owner serves it (addons-shell.spec.js
// does the same for the uifixture module).
const VOICE_DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons/voice/ui/dist')
const VOICE_DIGEST = 'd'.repeat(64)
const VOICE_BASE = `/api/addons/voice/assets/${VOICE_DIGEST}`

// dist/ is not committed and CI does not build it, so the spec builds the
// bundle itself, once per run, with the builder of the add-on.
test.beforeAll(async () => {
  const { build } = await import('../../../../addons/voice/ui/build.mjs')
  await build()
})

const voiceApp = () => app({
  id: 'voice', name: 'voice', title: 'Voice', icon: undefined, generation: 1, version: '0.1.0', mainPage: '/app/addons/voice/voice',
  ui: { entry: `${VOICE_BASE}/ui/voice.js`, css: [`${VOICE_BASE}/ui/voice.css`], sdk: { min: 2, max: 2 }, pages: ['voice', 'recognition', 'library', 'create', 'speech'], navigation: ['voice'], chatActions: [] },
})

async function serveVoice(page) {
  await page.route(/\/api\/addons\/voice\/assets\//, route => {
    const file = new URL(route.request().url()).pathname.split('/').slice(6).join('/')
    const known = { 'ui/voice.js': 'text/javascript', 'ui/voice.css': 'text/css' }
    if (!known[file]) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({ status: 200, contentType: known[file], headers: { 'cache-control': 'no-store' }, body: fs.readFileSync(path.join(VOICE_DIST, file)) })
  })
}

async function asAdmin(page, features = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true, voice_recognition: true, ...features }) }))
}

async function expectVoicesPage(page) {
  await expect(page.getByTestId('voices-page')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Voices', level: 1 })).toBeVisible()
  for (const name of [/Speakers/, /Speech voices/, /From a recording/]) {
    await expect(page.getByRole('tab', { name })).toBeVisible()
  }
}

// The tabs of the one Voice page. An administrator sees all four.
async function expectVoiceTabs(page, active) {
  const tabs = page.getByTestId('voice-tabs')
  await expect(tabs).toBeVisible()
  for (const name of [/^Recognition/, /^Library/, /^Text to speech$/, /^From a recording$/]) {
    await expect(tabs.getByRole('tab', { name })).toBeVisible()
  }
  await expect(tabs.locator('[aria-selected="true"]')).toHaveAttribute('data-tab', active)
}

// The add-on draws its own title, once: the shell adds none, and each tab has
// exactly one h1.
async function expectOneVoiceTitle(page, title) {
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { name: title, level: 1 })).toBeVisible()
}

test.describe('Add-on navigation: the Voice recognition page', () => {
  test('the Voice add-on draws the recognition tab with the core test ids', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    await mockIdentity(page)
    await page.goto('/app/addons/voice/voice/recognition')
    await expect(page.getByTestId('voices-page')).toBeVisible()
    await expectOneVoiceTitle(page, 'Voices')
    await expectVoiceTabs(page, 'recognition')
    await expect(page.locator('[data-testid="addon-surface"][data-addon="voice"]')).toBeVisible()
    await expect(page.getByTestId('known-empty')).toContainText('Nobody enrolled in this browser yet')
  })

  test('the core page, with the add-on absent, shows the same test ids and tabs', async ({ page }) => {
    await setup(page, { apps: [] })
    await asAdmin(page)
    await mockIdentity(page)
    await page.goto('/app/voice')
    await expect(page).toHaveURL(/\/app\/voice$/)
    await expectVoicesPage(page)
  })

  test('an old model link ends in the add-on with the model in the query', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    await mockIdentity(page)
    await page.goto('/app/voice/org%2Fname')
    await expect(page).toHaveURL(/\/app\/addons\/voice\/voice\/recognition\?model=org%2Fname$/)
    await expect(page.getByTestId('voices-page')).toBeVisible()
  })

  test('a viewer without voice_recognition gets the off notice in the add-on', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    // An administrator holds every feature, so the viewer is a member.
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, voice_recognition: false }) }))
    await serveVoice(page)
    await page.goto('/app/addons/voice/voice/recognition')
    await expect(page.getByTestId('voice-off')).toBeVisible()
    await expect(page.getByTestId('voices-page')).toHaveCount(0)
  })

  test('the sections are tabs of one page and each tab keeps its address', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    await mockIdentity(page)
    await page.goto('/app/addons/voice/voice')
    // No tab in the address opens the default tab.
    await expect(page.getByTestId('voices-page')).toBeVisible()
    await expectVoiceTabs(page, 'recognition')

    const tabs = page.getByTestId('voice-tabs')
    await tabs.getByRole('tab', { name: /^Library/ }).click()
    await expect(page).toHaveURL(/\/app\/addons\/voice\/voice\/library$/)
    await expect(page.getByTestId('voice-library')).toBeVisible()
    await expectVoiceTabs(page, 'library')
    await expectOneVoiceTitle(page, /^Speech voices/)

    await page.getByTestId('voice-library-new').first().click()
    await expect(page).toHaveURL(/\/app\/addons\/voice\/voice\/library\/new$/)
    await expect(page.getByTestId('voice-profile-create')).toBeVisible()
    await expectVoiceTabs(page, 'library')
    await expectOneVoiceTitle(page, /Add a speech voice/)

    await tabs.getByRole('tab', { name: /^Text to speech$/ }).click()
    await expect(page).toHaveURL(/\/app\/addons\/voice\/voice\/speech$/)
    await expect(page.getByTestId('voice-speech')).toBeVisible()
    await expectVoiceTabs(page, 'speech')
    await expectOneVoiceTitle(page, 'Speak text')

    await tabs.getByRole('tab', { name: /^From a recording$/ }).click()
    await expect(page).toHaveURL(/\/app\/addons\/voice\/voice\/recording$/)
    await expect(page.getByTestId('recording-tab')).toBeVisible()
    await expectVoiceTabs(page, 'recording')
    await expectOneVoiceTitle(page, 'Voices')
  })

  test('a viewer who is not an administrator gets no Library tab', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, voice_recognition: true }) }))
    await serveVoice(page)
    await mockIdentity(page)
    await page.goto('/app/addons/voice/voice/recognition')
    const tabs = page.getByTestId('voice-tabs')
    await expect(tabs.getByRole('tab', { name: /^Recognition/ })).toBeVisible()
    await expect(tabs.getByRole('tab', { name: /^Text to speech$/ })).toBeVisible()
    await expect(tabs.getByRole('tab', { name: /^Library/ })).toHaveCount(0)
    await page.goto('/app/addons/voice/voice/library')
    await expect(page.getByTestId('voice-admin-only')).toBeVisible()
    await expect(page.getByTestId('voice-library')).toHaveCount(0)
  })

  test('the older page addresses of the add-on open the matching tab', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    await mockIdentity(page)
    const cases = [
      ['/app/addons/voice/recognition', /\/voice\/voice\/recognition$/, 'recognition'],
      ['/app/addons/voice/recognition?model=wespeaker-resnet34', /\/voice\/voice\/recognition\?model=wespeaker-resnet34$/, 'recognition'],
      ['/app/addons/voice/recognition?tab=recording', /\/voice\/voice\/recording$/, 'recording'],
      ['/app/addons/voice/library?selected=prof-1', /\/voice\/voice\/library\?selected=prof-1$/, 'library'],
      ['/app/addons/voice/create', /\/voice\/voice\/library\/new$/, 'library'],
      ['/app/addons/voice/speech?voice=prof-1', /\/voice\/voice\/speech\?voice=prof-1$/, 'speech'],
    ]
    for (const [from, to, active] of cases) {
      await page.goto(from)
      await expect(page, from).toHaveURL(to)
      await expectVoiceTabs(page, active)
    }
  })

  test('the Build hub has no Voices tab', async ({ page }) => {
    await setup(page, { apps: [] })
    await asAdmin(page)
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs')).toBeVisible()
    await expect(page.locator('.dk-hubtabs [data-hub-tab="voices"]')).toHaveCount(0)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="faces"]')).toHaveCount(0)
  })

  test('the add-on page identifies, enrols from a side panel and holds a removal behind Undo', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    const calls = await mockIdentity(page, { stored: people, identify: { matches: [match('v-marta', 'Marta Rossi', 0.18), match('v-jonas', 'Jonas Weber', 0.41)] } })
    await page.goto('/app/addons/voice/voice/recognition')
    await expect(page.getByTestId('voices-page')).toBeVisible()

    await page.locator('#voice-probe-audio-file').setInputFiles(clipFile())
    await expect(page.getByTestId('voice-probe-clip')).toHaveAttribute('data-state', 'filled')
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toContainText('Probably Marta Rossi')
    await expect(page.getByTestId('distance-scale')).toBeVisible()
    expect(calls.identify).toHaveLength(1)
    expect(calls.identify[0].model).toBe('wespeaker-resnet34')

    await page.getByTestId('enrol-open').click()
    const sheet = page.getByTestId('enrol-sheet')
    await expect(sheet).toBeVisible()
    await sheet.locator('#voice-enrol-audio-file').setInputFiles(clipFile('new.wav', 6))
    await sheet.locator('#voice-enrol-name').fill('Ada Byron')
    await sheet.getByLabel(/This person agreed/).check()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect.poll(() => calls.register.length).toBe(1)
    expect(calls.register[0].name).toBe('Ada Byron')
    await expect(sheet).toHaveCount(0)
    await expect(page.getByTestId('known-list')).toContainText('Ada Byron')

    await page.clock.install()
    await page.getByRole('button', { name: 'Remove Jonas Weber' }).click()
    await expect(page.getByRole('status').filter({ hasText: 'Jonas Weber' })).toBeVisible()
    await page.getByRole('button', { name: 'Undo' }).first().click()
    // Past the 10 second delay: a cancelled removal must send nothing.
    await page.clock.fastForward(15000)
    await page.waitForTimeout(300)
    expect(calls.forget).toHaveLength(0)
    await expect(page.getByTestId('known-list')).toContainText('Jonas Weber')
  })
})

// The built Face module, served the same way. dist/ is not committed, so the
// global setup builds it once per run with the builder of the add-on.
const FT_DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons/fine-tuning/ui/dist')
const FACE_DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons/face/ui/dist')
const FACE_BASE = `/api/addons/face/assets/${'e'.repeat(64)}`

test.beforeAll(async () => {
  // The global setup has built the bundles already. A build rewrites the
  // dist folder that every worker serves, so a worker must not start one.
  if (process.env.E2E_ADDON_BUNDLES_BUILT) return
  await buildAddonBundles(['face', 'fine-tuning'])
})

const faceApp = () => app({
  id: 'face', name: 'face', title: 'Face', icon: undefined, generation: 1, version: '0.1.0', mainPage: '/app/addons/face/recognition',
  ui: { entry: `${FACE_BASE}/ui/face.js`, css: [`${FACE_BASE}/ui/face.css`], sdk: { min: 2, max: 2 }, pages: ['recognition'], navigation: [], chatActions: [] },
})

const FT_BASE = `/api/addons/fine-tuning/assets/${'f'.repeat(64)}`
const ftApp = () => app({
  id: 'fine-tuning', name: 'fine-tuning', title: 'Fine-tuning', icon: undefined, generation: 1, version: '0.1.0', mainPage: '/app/addons/fine-tuning/jobs',
  ui: { entry: `${FT_BASE}/ui/fine-tuning.js`, css: [`${FT_BASE}/ui/fine-tuning.css`], sdk: { min: 2, max: 2 }, pages: ['jobs'], navigation: [], chatActions: [] },
})

async function serveFineTuning(page) {
  await page.route(/\/api\/addons\/fine-tuning\/assets\//, route => {
    const file = new URL(route.request().url()).pathname.split('/').slice(6).join('/')
    const known = { 'ui/fine-tuning.js': 'text/javascript', 'ui/fine-tuning.css': 'text/css' }
    if (!known[file]) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({ status: 200, contentType: known[file], headers: { 'cache-control': 'no-store' }, body: fs.readFileSync(path.join(FT_DIST, file)) })
  })
}

async function serveFace(page) {
  await page.route(/\/api\/addons\/face\/assets\//, route => {
    const file = new URL(route.request().url()).pathname.split('/').slice(6).join('/')
    const known = { 'ui/face.js': 'text/javascript', 'ui/face.css': 'text/css' }
    if (!known[file]) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({ status: 200, contentType: known[file], headers: { 'cache-control': 'no-store' }, body: fs.readFileSync(path.join(FACE_DIST, file)) })
  })
}

async function asFaceAdmin(page, features = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true, face_recognition: true, ...features }) }))
}

// The page draws its own title "Faces", once, in the core and in the add-on.
async function expectFacesPage(page, title = 'Faces') {
  await expect(page.getByTestId('faces-page')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('heading', { name: title, exact: true, level: 1 })).toBeVisible()
}

test.describe('Add-on navigation: the Face recognition page', () => {
  // Where the add-on page mounts, the add-on titles it and the shell adds
  // nothing; where it cannot mount, the shell names the add-on and nothing
  // else does. Either way the page has exactly one title.
  test('the Face page that is not enabled on this server has one title, the add-on\'s', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    await asFaceAdmin(page)
    await serveFace(page)
    await mockIdentity(page, { faceModels: [] })
    await page.goto('/app/addons/face/recognition')
    await expectFacesPage(page)
    await expect(page.getByText('Not enabled on this server')).toBeVisible()
    await expect(page.getByTestId('addon-unavailable')).toHaveCount(0)
    await expect(page.locator('h1')).toHaveCount(1)
  })

  test('the Face page of a stopped add-on has one title, the shell\'s, because the add-on cannot draw', async ({ page }) => {
    await setup(page, { apps: [{ ...faceApp(), enabled: false, state: 'stopped' }] })
    await asFaceAdmin(page)
    await serveFace(page)
    await mockIdentity(page)
    await page.goto('/app/addons/face/recognition')
    await expect(page.getByTestId('addon-unavailable')).toHaveAttribute('data-reason', 'disabled')
    await expect(page.getByTestId('faces-page')).toHaveCount(0)
    await expect(page.locator('h1')).toHaveCount(1)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Face')
  })

  test('the Face add-on draws the same page and test ids as the core page', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    await asFaceAdmin(page)
    await serveFace(page)
    await mockIdentity(page)
    await page.goto('/app/addons/face/recognition')
    await expectFacesPage(page)
    await expect(page.locator('[data-testid="addon-surface"][data-addon="face"]')).toBeVisible()
    await expect(page.getByTestId('known-empty')).toContainText('Nobody enrolled in this browser yet')
  })

  test('the core page, with the add-on absent, shows the same test id and title', async ({ page }) => {
    await setup(page, { apps: [] })
    await asFaceAdmin(page)
    await mockIdentity(page)
    await page.goto('/app/face')
    await expect(page).toHaveURL(/\/app\/face$/)
    await expectFacesPage(page)
  })

  test('an old model link ends in the add-on with the model in the query', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    await asFaceAdmin(page)
    await serveFace(page)
    await mockIdentity(page)
    await page.goto('/app/face/org%2Fname')
    await expect(page).toHaveURL(/\/app\/addons\/face\/recognition\?model=org%2Fname$/)
    await expect(page.getByTestId('faces-page')).toBeVisible()
  })

  test('a viewer without face_recognition gets the off notice in the add-on', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    // An administrator holds every feature, so the viewer is a member.
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, face_recognition: false }) }))
    await serveFace(page)
    await page.goto('/app/addons/face/recognition')
    await expect(page.getByTestId('face-off')).toBeVisible()
    await expect(page.getByTestId('faces-page')).toHaveCount(0)
  })

  test('a member without fine_tuning sees the off notice inside the add-on, and the core call answers 403', async ({ page }) => {
    await setup(page, { apps: [ftApp()] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, fine_tuning: false }) }))
    await page.route('**/api/fine-tuning/jobs', route => route.fulfill({ status: 403, json: { error: 'forbidden' } }))
    await serveFineTuning(page)
    await page.goto('/app/addons/fine-tuning/jobs')
    await expect(page.getByTestId('tool-off')).toBeVisible()
  })

  test('an old fine-tuning link opens the core page when the add-on is not installed', async ({ page }) => {
    await setup(page, { apps: [] })
    await page.goto('/app/fine-tune')
    await expect(page).toHaveURL(/\/app\/fine-tune$/)
  })

  test('an old fine-tuning link opens the add-on when it is installed and running', async ({ page }) => {
    await setup(page, { apps: [ftApp()] })
    await serveFineTuning(page)
    await page.goto('/app/fine-tune')
    await expect(page).toHaveURL(/\/app\/addons\/fine-tuning\/jobs$/)
  })

  test('an old quantization link opens the core page when the add-on is not installed', async ({ page }) => {
    await setup(page, { apps: [] })
    await page.goto('/app/quantize')
    await expect(page).toHaveURL(/\/app\/quantize$/)
  })

  test('an old quantization link opens the add-on when it is installed and running', async ({ page }) => {
    await setup(page, { apps: [app({ id: 'quantization', name: 'quantization', title: 'Quantization', icon: undefined, mainPage: '/app/addons/quantization/jobs', ui: { entry: `/api/addons/quantization/assets/${'a'.repeat(64)}/ui/quantization.js`, css: [], sdk: { min: 2, max: 2 }, pages: ['jobs'], navigation: [], chatActions: [] } })] })
    await page.goto('/app/quantize')
    await expect(page).toHaveURL(/\/app\/addons\/quantization\/jobs$/)
  })

  test('the Build hub has no fine-tuning or quantization tab', async ({ page }) => {
    await setup(page)
    await asFaceAdmin(page)
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs')).toBeVisible()
    await expect(page.getByRole('tab', { name: /Fine-tun/ })).toHaveCount(0)
    await expect(page.getByRole('tab', { name: /Quantiz/ })).toHaveCount(0)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="fine-tune"], .dk-hubtabs [data-hub-tab="quantize"]')).toHaveCount(0)
  })

  test('the Build hub shows no Faces tab', async ({ page }) => {
    await setup(page, { apps: [] })
    await asFaceAdmin(page)
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs')).toBeVisible()
    await expect(page.locator('.dk-hubtabs [data-hub-tab="faces"]')).toHaveCount(0)
  })

  test('the add-on page identifies, enrols from a side panel and holds a removal behind Undo', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    await asFaceAdmin(page)
    await serveFace(page)
    const calls = await mockIdentity(page, { storedFaces: faces, faceIdentify: { matches: [match('f-anna', 'Anna Kowalski', 0.21), match('f-tomas', 'Tomas Ribeiro', 0.52)] } })
    await page.goto('/app/addons/face/recognition')
    await expect(page.getByTestId('faces-page')).toBeVisible()

    await page.locator('#face-probe-image-file').setInputFiles(photoFile())
    await page.getByTestId('identify-go').click()
    await expect(page.getByTestId('verdict')).toContainText('Probably Anna Kowalski')
    expect(calls.faceIdentify).toHaveLength(1)
    expect(calls.faceIdentify[0].model).toBe(FACE_MODEL)
    expect(calls.faceIdentify[0].img).toMatch(/^data:image\//)

    await page.getByTestId('enrol-open').click()
    const sheet = page.getByTestId('enrol-sheet')
    await expect(sheet).toBeVisible()
    await sheet.locator('#face-enrol-image-file').setInputFiles(photoFile('new.png'))
    await sheet.locator('#face-enrol-name').fill('Ada Byron')
    await sheet.getByLabel(/This person agreed/).check()
    await sheet.getByRole('button', { name: /^Enrol/ }).click()
    await expect.poll(() => calls.faceRegister.length).toBe(1)
    expect(calls.faceRegister[0]).toMatchObject({ model: FACE_MODEL, name: 'Ada Byron' })
    await expect(sheet).toHaveCount(0)
    await expect(page.getByTestId('known-list')).toContainText('Ada Byron')

    await page.clock.install()
    await page.getByRole('button', { name: 'Remove Anna Kowalski' }).click()
    await expect(page.locator('.vc-undo').filter({ hasText: 'Anna Kowalski' })).toBeVisible()
    await page.getByRole('button', { name: 'Undo' }).first().click()
    // Past the 10 second delay: a cancelled removal must send nothing.
    await page.clock.fastForward(15000)
    await page.waitForTimeout(300)
    expect(calls.forget).toHaveLength(0)
    await expect(page.getByTestId('known-list')).toContainText('Anna Kowalski')
  })

  test('with the removal left to run, the add-on sends the face forget call', async ({ page }) => {
    await setup(page, { apps: [faceApp()] })
    await asFaceAdmin(page)
    await serveFace(page)
    await page.clock.install()
    const calls = await mockIdentity(page, { storedFaces: faces })
    await page.goto('/app/addons/face/recognition')
    await page.getByRole('button', { name: 'Remove Anna Kowalski' }).click()
    await page.clock.runFor(11000)
    await expect.poll(() => calls.forget.length).toBe(1)
    expect(calls.forget[0]).toMatchObject({ face: true, id: 'f-anna' })
  })
})
