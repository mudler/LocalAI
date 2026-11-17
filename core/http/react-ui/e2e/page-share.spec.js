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
// A page that others may edit reads a file when it starts and again on a click.
const GUARDED_HTML = `<!doctype html><body><p id="v">tasks</p><p id="d"></p><button id="r">read</button>
<script>
const show = (p) => p.then((f) => { document.getElementById('d').textContent = f.content }, (e) => { document.getElementById('d').textContent = 'ERR ' + e.message })
show(localai.files.read('data.txt'))
document.getElementById('r').onclick = () => show(localai.files.read('data.txt'))
</script></body>`
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
      { userId: 'u-bob', displayName: 'Bob Rossi', role: 'use', consent: 'allowed' },
      { userId: 'u-dana', displayName: 'Dana Weiss', role: 'view', consent: 'allowed' },
      { userId: 'u-eli', displayName: 'Eli Chen', role: 'edit', consent: 'none' },
    ],
    allowed: 2,
    shared: [],
    shareSeen: [],
    searches: [],
    edit: false,
    pageConsent: { state: 'none' },
    allows: [],
    stops: 0,
    fileSeen: [],
    ...init,
  }
  const answer = () => ({ name: 'tasks', enabled: state.on, hash: state.hash || 'h1', allowedCount: state.allowed, grants: state.grants })
  page.route(/\/api\/chats\/pages(\?.*)?$/, route => route.fulfill({
    json: { pages: state.pages.map(p => (p.name === 'tasks' ? { ...p, ...(state.edit ? { others_can_edit: true, hash: state.hash || 'h1', consent: state.pageConsent.state } : {}), ...(state.listFields || {}) } : p)) },
  }))
  page.route(/\/api\/chats\/pages\/tasks(\?.*)?$/, route => route.fulfill({
    json: state.edit
      ? {
        page: { ...TASKS, hash: state.hash || 'h1', shared_allowed: state.allowed, shared_with: state.grants.length, others_can_edit: true, code_files: [], consent: state.pageConsent.state },
        html: GUARDED_HTML, consent: state.pageConsent, lines: 12,
      }
      : { page: { ...TASKS, hash: state.hash || 'h1', shared_allowed: state.allowed }, html: HTML },
  }))
  page.route(/\/api\/chats\/pages\/tasks\/consent$/, route => {
    const req = route.request()
    if (req.method() === 'DELETE') {
      state.stops++
      state.pageConsent = { state: 'none' }
      return route.fulfill({ status: 204, body: '' })
    }
    const body = req.postDataJSON()
    state.allows.push(body)
    if (body.hash !== (state.hash || 'h1')) return route.fulfill({ status: 409, json: { error: { message: 'the page changed', type: 'conflict' } } })
    if (!body.once) state.pageConsent = { state: 'allowed' }
    return route.fulfill({ json: { hash: body.hash, ...(body.once ? { token: 'vt' } : {}) } })
  })
  page.route(/\/api\/chats\/pages\/tasks\/file(\?.*)?$/, route => {
    const h = route.request().headers()
    state.fileSeen.push({ hash: h['x-localai-page-hash'] || '', visit: h['x-localai-page-visit'] || '' })
    if (state.edit && !h['x-localai-page-hash']) return route.fulfill({ status: 403, json: { error: { message: 'allow it first', type: 'page_not_allowed' } } })
    if (state.edit && h['x-localai-page-hash'] !== (state.hash || 'h1')) return route.fulfill({ status: 409, json: { error: { message: 'the code changed', type: 'conflict' } } })
    return route.fulfill({ json: { path: 'data.txt', content: 'hello', version: 1 } })
  })
  page.route(/\/api\/chats\/pages\/shares\/search(\?.*)?$/, route => {
    const req = route.request()
    const q = new URL(req.url()).searchParams.get('q')
    state.searches.push({ q, confirm: req.headers()['x-localai-confirm'] || '' })
    const all = [{ id: 'u-carol', displayName: 'Carol Mendes' }, { id: 'u-bob', displayName: 'Bob Rossi' }]
    return route.fulfill({ json: { users: all.filter(u => u.displayName.toLowerCase().includes(String(q).toLowerCase())) } })
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
      const g = { userId: principal, displayName: names[principal] ?? principal, role, ...(principal === '@everyone' ? {} : { consent: 'none' }) }
      const i = state.grants.findIndex(x => x.userId === principal)
      if (i >= 0) state.grants[i] = { ...state.grants[i], role }
      else state.grants = principal === '@everyone' ? [g, ...state.grants] : [...state.grants, g]
    } else if (req.method() === 'DELETE') {
      const who = decodeURIComponent(rest.replace(/^\//, ''))
      state.grants = state.grants.filter(g => g.userId !== who)
    }
    return route.fulfill({ json: answer() })
  })
  page.route(/\/api\/chats\/shared-pages(\?.*)?$/, route => route.fulfill({
    json: { enabled: true, user_shares_enabled: state.on, pages: state.shared },
  }))
  return state
}

