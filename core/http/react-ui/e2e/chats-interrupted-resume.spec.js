import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { freezeClock } from './clock-fixtures.js'

// A restart of the server interrupts a turn. The page shows the turn as
// incomplete, resumes it by itself a few times and, when that does not work,
// leaves the Retry to the person. The chats API is stubbed with a small state
// machine; the clock is faked so the waits take no time.

const AT = '2026-11-06T10:00:00Z'
const line = (seq, role, text, extra = {}) => ({ seq, turn: 1, role, text, at: AT, ...extra })

const CUT = [
  line(1, 'user', 'build the quarterly report'),
  line(2, 'assistant', 'I will read the three source files first.'),
  line(3, 'tool', 'read_file: sales.csv\n240 rows'),
  line(4, 'checkpoint', ''),
]
const FINISHED = 'The report is ready: revenue is up 12 percent.'

function stubChat(page, { last = 'interrupted', resumeWorks = true, willResume = false, tried = 0 } = {}) {
  const state = { messages: [...CUT], running: false, last_turn: last, calls: [], will_resume: willResume, resume_attempts: tried }
  const detail = () => ({
    id: 'c1', title: 'Quarterly report', model: 'qwen3-8b', kind: 'chat', created: AT, updated: new Date().toISOString(),
    running: state.running, last_turn: state.last_turn, pending: [],
    will_resume: state.will_resume || undefined, resume_attempts: state.resume_attempts || undefined,
  })
  return {
    state,
    async install() {
      await page.route('**/api/chats/c1/events', route => route.abort())
      await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
      await page.route('**/api/chats/c1/resume', async route => {
        state.calls.push('resume')
        if (!resumeWorks) return route.fulfill({ json: { started: false } })
        state.messages = [...state.messages.filter(m => m.role !== 'checkpoint'), line(5, 'assistant', FINISHED), line(6, 'checkpoint', '')]
        state.last_turn = 'done'
        return route.fulfill({ json: { started: true } })
      })
      await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
      await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
        json: { conversations: [{ ...detail(), pending: undefined }], persistent: true, files: true, files_reason: '' },
      }))
    },
  }
}

// One screenshot per theme and screen size, to look at (set RESUME_SHOTS).
async function shot(page, name) {
  const dir = process.env.RESUME_SHOTS || 'test-results'
  for (const theme of ['light', 'dark']) {
    for (const [size, viewport] of [['desktop', { width: 1280, height: 800 }], ['phone', { width: 390, height: 780 }]]) {
      await page.setViewportSize(viewport)
      await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
      // A smaller screen leaves the end of the thread above the fold: scroll to it.
      await page.getByTestId('chat-thread').evaluate((el) => { for (let n = el; n; n = n.parentElement) n.scrollTop = n.scrollHeight })
      await page.screenshot({ path: `${dir}/${name}-${theme}-${size}.png` })
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow, `${name} ${theme} ${size} scrolls sideways`).toBeLessThanOrEqual(1)
    }
  }
}
const card = page => page.getByTestId('chat-error')

