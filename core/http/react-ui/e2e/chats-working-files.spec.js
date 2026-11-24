import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState } from './discovery-fixtures.js'

// A turn that made a Word document with a script, with every API call
// stubbed. The server marks the script a working file (kind working): the pane
// lists the document as what the chat made and folds the script into
// "Working files · 1", the preview opens on the document, the chips under the
// answer name the document only, and the step says "Also saved 1 working
// file". Opening the group shows the script.

// WORKING_SHOTS=<dir> saves the screenshots of the review; WORKING_THEME
// (light or dark) picks the theme for them.
const SHOTS = process.env.WORKING_SHOTS || ''
const THEME = process.env.WORKING_THEME || ''
if (THEME) {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript((theme) => { window.localStorage.setItem('localai-theme', theme) }, THEME)
  })
}

const AT = '2026-10-31T09:00:00Z'
const LATER = '2026-10-31T09:02:00Z'
const FOLDER = '/Chats/2026-10-31 Project summary'
const CHAT = { id: 'c1', title: 'Project summary', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }
const DOC = { kind: 'document', blocks: [{ type: 'heading', level: 1, text: 'Project summary' }, { type: 'paragraph', text: 'The pilot finished on time and under budget.' }] }

const firstTurn = () => [
  { seq: 1, turn: 1, role: 'user', text: 'Hi', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'Hello! What do you need?', at: AT },
]
// The script goes to the chat's work folder with write_file, then bash runs it
// and saves the document and an intermediate file.
const docTurn = () => [
  { seq: 3, turn: 2, role: 'user', text: 'Make me a Word document from my notes', at: LATER },
  {
    seq: 4, turn: 2, role: 'tool', at: LATER,
    text: `write_file: /workspace/home${FOLDER}/work/build_document.py\nWrote 812 bytes to /workspace/home${FOLDER}/work/build_document.py.`,
    card: { kind: 'files_change', id: `41:${FOLDER}/work/build_document.py`, file_kind: 'working' },
  },
  {
    seq: 5, turn: 2, role: 'tool', at: LATER,
    text: `bash: python3 "${FOLDER.slice(1)}/work/build_document.py"\n{"ok":true,"output":"Saved summary.docx\\n"}`,
    card: {
      kind: 'tool_provider', id: 'sandbox', files: [
        { path: '/summary.docx', rev: 42, op: 'created', kind: 'deliverable' },
        { path: `${FOLDER}/work/rows.json`, rev: 42, op: 'created', kind: 'working' },
      ],
    },
  },
  { seq: 6, turn: 2, role: 'assistant', text: 'Your document is ready: summary.docx in your home folder.', at: LATER },
]
const file = (name, path, size, mime, kind, at) => ({ name, path, guest_path: `/workspace/home${path}`, size, mime, origin: 'tool', turn: 2, kind, at })
const docFiles = () => [
  file('build_document.py', `${FOLDER}/work/build_document.py`, 812, 'text/x-python', 'working', '2026-10-31T09:02:01Z'),
  file('summary.docx', '/summary.docx', 42_000, 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'deliverable', '2026-10-31T09:02:05Z'),
  // The newest file of the turn is a working file: it must not open the pane.
  file('rows.json', `${FOLDER}/work/rows.json`, 300, 'application/json', 'working', '2026-10-31T09:02:09Z'),
]

