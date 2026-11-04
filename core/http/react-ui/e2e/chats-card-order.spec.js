import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// A card that waits for the person comes after the words that explain it: at
// the end of the assistant turn, below the final text, also while the text
// streams in. The API is stubbed; the event stream is faked and the spec plays
// the events of the turn. Tool lines keep their place.

const AT = '2026-11-03T09:00:00Z'
const proposal = () => ({
  v: 1, id: 'cp1', owner: 'alice', kind: 'memory', scope: 'user', target: 'Memory/index.md',
  summary: 'You live in Milan, not Rome', content: '- home: Milan\n', base_hash: 'abc', source: { chat: 'c1' }, status: 'pending',
  created: AT, expires: new Date(Date.now() + 20 * 3600_000).toISOString(),
})
const USER = { seq: 1, turn: 1, role: 'user', text: 'I moved to Milan.', at: AT }
const PROPOSE = { seq: 2, turn: 1, role: 'tool', text: 'propose_change: You live in Milan, not Rome\nStored.', card: { kind: 'change_proposal', id: 'cp1' }, at: AT }
const READ = { seq: 4, turn: 1, role: 'tool', text: 'read_file: /Memory/index.md\n- home: Rome', at: AT }
const FIRST = { seq: 3, turn: 1, role: 'assistant', text: 'Let me look at your notes first.', at: AT }
const FINAL = { seq: 5, turn: 1, role: 'assistant', text: 'Your notes say Rome. I proposed Milan instead; confirm it below.', at: AT }

async function stub(page, state) {
  await page.addInitScript(() => {
    window.__sources = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.l = {}; window.__sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(kind, fn) { (this.l[kind] = this.l[kind] || []).push(fn) }
      close() { this.closed = true }
    }
    window.__emit = (kind, payload) => {
      const src = window.__sources.filter((x) => x.url.includes('/api/chats/c1/events') && !x.closed).pop()
      for (const fn of (src && src.l[kind]) || []) fn({ data: JSON.stringify({ kind, ...payload }) })
    }
  })
  await mockChat(page)
  const detail = (running) => ({ id: 'c1', title: 'Notes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running, last_turn: running ? 'running' : 'done', pending: [] })
  await page.route('**/api/chats/notifications/events', route => route.abort())
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail(state.running) }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail(false), pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route('**/api/chats/files/content*', route => route.fulfill({ status: 404, json: { error: { message: 'not found' } } }))
  await page.route(/\/api\/chats\/changes(\?.*)?$/, route => route.fulfill({ json: [proposal()] }))
  await page.route(/\/api\/chats\/changes\/cp1$/, route => route.fulfill({ json: proposal() }))
}

// One read of the boxes in the same frame, since the transcript scrolls while it fills.
const boxes = (page) => page.evaluate(() => {
  const r = (el) => (el ? { top: el.getBoundingClientRect().top, bottom: el.getBoundingClientRect().bottom } : null)
  const prose = [...document.querySelectorAll('.cx-prose')]
  const steps = [...document.querySelectorAll('[data-testid="chat-activity"]')]
  return {
    texts: prose.map(r),
    lastText: r(prose[prose.length - 1]),
    card: r(document.querySelector('[data-testid="change-proposal"]')),
    cards: document.querySelectorAll('[data-testid="change-proposal"]').length,
    folds: steps.map(r),
  }
})

for (const [name, size] of [['desktop', { width: 1440, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
  test.describe(name, () => {
    test.use({ viewport: size })

    test('a turn with two messages draws its card after the final text', async ({ page }) => {
      await stub(page, { messages: [USER, PROPOSE, FIRST, READ, FINAL], running: false })
      await page.goto('/app/chats/c1')
      await expect(page.getByTestId('change-proposal')).toBeVisible()
      await page.screenshot({ path: `test-results/card-order-${name}-persisted.png` })
      const b = await boxes(page)
      expect(b.cards).toBe(1)
      expect(b.texts).toHaveLength(2)
      expect(b.card.top).toBeGreaterThanOrEqual(b.lastText.bottom - 1)
    })

    test('a card read mid-turn stays below the text while it streams, and after the turn ends', async ({ page }) => {
      const state = { messages: [USER], running: true }
      await stub(page, state)
      await page.goto('/app/chats/c1')
      await expect(page.getByText('I moved to Milan.')).toBeVisible()
      const input = page.getByTestId('chat-input')
      await input.focus()
      await page.evaluate(() => {
        window.__emit('turn_start', { turn: 1 })
        window.__emit('tool_start', { tool: { name: 'propose_change' } })
      })
      // The proposal is stored and the transcript holds its line: the page reads it.
      state.messages = [USER, PROPOSE]
      await page.evaluate(() => {
        window.__emit('tool_end', { tool: { name: 'propose_change', ok: true, output: 'Stored.', seq: 2 } })
        window.__emit('turn_start', { turn: 1 })
        window.__emit('text', { text: 'Your notes say Rome. ' })
      })
      await expect(page.getByTestId('change-proposal')).toBeVisible()
      await expect(page.getByTestId('chat-streaming')).toContainText('Your notes say Rome.')
      await page.screenshot({ path: `test-results/card-order-${name}-streaming.png` })
      let b = await boxes(page)
      expect(b.cards).toBe(1)
      expect(b.card.top).toBeGreaterThanOrEqual(b.lastText.bottom - 1)

      // The text grows; the card stays after it and the composer keeps focus.
      await page.evaluate(() => window.__emit('text', { text: 'I proposed Milan instead, and the reason is that you told me you moved. '.repeat(6) }))
      await expect(page.getByTestId('chat-streaming')).toContainText('you told me you moved')
      b = await boxes(page)
      expect(b.card.top).toBeGreaterThanOrEqual(b.lastText.bottom - 1)
      await expect(input).toBeFocused()
      // Text comes first in the reading order.
      const order = await page.evaluate(() => {
        const t = document.querySelector('[data-testid="chat-streaming"]')
        const c = document.querySelector('[data-testid="change-proposal"]')
        return t.compareDocumentPosition(c) & Node.DOCUMENT_POSITION_FOLLOWING
      })
      expect(order).toBeTruthy()

      // The turn ends: the answer is in the transcript and the card is still below it.
      state.messages = [USER, PROPOSE, { ...FINAL, seq: 3 }]
      state.running = false
      await page.evaluate(() => window.__emit('turn_end', { turn: 1 }))
      await expect(page.getByTestId('chat-streaming')).toHaveCount(0)
      await expect(page.getByTestId('change-proposal')).toBeVisible()
      await page.screenshot({ path: `test-results/card-order-${name}-ended.png` })
      b = await boxes(page)
      expect(b.cards).toBe(1)
      expect(b.card.top).toBeGreaterThanOrEqual(b.lastText.bottom - 1)
    })
  })
}
