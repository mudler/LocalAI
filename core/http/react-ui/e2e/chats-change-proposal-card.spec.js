import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The card of a change that a model proposed (propose_change): what it would
// write as a diff, Confirm, Edit then confirm, Dismiss, then a receipt with
// Undo, and the states expired, stale and failed. The API is stubbed by a
// small model of the server. That the server enforces the rules is tested in
// Go; here the card is checked for what the person sees and sends.

const AT = '2026-11-03T09:00:00Z'
const CURRENT = '- home: Rome\n- job: dev\n- pet: dog\n'
const PROPOSED = '- home: Milan\n- job: dev\n- pet: dog\n- tea: green\n'

const proposal = (extra = {}) => ({
  v: 1, id: 'cp1', owner: 'alice', kind: 'memory', scope: 'user', target: 'Memory/index.md',
  summary: 'You live in Milan, not Rome', content: PROPOSED, base_hash: 'abc', source: { chat: 'c1' }, status: 'pending',
  created: AT, expires: new Date(Date.now() + 20 * 3600_000 + 5 * 60_000).toISOString(), ...extra,
})

const history = [
  { seq: 1, turn: 1, role: 'user', text: 'I moved to Milan.', at: AT },
  { seq: 2, turn: 1, role: 'tool', text: 'propose_change: You live in Milan, not Rome\nStored.', card: { kind: 'change_proposal', id: 'cp1' }, at: AT },
  { seq: 3, turn: 1, role: 'assistant', text: 'I proposed a change to your notes. Confirm it below.', at: AT },
]

