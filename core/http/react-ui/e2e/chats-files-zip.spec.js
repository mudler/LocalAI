import { readFileSync } from 'node:fs'
import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState } from './discovery-fixtures.js'

// The Download all button of the files pane: one click puts every file of the
// list into one zip. The files are served by stubbed routes; the zip is read
// back from the download and checked entry by entry.

const AT = '2026-10-31T09:00:00Z'
const FOLDER = '/Chats/2026-10-31 Project summary'
const CHAT = { id: 'c1', title: 'Project summary', model: 'qwen3-8b-instruct', kind: 'chat', created: AT, updated: AT, running: false }
const MESSAGES = [
  { seq: 1, turn: 1, role: 'user', text: 'Hi', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'Here are your files.', at: AT },
]
const file = (name, path, size, origin = 'tool') => ({ name, path, guest_path: `/workspace/home${path}`, size, mime: 'text/plain', origin, turn: 1, at: AT })
const BODIES = {
  [`${FOLDER}/notes.txt`]: 'first file',
  [`${FOLDER}/data/rows.txt`]: 'second file, in a folder',
}

async function openPane(page, files, content) {
  await page.setViewportSize({ width: 1440, height: 900 })
  const state = newState({ chats: [CHAT], messages: MESSAGES, chatFiles: files })
  await mockDiscovery(page, state)
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [CHAT], persistent: true, files: true } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: { ...CHAT, pending: [] } }))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: MESSAGES } }))
  await page.route(/\/api\/chats\/c1\/files(\?.*)?$/, route => route.fulfill({ json: { files, folder: `/workspace/home${FOLDER}/`, folder_path: FOLDER } }))
  await page.route(/\/api\/chats\/files\/preview\?/, route => route.fulfill({ status: 415, json: { error: { message: 'no preview', type: 'unsupported_preview' } } }))
  await page.route(/\/api\/chats\/files\/content\?/, content)
  await page.route('**/api/chats/c1/events', route => route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: '' }).catch(() => {}))
  await page.goto('/app/chats/c1')
  await expect(page.getByText('Here are your files.')).toBeVisible({ timeout: 15_000 })
  const pane = page.getByTestId('chat-files-pane')
  if (!(await pane.isVisible())) await page.getByTestId('chat-files-toggle').click()
  await expect(pane).toBeVisible()
  return pane
}

// Reads the entries of a stored zip: [{ name, text }].
function readZip(path) {
  const buf = readFileSync(path)
  const end = buf.length - 22
  expect(buf.readUInt32LE(end)).toBe(0x06054b50)
  const n = buf.readUInt16LE(end + 10)
  let p = buf.readUInt32LE(end + 16)
  const out = []
  for (let i = 0; i < n; i++) {
    const size = buf.readUInt32LE(p + 24)
    const len = buf.readUInt16LE(p + 28)
    const local = buf.readUInt32LE(p + 42)
    const name = buf.toString('utf8', p + 46, p + 46 + len)
    const start = local + 30 + len
    out.push({ name, text: buf.toString('utf8', start, start + size) })
    p += 46 + len
  }
  return out
}

test('Download all puts every file of the list into one zip, with relative paths', async ({ page }) => {
  const files = [file('notes.txt', `${FOLDER}/notes.txt`, 10), file('rows.txt', `${FOLDER}/data/rows.txt`, 24)]
  const pane = await openPane(page, files, (route) => {
    const path = new URL(route.request().url()).searchParams.get('path')
    return route.fulfill({ status: 200, contentType: 'text/plain', body: BODIES[path] })
  })
  const button = pane.getByTestId('chat-files-download-all')
  await expect(button).toBeEnabled()
  await expect(button).toHaveAttribute('aria-label', 'Download all files as ZIP')
  await expect(button).toHaveAttribute('title', 'Download all files as ZIP')
  const [download] = await Promise.all([page.waitForEvent('download'), button.click()])
  expect(download.suggestedFilename()).toBe('chat-files.zip')
  const entries = readZip(await download.path())
  expect(entries).toEqual([
    { name: 'notes.txt', text: 'first file' },
    { name: 'data/rows.txt', text: 'second file, in a folder' },
  ])
})

test('a file that cannot be read stops the zip and shows an error', async ({ page }) => {
  const files = [file('notes.txt', `${FOLDER}/notes.txt`, 10)]
  const pane = await openPane(page, files, route => route.fulfill({ status: 500, json: { error: { message: 'boom' } } }))
  let downloaded = false
  page.on('download', () => { downloaded = true })
  await pane.getByTestId('chat-files-download-all').click()
  await expect(page.getByText('The files could not be downloaded as a ZIP.')).toBeVisible()
  await expect(pane.getByTestId('chat-files-download-all')).toBeEnabled()
  expect(downloaded).toBe(false)
})
