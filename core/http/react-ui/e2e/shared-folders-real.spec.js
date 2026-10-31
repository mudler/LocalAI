import { test, expect } from './coverage-fixtures.js'

// The Shared tab of Files against the real test server started with
// --shared-pages-fixture. Auth is off, so the person in the browser is the
// administrator and also the viewer of the fixture's folders: /shared/team
// (read, pages standup and releases) and /shared/board (write, pages board and digest).
// The spec takes the viewer's grant on team away and gives it back through the
// UI, and checks that the pages follow: gone, then back with the trust sheet.
// A new folder is created, shared with everyone, renamed, removed and brought
// back with Undo. The specs for several users are in the Go route tests.

test.describe.configure({ mode: 'serial', timeout: 90_000 })

const VIEWER = 'anonymous'
const SHOTS = process.env.SHARED_FOLDERS_SHOTS || 'test-results/shared-folders'
const settle = (page) => page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))))
const shot = async (page, name) => { await settle(page); await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: false }) }
const api = (request, path) => request.get(path).then(r => r.json())
const row = (page, name) => page.locator(`[data-testid="sf-row"][data-folder="${name}"]`)
const openShared = (page) => page.goto('/app/files?tab=shared')

test.beforeAll(async ({ request }) => {
  // A run before this one may have left a folder or a missing grant behind.
  const { folders } = await api(request, '/api/chats/admin/shared-folders')
  for (const f of folders) {
    if (f.name === 'team') {
      await request.put(`/api/chats/admin/shared-folders/${f.id}/members/${VIEWER}`, { data: { mode: 'read' } })
      await request.delete(`/api/chats/admin/shared-folders/${f.id}/members/%40everyone`)
    }
    if (f.name !== 'team' && f.name !== 'board') await request.delete(`/api/chats/admin/shared-folders/${f.id}`)
  }
  for (const p of (await api(request, '/api/chats/shared-pages')).pages.filter(x => x.name === 'standup')) {
    await request.delete(`/api/chats/shared-pages/${p.folder_id}/${p.name}/consent`)
  }
})

test('the admin sees every shared folder with its pages and members', async ({ page }) => {
  await openShared(page)
  const admin = page.getByTestId('sf-admin')
  await expect(admin.getByRole('heading', { name: /Shared folders/ })).toBeVisible()
  await expect(row(page, 'team')).toContainText('shared/team')
  await expect(row(page, 'team').getByTestId('sf-pages')).toHaveText('2 pages')
  await expect(row(page, 'team').getByTestId('sf-summary')).toHaveText('2 people')
  await expect(row(page, 'board').getByTestId('sf-pages')).toHaveText('2 pages')
  await expect(page.getByRole('tab', { name: 'Shared' })).toHaveAttribute('aria-selected', 'true')
})

test('taking the viewer off a folder hides its pages, and giving read back brings the trust sheet', async ({ page }) => {
  await openShared(page)
  await row(page, 'team').getByTestId('sf-members').click()
  const pane = page.getByTestId('sf-pane')
  await expect(pane).toBeVisible()
  const me = pane.locator(`[data-testid="sf-member"][data-principal="${VIEWER}"]`)
  await expect(me.getByTestId('sf-member-mode')).toHaveValue('read')
  await me.getByTestId('sf-member-remove').click()
  await expect(me).toHaveCount(0)
  await expect(page.getByText(`${VIEWER} can no longer use “team”.`)).toBeVisible()

  await page.goto('/app/pages')
  await expect(page.locator('[data-testid="shared-card"][data-page="standup"]')).toHaveCount(0)
  await expect(page.locator('[data-testid="shared-card"][data-page="board"]')).toHaveCount(1)

  // Without a user database the picker offers Everyone: the viewer gets the
  // folder back through the everyone group.
  await openShared(page)
  await row(page, 'team').getByTestId('sf-members').click()
  await pane.getByTestId('sf-add-who').selectOption('@everyone')
  await pane.getByTestId('sf-add-mode').selectOption('read')
  await pane.getByTestId('sf-add-submit').click()
  await expect(pane.locator('[data-testid="sf-member"][data-principal="@everyone"]')).toBeVisible()
  await expect(row(page, 'team').getByTestId('sf-summary')).toHaveText('Everyone can read · 1 person')

  await page.goto('/app/pages')
  const standup = page.locator('[data-testid="shared-card"][data-page="standup"]')
  await expect(standup).toBeVisible()
  await standup.getByTestId('shared-open').click()
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet).toContainText('You can only read this shared folder')
  await page.getByTestId('trust-allow').click()
  await expect(page.getByTestId('page-frame')).toBeVisible()
  await expect(page.getByTestId('shared-pill')).toHaveText(/Allowed/)
})

