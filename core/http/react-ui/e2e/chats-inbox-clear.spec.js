import { test, expect } from '@playwright/test'
import { makeInbox, menuPick, openInbox, stubInbox } from './inbox-fixtures.js'

// The inbox of the chat header: clear all, clear read, per-item actions, more
// pages, and a second tab that follows the first.

test.describe('Inbox: clearing', () => {
  test('clear all asks first, says how many go, then empties the inbox and the badge', async ({ page }) => {
    const inbox = makeInbox(3, [1])
    await stubInbox(page, inbox)
    await openInbox(page)
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    await menuPick(page, 'Clear all')
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove all 3 notifications?')
    // Nothing is removed before the answer.
    expect(inbox.calls).toEqual([])
    await page.getByTestId('inbox-confirm').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('inbox-confirm')).toBeHidden()
    expect(inbox.rows).toHaveLength(3)

    await menuPick(page, 'Clear all')
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(0)
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    await expect(page.getByTestId('inbox-done')).toHaveText('Removed 3 notifications.')
    await expect(page.getByTestId('inbox-empty')).toContainText('No notifications yet')
    await page.getByTestId('inbox-menu').getByRole('button').click()
    await expect(page.getByRole('menuitem', { name: 'Clear all' })).toBeDisabled()
    expect(inbox.calls).toEqual([{ all: true }])
  })

  test('clear read keeps the unread ones', async ({ page }) => {
    const inbox = makeInbox(4, [1, 2])
    await stubInbox(page, inbox)
    await openInbox(page)
    await menuPick(page, 'Clear all')
    await page.getByTestId('inbox-confirm-read').click()
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove 2 read notifications?')
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    expect(inbox.calls).toEqual([{ all: true, read_only: true }])
  })

  test('the menu clears only the read ones straight away after the question', async ({ page }) => {
    const inbox = makeInbox(3, [1])
    await stubInbox(page, inbox)
    await openInbox(page)
    await menuPick(page, 'Clear read')
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove 1 read notification?')
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    expect(inbox.calls).toEqual([{ all: true, read_only: true }])
  })

  test('Escape backs out of the question before it closes the panel', async ({ page }) => {
    await stubInbox(page, makeInbox(2))
    await openInbox(page)
    await menuPick(page, 'Clear all')
    await expect(page.getByTestId('inbox-confirm')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('inbox-confirm')).toBeHidden()
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeHidden()
  })

  test('one item can be marked read or deleted', async ({ page }) => {
    const inbox = makeInbox(2)
    await stubInbox(page, inbox)
    await openInbox(page)
    // The newest is first.
    const first = page.getByTestId('inbox-item').first()
    await first.hover()
    await first.getByTestId('inbox-mark-read').click()
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    expect(inbox.reads).toEqual([{ ids: [2] }])
    await expect(first).toHaveAttribute('data-read', 'true')
    await first.hover()
    await first.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(1)
    expect(inbox.calls).toEqual([{ ids: [2] }])
  })

  test('a long inbox loads a page at a time', async ({ page }) => {
    await stubInbox(page, makeInbox(120))
    await openInbox(page)
    await expect(page.getByTestId('inbox-item')).toHaveCount(50)
    await expect(page.getByText('Showing 50 of 120')).toBeVisible()
    await page.getByTestId('inbox-more').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(100)
    await page.getByTestId('inbox-more').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(120)
    await expect(page.getByTestId('inbox-more')).toBeHidden()
    // The confirmation counts everything kept, not the page on screen.
    await menuPick(page, 'Clear all')
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove all 120 notifications?')
  })

  test('a clear in another tab updates this one through the stream', async ({ page }) => {
    const inbox = makeInbox(3)
    await stubInbox(page, inbox)
    await openInbox(page)
    await expect(page.getByTestId('inbox-count')).toHaveText('3')
    inbox.rows = []
    await page.evaluate(() => window.__inboxEmit('notifications_changed'))
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    await expect(page.getByTestId('inbox-item')).toHaveCount(0)
  })
})
