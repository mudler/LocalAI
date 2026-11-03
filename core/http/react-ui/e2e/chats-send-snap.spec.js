import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// Sending a message snaps the thread to the bottom, also when the reader had
// scrolled up, so the new message is in view. Scrolling up while an answer
// streams still stays where the reader put it.

const AT = '2026-10-25T10:00:00Z'
const LONG = 'A long line of history that fills the thread so that it needs to scroll. '.repeat(6)
const HISTORY = Array.from({ length: 40 }, (_, i) => ({ seq: i + 1, turn: Math.floor(i / 2) + 1, role: i % 2 ? 'assistant' : 'user', text: `Message ${i + 1}. ${LONG}`, at: AT }))

async function open(page) {
  await page.addInitScript(() => {
    window.__sources = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.l = {}; window.__sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(kind, fn) { (this.l[kind] = this.l[kind] || []).push(fn) }
      close() { this.closed = true }
    }
    window.__emit = (kind, payload) => {
      const src = window.__sources.filter((x) => x.url.includes('/api/chats/c1/events') && !x.closed).pop()
      for (const fn of (src && src.l[kind]) || []) fn({ data: JSON.stringify(payload) })
    }
  })
  await mockChat(page)
  const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  let messages = HISTORY
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      const text = route.request().postDataJSON().text
      messages = [...messages, { seq: messages.length + 1, turn: 99, role: 'user', text, at: AT }]
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-foot')).toBeVisible()
}

const body = (page) => page.locator('.cx-body')
const gap = (page) => body(page).evaluate(el => el.scrollHeight - el.scrollTop - el.clientHeight)

test.describe('Chat scroll on send', () => {
  test('sending a message snaps to the bottom after the reader scrolled up', async ({ page }) => {
    await open(page)
    await expect(page.getByTestId('chat-thread')).toContainText('Message 40.')
    await expect.poll(() => gap(page)).toBeLessThan(5)
    await body(page).evaluate(el => { el.scrollTop = 0 })
    await expect.poll(() => gap(page)).toBeGreaterThan(500)
    await page.getByTestId('chat-input').fill('the new question')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-thread')).toContainText('the new question')
    await expect.poll(() => gap(page), { timeout: 5000 }).toBeLessThan(5)
    await expect(page.getByTestId('chat-thread').getByText('the new question')).toBeInViewport()
    // The answer that streams in after it stays in view too.
    await page.evaluate(() => window.__emit('turn_start', {}))
    for (let i = 0; i < 6; i++) {
      await page.evaluate(() => window.__emit('text', { text: 'The answer keeps coming in, line after line of it.\n\n'.repeat(6) }))
      await page.waitForTimeout(150)
    }
    await expect.poll(() => gap(page), { timeout: 5000 }).toBeLessThan(5)
  })

  test('scrolling up while an answer streams is not undone', async ({ page }) => {
    await open(page)
    await expect.poll(() => gap(page)).toBeLessThan(5)
    await body(page).evaluate(el => { el.scrollTop = 0 })
    await expect.poll(() => gap(page)).toBeGreaterThan(500)
    await page.evaluate(() => window.__emit('message', { seq: 41, turn: 100, role: 'assistant', text: 'streaming answer '.repeat(80), at: '2026-10-25T10:00:00Z' }))
    await page.waitForTimeout(500)
    expect(await body(page).evaluate(el => el.scrollTop)).toBeLessThan(5)
  })
})
