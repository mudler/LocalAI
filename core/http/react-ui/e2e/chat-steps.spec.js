import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The turn that is still running, in the order it happened: a thought line, the
// note the model said before a call, the call; calls in a row group. The API
// is stubbed and the event stream faked, so each moment can be held still.

const AT = '2026-11-24T10:00:00Z'
const CHAT = { id: 'n1', title: 'Steps', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, pending: [] }

const emit = (page, kind, payload) => page.evaluate(([k, p]) => window.__emit(k, p), [kind, payload])
const start = (page, name, summary) => emit(page, 'tool_start', { kind: 'tool_start', tool: { name, summary } })
const end = (page, name, summary, extra = {}) => emit(page, 'tool_end', { kind: 'tool_end', tool: { name, summary, ok: true, output: 'ok', seq: 0, ...extra } })

async function open(page) {
  await page.addInitScript(() => {
    window.__sources = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.l = {}; window.__sources.push(this); setTimeout(() => this.onopen && this.onopen(), 0) }
      addEventListener(kind, fn) { (this.l[kind] = this.l[kind] || []).push(fn) }
      close() { this.closed = true }
    }
    window.__emit = (kind, payload) => {
      const src = window.__sources.filter((x) => x.url.includes('/api/chats/n1/events') && !x.closed).pop()
      for (const fn of (src && src.l[kind]) || []) fn({ data: JSON.stringify(payload) })
    }
  })
  await mockChat(page)
  const chat = { running: true, messages: [{ seq: 1, turn: 1, role: 'user', text: 'make the deck', at: AT }] }
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: chat.messages } }))
  await page.route('**/api/chats/n1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: [] } }))
  await page.route('**/api/chats/n1/addons', route => route.fulfill({ json: { addons: [] } }))
  await page.route('**/api/chats/n1', route => route.fulfill({ json: { ...CHAT, running: chat.running } }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...CHAT, running: chat.running, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.goto('/app/chats/n1')
  await expect(page.getByTestId('chat-thread')).toBeVisible()
  await page.waitForFunction(() => window.__sources.some((s) => s.url.includes('/api/chats/n1/events')))
  await emit(page, 'turn_start', { kind: 'turn_start', turn: 1 })
  return chat
}

const flow = (page) => page.getByTestId('chat-flow')
const order = (page) => flow(page).locator('> li:not(.cx-flow__fold)').evaluateAll((els) => els.map((el) => (
  el.matches('[data-testid="chat-note"]') ? 'note'
    : el.matches('[data-testid="chat-answer"]') ? 'answer'
      : el.matches('.cx-step--thought') ? 'thought'
        : el.matches('[data-testid="chat-step-group"]') ? 'group'
          : el.matches('[data-testid="chat-step"]') ? 'tool' : 'other')))

test.describe('Live turn steps', () => {
  test('three rounds draw as thought, note and call, in order, and the last text is the answer', async ({ page }) => {
    await open(page)
    for (const [i, call] of [['1', 'search'], ['2', 'read_file'], ['3', 'bash']]) {
      await emit(page, 'reasoning', { kind: 'reasoning', text: `Thought ${i}. ` })
      await emit(page, 'text', { kind: 'text', text: `Note ${i}.` })
      await start(page, call, `arg ${i}`)
      await end(page, call, `arg ${i}`)
    }
    await emit(page, 'text', { kind: 'text', text: 'The answer.' })
    await expect.poll(() => order(page)).toEqual(['thought', 'note', 'tool', 'thought', 'note', 'tool', 'thought', 'note', 'tool', 'answer'])
    await expect(page.getByTestId('chat-note')).toHaveText(['Note 1.', 'Note 2.', 'Note 3.'])
    await expect(page.getByTestId('chat-answer')).toHaveText('The answer.')
    await expect(page.getByTestId('chat-step')).toHaveCount(3)
    await expect(page.getByTestId('chat-step-group')).toHaveCount(0)
  })

  test('the text before a call stays on the page: the answer so far becomes a note', async ({ page }) => {
    await open(page)
    await emit(page, 'text', { kind: 'text', text: 'I will look.' })
    await expect(page.getByTestId('chat-answer')).toHaveText('I will look.')
    await start(page, 'bash', 'ls')
    await expect(page.getByTestId('chat-answer')).toHaveCount(0)
    await expect(page.getByTestId('chat-note')).toHaveText('I will look.')
  })

  test('calls in a row group from three and open on the one that failed', async ({ page }) => {
    await open(page)
    await emit(page, 'reasoning', { kind: 'reasoning', text: 'Read them all.' })
    for (let i = 1; i <= 2; i++) { await start(page, 'read_file', `/f${i}`); await end(page, 'read_file', `/f${i}`) }
    await expect(page.getByTestId('chat-step')).toHaveCount(2)
    await expect(page.getByTestId('chat-step-group')).toHaveCount(0)
    await start(page, 'read_file', '/f3')
    const group = page.getByTestId('chat-step-group')
    await expect(group).toHaveCount(1)
    await expect(group).toContainText('3 calls')
    await end(page, 'read_file', '/f3', { ok: false, output: 'no such file' })
    await start(page, 'read_file', '/f4')
    await end(page, 'read_file', '/f4')
    await expect(group).toContainText('Used 4 tools')
    await expect(page.getByTestId('chat-step-group-failed')).toHaveText('1 failed')
    await expect(group.getByRole('button', { name: /Used 4 tools/ })).toHaveAttribute('aria-expanded', 'true')
    await expect(group.getByTestId('chat-step-output')).toHaveText('no such file')
  })

  test('a group the reader opened stays open when it settles', async ({ page }) => {
    await open(page)
    for (let i = 1; i <= 3; i++) { await start(page, 'read_file', `/f${i}`); if (i < 3) await end(page, 'read_file', `/f${i}`) }
    const head = page.getByTestId('chat-step-group').getByRole('button').first()
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    await head.click()
    await expect(head).toHaveAttribute('aria-expanded', 'true')
    await end(page, 'read_file', '/f3')
    await start(page, 'read_file', '/f4')
    await end(page, 'read_file', '/f4')
    await expect(page.getByTestId('chat-step-group').getByRole('button').first()).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('chat-step-group').locator('.cx-group__steps > li')).toHaveCount(4)
  })

  test('a thought is one line that ends on a word, and opens to the whole text', async ({ page }) => {
    await open(page)
    const long = 'The user wants a deck of the newest features and I should read every note about them before I start, so that the slides are right the first time and I do not need to go back. Second sentence here.'
    await emit(page, 'reasoning', { kind: 'reasoning', text: `${long}\n\nA second paragraph.` })
    await start(page, 'bash', 'ls')
    const thought = page.locator('.cx-step--thought').first()
    const gist = await thought.locator('.cx-step__gist').innerText()
    expect(gist.length).toBeLessThanOrEqual(121)
    expect(gist).toMatch(/(\.|…)$/)
    expect(gist.replace(/…$/, '')).toBe(long.slice(0, gist.replace(/…$/, '').length))
    await thought.getByRole('button').click()
    await expect(thought.locator('.cx-think')).toContainText('A second paragraph.')
  })

  test('when the turn ends the steps fold into one line in place, then the record takes over', async ({ page }) => {
    await open(page)
    await emit(page, 'reasoning', { kind: 'reasoning', text: 'Look first.' })
    await emit(page, 'text', { kind: 'text', text: 'On it.' })
    await start(page, 'bash', 'ls')
    await end(page, 'bash', 'ls')
    await start(page, 'read_file', '/a')
    await end(page, 'read_file', '/a')
    await emit(page, 'text', { kind: 'text', text: 'Done.' })
    const head = flow(page).locator('.cx-fold__head')
    await expect(head).not.toBeVisible()
    await emit(page, 'turn_end', { kind: 'turn_end' })
    await expect(flow(page)).toHaveAttribute('data-folded', 'true')
    await expect(head).toContainText(/Worked for .* · 3 steps/)
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('chat-answer')).toHaveText('Done.')
    await expect(page.getByTestId('chat-step').first()).toBeHidden()
    await expect(flow(page)).toHaveCount(0)
  })

  test('the line of a finished turn opens the steps again', async ({ page }) => {
    await open(page)
    await emit(page, 'reasoning', { kind: 'reasoning', text: 'Look first.' })
    await start(page, 'bash', 'ls')
    await end(page, 'bash', 'ls')
    await emit(page, 'turn_end', { kind: 'turn_end' })
    const head = flow(page).locator('.cx-fold__head')
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    // The fold is still moving for a moment: click without waiting for it to settle.
    await head.evaluate((el) => el.click())
    await expect(head).toHaveAttribute('aria-expanded', 'true')
    await expect(flow(page)).not.toHaveAttribute('data-folded', 'true')
  })

  test('a failed call marks the folded line, and opening it shows the call', async ({ page }) => {
    await open(page)
    await emit(page, 'reasoning', { kind: 'reasoning', text: 'Try it.' })
    await start(page, 'read_file', '/missing')
    await end(page, 'read_file', '/missing', { ok: false, output: 'no such file' })
    await emit(page, 'text', { kind: 'text', text: 'It failed.' })
    const head = flow(page).locator('.cx-fold__head')
    await expect(head.getByTestId('chat-fold-failed')).not.toBeVisible()
    await emit(page, 'turn_end', { kind: 'turn_end' })
    await expect(head.getByTestId('chat-fold-failed')).toHaveText('1 failed')
    await head.evaluate((el) => el.click())
    await expect(flow(page).getByTestId('chat-step-output')).toHaveText('no such file')
  })

  test('nothing shows before it has content', async ({ page }) => {
    await open(page)
    await expect(page.getByTestId('chat-flow')).toHaveCount(0)
    await emit(page, 'reasoning', { kind: 'reasoning', text: '' })
    await expect(page.getByTestId('chat-flow')).toHaveCount(0)
    await expect(page.getByTestId('chat-step-group')).toHaveCount(0)
  })
})