test.describe('Interrupted turn', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await page.clock.install()
  })

  test('shows as incomplete, waits, and Stop trying leaves the Retry to the person', async ({ page }) => {
    const api = stubChat(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByText('I will read the three source files first.')).toBeVisible()
    await expect(card(page)).toHaveAttribute('data-code', 'interrupted')
    await expect(card(page)).toHaveAttribute('data-phase', 'waiting')
    await freezeClock(page)
    await expect(card(page).getByRole('heading')).toHaveText('The reply was interrupted')
    await expect(page.getByTestId('chat-error-reason')).toContainText('The server restarted while it was answering.')
    await expect(page.getByTestId('chat-error-resume')).toHaveText('Going on in 3 seconds (try 1 of 3).')
    await shot(page, 'waiting')

    await page.getByTestId('chat-error-stop').click()
    await expect(card(page)).toHaveAttribute('data-phase', 'idle')
    await page.clock.fastForward(60_000)
    expect(api.state.calls).toEqual([])
    await expect(card(page).getByRole('button', { name: 'Retry' })).toBeEnabled()

    await card(page).getByRole('button', { name: 'Retry' }).click()
    await expect(page.getByText(FINISHED)).toBeVisible()
    await expect(card(page)).toHaveCount(0)
    expect(api.state.calls).toEqual(['resume'])
  })

  test('resumes by itself after the wait and keeps the transcript', async ({ page }) => {
    const api = stubChat(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(card(page)).toHaveAttribute('data-phase', 'waiting')
    await freezeClock(page)
    await page.clock.fastForward(3500)
    await expect(page.getByText(FINISHED)).toBeVisible()
    await expect(page.getByText('I will read the three source files first.')).toHaveCount(1)
    await expect(card(page)).toHaveCount(0)
    expect(api.state.calls).toEqual(['resume'])
  })

  test('gives up after three tries and says so', async ({ page }) => {
    const api = stubChat(page, { resumeWorks: false })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(card(page)).toHaveAttribute('data-phase', 'waiting')
    await freezeClock(page)
    await page.clock.fastForward(3500)
    await expect(page.getByTestId('chat-error-resume')).toHaveText('Going on in 6 seconds (try 2 of 3).')
    await page.clock.fastForward(6500)
    await expect(page.getByTestId('chat-error-resume')).toHaveText('Going on in 12 seconds (try 3 of 3).')
    await page.clock.fastForward(12_500)
    await expect(card(page)).toHaveAttribute('data-phase', 'exhausted')
    await expect(page.getByTestId('chat-error-resume')).toHaveText('It could not go on after 3 tries. Retry to try again.')
    expect(api.state.calls).toHaveLength(3)
    await page.clock.fastForward(120_000)
    expect(api.state.calls).toHaveLength(3)
    // The stream is still down, but the card is all the page shows: the
    // reconnect notice would repeat it.
    await expect(page.getByTestId('chat-reconnecting')).toHaveCount(0)
    await shot(page, 'exhausted')
  })

  test('shows that the server goes on by itself and starts no resume of its own', async ({ page }) => {
    const api = stubChat(page, { willResume: true, tried: 1 })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByText('I will read the three source files first.')).toBeVisible()
    await expect(card(page)).toHaveAttribute('data-phase', 'server')
    await expect(page.getByTestId('chat-error-resume')).toHaveText('The server is going on from where it stopped.')
    await expect(page.getByTestId('chat-error-stop')).toHaveCount(0)
    await shot(page, 'server')
    await page.clock.fastForward(20_000)
    expect(api.state.calls).toEqual([])
    // The server resumed and finished; the page finds out when it reads the chat again.
    api.state.will_resume = false
    api.state.last_turn = 'done'
    api.state.messages = [...CUT.filter(m => m.role !== 'checkpoint'), line(5, 'assistant', FINISHED), line(6, 'checkpoint', '')]
    await page.clock.fastForward(15_000)
    await expect(page.getByText(FINISHED)).toBeVisible()
    await expect(card(page)).toHaveCount(0)
    expect(api.state.calls).toEqual([])
  })

  test('does not retry a chat that the server gave up on', async ({ page }) => {
    const api = stubChat(page, { tried: 3 })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(card(page)).toHaveAttribute('data-phase', 'exhausted')
    await expect(page.getByTestId('chat-error-resume')).toHaveText('It could not go on after 3 tries. Retry to try again.')
    await page.clock.fastForward(120_000)
    expect(api.state.calls).toEqual([])
    await expect(page.getByTestId('chat-reconnecting')).toHaveCount(0)
    await card(page).getByRole('button', { name: 'Retry' }).click()
    await expect(page.getByText(FINISHED)).toBeVisible()
    expect(api.state.calls).toEqual(['resume'])
  })

  test('a failed turn is not resumed by the page', async ({ page }) => {
    const api = stubChat(page, { last: 'failed' })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByText('I will read the three source files first.')).toBeVisible()
    await page.clock.fastForward(60_000)
    expect(api.state.calls).toEqual([])
  })

  test('says so when the event stream stays down', async ({ page }) => {
    const api = stubChat(page, { last: 'done' })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByText('I will read the three source files first.')).toBeVisible()
    await expect(page.getByTestId('chat-reconnecting')).toHaveCount(0)
    await page.clock.fastForward(2500)
    await expect(page.getByTestId('chat-reconnecting')).toHaveText('Connection lost. Reconnecting.')
    await shot(page, 'reconnecting')
  })
})
