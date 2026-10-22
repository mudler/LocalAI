import { test, expect } from '@playwright/test'
import { ADMIN, MEMBER, HEADLESS, app, installedAddon, release, sessionFor, mockAddons, ok, fail } from './addons-fixtures.js'

// The add-ons page (src/pages/Addons.jsx): Apps for everyone, Browse,
// Installed and Libraries for administrators. The server is the authority for
// every answer, so these specs drive the page against a stand-in that gives
// the same refusals the real routes give, and assert what the page sent.

const admin = { canStart: false, canStop: true, managementUrl: '/app/addons?manage=notes' }
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

async function signIn(page, user = ADMIN, permissions = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(user, permissions) }))
}

const shots = process.env.ADDONS_SHOTS
async function shoot(page, name) {
  if (!shots) return
  // Capture the settled page, not a frame of the fade-in.
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true })
}

const tab = (page, name) => page.getByRole('tab', { name })
// The start, stop and tool controls of an add-on live in the detail sheet of the drawer.
const detail = (page, id) => page.getByTestId('addon-detail').locator(`[data-app="${id}"]`)

test.describe('Add-ons: apps', () => {
  test('apps: lists enabled apps with state chips, the icon route and Open only for a main page', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await mockAddons(page, { apps: [app(), HEADLESS, app({ id: 'broken', name: 'broken', title: 'Broken', state: 'failed', icon: undefined, mainPage: undefined, hasUI: true })] })
    await page.goto('/app/addons')
    const notes = page.locator('[data-app="notes"]')
    await expect(notes.locator('.ad-tile__title')).toHaveText('Notes')
    await expect(notes.locator('.ad-tile__state')).toHaveText('Running')
    await expect(notes.getByRole('link')).toHaveAttribute('href', '/app/addons/notes/home')
    await expect(notes.locator('img.ad-icon')).toHaveAttribute('src', '/api/addons/apps/notes/icon?generation=3')
    await expect(page.locator('[data-app="broken"] .ad-tile__state')).toHaveText('Stopped, failed')
    await expect(page.locator('[data-app="broken"]').getByRole('link')).toHaveCount(0)
    await expect(page.locator('[data-app="indexer"]').getByRole('link')).toHaveCount(0)
    // Every image is served by the host itself, never from another origin.
    const sources = await page.locator('.ad-tiles img').evaluateAll(imgs => imgs.map(i => new URL(i.src).origin))
    expect(new Set(sources)).toEqual(new Set([new URL(page.url()).origin]))
    await shoot(page, 'apps-wide')
  })

  test('apps: a headless add-on shows its tools and no Open action', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await mockAddons(page, { apps: [HEADLESS] })
    await page.goto('/app/addons?detail=indexer')
    const card = detail(page, 'indexer')
    await expect(card.getByTestId('app-tools')).toContainText('3 tools')
    await expect(card.getByTestId('app-tools')).toContainText('query, export, reindex')
    await expect(card.getByText('Runs in the background')).toBeVisible()
    await expect(card.getByRole('link')).toHaveCount(0)
  })

  test('apps: a reader gets no start, stop, manage or management tabs', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await mockAddons(page)
    await page.goto('/app/addons?tab=libraries')
    await expect(page.locator('[data-app="notes"]')).toBeVisible()
    await expect(page.getByRole('tablist')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /^(Start|Stop)$/ })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Manage' })).toHaveCount(0)
    await expect(page.getByText('Add a library')).toHaveCount(0)
    // The detail sheet offers the reader nothing to start, stop or manage either.
    await page.goto('/app/addons?detail=notes')
    await expect(detail(page, 'notes')).toBeVisible()
    await expect(page.getByRole('button', { name: /^(Start|Stop)$/ })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Manage' })).toHaveCount(0)
  })

  test('apps: an administrator starts an app with a fresh operation ID and the listed generation', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, { apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 4, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })] })
    await page.goto('/app/addons?detail=indexer')
    const card = detail(page, 'indexer')
    await expect(card.getByRole('img', { name: 'State: Stopped' })).toBeVisible()
    await expect(card.getByRole('link', { name: 'Manage' })).toHaveAttribute('href', '/app/addons?manage=indexer')
    await card.getByRole('button', { name: 'Start' }).click()
    await expect(card.getByRole('img', { name: 'State: Running' })).toBeVisible()
    expect(state.log.start).toHaveLength(1)
    expect(state.log.start[0].generation).toBe(4)
    expect(state.log.start[0].operationId).toMatch(UUID)
    expect(state.log.stop).toHaveLength(0)
  })

  test('apps: a lost start is read with the same operation ID and never submitted again', async ({ page }) => {
    await signIn(page, ADMIN)
    let reads = 0
    const state = await mockAddons(page, {
      apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 2, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })],
      start: (body) => ({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { type: 'outcome_unknown', message: 'unknown', operationId: body.operationId } }) }),
      dispatch: (body, st) => {
        if (body.action !== 'operation-status') return null
        reads += 1
        if (reads >= 2) { st.apps[0].state = 'running'; st.apps[0].enabled = true }
        return ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: reads >= 2 ? 'completed' : 'prepared', Generation: 2 })
      },
    })
    await page.goto('/app/addons?detail=indexer')
    await detail(page, 'indexer').getByRole('button', { name: 'Start' }).click()
    await expect.poll(() => reads, { timeout: 15_000 }).toBeGreaterThanOrEqual(2)
    expect(state.log.start).toHaveLength(1)
    const ids = new Set([state.log.start[0].operationId, ...state.log.dispatch.filter(d => d.action === 'operation-status').map(d => d.operationId)])
    expect(ids.size).toBe(1)
  })

  // The owner answers a replayed operation ID with the old status and applies
  // nothing. This stand-in does the same, so a reused ID is a visible no-op.
  function replayingOwner(state) {
    const done = new Map()
    const handler = (verb) => (body) => {
      const known = done.get(body.operationId)
      if (known) return ok({ OperationID: body.operationId, AddonID: body.id, Phase: 'completed', Generation: body.generation })
      done.set(body.operationId, verb)
      return null
    }
    return { start: handler('start'), stop: handler('stop'), done, state }
  }

  test('apps: an unknown start is resolved or dismissed before the next action and never replays its ID', async ({ page }) => {
    test.setTimeout(90_000)
    await signIn(page, ADMIN)
    let reads = 0
    let lose = true
    const owner = replayingOwner(null)
    const state = await mockAddons(page, {
      apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 2, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })],
      start: (body) => {
        const replay = owner.start(body)
        if (replay) return replay
        if (lose) {
          // The owner applies the start but the answer is lost.
          const a = state.apps[0]
          a.enabled = true; a.state = 'running'; a.admin = { canStart: false, canStop: true, managementUrl: a.admin.managementUrl }
          return { status: 503, contentType: 'application/json', body: JSON.stringify({ error: { type: 'outcome_unknown', message: 'unknown', operationId: body.operationId } }) }
        }
        return null
      },
      stop: (body) => owner.stop(body),
      dispatch: (body) => {
        if (body.action !== 'operation-status') return null
        reads += 1
        // Polling is exhausted first; an explicit check later finds it done.
        return ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: lose && reads <= 6 ? 'prepared' : 'completed', Generation: 2 })
      },
    })
    await page.goto('/app/addons?detail=indexer')
    const card = detail(page, 'indexer')
    await card.getByRole('button', { name: 'Start' }).click()
    const notice = card.getByTestId('unknown-outcome')
    await expect(notice).toBeVisible({ timeout: 40_000 })
    expect(state.log.start).toHaveLength(1)
    const firstId = state.log.start[0].operationId
    lose = false
    // The list now shows the app running, but a new action on it is blocked
    // until the unknown one is read or dismissed.
    await expect(card.getByRole('button', { name: 'Stop' })).toBeVisible({ timeout: 15_000 })
    await expect(card.getByRole('button', { name: 'Stop' })).toBeDisabled()
    expect(state.log.stop).toHaveLength(0)
    await notice.getByRole('button', { name: 'Check status' }).click()
    await expect(notice).toHaveCount(0)
    expect(state.log.start).toHaveLength(1)
    // Stop is a new operation.
    await card.getByRole('button', { name: 'Stop' }).click()
    await expect.poll(() => state.log.stop.length).toBe(1)
    expect(state.log.stop[0].operationId).toMatch(UUID)
    expect(state.log.stop[0].operationId).not.toBe(firstId)
    await expect(card.getByRole('button', { name: 'Start' })).toBeVisible()
    // Start again: a fresh ID, applied by the owner, not replayed as a no-op.
    await card.getByRole('button', { name: 'Start' }).click()
    await expect.poll(() => state.log.start.length).toBe(2)
    expect(state.log.start[1].operationId).not.toBe(firstId)
    await expect(card.getByRole('img', { name: 'State: Running' })).toBeVisible()
    await expect(card.getByRole('alert')).toHaveCount(0)
    expect(state.apps[0].enabled).toBe(true)
  })

  test('apps: dismissing an unknown outcome lets the next click mint a new operation', async ({ page }) => {
    test.setTimeout(90_000)
    await signIn(page, ADMIN)
    const state = await mockAddons(page, {
      apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 2, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })],
      start: (body, n) => (n === 1
        ? { status: 503, contentType: 'application/json', body: JSON.stringify({ error: { type: 'outcome_unknown', message: 'unknown', operationId: body.operationId } }) }
        : null),
      dispatch: (body) => (body.action === 'operation-status' ? ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: 'prepared', Generation: 2 }) : null),
    })
    await page.goto('/app/addons?detail=indexer')
    const card = detail(page, 'indexer')
    await card.getByRole('button', { name: 'Start' }).click()
    const notice = card.getByTestId('unknown-outcome')
    await expect(notice).toBeVisible({ timeout: 40_000 })
    await expect(card.getByRole('button', { name: 'Start' })).toBeDisabled()
    await notice.getByRole('button', { name: 'Dismiss' }).click()
    await expect(notice).toHaveCount(0)
    await card.getByRole('button', { name: 'Start' }).click()
    await expect.poll(() => state.log.start.length).toBe(2)
    expect(state.log.start[1].operationId).not.toBe(state.log.start[0].operationId)
  })

  test('apps: a completed status that left the app unchanged is not shown as done', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page, {
      apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 2, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })],
      // A replay answers completed and applies nothing.
      start: (body) => ok({ OperationID: body.operationId, AddonID: 'indexer', Phase: 'completed', Generation: 2 }),
    })
    await page.goto('/app/addons?detail=indexer')
    const card = detail(page, 'indexer')
    await card.getByRole('button', { name: 'Start' }).click()
    await expect(card.getByRole('alert')).toContainText('did not change')
    await expect(card.getByRole('img', { name: 'State: Stopped' })).toBeVisible()
  })

  test('apps: a refused start shows the server denial and refreshes the list', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, {
      apps: [app({ id: 'indexer', title: 'Indexer', enabled: false, state: 'stopped', generation: 2, mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=indexer' } })],
      start: () => fail(403, 'addon_error'),
    })
    await page.goto('/app/addons?detail=indexer')
    const before = state.log.appsList
    await detail(page, 'indexer').getByRole('button', { name: 'Start' }).click()
    await expect(page.getByRole('alert').filter({ hasText: 'The server refused this' })).toBeVisible()
    await expect.poll(() => state.log.appsList).toBeGreaterThan(before)
  })

  test('apps: the list refreshes on a bounded interval, backs off on errors and stops when left', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await page.clock.install()
    const state = await mockAddons(page, { appsList: (n) => (n === 2 || n === 3 ? fail(503, 'addon_error') : null) })
    await page.goto('/app/addons')
    await expect(page.locator('[data-app="notes"]')).toBeVisible()
    expect(state.log.appsList).toBe(1)
    // From here only runFor moves the page clock, so a slow machine cannot make
    // a refresh come early.
    await page.clock.pauseAt((await page.evaluate(() => Date.now())) + 50)
    await page.clock.runFor(4_900)
    expect(state.log.appsList).toBe(1)
    await page.clock.runFor(200)
    await expect.poll(() => state.log.appsList).toBe(2)
    // The failure doubles the wait, and the old list stays visible and flagged.
    await expect(page.getByText('may be out of date')).toBeVisible()
    await page.clock.runFor(9_000)
    expect(state.log.appsList).toBe(2)
    await page.clock.runFor(1_500)
    await expect.poll(() => state.log.appsList).toBe(3)
    await page.clock.runFor(21_000)
    await expect.poll(() => state.log.appsList).toBe(4)
    await expect(page.getByText('may be out of date')).toHaveCount(0)
    // Leaving the page stops the requests.
    await page.evaluate(() => { window.history.pushState({}, '', '/app/chat'); window.dispatchEvent(new PopStateEvent('popstate')) })
    await expect(page.locator('[data-app="notes"]')).toHaveCount(0)
    const seen = state.log.appsList
    await page.clock.runFor(180_000)
    expect(state.log.appsList).toBe(seen)
  })
})

