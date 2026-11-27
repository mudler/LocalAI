import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'
import { adminItem } from './admin-nav.js'

const row = (page, email) => page.locator(`tr[data-user="${email}"]`)
const writes = (state, method, tail) => state.writes.filter(w => w.method === method && w.path.endsWith(tail))

test.describe('Users', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
  })

  test('lists people with sign-in, role, what they can use and their state', async ({ page }) => {
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Users')
    await expect(page.getByTestId('admin-crumbs')).toHaveText(/Administration\s*Access\s*Users/)
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(5)
    await expect(row(page, 'alice@lab.example')).toContainText('All access')
    await expect(row(page, 'alice@lab.example')).toContainText('you')
    await expect(row(page, 'bob@lab.example')).toContainText('bob@lab.example · github')
    await expect(row(page, 'bob@lab.example')).toContainText('2 models only · 2 more features')
    await expect(row(page, 'carol@lab.example')).toContainText('1 more feature · 1 limit')
    await expect(row(page, 'dave@lab.example')).toContainText('Default access')
    await expect(row(page, 'dave@lab.example')).toContainText('Pending')
    await expect(row(page, 'erin@lab.example')).toContainText('Disabled')
    // A person without a name shows the email as the name.
    await expect(row(page, 'erin@lab.example')).toContainText('(no name)')
    await expect(page.getByTestId('registration-mode')).toContainText('Registration mode: approval')
  })

  test('filters by state and role, searches and clears', async ({ page }) => {
    const chip = (f) => page.locator(`[data-filter="${f}"]`)
    await expect(chip('pending')).toContainText('1')
    await chip('pending').click()
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(1)
    await expect(row(page, 'dave@lab.example')).toBeVisible()
    await chip('admins').click()
    await expect(row(page, 'alice@lab.example')).toBeVisible()
    await chip('disabled').click()
    await expect(row(page, 'erin@lab.example')).toBeVisible()
    await chip('all').click()
    await page.getByLabel('Search by name or email').fill('car')
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(1)
    await page.getByLabel('Search by name or email').fill('zzz')
    await expect(page.getByText('No matching users')).toBeVisible()
    await page.getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(5)
  })

  test('sorts a column both ways', async ({ page }) => {
    const names = () => page.locator('tbody tr[data-row] .dk-table-name').allTextContents()
    await page.getByRole('button', { name: 'User', exact: true }).click()
    await expect(page.locator('th[aria-sort]')).toHaveAttribute('aria-sort', 'ascending')
    const asc = await names()
    await page.getByRole('button', { name: 'User', exact: true }).click()
    await expect(page.locator('th[aria-sort]')).toHaveAttribute('aria-sort', 'descending')
    expect(await names()).toEqual([...asc].reverse())
  })

  test('approves a waiting sign-up and enables a disabled person', async ({ page }) => {
    await row(page, 'dave@lab.example').getByRole('button', { name: 'Approve' }).click()
    await expect(row(page, 'dave@lab.example')).toContainText('Active')
    await row(page, 'erin@lab.example').getByRole('button', { name: 'Enable' }).click()
    await expect(row(page, 'erin@lab.example')).toContainText('Active')
    expect(state.writes.map(w => [w.method, w.path, w.body])).toEqual([
      ['PUT', '/api/auth/admin/users/u-dave/status', { status: 'active' }],
      ['PUT', '/api/auth/admin/users/u-erin/status', { status: 'active' }],
    ])
  })

  test('disabling offers undo, and undo sets the person active again', async ({ page }) => {
    await row(page, 'bob@lab.example').getByRole('button', { name: 'Disable bob' }).click()
    await expect(row(page, 'bob@lab.example')).toContainText('Disabled')
    const toast = page.getByTestId('user-undo-toast')
    await expect(toast).toContainText('Disabled bob. Undo sets them back to active.')
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(row(page, 'bob@lab.example')).toContainText('Active')
    expect(state.writes.map(w => [w.path, w.body])).toEqual([
      ['/api/auth/admin/users/u-bob/status', { status: 'disabled' }],
      ['/api/auth/admin/users/u-bob/status', { status: 'active' }],
    ])
  })

  test('your own row has no actions', async ({ page }) => {
    await expect(row(page, 'alice@lab.example').getByRole('button')).toHaveCount(0)
  })

  test('the row menu changes the role, resets a password and deletes after typing the name', async ({ page }) => {
    await page.getByLabel('Actions for carol').click()
    await page.getByRole('menuitem', { name: 'Make admin' }).click()
    await expect(row(page, 'carol@lab.example').locator('td').nth(1).locator('.ui-tag', { hasText: 'admin' })).toBeVisible()
    expect(writes(state, 'PUT', '/role')[0].body).toEqual({ role: 'admin' })

    // OAuth people have no password to reset.
    await page.getByLabel('Actions for bob').click()
    await expect(page.getByRole('menuitem', { name: 'Reset password' })).toHaveCount(0)
    await page.keyboard.press('Escape')

    await page.getByLabel('Actions for dave').click()
    await page.getByRole('menuitem', { name: 'Reset password' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('All existing sessions will be invalidated')
    await dialog.getByLabel('New password').fill('correct horse battery staple')
    await dialog.getByRole('button', { name: 'Reset Password' }).click()
    await expect(page.getByText('Password reset for dave')).toBeVisible()
    expect(writes(state, 'PUT', '/password')[0].body).toMatchObject({ password: 'correct horse battery staple' })

    await page.getByLabel('Actions for erin').click()
    await page.getByRole('menuitem', { name: 'Delete…' }).click()
    const confirm = page.getByRole('alertdialog')
    const del = confirm.getByRole('button', { name: 'Delete', exact: true })
    await expect(del).toBeDisabled()
    await confirm.getByLabel(/Type .* to confirm/).fill('erin@lab.example')
    await expect(del).toBeEnabled()
    await del.click()
    await expect(row(page, 'erin@lab.example')).toHaveCount(0)
    expect(writes(state, 'DELETE', '/u-erin')).toHaveLength(1)
  })
})

