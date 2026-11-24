import { test, expect } from './coverage-fixtures.js'
import { offerMockModel, openNewChat, composer, b64 } from './journey-fixtures.js'

// A command starts in the chat's work folder, so a chart that a script saves
// with a bare file name lands there. The pane must open on it all the same:
// the person asked for the chart. A script or a log in that folder never
// opens it. Real test server, real events, the mock model; the write is made
// with write_file at the path a command's output would have.

test.describe.configure({ timeout: 120_000 })

const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180" viewBox="0 0 320 180"><rect width="320" height="180" fill="#f4f1ea"/><rect x="30" y="90" width="40" height="70" fill="#3b6fb6"/><rect x="100" y="50" width="40" height="110" fill="#3b6fb6"/><rect x="170" y="20" width="40" height="140" fill="#3b6fb6"/></svg>'
const write = (path, content) => `ADDON_TOOL_CALL:write_file:${b64({ path, content })}`

// A first message with a small attachment: the attachment makes the chat
// folder, and its path names it.
async function firstTurn(page) {
  await offerMockModel(page)
  await openNewChat(page)
  await page.getByTestId('chat-file-input').setInputFiles({ name: 'brief.txt', mimeType: 'text/plain', buffer: Buffer.from('sales by quarter') })
  await composer(page).fill('hello')
  await page.getByRole('button', { name: 'Send' }).click()
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const id = page.url().split('/').pop()
  await expect(page.getByTestId('chat-streaming')).toHaveCount(0, { timeout: 30_000 })
  const brief = await expect.poll(async () => (await page.request.get(`/api/chats/${id}/files`).then(r => r.json())).files.find(f => f.name === 'brief.txt')?.path, { timeout: 15_000 }).toMatch(/^\/Chats\//)
    .then(async () => (await page.request.get(`/api/chats/${id}/files`).then(r => r.json())).files.find(f => f.name === 'brief.txt').path)
  await expect(page.getByTestId('chat-files-pane')).toHaveCount(0)
  return { id, folder: brief.replace(/brief\.txt$/, '') }
}

async function sendWrite(page, path, content) {
  await composer(page).fill(`save it ${write(path, content)}`)
  await page.getByRole('button', { name: 'Send' }).click()
}

test('a chart saved in the work folder opens the pane on the chart', async ({ page }) => {
  const { id, folder } = await firstTurn(page)
  await sendWrite(page, `${folder}work/chart.svg`, SVG)
  const pane = page.getByTestId('chat-files-pane')
  await expect(pane).toBeVisible({ timeout: 60_000 })
  const listed = await page.request.get(`/api/chats/${id}/files`).then(r => r.json())
  expect(listed.files.find(f => f.name === 'chart.svg')?.kind).toBe('deliverable')
  await expect(pane.getByTestId('file-preview')).toBeVisible()
  await expect(pane.getByRole('button', { name: 'chart.svg' })).toHaveAttribute('aria-expanded', 'true')
  await page.waitForTimeout(800)
  await page.screenshot({ path: process.env.SANDBOX_PANE_SHOT || 'test-results/work-folder-chart.png' })
})

test('a script saved in the work folder leaves the pane closed', async ({ page }) => {
  const { id, folder } = await firstTurn(page)
  await sendWrite(page, `${folder}work/make_chart.py`, 'print("chart")\n')
  await expect.poll(async () => (await page.request.get(`/api/chats/${id}/files`).then(r => r.json())).files.some(f => f.name === 'make_chart.py'), { timeout: 60_000 }).toBe(true)
  await expect(page.getByTestId('chat-streaming')).toHaveCount(0, { timeout: 30_000 })
  await page.waitForTimeout(1500)
  await expect(page.getByTestId('chat-files-pane')).toHaveCount(0)
})
