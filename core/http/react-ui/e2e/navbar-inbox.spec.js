import { test, expect } from './coverage-fixtures.js'
import { inboxButton, makeInbox, stubInbox } from './inbox-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'

// The notification inbox in the app shell: one entry in the sidebar (an icon
// with a corner badge on the rail, a bell in the header of a phone) that opens
// one panel from any page. An administrator always has it, anyone else once
// they have items, nobody when chats are off. The API and the stream are stubbed.

const ago = (min) => new Date(Date.now() - min * 60_000).toISOString()

function rows() {
  const inbox = makeInbox(0)
  inbox.rows = [
    { id: 3, kind: 'needs_attention', status: 'failed', source: 'system', title: 'whisper update failed', body: 'download interrupted', link: '/app/models', read: false, created: ago(2) },
    { id: 2, kind: 'needs_attention', status: 'ok', source: 'system', title: 'Two updates are ready', body: '', link: '/app/models', read: false, created: ago(30) },
    { id: 1, kind: 'run_ended', status: 'ok', source: 'trigger', title: 'Old run', body: '', link: '/app/chats/r1', read: true, created: ago(60 * 30) },
  ]
  return inbox
}

const panelOf = (page) => page.getByRole('dialog', { name: 'Notifications' })

// A person who is not an administrator; chats on or off for them.
async function asMember(page, { chats = true } = {}) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true, ...(chats ? { chats: true } : {}) } } }),
  }))
}

// Counts the reads of the inbox hook (a page of 50; the recent chats of the
// sidebar read the unread ones with a limit of their own).
function countLists(page) {
  const seen = { lists: 0 }
  page.on('request', (req) => { if (/\/api\/chats\/notifications\?.*limit=50/.test(req.url()) && req.method() === 'GET') seen.lists += 1 })
  return seen
}

test.describe('Navbar inbox: who sees it', () => {
  test('an administrator sees the entry on a page that is not Chat, with the count, red for a failure', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app')
    const entry = page.getByTestId('nav-inbox')
    await expect(entry).toBeVisible()
    await expect(entry).toContainText('Notifications')
    await expect(entry).toHaveAccessibleName('Notifications, 2 unread')
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    // An unread item failed: the badge is the red one.
    await expect(page.getByTestId('inbox-count')).toHaveClass(/nav-badge--error/)
  })

  test('the badge is the plain one when no unread item failed', async ({ page }) => {
    const inbox = rows()
    inbox.rows[0].status = 'ok'
    await stubInbox(page, inbox)
    await page.goto('/app')
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    await expect(page.getByTestId('inbox-count')).not.toHaveClass(/nav-badge--error/)
  })

  test('an administrator with an empty inbox still has the entry, and the panel says what will show up', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await page.goto('/app')
    await expect(inboxButton(page)).toBeVisible()
    await expect(page.getByTestId('inbox-count')).toHaveCount(0)
    await inboxButton(page).click()
    await expect(page.getByTestId('inbox-empty')).toBeVisible()
  })

  test('someone who is not an administrator and has no items does not see it', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await asMember(page)
    const seen = countLists(page)
    await page.goto('/app')
    await expect.poll(() => seen.lists).toBeGreaterThan(0)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
    await expect(page.getByTestId('mobile-inbox')).toHaveCount(0)
  })

  test('someone who is not an administrator sees it when they have items, and keeps it after clearing', async ({ page }) => {
    await stubInbox(page, rows())
    await asMember(page)
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await page.getByTestId('nav-inbox').click()
    await page.getByTestId('inbox-menu').getByRole('button').click()
    await page.getByRole('menuitem', { name: /Clear all/ }).click()
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-empty')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
  })

  test('nobody sees it when chats are off on the server, and the server is asked nothing more', async ({ page }) => {
    await stubInbox(page, rows())
    await page.route('**/api/features', route => route.fulfill({ json: { chats: false } }))
    const seen = countLists(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav').first()).toBeVisible()
    await page.waitForTimeout(500)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
    expect(seen.lists).toBe(0)
    expect(await page.evaluate(() => window.__inboxStreams().opened)).toBe(0)
  })

  test('a server that answers 409 has no inbox: no entry, no retry', async ({ page }) => {
    await stubInbox(page, rows())
    const seen = countLists(page)
    await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ status: 409, json: { error: { message: 'chats are off' } } }))
    await page.goto('/app')
    await expect.poll(() => seen.lists).toBeGreaterThan(0)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
  })

  test('a person without the chats feature makes no request and has no stream', async ({ page }) => {
    await stubInbox(page, rows())
    await asMember(page, { chats: false })
    const seen = countLists(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav').first()).toBeVisible()
    await page.waitForTimeout(500)
    expect(seen.lists).toBe(0)
    expect(await page.evaluate(() => window.__inboxStreams().opened)).toBe(0)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
  })

  test('a 403 means off: it is asked a time or two and then never again', async ({ page }) => {
    await stubInbox(page, rows())
    await asMember(page)
    const seen = countLists(page)
    await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill({ status: 403, json: { error: { message: 'forbidden' } } }))
    await page.goto('/app')
    await expect.poll(() => seen.lists).toBeGreaterThan(0)
    await page.waitForTimeout(500)
    const settled = seen.lists
    // The first read and the one the stream's open triggers may both be in flight.
    expect(settled).toBeLessThanOrEqual(2)
    await page.waitForTimeout(2500)
    expect(seen.lists).toBe(settled)
    expect(await page.evaluate(() => window.__inboxStreams().open)).toBe(0)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
  })

  test('the Chat page header has no bell of its own any more', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await expect(page.getByRole('button', { name: /^Notifications/ })).toHaveCount(1)
    await expect(page.locator('.cx-hd').getByRole('button', { name: /^Notifications/ })).toHaveCount(0)
  })
})

