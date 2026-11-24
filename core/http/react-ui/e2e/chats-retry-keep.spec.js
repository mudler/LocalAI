import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// A long turn that fails part way: the card stands after the kept transcript,
// and Retry goes on from there. The chats API is stubbed with a small state
// machine, so the transcript before and after the click can be compared.

const AT = '2026-11-05T10:00:00Z'
const line = (seq, role, text, extra = {}) => ({ seq, turn: 1, role, text, at: AT, ...extra })

const FAILED_TRANSCRIPT = [
  line(1, 'user', 'build the quarterly report'),
  line(2, 'assistant', 'I will read the three source files first.'),
  line(3, 'tool', 'read_file: sales.csv\n240 rows'),
  line(4, 'assistant', 'The sales file has 240 rows. Now the costs.'),
  line(5, 'tool', 'read_file: costs.csv\n180 rows'),
  line(6, 'failure', 'The provider behind the model failed several times in a row (HTTP 502), so the turn was stopped.', {
    failure: { code: 'upstream', detail: 'the model failed 3 times in a row (HTTP 502)' },
  }),
  line(7, 'checkpoint', ''),
]

function stubChat(page) {
  const state = { messages: [...FAILED_TRANSCRIPT], running: false, last_turn: 'failed', calls: [] }
  const detail = () => ({
    id: 'c1', title: 'Quarterly report', model: 'qwen3-8b', kind: 'chat', created: AT, updated: new Date().toISOString(),
    running: state.running, last_turn: state.last_turn, pending: [],
  })
  return {
    state,
    async install() {
      await page.route('**/api/chats/c1/events', route => route.abort())
      await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
      await page.route('**/api/chats/c1/resume', async route => {
        state.calls.push('resume')
        // The server goes on from the failed turn: nothing is removed, the
        // rest of the work is added after the kept lines.
        state.messages = [
          ...state.messages.filter(m => m.role !== 'checkpoint'),
          line(8, 'assistant', 'The costs file has 180 rows. The report is ready: revenue is up 12 percent.'),
          line(9, 'checkpoint', ''),
        ]
        state.last_turn = 'done'
        await route.fulfill({ json: { started: true } })
      })
      await page.route('**/api/chats/c1/regenerate', async route => {
        state.calls.push('regenerate')
        await route.fulfill({ json: { started: true } })
      })
      await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
      await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
        json: { conversations: [{ ...detail(), pending: undefined }], persistent: true, files: true, files_reason: '' },
      }))
    },
  }
}

test.describe('Retry of a failed turn', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the card stands after the kept transcript and says what failed', async ({ page }) => {
    // Tall enough to show the whole transcript in the screenshot.
    await page.setViewportSize({ width: 1280, height: 1100 })
    const api = stubChat(page)
    await api.install()
    await page.goto('/app/chats/c1')

    // Read from the server after a plain load: no live event was seen.
    await expect(page.getByText('The sales file has 240 rows. Now the costs.')).toBeVisible()
    const card = page.getByTestId('chat-error')
    await expect(card).toBeVisible()
    await expect(card).toHaveAttribute('data-code', 'upstream')
    await expect(page.getByTestId('chat-error-reason')).toContainText('The provider behind the model returned an error.')
    await expect(page.getByTestId('chat-error-reason')).toContainText('What was written so far is kept above.')
    await expect(card.getByRole('button', { name: 'Retry' })).toBeEnabled()

    // The card comes after the last kept message, not before.
    const order = await page.evaluate(() => {
      const last = [...document.querySelectorAll('[data-testid="chat-message"]')].pop()
      const err = document.querySelector('[data-testid="chat-error"]')
      return last && err ? Boolean(last.compareDocumentPosition(err) & Node.DOCUMENT_POSITION_FOLLOWING) : null
    })
    expect(order).toBe(true)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await page.screenshot({ path: process.env.RETRY_SHOTS ? `${process.env.RETRY_SHOTS}/failure-card-kept-transcript.png` : 'test-results/failure-card-kept-transcript.png' })
  })

  test('Retry goes on without removing a line, and a double click is one call', async ({ page }) => {
    const api = stubChat(page)
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-error')).toBeVisible()

    await page.setViewportSize({ width: 1280, height: 1100 })
    await page.getByTestId('chat-error').getByRole('button', { name: 'Retry' }).dblclick()

    await expect(page.getByText('The report is ready: revenue is up 12 percent.')).toBeVisible()
    await expect(page.getByTestId('chat-error')).toHaveCount(0)
    // Everything the failed turn did is still on the page, once.
    await expect(page.getByText('I will read the three source files first.')).toHaveCount(1)
    await expect(page.getByText('The sales file has 240 rows. Now the costs.')).toHaveCount(1)
    await expect(page.getByText('build the quarterly report')).toHaveCount(1)
    expect(api.state.calls).toEqual(['resume'])
    await page.screenshot({ path: process.env.RETRY_SHOTS ? `${process.env.RETRY_SHOTS}/after-retry.png` : 'test-results/after-retry.png' })
  })
})
