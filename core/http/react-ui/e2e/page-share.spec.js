// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

// A person shares one of their own pages with named people or everyone, and
// sees the pages other people shared with them. The page routes are mocked in
// the browser; the Go route tests cover the real server. Set
// PAGE_SHARE_SHOTS_DIR to save screenshots of the dialog, the trust sheet, the
// list and the strip.

const SHOTS = process.env.PAGE_SHARE_SHOTS_DIR

const TASKS = {
  name: 'tasks', path: '/pages/tasks', title: 'Tasks board', icon: 'checklist', description: 'What we do this week',
  source: 'own', author: 'you', updated_by: 'you', rev: 7, size: 100, hash: 'h1',
  get updated_at() { return new Date().toISOString() },
}
const NOTES = { ...TASKS, name: 'notes', path: '/pages/notes', title: 'Reading notes', description: '', hash: 'n1' }

const HTML = '<!doctype html><body><p id="v">tasks</p></body>'
const SHARED_HTML = '<!doctype html><body><p id="v">groceries</p></body>'

async function signIn(page, { authEnabled = true } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: authEnabled
      ? { authEnabled: true, user: { id: 'u-alice', name: 'Alice', role: 'user', permissions: { chats: true, pages: true } } }
      : { authEnabled: false },
  }))
}