test.describe('Navbar inbox: use', () => {
  test('the panel opens beside the sidebar, an item marks read and opens its page and closes the panel', async ({ page }) => {
    const inbox = rows()
    await stubInbox(page, inbox)
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    const panel = panelOf(page)
    await expect(panel).toBeVisible()
    await expect(page.getByTestId('nav-inbox')).toHaveAttribute('aria-expanded', 'true')
    const box = await panel.boundingBox()
    const side = await page.locator('.sidebar').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(side.x + side.width)
    // The panel is not inside the sidebar or the main area.
    expect(await panel.evaluate(el => Boolean(el.closest('.sidebar, main')))).toBe(false)
    await panel.getByRole('button', { name: /^Two updates are ready/ }).click()
    await expect(panel).toBeHidden()
    await expect(page).toHaveURL(/\/app\/models$/)
    expect(inbox.reads).toEqual([{ ids: [2] }])
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
  })

  test('mark read and delete work from a page that is not Chat', async ({ page }) => {
    const inbox = rows()
    await stubInbox(page, inbox)
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    const item = page.locator('[data-testid="inbox-item"][data-id="2"]')
    await item.hover()
    await item.getByTestId('inbox-mark-read').click()
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    await item.getByTestId('inbox-delete').click()
    await expect(item).toHaveCount(0)
    expect(inbox.calls).toEqual([{ ids: [2] }])
  })

  test('a second click on the entry closes the panel', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    await expect(panelOf(page)).toBeVisible()
    await page.getByTestId('nav-inbox').click()
    await expect(panelOf(page)).toBeHidden()
    await expect(page.getByTestId('nav-inbox')).toHaveAttribute('aria-expanded', 'false')
  })

  test('a click outside closes the panel', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    await expect(panelOf(page)).toBeVisible()
    await page.locator('.main-content').click({ position: { x: 600, y: 300 } })
    await expect(panelOf(page)).toBeHidden()
  })

  test('the keyboard opens the panel, moves in it, and Escape returns focus to the entry', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app')
    await page.getByTestId('nav-inbox').focus()
    await page.keyboard.press('Enter')
    const panel = panelOf(page)
    await expect(panel).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(panel.locator('.nx-item__main').first()).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(panel.locator('.nx-item__main').nth(1)).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(panel).toBeHidden()
    await expect(page.getByTestId('nav-inbox')).toBeFocused()
  })

  test('an arrival is announced and counted on a page that is not Chat', async ({ page }) => {
    const inbox = rows()
    await stubInbox(page, inbox)
    await page.goto('/app')
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    inbox.rows.unshift({ id: 9, kind: 'needs_attention', status: 'ok', source: 'system', title: 'Backups finished', body: '', link: '', read: false, created: new Date().toISOString() })
    await page.evaluate(() => window.__inboxEmit('notification'))
    await expect(page.getByTestId('inbox-count')).toHaveText('3')
    const live = page.getByTestId('inbox-live')
    await expect(live).toHaveText('New notification: Backups finished')
    await expect(live).toHaveAttribute('aria-live', 'polite')
  })

  test('the stream is open only while the tab is visible', async ({ page }) => {
    await stubInbox(page, rows())
    await page.goto('/app')
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    const streams = () => page.evaluate(() => window.__inboxStreams())
    expect((await streams()).open).toBe(1)
    const setVisibility = (state) => page.evaluate((v) => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => v })
      document.dispatchEvent(new Event('visibilitychange'))
    }, state)
    await setVisibility('hidden')
    await expect.poll(async () => (await streams()).open).toBe(0)
    await setVisibility('visible')
    await expect.poll(async () => (await streams()).open).toBe(1)
  })
})

