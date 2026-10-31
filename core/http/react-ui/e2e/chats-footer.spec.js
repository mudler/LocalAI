import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The status line under the chat composer, with the API stubbed and the event
// stream faked, so each moment of a turn can be held still: one live state on
// the left, the context meter and its details on the right.

const AT = '2026-10-25T10:00:00Z'
const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Compare the quotes.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'The second one is cheapest.', at: AT },
]
const usage = (used, extra = {}) => ({ kind: 'usage', usage: { used, limit: 32768, completion: 180, tokens_per_second: 41.3, ...extra } })

const state = (page) => page.getByTestId('chat-state')
const meter = (page) => page.getByTestId('chat-context-meter')
const bar = (page) => meter(page).locator('.cx-meter__bar')
// The share of the bar that is filled, and where its ticks are, as the page
// set them.
const barVar = (page, name) => bar(page).evaluate((el, n) => el.style.getPropertyValue(n), name)
// The live region of the line (the toast stack is another status region).
const live = (page) => state(page).getByRole('status')
const emit = (page, kind, payload) => page.evaluate(([k, p]) => window.__emit(k, p), [kind, payload])

async function open(page, { running = false, lastTurn = 'done', pending = [], messages = HISTORY, path = '/app/chats/c1' } = {}) {
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
  const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running, last_turn: lastTurn, pending }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  if (path === '/app/chats') {
    // The bare address opens Home; the empty chat opens only when the app hands
    // it navigation state, so the test does the same.
    await page.goto('/app')
    await page.evaluate(() => {
      window.history.pushState({ usr: { fresh: true }, key: 'fresh', idx: 1 }, '', '/app/chats')
      window.dispatchEvent(new PopStateEvent('popstate', { state: window.history.state }))
    })
  } else {
    await page.goto(path)
  }
  await expect(page.getByTestId('chat-foot')).toBeVisible()
}

