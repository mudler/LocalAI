import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The loops of a chat, with the API stubbed by a small model of the server:
// the card that asks before the first loop of a chat runs, the line of each
// loop above the message box with Pause, Resume and Stop, the reason of a loop
// that ended, and the label of a message that a loop sent. The server side
// (firing, limits, the yes stored with the chat) is tested in Go.

const AT = '2026-10-31T09:00:00Z'
const SHOTS = process.env.LOOP_SHOTS || ''

const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Keep an eye on the build.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'Sure, I will check it every few minutes.', at: AT },
  { seq: 3, turn: 2, role: 'user', text: 'Check the build and tell me when it is green.', loop: { id: 'l1', run: 4 }, at: AT },
  { seq: 4, turn: 2, role: 'assistant', text: 'Still red: two tests fail in the store package.', at: AT },
]

const loop = (extra = {}) => ({
  id: 'l1', prompt: 'Check the build and tell me when it is green.', interval: '5m', interval_seconds: 300,
  state: 'active', runs: 4, running: false, next: '2026-10-31T09:20:00Z', created: AT, ...extra,
})

// The server, in memory: the loops of chat c1 and the calls the page made.
async function serve(page, { loops = [loop()] } = {}) {
  const s = { loops: loops.map(l => ({ ...l })), calls: [] }
  await mockChat(page)
  const detail = () => ({
    id: 'c1', title: 'Build watch', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done',
    pending: [], loops: s.loops,
  })
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: HISTORY } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail(), pending: undefined, loops: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.route(/\/api\/chats\/c1\/loops(\/[a-z0-9]+)?$/, (route) => {
    const req = route.request()
    const id = new URL(req.url()).pathname.split('/').pop()
    s.calls.push(`${req.method()} ${id === 'loops' ? '' : id}`.trim())
    if (req.method() === 'GET') return route.fulfill({ json: { loops: s.loops } })
    if (req.method() === 'DELETE') {
      const n = s.loops.length
      s.loops = id === 'loops' ? [] : s.loops.filter(l => l.id !== id)
      return route.fulfill({ json: id === 'loops' ? { stopped: n } : { stopped: true } })
    }
    if (req.method() === 'PUT') {
      const { state } = req.postDataJSON()
      s.loops = s.loops.map(l => (l.id === id ? { ...l, state, next: state === 'active' ? AT : undefined } : l))
      return route.fulfill({ json: s.loops.find(l => l.id === id) })
    }
    if (req.method() === 'POST') {
      const body = req.postDataJSON()
      const created = loop({ id: `n${s.loops.length + 1}`, prompt: body.prompt, interval: body.interval || '', interval_seconds: body.interval ? 300 : 0, state: 'active', runs: 0 })
      s.loops = [...s.loops, created]
      return route.fulfill({ status: 201, json: created })
    }
    return route.fallback()
  })
  return s
}

async function open(page, opts) {
  const s = await serve(page, opts)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-message').nth(3)).toBeVisible()
  return s
}

