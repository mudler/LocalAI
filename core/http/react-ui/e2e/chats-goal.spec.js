import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The goal of a chat with the API stubbed: the bar above the composer, the
// progress in the status line, the goal lines of the transcript, and Pause,
// Resume and Clear. GOAL_SHOTS=<dir> also saves screenshots of each state, in
// the theme GOAL_THEME (light or dark) and at phone width with GOAL_PHONE=1.

const SHOTS = process.env.GOAL_SHOTS || ''
const THEME = process.env.GOAL_THEME || ''
const PHONE = process.env.GOAL_PHONE === '1'

const goal = (over = {}) => ({
  text: 'Turn the notes in Research into a two-page summary', state: 'active', turns: 3, max_turns: 20,
  steps: 41, max_steps: 400, set: '2026-10-31T10:00:00Z', updated: '2026-10-31T10:05:00Z', ...over,
})

const transcript = [
  { seq: 1, turn: 0, role: 'goal', goal_event: 'set', text: 'Turn the notes in Research into a two-page summary', at: '2026-10-31T10:00:00Z' },
  { seq: 2, turn: 1, role: 'goal', goal_event: 'continue', goal_turn: 1, text: 'Work toward this goal now', at: '2026-10-31T10:00:01Z' },
  { seq: 3, turn: 1, role: 'assistant', text: 'I read the four notes and drafted the outline.', at: '2026-10-31T10:01:00Z' },
  { seq: 4, turn: 2, role: 'goal', goal_event: 'continue', goal_turn: 2, text: 'Continue toward the goal', at: '2026-10-31T10:01:01Z' },
  { seq: 5, turn: 2, role: 'assistant', text: 'The first page is written.', at: '2026-10-31T10:03:00Z' },
]

function goalApi(page, initial = {}) {
  const state = { goal: goal(), running: false, last_turn: 'done', messages: transcript, calls: [], events: '', ...initial }
  const detail = () => ({
    id: 'c1', title: 'Research summary', model: 'qwen3-8b', kind: 'chat',
    created: '2026-10-31T10:00:00Z', updated: '2026-10-31T10:05:00Z',
    running: state.running, last_turn: state.last_turn, pending: [], ...(state.goal ? { goal: state.goal } : {}),
  })
  const line = (goal_event, text) => {
    state.messages = [...state.messages, { seq: state.messages.length + 1, turn: 3, role: 'goal', goal_event, text, at: '2026-10-31T10:06:00Z' }]
  }
  return {
    state,
    async install() {
      await page.route('**/api/chats/c1/events', route => (state.events
        ? route.fulfill({ status: 200, contentType: 'text/event-stream', body: state.events })
        : route.abort()))
      await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => {
        if (route.request().method() === 'POST') state.calls.push('send')
        return route.fulfill({ json: { messages: state.messages } })
      })
      await page.route('**/api/chats/c1/stop', route => {
        state.calls.push('stop')
        state.running = false
        if (state.goal?.state === 'active') {
          state.goal = { ...state.goal, state: 'paused', paused_by: 'user' }
          line('paused', 'user')
        }
        return route.fulfill({ json: { stopped: true } })
      })
      await page.route('**/api/chats/c1/goal/resume', route => {
        state.calls.push('resume')
        state.goal = { ...state.goal, state: 'active', paused_by: undefined, note: undefined, turns: 1, steps: 0 }
        state.running = true
        line('resumed', state.goal.text)
        return route.fulfill({ json: { goal: state.goal } })
      })
      await page.route('**/api/chats/c1/goal', route => {
        const method = route.request().method()
        state.calls.push(`goal ${method}`)
        if (method === 'PUT') {
          const was = state.goal
          state.puts = [...(state.puts || []), route.request().postDataJSON()]
          state.goal = goal({ text: route.request().postDataJSON().text, turns: 0 })
          line(was ? 'replaced' : 'set', state.goal.text)
          return route.fulfill({ json: { goal: state.goal, ...(was ? { replaced: was } : {}) } })
        }
        if (method === 'DELETE') {
          const was = state.goal
          state.goal = null
          line('cleared', was?.text || '')
          return route.fulfill({ json: { goal: null, replaced: was } })
        }
        return route.fulfill({ json: { goal: state.goal } })
      })
      await page.route('**/api/chats/c1', route => route.fulfill({ json: detail() }))
      await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
        json: { conversations: [{ ...detail(), pending: undefined }], persistent: true, files: true },
      }))
    },
  }
}

