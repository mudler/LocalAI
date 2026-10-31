import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The card of a change of a trigger that the assistant asks for (update_trigger,
// delete_trigger), with the API stubbed by a small model of the server: an edit
// with Apply and Dismiss, a pause that applied at once with Undo, a delete with
// Delete, Keep and Undo, and a change the Triggers page overtook. That the
// server keeps the answer is tested in Go.

const AT = '2026-10-30T09:00:00Z'
const trigger = (extra = {}) => ({
  id: 'tr1', name: 'Nightly review', prompt: 'Review the open items.', kind: 'schedule', schedule: 'daily 08:00', time_zone: 'UTC',
  daily_cap: 24, enabled: true, model: 'qwen3-8b', summary: 'Every day at 08:00 (UTC)', plan: {}, cost: { max_runs_per_day: 1 }, ...extra,
})
const renamed = trigger({ name: 'Morning review', schedule: 'daily 07:30', summary: 'Every day at 07:30 (UTC)' })

const history = (tool, id) => [
  { seq: 1, turn: 1, role: 'user', text: 'Change my nightly review.', at: AT },
  { seq: 2, turn: 1, role: 'tool', text: `${tool}: Nightly review\nAsked.`, card: { kind: 'trigger_change', id }, at: AT },
  { seq: 3, turn: 1, role: 'assistant', text: 'Here is the change. Apply it below if it looks right.', at: AT },
]

// The server, in memory: one change and what each call does to it.
async function serve(page, { op = 'update', state = 'pending', stale = false } = {}) {
  const s = { state, calls: [] }
  const view = () => {
    let before = trigger()
    let after = op === 'delete' ? undefined : renamed
    let fields = ['name', 'when']
    if (op === 'pause') { after = trigger({ enabled: false }); fields = ['enabled'] }
    if (s.state === 'undone' && after) [before, after] = [after, before]
    return {
      id: 'ch1', op, state: s.state, trigger_id: 'tr1', before, after, fields: op === 'delete' ? [] : fields,
      answered_at: s.state === 'pending' ? undefined : AT, stale, can_undo: s.state === 'applied',
      last_run: { id: 'r1', status: 'done', due: AT, ended: AT },
    }
  }
  await mockChat(page)
  const detail = { id: 'c1', title: 'Review', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: history(op === 'delete' ? 'delete_trigger' : 'update_trigger', 'ch1') } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route(/\/api\/chats\/triggers\/changes\/ch1(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const verb = new URL(req.url()).pathname.split('/').pop()
    const tail = verb === 'ch1' ? '' : verb
    s.calls.push(`${req.method()} ${tail}`.trim())
    if (req.method() === 'GET') return route.fulfill({ json: view() })
    if (tail === 'apply') s.state = 'applied'
    else if (tail === 'undo') s.state = 'undone'
    else if (req.method() === 'DELETE') { s.state = 'dismissed'; return route.fulfill({ status: 204, body: '' }) }
    return route.fulfill({ json: view() })
  })
  return s
}

const card = (page) => page.getByTestId('trigger-change')

async function open(page, opts) {
  const s = await serve(page, opts)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-message').nth(1)).toBeVisible()
  await expect(card(page)).not.toHaveAttribute('data-state', 'loading')
  return s
}

async function shoot(page, name) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await card(page).screenshot({ path: `test-results/trigger-change-${name}-${theme}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

test('an edit shows the fields before and after, applies, and Undo takes it back', async ({ page }) => {
  const s = await open(page)
  await expect(card(page)).toHaveAttribute('data-state', 'pending')
  await expect(card(page)).toContainText('Change Nightly review')
  await expect(card(page)).toContainText('Nothing changes until you apply it.')
  const name = card(page).locator('[data-row="name"]')
  await expect(name.getByTestId('change-before')).toHaveText('Nightly review')
  await expect(name.getByTestId('change-after')).toHaveText('Morning review')
  await expect(card(page).locator('[data-row="when"] [data-testid="change-after"]')).toHaveText('Every day at 07:30 (UTC)')
  // The card sits below the text of its message.
  const text = await page.locator('[data-testid="chat-message"][data-role="assistant"] .cx-prose, [data-testid="chat-message"][data-role="assistant"] .prose').first().boundingBox()
  const box = await card(page).boundingBox()
  expect(box.y).toBeGreaterThanOrEqual(text.y + text.height - 1)
  await shoot(page, 'edit-pending')

  await card(page).getByRole('button', { name: 'Apply' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await expect(card(page)).toContainText('Changed:')
  await expect(card(page).getByTestId('change-fields')).toHaveText('What changed: Name, When')
  await expect(page.getByRole('status').filter({ hasText: 'Changed Nightly review.' })).toHaveCount(1)
  await shoot(page, 'edit-applied')

  await page.reload()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await card(page).getByRole('button', { name: 'Undo' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'undone')
  await expect(card(page)).toContainText('Undone:')
  expect(s.calls).toEqual(expect.arrayContaining(['POST apply', 'POST undo']))
})

test('a dismissed edit leaves a quiet line', async ({ page }) => {
  const s = await open(page)
  await card(page).getByRole('button', { name: 'Dismiss' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'dismissed')
  await expect(card(page)).toContainText('Change dismissed:')
  expect(s.calls).toContain('DELETE')
  expect(s.calls).not.toContain('POST apply')
})

test('a pause applied at once shows a receipt with Undo', async ({ page }) => {
  const s = await open(page, { op: 'pause', state: 'applied' })
  await expect(card(page)).toHaveAttribute('data-op', 'pause')
  await expect(card(page)).toContainText('Paused:')
  await expect(card(page).getByRole('link', { name: 'View trigger' })).toHaveAttribute('href', /\/app\/triggers\/tr1$/)
  await shoot(page, 'pause-applied')
  await card(page).getByRole('button', { name: 'Undo' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'undone')
  await expect(page.getByRole('status').filter({ hasText: 'Undid the change to Nightly review.' })).toHaveCount(1)
  expect(s.calls).toContain('POST undo')
})

test('a delete waits for Delete, and Undo restores the trigger', async ({ page }) => {
  const s = await open(page, { op: 'delete' })
  await expect(card(page)).toContainText('Delete Nightly review?')
  await expect(card(page).locator('[data-row="when"]')).toContainText('Every day at 08:00 (UTC)')
  await expect(card(page).locator('[data-row="lastRun"]')).toContainText('Done')
  await shoot(page, 'delete-pending')
  await card(page).getByRole('button', { name: 'Delete' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await expect(card(page)).toContainText('Deleted:')
  await expect(card(page).getByRole('link', { name: 'View trigger' })).toHaveCount(0)
  await shoot(page, 'delete-applied')
  await card(page).getByRole('button', { name: 'Undo' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'undone')
  await expect(card(page)).toContainText('Restored:')
  await expect(page.getByRole('status').filter({ hasText: 'Restored Nightly review.' })).toHaveCount(1)
  expect(s.calls).toEqual(expect.arrayContaining(['POST apply', 'POST undo']))
})

test('Keep leaves the trigger', async ({ page }) => {
  const s = await open(page, { op: 'delete' })
  await card(page).getByRole('button', { name: 'Keep' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'dismissed')
  await expect(card(page)).toContainText('Kept:')
  expect(s.calls).not.toContain('POST apply')
})

test('a change the Triggers page overtook offers only Dismiss', async ({ page }) => {
  await open(page, { stale: true })
  await expect(card(page)).toContainText('cannot apply')
  await expect(card(page).getByRole('button', { name: 'Apply' })).toHaveCount(0)
  await expect(card(page).getByRole('button', { name: 'Dismiss' })).toBeVisible()
})

test('a change the server no longer keeps reads as removed', async ({ page }) => {
  await serve(page)
  await page.route(/\/api\/chats\/triggers\/changes\/ch1$/, route => route.fulfill({ status: 404, json: { error: { message: 'trigger not found' } } }))
  await page.goto('/app/chats/c1')
  await expect(card(page)).toHaveAttribute('data-state', 'removed')
  await expect(card(page)).toContainText('This change is no longer kept.')
})

test('the card fits a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await open(page)
  const box = await card(page).boundingBox()
  expect(box.x + box.width).toBeLessThanOrEqual(390)
  await shoot(page, 'edit-phone')
})