const sharedPage = (extra = {}) => ({
  folder_id: 7, folder: '', path: '', name: 'groceries', title: 'Groceries', icon: 'users', description: 'What to buy',
  author_id: 'u-bob', author_name: 'Bob Rossi', owner_name: 'Bob Rossi', user_shared: true, turned_off: false, role: 'use', consent: 'none', source: 'shared', hash: 'g1',
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
  expect(state.searches[0]).toEqual({ q: 'car', confirm: 'pages' })
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
  const state = mock(page, { listFields: { shared_with: 3 } })
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
  mock(page, { shared: [sharedPage(), sharedPage({ folder_id: 8, name: 'plans', title: 'Weekend plans', author_name: '', owner_name: '' })] })
  mockSharedPage(page, { author_name: '', owner_name: '' })
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

test('the dock says a page that a person wrote must be opened, and offers no Allow', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage()] })
  mockSharedPage(page)
  await page.goto('/app/pages')
  await page.getByTestId('shared-dock').click()
  await expect(page.getByTestId('dock-open-only')).toContainText('Open it from Pages to run it.')
  await expect(page.getByTestId('dock-allow-open')).toHaveCount(0)
  await page.getByTestId('dock-open-page').click()
  await expect(page).toHaveURL(/\/app\/pages\/shared\/7\/groceries$/)
  // The page's own screen still offers Allow and Allow once, never the third choice.
  await expect(page.getByTestId('trust-sheet').getByTestId('trust-allow-embed')).toHaveCount(0)
})

test('never sends the embed consent for a page that a person wrote', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage()] })
  const consent = mockSharedPage(page)
  await page.goto('/app/pages/shared/7/groceries')
  await page.getByTestId('trust-allow').click()
  await expect(page.getByTestId('shared-pill')).toHaveText('Allowed')
  expect(consent.allows.every(b => b.embed === undefined)).toBe(true)
})

test('names the owner of a shared page, then the author, then says someone, and never the account ID', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [
    sharedPage({ owner_name: 'Olga Ivanova', author_name: 'Bob Rossi' }),
    sharedPage({ folder_id: 8, name: 'second', title: 'Second', owner_name: '', author_name: 'Bob Rossi' }),
    sharedPage({ folder_id: 9, name: 'third', title: 'Third', owner_name: '', author_name: '', author_id: 'u-bob' }),
  ] })
  await page.goto('/app/pages')
  const from = (n) => page.locator(`[data-testid="shared-card"][data-page="${n}"]`).getByTestId('shared-from')
  await expect(from('groceries')).toHaveText('From Olga Ivanova')
  await expect(from('second')).toHaveText('From Bob Rossi')
  await expect(from('third')).toHaveText('From someone')
})

test('a page turned off by an administrator is dimmed and does not open, with sharing on for others', async ({ page }) => {
  await signIn(page)
  mock(page, { shared: [sharedPage({ turned_off: true }), sharedPage({ folder_id: 8, name: 'plans', title: 'Weekend plans' })] })
  await page.goto('/app/pages')
  const off = page.locator('[data-testid="shared-card"][data-page="groceries"]')
  await expect(off).toHaveClass(/pg-row--off/)
  await expect(off.getByTestId('shared-user-off')).toBeVisible()
  await expect(off.getByTestId('shared-open')).toHaveCount(0)
  await expect(page.locator('[data-testid="shared-card"][data-page="plans"]').getByTestId('shared-open')).toBeVisible()
})