test.describe('Add-ons: registry polling', () => {
  const leave = (page) => page.evaluate(() => { window.history.pushState({}, '', '/app/chat'); window.dispatchEvent(new PopStateEvent('popstate')) })
  const contributing = (over = {}) => app({ ui: { entry: '/api/addons/notes/assets/' + 'c'.repeat(64) + '/ui.js', css: [], sdk: { min: 1, max: 2 }, pages: ['home'], navigation: ['home'] }, ...over })

  test('apps: an add-on that contributes UI keeps the list current off the page and its disable stops the reads', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await page.clock.install()
    const state = await mockAddons(page, { apps: [contributing()] })
    await page.goto('/app/addons')
    await expect(page.locator('[data-app="notes"]')).toBeVisible()
    await leave(page)
    await expect(page.locator('[data-app="notes"]')).toHaveCount(0)
    await page.clock.pauseAt((await page.evaluate(() => Date.now())) + 50)
    await expect.poll(() => state.log.appsList).toBeGreaterThanOrEqual(1)
    // Reads continue, one per interval and no faster. The clock only moves
    // by runFor, and each step waits for its answer before the next one.
    const before = state.log.appsList
    await page.clock.runFor(4_000)
    await page.waitForTimeout(150)
    expect(state.log.appsList).toBe(before)
    await page.clock.runFor(1_100)
    await expect.poll(() => state.log.appsList).toBe(before + 1)
    for (let n = 2; n <= 4; n++) {
      await page.clock.runFor(5_100)
      await expect.poll(() => state.log.appsList).toBe(before + n)
    }
    // The owner disables it: the next list withdraws the contribution, and
    // with nothing left to keep current the reads stop.
    state.apps[0].enabled = false
    state.apps[0].state = 'stopped'
    await page.clock.runFor(5_100)
    await expect.poll(() => state.log.appsList).toBe(before + 5)
    await page.waitForTimeout(150)
    await page.clock.runFor(180_000)
    await page.waitForTimeout(300)
    expect(state.log.appsList).toBe(before + 5)
  })

  test('apps: a viewer without the add-ons feature never requests the list', async ({ page }) => {
    await signIn(page, MEMBER, {})
    await page.clock.install()
    const state = await mockAddons(page, { apps: [contributing()] })
    await page.goto('/app/chat')
    await page.clock.runFor(120_000)
    await page.goto('/app/addons')
    await page.clock.runFor(120_000)
    expect(state.log.appsList).toBe(0)
  })
})