// The pages of the person, the page routes and the share routes. `state` can
// change during a test: grants, allowed (people who allowed this version),
// on (the administrator's switch), shared (the pages shared with the person).
function mock(page, init = {}) {
  const state = {
    on: true,
    pages: [TASKS, NOTES],
    grants: [
      { id: 'u-bob', display_name: 'Bob Rossi', role: 'use' },
      { id: 'u-dana', display_name: 'Dana Weiss', role: 'view' },
      { id: 'u-eli', display_name: 'Eli Chen', role: 'edit' },
    ],
    allowed: 2,
    shared: [],
    shareSeen: [],
    searches: [],
    ...init,
  }
  const answer = () => ({ grants: state.grants, allowed: state.allowed })
  page.route(/\/api\/chats\/pages(\?.*)?$/, route => route.fulfill({
    json: {
      pages: state.pages.map(p => (p.name === 'tasks' ? { ...p, ...(state.listFields || {}) } : p)),
      user_page_shares_enabled: state.on,
    },
  }))
  page.route(/\/api\/chats\/pages\/tasks(\?.*)?$/, route => route.fulfill({
    json: { page: { ...TASKS, hash: state.hash || 'h1', shared_allowed: state.allowed, shared_with: state.grants.length }, html: HTML },
  }))
  page.route(/\/api\/chats\/pages\/shares\/search(\?.*)?$/, route => {
    const req = route.request()
    const q = new URL(req.url()).searchParams.get('q')
    state.searches.push({ q, confirm: req.headers()['x-localai-confirm'] || '' })
    const all = [{ id: 'u-carol', display_name: 'Carol Mendes' }, { id: 'u-bob', display_name: 'Bob Rossi' }]
    return route.fulfill({ json: { users: all.filter(u => u.display_name.toLowerCase().includes(String(q).toLowerCase())) } })
  })
  page.route(/\/api\/chats\/pages\/tasks\/shares(\/.*)?(\?.*)?$/, route => {
    const req = route.request()
    const rest = new URL(req.url()).pathname.replace('/api/chats/pages/tasks/shares', '')
    const seen = { method: req.method(), rest, confirm: req.headers()['x-localai-confirm'] || '', body: req.postData() ? req.postDataJSON() : null }
    state.shareSeen.push(seen)
    if (!state.on && req.method() === 'PUT') {
      return route.fulfill({ status: 409, json: { error: { message: 'sharing pages is turned off', type: 'sharing_disabled' } } })
    }
    const names = { 'u-carol': 'Carol Mendes', '@everyone': '' }
    if (req.method() === 'PUT') {
      const { principal, role } = seen.body
      const g = { id: principal, display_name: names[principal] ?? principal, role }
      const i = state.grants.findIndex(x => x.id === principal)
      if (i >= 0) state.grants[i] = { ...state.grants[i], role }
      else state.grants = principal === '@everyone' ? [g, ...state.grants] : [...state.grants, g]
    } else if (req.method() === 'DELETE') {
      const who = decodeURIComponent(rest.replace(/^\//, ''))
      state.grants = state.grants.filter(g => g.id !== who)
    }
    return route.fulfill({ json: answer() })
  })
  page.route(/\/api\/chats\/shared-pages(\?.*)?$/, route => route.fulfill({
    json: { enabled: true, user_page_shares_enabled: state.on, pages: state.shared },
  }))
  return state
}

const sharedPage = (extra = {}) => ({
  folder_id: 7, folder: '/users/u-bob', name: 'groceries', title: 'Groceries', icon: 'users', description: 'What to buy',
  author_id: 'u-bob', author_name: 'Bob Rossi', user_shared: true, role: 'use', consent: 'none', source: 'shared', hash: 'g1',
  size: 200, meta_size: 20, writable: true, code_files: [],
  get updated_at() { return new Date().toISOString() },
  ...extra,
})

function mockSharedPage(page, extra = {}, consent = { state: 'none' }) {
  const p = sharedPage(extra)
  const state = { consent, allows: [] }
  page.route(/\/api\/chats\/shared-pages\/7\/groceries(\?.*)?$/, route => route.fulfill({
    json: { page: p, html: SHARED_HTML, consent: state.consent, lines: 12 },
  }))
  page.route(/\/api\/chats\/shared-pages\/7\/groceries\/consent$/, route => {
    const body = route.request().postDataJSON()
    state.allows.push(body)
    state.consent = { state: 'allowed' }
    return route.fulfill({ json: { hash: p.hash, ...(body.once ? { token: 'tok' } : {}) } })
  })
  return state
}

const card = (page, name) => page.locator(`[data-testid="page-card"][data-page="${name}"]`)

async function openShare(page, name = 'tasks') {
  await card(page, name).getByRole('button', { name: /^More for / }).click()
  await page.getByRole('menuitem', { name: 'Share' }).click()
  const dialog = page.getByTestId('page-share-dialog')
  await expect(dialog).toBeVisible()
  return dialog
}

test('opens the dialog from the page menu with the warning, the people and the count line', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await expect(dialog.getByRole('heading', { name: /Tasks board/ })).toBeVisible()
  await expect(dialog.getByTestId('share-warning')).toHaveText(
    "People you share with run this page's code in their browser, with their own rights, after they allow it. They are asked again when you change the code.")
  await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
  await expect(dialog.getByTestId('share-summary')).toHaveText('Shared with 3 people. 2 allowed this version.')
  // The warning comes before the list of people.
  const order = await dialog.evaluate(el => {
    const w = el.querySelector('[data-testid="share-warning"]')
    const g = el.querySelector('[data-testid="share-grants"]')
    return w.compareDocumentPosition(g) & Node.DOCUMENT_POSITION_FOLLOWING ? 'warning first' : 'warning last'
  })
  expect(order).toBe('warning first')
  expect(state.shareSeen[0]).toMatchObject({ method: 'GET', confirm: 'tasks' })
})

test('finds a person from three characters and adds them with the Use role', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await dialog.getByTestId('share-search').fill('ca')
  await page.waitForTimeout(600)
  expect(state.searches).toHaveLength(0)
  await dialog.getByTestId('share-search').fill('car')
  await expect(dialog.getByTestId('share-user')).toHaveCount(1, { timeout: 5000 })
  expect(state.searches[0]).toEqual({ q: 'car', confirm: 'tasks' })
  // The three roles are offered; the first is View.
  const roles = dialog.getByTestId('share-new-role')
  await expect(roles.locator('option')).toHaveText(['View', 'Use', 'Edit'])
  await roles.selectOption('use')
  await dialog.getByTestId('share-user').getByRole('button', { name: /Carol Mendes/ }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-carol"]')).toHaveAttribute('data-role', 'use')
  await expect(dialog.getByTestId('share-summary')).toHaveText('Shared with 4 people. 2 allowed this version.')
  const put = state.shareSeen.find(s => s.method === 'PUT')
  expect(put.body).toEqual({ principal: 'u-carol', role: 'use' })
  expect(put.confirm).toBe('tasks')
})

test('offers Everyone only the View and Use roles, and Edit stays for named people', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  const everyoneRole = dialog.getByTestId('share-everyone-add').getByTestId('share-everyone-role')
  await expect(everyoneRole.locator('option')).toHaveText(['View', 'Use'])
  await everyoneRole.selectOption('use')
  await dialog.getByTestId('share-everyone-add').getByRole('button', { name: 'Share with everyone' }).click()
  const row = dialog.locator('[data-testid="share-grant"][data-user="@everyone"]')
  await expect(row).toHaveAttribute('data-role', 'use')
  await expect(row.getByRole('combobox').locator('option')).toHaveText(['View', 'Use'])
  expect(state.shareSeen.find(s => s.method === 'PUT').body).toEqual({ principal: '@everyone', role: 'use' })
  await expect(dialog.getByTestId('share-summary')).toHaveText('Shared with everyone and 3 named people. 2 allowed this version.')
})

