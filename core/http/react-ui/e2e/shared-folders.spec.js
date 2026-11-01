import { test, expect } from './coverage-fixtures.js'

// The Shared tab of Files with the shared folders API mocked in the browser, so
// it runs in the default suite. The real server is covered by
// shared-folders-real.spec.js and the Go route tests.

const row = (page, name) => page.locator(`[data-testid="sf-row"][data-folder="${name}"]`)

// A small in-memory server: folders by id, with members.
function mockFolders(page, initial) {
  const state = { folders: structuredClone(initial), next: 100, rev: 1, removed: {}, fail: false }
  const json = (route, body, status = 200) => route.fulfill({ status, json: body })
  const find = (id) => state.folders.find(f => f.id === Number(id))
  page.route(/\/api\/chats\/admin\/shared-folders(\/.*)?(\?.*)?$/, async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/chats/admin/shared-folders', '')
    const body = req.postDataJSON?.() || {}
    const method = req.method()
    if (state.fail && method === 'GET') return json(route, { error: { message: 'database is locked' } }, 500)
    if (path === '' && method === 'GET') return json(route, { folders: state.folders, max_folders: 100, max_members: 200 })
    if (path === '' && method === 'POST') {
      if (state.folders.some(f => f.name.toLowerCase() === body.name.toLowerCase())) {
        return json(route, { error: { message: 'a shared folder with this name exists' } }, 409)
      }
      const f = { id: state.next++, name: body.name, path: `/shared/${body.name}`, pages: 0, members: [{ principal: 'root', name: 'Root', mode: 'write' }] }
      state.folders.push(f)
      return json(route, f, 201)
    }
    if (path === '/restore') {
      const f = state.removed[body.rev]
      state.folders.push(f)
      return json(route, f)
    }
    const m = path.match(/^\/(\d+)(?:\/members\/(.+))?$/)
    const f = m && find(m[1])
    if (!f) return json(route, { error: { message: 'shared folder not found' } }, 404)
    if (m[2]) {
      const who = decodeURIComponent(m[2])
      f.members = f.members.filter(x => x.principal !== who)
      if (method === 'PUT') {
        const user = { '@everyone': 'Everyone', 'u-marta': 'Marta', 'u-sara': 'Sara' }[who] || who
        f.members.push({ principal: who, name: who === '@everyone' ? '@everyone' : user, everyone: who === '@everyone', mode: body.mode })
        f.members.sort((a, b) => (a.everyone ? -1 : b.everyone ? 1 : a.name.localeCompare(b.name)))
      }
      return json(route, f)
    }
    if (method === 'PATCH') { f.name = body.name; f.path = `/shared/${body.name}`; return json(route, f) }
    if (method === 'DELETE') {
      state.folders = state.folders.filter(x => x !== f)
      const rev = ++state.rev
      state.removed[rev] = f
      return json(route, { folder: f, undo_rev: rev })
    }
    return json(route, f)
  })
  return state
}

const TEAM = { id: 7, name: 'Team', path: '/shared/Team', pages: 2, members: [
  { principal: '@everyone', name: '@everyone', everyone: true, mode: 'read' },
  { principal: 'u-marta', name: 'Marta', mode: 'write' },
] }
const BOARD = { id: 9, name: 'Board', path: '/shared/Board', pages: 0, members: [] }

// An administrator signed in, so the members pane lists the users.
test.beforeEach(async ({ page }) => {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, user: { id: 'root', name: 'Root', role: 'admin', permissions: {} } },
  }))
  await page.route('**/api/auth/admin/users', route => route.fulfill({ json: { users: [
    { id: 'u-marta', name: 'Marta', email: 'marta@example.test' },
    { id: 'u-sara', name: 'Sara', email: 'sara@example.test' },
  ] } }))
})

test('lists the shared folders with their pages and members', async ({ page }) => {
  mockFolders(page, [TEAM, BOARD])
  await page.goto('/app/files?tab=shared')
  await expect(row(page, 'Team').getByTestId('sf-summary')).toHaveText('Everyone can read · 1 person')
  await expect(row(page, 'Team').getByTestId('sf-pages')).toHaveText('2 pages')
  await expect(row(page, 'Board')).toContainText('No pages')
  await expect(row(page, 'Board').getByTestId('sf-summary')).toHaveText('No members')
})