test.describe('Navbar inbox: widths', () => {
  test('on the tablet rail the entry is an icon with the badge on its corner', async ({ page }) => {
    await page.setViewportSize({ width: 800, height: 800 })
    await stubInbox(page, rows())
    await page.goto('/app')
    const entry = page.getByTestId('nav-inbox')
    await expect(entry).toBeVisible()
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    const e = await entry.boundingBox()
    const b = await page.getByTestId('inbox-count').boundingBox()
    // The badge sits inside the icon tile, on its upper right corner.
    expect(b.x + b.width).toBeLessThanOrEqual(e.x + e.width + 1)
    expect(b.y).toBeLessThan(e.y + e.height / 2)
    await entry.click()
    const panel = await panelOf(page).boundingBox()
    const rail = await page.locator('.sidebar').boundingBox()
    expect(panel.x).toBeGreaterThanOrEqual(rail.x + rail.width)
  })

  test('on a phone the bell is in the header, with the count, and opens the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await stubInbox(page, rows())
    await page.goto('/app')
    const bell = page.getByTestId('mobile-inbox')
    await expect(bell).toBeVisible()
    await expect(bell).toHaveAccessibleName('Notifications, 2 unread')
    await expect(page.getByTestId('mobile-inbox-count')).toHaveText('2')
    const box = await bell.boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(44)
    expect(box.height).toBeGreaterThanOrEqual(44)
    await bell.click()
    const panel = panelOf(page)
    await expect(panel).toBeVisible()
    await expect.poll(async () => { const b = await panel.boundingBox(); return Math.round(b.y + b.height) }).toBe(800)
    await page.keyboard.press('Escape')
    await expect(panel).toBeHidden()
    await expect(bell).toBeFocused()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })

  test('on a phone the entry in the drawer closes the drawer and opens the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await stubInbox(page, rows())
    await page.goto('/app')
    await page.getByRole('button', { name: 'Open menu' }).click()
    await expect(page.locator('.sidebar.open')).toBeVisible()
    await page.getByTestId('nav-inbox').click()
    await expect(page.locator('.sidebar.open')).toHaveCount(0)
    const panel = panelOf(page)
    await expect(panel).toBeVisible()
    await expect(panel).toBeFocused()
    // Closing returns to the page, not to the hidden drawer entry.
    await panel.getByRole('button', { name: 'Close' }).click()
    await expect(panel).toBeHidden()
    await expect(page.locator('main h1').first()).toBeFocused()
  })

  test('on a phone an empty inbox shows no bell for someone who is not an administrator', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await stubInbox(page, makeInbox(0))
    await asMember(page)
    await page.goto('/app')
    await expect(page.getByRole('button', { name: 'Open menu' })).toBeVisible()
    await expect(page.getByTestId('mobile-inbox')).toHaveCount(0)
  })
})