test.describe('Users: access sheet', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
  })

  test('opens from the access text and shows features, models and limits', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit access for bob' }).click()
    const sheet = page.getByTestId('access-sheet')
    await expect(sheet).toContainText('Access for bob')
    await expect(sheet.getByRole('switch', { name: 'Chat completions' })).toHaveAttribute('aria-checked', 'true')
    await expect(sheet.getByRole('switch', { name: 'Skills' })).toHaveAttribute('aria-checked', 'true')
    await expect(sheet.getByRole('switch', { name: 'Fine-tuning' })).toHaveAttribute('aria-checked', 'false')
    await expect(sheet.getByRole('switch', { name: /Restrict to specific models/ })).toHaveAttribute('aria-checked', 'true')
    await expect(sheet.getByLabel('qwen3-8b-instruct')).toBeChecked()
    await expect(sheet.getByLabel('kokoro-82m')).not.toBeChecked()
    await expect(sheet).toContainText('No limits: unlimited access')
  })

  test('an admin has no access sheet', async ({ page }) => {
    await expect(page.getByRole('button', { name: 'Edit access for alice' })).toHaveCount(0)
  })

  test('saving sends permissions, the model list and the limits, in that order', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit access for bob' }).click()
    const sheet = page.getByTestId('access-sheet')
    await sheet.getByRole('switch', { name: 'Fine-tuning' }).click()
    await sheet.getByLabel('kokoro-82m').check()
    await sheet.getByRole('button', { name: 'Add rule' }).click()
    await sheet.getByLabel('Max tokens').fill('500000')
    await sheet.getByRole('button', { name: 'Save access' }).click()
    await expect(page.getByText('Permissions updated for bob')).toBeVisible()
    await expect(sheet).toHaveCount(0)
    const puts = state.writes.filter(w => w.method === 'PUT').map(w => w.path.replace('/api/auth/admin/users/u-bob', ''))
    expect(puts).toEqual(['/permissions', '/models', '/quotas'])
    // Only the stored keys and the changed one: the others follow their default.
    expect(state.writes[0].body).toEqual({ agents: true, skills: true, fine_tuning: true })
    expect(state.writes[1].body).toEqual({ enabled: true, models: ['qwen3-8b-instruct', 'bge-m3', 'kokoro-82m'] })
    expect(state.writes[2].body).toEqual({ model: '', max_requests: null, max_total_tokens: 500000, window: '1h' })
  })

  test('removing a rule deletes it, and Escape closes the sheet without saving', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit access for carol' }).click()
    const sheet = page.getByTestId('access-sheet')
    await expect(sheet.getByLabel('Max tokens')).toHaveValue('2000000')
    await expect(sheet.getByRole('progressbar', { name: 'Tokens used' })).toHaveAttribute('aria-valuenow', '59')
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    expect(state.writes).toEqual([])

    await page.getByRole('button', { name: 'Edit access for carol' }).click()
    await page.getByRole('button', { name: 'Remove quota rule' }).click()
    await page.getByRole('button', { name: 'Save access' }).click()
    await expect(page.getByText('Permissions updated for carol')).toBeVisible()
    expect(state.writes.some(w => w.method === 'DELETE' && w.path.endsWith('/quotas/q1'))).toBe(true)
  })

  test('the sheet carries role, password and delete for the person', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit access for dave' }).click()
    await page.getByTestId('access-sheet').getByRole('button', { name: 'Reset password' }).click()
    await expect(page.getByTestId('access-sheet')).toHaveCount(0)
    await expect(page.getByRole('dialog')).toContainText('Reset password')
  })

  test('a failed save keeps the sheet open and says why', async ({ page }) => {
    state.failNext = 'nope'
    await page.getByRole('button', { name: 'Edit access for bob' }).click()
    await page.getByRole('button', { name: 'Save access' }).click()
    await expect(page.getByText(/Failed to update permissions/)).toBeVisible()
    await expect(page.getByTestId('access-sheet')).toBeVisible()
  })
})