test.describe('Add-ons: browse', () => {
  test('browse: searches the libraries, pages and shows trust and platform facts', async ({ page }) => {
    await signIn(page, ADMIN)
    const many = Array.from({ length: 30 }, (_, i) => release(`addon-${String(i).padStart(2, '0')}`))
    const state = await mockAddons(page, { releases: [release('tables', { policy: { requireSignature: true, issuer: 'https://issuer.example', identity: 'release@catalog.example' } }), ...many],
      sources: [
        { id: 'main', name: 'Main', url: 'https://catalog.example/releases.json', enabled: true, policy: { requireSignature: false } },
        { id: 'team', name: 'Team', url: 'https://team.example/releases.json', enabled: true, policy: { requireSignature: false } },
      ] })
    await page.goto('/app/addons?tab=browse')
    await expect(page.locator('.cat-item')).toHaveCount(24)
    await expect(page.getByText('31 results')).toBeVisible()
    const tables = page.locator('.cat-item', { hasText: 'Tables' }).first()
    await expect(tables.getByText('Signature required')).toBeVisible()
    await expect(tables.getByText('linux/amd64')).toBeVisible()
    await expect(page.getByRole('group', { name: 'Libraries' }).getByRole('button', { name: 'Team' })).toBeVisible()
    await page.getByRole('button', { name: 'Next page' }).click()
    await expect(page).toHaveURL(/page=2/)
    await expect(page.locator('.cat-item')).toHaveCount(7)
    expect(state.log.dispatch.filter(d => d.action === 'catalog-search').at(-1).arguments.offset).toBe(24)
    await page.getByRole('searchbox').or(page.getByLabel('Search the libraries')).first().fill('tables')
    await expect(page.locator('.cat-item')).toHaveCount(1)
    await expect(page).toHaveURL(/q=tables/)
    expect(state.log.dispatch.filter(d => d.action === 'catalog-search').at(-1).arguments.text).toBe('tables')
    await shoot(page, 'browse')
  })

  test('browse: installing asks first and leaves the add-on disabled until it is enabled', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, { installed: [], releases: [release('tables', { name: 'Tables' })] })
    await page.goto('/app/addons?tab=browse')
    const row = page.locator('.cat-item', { hasText: 'Tables' })
    await row.getByRole('button', { name: 'Install' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Install Tables?')
    await expect(dialog).toContainText('does not start the add-on')
    await expect(dialog).toContainText('sha256:aaaaaaaaaaaa')
    expect(state.log.dispatch.filter(d => d.action === 'install')).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Install' }).click()
    await expect(row.getByText('It is not enabled')).toBeVisible()
    const install = state.log.dispatch.filter(d => d.action === 'install')
    expect(install).toHaveLength(1)
    expect(install[0].operationId).toMatch(UUID)
    expect(install[0].arguments).toEqual({ sourceId: 'main', version: '1.0.0' })
    expect(state.log.dispatch.filter(d => d.action === 'enable')).toHaveLength(0)
    // Activation is its own step, on the Installed tab.
    await tab(page, 'Installed').click()
    const installed = page.locator('[data-addon="tables"]')
    await expect(installed.getByText('Disabled')).toBeVisible()
    await installed.getByRole('button', { name: 'Enable' }).click()
    await expect(installed.getByText('Enabled', { exact: true })).toBeVisible()
    expect(state.log.dispatch.filter(d => d.action === 'enable')).toHaveLength(1)
    expect(state.log.dispatch.find(d => d.action === 'enable').generation).toBe(1)
  })
})