test.describe('Chat status line', () => {
  test('an idle chat is Ready, and the keyboard hint shows only for a focused, empty composer', async ({ page }) => {
    await open(page)
    await expect(state(page)).toHaveAttribute('data-state', 'ready')
    await expect(live(page).filter({ hasText: 'Ready' })).toBeVisible()
    await expect(page.getByTestId('chat-foot-hint')).toHaveCount(0)
    await page.getByTestId('chat-input').focus()
    await expect(page.getByTestId('chat-foot-hint')).toHaveText('Enter to send · Shift+Enter for a new line')
    await page.getByTestId('chat-input').fill('hello')
    await expect(page.getByTestId('chat-foot-hint')).toHaveCount(0)
  })

  test('a running turn names each phase: thinking, a tool, answering', async ({ page }) => {
    await open(page, { running: true, messages: [HISTORY[0]] })
    await emit(page, 'turn_start', { kind: 'turn_start', turn: 1 })
    await expect(state(page)).toHaveAttribute('data-state', 'thinking')
    await expect(state(page)).toHaveAttribute('data-busy', 'true')

    await emit(page, 'tool_start', { kind: 'tool_start', tool: { name: 'read', summary: 'quotes/q2.pdf' } })
    await expect(state(page)).toHaveAttribute('data-state', 'tool')
    await expect(live(page)).toContainText('Reading')
    await expect(live(page)).toContainText('q2.pdf')

    await emit(page, 'tool_end', { kind: 'tool_end', tool: { name: 'read', ok: true, output: 'ok', seq: 2 } })
    await expect(state(page)).toHaveAttribute('data-state', 'thinking')

    await emit(page, 'text', { kind: 'text', text: 'The second ' })
    await expect(state(page)).toHaveAttribute('data-state', 'answering')
    await expect(live(page)).toHaveText('Answering')
  })

  test('a failed, an interrupted and a stopped turn each have a word and an icon', async ({ page }) => {
    await open(page, { lastTurn: 'failed', messages: [HISTORY[0]] })
    await expect(state(page)).toHaveAttribute('data-state', 'failed')
    await expect(live(page)).toContainText('Failed')
    // The banner in the thread says the rest; the line does not repeat it.
    await expect(live(page)).not.toContainText('The last turn failed.')
    await expect(state(page).locator('svg[data-icon="alert-circle"]')).toBeVisible()
  })

  test('an interrupted turn says so with its sentence', async ({ page }) => {
    await open(page, { lastTurn: 'interrupted' })
    await expect(live(page)).toContainText('Interrupted')
    await expect(live(page)).toContainText('The last turn was interrupted.')
    await expect(state(page).locator('svg[data-icon="warning"]')).toBeVisible()
  })

  test('a turn that reached the step limit is paused, and Continue sends a message that goes on', async ({ page }) => {
    await open(page, { lastTurn: 'paused' })
    await expect(state(page)).toHaveAttribute('data-state', 'paused')
    await expect(state(page)).toHaveAttribute('data-tone', 'warn')
    // After a reload the page has not seen the event: no number, still clear.
    await expect(live(page)).toContainText('Paused')
    await expect(live(page)).toContainText('at the step limit')
    await expect(state(page).locator('svg[data-icon="pause"]')).toBeVisible()
    // The paused event names the limit.
    await emit(page, 'paused', { kind: 'paused', steps: 40 })
    await expect(live(page)).toContainText('after 40 steps')

    const posted = []
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => {
      if (route.request().method() !== 'POST') return route.fallback()
      posted.push(route.request().postDataJSON())
      return route.fulfill({ json: { started: true } })
    })
    const button = page.getByTestId('chat-continue')
    await expect(button).toHaveText('Continue')
    await button.click()
    await expect.poll(() => posted.length).toBe(1)
    expect(posted[0].text).toBe('Continue from where you stopped.')
  })

  test('a done turn has no Continue button', async ({ page }) => {
    await open(page)
    await expect(state(page)).toHaveAttribute('data-state', 'ready')
    await expect(page.getByTestId('chat-continue')).toHaveCount(0)
  })

  test('messages left after a stop make the chat wait for you', async ({ page }) => {
    await open(page, { lastTurn: 'stopped', pending: [{ id: 'p1', text: 'use euros', seq: 3 }] })
    await expect(state(page)).toHaveAttribute('data-state', 'waiting')
    await expect(live(page)).toContainText('Waiting for you')
    await expect(live(page)).toContainText('1 message waiting')
  })

  test('the meter is a button that opens what the last request used, and Escape returns to it', async ({ page }) => {
    await open(page)
    await expect(meter(page)).toHaveCount(0)
    await emit(page, 'usage', usage(12345, { model: 'qwen3-8b', prompt_ms: 240 }))
    await expect(meter(page).locator('.cx-meter__text')).toHaveText('12.3k / 32.8k tokens')
    await expect(page.getByTestId('chat-meter-left')).toHaveText('20.4k left')
    await expect(meter(page)).toContainText('41 tok/s')
    await expect(meter(page)).toHaveAttribute('data-tone', 'ok')
    await expect(meter(page)).toHaveAttribute('aria-label', /^12\.3k \/ 32\.8k tokens \(38%\) · 20\.4k left · 41 tok\/s/)
    // A bar a reader can see: wide and tall enough, filled to the share, with
    // ticks where the meter warns and where it is hot.
    const box = await bar(page).boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(90)
    expect(box.height).toBeGreaterThanOrEqual(6)
    expect(await barVar(page, '--cx-fill')).toBe('0.38')
    expect(await barVar(page, '--cx-warn')).toBe('0.8')
    expect(await barVar(page, '--cx-hot')).toBe('0.9')
    // The speed is not in the live region: a new figure is not announced.
    await expect(live(page)).not.toContainText('tok/s')

    await meter(page).click()
    await expect(meter(page)).toHaveAttribute('aria-expanded', 'true')
    const details = page.getByRole('dialog', { name: 'Context and speed' })
    await expect(details.getByTestId('chat-meter-context')).toHaveText('12,345 of 32,768 tokens (38%)')
    await expect(details.getByTestId('chat-meter-left-detail')).toHaveText('20,423 tokens left')
    await expect(details.getByTestId('chat-meter-model')).toContainText('qwen3-8b')
    await expect(details.getByTestId('chat-meter-backend')).toContainText('llama-cpp')
    await expect(details.getByTestId('chat-meter-in')).toContainText('12,345')
    await expect(details.getByTestId('chat-meter-out')).toContainText('180')
    await expect(details.getByTestId('chat-meter-prompt')).toContainText('0.2 s')
    await expect(details.getByTestId('chat-meter-node')).toHaveCount(0)
    await expect(details.getByTestId('chat-meter-near-full')).toHaveCount(0)

    await page.keyboard.press('Escape')
    await expect(details).toHaveCount(0)
    await expect(meter(page)).toBeFocused()
  })

  test('Escape closes the details of a running turn without stopping the turn', async ({ page }) => {
    let stops = 0
    await page.route('**/api/chats/c1/stop', route => { stops++; return route.fulfill({ json: { stopped: true } }) })
    await open(page, { running: true, messages: [HISTORY[0]] })
    await emit(page, 'turn_start', { kind: 'turn_start', turn: 1 })
    await emit(page, 'usage', usage(8200))
    await meter(page).click()
    await expect(page.getByRole('dialog', { name: 'Context and speed' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Context and speed' })).toHaveCount(0)
    await page.waitForTimeout(300)
    expect(stops).toBe(0)
    await expect(state(page)).toHaveAttribute('data-busy', 'true')
  })

  test('near the limit the meter warns, and the details say what happens next', async ({ page }) => {
    await open(page)
    await emit(page, 'usage', usage(27500, { node: 'worker-2' }))
    await expect(meter(page)).toHaveAttribute('data-tone', 'warn')
    await expect(meter(page).locator('svg[data-icon="warning"]')).toBeVisible()
    await expect(page.getByTestId('chat-meter-left')).toHaveText('5.3k left')
    expect(await barVar(page, '--cx-fill')).toBe('0.84')
    await emit(page, 'usage', usage(31900, { node: 'worker-2' }))
    await expect(meter(page)).toHaveAttribute('data-tone', 'err')
    await meter(page).click()
    const details = page.getByRole('dialog', { name: 'Context and speed' })
    await expect(details.getByTestId('chat-meter-near-full')).toContainText('compacts the older part')
    await expect(details.getByTestId('chat-meter-node')).toContainText('worker-2')
    // Two requests with a speed: the average shows.
    await expect(details.getByTestId('chat-meter-avg')).toContainText('41 tok/s')
  })

  test('a full context leaves nothing, not a negative count', async ({ page }) => {
    await open(page)
    await emit(page, 'usage', usage(40000))
    await expect(meter(page)).toHaveAttribute('data-tone', 'err')
    await expect(page.getByTestId('chat-meter-left')).toHaveText('0 left')
    expect(await barVar(page, '--cx-fill')).toBe('1')
  })

  test('with no known limit the meter shows only the count used, without a bar', async ({ page }) => {
    await open(page)
    await emit(page, 'usage', usage(900, { limit: 0 }))
    await expect(meter(page).locator('.cx-meter__text')).toHaveText('900 tokens')
    await expect(meter(page)).toHaveAttribute('data-limit', 'unknown')
    await expect(meter(page)).toHaveAttribute('data-tone', 'ok')
    await expect(bar(page)).toHaveCount(0)
    await expect(page.getByTestId('chat-meter-left')).toHaveCount(0)
    await meter(page).click()
    const details = page.getByRole('dialog', { name: 'Context and speed' })
    await expect(details.getByTestId('chat-meter-context')).toHaveText('900 tokens')
    await expect(details.getByTestId('chat-meter-left-detail')).toHaveCount(0)
  })

  test('a new chat keeps the line quiet', async ({ page }) => {
    await open(page, { path: '/app/chats' })
    await expect(state(page)).toHaveAttribute('data-quiet', 'true')
    await expect(live(page).filter({ hasText: 'Ready' })).toBeHidden()
  })
})

test.describe('Chat status line on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('stays on one line with the word and a percentage, and the details open in a sheet', async ({ page }) => {
    await open(page, { lastTurn: 'interrupted' })
    await emit(page, 'usage', usage(12345))
    await expect(meter(page)).toBeVisible()
    await expect(meter(page).locator('.cx-meter__short')).toHaveText('38%')
    await expect(meter(page).locator('.cx-meter__text')).toBeHidden()
    // The bar and what is left stay; the speed goes to the sheet.
    await expect(bar(page)).toBeVisible()
    await expect(page.getByTestId('chat-meter-left')).toHaveText('20.4k left')
    await expect(page.getByTestId('chat-speed')).toBeHidden()
    const box = await page.getByTestId('chat-foot').boundingBox()
    expect(box.height).toBeLessThanOrEqual(32)
    await meter(page).click()
    await expect(page.getByTestId('chat-meter-sheet')).toBeVisible()
    await expect(page.getByTestId('chat-meter-context')).toHaveText('12,345 of 32,768 tokens (38%)')
    await expect(page.getByTestId('chat-meter-left-detail')).toHaveText('20,423 tokens left')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-meter-sheet')).toHaveCount(0)
    await expect(meter(page)).toBeFocused()
  })

  test('with no known limit the count keeps its word', async ({ page }) => {
    await open(page)
    await emit(page, 'usage', usage(900, { limit: 0 }))
    await expect(meter(page).locator('.cx-meter__text')).toHaveText('900 tokens')
    await expect(meter(page).locator('.cx-meter__text')).toBeVisible()
    await expect(meter(page).locator('.cx-meter__short')).toBeHidden()
  })
})