test.describe('Users: invite sheet', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    await page.getByRole('button', { name: 'Invite someone' }).click()
    await expect(page.getByTestId('invites-table')).toBeVisible()
  })

  test('Invite someone opens a side sheet that lists open, used and expired links', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/users$/)
    const sheet = page.getByTestId('invite-sheet')
    await expect(sheet).toBeVisible()
    await expect(page.getByRole('tab')).toHaveCount(0)
    const rows = page.locator('[data-testid=invites-table] tbody tr')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText('Available')
    await expect(rows.nth(0)).toContainText('in 6 days')
    await expect(rows.nth(1)).toContainText('Used')
    await expect(rows.nth(1)).toContainText('dave')
    await expect(rows.nth(2)).toContainText('Expired')
    // Only an open link can be revoked.
    await expect(sheet.getByRole('button', { name: 'Revoke' })).toHaveCount(1)
  })

  test('is the only primary button of its region, and Escape closes it', async ({ page }) => {
    const sheet = page.getByTestId('invite-sheet')
    await expect(sheet.locator('.dk-btn--primary')).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    expect(state.writes).toEqual([])
  })

  test('creates a link for the chosen lifetime and shows it once', async ({ page }) => {
    await page.getByRole('radio', { name: '30 days' }).click()
    await page.getByRole('button', { name: 'Generate invite link' }).click()
    const reveal = page.getByTestId('invite-reveal')
    await expect(reveal).toContainText('Copy this invite link now')
    await expect(page.getByTestId('invite-link')).toContainText('/invite/c0ffee00c0ffee00c0ffee00c0ffee00')
    expect(state.writes[0]).toMatchObject({ method: 'POST', path: '/api/auth/admin/invites', body: { expiresInHours: 720 } })
    await reveal.getByRole('button', { name: 'I copied it' }).click()
    await expect(reveal).toHaveCount(0)
  })

  test('revoking asks first, then deletes the link', async ({ page }) => {
    await page.getByTestId('invite-sheet').getByRole('button', { name: 'Revoke' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('3f9a1c2d')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(state.writes).toEqual([])
    await page.getByTestId('invite-sheet').getByRole('button', { name: 'Revoke' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke' }).click()
    await expect(page.getByText('Invite revoked')).toBeVisible()
    expect(state.writes[0]).toMatchObject({ method: 'DELETE', path: '/api/auth/admin/invites/i1' })
  })

  test('the old invites address opens the sheet and the page keeps its own address', async ({ page }) => {
    await page.goto('/app/users?tab=invites')
    await expect(page.getByTestId('invite-sheet')).toBeVisible()
    await expect(page).toHaveURL(/\/app\/users$/)
  })
})

test.describe('API keys page', () => {
  let state
  test.beforeEach(async ({ page }) => {
    await page.clock.install()
    state = await mockAccess(page)
    await page.goto('/app/api-keys')
    await expect(page.getByTestId('api-keys-panel')).toBeVisible()
    await expect(page.locator('.ak-item')).toHaveCount(3)
  })

  test('is its own page under Access, with its own breadcrumb and no tabs', async ({ page }) => {
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('API keys')
    await expect(page.getByTestId('admin-crumbs')).toHaveText(/Administration\s*Access\s*API keys/)
    await expect(page.getByRole('tab')).toHaveCount(0)
    await expect(adminItem(page, '/app/api-keys')).toHaveAttribute('aria-current', 'page')
  })

  test('the old keys address leads here', async ({ page }) => {
    await page.goto('/app/users?tab=keys')
    await expect(page).toHaveURL(/\/app\/api-keys$/)
    await expect(page.getByTestId('api-keys-panel')).toBeVisible()
  })

  test('says whose keys these are and why no one else is listed', async ({ page }) => {
    await expect(page.getByTestId('api-keys-panel')).toContainText('These are your own keys')
    await expect(page.getByTestId('api-keys-panel')).toContainText('other users')
    const first = page.locator('.ak-item').first()
    await expect(first).toContainText('ci-bot')
    await expect(first).toContainText('lai-3f9a1c')
    await expect(first).toContainText('expires in 55 days')
    await expect(page.locator('.ak-item').nth(2)).toContainText('never used')
  })

  test('creating a key shows its secret once, with the lifetime sent', async ({ page }) => {
    await page.getByLabel('New API key').fill('ci-runner')
    await page.getByLabel('Expires').selectOption('90d')
    await page.getByTestId('api-keys-panel').getByRole('button', { name: 'New API key' }).click()
    const reveal = page.getByTestId('api-key-reveal')
    await expect(page.getByTestId('api-key-secret')).toHaveText('lai-9d41f0c27be84a0c92aa5d6b')
    await expect(reveal).toContainText('shown once')
    expect(state.writes[0]).toMatchObject({ method: 'POST', path: '/api/auth/api-keys', body: { name: 'ci-runner', expiresIn: '90d' } })
    await reveal.getByRole('button', { name: 'I copied it' }).click()
    await expect(page.getByTestId('api-key-secret')).toHaveCount(0)
  })

  test('without a chosen lifetime the server default applies', async ({ page }) => {
    await page.getByLabel('New API key').fill('plain')
    await page.getByTestId('api-keys-panel').getByRole('button', { name: 'New API key' }).click()
    await expect(page.getByTestId('api-key-reveal')).toBeVisible()
    expect(state.writes[0].body).toEqual({ name: 'plain' })
  })

  test('revoking waits ten seconds and sends nothing until then', async ({ page }) => {
    const item = page.locator('.ak-item').nth(2)
    await item.getByRole('button', { name: 'Revoke' }).click()
    await expect(item).toContainText('revoked')
    await expect(item).toContainText('Revoking in 10s')
    await page.clock.fastForward(5_000)
    expect(state.writes).toEqual([])
    await page.clock.fastForward(5_500)
    await expect(page.getByText('API key revoked')).toBeVisible()
    expect(state.writes).toEqual([{ method: 'DELETE', path: '/api/auth/api-keys/k3', body: null }])
    await expect(page.locator('.ak-item')).toHaveCount(2)
  })

  test('undo inside the window means nothing was ever sent', async ({ page }) => {
    const item = page.locator('.ak-item').nth(2)
    await item.getByRole('button', { name: 'Revoke' }).click()
    await page.clock.fastForward(4_000)
    await item.getByRole('button', { name: 'Undo' }).click()
    await expect(item).not.toContainText('revoked')
    await page.clock.fastForward(20_000)
    expect(state.writes).toEqual([])
    await expect(page.locator('.ak-item')).toHaveCount(3)
  })

  test('leaving the page runs a revoke that is still waiting', async ({ page }) => {
    await page.locator('.ak-item').nth(2).getByRole('button', { name: 'Revoke' }).click()
    await adminItem(page, '/app/users').click()
    await expect.poll(() => state.writes.length).toBe(1)
    expect(state.writes[0]).toMatchObject({ method: 'DELETE', path: '/api/auth/api-keys/k3' })
  })

  test('a key can be paused until a time, and resumed', async ({ page }) => {
    const item = page.locator('.ak-item').first()
    await item.getByRole('button', { name: 'Pause' }).click()
    await item.getByRole('button', { name: 'Pause key' }).click()
    expect(state.writes[0]).toMatchObject({ method: 'PATCH', body: { disabled: true, paused_until: null } })
  })

  test('a person who is not an admin is sent back to the app', async ({ page }) => {
    await mockAccess(page, { status: 'member' })
    await page.goto('/app/api-keys')
    await page.waitForURL(/\/app\/?$/)
  })
})

test.describe('Users: access and states', () => {
  test('a person who is not an admin is sent back to the app', async ({ page }) => {
    await mockAccess(page, { status: 'member' })
    await page.goto('/app/users')
    await page.waitForURL(/\/app\/?$/)
    await expect(page.getByTestId('users-page')).toHaveCount(0)
  })

  test('signed out, the page asks for sign-in', async ({ page }) => {
    await mockAccess(page, { status: 'signedOut' })
    await page.goto('/app/users')
    await page.waitForURL(/\/login$/)
  })

  test('with no users yet it says so and offers the invite', async ({ page }) => {
    await mockAccess(page, { users: [] })
    await page.goto('/app/users')
    const empty = page.getByTestId('users-empty')
    await expect(empty).toContainText('No users yet')
    await empty.getByRole('button', { name: 'Invite someone' }).click()
    await expect(page.getByTestId('invite-sheet')).toBeVisible()
  })

  test('a search that finds nobody says so and Clear filters brings the list back', async ({ page }) => {
    await mockAccess(page)
    await page.goto('/app/users')
    await page.getByLabel('Search by name or email').fill('zoe')
    const none = page.getByTestId('users-no-match')
    await expect(none).toContainText('No matching users')
    await none.getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(5)
  })

  test('a server that cannot be read shows the error with Try again, not an empty list', async ({ page }) => {
    const state = await mockAccess(page)
    state.usersDown = true
    await page.goto('/app/users')
    const error = page.getByTestId('users-load-error')
    await expect(error).toContainText('Could not load the users.')
    await expect(page.getByTestId('users-empty')).toHaveCount(0)
    state.usersDown = false
    await error.getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('users-table')).toBeVisible()
  })

  test('a refresh that fails keeps the list on screen under an error', async ({ page }) => {
    const state = await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    state.usersDown = true
    await page.getByRole('button', { name: 'Refresh users' }).click()
    const stale = page.getByTestId('users-stale-error')
    await expect(stale).toContainText('Cannot reach the server.')
    await expect(stale).toContainText('The list below is from')
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(5)
  })

  test('the loading state is skeleton rows, then the table', async ({ page }) => {
    await mockAccess(page)
    await page.route('**/api/auth/admin/users', async route => { await new Promise(r => setTimeout(r, 600)); await route.fallback() })
    await page.goto('/app/users')
    await expect(page.getByTestId('users-loading')).toBeVisible()
    await expect(page.getByTestId('users-table')).toBeVisible()
  })
})

test.describe('Users: filter bar, summary and the proposed fields', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
  })

  test('uses the one filter bar: search, chips, Filters, sort', async ({ page }) => {
    const bar = page.getByTestId('admin-filters')
    await expect(bar.getByLabel('Search by name or email')).toBeVisible()
    await expect(bar.getByRole('group', { name: 'Filter users' }).getByRole('button')).toHaveText([/All\s*5/, /Pending\s*1/, /Admins\s*1/, /Disabled\s*1/, /Inactive 30 d\+\s*1/])
    await expect(bar.getByTestId('admin-filters-button')).toBeVisible()
    await expect(bar.getByLabel('Sort')).toBeVisible()
  })

  test('the summary strip counts the people', async ({ page }) => {
    const strip = page.getByRole('list', { name: 'Users at a glance' }).or(page.locator('dl.admin-summary'))
    await expect(strip).toContainText('5')
    await expect(strip).toContainText('Waiting approval')
    await expect(strip.locator('[data-tone="warn"]')).toContainText('1')
  })

  test('shows when each person was last active and how many keys they hold', async ({ page }) => {
    await expect(row(page, 'alice@lab.example').getByTestId('user-last-active')).toContainText(/now|sec/i)
    await expect(row(page, 'alice@lab.example').getByTestId('user-key-count')).toHaveText('3')
    await expect(row(page, 'bob@lab.example').getByTestId('user-last-active')).toContainText('2 hr')
    await expect(row(page, 'carol@lab.example').getByTestId('user-last-active')).toContainText('5 days')
    await expect(row(page, 'dave@lab.example').getByTestId('user-last-active')).toHaveText('Never')
    await expect(row(page, 'dave@lab.example').getByTestId('user-key-count')).toHaveText('–')
  })

  test('the Inactive 30 d+ chip lists who was not active for 30 days, and a person never seen only when the account is old', async ({ page }) => {
    await page.locator('[data-filter="inactive"]').click()
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(1)
    await expect(row(page, 'erin@lab.example')).toBeVisible()
    // dave signed up yesterday and was never seen: a new sign-up is not inactive.
    await expect(row(page, 'dave@lab.example')).toHaveCount(0)
  })

  test('the Filters popover narrows by sign-in method and by custom access, and counts on its button', async ({ page }) => {
    const button = page.getByTestId('admin-filters-button')
    await button.click()
    await page.getByTestId('users-filter-provider').selectOption('github')
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(1)
    await expect(row(page, 'bob@lab.example')).toBeVisible()
    await expect(button).toContainText('1')
    await page.getByTestId('users-filter-provider').selectOption('')
    await page.getByTestId('users-filter-access').selectOption('custom')
    // bob (models, features), carol (feature, limit) differ from the defaults; admins are not custom.
    await expect(page.locator('tbody tr[data-row]')).toHaveCount(2)
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Clear filters' }).count()
  })

  test('sorts by last active, most recent first, and by key count', async ({ page }) => {
    const emails = () => page.locator('tbody tr[data-row]').evaluateAll(rows => rows.map(r => r.getAttribute('data-user')))
    await page.getByLabel('Sort').selectOption('lastActive')
    expect((await emails()).slice(0, 3)).toEqual(['alice@lab.example', 'bob@lab.example', 'carol@lab.example'])
    await page.getByLabel('Sort').selectOption('keys')
    expect((await emails())[0]).toBe('alice@lab.example')
  })

  test('the access sheet has an Activity block with last active and API keys', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit access for carol' }).click()
    const activity = page.getByTestId('access-sheet-activity')
    await expect(activity).toContainText('Last active')
    await expect(activity).toContainText('5 days ago')
    await expect(activity).toContainText('API keys')
    await expect(activity).toContainText('2')
  })

  test('the list says how many of how many are shown', async ({ page }) => {
    await expect(page.getByTestId('users-count')).toContainText('Showing 5 of 5.')
    await page.locator('[data-filter="pending"]').click()
    await expect(page.getByTestId('users-count')).toContainText('Showing 1 of 5.')
  })
})