test('says in the dialog that Edit opens the page through the allow step for the owner too', async ({ page }) => {
  await signIn(page)
  mock(page, { grants: [{ userId: 'u-bob', displayName: 'Bob Rossi', role: 'use', consent: 'allowed' }] })
  await page.goto('/app/pages')
  const dialog = await openShare(page)
  await expect(dialog.getByTestId('share-role-note')).toHaveCount(0)
  await dialog.getByTestId('share-new-role').selectOption('edit')
  await expect(dialog.getByTestId('share-role-note')).toHaveText('With Edit, this page opens through the allow step for you too.')
  // The note does not block: a person can still be added.
  await dialog.getByTestId('share-search').fill('car')
  await expect(dialog.getByTestId('share-user')).toHaveCount(1, { timeout: 5000 })
  await dialog.getByTestId('share-user').getByRole('button', { name: /Carol Mendes/ }).click()
  await expect(dialog.locator('[data-testid="share-grant"][data-user="u-carol"]')).toHaveAttribute('data-role', 'edit')
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

        // A page that others can edit: the list, the trust sheet, the strip, a change, the dock.
        state.on = true
        state.edit = true
        state.pageConsent = { state: 'none' }
        await page.goto('/app/pages')
        await expect(card(page, 'tasks').getByTestId('page-others-edit-badge')).toBeVisible()
        await shot('list-edit')
        await card(page, 'tasks').getByRole('link', { name: 'Tasks board' }).click()
        await expect(page.getByTestId('trust-sheet')).toBeVisible()
        await shot('trust-owner')
        await page.getByTestId('trust-allow').click()
        await expect(frame(page).locator('#d')).toHaveText('hello')
        await shot('strip-edit')
        state.hash = 'h4'
        state.pageConsent = { state: 'changed' }
        await page.getByTestId('page-reload').click()
        await expect(page.getByTestId('page-blocked')).toBeVisible()
        await shot('blocked-stale')
        await page.getByTestId('page-review').click()
        await expect(page.getByTestId('trust-sheet')).toBeVisible()
        await shot('trust-owner-changed')
        await page.keyboard.press('Escape')
        await page.goto('/app/pages')
        await card(page, 'tasks').getByRole('button', { name: /^More for / }).click()
        await page.getByRole('menuitem', { name: 'Open in dock' }).click()
        await expect(page.getByTestId('dock-open-only')).toBeVisible()
        await shot('dock-open-only')
      })
    }
  }
})

const frame = (page) => page.frameLocator('[data-testid="page-frame"]')

test('the owner opens a page that others can edit through the trust sheet, and it runs with the version in its calls', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, listFields: { shared_with: 3 } })
  await page.goto('/app/pages')
  await expect(card(page, 'tasks').getByTestId('page-others-edit-badge')).toHaveText('Others can edit')
  await card(page, 'tasks').getByRole('link', { name: 'Tasks board' }).click()
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toBeVisible()
  await expect(sheet.getByTestId('trust-who')).toHaveText('Others can edit this page. Check it before it runs.')
  await expect(sheet.getByTestId('trust-allow-once')).toBeVisible()
  await expect(sheet.getByTestId('trust-allow')).toBeVisible()
  await expect(sheet.getByTestId('trust-allow-embed')).toHaveCount(0)
  // The owner can write to the own page: no read-only note.
  await expect(sheet).not.toContainText('can only read')
  // Nothing ran and no file call was made before the owner allowed it.
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  expect(state.fileSeen).toHaveLength(0)
  await sheet.getByTestId('trust-allow').click()
  await expect(frame(page).locator('#d')).toHaveText('hello')
  expect(state.allows).toEqual([{ hash: 'h1' }])
  expect(state.fileSeen[0]).toEqual({ hash: 'h1', visit: '' })
  await expect(page.getByTestId('page-others-edit')).toHaveText('Others can edit')
  await expect(page.getByTestId('page-consent-pill')).toContainText('Allowed')
})

test('Allow once sends the visit token with every call', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, listFields: { shared_with: 1 } })
  await page.goto('/app/pages/tasks')
  await page.getByTestId('trust-allow-once').click()
  await expect(frame(page).locator('#d')).toHaveText('hello')
  expect(state.allows).toEqual([{ hash: 'h1', once: true }])
  expect(state.fileSeen[0]).toEqual({ hash: 'h1', visit: 'vt' })
})