async function shot(page, name) {
  if (!SHOTS) return
  await page.screenshot({ path: `${SHOTS}/${name}-${THEME || 'light'}-${PHONE ? 'phone' : 'desktop'}.png`, fullPage: false })
}

test.describe('Chat goal', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    if (THEME) await page.addInitScript((theme) => localStorage.setItem('localai-theme', theme), THEME)
    if (PHONE) await page.setViewportSize({ width: 390, height: 844 })
  })

  test('shows the active goal above the composer, its progress and the goal lines', async ({ page }) => {
    const api = goalApi(page, { running: true })
    await api.install()
    await page.goto('/app/chats/c1')

    const bar = page.getByTestId('goal-bar')
    await expect(bar).toBeVisible()
    await expect(bar).toHaveAttribute('data-state', 'active')
    await expect(page.getByTestId('goal-text')).toHaveText('Turn the notes in Research into a two-page summary')
    await expect(page.getByTestId('goal-state')).toHaveText('Working on it')
    await expect(page.getByTestId('goal-pause')).toBeVisible()
    await expect(page.getByTestId('goal-clear')).toBeVisible()
    await expect(page.getByTestId('goal-resume')).toHaveCount(0)
    await expect(page.getByTestId('chat-goal-progress')).toHaveText('Goal · turn 3 of 20')

    const markers = page.getByTestId('goal-marker')
    await expect(markers).toHaveCount(3)
    await expect(markers.nth(0)).toContainText('Goal set')
    await expect(markers.nth(0)).toContainText('Turn the notes in Research')
    await expect(markers.nth(1)).toHaveText('Continuing toward the goal · turn 1')
    await expect(markers.nth(2)).toHaveText('Continuing toward the goal · turn 2')
    // An automatic message never shows as a message of the person.
    await expect(page.getByText('Work toward this goal now')).toHaveCount(0)
    await shot(page, 'goal-active')
  })

  test('Pause stops the turn and pauses the goal; Resume goes on', async ({ page }) => {
    const api = goalApi(page, { running: true })
    await api.install()
    await page.goto('/app/chats/c1')

    await page.getByTestId('goal-pause').click()
    await expect(page.getByTestId('goal-bar')).toHaveAttribute('data-state', 'paused')
    await expect(page.getByTestId('goal-state')).toHaveText('Paused')
    await expect(page.getByTestId('goal-detail')).toHaveText('You paused it.')
    await expect(page.getByTestId('goal-announce')).toContainText('Goal paused. You paused it.')
    await expect(page.getByTestId('chat-goal-progress')).toHaveCount(0)
    await expect(page.getByTestId('goal-marker').last()).toHaveText('Goal pausedYou paused it.')
    expect(api.state.calls).toContain('stop')
    await shot(page, 'goal-paused')

    await page.getByTestId('goal-resume').click()
    await expect(page.getByTestId('goal-bar')).toHaveAttribute('data-state', 'active')
    await expect(page.getByTestId('goal-announce')).toHaveText('Goal resumed')
    await expect(page.getByTestId('chat-goal-progress')).toHaveText('Goal · turn 1 of 20')
    expect(api.state.calls).toContain('resume')
  })

  test('Clear removes the goal and the transcript says so', async ({ page }) => {
    const api = goalApi(page)
    await api.install()
    await page.goto('/app/chats/c1')

    await page.getByTestId('goal-clear').click()
    await expect(page.getByTestId('goal-bar')).toHaveCount(0)
    await expect(page.getByTestId('goal-announce')).toHaveText('Goal cleared')
    await expect(page.getByTestId('goal-marker').last()).toContainText('Goal cleared')
    expect(api.state.calls).toContain('goal DELETE')
  })

  test('a reached goal and a blocked goal say what the model wrote', async ({ page }) => {
    const api = goalApi(page, {
      goal: goal({ state: 'done', note: 'The summary is in summary.docx, two pages.' }),
      messages: [...transcript, { seq: 6, turn: 3, role: 'goal', goal_event: 'done', text: 'The summary is in summary.docx, two pages.', at: '2026-10-31T10:07:00Z' }],
    })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('goal-bar')).toHaveAttribute('data-state', 'done')
    await expect(page.getByTestId('goal-state')).toHaveText('Reached')
    await expect(page.getByTestId('goal-detail')).toHaveText('The summary is in summary.docx, two pages.')
    await expect(page.getByTestId('goal-pause')).toHaveCount(0)
    await expect(page.getByTestId('goal-resume')).toHaveCount(0)
    await expect(page.getByTestId('goal-marker').last()).toContainText('Goal reached')
    await shot(page, 'goal-done')

    api.state.goal = goal({ state: 'blocked', note: 'the address of the shared drive' })
    await page.reload()
    await expect(page.getByTestId('goal-state')).toHaveText('Needs you')
    await expect(page.getByTestId('goal-detail')).toHaveText('Needs from you: the address of the shared drive')
    await expect(page.getByTestId('goal-resume')).toBeVisible()
    await shot(page, 'goal-blocked')
  })

  test('a goal set in another tab appears from its event', async ({ page }) => {
    const api = goalApi(page, { goal: null, messages: transcript.slice(2, 3) })
    await api.install()
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-thread')).toContainText('drafted the outline')
    await expect(page.getByTestId('goal-bar')).toHaveCount(0)

    api.state.goal = goal({ turns: 0 })
    api.state.events = `event: goal\ndata: ${JSON.stringify({ kind: 'goal', text: 'active' })}\n\n`
    await expect(page.getByTestId('goal-bar')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('chat-goal-progress')).toHaveText('Goal · starting')
  })

  test.describe('the /goal command', () => {
    const box = (page) => page.getByTestId('chat-input')
    const menu = (page) => page.locator('#home-slash')

    test('/goal shows the card with the goal, its state and its counts', async ({ page }) => {
      const api = goalApi(page)
      await api.install()
      await page.goto('/app/chats/c1')
      await box(page).fill('/goal')
      await expect(menu(page)).toContainText('/goal')
      await box(page).press('Enter')
      const card = page.getByTestId('chat-goal-card')
      await expect(card).toBeVisible()
      await expect(page.getByTestId('chat-goal-card-text')).toHaveText('Turn the notes in Research into a two-page summary')
      await expect(page.getByTestId('chat-goal-card-state')).toHaveText('Working on it')
      await expect(card).toContainText('Turn 3 of 20')
      await expect(card).toContainText('41 of 400 steps')
      await expect(card).toContainText('/goal clear removes it.')
      expect(api.state.calls).not.toContain('send')
      await card.scrollIntoViewIfNeeded()
      await shot(page, 'goal-card')
      await page.getByTestId('chat-cmdcard-close').click()
      await expect(card).toHaveCount(0)
    })

    test('/goal with no goal says how to set one', async ({ page }) => {
      const api = goalApi(page, { goal: null })
      await api.install()
      await page.goto('/app/chats/c1')
      await box(page).fill('/goal')
      await box(page).press('Enter')
      await expect(page.getByTestId('chat-goal-card')).toContainText('This chat has no goal.')
    })

    test('/goal <text> sets a new goal at once and shows the one it replaced', async ({ page }) => {
      const api = goalApi(page)
      await api.install()
      await page.goto('/app/chats/c1')
      await box(page).fill('/goal Write the release notes for 4.2')
      await box(page).press('Enter')
      await expect(page.getByTestId('goal-text')).toHaveText('Write the release notes for 4.2')
      await expect(page.getByTestId('goal-detail')).toHaveText('Replaced: Turn the notes in Research into a two-page summary')
      await expect(page.getByTestId('chat-goal-progress')).toHaveText('Goal · starting')
      await expect(page.getByTestId('goal-marker').last()).toContainText('Goal replaced')
      await expect(box(page)).toHaveValue('')
      expect(api.state.puts).toEqual([{ text: 'Write the release notes for 4.2' }])
      expect(api.state.calls).not.toContain('send')
    })

    test('/goal clear and /goal resume act on the goal from the menu', async ({ page }) => {
      const api = goalApi(page, { goal: goal({ state: 'paused', paused_by: 'turn_limit' }) })
      await api.install()
      await page.goto('/app/chats/c1')
      await box(page).fill('/goal ')
      await expect(menu(page)).toContainText('resume')
      await expect(menu(page)).toContainText('clear')
      if (SHOTS) await page.waitForTimeout(400) // the menu fades in
      await shot(page, 'goal-menu')
      await box(page).fill('/goal resume')
      await box(page).press('Enter')
      await expect(page.getByTestId('goal-bar')).toHaveAttribute('data-state', 'active')
      expect(api.state.calls).toContain('resume')

      await box(page).fill('/goal cl')
      await expect(menu(page)).toContainText('clear')
      await box(page).press('Enter')
      await expect(page.getByTestId('goal-bar')).toHaveCount(0)
      expect(api.state.calls).toContain('goal DELETE')
      expect(api.state.calls).not.toContain('send')
    })
  })
})
