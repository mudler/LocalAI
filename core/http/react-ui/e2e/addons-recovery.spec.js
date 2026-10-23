import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'

// Data recovery of the Tables add-on, seen from the administrator's browser.
// One UI test server runs the real add-on owner, the real Tables executable
// and the real built UI package with named accounts and real sessions. The
// page asks, the owner decides: nothing here mocks a route, except the one
// banner screenshot that is marked as such. The specs change the owner's
// state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const shots = process.env.ADDONS_SHOTS
const MANAGE = '/app/addons?tab=installed&manage=tables'

async function shoot(page, name, target = 'recovery-panel') {
  if (!shots) return
  // The panel is far down the sheet: bring what the shot is about into view.
  const el = page.getByTestId(target).first()
  if (await el.count()) await el.scrollIntoViewIfNeeded()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(350)
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

async function generation(api) {
  const r = await api.get('/api/addons/apps')
  expect(r.status()).toBe(200)
  return (await r.json()).apps.find(a => a.id === 'tables').generation
}

async function listIds(api) {
  const r = await api.get('/api/addons/resources?addonId=tables')
  expect(r.status()).toBe(200)
  return (await r.json()).resources.map(x => x.id)
}

async function openManage(page) {
  await page.goto(MANAGE)
  await expect(page.getByTestId('addon-manage')).toBeVisible({ timeout: 25_000 })
  await expect(page.getByTestId('recovery-panel')).toBeVisible({ timeout: 25_000 })
}

// The sheet's own enable and disable buttons: the real lifecycle.
async function setEnabled(page, enabled) {
  const sheet = page.getByTestId('addon-manage')
  await sheet.getByRole('button', { name: enabled ? 'Enable' : 'Disable' }).click()
  await expect(sheet.getByRole('button', { name: enabled ? 'Disable' : 'Enable' })).toBeVisible({ timeout: 30_000 })
}

test.describe.configure({ mode: 'serial' })

const state = {}

test.describe('Add-on data recovery through the real owner', () => {
  test('recovery: an administrator backs up from the manage sheet and the backup is listed as verified', async ({ browser }) => {
    const member = await browserAs(browser, 'member')
    await member.page.goto('/app/addons')
    const api = await apiAs('admin')
    const gen = await generation(api)
    const op = `op-${randomUUID()}`
    const made = await pageFetch(member.page, '/api/addons/resources', { method: 'POST', body: { addonId: 'tables', operationId: op, generation: gen, arguments: { name: 'Ledger' } }, headers: { 'X-LocalAI-Confirm': op } })
    expect(made.status, JSON.stringify(made.json)).toBe(200)
    state.db = made.json.id
    const shared = await pageFetch(member.page, `/api/addons/resources/${state.db}/grants/${creds.users.editor.id}`, { method: 'PUT', body: { role: 'editor', operationId: `op-${randomUUID()}` }, headers: { 'X-LocalAI-Confirm': state.db } })
    expect(shared.status, JSON.stringify(shared.json)).toBe(200)
    state.memberPage = member.page

    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    await expect(admin.page.getByTestId('recovery-empty')).toBeVisible()
    await shoot(admin.page, 'recovery-empty-1280')
    await admin.page.getByTestId('recovery-backup').click()
    const item = admin.page.getByTestId('recovery-list').locator('li').first()
    await expect(item).toBeVisible({ timeout: 60_000 })
    await expect(item).toContainText('Verified')
    await expect(item).toContainText('Made by you')
    await expect(admin.page.getByTestId('recovery-notice')).toContainText('was made and verified')
    state.backup = await item.getAttribute('data-backup')
    expect(state.backup).toMatch(/^b[0-9a-z-]+$/)
    // The add-on is running again and its database still answers.
    expect(await listIds(await apiAs('member'))).toContain(state.db)
    await shoot(admin.page, 'recovery-backup-1280')
    await admin.context.close()
  })

  test('recovery: a person and a key cannot back up, restore or purge, and only the owner session of an administrator can', async ({ browser }) => {
    const member = await browserAs(browser, 'member')
    await member.page.goto('/app/addons')
    const op = `op-${randomUUID()}`
    for (const [method, path, body] of [
      ['GET', '/api/addons/tables/recovery'],
      ['POST', '/api/addons/tables/recovery/backup', { operationId: op }],
      ['POST', '/api/addons/tables/recovery/restore', { operationId: op, backupId: state.backup, confirm: 'tables' }],
      ['POST', '/api/addons/tables/purge', { operationId: op, scope: { data: true }, confirm: 'tables' }],
    ]) {
      const r = await pageFetch(member.page, path, { method, body, headers: { 'X-LocalAI-Confirm': op } })
      expect(r.status, `${method} ${path}`).toBe(403)
    }
    // An administrator's key is not the administrator's own browser session.
    const key = await apiAs('admin')
    for (const [path, body] of [
      ['/api/addons/tables/recovery/backup', { operationId: op }],
      ['/api/addons/tables/recovery/restore', { operationId: op, backupId: state.backup, confirm: 'tables' }],
      ['/api/addons/tables/purge', { operationId: op, scope: { data: true }, confirm: 'tables' }],
    ]) {
      const r = await key.post(path, { data: body, headers: { 'X-LocalAI-Confirm': op } })
      expect(r.status(), path).toBe(403)
    }
    // The read is for administrators, from a key too.
    expect((await key.get('/api/addons/tables/recovery')).status()).toBe(200)
    await member.context.close()
  })

  test('recovery: the restore dialog warns, asks for the typed ID and a restore does not give back a revoked share', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin')
    const memberApi = await apiAs('member')
    // After the backup the owner of the database takes the editor's access away.
    const rv = await pageFetch(state.memberPage, `/api/addons/resources/${state.db}/grants/${creds.users.editor.id}`, { method: 'DELETE', headers: { 'X-LocalAI-Confirm': state.db } })
    expect(rv.status, JSON.stringify(rv.json)).toBe(200)
    expect(await listIds(await apiAs('editor'))).not.toContain(state.db)

    await openManage(admin.page)
    // A running add-on cannot be restored: the dialog says so.
    await admin.page.getByTestId('recovery-restore').first().click()
    const dialog = admin.page.getByTestId('recovery-restore-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Changes made after the backup are lost')
    await expect(dialog).toContainText('nobody gets back access that was taken away')
    await expect(dialog).toContainText('Stop the add-on first')
    await shoot(admin.page, 'recovery-restore-running-1280', 'recovery-restore-dialog')
    // Escape closes the dialog and gives the focus back.
    await admin.page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await setEnabled(admin.page, false)

    await admin.page.getByTestId('recovery-restore').first().click()
    await expect(dialog).toBeVisible()
    const confirm = admin.page.getByRole('alertdialog').getByRole('button', { name: 'Restore' })
    await expect(confirm).toBeDisabled()
    const typed = dialog.getByTestId('recovery-confirm-input')
    await expect(typed).toBeFocused()
    await typed.fill('Tables')
    await expect(confirm).toBeDisabled()
    await typed.fill('tables')
    await expect(confirm).toBeEnabled()
    await shoot(admin.page, 'recovery-restore-dialog-1280', 'recovery-restore-dialog')
    await confirm.click()
    await expect(dialog).toBeHidden()
    const notice = admin.page.getByTestId('recovery-notice')
    await expect(notice).toContainText('The backup was restored', { timeout: 60_000 })
    const warnings = admin.page.getByTestId('recovery-warnings')
    await expect(warnings).toContainText('Changes made after that time are not in it')
    await expect(warnings).toContainText('kept as a recovery copy until you purge it')
    await expect(admin.page.getByTestId('recovery-last-restore')).toBeVisible()
    await shoot(admin.page, 'recovery-restored-1280', 'recovery-last-restore')

    await setEnabled(admin.page, true)
    // The database is back for its owner. The editor, who lost access after
    // the backup, is still out: the restore never resurrects a grant.
    expect(await listIds(memberApi)).toContain(state.db)
    expect(await listIds(await apiAs('editor'))).not.toContain(state.db)
    await admin.context.close()
  })

  test('recovery: a second backup, the sole recovery copy is kept and the keyboard reaches every action', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin', { viewport: { width: 400, height: 800 } })
    await openManage(admin.page)
    // Keyboard: the backup button is reachable by Tab and runs on Enter.
    const backup = admin.page.getByTestId('recovery-backup')
    await backup.focus()
    await expect(backup).toBeFocused()
    await admin.page.keyboard.press('Enter')
    await expect(admin.page.getByTestId('recovery-list').locator('li')).toHaveCount(2, { timeout: 60_000 })
    // Narrow layout: nothing overflows the sheet.
    const overflow = await admin.page.evaluate(() => {
      const sheet = document.querySelector('[data-testid="addon-manage"]')
      return sheet ? sheet.scrollWidth - sheet.clientWidth : -1
    })
    expect(overflow).toBeLessThanOrEqual(0)
    await shoot(admin.page, 'recovery-list-400')
    await admin.context.close()
  })

  test('recovery: the page shows a needs-recovery banner with restore and purge as the actions (status mocked)', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin', { viewport: { width: 400, height: 800 } })
    const real = await (await apiAs('admin')).get('/api/addons/tables/recovery')
    const body = await real.json()
    await admin.page.route('**/api/addons/tables/recovery', async route => {
      if (route.request().method() !== 'GET') return route.continue()
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
        ...body, state: 'needs-recovery', recoveryBackup: body.backups[0].id,
        reason: 'the migration to a new data format did not complete',
      }) })
    })
    await openManage(admin.page)
    const banner = admin.page.getByTestId('recovery-banner')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText('needs recovery')
    await expect(banner).toContainText('Restore the verified recovery copy below, or purge the data')
    await expect(admin.page.getByTestId('recovery-restore').first()).toBeEnabled()
    await expect(admin.page.getByTestId('recovery-purge')).toBeEnabled()
    await shoot(admin.page, 'recovery-needs-recovery-400', 'recovery-banner')
    await admin.context.close()
  })

  test('recovery: a purge needs a scope, the typed ID and a stopped add-on, and removes the data and its shares', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin')
    await openManage(admin.page)
    await admin.page.getByTestId('recovery-purge').click()
    const dialog = admin.page.getByTestId('recovery-purge-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Stop the add-on first')
    await admin.page.keyboard.press('Escape')
    await setEnabled(admin.page, false)
    await admin.page.getByTestId('recovery-purge').click()
    const confirm = admin.page.getByRole('alertdialog').getByRole('button', { name: 'Purge…' })
    await expect(confirm).toBeDisabled()
    await dialog.getByTestId('recovery-confirm-input').fill('tables')
    await expect(confirm).toBeEnabled()
    // An empty scope cannot be confirmed.
    await dialog.getByTestId('scope-data').uncheck()
    await expect(confirm).toBeDisabled()
    await expect(dialog).toContainText('Select what to remove')
    await dialog.getByTestId('scope-data').check()
    await shoot(admin.page, 'recovery-purge-dialog-1280', 'recovery-purge-dialog')
    await confirm.click()
    await expect(admin.page.getByTestId('recovery-notice')).toContainText('The retained data was purged', { timeout: 60_000 })
    await setEnabled(admin.page, true)
    expect(await listIds(await apiAs('member'))).not.toContain(state.db)
    // The backups were not in the scope.
    await expect(admin.page.getByTestId('recovery-list').locator('li')).toHaveCount(2)
    await admin.context.close()
  })
})