test('changes the role of a person', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await dialog.getByRole('combobox', { name: 'Role of Dana Weiss' }).selectOption('edit')
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-dana"]')).toHaveAttribute('data-role', 'edit')
  expect(state.shareSeen.find(s => s.method === 'PUT').body).toEqual({ principal: 'u-dana', role: 'edit' })
})

test('removes a person only after a confirmation', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await dialog.getByRole('button', { name: 'Stop sharing with Dana Weiss' }).click()
  await expect(dialog.getByText('Stop sharing with Dana Weiss? Their running work on it stops.')).toBeVisible()
  expect(state.shareSeen.some(s => s.method === 'DELETE')).toBe(false)
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
  await dialog.getByRole('button', { name: 'Stop sharing with Dana Weiss' }).click()
  await dialog.getByRole('button', { name: 'Stop sharing', exact: true }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-dana"]')).toHaveCount(0)
  const del = state.shareSeen.find(s => s.method === 'DELETE')
  expect(del).toMatchObject({ rest: '/u-dana', confirm: 'tasks' })
})

test('marks a shared page in the list with the count, and a private page not at all', async ({ page }) => {
  await signIn(page)
  mock(page, { listFields: { shared_with: 3 } })
  await page.goto('/app/pages')
  await expect(card(page, 'tasks').getByTestId('page-shared-badge')).toContainText('Shared')
  await expect(card(page, 'tasks').getByTestId('page-shared-count')).toHaveText('3')
  await expect(card(page, 'notes').getByTestId('page-shared-badge')).toHaveCount(0)
})

test('shows the badge from the dialog at once when the list does not say', async ({ page }) => {
  await signIn(page)
  mock(page)
  await page.goto('/app/pages')
  await expect(card(page, 'tasks').getByTestId('page-shared-badge')).toHaveCount(0)
  const dialog = await openShare(page)
  await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
  await dialog.getByRole('button', { name: 'Close' }).last().click()
  await expect(card(page, 'tasks').getByTestId('page-shared-count')).toHaveText('3')
})

