import { test, expect } from './coverage-fixtures.js'
import { projectCalls, setupViewAs } from './view-as-fixtures.js'

// View as: an administrator looks at, and with a second step acts on, another
// person's projects. The Users and Projects APIs are stubbed; the specs check
// the bar, the confirmation, which requests carry user_id, what read-only
// stops, and the marks on the pages that do and do not follow.

const row = (page, email) => page.locator(`tr[data-user="${email}"]`)
const bar = (page) => page.getByTestId('viewas-bar')

async function enterFromUsers(page, who = 'alice@lab.example') {
  await page.goto('/app/users')
  await expect(page.getByTestId('users-table')).toBeVisible()
  await row(page, who).getByTestId('user-viewas').click()
  await expect(page).toHaveURL(/\/app\/projects$/)
  await expect(bar(page)).toBeVisible()
}

async function allowChanges(page) {
  await page.getByTestId('viewas-allow').click()
  await expect(page.getByTestId('viewas-confirm')).toBeVisible()
  await page.getByTestId('viewas-confirm-allow').click()
  await expect(bar(page)).toHaveAttribute('data-mode', 'actas')
}

test.describe('View as', () => {
  test('the Users page offers it on active, other, non-admin rows only', async ({ page }) => {
    await setupViewAs(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    for (const email of ['alice@lab.example', 'bob@lab.example', 'carol@lab.example']) {
      await expect(row(page, email).getByTestId('user-viewas')).toBeVisible()
    }
    // Not the viewer, not another administrator, not a person waiting or disabled.
    for (const email of ['morgan@lab.example', 'riley@lab.example', 'dave@lab.example', 'erin@lab.example']) {
      await expect(row(page, email).getByTestId('user-viewas')).toHaveCount(0)
    }
    await expect(row(page, 'alice@lab.example').getByTestId('user-viewas')).toHaveAccessibleName('View as alice')
    // The row menu has the same item.
    await row(page, 'bob@lab.example').getByRole('button', { name: /Actions for/ }).click()
    await expect(page.getByRole('menuitem', { name: 'View as' })).toBeVisible()
  })

  test('entering shows a read-only bar, adds user_id to the project routes and marks the pages that follow', async ({ page }) => {
    const { seen } = await setupViewAs(page)
    await enterFromUsers(page)
    await expect(bar(page)).toHaveAttribute('data-mode', 'readonly')
    await expect(page.getByTestId('viewas-status')).toHaveText(/READ-ONLY\s*Viewing as alice\. Read-only\./i)
    await expect(page.getByTestId('viewas-tag')).toHaveText('Read-only')
    await expect(page.getByTestId('viewas-allow')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('viewas-chip')).toHaveText("alice's data")
    await expect(page.getByTestId('project-row').first()).toBeVisible()
    // The list is read as alice; nothing else gets the parameter.
    expect(projectCalls(seen, 'GET').some(c => c.user === 'u-alice')).toBe(true)
    expect(seen.filter(c => !c.path.startsWith('/api/chats/projects') && c.user)).toEqual([])
    // The nav marks Projects, and the legend explains the mark.
    await expect(page.getByTestId('viewas-mark')).toHaveCount(1)
    await expect(page.locator('a.nav-item', { hasText: 'Projects' }).getByTestId('viewas-mark')).toBeVisible()
    await expect(page.getByTestId('viewas-legend')).toContainText("Pages with this mark show alice's data. The others show yours.")
    // The shared-projects routes belong to the viewer: no user_id there.
    expect(projectCalls(seen).filter(c => c.path.includes('/shared') && c.user)).toEqual([])
  })

  test('read-only stops writes: controls are aria-disabled with a hint, requests never leave', async ({ page }) => {
    const { seen } = await setupViewAs(page)
    await enterFromUsers(page)
    const create = page.getByTestId('project-new')
    await expect(create).toHaveAttribute('aria-disabled', 'true')
    await expect(create).not.toHaveAttribute('disabled', /.*/)
    await expect(create).toHaveAccessibleDescription('Allow changes to edit')
    // A keyboard reaches it, and pressing it does nothing.
    await create.focus()
    await expect(create).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    // The central guard stops a write that a control did not guard.
    const outcome = await page.evaluate(async () => {
      try { await fetch('/api/chats/projects', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"name":"x"}' }); return 'sent' } catch (e) { return e.message }
    })
    expect(outcome).toContain("alice's data")
    expect(projectCalls(seen, 'POST')).toEqual([])
    await expect(page.getByText('Turn on Allow changes in the bar')).toBeVisible()
  })

  test('Allow changes asks first, Stay read-only is the default, acting writes on the account and is undone without a question', async ({ page }) => {
    const { seen } = await setupViewAs(page)
    await enterFromUsers(page)
    await page.getByTestId('viewas-allow').click()
    const dialog = page.getByTestId('viewas-confirm')
    await expect(dialog).toContainText("Allow changes on alice's account?")
    await expect(page.getByTestId('viewas-stay')).toBeFocused()
    await page.getByTestId('viewas-stay').click()
    await expect(dialog).toHaveCount(0)
    await expect(bar(page)).toHaveAttribute('data-mode', 'readonly')

    await allowChanges(page)
    await expect(page.getByTestId('viewas-tag')).toHaveText('Acting')
    await expect(page.getByTestId('viewas-status')).toContainText("Acting as alice. Changes are made on alice's account and are logged.")
    await expect(page.getByTestId('viewas-allow')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('viewas-chip')).toHaveText("alice's data")

    // A write works, as alice.
    await page.getByTestId('project-new').click()
    await page.getByLabel('Name').first().fill('Garden planner')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => projectCalls(seen, 'POST').length).toBe(1)
    expect(projectCalls(seen, 'POST')[0].user).toBe('u-alice')

    // Off needs no confirmation.
    await page.goto('/app/projects')
    await expect(bar(page)).toHaveAttribute('data-mode', 'readonly')
    await allowChanges(page)
    await page.getByTestId('viewas-allow').click()
    await expect(bar(page)).toHaveAttribute('data-mode', 'readonly')
    await expect(page.getByTestId('viewas-confirm')).toHaveCount(0)
  })

  test('a reload keeps the person and goes back to read-only', async ({ page }) => {
    await setupViewAs(page)
    await enterFromUsers(page, 'bob@lab.example')
    await allowChanges(page)
    await page.reload()
    await expect(bar(page)).toHaveAttribute('data-mode', 'readonly')
    await expect(page.getByTestId('viewas-status')).toContainText('Viewing as bob. Read-only.')
  })

  test('Exit clears the mode: no bar, no marks, no user_id', async ({ page }) => {
    const { seen } = await setupViewAs(page)
    await enterFromUsers(page)
    await page.getByTestId('viewas-exit').click()
    await expect(bar(page)).toHaveCount(0)
    await expect(page.getByTestId('viewas-mark')).toHaveCount(0)
    await expect(page.getByTestId('viewas-chip')).toHaveCount(0)
    expect(await page.evaluate(() => sessionStorage.getItem('localai.viewAs'))).toBeNull()
    seen.length = 0
    await page.goto('/app/projects')
    await expect(page.getByTestId('project-row').first()).toBeVisible()
    expect(projectCalls(seen).filter(c => c.user)).toEqual([])
    await expect(page.getByTestId('project-new')).not.toHaveAttribute('aria-disabled', 'true')
  })

  test('a page that does not follow says so, and the bar stays on Administration', async ({ page }) => {
    await setupViewAs(page)
    await enterFromUsers(page)
    await page.goto('/app/users')
    await expect(bar(page)).toBeVisible()
    await expect(page.getByTestId('viewas-notice')).toHaveText("This page shows your own data, not alice's.")
    await expect(page.getByTestId('viewas-chip')).toHaveCount(0)
    await page.goto('/app/projects')
    await expect(page.getByTestId('viewas-notice')).toHaveCount(0)
  })

  test('the command palette offers View as for people who can be viewed', async ({ page }) => {
    await setupViewAs(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    await page.keyboard.press('Control+k')
    await page.getByRole('combobox', { name: /Search pages/ }).fill('alice')
    const option = page.getByRole('option', { name: /View as alice/ })
    await expect(option).toBeVisible()
    await expect(page.getByRole('option', { name: /riley|morgan|dave|erin/ })).toHaveCount(0)
    await page.getByRole('combobox', { name: /Search pages/ }).fill('morgan')
    await expect(page.getByRole('option', { name: /View as/ })).toHaveCount(0)
    await page.getByRole('combobox', { name: /Search pages/ }).fill('alice')
    await option.click()
    await expect(page).toHaveURL(/\/app\/projects$/)
    await expect(page.getByTestId('viewas-status')).toContainText('Viewing as alice.')
  })

  test('a viewed person who is gone ends the mode with a message', async ({ page }) => {
    await setupViewAs(page)
    await page.route(/\/api\/chats\/projects(\?.*)?$/, (route) => {
      if (new URL(route.request().url()).searchParams.get('user_id')) {
        return route.fulfill({ status: 404, json: { error: { message: 'user not found', code: 404, type: 'not_found_error' } } })
      }
      return route.fallback()
    })
    await page.goto('/app/users')
    await row(page, 'carol@lab.example').getByTestId('user-viewas').click()
    await expect(page.getByText('carol is not available any more, so View as ended.')).toBeVisible()
    await expect(bar(page)).toHaveCount(0)
  })

  test('a missing project is not a missing person: the mode stays', async ({ page }) => {
    await setupViewAs(page)
    await enterFromUsers(page)
    await page.goto('/app/projects/p-nope')
    await expect(page.getByTestId('viewas-bar')).toBeVisible()
  })

  test('on a phone the bar wraps, and View as is in the row menu only', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await setupViewAs(page)
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    await expect(row(page, 'alice@lab.example').getByTestId('user-viewas')).toBeHidden()
    await row(page, 'alice@lab.example').getByRole('button', { name: /Actions for/ }).click()
    await page.getByRole('menuitem', { name: 'View as' }).click()
    await expect(bar(page)).toBeVisible()
    await expect(page.getByTestId('viewas-exit')).toBeVisible()
    const box = await bar(page).boundingBox()
    expect(box.x).toBe(0)
    expect(box.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
})
