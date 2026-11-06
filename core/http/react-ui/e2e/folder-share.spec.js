// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

// A person shares a folder of their own home from Files, and sees the folders
// other people shared with them in the Shared tab. The folder sharing routes
// are mocked in the browser; the Go route tests cover the real server. Set
// FOLDER_SHARE_SHOTS_DIR to save screenshots of the dialog and of the list.

const SHOTS = process.env.FOLDER_SHARE_SHOTS_DIR

const FOLDER_ID = 42

const ENTRIES = [
  { path: '/Projects', name: 'Projects', kind: 'dir', size: 0, version: 1, rev: 3 },
  { path: '/Reports', name: 'Reports', kind: 'dir', size: 0, version: 1, rev: 4 },
  { path: '/notes.md', name: 'notes.md', kind: 'file', size: 120, version: 1, rev: 5 },
  { path: '/LocalAI', name: 'LocalAI', kind: 'dir', size: 0, version: 1, rev: 1, managed: 'localai' },
  { path: '/Chats', name: 'Chats', kind: 'dir', size: 0, version: 1, rev: 2, managed: 'chats' },
]

async function signIn(page, { authEnabled = true, role = 'user' } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: authEnabled
      ? { authEnabled: true, user: { id: 'u-alice', name: 'Alice', role, permissions: { chats: true } } }
      : { authEnabled: false },
  }))
}

async function homeListing(page) {
  await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
    if (route.request().method() !== 'GET') return route.fallback()
    return route.fulfill({ json: { path: '/', entries: ENTRIES, max_file_size: 1 << 30 } })
  })
}

// The folder sharing routes over one folder, with the requests they saw.
function mockShares(page, { sharing = true } = {}) {
  const state = {
    grants: [{ userId: 'u-bob', displayName: 'Bob', role: 'read' }],
    seen: [],
  }
  const answer = () => ({ id: FOLDER_ID, name: 'Projects', path: '/Projects', grants: state.grants })
  page.route(/\/api\/chats\/files\/shares(\/.*)?(\?.*)?$/, route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/chats/files/shares', '')
    state.seen.push({ method: req.method(), path, confirm: req.headers()['x-localai-confirm'] || '' })
    if (path === '' && req.method() === 'GET') {
      const folders = state.grants.length > 0 ? [{ id: FOLDER_ID, name: 'Projects', path: '/Projects', sharedWith: state.grants.length, everyone: state.grants.some(g => g.userId === '@everyone') }] : []
      return route.fulfill({ json: { sharing, folders } })
    }
    if (path === '' && req.method() === 'POST') {
      return route.fulfill({ json: { id: FOLDER_ID, name: 'Projects', path: '/Projects', sharedWith: state.grants.length, everyone: false } })
    }
    if (path === `/${FOLDER_ID}/users`) {
      return route.fulfill({ json: { users: [{ id: 'u-bob', displayName: 'Bob' }, { id: 'u-carol', displayName: 'Carol' }] } })
    }
    const m = path.match(/^\/42\/grants(?:\/(.+))?$/)
    if (!m) return route.fulfill({ status: 403, json: { error: { message: 'you cannot share this folder', type: 'authorization_error' } } })
    if (m[1]) {
      const who = decodeURIComponent(m[1])
      const names = { 'u-bob': 'Bob', 'u-carol': 'Carol', '@everyone': '' }
      state.grants = state.grants.filter(g => g.userId !== who)
      if (req.method() === 'PUT') {
        const g = { userId: who, displayName: names[who] ?? who, role: req.postDataJSON().role }
        state.grants = who === '@everyone' ? [g, ...state.grants] : [...state.grants, g]
      }
    }
    return route.fulfill({ json: answer() })
  })
  return state
}

const row = (page, name) => page.getByTestId('ws-row').filter({ has: page.locator(`button.ws-row__name:text-is("${name}")`) })

async function openMenu(page, name) {
  await row(page, name).getByRole('button', { name: `More actions for ${name}` }).click()
}