async function shot(page, name) {
  if (!SHOTS) return
  await page.getByTestId('chat-composer').scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${SHOTS}/${name}.png` })
}

for (const [name, size] of [['desktop', { width: 1440, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
  test.describe(name, () => {
    test.use({ viewport: size })

    test('the first loop of a chat asks before it runs; Start confirms it', async ({ page }) => {
      const s = await open(page, { loops: [loop({ state: 'pending', runs: 0, next: undefined })] })
      const card = page.getByTestId('loop-confirm')
      await expect(card).toContainText('Run this every 5 minutes until you stop it?')
      await expect(card).toContainText('Check the build')
      await expect(page.getByTestId('loop-chip')).toHaveCount(0)
      await shot(page, `${name}-confirm`)
      await card.getByTestId('loop-confirm-start').click()
      await expect(card).toHaveCount(0)
      await expect(page.getByTestId('loop-chip')).toHaveAttribute('data-state', 'active')
      expect(s.calls).toContain('PUT l1')
    })

    test('Cancel removes a loop that waits for a yes', async ({ page }) => {
      const s = await open(page, { loops: [loop({ state: 'pending', runs: 0, interval: '', interval_seconds: 0 })] })
      await expect(page.getByTestId('loop-confirm')).toContainText('each time the model asks')
      await page.getByTestId('loop-confirm-cancel').click()
      await expect(page.getByTestId('loop-bar')).toHaveCount(0)
      expect(s.calls).toContain('DELETE l1')
    })

    test('a running loop shows its pace and run, and pauses, resumes and stops', async ({ page }) => {
      const s = await open(page)
      const chip = page.getByTestId('loop-chip')
      await expect(chip.getByTestId('loop-chip-text')).toHaveText(/^every 5 min · run 4 · next at /)
      await shot(page, `${name}-chip`)
      await chip.getByTestId('loop-pause').click()
      await expect(chip).toHaveAttribute('data-state', 'paused')
      await expect(chip.getByTestId('loop-chip-text')).toHaveText('every 5 min · run 4 · paused')
      await shot(page, `${name}-paused`)
      await chip.getByTestId('loop-resume').click()
      await expect(chip).toHaveAttribute('data-state', 'active')
      await chip.getByTestId('loop-stop').click()
      await expect(page.getByTestId('loop-bar')).toHaveCount(0)
      expect(s.calls).toEqual(expect.arrayContaining(['PUT l1', 'DELETE l1']))
    })

    test('a loop that ended says why and can be dismissed', async ({ page }) => {
      await open(page, { loops: [
        loop({ id: 'l2', state: 'ended', next: undefined, interval: '', interval_seconds: 0, reason: 'Done: the build is green' }),
        loop({ id: 'l3', state: 'paused', runs: 3, next: undefined, reason: 'Paused after 3 failed runs in a row.' }),
      ] })
      const chips = page.getByTestId('loop-chip')
      await expect(chips).toHaveCount(2)
      // Paused first, then ended.
      await expect(chips.nth(0)).toHaveAttribute('data-state', 'paused')
      await expect(chips.nth(0).getByTestId('loop-chip-why')).toHaveText('Paused after 3 failed runs in a row.')
      await expect(chips.nth(1).getByTestId('loop-chip-text')).toHaveText('ended')
      await expect(chips.nth(1).getByTestId('loop-chip-why')).toHaveText('Done: the build is green')
      await shot(page, `${name}-ended`)
      await chips.nth(1).getByTestId('loop-dismiss').click()
      await expect(chips).toHaveCount(1)
    })

    test('a message that a loop sent carries a label, one you typed does not', async ({ page }) => {
      await open(page)
      const users = page.locator('[data-testid="chat-message"][data-role="user"]')
      await expect(users.nth(1).getByTestId('message-loop-label')).toHaveText('Loop run 4')
      await expect(users.nth(0).getByTestId('message-loop-label')).toHaveCount(0)
      await users.nth(1).scrollIntoViewIfNeeded()
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}-label.png` })
    })
  })
}

for (const theme of ['light', 'dark']) {
  test.describe(theme, () => {
    test.use({ viewport: { width: 1440, height: 900 }, colorScheme: theme })
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private mode */ } }, theme)
    })

    test('the card and the chips read in this theme', async ({ page }) => {
      await open(page, { loops: [loop({ id: 'p1', state: 'pending', runs: 0 }), loop(), loop({ id: 'l3', state: 'paused', runs: 3, next: undefined, reason: 'Paused after 3 failed runs in a row.' })] })
      await expect(page.getByTestId('loop-confirm')).toBeVisible()
      await expect(page.getByTestId('loop-chip')).toHaveCount(2)
      await shot(page, `desktop-${theme}`)
    })

    test('on a phone too', async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 })
      await open(page, { loops: [loop({ id: 'p1', state: 'pending', runs: 0 }), loop()] })
      await expect(page.getByTestId('loop-chip')).toHaveCount(1)
      await shot(page, `phone-${theme}`)
    })
  })
}