test('an own page that nobody else can edit opens at once and sends no version header', async ({ page }) => {
  await signIn(page)
  const state = mock(page)
  await page.goto('/app/pages/tasks')
  await expect(frame(page).locator('#v')).toHaveText('tasks')
  await expect(page.getByTestId('trust-sheet')).toHaveCount(0)
  await expect(page.getByTestId('page-others-edit')).toHaveCount(0)
  expect(state.allows).toHaveLength(0)
  expect(state.fileSeen).toHaveLength(0)
})

test('an allowed version runs again at once, and Stop allowing takes it back', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, pageConsent: { state: 'allowed' } })
  await page.goto('/app/pages/tasks')
  await expect(page.getByTestId('trust-sheet')).toHaveCount(0)
  await expect(frame(page).locator('#d')).toHaveText('hello')
  await page.getByTestId('page-menu').click()
  await page.getByTestId('page-stop-allowing').click()
  await expect(page.getByTestId('page-blocked')).toContainText('You stopped allowing this page')
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  expect(state.stops).toBe(1)
})

test('when the code changes under a running page, it stops and the owner is asked again', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, pageConsent: { state: 'allowed' } })
  await page.goto('/app/pages/tasks')
  await expect(frame(page).locator('#d')).toHaveText('hello')
  // The next call of the running page is a conflict.
  state.hash = 'h2'
  state.pageConsent = { state: 'changed' }
  await frame(page).locator('#r').click()
  const blocked = page.getByTestId('page-blocked')
  await expect(blocked).toHaveAttribute('data-reason', 'stale')
  await expect(blocked).toContainText('This page changed. Review it again.')
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  await page.getByTestId('page-review').click()
  const sheet = page.getByTestId('trust-sheet')
  await expect(sheet).toHaveAttribute('data-variant', 'changed')
  await expect(sheet.getByTestId('trust-who')).toHaveText('This page changed since you allowed it.')
  await sheet.getByTestId('trust-allow').click()
  await expect(frame(page).locator('#d')).toHaveText('hello')
  expect(state.allows.at(-1)).toEqual({ hash: 'h2' })
})

test('a change found on reload asks again, and an allow of an old version shows the new one', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, pageConsent: { state: 'allowed' } })
  await page.goto('/app/pages/tasks')
  await expect(frame(page).locator('#d')).toHaveText('hello')
  state.hash = 'h2'
  state.pageConsent = { state: 'changed' }
  await page.getByTestId('page-reload').click()
  await expect(page.getByTestId('page-blocked')).toHaveAttribute('data-reason', 'stale')
  await page.getByTestId('page-review').click()
  // The code changes again while the sheet is open: the allow is refused with 409.
  state.hash = 'h3'
  await page.getByTestId('trust-allow').click()
  await expect(page.getByTestId('trust-sheet')).toContainText('The page changed while you were reviewing it.')
  await page.getByTestId('trust-allow').click()
  await expect(frame(page).locator('#d')).toHaveText('hello')
  expect(state.allows.map(a => a.hash)).toEqual(['h2', 'h3'])
})

test('after the last Edit is taken away the page opens as any own page', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, pageConsent: { state: 'allowed' } })
  await page.goto('/app/pages/tasks')
  await expect(frame(page).locator('#d')).toHaveText('hello')
  state.edit = false
  await page.getByTestId('page-reload').click()
  await expect(frame(page).locator('#v')).toHaveText('tasks')
  await expect(page.getByTestId('page-others-edit')).toHaveCount(0)
  await expect(page.getByTestId('trust-sheet')).toHaveCount(0)
})

test('the dock does not offer Allow for a page that others can edit', async ({ page }) => {
  await signIn(page)
  const state = mock(page, { edit: true, pageConsent: { state: 'allowed' } })
  await page.goto('/app/pages')
  await card(page, 'tasks').getByRole('button', { name: /^More for / }).click()
  await page.getByRole('menuitem', { name: 'Open in dock' }).click()
  await expect(page.getByTestId('dock-open-only')).toContainText('Others can edit this page. Open it from Pages to run it.')
  await expect(page.getByTestId('dock-allow-open')).toHaveCount(0)
  await expect(page.getByTestId('page-frame')).toHaveCount(0)
  expect(state.fileSeen).toHaveLength(0)
})
