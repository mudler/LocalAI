import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The card of a trigger the assistant proposes, with the API stubbed by a small
// model of the server: where the card sits in the message, the receipt after
// Accept, Undo and what refuses it, the Declined line, and the state after a
// reload. That the server keeps the answer is tested in Go and in
// chats-features-real.spec.js.

const AT = '2026-10-30T09:00:00Z'
const PID = 'p1'
const trigger = (extra = {}) => ({
  id: PID, name: 'Monday digest', prompt: 'Sum up the week.', kind: 'schedule', schedule: 'weekly mon 09:00', time_zone: 'Europe/Rome',
  daily_cap: 1, enabled: false, model: 'qwen3-8b', source_chat_id: 'c1', summary: 'Every Monday at 09:00 (Europe/Rome)',
  plan: {}, cost: { max_runs_per_day: 1 }, ...extra,
})
const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Send me a digest every Monday.', at: AT },
  { seq: 2, turn: 1, role: 'tool', text: 'propose_trigger: Monday digest\nProposed it.', card: { kind: 'trigger_proposal', id: PID }, at: AT },
  { seq: 3, turn: 1, role: 'assistant', text: 'I can send you a digest every Monday morning. Create it below if that looks right.', at: AT },
]

// The server, in memory: the state of the proposal and what each call does.
async function serve(page, { messages = HISTORY, undoRefusal = '', confirmFails = false } = {}) {
  const s = { state: 'pending', canUndo: true, blocked: '', calls: [] }
  const view = () => ({
    state: s.state,
    trigger: trigger(s.state === 'accepted' ? { enabled: true, next_run: '2026-11-02T08:00:00Z' } : {}),
    answered_at: s.state === 'pending' ? undefined : AT,
    can_undo: s.state === 'accepted' && s.canUndo,
    undo_blocked: s.blocked || undefined,
  })
  await mockChat(page)
  const detail = { id: 'c1', title: 'Digest', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route('**/api/chats/triggers/preview*', route => route.fulfill({ json: { times: ['2026-11-02T08:00:00Z'], summary: 'Every Monday at 09:00' } }))
  await page.route(/\/api\/chats\/triggers\/proposals\/p1(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const verb = new URL(req.url()).pathname.split('/').pop()
    const tail = verb === PID ? '' : verb
    s.calls.push(`${req.method()} ${tail}`)
    if (req.method() === 'GET') return route.fulfill({ json: view() })
    if (tail === 'confirm') {
      if (confirmFails) return route.fulfill({ status: 400, json: { error: { message: 'the folder does not exist in your files any more' } } })
      s.state = 'accepted'
    } else if (tail === 'undo') {
      if (undoRefusal) {
        s.canUndo = false
        s.blocked = 'ran'
        return route.fulfill({ status: 409, json: { error: { message: undoRefusal } } })
      }
      s.state = 'undone'
    } else if (tail === 'reopen') s.state = 'pending'
    else if (req.method() === 'DELETE') s.state = 'declined'
    return route.fulfill({ json: view() })
  })
  return s
}

const card = (page) => page.getByTestId('trigger-proposal')

async function open(page, opts) {
  const s = await serve(page, opts)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-message').nth(1)).toBeVisible()
  return s
}

for (const [name, size] of [['desktop', { width: 1440, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
  test.describe(name, () => {
    test.use({ viewport: size })

    test('the card sits below the text of its message and above its actions', async ({ page }) => {
      await open(page)
      await expect(card(page)).toHaveAttribute('data-state', 'proposed')
      const text = await page.locator('[data-testid="chat-message"][data-role="assistant"] .cx-prose, [data-testid="chat-message"][data-role="assistant"] .prose').first().boundingBox()
      const box = await card(page).boundingBox()
      const acts = await page.locator('[data-testid="chat-message"][data-role="assistant"] [data-testid="message-actions"]').boundingBox()
      expect(text, 'the message text was not found').not.toBeNull()
      expect(box.y).toBeGreaterThanOrEqual(text.y + text.height - 1)
      expect(acts.y).toBeGreaterThanOrEqual(box.y + box.height - 1)
      await page.screenshot({ path: `test-results/proposal-${name}-pending.png` })
    })

    test('accepting leaves a receipt in place, undo takes it back, and the state survives a reload', async ({ page }) => {
      const s = await open(page)
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'accepted')
      await expect(card(page).getByTestId('receipt-when')).toHaveText('Every Monday at 09:00 (Europe/Rome)')
      await expect(card(page)).toContainText('Scheduled:')
      await expect(card(page).getByTestId('receipt-title')).toHaveText('Monday digest')
      await expect(card(page).getByTestId('receipt-next')).toContainText('Next run')
      await expect(card(page).getByRole('link', { name: 'View trigger' })).toHaveAttribute('href', /\/app\/triggers\/p1$/)
      await expect(page.getByRole('status').filter({ hasText: 'Scheduled Monday digest.' })).toHaveCount(1)
      await page.screenshot({ path: `test-results/proposal-${name}-receipt.png` })

      await page.reload()
      await expect(card(page)).toHaveAttribute('data-state', 'accepted')

      await card(page).getByRole('button', { name: 'Undo' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'undone')
      await expect(card(page)).toContainText('Undone:')
      await expect(page.getByRole('status').filter({ hasText: 'Undid Monday digest.' })).toHaveCount(1)
      await page.screenshot({ path: `test-results/proposal-${name}-undone.png` })
      await page.reload()
      await expect(card(page)).toHaveAttribute('data-state', 'undone')

      await card(page).getByRole('button', { name: 'Schedule it again' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'proposed')
      expect(s.calls).toContain('POST undo')
      expect(s.calls).toContain('POST reopen')
    })

    test('a trigger that already ran cannot be undone, and the card says so', async ({ page }) => {
      await open(page, { undoRefusal: 'It already ran once; remove it from the Triggers page.' })
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await card(page).getByRole('button', { name: 'Undo' }).click()
      await expect(card(page).getByTestId('receipt-blocked')).toHaveText('It already ran once; remove it from the Triggers page.')
      await expect(card(page).getByRole('button', { name: 'Undo' })).toHaveCount(0)
      await expect(card(page)).toHaveAttribute('data-state', 'accepted')
      await page.screenshot({ path: `test-results/proposal-${name}-blocked.png` })
    })

    test('a failed accept says why in the card and keeps the proposal', async ({ page }) => {
      await open(page, { confirmFails: true })
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await expect(card(page).getByRole('alert')).toContainText('the folder does not exist in your files any more')
      await expect(card(page)).toHaveAttribute('data-state', 'proposed')
      await page.screenshot({ path: `test-results/proposal-${name}-error.png` })
    })

    test('declining leaves a quiet line that can be restored', async ({ page }) => {
      await open(page)
      await card(page).getByRole('button', { name: 'Dismiss' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'declined')
      await expect(card(page)).toContainText('Declined:')
      await expect(page.getByRole('status').filter({ hasText: 'Declined Monday digest.' })).toHaveCount(1)
      await page.screenshot({ path: `test-results/proposal-${name}-declined.png` })
      await page.reload()
      await expect(card(page)).toHaveAttribute('data-state', 'declined')
      await card(page).getByRole('button', { name: 'Restore' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'proposed')
    })
  })
}

test('the receipt is reachable and undoable from the keyboard', async ({ page }) => {
  await open(page)
  await card(page).getByRole('button', { name: 'Create trigger' }).focus()
  await page.keyboard.press('Enter')
  await expect(card(page)).toHaveAttribute('data-state', 'accepted')
  await card(page).getByRole('link', { name: 'View trigger' }).focus()
  await page.keyboard.press('Tab')
  await expect(card(page).getByRole('button', { name: 'Undo' })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(card(page)).toHaveAttribute('data-state', 'undone')
})

test('a trigger removed on the Triggers page reads as removed', async ({ page }) => {
  await serve(page)
  await page.route(/\/api\/chats\/triggers\/proposals\/p1$/, route => route.fulfill({ status: 404, json: { error: { message: 'trigger not found' } } }))
  await page.goto('/app/chats/c1')
  await expect(card(page)).toHaveAttribute('data-state', 'removed')
  await expect(card(page)).toContainText('This trigger was removed.')
})
