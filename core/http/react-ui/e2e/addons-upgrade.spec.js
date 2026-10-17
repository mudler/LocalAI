import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'

// Update detection and the explicit upgrade of the Tables add-on, seen from the
// browser. One UI test server runs the real add-on owner, the real Tables
// executable and the real built UI package with named accounts and real
// sessions, against a library whose "development" channel the spec moves: each
// move keeps the version label and changes the bytes. The page asks, the owner
// decides: nothing here mocks a route. The specs change the owner's state and
// run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const shots = process.env.ADDONS_SHOTS
const MANAGE = '/app/addons?tab=installed&manage=tables'

async function shoot(page, name, target, { settle = 350 } = {}) {
  if (!shots) return
  const el = target ? page.getByTestId(target).first() : null
  if (el && await el.count()) await el.scrollIntoViewIfNeeded()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  if (settle) await page.waitForTimeout(settle)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false })
}

async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  return { context, page }
}

async function apiAs(who) {
  return playwrightRequest.newContext({ baseURL: BASE, extraHTTPHeaders: { Authorization: `Bearer ${creds.users[who].token}` } })
}

// A fetch made by the page itself: a real same-origin request with the session
// cookie and the browser's own Sec-Fetch metadata.
async function pageFetch(page, path, { method = 'GET', body, headers = {} } = {}) {
  return page.evaluate(async ({ path, method, body, headers }) => {
    const r = await fetch(path, {
      method, credentials: 'same-origin',
      headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    let json = null
    try { json = await r.json() } catch { json = null }
    return { status: r.status, json }
  }, { path, method, body, headers })
}

// The library's control routes: move the channel on, take the library away.
async function control(route) {
  const r = await fetch(`${creds.control}/${route}`, { method: 'POST' })
  expect(r.status).toBe(200)
  return r.json()
}

async function installed(api) {
  const r = await api.post('/api/addons/dispatch', { data: { operationId: `op-${randomUUID()}`, action: 'addons-installed', arguments: {} } })
  expect(r.status()).toBe(200)
  return (await r.json()).payload.addons.find(a => a.id === 'tables')
}

async function openManage(page) {
  await page.goto(MANAGE)
  await expect(page.getByTestId('addon-manage')).toBeVisible({ timeout: 25_000 })
  await expect(page.getByTestId('manage-updates')).toBeVisible({ timeout: 25_000 })
}

async function check(page) {
  const before = await page.getByTestId('update-times').innerText()
  await page.getByTestId('update-check').click()
  await expect(page.getByTestId('update-check')).toBeEnabled({ timeout: 30_000 })
  return before
}

// Waits out the interval of the rate limit of the owner's refresh so that the
// next check really reads the library.
const settle = (page) => page.waitForTimeout(1_200)

test.describe.configure({ mode: 'serial' })

const state = {}

test.describe('Add-on updates and the explicit upgrade through the real owner', () => {
  test('updates: a library with nothing newer is up to date, and a member sees no update mark', async ({ browser }) => {
    const member = await browserAs(browser, 'member')
    await member.page.goto('/app/addons')
    const api = await apiAs('admin')
    const before = await installed(api)
    state.firstDigest = before.digest
    expect(before.channel).toBe('development')
    const op = `op-${randomUUID()}`
    const made = await pageFetch(member.page, '/api/addons/resources', { method: 'POST', body: { addonId: 'tables', operationId: op, generation: before.generation, arguments: { name: 'Ledger' } }, headers: { 'X-LocalAI-Confirm': op } })
    expect(made.status, JSON.stringify(made.json)).toBe(200)
    state.db = made.json.id
    state.memberPage = member.page

    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'up-to-date')
    await expect(admin.page.getByTestId('update-state')).toContainText('up to date')
    await expect(admin.page.getByTestId('update-upgrade')).toHaveCount(0)
    await expect(admin.page.getByTestId('update-times')).toContainText('Checked')
    await check(admin.page)
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'up-to-date')
    await shoot(admin.page, 'updates-up-to-date-1280', 'manage-updates')
    await expect(member.page.getByTestId('update-chip')).toHaveCount(0)
    await admin.context.close()
  })

  test('updates: a moved channel with the same version label is an update, in the list and as a pending mark', async ({ browser }) => {
    await control('step')
    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    // Nothing was fetched yet: the owner answers from what it holds.
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'up-to-date')
    await settle(admin.page)
    await check(admin.page)
    const status = admin.page.getByTestId('update-state')
    await expect(status).toHaveAttribute('data-state', 'update-available')
    await expect(status).toContainText('Version 0.1.0-dev is available')
    const candidate = admin.page.getByTestId('update-candidate')
    await expect(candidate).toContainText('0.1.0-dev')
    await expect(candidate).not.toContainText(state.firstDigest.slice(0, 19))
    await expect(admin.page.getByTestId('update-upgrade')).toBeVisible()
    await shoot(admin.page, 'updates-available-1280', 'manage-updates')
    // The installed list carries the badge.
    await admin.page.goto('/app/addons?tab=installed')
    await expect(admin.page.locator('[data-addon="tables"]').getByText('Update 0.1.0-dev')).toBeVisible({ timeout: 25_000 })
    await shoot(admin.page, 'updates-badge-1280')
    await admin.context.close()

    // A member's apps list shows the pending mark from the owner's cache, and
    // the status route gives a member the state and versions only.
    await state.memberPage.goto('/app/addons')
    const card = state.memberPage.locator('[data-app="tables"]')
    await expect(card.getByTestId('update-chip')).toBeVisible({ timeout: 25_000 })
    await expect(card.getByRole('img', { name: 'State: Running' })).toBeVisible()
    await shoot(state.memberPage, 'updates-pending-chip-1280', 'update-chip')
    const read = await pageFetch(state.memberPage, '/api/addons/tables/update')
    expect(read.status).toBe(200)
    expect(read.json.state).toBe('update-available')
    expect(read.json.candidateVersion).toBe('0.1.0-dev')
    for (const hidden of ['installedDigest', 'candidateDigest', 'sourceId', 'channel', 'selection']) expect(read.json).not.toHaveProperty(hidden)
  })

  test('updates: a member, a key and an outsider cannot check or upgrade', async ({ browser }) => {
    const member = await browserAs(browser, 'member')
    await member.page.goto('/app/addons')
    const op = `op-${randomUUID()}`
    const headers = { 'X-LocalAI-Confirm': op }
    const pins = { operationId: op, expectedDigest: state.firstDigest, candidateDigest: `sha256:${'0'.repeat(64)}` }
    for (const [path, body] of [['/api/addons/tables/update/check', { operationId: op }], ['/api/addons/tables/upgrade', pins]]) {
      const r = await pageFetch(member.page, path, { method: 'POST', body, headers })
      expect(r.status, path).toBe(403)
    }
    // An administrator's key is not the administrator's own browser session.
    const key = await apiAs('admin')
    for (const [path, body] of [['/api/addons/tables/update/check', { operationId: op }], ['/api/addons/tables/upgrade', pins]]) {
      const r = await key.post(path, { data: body, headers })
      expect(r.status(), path).toBe(403)
    }
    // The status read is for people who may see the add-on.
    const outsider = await browserAs(browser, 'outsider')
    await outsider.page.goto('/app/addons')
    expect((await pageFetch(outsider.page, '/api/addons/tables/update')).status).toBe(403)
    // A refused request left everything as it was.
    expect((await installed(key)).digest).toBe(state.firstDigest)
    await member.context.close()
    await outsider.context.close()
  })

  test('updates: the dialog names the library, versions and digests, a cancel sends nothing, and the upgrade backs up and keeps the data', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin')
    const api = await apiAs('admin')
    await openManage(admin.page)
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'update-available')
    state.candidate = (await installed(api)).update.digest
    await admin.page.getByTestId('update-upgrade').click()
    const dialog = admin.page.getByTestId('upgrade-confirm')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('development')
    await expect(dialog).toContainText(state.firstDigest.slice(0, 19))
    await expect(dialog).toContainText(state.candidate.slice(0, 19))
    await expect(dialog).toContainText('backed up before anything changes')
    await shoot(admin.page, 'updates-dialog-1280', 'upgrade-confirm')
    // Escape closes it and sends nothing.
    await admin.page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    expect((await installed(api)).digest).toBe(state.firstDigest)

    await admin.page.getByTestId('update-upgrade').click()
    await admin.page.getByRole('alertdialog').getByRole('button', { name: 'Upgrade' }).click()
    await expect(admin.page.getByTestId('upgrade-progress')).toBeVisible()
    await shoot(admin.page, 'updates-progress-1280', null, { settle: 0 })
    await expect(admin.page.getByTestId('upgrade-progress')).toBeHidden({ timeout: 90_000 })
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'up-to-date', { timeout: 30_000 })
    const now = await installed(api)
    expect(now.digest).toBe(state.candidate)
    expect(now.generation).toBe(2)
    state.secondDigest = now.digest
    await shoot(admin.page, 'updates-done-1280', 'manage-updates')
    // The add-on runs on its own data: the member's database is still there,
    // and the upgrade made a backup first.
    const list = await (await (await apiAs('member')).get('/api/addons/resources?addonId=tables')).json()
    expect(list.resources.map(r => r.id)).toContain(state.db)
    const recovery = await (await api.get('/api/addons/tables/recovery')).json()
    expect(recovery.backups.length).toBeGreaterThan(0)
    expect(recovery.backups.some(b => b.reason === 'upgrade')).toBe(true)
    await admin.context.close()
    // The pending mark is gone for the member.
    await state.memberPage.goto('/app/addons')
    await expect(state.memberPage.locator('[data-app="tables"]')).toBeVisible({ timeout: 25_000 })
    await expect(state.memberPage.getByTestId('update-chip')).toHaveCount(0)
  })

  test('updates: two requests for the same transition under different operations run it once', async ({ browser }) => {
    await control('step')
    const a = await browserAs(browser, 'admin')
    const b = await browserAs(browser, 'admin')
    await openManage(a.page)
    await settle(a.page)
    await check(a.page)
    await expect(a.page.getByTestId('update-state')).toHaveAttribute('data-state', 'update-available')
    await b.page.goto('/app/addons')
    const api = await apiAs('admin')
    const now = await installed(api)
    expect(now.update.state).toBe('update-available')
    const body = (op) => ({ operationId: op, expectedDigest: now.digest, candidateDigest: now.update.digest })
    const [one, two] = [`op-${randomUUID()}`, `op-${randomUUID()}`]
    const results = await Promise.all([
      pageFetch(a.page, '/api/addons/tables/upgrade', { method: 'POST', body: body(one), headers: { 'X-LocalAI-Confirm': one } }),
      pageFetch(b.page, '/api/addons/tables/upgrade', { method: 'POST', body: body(two), headers: { 'X-LocalAI-Confirm': two } }),
    ])
    expect(results.map(r => r.status).sort(), JSON.stringify(results)).toEqual([200, 409])
    const loser = results.find(r => r.status === 409)
    expect(loser.json.error.type).toBe('upgrade_conflict')
    const after = await installed(api)
    expect(after.generation).toBe(3)
    expect(after.digest).toBe(now.update.digest)
    state.thirdDigest = after.digest
    // The winner's operation replays and does not run again.
    const again = await pageFetch(a.page, '/api/addons/tables/upgrade', { method: 'POST', body: body(results[0].status === 200 ? one : two), headers: { 'X-LocalAI-Confirm': results[0].status === 200 ? one : two } })
    expect(again.status).toBe(200)
    expect((await installed(api)).generation).toBe(3)
    await a.context.close()
    await b.context.close()
  })

  test('updates: a page that is behind cannot upgrade over a newer installed package', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin')
    await admin.page.goto('/app/addons')
    const api = await apiAs('admin')
    const op = `op-${randomUUID()}`
    const stale = await pageFetch(admin.page, '/api/addons/tables/upgrade', { method: 'POST', body: { operationId: op, expectedDigest: state.secondDigest, candidateDigest: `sha256:${'2'.repeat(64)}` }, headers: { 'X-LocalAI-Confirm': op } })
    expect(stale.status).toBe(409)
    expect(stale.json.error.type).toBe('stale_installed')
    // The transition this page remembers was already made, under another
    // operation: it is refused as that, and nothing runs twice.
    const op1 = `op-${randomUUID()}`
    const again = await pageFetch(admin.page, '/api/addons/tables/upgrade', { method: 'POST', body: { operationId: op1, expectedDigest: state.secondDigest, candidateDigest: state.thirdDigest }, headers: { 'X-LocalAI-Confirm': op1 } })
    expect(again.status).toBe(409)
    expect(again.json.error.type).toBe('upgrade_conflict')
    // A candidate that is not what the library resolves now is refused too.
    const op2 = `op-${randomUUID()}`
    const wrong = await pageFetch(admin.page, '/api/addons/tables/upgrade', { method: 'POST', body: { operationId: op2, expectedDigest: state.thirdDigest, candidateDigest: `sha256:${'1'.repeat(64)}` }, headers: { 'X-LocalAI-Confirm': op2 } })
    expect(wrong.status).toBe(409)
    expect(wrong.json.error.type).toBe('candidate_changed')
    expect((await installed(api)).generation).toBe(3)
    await admin.context.close()
  })

  test('updates: a candidate this host cannot run shows its reason and has no upgrade', async ({ browser }) => {
    await control('step')
    await control('step')
    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    await settle(admin.page)
    await check(admin.page)
    const status = admin.page.getByTestId('update-state')
    await expect(status).toHaveAttribute('data-state', 'incompatible')
    await expect(admin.page.getByTestId('update-reasons')).toContainText('The host version does not fit')
    await expect(admin.page.getByTestId('update-upgrade')).toHaveCount(0)
    await shoot(admin.page, 'updates-incompatible-1280', 'manage-updates')
    const api = await apiAs('admin')
    const now = await installed(api)
    const op = `op-${randomUUID()}`
    const refused = await pageFetch(admin.page, '/api/addons/tables/upgrade', { method: 'POST', body: { operationId: op, expectedDigest: now.digest, candidateDigest: now.update.digest }, headers: { 'X-LocalAI-Confirm': op } })
    expect(refused.status).toBe(409)
    expect(refused.json.error.type).toBe('incompatible')
    expect((await installed(api)).generation).toBe(3)
    await admin.context.close()
  })

  test('updates: an unreachable library is a failed check, never up to date, and says when it was last read', async ({ browser }) => {
    await control('offline')
    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    await settle(admin.page)
    await check(admin.page)
    const status = admin.page.getByTestId('update-state')
    await expect(status).toHaveAttribute('data-state', 'check-failed')
    await expect(status).not.toContainText('up to date')
    await expect(admin.page.getByTestId('update-error')).toBeVisible()
    await expect(admin.page.getByTestId('update-times')).toContainText('Library last read')
    await expect(admin.page.getByTestId('update-upgrade')).toHaveCount(0)
    await shoot(admin.page, 'updates-check-failed-1280', 'manage-updates')
    await admin.page.goto('/app/addons?tab=installed')
    await expect(admin.page.locator('[data-addon="tables"]').getByText('Update check failed')).toBeVisible({ timeout: 25_000 })
    await admin.context.close()
    // The member's list shows no mark for a check that failed.
    await state.memberPage.goto('/app/addons')
    await expect(state.memberPage.locator('[data-app="tables"]')).toBeVisible({ timeout: 25_000 })
    await expect(state.memberPage.getByTestId('update-chip')).toHaveCount(0)
    await control('online')
  })

  test('updates: on a narrow screen the check is reachable and runs from the keyboard', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin', { viewport: { width: 400, height: 800 } })
    await openManage(admin.page)
    await settle(admin.page)
    const button = admin.page.getByTestId('update-check')
    await button.focus()
    await expect(button).toBeFocused()
    await admin.page.keyboard.press('Enter')
    // The library is back: the same moved channel is incompatible again, not
    // the stale failure.
    await expect(admin.page.getByTestId('update-state')).toHaveAttribute('data-state', 'incompatible', { timeout: 30_000 })
    await shoot(admin.page, 'updates-narrow-400', 'manage-updates')
    await admin.context.close()
  })
})
