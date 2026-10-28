import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'
import { offerMockModel, watchProblems, composer } from './journey-fixtures.js'

// Cross-cutting quality of the pages on the real test server: a quiet console,
// no sideways scroll on a phone, the redirects of the removed pages, the theme,
// the language and the keyboard. Selectors are roles and visible text.

// Pages of the shell. The test server runs no add-on host and is not
// distributed, so these two answers are expected and do not count.
const EXPECTED = [
  /Failed to load resource/, // the browser line has no address; the response check below names it
  /\/api\/addons\//, /\/api\/nodes/, /\/api\/collections/, /\/api\/(fine-tuning|quantization)\//,
  /notifications\/events/, /\/backends\/known/,
]

const ROUTES = [
  '/app', '/app/chats', '/app/chats/skills', '/app/chats/agents', '/app/chats/triggers', '/app/chats/connectors',
  '/app/chats/conversion', '/app/files', '/app/collections', '/app/build', '/app/operate', '/app/models',
  '/app/settings', '/app/usage', '/app/account', '/app/activity', '/app/traces', '/app/backends',
]

const PHONE = { width: 390, height: 844 }

test.describe('Every page of the shell', () => {
  for (const route of ROUTES) {
    test(`${route} loads quietly and fits a phone`, async ({ page }) => {
      await page.setViewportSize(PHONE)
      await offerMockModel(page)
      const watch = watchProblems(page, { ignore: EXPECTED })
      await page.goto(route)
      await expect(page.getByRole('heading', { level: 1 }).or(page.getByRole('heading', { level: 2 })).first()).toBeVisible({ timeout: 20_000 })
      // Let the lazy chunks and first reads settle before judging.
      await page.evaluate(() => new Promise(r => setTimeout(r, 750)))
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(1)
      expect(watch.problems()).toEqual([])
      // No translation key shows as text.
      const body = await page.locator('body').innerText()
      expect(body).not.toMatch(/\b(?:errors|composer|list|attach|pending|skillsPage|agentsPage|triggersPage|connectorsPage|mcpPage|confirmDelete)\.[a-zA-Z]+\b/)
    })
  }
})

test.describe('Removed pages', () => {
  const MAP = [
    ['/app/chat', /\/app\/chats$/],
    ['/app/chat/some-model', /\/app\/chats\?model=some-model$/],
    ['/app/chats/agents', /\/app\/agents$/],
    ['/app/chats/skills', /\/app\/skills$/],
    ['/app/chats/triggers', /\/app\/triggers$/],
    ['/app/agent-jobs', /\/app\/triggers$/],
  ]
  for (const [from, to] of MAP) {
    test(`${from} opens its current page`, async ({ page }) => {
      await page.goto(from)
      await expect(page).toHaveURL(to)
    })
  }
  test('an address that never existed shows the not-found page, not a blank one', async ({ page }) => {
    await page.goto('/app/no-such-page')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  })
})

test.describe('Theme and language', () => {
  test('the theme choice sticks across a reload', async ({ page }) => {
    await page.goto('/app')
    await page.getByRole('button', { name: /Switch to (dark|light) mode/ }).click()
    const after = await page.evaluate(() => document.documentElement.getAttribute('data-theme') || document.documentElement.className)
    await page.reload()
    const again = await page.evaluate(() => document.documentElement.getAttribute('data-theme') || document.documentElement.className)
    expect(again).toBe(after)
  })

  test('switching the language to Italian changes the sidebar and leaves no raw key behind', async ({ page }) => {
    await page.goto('/app')
    await page.getByRole('button', { name: 'Change language' }).click()
    await page.getByRole('option', { name: /Italiano/ }).click()
    await expect(page.getByRole('link', { name: 'File', exact: true })).toBeVisible()
    for (const route of ['/app/chats', '/app/agents', '/app/files', '/app/skills']) {
      await page.goto(route)
      await expect(page.getByRole('heading').first()).toBeVisible()
      const body = await page.locator('body').innerText()
      expect(body, route).not.toMatch(/\b(?:errors|composer|list|attach|agentsPage|skillsPage|triggersPage)\.[a-zA-Z]+\b/)
    }
  })

  test('a language the chats pages are not translated into shows English, not keys', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('i18nextLng', 'de'))
    await offerMockModel(page)
    await page.goto('/app/chats')
    await expect(composer(page)).toBeVisible()
    await expect(page.getByTestId('chat-send')).toBeVisible()
  })
})

test.describe('Keyboard', () => {
  test('Tab reaches the sidebar links and Enter follows one', async ({ page }) => {
    await page.goto('/app')
    const nav = page.getByRole('complementary', { name: 'Primary navigation' })
    await nav.getByRole('link', { name: 'Chats' }).focus()
    await expect(nav.getByRole('link', { name: 'Chats' })).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/chats$/)
  })

  test('on the chats page the composer takes keyboard input, Enter sends and Shift+Enter adds a line', async ({ page }) => {
    await offerMockModel(page)
    await page.goto('/app/chats')
    await composer(page).focus()
    await page.keyboard.type('line one')
    await page.keyboard.press('Shift+Enter')
    await page.keyboard.type('line two')
    await expect(composer(page)).toHaveValue('line one\nline two')
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
  })

  test('the Home slash menu opens with / and closes with Escape', async ({ page }) => {
    await mockHome(page)
    await page.goto('/app')
    const box = page.getByRole('combobox', { name: 'Message' }) // the Home command bar
    await box.focus()
    await page.keyboard.type('/')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeHidden()
  })

  test('the delete dialog holds the focus and Escape cancels it', async ({ page }) => {
    await page.goto('/app/skills')
    const r = await page.request.post('/api/chats/skills', { data: { layer: 'personal', name: `kb-${Date.now().toString(36)}`, description: 'x', body: 'y' } })
    test.skip(!r.ok(), 'files are off on this server build')
    await page.reload()
    await page.getByRole('button', { name: 'Delete' }).first().click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await page.keyboard.press('Tab')
    await expect(dialog.locator(':focus')).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
  })
})