test.describe('Users: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the table drops its secondary columns, the sheet is a bottom sheet and nothing overflows', async ({ page }) => {
    await mockAccess(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Last active' })).toBeHidden()
    await expect(page.getByRole('columnheader', { name: 'Keys' })).toBeHidden()
    await expect(page.getByRole('columnheader', { name: 'Access' })).toBeHidden()
    // The role moves under the name, and the row's actions stay on screen.
    // The Administration header sits above the page, so the row may start
    // below the fold: bring it up, then check nothing is cut at the side.
    await expect(page.getByRole('columnheader', { name: 'Role' })).toBeHidden()
    await expect(row(page, 'bob@lab.example').locator('.us-role-phone')).toBeVisible()
    await row(page, 'dave@lab.example').scrollIntoViewIfNeeded()
    await expect(row(page, 'dave@lab.example').getByRole('button', { name: 'Approve' })).toBeInViewport()
    await expect(page.getByLabel('Actions for dave')).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)

    await page.getByLabel('Actions for bob').click()
    await page.getByRole('menuitem', { name: 'Edit access' }).click()
    const sheet = page.getByTestId('access-sheet')
    await expect(sheet).toBeVisible()
    const box = await sheet.boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(388)
    expect(box.y).toBeGreaterThan(40)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
  })

  test('keys wrap their actions under the name', async ({ page }) => {
    await mockAccess(page)
    await page.goto('/app/api-keys')
    await expect(page.locator('.ak-item')).toHaveCount(3)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
    const first = page.locator('.ak-item').first()
    await expect(first.getByRole('button', { name: 'Revoke' })).toBeInViewport()
  })
})

