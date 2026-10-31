import { test, expect } from '@playwright/test'
import { inboxButton, makeInbox, openInbox, stubInbox } from './inbox-fixtures.js'

// The notification panel of the chat header: filters, day groups, opening an
// item, keyboard use, the empty, caught-up and error states, live arrivals
// and the phone sheet.

const ago = (min) => new Date(Date.now() - min * 60_000).toISOString()

function mixedInbox() {
  const inbox = makeInbox(0)
  inbox.rows = [
    { id: 5, kind: 'run_ended', status: 'ok', source: 'trigger', title: 'News digest finished', body: 'Twelve stories.', link: '/app/chats/r5', read: false, created: ago(2) },
    { id: 4, kind: 'run_failed', status: 'failed', source: 'trigger', title: 'Inbox summary failed', body: 'took longer than 15m', link: '/app/chats/r4', read: false, created: ago(30) },
    { id: 3, kind: 'needs_attention', status: 'failed', source: 'connector', title: 'Team chat was switched off', body: 'The service refused the sign-in.', link: '', read: true, created: ago(60 * 24 + 5) },
    { id: 2, kind: 'needs_attention', status: 'ok', source: 'system', title: 'Your old chats were converted', body: '14 items converted.', link: '/app/chats/conversion', read: true, created: ago(60 * 24 * 5) },
  ]
  // A first entry at 23:59 would be "yesterday" a minute later: keep the
  // newest two today by moving them to the start of the day when it is early.
  const startOfDay = new Date(); startOfDay.setHours(0, 0, 0, 0)
  for (const r of inbox.rows.slice(0, 2)) if (Date.parse(r.created) < startOfDay.getTime()) r.created = new Date(startOfDay.getTime() + 60_000).toISOString()
  const yesterday = new Date(startOfDay.getTime() - 12 * 3600_000)
  inbox.rows[2].created = yesterday.toISOString()
  return inbox
}

