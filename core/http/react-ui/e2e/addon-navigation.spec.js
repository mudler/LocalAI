import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { ADMIN, MEMBER, app, sessionFor, mockAddons } from './addons-fixtures.js'
import { FACE_MODEL, clipFile, faces, match, mockIdentity, people, photoFile } from './identity-fixtures.js'

const STUDIO = { id: 'studio', kind: 'page', titleKey: 'items.studio', icon: 'palette', mainPath: '/app/studio' }
const TALK = { id: 'talk', kind: 'page', titleKey: 'items.talk', icon: 'phone', mainPath: '/app/talk' }

async function setup(page, { apps = [], pins = ['studio', 'talk'], addons = true, builtin = [STUDIO, TALK] } = {}) {
  let saved = pins
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons }) }))
  await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: true } }))
  await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin } }))
  // mockAddons installs the Apps routes with the real response shape ({ apps }).
  await mockAddons(page, { apps })
  await page.route('**/api/addons/pins', async route => {
    if (route.request().method() === 'PUT') saved = route.request().postDataJSON()
    await route.fulfill({ json: saved })
  })
  return { saved: () => saved }
}

test.describe('Add-on navigation', () => {
  test('the sidebar shows Chat, Add-ons and the Pinned section', async ({ page }) => {
    await setup(page)
    await page.goto('/app')
    const nav = page.getByRole('navigation')
    await expect(nav.getByRole('link', { name: 'Chat' })).toBeVisible()
    await expect(nav.getByRole('link', { name: 'Add-ons' })).toBeVisible()
    const pinned = page.getByTestId('pinned-nav')
    await expect(pinned.getByRole('link', { name: 'Studio' })).toBeVisible()
    await expect(pinned.getByRole('link', { name: 'Talk' })).toBeVisible()
  })

  test('an empty Pinned section is hidden', async ({ page }) => {
    await setup(page, { pins: [] })
    await page.goto('/app')
    await expect(page.getByTestId('pinned-nav')).toHaveCount(0)
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
    await toggle.focus()
    await page.keyboard.press('Enter')
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(server.saved()).toEqual(['notes'])
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
    await setup(page, { addons: false })
    // Registered after setup, so it wins over the mocked Apps route.
    await page.route(/\/api\/addons\/apps$/, route => { appsCalled = true; return route.fulfill({ status: 403, json: {} }) })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(2)
    await expect(page.getByTestId('addons-browse')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(appsCalled).toBe(false)
  })

  test('the legacy flag brings the old sidebar back', async ({ page }) => {
    await setup(page)
    await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: false } }))
    await page.goto('/app')
    await expect(page.getByTestId('pinned-nav')).toHaveCount(0)
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Studio' })).toBeVisible()
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

  test('a pin of an add-on that is no longer listed is not drawn and shows no error', async ({ page }) => {
    await setup(page, { pins: ['studio', 'removed-addon'] })
    // Hold the Apps list back so the assertions run after it has arrived and
    // been drawn from, not before, when a missing pin proves nothing.
    await page.route(/\/api\/addons\/apps$/, async route => {
      await new Promise(resolve => setTimeout(resolve, 4000))
      await route.fulfill({ json: { apps: [] } })
    })
    const listed = page.waitForResponse(res => /\/api\/addons\/apps$/.test(res.url()))
    await page.goto('/app')
    await listed
    // Two frames: the list reaches React and the sidebar redraws from it.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    const pinned = page.getByTestId('pinned-nav')
    await expect(pinned.getByRole('link')).toHaveCount(1)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('a stopped pinned add-on is muted and opens the detail view', async ({ page }) => {
    await setup(page, { pins: ['notes'], apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped' })] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor({ ...MEMBER, role: 'admin' }, { addons: true }) }))
    await page.goto('/app')
    const link = page.getByTestId('pinned-nav').getByRole('link', { name: /Notes/ })
    await expect(link).toHaveClass(/nav-item--muted/)
    await link.click()
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
    await expect(page).toHaveURL(/\/app\/studio\/images\/stable%2Fdiffusion\?x=1#top$/)
  })

  test('the legacy ?tab= link of Studio still works', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio?tab=tts')
    await expect(page).toHaveURL(/\/app\/studio\/tts$/)
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
  id: 'voice', name: 'voice', title: 'Voice', icon: undefined, generation: 1, version: '0.1.0', mainPage: '/app/addons/voice/recognition',
  ui: { entry: `${VOICE_BASE}/ui/voice.js`, css: [`${VOICE_BASE}/ui/voice.css`], sdk: { min: 2, max: 2 }, pages: ['recognition', 'library', 'create', 'speech'], navigation: [], chatActions: [] },
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

test.describe('Add-on navigation: the Voice recognition page', () => {
  test('the Voice add-on draws the same page, test ids and tabs as the core page', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    await asAdmin(page)
    await serveVoice(page)
    await mockIdentity(page)
    await page.goto('/app/addons/voice/recognition')
    await expectVoicesPage(page)
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
    await expect(page).toHaveURL(/\/app\/addons\/voice\/recognition\?model=org%2Fname$/)
    await expect(page.getByTestId('voices-page')).toBeVisible()
  })

  test('a viewer without voice_recognition gets the off notice in the add-on', async ({ page }) => {
    await setup(page, { apps: [voiceApp()] })
    // An administrator holds every feature, so the viewer is a member.
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, voice_recognition: false }) }))
    await serveVoice(page)
    await page.goto('/app/addons/voice/recognition')
    await expect(page.getByTestId('voice-off')).toBeVisible()
    await expect(page.getByTestId('voices-page')).toHaveCount(0)
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
    await page.goto('/app/addons/voice/recognition')
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
// spec builds it once per run with the builder of the add-on.
const FT_DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons/fine-tuning/ui/dist')
const FACE_DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../addons/face/ui/dist')
const FACE_BASE = `/api/addons/face/assets/${'e'.repeat(64)}`

test.beforeAll(async () => {
  const { build } = await import('../../../../addons/face/ui/build.mjs')
  await build()
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

async function expectFacesPage(page) {
  await expect(page.getByTestId('faces-page')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Faces', level: 1 })).toBeVisible()
}

test.describe('Add-on navigation: the Face recognition page', () => {
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
