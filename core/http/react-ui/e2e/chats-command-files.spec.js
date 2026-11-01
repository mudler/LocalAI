import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState } from './discovery-fixtures.js'

// A document that a command saved (the Sandbox's bash ran a script that wrote
// it), with every API call stubbed. The bash step lists the file it saved,
// like the step of a write_file; the preview pane opens on it by itself; the
// chips under the answer name it; a chip of the step opens it again.

// COMMAND_SHOTS=<dir> saves the screenshots of the review; COMMAND_THEME
// (light or dark) picks the theme for them.
const SHOTS = process.env.COMMAND_SHOTS || ''
const THEME = process.env.COMMAND_THEME || ''
if (THEME) {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript((theme) => { window.localStorage.setItem('localai-theme', theme) }, THEME)
  })
}

const AT = '2026-10-30T09:00:00Z'
const LATER = '2026-10-30T09:02:00Z'
const CHAT = { id: 'c1', title: 'Project summary', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }
const DOC = { kind: 'document', blocks: [{ type: 'heading', level: 1, text: 'Project summary' }, { type: 'paragraph', text: 'The pilot finished on time and under budget.' }, { type: 'item', text: 'Two teams joined in October' }] }

const firstTurn = () => [
  { seq: 1, turn: 1, role: 'user', text: 'Hi', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'Hello! What do you need?', at: AT },
]
// The second turn: a script written with write_file, run with the Sandbox's
// bash, which saved the document.
const docTurn = () => [
  { seq: 3, turn: 2, role: 'user', text: 'Make me a Word document from my notes', at: LATER },
  { seq: 4, turn: 2, role: 'tool', text: 'write_file: /workspace/home/scripts/build_summary.py\nWrote 812 bytes to /workspace/home/scripts/build_summary.py.', card: { kind: 'files_change', id: '41:/scripts/build_summary.py' }, at: LATER },
  {
    seq: 5, turn: 2, role: 'tool', text: 'bash: python3 scripts/build_summary.py\n{"ok":true,"output":"Saved summary.docx\\n"}', at: LATER,
    card: { kind: 'tool_provider', id: 'sandbox', files: [{ path: '/summary.docx', rev: 42, op: 'created' }, { path: '/notes.md', rev: 42, op: 'changed' }] },
  },
  { seq: 6, turn: 2, role: 'assistant', text: 'Your document is ready: summary.docx in your home folder.', at: LATER },
]
const docFiles = () => [
  { name: 'build_summary.py', path: '/scripts/build_summary.py', guest_path: '/workspace/home/scripts/build_summary.py', size: 812, mime: 'text/x-python', origin: 'tool', turn: 2, at: '2026-10-30T09:02:01Z' },
  { name: 'summary.docx', path: '/summary.docx', guest_path: '/workspace/home/summary.docx', size: 42_000, mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', origin: 'tool', turn: 2, at: '2026-10-30T09:02:05Z' },
  { name: 'notes.md', path: '/notes.md', guest_path: '/workspace/home/notes.md', size: 300, mime: 'text/markdown', origin: 'tool', changed: true, turn: 2, at: '2026-10-30T09:02:03Z' },
]

async function stubChat(page, state) {
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
  await page.route(/\/api\/chats\/c1\/files(\?.*)?$/, route => route.fulfill({ json: { files: state.chatFiles, folder: '', folder_path: '' } }))
  await page.route(/\/api\/chats\/files\/preview\?/, (route) => {
    const name = new URL(route.request().url()).searchParams.get('path').split('/').pop()
    return name === 'summary.docx'
      ? route.fulfill({ json: DOC })
      : route.fulfill({ status: 415, json: { error: { message: 'no preview', type: 'unsupported_preview' } } })
  })
  await page.route(/\/api\/chats\/files\/content\?/, route => route.fulfill({ status: 200, contentType: 'text/plain', body: '# Notes\n\n- pilot done\n' }))
  const queue = []
  let wake = null
  await page.route('**/api/chats/c1/events', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body }).catch(() => {})
  })
  return (turn) => {
    queue.push(`event: turn_start\ndata: ${JSON.stringify({ kind: 'turn_start', turn })}\n\n`)
    queue.push(`event: files\ndata: ${JSON.stringify({ kind: 'files' })}\n\n`)
    queue.push(`event: turn_end\ndata: ${JSON.stringify({ kind: 'turn_end', turn })}\n\n`)
    wake?.()
  }
}

const pane = (page) => page.getByTestId('chat-files-pane')

test('a document a command saved is listed under its step, opens in the pane and gets a chip', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  await mockDiscovery(page, state)
  const endTurn = await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('Hello! What do you need?')).toBeVisible()
  await expect(pane(page)).toHaveCount(0)

  state.messages = [...state.messages, ...docTurn()]
  state.chatFiles = docFiles()
  endTurn(2)

  // The pane opens by itself on the newest file the turn made: the document.
  await expect(pane(page)).toBeVisible({ timeout: 15_000 })
  const preview = pane(page).getByTestId('file-preview')
  await expect(preview.getByRole('heading', { name: 'summary.docx' })).toBeVisible()
  await expect(preview.getByText('The pilot finished on time and under budget.')).toBeVisible()
  await expect(page.getByTestId('chat-preview-status')).toHaveText('Preview opened: summary.docx')
  // The pane lists it with the turn's other files.
  await expect(pane(page).getByTestId('chat-files-created')).toContainText('summary.docx')

  // The chips under the answer name it.
  await expect(page.getByTestId('chat-file-chip').filter({ hasText: 'summary.docx' })).toHaveText(/Created\s*summary\.docx\s*·\s*Open preview/)

  // The bash step lists what it saved.
  await page.getByTestId('chat-activity').last().locator('.cx-fold__head').click()
  const saved = page.getByTestId('chat-step-files')
  await expect(saved).toHaveCount(1)
  await expect(saved).toContainText('Saved to your files:')
  const chips = saved.getByTestId('chat-step-saved-file')
  await expect(chips).toHaveCount(2)
  await expect(chips.first()).toHaveText('summary.docx')
  await expect(chips.nth(1)).toHaveAttribute('data-op', 'changed')
  await expect(page.getByTestId('chat-step-provider')).toBeVisible()
  if (SHOTS) {
    await page.waitForTimeout(500)
    await page.screenshot({ path: `${SHOTS}/command-files-${THEME || 'light'}.png` })
  }

  // A chip of the step opens its file in the pane.
  await chips.nth(1).click()
  await expect(preview.getByRole('heading', { name: 'notes.md' })).toBeVisible()
  await chips.first().click()
  await expect(preview.getByRole('heading', { name: 'summary.docx' })).toBeVisible()
})