test('marks a shared folder and shares a folder of the home from its menu', async ({ page }) => {
  await signIn(page)
  await homeListing(page)
  const state = mockShares(page)
  await page.goto('/app/files')
  await expect(row(page, 'Projects').getByTestId('ws-shared-mark-line')).toHaveText('Shared with 1')
  await expect(row(page, 'Reports').getByTestId('ws-shared-mark')).toHaveCount(0)

  // Only a folder of one's own gets the action.
  await openMenu(page, 'notes.md')
  await expect(page.getByRole('menuitem', { name: 'Share…' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await openMenu(page, 'LocalAI')
  await expect(page.getByRole('menuitem', { name: 'Share…' })).toHaveCount(0)
  await page.keyboard.press('Escape')

  await openMenu(page, 'Projects')
  await page.getByRole('menuitem', { name: 'Share…' }).click()
  const dialog = page.getByTestId('ws-share-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('heading', { name: /Projects/ })).toBeVisible()
  await expect(dialog.getByTestId('share-grant')).toHaveCount(1)
  await expect(dialog.getByTestId('share-grant').first()).toContainText('Bob')

  // Two characters are too few: the dialog asks the server nothing, as for
  // every kind of sharing.
  await dialog.getByTestId('share-search').fill('ca')
  await page.waitForTimeout(600)
  expect(state.seen.filter(r => r.path === `/${FOLDER_ID}/users`)).toHaveLength(0)
  await expect(dialog.getByTestId('share-user')).toHaveCount(0)
  await dialog.getByTestId('share-search').fill('car')
  await expect(dialog.getByTestId('share-user')).toHaveCount(1, { timeout: 5000 })
  expect(state.seen.filter(r => r.path === `/${FOLDER_ID}/users`).length).toBeGreaterThan(0)
  await dialog.getByTestId('share-new-role').selectOption('write')
  await dialog.getByTestId('share-user').getByRole('button', { name: /Carol/ }).click()
  await expect(dialog.getByTestId('share-grant')).toHaveCount(2)
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-carol"]')).toHaveAttribute('data-role', 'write')

  // Everyone gets the role chosen for the next person: read here.
  await dialog.getByTestId('share-new-role').selectOption('read')
  await dialog.getByTestId('share-everyone-add').getByRole('button').click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="@everyone"]')).toHaveAttribute('data-role', 'read')

  await dialog.getByRole('button', { name: 'Stop sharing with Bob' }).click()
  await dialog.getByRole('button', { name: 'Stop sharing', exact: true }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-bob"]')).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Close' }).last().click()
  await expect(row(page, 'Projects').getByTestId('ws-shared-mark-line')).toHaveText('Shared with 2')

  const changes = state.seen.filter(s => s.path.startsWith(`/${FOLDER_ID}`))
  expect(changes.length).toBeGreaterThan(3)
  expect(changes.every(s => s.confirm === String(FOLDER_ID)), 'every grant call names the folder in the confirmation header').toBe(true)
})

test('offers no Share action in local-owner mode and calls no sharing route', async ({ page }) => {
  await signIn(page, { authEnabled: false })
  await homeListing(page)
  const state = mockShares(page)
  await page.goto('/app/files')
  await expect(row(page, 'Projects')).toBeVisible()
  await openMenu(page, 'Projects')
  await expect(page.getByRole('menuitem', { name: 'Rename' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Share…' })).toHaveCount(0)
  expect(state.seen).toHaveLength(0)
})

test('offers no Share action when the server does not share folders', async ({ page }) => {
  await signIn(page)
  await homeListing(page)
  const state = mockShares(page, { sharing: false })
  await page.goto('/app/files')
  await expect.poll(() => state.seen.length).toBeGreaterThan(0)
  await openMenu(page, 'Projects')
  await expect(page.getByRole('menuitem', { name: 'Rename' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Share…' })).toHaveCount(0)
})

const MINE = [
  { id: 3, name: 'Team', path: '/shared/Team', pages: 1, mode: 'read' },
  { id: 42, name: 'Projects', path: '', pages: 0, mode: 'write', owner: 'Alice' },
  { id: 43, name: 'Board', path: '', pages: 0, mode: 'read', owner: 'Carol', everyone: true },
]

test('lists the folders shared with me with the owner and the access', async ({ page }) => {
  await signIn(page)
  await page.route('**/api/chats/shared-folders', route => route.fulfill({ json: { folders: MINE } }))
  await page.goto('/app/files?tab=shared')
  const list = page.getByTestId('sf-mine')
  const sfRow = (name) => list.locator(`[data-testid="sf-row"][data-folder="${name}"]`)
  await expect(sfRow('Projects').getByTestId('sf-shared-by-line')).toHaveText('Shared by Alice')
  await expect(sfRow('Board').getByTestId('sf-shared-by-line')).toHaveText('Shared with everyone by Carol')
  await expect(sfRow('Projects').getByTestId('sf-mode')).toHaveText('Can edit')
  await expect(sfRow('Team').locator('.sf-row__path')).toHaveText('shared/Team')
  await expect(sfRow('Projects').locator('.sf-row__path')).toHaveCount(0)
})

test('shows an administrator the folders people shared, under the administrator list', async ({ page }) => {
  await signIn(page, { role: 'admin' })
  await page.route('**/api/auth/admin/users', route => route.fulfill({ json: { users: [] } }))
  await page.route(/\/api\/chats\/admin\/shared-folders(\?.*)?$/, route => route.fulfill({ json: { folders: [{ id: 3, name: 'Team', path: '/shared/Team', pages: 1, members: [] }] } }))
  await page.route('**/api/chats/shared-folders', route => route.fulfill({ json: { folders: MINE } }))
  await page.goto('/app/files?tab=shared')
  await expect(page.getByTestId('sf-admin')).toBeVisible()
  const people = page.getByTestId('sf-people')
  await expect(people.getByTestId('sf-row')).toHaveCount(2)
  await expect(people.locator('[data-folder="Team"]')).toHaveCount(0)
})

test.describe('screenshots', () => {
  test.skip(!SHOTS, 'set FOLDER_SHARE_SHOTS_DIR to save the folder sharing screenshots')
  for (const theme of ['light', 'dark']) {
    for (const [size, viewport] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
      test(`folder sharing, ${theme}, ${size}`, async ({ page }) => {
        await page.setViewportSize(viewport)
        await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
        const shot = async (name) => {
          await page.evaluate(() => Promise.all(document.getAnimations()
            .filter(a => a.effect?.getTiming().iterations !== Infinity)
            .map(a => a.finished.catch(() => {}))))
          await page.screenshot({ path: `${SHOTS}/folder-share-${name}-${theme}-${size}.png` })
        }
        await signIn(page)
        await homeListing(page)
        const state = mockShares(page)
        state.grants = [{ userId: '@everyone', displayName: '', role: 'read' }, { userId: 'u-carol', displayName: 'Carol Mendes', role: 'write' }]
        await page.goto('/app/files')
        await page.evaluate(() => document.fonts.ready)
        await expect(row(page, 'Projects').getByTestId('ws-shared-mark-line')).toHaveText('Shared with 2')
        await shot('files')
        await openMenu(page, 'Projects')
        await expect(page.getByRole('menuitem', { name: 'Share…' })).toBeVisible()
        await shot('menu')
        await page.getByRole('menuitem', { name: 'Share…' }).click()
        const dialog = page.getByTestId('ws-share-dialog')
        await expect(dialog.getByTestId('share-grant')).toHaveCount(2)
        await shot('dialog')
        await dialog.getByTestId('share-search').fill('bob')
        await expect(dialog.getByTestId('share-user')).toHaveCount(1)
        await shot('dialog-search')
        await page.keyboard.press('Escape')

        await page.route('**/api/chats/shared-folders', route => route.fulfill({ json: { folders: MINE } }))
        await page.goto('/app/files?tab=shared')
        await expect(page.getByTestId('sf-shared-by-line').first()).toBeVisible()
        await page.evaluate(() => document.fonts.ready)
        await shot('shared-with-me')
      })
    }
  }
})
