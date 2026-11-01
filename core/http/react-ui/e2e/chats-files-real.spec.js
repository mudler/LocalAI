import { test, expect } from './coverage-fixtures.js'
import { startChat, composer, offerMockModel, openNewChat, b64 } from './journey-fixtures.js'

// The files pane of a chat, driven from the chat page against the real test
// server and the mock model. After each step the spec reads the API, so what
// the pane shows is what the server keeps and what the model is told.

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const api = (request, path) => request.get(path).then(r => r.json())
const settled = async (page, id) => {
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
  await expect(page.getByTestId('chat-streaming')).toHaveCount(0, { timeout: 15_000 })
}
const files = async (page, id) => (await api(page.request, `/api/chats/${id}/files`)).files
const byName = (list, name) => list.find(f => f.name === name)

test('the files pane lists what was attached and what the model wrote, live, and follows a rename', async ({ page }) => {
  const stamp = Date.now().toString(36)
  const note = `notes-${stamp}.txt`
  await offerMockModel(page)
  // A file goes with the first message from the empty chat (/new on Home).
  await openNewChat(page)
  await page.getByTestId('chat-file-input').setInputFiles({ name: note, mimeType: 'text/plain', buffer: Buffer.from('my notes for the files pane') })
  await composer(page).fill('read my notes')
  await page.getByRole('button', { name: 'Send' }).click()
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const id = page.url().split('/').pop()
  await settled(page, id)

  // The API lists the attachment as the user's, in the chat folder.
  const first = await files(page, id)
  const attached = byName(first, note)
  expect(attached).toBeTruthy()
  expect(attached.origin).toBe('user')
  expect(attached.path).toMatch(/^\/Chats\//)
  expect(attached.guest_path).toBe(`/workspace/home${attached.path}`)

  // The chip counts the files and opens the pane from the keyboard.
  const chip = page.getByTestId('chat-files-chip')
  await expect(page.getByTestId('chat-files-count')).toHaveText(String(first.length))
  await chip.focus()
  await page.keyboard.press('Enter')
  const pane = page.getByTestId('chat-files-pane')
  await expect(pane).toBeVisible()
  await expect(chip).toHaveAttribute('aria-pressed', 'true')
  await expect(pane.getByTestId('chat-files-uploaded')).toContainText(note)
  await page.screenshot({ path: 'test-results/chat-files-uploaded.png' })

  // The model writes a file with a tool: the pane shows it without a reload.
  const report = `/files-pane-${stamp}/report.md`
  await composer(page).fill(`write a report ADDON_TOOL_CALL:write_file:${b64({ path: report, content: '# Report\n\nWritten by the model.' })}`)
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(pane.getByTestId('chat-files-created')).toContainText('report.md', { timeout: 30_000 })
  await settled(page, id)
  const second = await files(page, id)
  expect(byName(second, 'report.md')).toMatchObject({ origin: 'tool', path: report, guest_path: `/workspace/home${report}`, mime: 'text/markdown' })
  await expect(page.getByTestId('chat-files-count')).toHaveText(String(second.length))
  await page.screenshot({ path: 'test-results/chat-files-created.png' })

  // The file the model just wrote opens by itself in the preview on top of
  // the pane, with its text, a download and no focus move. The preview is part
  // of the pane; the Canvas is gone.
  const preview = pane.getByTestId('file-preview')
  await expect(preview).toContainText('Written by the model.')
  await expect(preview.getByRole('heading', { name: 'report.md' })).toBeVisible()
  await expect(preview.getByTestId('chat-preview-download')).toHaveAttribute('href', /report\.md/)
  await expect(pane.getByRole('button', { name: 'report.md' })).toHaveAttribute('aria-expanded', 'true')
  await page.screenshot({ path: 'test-results/chat-files-preview.png' })

  // A file of the list takes the preview over; its row is the one expanded.
  await pane.getByRole('button', { name: note }).click()
  await expect(preview.getByRole('heading', { name: note })).toBeVisible()
  await expect(preview).toContainText('my notes for the files pane')
  await expect(preview.getByTestId('chat-preview-download')).toHaveAttribute('href', new RegExp(note.replace('.', '\\.')))
  await expect(pane.getByRole('button', { name: 'report.md' })).toHaveAttribute('aria-expanded', 'false')

  // The same row again closes the preview and leaves the list.
  await pane.getByRole('button', { name: note }).click()
  await expect(preview).toHaveCount(0)
  await pane.getByRole('button', { name: 'report.md' }).click()
  await expect(preview).toContainText('Written by the model.')

  // A rename moves the chat folder: the pane and the API give the new path.
  const r = await page.request.patch(`/api/chats/${id}`, { data: { title: `Renamed ${stamp}` } })
  expect(r.ok()).toBe(true)
  await expect(pane.locator('.cx-files__folder')).toContainText(`Renamed ${stamp}`, { timeout: 20_000 })
  const third = await api(page.request, `/api/chats/${id}/files`)
  expect(byName(third.files, note).path).toContain(`Renamed ${stamp}/${note}`)
  expect(third.stale).toHaveLength(1)
  const download = await page.request.get(`/api/chats/files/content?path=${encodeURIComponent(byName(third.files, note).path)}`)
  expect(await download.text()).toBe('my notes for the files pane')
  await page.screenshot({ path: 'test-results/chat-files-renamed.png' })

  // Esc closes the pane.
  await page.locator('body').click({ position: { x: 5, y: 5 } })
  await page.keyboard.press('Escape')
  await expect(pane).toHaveCount(0)
  await expect(chip).toHaveAttribute('aria-pressed', 'false')
})

test('a chat with no files teaches where files come from', async ({ page }) => {
  const id = await startChat(page, 'just talk')
  await settled(page, id)
  expect(await files(page, id)).toEqual([])
  await page.getByTestId('chat-files-chip').click()
  const empty = page.getByTestId('chat-files-empty')
  await expect(empty).toBeVisible()
  await expect(empty).toContainText('No files in this chat yet')
  await expect(page.getByTestId('chat-files-count')).toHaveCount(0)
  await page.screenshot({ path: 'test-results/chat-files-empty.png' })

  // A file dropped on the pane is attached to the next message.
  const drop = await page.evaluateHandle(() => {
    const dt = new DataTransfer()
    dt.items.add(new File(['dropped text'], 'dropped.txt', { type: 'text/plain' }))
    return dt
  })
  await page.getByTestId('chat-files-pane').dispatchEvent('drop', { dataTransfer: drop })
  await expect(page.getByTestId('attachment')).toContainText('dropped.txt')
  await composer(page).fill('here is a file')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByTestId('chat-files-uploaded')).toContainText('dropped.txt', { timeout: 30_000 })
  await settled(page, id)
  expect(byName(await files(page, id), 'dropped.txt')).toMatchObject({ origin: 'user', turn: 2 })
  await page.screenshot({ path: 'test-results/chat-files-dropped.png' })
})