test.describe('Add-ons: installed', () => {
  test('installed: health is separate from enabled and the details show trust, compatibility and storage', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page, { installed: [installedAddon({ state: 'failed', restarts: 3, verification: 'sha256+sigstore' })] })
    await page.goto('/app/addons?tab=installed')
    const row = page.locator('[data-addon="notes"]')
    await expect(row.getByText('Enabled', { exact: true })).toBeVisible()
    await expect(row.getByRole('img', { name: 'State: Failed' })).toBeVisible()
    await row.getByRole('button', { name: 'Details' }).click()
    const sheet = page.getByTestId('addon-manage')
    await expect(sheet.getByText('Wanted state')).toBeVisible()
    await expect(sheet.getByRole('img', { name: 'State: Failed' })).toBeVisible()
    await expect(sheet.getByText('after 3 restart attempts')).toBeVisible()
    await expect(sheet.getByText('Signed and digest checked')).toBeVisible()
    // The trust section and the update section both name the installed digest.
    await expect(sheet.getByText('sha256:0123456789ab').first()).toBeVisible()
    await expect(sheet.getByTestId('manage-updates').getByText('sha256:0123456789ab')).toBeVisible()
    await expect(sheet.locator('dt', { hasText: 'Host API' }).locator('xpath=following-sibling::dd[1]')).toHaveText('1-2')
    await expect(sheet.getByText('12 KiB')).toBeVisible()
    await expect(sheet.getByTestId('update-state')).toHaveAttribute('data-state', 'unknown')
    await expect(sheet.getByText('not collected in this release')).toBeVisible()
    await shoot(page, 'manage')
  })

  test('installed: an update is shown only from the owner update field and upgrade sends that version', async ({ page }) => {
    await signIn(page, ADMIN)
    const candidate = 'sha256:' + 'b'.repeat(64)
    const state = await mockAddons(page, { installed: [
      installedAddon({ update: { available: true, state: 'update-available', version: '1.3.0', digest: candidate, checkedAt: '2026-10-01T00:00:00Z' } }),
      installedAddon({ id: 'plain', title: 'Plain', version: '9.9.9' }),
    ] })
    await page.goto('/app/addons?tab=installed')
    await expect(page.locator('[data-addon="notes"]').getByText('Update 1.3.0')).toBeVisible()
    await expect(page.locator('[data-addon="plain"]').getByText(/Update/)).toHaveCount(0)
    await page.locator('[data-addon="notes"]').getByRole('button', { name: 'Details' }).click()
    await page.getByTestId('addon-manage').getByRole('button', { name: 'Upgrade' }).click()
    // The confirmation names the version and digest the owner resolved and
    // nothing is sent until it is confirmed.
    const dialog = page.getByTestId('upgrade-confirm')
    await expect(dialog).toContainText('1.3.0')
    await expect(dialog).toContainText('sha256:bbbbbbbbbbbb')
    expect(state.log.upgrade).toHaveLength(0)
    await page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect.poll(() => state.log.upgrade.length).toBe(1)
    const sent = state.log.upgrade[0]
    expect(sent.id).toBe('notes')
    expect(sent.expectedDigest).toBe('sha256:0123456789abcdef0123456789abcdef')
    expect(sent.candidateDigest).toBe(candidate)
    expect(sent.operationId).toMatch(UUID)
    expect(sent.confirm).toBe(sent.operationId)
    expect(state.log.dispatch.filter(d => d.action === 'upgrade')).toHaveLength(0)
    // An add-on without the field says the status is unknown and has no Upgrade.
    await page.goto('/app/addons?tab=installed&manage=plain')
    const sheet = page.getByTestId('addon-manage')
    await expect(sheet.getByText('The update status is not known')).toBeVisible()
    await expect(sheet.getByRole('button', { name: 'Upgrade' })).toHaveCount(0)
  })

  test('installed: uninstalling warns that data is kept before anything is submitted', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page)
    await page.goto('/app/addons?tab=installed&manage=notes')
    await page.getByTestId('addon-manage').getByRole('button', { name: 'Uninstall' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('data, identity and grants are kept')
    expect(state.log.dispatch.filter(d => d.action === 'uninstall')).toHaveLength(0)
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(state.log.dispatch.filter(d => d.action === 'uninstall')).toHaveLength(0)
    await page.getByTestId('addon-manage').getByRole('button', { name: 'Uninstall' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Uninstall' }).click()
    await expect.poll(() => state.log.dispatch.filter(d => d.action === 'uninstall').length).toBe(1)
  })

  test('installed: Escape cancels the uninstall dialog only, then closes the sheet, and focus returns', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page)
    await page.goto('/app/addons?tab=installed')
    const manage = page.locator('[data-addon="notes"]').getByRole('button', { name: 'Details' })
    await manage.click()
    const sheet = page.getByTestId('addon-manage')
    await expect(sheet).toBeVisible()
    const uninstall = sheet.getByRole('button', { name: 'Uninstall' })
    await uninstall.focus()
    await page.keyboard.press('Enter')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(sheet).toBeVisible()
    await expect(uninstall).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    await expect(manage).toBeFocused()
  })

  test('installed: an unknown enable blocks other actions on the add-on until it is read', async ({ page }) => {
    test.setTimeout(90_000)
    await signIn(page, ADMIN)
    let phase = 'prepared'
    const state = await mockAddons(page, {
      installed: [installedAddon({ enabled: false, state: 'stopped' })],
      dispatch: (body, st) => {
        if (body.action === 'enable' && st.log.dispatch.filter(d => d.action === 'enable').length === 1) {
          return fail(503, 'outcome_unknown', { operationId: body.operationId })
        }
        if (body.action === 'operation-status') return ok({ OperationID: body.operationId, AddonID: body.addonId, Phase: phase, Generation: 3 })
        return null
      },
    })
    await page.goto('/app/addons?tab=installed')
    const row = page.locator('[data-addon="notes"]')
    await row.getByRole('button', { name: 'Enable' }).click()
    const notice = row.getByTestId('unknown-outcome')
    await expect(notice).toBeVisible({ timeout: 40_000 })
    await expect(row.getByRole('button', { name: 'Enable' })).toBeDisabled()
    phase = 'failed'
    await notice.getByRole('button', { name: 'Check status' }).click()
    await expect(notice).toHaveCount(0)
    await expect(row.getByRole('alert')).toContainText('did not complete')
    // The failed operation is over: a new click is a new operation.
    await row.getByRole('button', { name: 'Enable' }).click()
    await expect.poll(() => state.log.dispatch.filter(d => d.action === 'enable').length).toBe(2)
    const ids = state.log.dispatch.filter(d => d.action === 'enable').map(d => d.operationId)
    expect(ids[1]).not.toBe(ids[0])
  })

  test('installed: a headless add-on keeps its details, health and tools', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page, { installed: [installedAddon({ id: 'indexer', title: 'indexer', hasUI: false, mainPage: undefined, icon: undefined, tools: ['query', 'export'], toolCount: 2, state: 'running' })] })
    await page.goto('/app/addons?tab=installed&manage=indexer')
    const sheet = page.getByTestId('addon-manage')
    await expect(sheet.getByText('Headless, no pages')).toBeVisible()
    await expect(sheet.getByText('2: query, export')).toBeVisible()
    await expect(sheet.getByRole('img', { name: 'State: Running' })).toBeVisible()
    await expect(sheet.getByRole('link', { name: 'Open' })).toHaveCount(0)
  })

  test('installed: a refusal from the server is shown even though the tab was offered', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page, { dispatch: (body) => (body.action === 'addons-installed' ? fail(403, 'addon_error') : null) })
    await page.goto('/app/addons?tab=installed')
    await expect(page.getByRole('alert').filter({ hasText: 'The server refused this' })).toBeVisible()
    await expect(page.locator('[data-addon]')).toHaveCount(0)
  })
})