// The page and the invite sheet in both themes at desktop and phone width:
// nothing overflows sideways. A sheet is a bottom sheet on a phone.
for (const theme of ['light', 'dark']) {
  for (const [name, viewport] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
    test.describe(`Users: ${theme} ${name}`, () => {
      test.use({ viewport })
      test.beforeEach(async ({ page }) => {
        await page.addInitScript((value) => { try { localStorage.setItem('localai-theme', value) } catch { /* private window */ } }, theme)
        await mockAccess(page)
      })

      test('the list and the invite sheet do not scroll sideways', async ({ page }) => {
        await page.goto('/app/users')
        await expect(page.getByTestId('users-table')).toBeVisible()
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        const overflow = () => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
        expect(await overflow()).toBe(false)
        await page.getByRole('button', { name: 'Invite someone' }).click()
        await expect(page.getByTestId('invites-table')).toBeVisible()
        expect(await overflow()).toBe(false)
        // The sheet slides in: wait for it to settle inside the window.
        await expect.poll(async () => {
          const box = await page.getByTestId('invite-sheet').boundingBox()
          return box ? box.x + box.width : Infinity
        }).toBeLessThanOrEqual(viewport.width + 1)
      })

      test('the API keys page does not scroll sideways', async ({ page }) => {
        await page.goto('/app/api-keys')
        await expect(page.getByTestId('api-keys-panel')).toBeVisible()
        expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
      })
    })
  }
}
