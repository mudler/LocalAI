import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// A message shows in the thread the moment it is sent, before the server has
// answered, and the thread follows what grows under it: the message, the card
// of a model that is loading and the answer that streams in.

const AT = '2026-10-25T10:00:00Z'
const LONG = 'A long line of history that fills the thread so that it needs to scroll. '.repeat(6)
const HISTORY = Array.from({ length: 40 }, (_, i) => ({ seq: i + 1, turn: Math.floor(i / 2) + 1, role: i % 2 ? 'assistant' : 'user', text: `Message ${i + 1}. ${LONG}`, at: AT }))
const SLOW_MS = 3000

async function open(page, { loaded, post = 'slow', operations = [] } = {}) {
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
  await mockChat(page, loaded ? { loaded } : {})
  const state = { detail: { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }, messages: HISTORY, ops: operations }
  await page.route('**/api/operations', route => route.fulfill({ json: state.ops }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, async route => {
    if (route.request().method() === 'POST') {
      let text = ''
      try { text = route.request().postDataJSON().text } catch { text = (route.request().postData() || '').match(/name="text"\r\n\r\n([^\r]*)/)?.[1] || '' }
      await new Promise(r => setTimeout(r, SLOW_MS))
      if (post === 'fail') return route.fulfill({ status: 500, json: { error: { message: 'the server is down' } } })
      state.messages = [...state.messages, { seq: state.messages.length + 1, turn: 99, role: 'user', text, at: AT }]
      state.detail = { ...state.detail, running: true }
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: state.messages } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: state.detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...state.detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-foot')).toBeVisible()
  await expect(page.getByTestId('chat-thread')).toContainText('Message 40.')
  return state
}

const body = (page) => page.locator('.cx-body')
const gap = (page) => body(page).evaluate(el => el.scrollHeight - el.scrollTop - el.clientHeight)
const send = async (page, text) => {
  await page.getByTestId('chat-input').fill(text)
  await page.getByTestId('chat-input').press('Enter')
}

test.describe('Chat send is optimistic', () => {
  test('the message shows at once, once, at the bottom, while the server is slow', async ({ page }) => {
    await open(page)
    await expect.poll(() => gap(page)).toBeLessThan(5)
    await body(page).evaluate(el => { el.scrollTop = 0 })
    await expect.poll(() => gap(page)).toBeGreaterThan(500)
    await send(page, 'the new question')
    // The POST takes three seconds; the message must not wait for it.
    await expect(page.getByTestId('chat-thread').getByText('the new question')).toBeVisible({ timeout: 800 })
    await expect.poll(() => gap(page), { timeout: 2000 }).toBeLessThan(5)
    await expect(page.getByTestId('chat-thread').getByText('the new question')).toBeInViewport()
    // The row is the same element when the server's copy arrives: no second
    // row, no remount.
    const row = await page.getByTestId('chat-thread').getByText('the new question').elementHandle()
    await expect(page.getByTestId('chat-thread').getByText('the new question')).toHaveCount(1)
    await page.waitForTimeout(SLOW_MS + 1200)
    await expect(page.getByTestId('chat-thread').getByText('the new question')).toHaveCount(1)
    expect(await row.evaluate(el => el.isConnected)).toBe(true)
    await expect.poll(() => gap(page)).toBeLessThan(5)
  })

  test('the message is faint while it is sent, and the same element is solid once it is confirmed', async ({ page }) => {
    await open(page)
    await send(page, 'the new question')
    const msg = page.getByTestId('chat-message').filter({ hasText: 'the new question' })
    await expect(msg).toHaveAttribute('data-sending', 'true', { timeout: 800 })
    const handle = await msg.elementHandle()
    await expect(msg).not.toHaveAttribute('data-sending', /.*/, { timeout: SLOW_MS + 3000 })
    expect(await handle.evaluate(el => el.isConnected)).toBe(true)
    const bubble = msg.locator('.cx-bubble')
    await expect.poll(() => bubble.evaluate(el => getComputedStyle(el).opacity)).toBe('1')
  })

  test('a message that was not sent folds away without a jump of the thread', async ({ page }) => {
    await open(page, { post: 'fail' })
    await send(page, 'this one fails')
    await expect(page.getByTestId('chat-thread').getByText('this one fails')).toBeVisible({ timeout: 800 })
    // The top of the last line of the history, in every frame, until after the failure.
    const tops = await page.evaluate((ms) => new Promise(resolve => {
      const last = [...document.querySelectorAll('[data-testid=chat-message]')].find(e => e.textContent.includes('Message 40.'))
      const out = []
      const t0 = performance.now()
      const tick = () => {
        out.push(last.getBoundingClientRect().top)
        if (performance.now() - t0 < ms) requestAnimationFrame(tick); else resolve(out)
      }
      tick()
    }), SLOW_MS + 800)
    const steps = tops.slice(1).map((t, i) => Math.abs(t - tops[i]))
    // It moves, by many small steps and not one: the row's 66 px fold over frames.
    expect(Math.abs(tops[tops.length - 1] - tops[0])).toBeGreaterThan(40)
    expect(Math.max(...steps)).toBeLessThan(40)
  })

  test('a message with a file keeps the file in the box when the send fails', async ({ page }) => {
    await open(page, { post: 'fail' })
    await page.locator('input[type=file]').first().setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') })
    await send(page, 'read this')
    await expect(page.getByTestId('chat-thread').getByText('read this')).toBeVisible({ timeout: 800 })
    await expect(page.getByTestId('chat-thread').getByText('read this')).toHaveCount(0, { timeout: SLOW_MS + 3000 })
    await expect(page.getByTestId('chat-input')).toHaveValue('read this')
    await expect(page.getByText('notes.txt').first()).toBeVisible()
  })

  test('the first message of a new chat shows before the chat is created', async ({ page }) => {
    const state = await open(page, { post: 'fast' })
    // A new chat: nothing is open. Creating it takes three seconds.
    let created = 0
    await page.route(/\/api\/chats$/, async route => {
      if (route.request().method() !== 'POST') return route.fallback()
      created++
      await new Promise(r => setTimeout(r, SLOW_MS))
      return route.fulfill({ json: state.detail })
    })
    // The chat page with no chat open is reached with the state of a new chat.
    await page.evaluate(() => {
      window.history.pushState({ usr: { newChat: true }, key: 'new-chat', idx: 5 }, '', '/app/chats')
      window.dispatchEvent(new PopStateEvent('popstate', { state: window.history.state }))
    })
    await expect(page.getByTestId('chat-thread')).toBeHidden()
    await expect(page.getByTestId('chat-input')).toBeVisible()
    await send(page, 'hello new chat')
    await expect(page.getByTestId('chat-thread').getByText('hello new chat')).toBeVisible({ timeout: 800 })
    await page.waitForTimeout(SLOW_MS + 1500)
    expect(created).toBe(1)
    await expect(page).toHaveURL(/\/app\/chats\/c1$/)
    await expect(page.getByTestId('chat-thread').getByText('hello new chat')).toHaveCount(1)
  })

  test('a failed send takes the message back and gives the text to the box', async ({ page }) => {
    await open(page, { post: 'fail' })
    await send(page, 'this one fails')
    await expect(page.getByTestId('chat-thread').getByText('this one fails')).toBeVisible({ timeout: 800 })
    await expect(page.getByTestId('chat-thread').getByText('this one fails')).toHaveCount(0, { timeout: SLOW_MS + 3000 })
    await expect(page.getByTestId('chat-input')).toHaveValue('this one fails')
  })
})

test.describe('Chat follows what grows under it', () => {
  test('the card of a model that is loading, and its progress, stay in view', async ({ page }) => {
    const state = await open(page, { loaded: [], post: 'fast' })
    await expect.poll(() => gap(page)).toBeLessThan(5)
    state.detail = { ...state.detail, running: true }
    await page.evaluate(() => window.__emit('turn_start', {}))
    await expect(page.getByTestId('chat-load')).toBeVisible()
    await expect.poll(() => gap(page), { timeout: 3000 }).toBeLessThan(5)
    // The transfer starts: the card gets a phase, a bar and a note, and grows.
    state.ops = [{ id: 'op1', jobID: 'op1', taskType: 'staging', name: 'qwen3-8b', progress: 40, message: 'Copying the model to the node. '.repeat(8), nodeName: 'node-a' }]
    await expect(page.getByTestId('chat-load-phase')).toBeVisible({ timeout: 5000 })
    await expect.poll(() => gap(page), { timeout: 3000 }).toBeLessThan(5)
  })

  test('a reader who scrolled up while the card shows is left there', async ({ page }) => {
    const state = await open(page, { loaded: [], post: 'fast' })
    state.detail = { ...state.detail, running: true }
    await page.evaluate(() => window.__emit('turn_start', {}))
    await expect(page.getByTestId('chat-load')).toBeVisible()
    await expect.poll(() => gap(page), { timeout: 3000 }).toBeLessThan(5)
    await body(page).evaluate(el => { el.scrollTop = 0 })
    await page.waitForTimeout(300)
    state.ops = [{ id: 'op1', jobID: 'op1', taskType: 'staging', name: 'qwen3-8b', progress: 40, message: 'Copying the model to the node. '.repeat(8), nodeName: 'node-a' }]
    await expect(page.getByTestId('chat-load-phase')).toBeAttached({ timeout: 5000 })
    await page.waitForTimeout(500)
    expect(await body(page).evaluate(el => el.scrollTop)).toBeLessThan(5)
  })
})