test.describe('Add-ons: libraries', () => {
  test('libraries: a private library is saved by credential reference and never carries a secret', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page)
    await page.goto('/app/addons?tab=libraries')
    await page.getByRole('button', { name: 'Add a library' }).click()
    const sheet = page.getByTestId('source-editor')
    await expect(sheet.locator('input[type="password"]')).toHaveCount(0)
    await sheet.getByLabel('Name').fill('Team library')
    await expect(sheet.getByLabel('Identifier')).toHaveValue('team-library')
    await sheet.getByLabel('Catalog URL').fill('https://team.example/releases.json')
    // A pasted secret is refused before anything is sent.
    await sheet.getByLabel('Credential reference (optional)').fill('ghp_Hunter2Hunter2Hunter2')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(sheet.getByText('does not look like a reference name')).toBeVisible()
    expect(state.log.dispatch.filter(d => d.action === 'catalog-source-save')).toHaveLength(0)
    await sheet.getByLabel('Credential reference (optional)').fill('team-token')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.locator('[data-source="team-library"]')).toBeVisible()
    const saved = state.log.dispatch.filter(d => d.action === 'catalog-source-save')
    expect(saved).toHaveLength(1)
    expect(saved[0].arguments).toEqual({ id: 'team-library', name: 'Team library', url: 'https://team.example/releases.json', enabled: true, policy: { requireSignature: false }, authReference: 'team-token' })
    expect(JSON.stringify(saved[0])).not.toMatch(/hunter2/i)
    await expect(page.locator('[data-source="team-library"]')).toContainText('Credential reference: team-token')
  })

  test('libraries: removing a library says installed add-ons stay and leaves them installed', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page)
    await page.goto('/app/addons?tab=libraries')
    await page.locator('[data-source="main"]').getByRole('button', { name: 'Remove' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('stay installed and keep running')
    await dialog.getByRole('button', { name: 'Remove' }).click()
    await expect(page.locator('[data-source="main"]')).toHaveCount(0)
    const removal = state.log.dispatch.find(d => d.action === 'catalog-source-save')
    expect(removal.arguments.removed).toBe(true)
    expect(removal.arguments.id).toBe('main')
    await tab(page, 'Installed').click()
    await expect(page.locator('[data-addon="notes"]')).toBeVisible()
    expect(state.installed).toHaveLength(1)
  })

  test('libraries: a refresh reports its result and a failed fetch is shown as failed', async ({ page }) => {
    await signIn(page, ADMIN)
    const failing = { id: 'broken', name: 'Broken', url: 'https://broken.example/releases.json', enabled: true, policy: { requireSignature: false }, refresh: { checkedAt: new Date().toISOString(), lastSuccess: '0001-01-01T00:00:00Z', count: 0, failed: true } }
    const state = await mockAddons(page, {
      sources: [failing],
      dispatch: (body) => (body.action === 'catalog-source-refresh'
        ? { status: 503, contentType: 'application/json', body: JSON.stringify({ error: { type: 'outcome_unknown', message: 'unknown', operationId: body.operationId } }) }
        : null),
    })
    await page.goto('/app/addons?tab=libraries')
    const row = page.locator('[data-source="broken"]')
    await expect(row.getByText('Last refresh failed')).toBeVisible()
    await row.getByRole('button', { name: 'Refresh' }).click()
    await expect(page.getByRole('alert').filter({ hasText: 'could not be confirmed' })).toBeVisible()
    expect(state.log.dispatch.filter(d => d.action === 'catalog-source-refresh')).toHaveLength(1)
  })
})

