import { test, expect } from '@playwright/test'
import { mockChat } from './chat-fixtures.js'

// The inbox of the chat header: clear all, clear read, per-item actions, more
// pages, and a second tab that follows the first. The server is faked with a
// small stateful inbox; the notification stream is a fake EventSource the spec
// drives with window.__inboxEmit(name).

const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, pending: [] }

function makeInbox(count, readIds = []) {
  const rows = Array.from({ length: count }, (_, i) => ({
    id: i + 1, kind: 'run_ended', status: 'ok', title: `Run ${i + 1}`, body: '', link: `/app/chats/r${i + 1}`,
    read: readIds.includes(i + 1), created: '2026-10-28T08:00:00Z',
  }))
  return { rows, calls: [] }
}

async function stub(page, inbox) {
  await mockChat(page)
  await page.addInitScript(() => {
    const sources = []
    window.EventSource = class {
      constructor() { this.listeners = {}; sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn) }
      close() {}
    }
    window.__inboxEmit = (name) => sources.forEach(s => (s.listeners[name] || []).forEach(fn => fn({})))
  })
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [detail], persistent: true, files: true } }))
  await page.route('**/api/chats/c1/messages*', route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route('**/api/chats/push/**', route => route.fulfill({ status: 409, json: { error: { message: 'off' } } }))
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, async (route) => {
    const req = route.request()
    const unread = () => inbox.rows.filter(r => !r.read).length
    if (req.method() === 'DELETE') {
      const body = req.postDataJSON()
      inbox.calls.push(body)
      const before = inbox.rows.length
      if (body.all) inbox.rows = inbox.rows.filter(r => body.read_only && !r.read)
      else inbox.rows = inbox.rows.filter(r => !body.ids.includes(r.id))
      return route.fulfill({ json: { unread: unread(), deleted: before - inbox.rows.length } })
    }
    const limit = Number(new URL(req.url()).searchParams.get('limit') || 50)
    return route.fulfill({ json: { notifications: inbox.rows.slice(0, limit), unread: unread(), total: inbox.rows.length } })
  })
  await page.route('**/api/chats/notifications/read', async (route) => {
    const body = route.request().postDataJSON()
    inbox.rows.forEach((r) => { if (body.all || (body.ids || []).includes(r.id)) r.read = true })
    return route.fulfill({ json: { unread: inbox.rows.filter(r => !r.read).length } })
  })
}

async function openInbox(page) {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-inbox').getByRole('button', { name: 'Inbox' }).click()
}

test.describe('Inbox: clearing', () => {
  test('clear all asks first, says how many go, then empties the inbox and the badge', async ({ page }) => {
    const inbox = makeInbox(3, [1])
    await stub(page, inbox)
    await openInbox(page)
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    await page.getByTestId('inbox-clear').click()
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove all 3 notifications?')
    // Nothing is removed before the answer.
    expect(inbox.calls).toEqual([])
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('inbox-confirm')).toBeHidden()
    expect(inbox.rows).toHaveLength(3)

    await page.getByTestId('inbox-clear').click()
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(0)
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    await expect(page.getByTestId('inbox-done')).toHaveText('Removed 3 notifications.')
    await expect(page.getByText('Nothing here yet.')).toBeVisible()
    await expect(page.getByTestId('inbox-clear')).toBeDisabled()
    expect(inbox.calls).toEqual([{ all: true }])
  })

  test('clear read keeps the unread ones', async ({ page }) => {
    const inbox = makeInbox(4, [1, 2])
    await stub(page, inbox)
    await openInbox(page)
    await page.getByTestId('inbox-clear').click()
    await page.getByTestId('inbox-confirm-read').click()
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove 2 read notifications?')
    await page.getByTestId('inbox-confirm-yes').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(2)
    await expect(page.getByTestId('inbox-count')).toHaveText('2')
    expect(inbox.calls).toEqual([{ all: true, read_only: true }])
  })

  test('Escape backs out of the question before it closes the panel', async ({ page }) => {
    await stub(page, makeInbox(2))
    await openInbox(page)
    await page.getByTestId('inbox-clear').click()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('inbox-confirm')).toBeHidden()
    await expect(page.getByRole('dialog', { name: 'Inbox' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Inbox' })).toBeHidden()
  })

  test('one item can be marked read or deleted', async ({ page }) => {
    const inbox = makeInbox(2)
    await stub(page, inbox)
    await openInbox(page)
    await page.getByTestId('inbox-mark-read').first().click()
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    await page.getByTestId('inbox-item').first().getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(1)
    expect(inbox.calls).toEqual([{ ids: [1] }])
  })

  test('a long inbox loads a page at a time', async ({ page }) => {
    await stub(page, makeInbox(120))
    await openInbox(page)
    await expect(page.getByTestId('inbox-item')).toHaveCount(50)
    await expect(page.getByText('Showing 50 of 120')).toBeVisible()
    await page.getByTestId('inbox-more').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(100)
    await page.getByTestId('inbox-more').click()
    await expect(page.getByTestId('inbox-item')).toHaveCount(120)
    await expect(page.getByTestId('inbox-more')).toBeHidden()
    // The confirmation counts everything kept, not the page on screen.
    await page.getByTestId('inbox-clear').click()
    await expect(page.getByTestId('inbox-confirm')).toContainText('Remove all 120 notifications?')
  })

  test('a clear in another tab updates this one through the stream', async ({ page }) => {
    const inbox = makeInbox(3)
    await stub(page, inbox)
    await openInbox(page)
    await expect(page.getByTestId('inbox-count')).toHaveText('3')
    inbox.rows = []
    await page.evaluate(() => window.__inboxEmit('notifications_changed'))
    await expect(page.getByTestId('inbox-count')).toBeHidden()
    await expect(page.getByTestId('inbox-item')).toHaveCount(0)
  })
})