async function stubChat(page, state) {
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: state.messages } }))
  await page.route(/\/api\/chats\/c1\/files(\?.*)?$/, route => route.fulfill({
    json: { files: state.chatFiles, folder: state.chatFiles.length ? `/workspace/home${FOLDER}/` : '', folder_path: state.chatFiles.length ? FOLDER : '' },
  }))
  await page.route(/\/api\/chats\/files\/preview\?/, (route) => {
    const name = new URL(route.request().url()).searchParams.get('path').split('/').pop()
    return name === 'summary.docx'
      ? route.fulfill({ json: DOC })
      : route.fulfill({ status: 415, json: { error: { message: 'no preview', type: 'unsupported_preview' } } })
  })
  await page.route(/\/api\/chats\/files\/content\?/, route => route.fulfill({ status: 200, contentType: 'text/plain', body: 'from docx import Document\n\ndoc = Document()\ndoc.save("summary.docx")\n' }))
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
const shot = async (page, name) => {
  if (!SHOTS) return
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${SHOTS}/${name}-${THEME || 'light'}.png` })
}

async function runTurn(page, width, height) {
  await page.setViewportSize({ width, height })
  const state = newState({ chats: [CHAT], messages: firstTurn(), chatFiles: [] })
  await mockDiscovery(page, state)
  const endTurn = await stubChat(page, state)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('Hello! What do you need?')).toBeVisible()
  state.messages = [...state.messages, ...docTurn()]
  state.chatFiles = docFiles()
  endTurn(2)
  await expect(page.getByText('Your document is ready')).toBeVisible({ timeout: 15_000 })
}

test('the working files of a turn fold away under what it made, and the document opens', async ({ page }) => {
  await runTurn(page, 1440, 900)

  // The pane opens by itself on the document, not on the newer working file.
  await expect(pane(page)).toBeVisible({ timeout: 15_000 })
  const preview = pane(page).getByTestId('file-preview')
  await expect(preview.getByRole('heading', { name: 'summary.docx' })).toBeVisible()
  await expect(preview.getByText('The pilot finished on time and under budget.')).toBeVisible()

  // The document is what the chat made; the working files are one folded line.
  const created = pane(page).getByTestId('chat-files-created')
  await expect(created.getByTestId('chat-file-row')).toHaveCount(1)
  await expect(created).toContainText('summary.docx')
  const working = pane(page).getByTestId('chat-files-working')
  const toggle = working.getByTestId('chat-files-working-toggle')
  await expect(toggle).toHaveText(/Working files\s*·\s*2/)
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(working.getByTestId('chat-file-row')).toHaveCount(0)

  // The chips under the answer name the document only.
  const chips = page.getByTestId('chat-file-chip')
  await expect(chips).toHaveCount(1)
  await expect(chips.first()).toContainText('summary.docx')
  await shot(page, 'working-collapsed')

  // Opening the group shows the script and the data, and a row previews.
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const rows = working.getByTestId('chat-file-row')
  await expect(rows).toHaveCount(2)
  await expect(rows.first()).toHaveAttribute('data-kind', 'working')
  await expect(working).toContainText('build_document.py')
  await working.getByRole('button', { name: 'build_document.py' }).click()
  await expect(preview.getByRole('heading', { name: 'build_document.py' })).toBeVisible()
  await expect(preview).toContainText('from docx import Document')
  await shot(page, 'working-expanded')

  // The steps: the script of write_file is a quiet chip, and the bash step
  // lists the document and folds its working file into one line.
  await page.getByTestId('chat-activity').last().locator('.cx-fold__head').click()
  await expect(page.getByTestId('chat-step-file').first()).toHaveAttribute('data-kind', 'working')
  const saved = page.getByTestId('chat-step-files')
  await expect(saved.getByTestId('chat-step-saved-file')).toHaveCount(1)
  await expect(saved.getByTestId('chat-step-saved-file')).toHaveText('summary.docx')
  const also = saved.getByTestId('chat-step-working')
  await expect(also).toHaveText('Also saved 1 working file')
  await also.click()
  await expect(saved.getByTestId('chat-step-saved-file')).toHaveCount(2)
  await expect(saved.getByTestId('chat-step-saved-file').nth(1)).toHaveAttribute('data-kind', 'working')
  await shot(page, 'working-steps')
})

test('on a phone the working files fold away in the sheet', async ({ page }) => {
  await runTurn(page, 390, 844)
  // A phone never opens the pane by itself: open it from the chip.
  await page.getByTestId('chat-file-chip').first().click()
  await expect(pane(page)).toBeVisible()
  await expect(pane(page).getByTestId('chat-files-working-toggle')).toHaveText(/Working files\s*·\s*2/)
  await pane(page).locator('.cx-files__body').evaluate((el) => { el.scrollTop = el.scrollHeight })
  await shot(page, 'working-phone-collapsed')
  await pane(page).getByTestId('chat-files-working-toggle').click()
  await pane(page).locator('.cx-files__body').evaluate((el) => { el.scrollTop = el.scrollHeight })
  await shot(page, 'working-phone-expanded')
})
