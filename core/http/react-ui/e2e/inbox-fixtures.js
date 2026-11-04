import { mockChat } from './chat-fixtures.js'

// The chat page with a fake inbox. The server is a small stateful inbox kept
// in `inbox.rows`; the notification stream is a fake EventSource the spec
// drives with window.__inboxEmit(name). `inbox.fail` makes the list read
// answer 500 while it is true. `inbox.calls` records the deletes, and
// `inbox.reads` the mark-read bodies.

export const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, pending: [] }

export function makeInbox(count, readIds = []) {
  const rows = Array.from({ length: count }, (_, i) => ({
    id: i + 1, kind: 'run_ended', status: 'ok', source: 'trigger', title: `Run ${i + 1}`, body: '', link: `/app/chats/r${i + 1}`,
    read: readIds.includes(i + 1), created: new Date(Date.now() - (count - i) * 60_000).toISOString(),
  }))
  // Newest first, as the server lists them.
  rows.reverse()
  return { rows, calls: [], reads: [], lists: [], fail: false }
}

export async function stubInbox(page, inbox) {
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
    const url = new URL(req.url())
    inbox.lists.push(url.search)
    if (inbox.fail) return route.fulfill({ status: 500, json: { error: { message: 'the database is busy' } } })
    const limit = Number(url.searchParams.get('limit') || 50)
    const only = url.searchParams.get('unread') === '1'
    const rows = only ? inbox.rows.filter(r => !r.read) : inbox.rows
    return route.fulfill({ json: { notifications: rows.slice(0, limit), unread: unread(), total: inbox.rows.length } })
  })
  await page.route('**/api/chats/notifications/read', async (route) => {
    const body = route.request().postDataJSON()
    inbox.reads.push(body)
    inbox.rows.forEach((r) => { if (body.all || (body.ids || []).includes(r.id)) r.read = true })
    return route.fulfill({ json: { unread: inbox.rows.filter(r => !r.read).length } })
  })
}

export function inboxButton(page) {
  return page.getByTestId('chat-inbox').getByRole('button', { name: /^Notifications/ })
}

export async function openInbox(page) {
  await page.goto('/app/chats/c1')
  await inboxButton(page).click()
  await page.getByRole('dialog', { name: 'Notifications' }).waitFor()
}

// Opens the panel's menu and picks one of its items.
export async function menuPick(page, name) {
  await page.getByTestId('inbox-menu').getByRole('button').click()
  await page.getByRole('menuitem', { name }).click()
}