test('the members pane adds, changes and removes a member, with Undo', async ({ page }) => {
  const state = mockFolders(page, [TEAM, BOARD])
  await page.goto('/app/files?tab=shared')
  await row(page, 'Board').getByTestId('sf-members').click()
  const pane = page.getByTestId('sf-pane')
  await expect(pane.getByTestId('sf-no-members')).toBeVisible()
  await pane.getByTestId('sf-add-who').selectOption('u-sara')
  await pane.getByTestId('sf-add-mode').selectOption('write')
  await pane.getByTestId('sf-add-submit').click()
  const sara = pane.locator('[data-testid="sf-member"][data-principal="u-sara"]')
  await expect(sara.getByTestId('sf-member-mode')).toHaveValue('write')
  await expect(page.getByText('Added Sara: Can edit.')).toBeVisible()

  await sara.getByTestId('sf-member-mode').selectOption('read')
  await expect.poll(() => state.folders.find(f => f.id === 9).members[0].mode).toBe('read')

  await sara.getByTestId('sf-member-remove').click()
  await expect(sara).toHaveCount(0)
  await page.getByRole('button', { name: 'Undo' }).click()
  await expect(sara).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(pane).toHaveCount(0)
})

test('creates, renames and removes a folder, and Undo brings it back', async ({ page }) => {
  mockFolders(page, [TEAM])
  await page.goto('/app/files?tab=shared')
  await page.getByTestId('sf-new').click()
  const input = page.getByTestId('sf-create').getByRole('textbox')
  await input.fill('.hidden')
  await expect(page.getByTestId('sf-create').getByRole('button', { name: 'Create' })).toBeDisabled()
  await input.fill('team')
  await page.getByTestId('sf-create').getByRole('button', { name: 'Create' }).click()
  await expect(page.getByText('Not done: a shared folder with this name exists')).toBeVisible()
  await input.fill('Reports')
  await page.getByTestId('sf-create').getByRole('button', { name: 'Create' }).click()
  await expect(page.getByTestId('sf-pane')).toContainText('shared/Reports')
  await page.keyboard.press('Escape')

  await row(page, 'Reports').getByRole('button', { name: 'More actions for Reports' }).click()
  await page.getByRole('menuitem', { name: 'Rename' }).click()
  const rename = row(page, 'Reports').getByRole('textbox')
  await rename.fill('Q4 reports')
  await rename.press('Enter')
  await expect(row(page, 'Q4 reports')).toBeVisible()

  await row(page, 'Q4 reports').getByRole('button', { name: 'More actions for Q4 reports' }).click()
  await page.getByRole('menuitem', { name: 'Remove' }).click()
  await expect(row(page, 'Q4 reports')).toHaveCount(0)
  await page.getByTestId('sf-removed-toast').getByRole('button', { name: 'Undo' }).click()
  await expect(row(page, 'Q4 reports')).toBeVisible()
  await expect(page.getByText('“Q4 reports” is back, with its members.')).toBeVisible()
})

test('the empty state starts a folder, and a failed load says so', async ({ page }) => {
  const state = mockFolders(page, [])
  state.fail = true
  await page.goto('/app/files?tab=shared')
  await expect(page.getByTestId('load-error')).toContainText('Could not load the shared folders.')
  state.fail = false
  await page.getByRole('button', { name: 'Try again' }).click()
  await expect(page.getByTestId('sf-empty')).toContainText('No shared folders yet')
  await page.getByTestId('sf-new').click()
  await expect(page.getByTestId('sf-create')).toBeVisible()
  await page.getByTestId('sf-create').getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByTestId('sf-empty')).toBeVisible()
})

test('a member sees the folders shared with them, or an empty state', async ({ page }) => {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, user: { id: 'u-marta', name: 'Marta', role: 'user', permissions: { chats: true, pages: true } } },
  }))
  let folders = [{ id: 7, name: 'Team', path: '/shared/Team', pages: 1, mode: 'write' }]
  await page.route('**/api/chats/shared-folders', route => route.fulfill({ json: { folders } }))
  await page.goto('/app/files?tab=shared')
  await expect(page.getByTestId('sf-mine').getByRole('heading', { name: /Shared with you/ })).toBeVisible()
  await expect(row(page, 'Team').getByTestId('sf-mode')).toHaveText('Can edit')
  await expect(row(page, 'Team').getByTestId('sf-pages')).toHaveText('1 page')
  folders = []
  await page.reload()
  await expect(page.getByTestId('sf-empty')).toContainText('Nothing is shared with you yet')
})