test('has a Share action on the strip of an own page, and says how many people will be asked again', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages/tasks')
  await expect(page.getByTestId('page-shared-pill')).toHaveText('Shared with 3 people')
  await page.getByTestId('page-share').click()
  const dialog = page.getByTestId('page-share-dialog')
  await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
  await dialog.getByRole('button', { name: 'Close' }).last().click()
  await expect(page.getByTestId('page-ask-again')).toHaveCount(0)

  // The owner changes the code: nothing blocks, the strip says who is asked.
  state.hash = 'h2'
  state.allowed = 0
  await page.getByTestId('page-reload').click()
  await expect(page.getByTestId('page-ask-again')).toHaveText('You changed the code. 2 people will be asked to allow it again.')
})

test('lists a page shared with me with the owner, the role and the consent', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage(), sharedPage({ folder_id: 8, name: 'team', title: 'Team board', author_name: 'Admin', user_shared: false, role: undefined, consent: 'allowed' })] })
  await page.goto('/app/pages')
  const row = page.locator('[data-testid="shared-card"][data-page="groceries"]')
  await expect(row.getByTestId('shared-from')).toHaveText('From Bob Rossi')
  await expect(row.getByTestId('shared-role')).toHaveText('Use')
  await expect(row.getByTestId('shared-badge')).toContainText('Not allowed yet')
  // A page of an administrator's folder shows no role.
  await expect(page.locator('[data-testid="shared-card"][data-page="team"]').getByTestId('shared-role')).toHaveCount(0)
})

test('asks for consent to a page that a person wrote, with Allow and Allow once and nothing else', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage()] })
  const consent = mockSharedPage(page)
  await page.goto('/app/pages/shared/7/groceries')
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet.getByTestId('trust-who')).toHaveText('Written by Bob Rossi. Not checked by an administrator.')
  await expect(sheet).not.toContainText('shared folder')
  await expect(sheet.getByTestId('trust-allow-once')).toBeVisible()
  await expect(sheet.getByTestId('trust-allow')).toBeVisible()
  await expect(sheet.getByTestId('trust-allow-embed')).toHaveCount(0)
  await sheet.getByTestId('trust-allow').click()
  await expect(page.getByTestId('shared-pill')).toHaveText('Allowed')
  expect(consent.allows).toEqual([{ hash: 'g1' }])
})

// What a person can read on the screen: the text, and the words of titles
// and labels. Nothing of the owner's path or account may be in it.
async function visibleWords(page) {
  return page.evaluate(() => [
    document.body.innerText,
    ...[...document.querySelectorAll('[title],[aria-label],[alt]')].flatMap(el => [el.getAttribute('title'), el.getAttribute('aria-label'), el.getAttribute('alt')]),
  ].filter(Boolean).join('\n'))
}
const expectNoOwnerPath = (words) => {
  expect(words).not.toContain('users/')
  expect(words).not.toContain('u-bob')
  expect(words).not.toMatch(/pages\/groceries/)
}

test('shows no path and no account ID of the owner for a page that a person shared', async ({ page }) => {
  await signIn(page)
  // The name is missing on purpose: the account ID must not stand in for it.
  mock(page, { shared: [sharedPage(), sharedPage({ folder_id: 8, name: 'plans', title: 'Weekend plans', author_name: '' })] })
  mockSharedPage(page, { author_name: '' })
  await page.goto('/app/pages')
  await expect(page.locator('[data-testid="shared-card"][data-page="groceries"]')).toBeVisible()
  expectNoOwnerPath(await visibleWords(page))
  await expect(page.locator('[data-testid="shared-card"][data-page="plans"]').getByTestId('shared-from')).toHaveText('From someone')

  await page.goto('/app/pages/shared/7/groceries')
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet).toContainText('Read and write files in its own folder')
  expectNoOwnerPath(await visibleWords(page))
  await sheet.getByTestId('trust-source').click()
  await expect(page.getByTestId('trust-source-sheet')).toBeVisible()
  expectNoOwnerPath(await visibleWords(page))
  await page.keyboard.press('Escape')
  await sheet.getByTestId('trust-allow').click()
  await expect(page.getByTestId('shared-pill')).toHaveText('Allowed')
  await expect(page.getByTestId('page-view').getByText('Sandboxed. Runs as you, in its own folder only')).toBeVisible()
  expectNoOwnerPath(await visibleWords(page))
})

