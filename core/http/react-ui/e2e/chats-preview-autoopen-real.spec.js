import { test, expect } from './coverage-fixtures.js'
import { offerMockModel, sendFromHome, composer, b64 } from './journey-fixtures.js'

// The file pane opens by itself on a file the assistant writes, against the
// real test server and the mock model. The first read of a chat that Home just
// started lands after a quick first turn has ended, as it does on a slow
// connection or a busy server: the watermark of "what is new" must not be
// taken from that late read.

test.describe.configure({ timeout: 120_000 })

const write = (path, content) => `ADDON_TOOL_CALL:write_file:${b64({ path, content })}`

// Holds back the reads that the page makes of a chat (its detail, its transcript
// and its files) until the server says no turn runs, so the page
// reads a chat whose first turn is over. Events stream unchanged.
async function readsAfterTheTurn(page) {
  await page.route(/\/api\/chats\/([0-9a-f]+)(\/(messages|files))?(\?.*)?$/, async (route) => {
    const id = /\/api\/chats\/([0-9a-f]+)/.exec(route.request().url())[1]
    await expect.poll(async () => {
      const d = await page.request.get(`/api/chats/${id}`).then(r => r.json())
      return d.running
    }, { timeout: 90_000, intervals: [200] }).toBe(false)
    // The transcript first, the files after it: the page then has a chat whose
    // turn is over when it takes its watermark.
    if (route.request().url().includes('/files')) await page.waitForTimeout(1000)
    await route.continue()
  })
}

test('a file written in the first turn of a new chat opens the pane although the first read is late', async ({ page }) => {
  const stamp = Date.now().toString(36)
  await offerMockModel(page)
  await readsAfterTheTurn(page)
  await sendFromHome(page, `write a report ${write(`/autoopen-${stamp}/report.md`, '# Report\n\nWritten by the model.')}`)
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  const pane = page.getByTestId('chat-files-pane')
  // The chip under the answer shows the file once the page reads the chat.
  await expect(page.getByTestId('chat-files-count')).toHaveText('1', { timeout: 60_000 })
  await page.waitForTimeout(1500)
  await page.screenshot({ path: process.env.AUTOOPEN_SHOT || 'test-results/chat-autoopen-late-read.png' })
  await expect(pane).toBeVisible({ timeout: 5_000 })
  await expect(pane.getByTestId('file-preview')).toContainText('Written by the model.')
})

test('a file written in a later turn opens the pane although the reads are late', async ({ page }) => {
  const stamp = Date.now().toString(36)
  await offerMockModel(page)
  await sendFromHome(page, 'hello')
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  await expect(page.getByTestId('chat-streaming')).toHaveCount(0, { timeout: 30_000 })
  await expect(page.getByTestId('chat-files-pane')).toHaveCount(0)
  await readsAfterTheTurn(page)
  await composer(page).fill(`write ${write(`/autoopen-${stamp}/second.md`, '# Second\n\nSecond turn.')}`)
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByTestId('chat-files-pane')).toBeVisible({ timeout: 60_000 })
})
