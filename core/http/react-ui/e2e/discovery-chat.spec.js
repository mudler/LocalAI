import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState, TASK_PAGE } from './discovery-fixtures.js'

// What proves a change worked: the note under the answer that changed a
// well-known file or a page, with Undo, and a toast only when the change shows
// outside the transcript (the composer, the sidebar, Home). Every API call is
// stubbed; the transcript carries the cards and checkpoint lines the server
// writes (files_change "<rev>:<path>", checkpoint rev and paths).

const SHOTS = process.env.DISCOVERY_SHOTS || ''
const shot = async (page, name) => { if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png` }) }

const AT = '2026-10-30T09:00:00Z'
const CHAT = { id: 'c1', title: 'Getting started', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }
const user = (seq, turn, text) => ({ seq, turn, role: 'user', text, at: AT })
const answer = (seq, turn, text) => ({ seq, turn, role: 'assistant', text, at: AT })
const write = (seq, turn, id) => ({ seq, turn, role: 'tool', text: `write_file: ${id.split(':')[1]}\nWrote 40 bytes.`, card: { kind: 'files_change', id }, at: AT })

// stubChat answers the chat c1. state.messages can change during a test;
// endTurn() delivers turn_start and turn_end on the chat's stream, as the
// server does when a turn ends, and the page reads the transcript again.
async function stubChat(page, state) {
  state.undos = []
  state.chatFiles = state.chatFiles || []
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
  await page.route(/\/api\/chats\/c1\/files(\?.*)?$/, route => route.fulfill({ json: { files: state.chatFiles, folder: '', folder_path: '' } }))
  await page.route('**/api/chats/files/undo', (route) => {
    state.undos.push(route.request().postDataJSON().rev)
    return route.fulfill({ json: { rev: 99 } })
  })
  await page.route(/\/api\/chats\/wellknown\/[a-z]+\/versions/, (route) => {
    const name = /wellknown\/([a-z]+)\/versions/.exec(route.request().url())[1]
    return route.fulfill({ json: { versions: (state.versionsOf || {})[name] || [] } })
  })
  const queue = []
  let wake = null
  await page.route('**/api/chats/c1/events', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body }).catch(() => {})
  })
  return (turn) => {
    queue.push(`event: turn_start\ndata: ${JSON.stringify({ kind: 'turn_start', turn })}\n\n`)
    queue.push(`event: turn_end\ndata: ${JSON.stringify({ kind: 'turn_end', turn })}\n\n`)
    wake?.()
  }
}

const note = (page) => page.getByTestId('workspace-change')

test('a turn that saved the persona and the profile shows one note with Undo and Open in Account, and no toast', async ({ page }) => {
  const state = newState({
    messages: [user(1, 1, 'Call me Marta and keep answers short'), write(2, 1, '10:/persona.json'), write(3, 1, '11:/user.json'), answer(4, 1, 'Done, Marta. Short answers from now on.')],
  })
  await mockDiscovery(page, state)
  await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(note(page)).toContainText('Saved your name in user.json and your instructions in persona.json', { timeout: 15_000 })
  await expect(note(page).getByRole('button', { name: 'Open in Account' })).toBeVisible()
  // A reload is not a change made now: nothing pops up.
  await expect(page.locator('[data-testid^="change-toast"]')).toHaveCount(0)
  await shot(page, 'B1-chat-settings-saved')

  await note(page).getByTestId('workspace-change-undo').click()
  await expect(note(page).getByTestId('workspace-change-undone')).toHaveText('Undone.')
  expect(state.undos).toEqual([11, 10])
})

test('a shortcut added in this turn: the note in the transcript and a toast above the composer, both with Undo', async ({ page }) => {
  const state = newState({ messages: [user(1, 1, 'hello'), answer(2, 1, 'Hi.')] })
  state.versionsOf = { shortcuts: [{ rev: 20, valid: true, kind: 'agent', data: { shortcuts: [{ label: 'Summarise', prompt: 's' }, { label: 'Plan my day', prompt: 'p' }] } }, { rev: 5, valid: true, kind: 'user', data: { shortcuts: [{ label: 'Summarise', prompt: 's' }] } }] }
  await mockDiscovery(page, state)
  const endTurn = await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByText('Hi.')).toBeVisible({ timeout: 15_000 })

  state.messages = [...state.messages, user(3, 2, 'Make that a shortcut'), write(4, 2, '20:/shortcuts.json'), answer(5, 2, 'Added a shortcut called Plan my day.')]
  endTurn(2)
  await expect(note(page)).toContainText('Saved shortcuts.json.')
  const toast = page.getByTestId('change-toast-shortcut')
  await expect(toast).toContainText('Added to your composer: Plan my day')
  const composer = await page.locator('.cx-dock__in').boundingBox()
  const box = await toast.boundingBox()
  expect(box.y + box.height).toBeLessThanOrEqual(composer.y + 1)
  await note(page).scrollIntoViewIfNeeded()
  await shot(page, 'B2-chat-shortcut-added')
  await toast.getByRole('button', { name: 'Undo' }).click()
  await expect(note(page).getByTestId('workspace-change-undone')).toHaveText('Undone.')
  expect(state.undos).toEqual([20])
})

test('the first page: the note says where it is, and a toast opens it', async ({ page }) => {
  const state = newState({ messages: [user(1, 1, 'hello'), answer(2, 1, 'Hi.')] })
  await mockDiscovery(page, state)
  const endTurn = await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByText('Hi.')).toBeVisible({ timeout: 15_000 })

  state.pages = [TASK_PAGE]
  state.messages = [...state.messages, user(3, 2, 'Make me a task board'), answer(4, 2, 'I made a task board.'),
    { seq: 5, turn: 2, role: 'checkpoint', rev: 30, text: '/pages/tasks/index.html\n/pages/tasks/tasks.json', at: AT }]
  endTurn(2)
  await expect(note(page)).toContainText('Created the page Task board in pages/tasks/.')
  const toast = page.getByTestId('change-toast-page')
  await expect(toast).toContainText('Task board is in Pages, in the sidebar')
  await note(page).scrollIntoViewIfNeeded()
  await shot(page, 'B3-chat-first-page-created')
  await toast.getByRole('button', { name: 'Open' }).click()
  await expect(page).toHaveURL(/\/app\/pages\/tasks$/)
})

test('suggestions switched off by the assistant: the note says so and links to Account; a change of the hint record alone has no note', async ({ page }) => {
  const state = newState({
    messages: [user(1, 1, "Don't suggest things like that"), write(2, 1, '40:/user.json'), answer(3, 1, 'Understood. I will not offer to save things again.'),
      user(4, 2, 'no thanks'), write(5, 2, '41:/user.json'), answer(6, 2, 'Fine.')],
  })
  state.versionsOf = { user: [
    { rev: 41, valid: true, kind: 'agent', data: { seen: { offers_off: true, offers: { profile: { state: 'declined' } } } } },
    { rev: 40, valid: true, kind: 'agent', data: { seen: { offers_off: true } } },
    { rev: 3, valid: true, kind: 'user', data: { seen: {} } },
  ] }
  await mockDiscovery(page, state)
  await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(note(page)).toHaveCount(1, { timeout: 15_000 })
  await expect(note(page)).toContainText('Suggestions from the assistant are off.')
  await expect(note(page).getByRole('button', { name: 'Turn them back on in Account' })).toBeVisible()
  await shot(page, 'B4-chat-offers-off')
})

test('the Files pane tags the settings files and says what they do', async ({ page }) => {
  const state = newState({
    messages: [user(1, 1, 'Call me Marta'), write(2, 1, '10:/user.json'), answer(3, 1, 'Done.')],
    chatFiles: [
      { path: '/user.json', name: 'user.json', size: 200, origin: 'agent', changed: true, turn: 1 },
      { path: '/notes.md', name: 'notes.md', size: 20, origin: 'agent', turn: 1 },
    ],
  })
  await mockDiscovery(page, state)
  await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-files-chip').click({ timeout: 15_000 })
  const pane = page.getByTestId('chat-files-pane')
  await expect(pane.getByTestId('chat-file-wellknown')).toHaveText('Your profile')
  await expect(pane.getByTestId('chat-file-wellknown')).toHaveCount(1)
  await expect(pane.getByTestId('chat-files-wellknown-note')).toContainText('These files change how LocalAI works for you.')
  await shot(page, 'B5-chat-files-pane')
})

for (const theme of ['dark', 'light']) {
  test(`on a phone the note wraps under the answer (${theme})`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private window */ } }, theme)
    const state = newState({ messages: [user(1, 1, 'Make me a task board'), answer(2, 1, 'Done.'), { seq: 3, turn: 1, role: 'checkpoint', rev: 30, text: '/pages/tasks/index.html', at: AT }], pages: [TASK_PAGE] })
    await mockDiscovery(page, state)
    await stubChat(page, state)
    await page.goto('/app/chats/c1')
    await expect(note(page)).toContainText('Created the page Task board', { timeout: 15_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    expect(overflow).toBe(false)
    await shot(page, `B6-mobile-chat-note-${theme}`)
  })
}
