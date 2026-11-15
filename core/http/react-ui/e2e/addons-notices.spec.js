import { test, expect } from '@playwright/test'
import { ADMIN, app, installedAddon, release, sessionFor, mockAddons, fail } from './addons-fixtures.js'

// The Add-ons page says that something is off or unavailable with the shared
// notice (what, why, who, fix): the update states on a library card, the update
// on the detail pane, and a turn on that the server refuses because the
// installed package cannot run here. The routes are stand-ins.

const shots = process.env.NOTICE_SHOTS
const theme = process.env.NOTICE_THEME
async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}${process.env.NOTICE_SUFFIX || ''}.png`, fullPage: false })
}

const CANDIDATE = 'sha256:' + 'b'.repeat(64)
const entry = (page, id) => page.locator('.cat-item', { has: page.locator(`[data-addon="${id}"]`) })
const rec = (id, title, update, over = {}) => installedAddon({ id, title, icon: undefined, mainPage: undefined, digest: 'sha256:' + id.padEnd(32, '0'), update, ...over })

async function signIn(page) {
  if (theme) await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
}

test.describe('Add-ons: notices', () => {
  test('a library card says what an installed add-on is not up to date on, in the notice', async ({ page }) => {
    await signIn(page)
    const host = await mockAddons(page, {
      releases: ['wiki', 'docs', 'slack', 'tables', 'mail'].map(id => release(id, { version: '1.0.0' })),
      installed: [
        rec('wiki', 'Wiki', { available: true, state: 'update-available', version: '1.3.0', digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z' }),
        rec('docs', 'Docs', { available: false, state: 'incompatible', version: '3.0.0', reasons: ['host-version'] }),
        rec('slack', 'Slack', { state: 'check-failed', available: false, error: 'refresh-failed' }),
        rec('tables', 'Tables', undefined),
        rec('mail', 'Mail', { state: 'up-to-date', available: false }),
      ],
    })
    await page.setViewportSize({ width: 1280, height: 1500 })
    await page.goto('/app/addons?tab=browse')
    const note = (id) => entry(page, id).getByTestId('entry-update-note')

    await expect(note('docs')).toHaveAttribute('role', 'status')
    await expect(note('docs')).toHaveAttribute('data-state', 'incompatible')
    await expect(note('docs')).toContainText('cannot run on this host. It is not offered.')
    await expect(note('slack')).toHaveAttribute('role', 'status')
    await expect(note('slack')).toContainText('The update check failed, so it is not known whether an update exists.')
    // An add-on never checked says nothing on its card: one line above the grid says it once.
    await expect(note('tables')).toHaveCount(0)
    await expect(page.getByTestId('browse-unchecked')).toHaveCount(1)
    await expect(page.getByTestId('browse-unchecked')).toContainText('Updates have not been checked yet.')
    await expect(page.getByTestId('browse-check-all')).toHaveText('Check for updates')
    await expect(page.getByText('The update status is not known.')).toHaveCount(0)
    await page.getByTestId('browse-check-all').click()
    await expect.poll(() => host.log.check.map(c => c.id)).toEqual(['tables'])
    // Up to date is not a problem: one quiet line, not a notice.
    await expect(note('mail')).toContainText('This add-on is up to date.')
    await expect(note('mail')).not.toHaveClass(/ui-notice/)

    // An update is the one control of the card, which opens the confirmation.
    const update = entry(page, 'wiki').getByTestId('entry-upgrade')
    await expect(update.getByTestId('update-upgrade-card')).toHaveText('Update to 1.3.0')
    await shoot(page, 'library')
    await update.getByTestId('update-upgrade-card').click()
    await expect(page.getByTestId('upgrade-confirm')).toContainText('1.3.0')
  })

  test('the pane shows the update as a notice with the Upgrade fix', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, {
      apps: [app({ id: 'wiki', title: 'Wiki', update: 'update-pending', mainPage: undefined, admin: { canStart: false, canStop: true, managementUrl: '/app/addons?manage=wiki' } })],
      installed: [rec('wiki', 'Wiki', { available: true, state: 'update-available', version: '1.3.0', digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z' })],
    })
    await page.goto('/app/addons?detail=wiki')
    const notice = page.getByTestId('pane-update')
    await expect(notice).toHaveClass(/ui-notice/)
    await expect(notice).toContainText('1.3.0')
    await shoot(page, 'pane-update')
    await notice.getByRole('button', { name: /^Update/ }).click()
    await expect(page.getByTestId('upgrade-confirm')).toContainText('1.3.0')
  })

  test('a turn on refused as incompatible says why and offers Upgrade', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, {
      apps: [app({ id: 'tg', title: 'Telegram', enabled: false, state: 'stopped', update: 'update-pending', mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=tg' } })],
      installed: [rec('tg', 'Telegram', { available: true, state: 'update-available', version: '1.5.0', digest: CANDIDATE, checkedAt: '2026-10-01T00:00:00Z' }, { enabled: false, state: 'stopped' })],
      start: () => fail(403, 'installed_incompatible', { message: 'This version of the add-on is not compatible with this server, so it cannot be turned on. Upgrade it to a newer version, then turn it on. Nothing was changed' }),
    })
    await page.goto('/app/addons?detail=tg')
    await page.getByRole('button', { name: 'Turn on' }).click()
    const notice = page.getByTestId('pane-enable-failed')
    await expect(notice).toHaveAttribute('role', 'alert')
    await expect(notice).toHaveAttribute('data-cause', 'installed_incompatible')
    await expect(notice).toContainText('Cannot turn on Telegram: not compatible with this server')
    await expect(notice).toContainText('Upgrade it to a newer version, then turn it on.')
    // The plain update notice steps aside: one Upgrade, on the failure.
    await expect(page.getByTestId('pane-update')).toHaveCount(0)
    await shoot(page, 'enable-incompatible')
    await notice.getByRole('button', { name: /^Update/ }).click()
    await expect(page.getByTestId('upgrade-confirm')).toContainText('1.5.0')
  })

  test('with no newer version listed the refusal says there is nothing to upgrade to', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, {
      apps: [app({ id: 'tg', title: 'Telegram', enabled: false, state: 'stopped', mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=tg' } })],
      installed: [rec('tg', 'Telegram', { available: false, state: 'incompatible', version: '', reasons: ['api-version'] }, { enabled: false, state: 'stopped' })],
      start: () => fail(403, 'installed_incompatible', { message: 'This version of the add-on is not compatible with this server, so it cannot be turned on. Upgrade it to a newer version, then turn it on. Nothing was changed' }),
    })
    await page.goto('/app/addons?detail=tg')
    await page.getByRole('button', { name: 'Turn on' }).click()
    const notice = page.getByTestId('pane-enable-failed')
    await expect(notice).toContainText('No newer version is listed yet. Keep it off, or remove it.')
    await expect(notice.getByRole('button')).toHaveCount(0)
    await shoot(page, 'enable-no-upgrade')
  })

  test('another refused turn on is a notice with the server reason, and no fix', async ({ page }) => {
    await signIn(page)
    await mockAddons(page, {
      apps: [app({ id: 'tg', title: 'Telegram', enabled: false, state: 'stopped', mainPage: undefined, admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=tg' } })],
      start: () => fail(403, 'addon_error', { message: 'missing configuration: data directory' }),
    })
    await page.goto('/app/addons?detail=tg')
    await page.getByRole('button', { name: 'Turn on' }).click()
    const notice = page.getByTestId('pane-enable-failed')
    await expect(notice).toContainText('Could not turn on Telegram')
    await expect(notice).toContainText('missing configuration: data directory')
    await shoot(page, 'enable-failed')
  })
})