// The server, in memory: one proposal and what each call does to it.
async function serve(page, { p = proposal(), file = CURRENT, confirmFails = 0, confirmStatus = 200 } = {}) {
  const s = { p, calls: [], bodies: [], sent: [], failsLeft: confirmFails }
  await mockChat(page)
  const detail = { id: 'c1', title: 'Notes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, (route) => {
    if (route.request().method() === 'POST') { s.sent.push(route.request().postDataJSON()); return route.fulfill({ json: { started: true } }) }
    return route.fulfill({ json: { messages: history } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route('**/api/chats/files/content*', route => route.fulfill(file === null ? { status: 404, json: { error: { message: 'not found' } } } : { status: 200, contentType: 'text/plain', body: file }))
  await page.route(/\/api\/chats\/changes(\?.*)?$/, route => route.fulfill({ json: s.p.status === 'pending' ? [s.p] : [] }))
  await page.route(/\/api\/chats\/changes\/cp1(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const verb = new URL(req.url()).pathname.split('/').pop()
    const tail = verb === 'cp1' ? '' : verb
    s.calls.push(`${req.method()} ${tail}`.trim())
    if (req.method() === 'GET') return route.fulfill({ json: s.p })
    if (tail === 'confirm') {
      s.bodies.push(req.postDataJSON())
      if (s.failsLeft > 0) { s.failsLeft--; return route.fulfill({ status: 500, json: { error: { message: 'the request failed' } } }) }
      if (confirmStatus === 409) { s.p = { ...s.p, status: 'stale' }; return route.fulfill({ status: 409, json: { error: { message: 'studio: the file changed since this was proposed' } } }) }
      s.p = { ...s.p, status: 'applied', rev: 7, answered: AT }
    } else if (tail === 'dismiss') s.p = { ...s.p, status: 'dismissed', answered: AT }
    else if (tail === 'undo') s.p = { ...s.p, status: 'reverted', revert_rev: 8 }
    return route.fulfill({ json: s.p })
  })
  return s
}

const card = (page) => page.getByTestId('change-proposal')

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
    await page.waitForTimeout(500)
    await card(page).screenshot({ path: `test-results/change-proposal-${name}-${theme}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

test('a pending change shows its kind, target, source, expiry and a line diff, and nothing is sent yet', async ({ page }) => {
  const s = await open(page)
  await expect(card(page)).toHaveAttribute('data-state', 'pending')
  await expect(card(page)).toContainText('You live in Milan, not Rome')
  await expect(card(page)).toContainText('Nothing is saved until you confirm.')
  await expect(card(page).locator('[data-row="kind"]')).toContainText('Memory · All your chats')
  await expect(card(page).locator('[data-row="target"] code')).toHaveText('Memory/index.md')
  await expect(card(page).locator('[data-row="source"] dd')).toHaveText('A chat')
  await expect(card(page).locator('[data-row="expires"]')).toContainText(/20 h \d+ min/)
  const diff = card(page).getByTestId('change-diff')
  await expect(diff.getByTestId('change-diff-stat')).toContainText('2 lines added')
  await expect(diff.getByTestId('change-diff-stat')).toContainText('1 line removed')
  await expect(diff.locator('[data-op="del"]')).toHaveText(/Removed:\s*- home: Rome/)
  await expect(diff.locator('[data-op="add"]')).toHaveCount(2)
  await expect(diff.locator('[data-op="add"]').first()).toHaveText(/Added:\s*- home: Milan/)
  // The card sits below the text of its message.
  // Both boxes are read in one step: the chat scrolls to its end as it fills,
  // and two reads apart can see two scroll positions.
  await expect.poll(() => page.evaluate(() => {
    const text = document.querySelector('[data-testid="chat-message"][data-role="assistant"] .cx-prose, [data-testid="chat-message"][data-role="assistant"] .prose').getBoundingClientRect()
    const box = document.querySelector('[data-testid="change-proposal"]').getBoundingClientRect()
    return box.y - (text.y + text.height)
  })).toBeGreaterThanOrEqual(-1)
  expect(s.calls).toEqual(['GET'])
  await shoot(page, 'pending')
})

test('Confirm applies the change, shows a receipt, and Undo takes it back', async ({ page }) => {
  const s = await open(page)
  await card(page).getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await expect(card(page)).toContainText('Applied:')
  await expect(card(page)).toContainText('You live in Milan, not Rome')
  await expect(page.getByRole('status').filter({ hasText: 'Change applied.' })).toHaveCount(1)
  expect(s.bodies).toEqual([{}])
  await shoot(page, 'applied')

  await page.reload()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await card(page).getByRole('button', { name: 'Undo' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'reverted')
  await expect(card(page)).toContainText('Undone:')
  await expect(card(page).getByRole('button', { name: 'Undo' })).toHaveCount(0)
  expect(s.calls).toEqual(expect.arrayContaining(['POST confirm', 'POST undo']))
})

test('a note the host saved at once says so, offers Undo, and never showed a confirmation', async ({ page }) => {
  const s = await open(page, { p: proposal({ status: 'applied', auto: true, rev: 7, answered: AT }) })
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  await expect(card(page)).toHaveAttribute('data-auto', 'true')
  await expect(card(page)).toContainText('Saved to memory:')
  await expect(card(page)).not.toContainText('Applied:')
  await expect(card(page)).toContainText('You live in Milan, not Rome')
  await expect(card(page).getByRole('button', { name: 'Confirm', exact: true })).toHaveCount(0)
  await expect(card(page).getByRole('button', { name: 'Dismiss' })).toHaveCount(0)
  await shoot(page, 'saved-at-once')

  await card(page).getByRole('button', { name: 'Undo' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'reverted')
  await expect(card(page)).toContainText('Undone:')
  expect(s.calls).toEqual(expect.arrayContaining(['POST undo']))
  expect(s.calls).not.toContain('POST confirm')
})

test('Edit then confirm sends the edited text, which the person can correct first', async ({ page }) => {
  const s = await open(page)
  await card(page).getByRole('button', { name: 'Edit then confirm' }).click()
  const area = card(page).getByLabel('Text to save in Memory/index.md')
  await expect(area).toBeFocused()
  await expect(area).toHaveValue(PROPOSED)
  await expect(card(page).getByTestId('change-diff')).toHaveCount(0)
  await area.fill('- home: Milan\n- tea: black\n')
  await shoot(page, 'editing')
  await card(page).getByRole('button', { name: 'Confirm with my text' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  expect(s.bodies).toEqual([{ content: '- home: Milan\n- tea: black\n' }])
})

test('Escape and Cancel leave the editor and keep the proposal waiting', async ({ page }) => {
  const s = await open(page)
  await card(page).getByRole('button', { name: 'Edit then confirm' }).click()
  await card(page).getByLabel('Text to save in Memory/index.md').press('Escape')
  await expect(card(page).getByRole('button', { name: 'Confirm', exact: true })).toBeVisible()
  await expect(card(page)).toHaveAttribute('data-state', 'pending')
  expect(s.calls).toEqual(['GET'])
})

test('Dismiss leaves a quiet line and writes nothing', async ({ page }) => {
  const s = await open(page)
  await card(page).getByRole('button', { name: 'Dismiss' }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'dismissed')
  await expect(card(page)).toContainText('Dismissed:')
  expect(s.calls).toContain('POST dismiss')
  expect(s.calls).not.toContain('POST confirm')
})

test('the buttons work from the keyboard', async ({ page }) => {
  await open(page)
  await card(page).getByRole('button', { name: 'Confirm', exact: true }).focus()
  await page.keyboard.press('Tab')
  await expect(card(page).getByRole('button', { name: 'Edit then confirm' })).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(card(page).getByRole('button', { name: 'Dismiss' })).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Enter')
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
})

test('an expired proposal says so and offers to ask again, which sends a message to the chat', async ({ page }) => {
  const s = await open(page, { p: proposal({ status: 'expired', expires: AT }) })
  await expect(card(page)).toHaveAttribute('data-state', 'expired')
  await expect(card(page)).toContainText('Expired:')
  await expect(card(page).getByTestId('change-why')).toContainText('waited more than a day')
  await expect(card(page).getByRole('button', { name: 'Confirm', exact: true })).toHaveCount(0)
  await shoot(page, 'expired')
  await card(page).getByRole('button', { name: 'Ask again' }).click()
  await expect.poll(() => s.sent.length).toBe(1)
  expect(s.sent[0].text).toContain('propose this change again')
})

test('a pending proposal whose time ran out shows as expired without waiting for the server', async ({ page }) => {
  await open(page, { p: proposal({ expires: new Date(Date.now() - 1000).toISOString() }) })
  await expect(card(page)).toHaveAttribute('data-state', 'expired')
})

test('a file that changed since the proposal is stale: the card says nothing was saved and offers to ask again', async ({ page }) => {
  const s = await open(page, { confirmStatus: 409 })
  await card(page).getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'stale')
  await expect(card(page)).toContainText('Not applied:')
  await expect(card(page).getByTestId('change-why')).toContainText('file changed after this was proposed')
  await shoot(page, 'stale')
  await card(page).getByRole('button', { name: 'Ask again' }).click()
  await expect.poll(() => s.sent.length).toBe(1)
  expect(s.sent[0].text).toContain('Memory/index.md')
})

test('a write that failed puts the proposal back to waiting, with the reason', async ({ page }) => {
  const s = await open(page, { confirmFails: 1 })
  await card(page).getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(card(page).getByTestId('change-error')).toContainText('Could not save the change')
  await expect(card(page).getByTestId('change-error')).toContainText('still waiting')
  await expect(card(page)).toHaveAttribute('data-state', 'pending')
  await shoot(page, 'failed')
  await card(page).getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(card(page)).toHaveAttribute('data-state', 'applied')
  expect(s.calls.filter(c => c === 'POST confirm')).toHaveLength(2)
})

test('a new file is all added, and a kind or source the card does not know is drawn generically', async ({ page }) => {
  await open(page, {
    file: null,
    p: proposal({ kind: 'widget', scope: 'channel', target: 'Memory/channels/room/index.md', summary: 'Add the team rule', source: { kind: 'connector', id: 'team-bot', chat: 'c1', sender: 'Ada' } }),
  })
  await expect(card(page)).toHaveAttribute('data-kind', 'widget')
  await expect(card(page).locator('[data-row="kind"]')).toContainText('widget · This conversation')
  await expect(card(page).locator('[data-row="source"] dd')).toHaveText('Connector team-bot, from Ada')
  await expect(card(page).getByTestId('change-diff-stat')).toContainText('New file')
  await expect(card(page).getByTestId('change-diff').locator('[data-op="del"]')).toHaveCount(0)
})

test('a profile change names its agent', async ({ page }) => {
  await open(page, { p: proposal({ kind: 'profile', scope: 'agent', agent: 'ag1', target: 'LocalAI/agents/ag1/profile.md', summary: 'Be brief' }) })
  await expect(card(page).locator('[data-row="kind"]')).toContainText('Profile · Agent ag1')
})

test('on a phone the card fits and keeps its buttons in reach', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 })
  await open(page)
  const box = await card(page).boundingBox()
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(390)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  // The whole screen, to see the card between the header and the composer.
  await card(page).scrollIntoViewIfNeeded()
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(500)
    await page.screenshot({ path: `test-results/change-proposal-phone-screen-${theme}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
})
