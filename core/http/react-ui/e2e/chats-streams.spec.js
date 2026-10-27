import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The two event streams of the chats page must give up when the server says
// they have nothing to offer, and back off when it is merely unreachable.

const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, pending: [] }
const list = { conversations: [detail], persistent: true, files: true }

async function stub(page, hits, { inbox = 409, chat = 200 } = {}) {
  await mockChat(page)
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: list }))
  await page.route('**/api/chats/notifications/events', route => { hits.inboxEvents++; return route.abort() })
  await page.route(/\/api\/chats\/notifications(\?.*)?$/, route => route.fulfill(
    inbox === 409 ? { status: 409, json: { error: { message: 'no inbox' } } } : { json: { notifications: [], unread: 0 } }))
  await page.route('**/api/chats/c1/events', route => { hits.chatEvents++; return route.abort() })
  await page.route('**/api/chats/c1/messages*', route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c1', route => {
    hits.chatGets++
    return chat === 404 ? route.fulfill({ status: 404, json: { error: { message: 'chat not found' } } }) : route.fulfill({ json: detail })
  })
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
}

test.describe('Chat event streams', () => {
  test('a server without an inbox is not asked for its events again', async ({ page }) => {
    const hits = { inboxEvents: 0, chatEvents: 0, chatGets: 0 }
    await stub(page, hits, { inbox: 409 })
    await page.goto('/app/chats')
    await expect(page.getByTestId('chats-page')).toBeVisible()
    await page.waitForTimeout(5000)
    expect(hits.inboxEvents).toBeLessThanOrEqual(1)
  })

  test('a chat deleted elsewhere stops the stream and is read no more', async ({ page }) => {
    const hits = { inboxEvents: 0, chatEvents: 0, chatGets: 0 }
    await stub(page, hits, { inbox: 200, chat: 404 })
    await page.goto('/app/chats/c1')
    await page.waitForTimeout(6000)
    expect(hits.chatGets).toBeLessThanOrEqual(2)
    expect(hits.chatEvents).toBeLessThanOrEqual(2)
  })

  test('an unreachable stream backs off instead of retrying every second', async ({ page }) => {
    const hits = { inboxEvents: 0, chatEvents: 0, chatGets: 0 }
    await stub(page, hits, { inbox: 200 })
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chats-page')).toBeVisible()
    await page.waitForTimeout(6000)
    // 1 s, 2 s, 4 s: the connection at 0, 1, 3 and 7 s. A fixed 1 s retry makes 6 or more.
    expect(hits.chatEvents).toBeLessThanOrEqual(4)
  })

  test('a dropped stream does not clear the error of a failed send', async ({ page }) => {
    const hits = { inboxEvents: 0, chatEvents: 0, chatGets: 0 }
    await stub(page, hits, { inbox: 200 })
    await page.route('**/api/chats/c1/messages', route => (route.request().method() === 'POST'
      ? route.fulfill({ status: 500, json: { error: { message: 'model is gone' } } })
      : route.fulfill({ json: { messages: [] } })))
    await page.goto('/app/chats/c1')
    const box = page.getByTestId('chat-input')
    await box.fill('hello')
    await box.press('Enter')
    await expect(page.getByRole('alert')).toContainText('model is gone')
    await page.waitForTimeout(2500)
    await expect(page.getByRole('alert')).toContainText('model is gone')
  })
})