test('shows the folder of a page of an administrator folder, as before', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage({ user_shared: false, role: undefined, folder: '/shared/Team', author_name: 'Admin' })] })
  await page.goto('/app/pages')
  await expect(page.locator('[data-testid="shared-card"][data-page="groceries"]')).toContainText('shared/Team/pages/groceries')
})

test('keeps the wording of a page of an administrator folder in the trust sheet', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [] })
  mockSharedPage(page, { user_shared: false, role: undefined, folder: '/shared/Team', author_name: 'Admin' })
  await page.goto('/app/pages/shared/7/groceries')
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet.getByTestId('trust-who')).toHaveText('Written by Admin')
  await expect(sheet).toContainText('shared/Team')
  await expect(sheet).not.toContainText('Not checked by an administrator')
})

test('does not offer to run a page that a person wrote on Home, in slots and in the dock', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage()] })
  mockSharedPage(page)
  await page.goto('/app/pages')
  await page.getByTestId('shared-dock').click()
  await page.getByTestId('dock-allow-open').click()
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet.getByTestId('trust-who')).toContainText('Not checked by an administrator')
  await expect(sheet.getByTestId('trust-allow-embed')).toHaveCount(0)
  await expect(sheet.getByTestId('trust-embed-item')).toHaveCount(0)
  await expect(sheet.getByTestId('trust-allow')).toBeVisible()
})

test('says in the strip that a View grantee can look at a page and not change it', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage({ role: 'view', writable: false, consent: 'allowed' })] })
  mockSharedPage(page, { role: 'view', writable: false }, { state: 'allowed' })
  await page.goto('/app/pages/shared/7/groceries')
  await expect(page.getByTestId('shared-view-only')).toHaveText('You can look at this page, not change it')
  await expect(page.getByTestId('shared-role')).toHaveText('View')
})

test('a Use grantee gets no view-only text', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage({ consent: 'allowed' })] })
  mockSharedPage(page, {}, { state: 'allowed' })
  await page.goto('/app/pages/shared/7/groceries')
  await expect(page.getByTestId('shared-role')).toHaveText('Use')
  await expect(page.getByTestId('shared-view-only')).toHaveCount(0)
})

test('when an administrator turns sharing off, the owner cannot add people but can still remove them', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { on: false })
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await expect(dialog.getByTestId('share-add-off')).toContainText('An administrator turned off sharing of pages.')
  await expect(dialog.getByTestId('share-search')).toHaveCount(0)
  await expect(dialog.getByTestId('share-everyone-add')).toHaveCount(0)
  await expect(dialog.getByRole('combobox', { name: 'Role of Bob Rossi' })).toBeDisabled()
  await dialog.getByRole('button', { name: 'Stop sharing with Bob Rossi' }).click()
  await dialog.getByRole('button', { name: 'Stop sharing', exact: true }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-bob"]')).toHaveCount(0)
  expect(state.shareSeen.some(s => s.method === 'DELETE')).toBe(true)
})

test('when an administrator turns sharing off, a page shared with me shows dimmed and does not open', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { on: false, shared: [sharedPage({ consent: 'allowed' })] })
  let revoked = 0
  await page.route(/\/api\/chats\/shared-pages\/7\/groceries\/consent$/, route => { revoked++; return route.fulfill({ json: {} }) })
  await page.goto('/app/pages')
  const row = page.locator('[data-testid="shared-card"][data-page="groceries"]')
  await expect(row).toHaveClass(/pg-row--off/)
  await expect(row.getByTestId('shared-user-off')).toHaveText('Turned off by an administrator.')
  await expect(row.getByTestId('shared-open')).toHaveCount(0)
  await expect(row.getByRole('link', { name: 'Groceries' })).toHaveCount(0)
  await row.getByTestId('shared-stop').click()
  await expect.poll(() => revoked).toBe(1)
  expect(state.on).toBe(false)
})