test('a new folder: create, share with everyone, rename, remove and undo', async ({ page }) => {
  await openShared(page)
  await page.getByTestId('sf-new').click()
  const form = page.getByTestId('sf-create')
  await form.getByRole('textbox').fill('bad/name')
  await expect(form.getByRole('button', { name: 'Create' })).toBeDisabled()
  await form.getByRole('textbox').fill('Design reviews')
  await form.getByRole('button', { name: 'Create' }).click()

  const pane = page.getByTestId('sf-pane')
  await expect(pane).toBeVisible()
  await expect(pane).toContainText('shared/Design reviews')
  await expect(pane.getByTestId('sf-member')).toHaveCount(1)
  await pane.getByTestId('sf-add-who').selectOption('@everyone')
  await pane.getByTestId('sf-add-mode').selectOption('write')
  await pane.getByTestId('sf-add-submit').click()
  await expect(pane.locator('[data-testid="sf-member"][data-principal="@everyone"]')).toContainText('Everyone')
  await expect(row(page, 'Design reviews').getByTestId('sf-summary')).toHaveText('Everyone can edit · 1 person')
  await page.keyboard.press('Escape')
  await expect(pane).toHaveCount(0)

  await row(page, 'Design reviews').getByRole('button', { name: 'More actions for Design reviews' }).click()
  await page.getByRole('menuitem', { name: 'Rename' }).click()
  const rename = row(page, 'Design reviews').getByRole('textbox')
  await rename.fill('Reviews')
  await rename.press('Enter')
  await expect(row(page, 'Reviews')).toBeVisible()

  await row(page, 'Reviews').getByRole('button', { name: 'More actions for Reviews' }).click()
  await page.getByRole('menuitem', { name: 'Remove' }).click()
  await expect(row(page, 'Reviews')).toHaveCount(0)
  const toast = page.getByTestId('sf-removed-toast')
  await expect(toast).toContainText('Removed “Reviews”.')
  await toast.getByRole('button', { name: 'Undo' }).click()
  await expect(row(page, 'Reviews')).toBeVisible()
  await expect(row(page, 'Reviews').getByTestId('sf-summary')).toHaveText('Everyone can edit · 1 person')

  const { events } = await api(page.request, '/api/chats/admin/shared-folders/events')
  expect(events.slice(0, 3).map(e => e.action)).toEqual(['restore', 'delete', 'rename'])
})

// A member who is not an administrator: the same tab lists the folders shared
// with them and their mode. The auth status is answered by the browser, so the
// UI takes the person for a user; the list is the server's.
const asMember = (page) => page.route('**/api/auth/status', route => route.fulfill({
  json: { authEnabled: true, user: { id: VIEWER, name: 'Marta', role: 'user', permissions: { chats: true, pages: true } } },
}))

test('a member sees the folders shared with them and what they may do', async ({ page }) => {
  await asMember(page)
  await openShared(page)
  const mine = page.getByTestId('sf-mine')
  await expect(mine.getByRole('heading', { name: /Shared with you/ })).toBeVisible()
  await expect(row(page, 'team').getByTestId('sf-mode')).toHaveText('Can read')
  await expect(row(page, 'board').getByTestId('sf-mode')).toHaveText('Can edit')
  await expect(row(page, 'team').getByTestId('sf-pages')).toHaveAttribute('href', /\/app\/pages#pg-shared$/)
  await expect(page.getByTestId('sf-members')).toHaveCount(0)
})

for (const v of [{ name: 'desktop', width: 1440, height: 900 }, { name: 'phone', width: 390, height: 844 }]) {
  for (const theme of ['dark', 'light']) {
    test(`screenshots, ${v.name} ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: v.width, height: v.height })
      await page.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
      await openShared(page)
      await expect(row(page, 'team')).toBeVisible()
      await shot(page, `sf-admin-list-${v.name}-${theme}`)
      await row(page, 'team').getByTestId('sf-members').click()
      // The pane slides in: draw it once it rests.
      await expect(page.getByTestId('sf-pane')).toHaveAttribute('data-state', 'open')
      await shot(page, `sf-admin-members-${v.name}-${theme}`)

      const member = await page.context().newPage()
      await member.setViewportSize({ width: v.width, height: v.height })
      await member.addInitScript((m) => localStorage.setItem('localai-theme', m), theme)
      await asMember(member)
      await openShared(member)
      await expect(row(member, 'team')).toBeVisible()
      await shot(member, `sf-member-list-${v.name}-${theme}`)
      await member.close()
    })
  }
}

test('screenshot of the empty state', async ({ page }) => {
  await page.route('**/api/chats/admin/shared-folders', route => (route.request().method() === 'GET'
    ? route.fulfill({ json: { folders: [], max_folders: 100, max_members: 200 } })
    : route.continue()))
  await page.setViewportSize({ width: 1440, height: 900 })
  await openShared(page)
  await expect(page.getByTestId('sf-empty')).toContainText('No shared folders yet')
  await shot(page, 'sf-admin-empty-desktop-dark')
})