test.describe('Inbox panel', () => {
  test('items are grouped by day with a title, the source and a reason for failures', async ({ page }) => {
    await stubInbox(page, mixedInbox())
    await openInbox(page)
    const panel = page.getByRole('dialog', { name: 'Notifications' })
    await expect(panel.getByRole('heading', { level: 2 })).toHaveText('Notifications')
    await expect(panel.getByText('2 unread', { exact: true })).toBeVisible()
    await expect(panel.getByRole('heading', { level: 3 })).toHaveText(['Today', 'Yesterday', 'Earlier'])
    const today = panel.getByRole('region', { name: 'Today' })
    await expect(today.getByTestId('inbox-item')).toHaveCount(2)
    // A failure leads with its reason, capitalised; the unread state is in the name.
    const failed = today.getByTestId('inbox-item').nth(1)
    await expect(failed).toHaveClass(/is-failed/)
    await expect(failed).toContainText('Took longer than 15m')
    await expect(failed.getByTestId('inbox-open')).toHaveAccessibleName('Inbox summary failed, unread')
    await expect(panel.getByRole('region', { name: 'Yesterday' })).toContainText('Connector')
    await expect(panel.getByRole('region', { name: 'Earlier' })).toContainText('LocalAI')
    // The exact time is on hover.
    await expect(today.locator('time').first()).toHaveAttribute('title', /\d/)
  })

  test('the filters show unread or failed items and say when there are none', async ({ page }) => {
    const inbox = mixedInbox()
    await stubInbox(page, inbox)
    await openInbox(page)
    await page.getByTestId('inbox-filter-unread').click()
    await expect(page.getByTestId('inbox-filter-unread')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    expect(inbox.lists.some(q => q.includes('unread=1'))).toBe(true)

    await page.getByTestId('inbox-filter-failed').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    await expect(page.getByTestId('inbox-item').first()).toContainText('Inbox summary failed')
    await expect(page.getByTestId('inbox-item').nth(1)).toContainText('Team chat was switched off')
    // Failed looks through everything kept, not one page.
    expect(inbox.lists.at(-1)).toContain('limit=500')

    // Read everything: the unread view says so.
    await page.getByTestId('inbox-mark-all').click()
    await page.getByTestId('inbox-filter-unread').click()
    await expect(page.getByTestId('inbox-caught-up')).toContainText("You're all caught up")
    await page.getByTestId('inbox-filter-all').click()
    await expect(page.getByTestId('inbox-caught-up-note')).toBeVisible()
    await expect(inboxButton(page)).toHaveAccessibleName('Notifications')
  })

  test('a click on an item marks it read and opens what it is about', async ({ page }) => {
    const inbox = mixedInbox()
    await stubInbox(page, inbox)
    await page.route('**/api/chats/r5', route => route.fulfill({ json: { id: 'r5', title: 'News', model: 'qwen3-8b', kind: 'chat', created: ago(3), updated: ago(2), running: false, pending: [] } }))
    await page.route('**/api/chats/r5/messages*', route => route.fulfill({ json: { messages: [] } }))
    await openInbox(page)
    await page.getByTestId('inbox-item').first().getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/chats\/r5$/)
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeHidden()
    expect(inbox.reads).toContainEqual({ ids: [5] })
  })

  test('an item with nowhere to go is only marked read; a connector opens the connectors page', async ({ page }) => {
    const inbox = mixedInbox()
    inbox.rows[2].read = false
    await stubInbox(page, inbox)
    await openInbox(page)
    await page.getByRole('region', { name: 'Yesterday' }).getByTestId('inbox-open').click()
    await expect(page).toHaveURL(/\/app\/chats\/connectors$/)
    expect(inbox.reads).toContainEqual({ ids: [3] })
  })

  test('the keyboard moves through the items, deletes one and closes the panel', async ({ page }) => {
    const inbox = makeInbox(3)
    await stubInbox(page, inbox)
    await openInbox(page)
    // Focus is in the panel when it opens.
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeFocused()
    await page.keyboard.press('ArrowDown')
    const opens = page.getByTestId('inbox-open')
    await expect(opens.nth(0)).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(opens.nth(1)).toBeFocused()
    await page.keyboard.press('End')
    await expect(opens.nth(2)).toBeFocused()
    await page.keyboard.press('Home')
    await expect(opens.nth(0)).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Delete')
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    expect(inbox.calls).toEqual([{ ids: [2] }])
    // Focus stays in the list, on the item that took its place.
    await expect(opens.nth(1)).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeHidden()
    await expect(inboxButton(page)).toBeFocused()
  })

  test('an empty inbox says what will show up here', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await openInbox(page)
    await expect(page.getByTestId('inbox-empty')).toContainText('No notifications yet')
    await expect(page.getByTestId('inbox-empty')).toContainText('Results of your triggers and connectors appear here')
    // Nothing to filter or mark yet.
    await expect(page.getByTestId('inbox-filter-all')).toBeHidden()
    await expect(page.getByTestId('inbox-mark-all')).toBeHidden()
  })

  test('a list that fails to load says so and loads again on Try again', async ({ page }) => {
    const inbox = makeInbox(2)
    inbox.fail = true
    await stubInbox(page, inbox)
    await openInbox(page)
    const err = page.getByTestId('inbox-load-error')
    await expect(err).toContainText('Could not load your notifications.')
    await expect(err).toContainText('the database is busy')
    // A failed load never looks like an empty inbox.
    await expect(page.getByTestId('inbox-empty')).toBeHidden()
    inbox.fail = false
    await err.getByRole('button', { name: 'Try again' }).click()
    await expect(err).toBeHidden()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
  })

  test('a new notification slides in at the top, is announced and updates the badge', async ({ page }) => {
    const inbox = makeInbox(2, [1, 2])
    await stubInbox(page, inbox)
    await openInbox(page)
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    inbox.rows.unshift({ id: 3, kind: 'run_failed', status: 'failed', source: 'trigger', title: 'Backup check failed', body: 'the disk is full', link: '/app/chats/r3', read: false, created: new Date().toISOString() })
    await page.evaluate(() => window.__inboxEmit('notification'))
    const first = page.getByTestId('inbox-item').first()
    await expect(first).toContainText('Backup check failed')
    await expect(first).toHaveClass(/is-fresh/)
    await expect(page.getByTestId('inbox-live')).toHaveText('New notification: Backup check failed')
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    // The highlight fades after a few seconds.
    await expect(first).not.toHaveClass(/is-fresh/, { timeout: 8000 })
  })

  test('the badge stops at 99+', async ({ page }) => {
    await stubInbox(page, makeInbox(120))
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('inbox-count')).toHaveText('99+')
    await expect(inboxButton(page)).toHaveAccessibleName('Notifications, 120 unread')
  })

  test('on a phone the panel is a sheet with a close button and large targets', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubInbox(page, mixedInbox())
    await openInbox(page)
    const panel = page.getByRole('dialog', { name: 'Notifications' })
    // It rises from the bottom edge; measure it once it is in place.
    await expect.poll(async () => { const b = await panel.boundingBox(); return Math.round(b.y + b.height) }).toBe(844)
    const box = await panel.boundingBox()
    expect(box.x).toBe(0)
    expect(Math.round(box.width)).toBe(390)
    const close = panel.getByRole('button', { name: 'Close' })
    const cb = await close.boundingBox()
    expect(cb.width).toBeGreaterThanOrEqual(44)
    expect(cb.height).toBeGreaterThanOrEqual(44)
    await close.click()
    await expect(panel).toBeHidden()
  })
})