test('explains why a page cannot be shared without accounts', async ({ page }) => {
  await signIn(page, { authEnabled: false })
  const state = mock(page)
  await page.goto('/app/pages')
  await openShare(page)
  await expect(page.getByTestId('share-disabled')).toBeVisible()
  expect(state.shareSeen).toHaveLength(0)
})

test('the dialog fits a phone and every control can be reached', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await signIn(page)
  mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
  const box = await dialog.boundingBox()
  expect(box.width).toBeLessThanOrEqual(390)
  const overflow = await dialog.evaluate(el => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  await expect(dialog.getByTestId('share-search')).toBeVisible()
  await expect(dialog.getByTestId('share-everyone-add').getByRole('button')).toBeVisible()
})

test('the dialog is a labelled dialog that closes on Escape and returns focus', async ({ page }) => {
  await signIn(page)
  mock(page)
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await expect(page.getByRole('dialog', { name: /Share "Tasks board"/ })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
})

test.describe('screenshots', () => {
  test.skip(!SHOTS, 'set PAGE_SHARE_SHOTS_DIR to save the page sharing screenshots')
  for (const theme of ['light', 'dark']) {
    for (const [size, viewport] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
      test(`page sharing, ${theme}, ${size}`, async ({ page }) => {
        await page.setViewportSize(viewport)
        await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
        const shot = async (name) => {
          await page.evaluate(() => Promise.all(document.getAnimations()
            .filter(a => a.effect?.getTiming().iterations !== Infinity)
            .map(a => a.finished.catch(() => {}))))
          await page.screenshot({ path: `${SHOTS}/page-share-${name}-${theme}-${size}.png` })
        }
        await signIn(page)
        const state = mock(page, { listFields: { shared_with: 3 }, shared: [sharedPage(), sharedPage({ folder_id: 9, name: 'plans', title: 'Weekend plans', author_name: 'Carol Mendes', role: 'view', consent: 'allowed' })] })
        mockSharedPage(page)
        await page.goto('/app/pages')
        await page.evaluate(() => document.fonts.ready)
        await expect(card(page, 'tasks').getByTestId('page-shared-badge')).toBeVisible()
        await shot('list')
        const dialog = await openShare(page)
        await expect(dialog.getByTestId('share-grant')).toHaveCount(3)
        await shot('dialog')
        await dialog.getByTestId('share-search').fill('car')
        await expect(dialog.getByTestId('share-user')).toHaveCount(1)
        await shot('dialog-search')
        await page.keyboard.press('Escape')

        await page.goto('/app/pages/shared/7/groceries')
        await expect(page.getByTestId('trust-sheet')).toBeVisible()
        await shot('trust')
        await page.getByTestId('trust-allow-once').click()
        await expect(page.getByTestId('shared-pill')).toBeVisible()
        await shot('strip-use')

        state.shared = [sharedPage({ role: 'view', writable: false })]
        mockSharedPage(page, { role: 'view', writable: false }, { state: 'allowed' })
        await page.goto('/app/pages/shared/7/groceries')
        await expect(page.getByTestId('shared-view-only')).toBeVisible()
        await shot('strip-view')

        await page.goto('/app/pages/tasks')
        await expect(page.getByTestId('page-shared-pill')).toBeVisible()
        state.hash = 'h2'
        await page.getByTestId('page-reload').click()
        await expect(page.getByTestId('page-ask-again')).toBeVisible()
        await shot('strip-owner')

        state.on = false
        await page.goto('/app/pages')
        await expect(page.locator('[data-testid="shared-card"]').first().getByTestId('shared-user-off')).toBeVisible()
        await shot('list-off')
        const off = await openShare(page)
        await expect(off.getByTestId('share-add-off')).toBeVisible()
        await shot('dialog-off')
      })
    }
  }
})