test.describe('Add-ons: access, keyboard, layout and language', () => {
  test('keyboard: the tab list moves with the arrow keys and actions are reachable', async ({ page }) => {
    await signIn(page, ADMIN)
    const state = await mockAddons(page, { apps: [app({ admin })] })
    await page.goto('/app/addons')
    await tab(page, 'Apps').focus()
    await page.keyboard.press('ArrowRight')
    await expect(tab(page, 'Browse')).toBeFocused()
    await expect(tab(page, 'Browse')).toHaveAttribute('aria-selected', 'true')
    await expect(page).toHaveURL(/tab=browse/)
    await page.keyboard.press('End')
    await expect(tab(page, 'Libraries')).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Home')
    await expect(tab(page, 'Apps')).toHaveAttribute('aria-selected', 'true')
    // The selected tab is the only one in the tab order.
    await expect(tab(page, 'Browse')).toHaveAttribute('tabindex', '-1')
    // Stop is reachable and operable with the keyboard alone, in the detail sheet.
    await page.goto('/app/addons?detail=notes')
    const stop = detail(page, 'notes').getByRole('button', { name: 'Stop' })
    await stop.focus()
    await expect(stop).toBeFocused()
    await page.keyboard.press('Enter')
    await expect.poll(() => state.log.stop.length).toBe(1)
  })

  test('narrow: at 400 px the apps page is one column without sideways scrolling', async ({ page }) => {
    await page.setViewportSize({ width: 400, height: 800 })
    await signIn(page, ADMIN)
    await mockAddons(page, { apps: [app({ admin }), HEADLESS, app({ id: 'longer-identifier-for-wrapping', title: 'A fairly long add-on title that has to wrap', description: 'A description that is long enough to need several lines on a narrow screen without breaking out of its card.', admin })] })
    await page.goto('/app/addons')
    await expect(page.locator('[data-app="notes"]')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    const lefts = await page.locator('.ad-tile').evaluateAll(cards => cards.map(c => Math.round(c.getBoundingClientRect().left)))
    expect(new Set(lefts).size).toBe(1)
    await shoot(page, 'apps-narrow')
    await page.goto('/app/addons?tab=installed&manage=notes')
    await expect(page.getByTestId('addon-manage')).toBeVisible()
    const sheetOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(sheetOverflow).toBeLessThanOrEqual(0)
    await shoot(page, 'manage-narrow')
  })

  test('theme: the apps page follows the light and dark tokens', async ({ page }) => {
    await signIn(page, MEMBER, { addons: true })
    await mockAddons(page)
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'light'))
    await page.goto('/app/addons')
    await page.goto('/app/addons?detail=notes')
    const read = () => page.locator('.ad-card').first().evaluate(el => {
      const css = getComputedStyle(el)
      const chip = getComputedStyle(el.querySelector('.ad-chip'))
      return { card: css.backgroundColor, chip: chip.color, chipBg: chip.backgroundColor }
    })
    const light = await read()
    await shoot(page, 'apps-light')
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'))
    await expect.poll(async () => (await read()).card).not.toBe(light.card)
    const dark = await read()
    expect(dark.chipBg).not.toBe('')
    expect(dark.chip).not.toBe(dark.chipBg)
    await shoot(page, 'apps-dark')
  })

  test('i18n: the page follows the interface language', async ({ page }) => {
    await signIn(page, ADMIN)
    await mockAddons(page)
    await page.addInitScript(() => localStorage.setItem('localai-language', 'it'))
    await page.goto('/app/addons')
    await expect(page.getByRole('tab', { name: 'Sfoglia' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Librerie' })).toBeVisible()
    await page.goto('/app/addons?detail=notes')
    await expect(detail(page, 'notes').getByRole('img', { name: 'Stato: In esecuzione' })).toBeVisible()
  })

  test('hub: under the legacy switch the Add-ons tab appears only for an account that holds the feature', async ({ page }) => {
    await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: false } }))
    await signIn(page, MEMBER, { chat: true })
    await mockAddons(page)
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="addons"]')).toHaveCount(0)
    await page.unroute('**/api/auth/status')
    await signIn(page, MEMBER, { chat: true, addons: true })
    await page.goto('/app/build')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="addons"]')).toHaveAttribute('href', '/app/addons')
    await page.locator('.dk-hubtabs [data-hub-tab="addons"]').click()
    await expect(page.locator('.dk-hubtabs [data-hub-tab="addons"]')).toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Add-ons: shared catalog components keep the backends gallery', () => {
  test('regression: the backends catalog still searches, pages and shows its empty state', async ({ page }) => {
    const backends = Array.from({ length: 70 }, (_, i) => ({ name: `backend-${String(i).padStart(2, '0')}`, description: 'A backend', installed: false, tags: ['chat'] }))
    await page.route('**/api/backends*', route => {
      const term = new URL(route.request().url()).searchParams.get('term') || ''
      return route.fulfill({ json: { backends: term === 'nothing-here' ? [] : backends } })
    })
    await page.goto('/app/backends')
    await expect(page.locator('[data-entity="backend-00"]')).toBeVisible({ timeout: 10_000 })
    const pager = page.locator('.bk-pager')
    await expect(pager).toContainText('1 / 2')
    await expect(pager).toHaveClass(/cat-pager/)
    await pager.getByRole('button').last().click()
    await expect(pager).toContainText('2 / 2')
    await expect(page.locator('[data-entity="backend-60"]')).toBeVisible()
    await expect(page.locator('.bk-search.dk-input-icon input')).toBeVisible()
    await page.getByPlaceholder(/search backends/i).fill('nothing-here')
    await expect(page.locator('.dk-empty')).toBeVisible()
    await expect(page.locator('.dk-empty .dk-empty-title')).toBeVisible()
  })
})
