import { test, expect } from './coverage-fixtures.js'

// The chats page against the real test server: the browser, the chats API, the
// conversation service, the agent loop and the mock inference backend. Nothing
// here is stubbed, so a pass means the whole path works.
//
// The mock backend answers every prompt with a fixed reply, and one containing
// MOCK_SLOW_STREAM waits half a second first, which leaves a window to type
// while the turn runs.

const composer = (page) => page.getByRole('textbox', { name: 'Message' })
const log = (page) => page.getByRole('log', { name: 'Conversation' })

// The test server's mock model is not tagged as chat-capable, so the model list
// of the picker is empty. That one list is stubbed; the chat itself is not.
async function offerMockModel(page) {
  await page.route('**/api/models/capabilities', route =>
    route.fulfill({ json: { data: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }] } }))
}

async function startChat(page, text) {
  await offerMockModel(page)
  await page.goto('/app/chats')
  await expect(page.getByTestId('chats-page')).toBeVisible()
  await composer(page).fill(text)
  await page.getByRole('button', { name: 'Send' }).click()
}

test.describe('Chats against the real server', () => {
  test('starts a chat, shows the answer and keeps the chat in the list', async ({ page }) => {
    await startChat(page, 'hello there')

    await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
    await expect(log(page).locator('.ch-msg--user')).toHaveText('hello there')
    // The model's answer arrives as a second, non-user message.
    await expect(log(page).locator('.ch-msg:not(.ch-msg--user)').first()).not.toBeEmpty({ timeout: 20_000 })
    await expect(page.getByText('Working…')).toBeHidden({ timeout: 20_000 })
    await expect(page.getByRole('complementary', { name: 'Your chats' }).getByRole('button', { name: /hello there|New chat/ }).first()).toBeVisible()
  })

  test('a message typed while the model works is not lost', async ({ page }) => {
    await startChat(page, 'MOCK_SLOW_STREAM first')
    await expect(page.getByText('Working…')).toBeVisible()

    await composer(page).fill('second')
    await composer(page).press('Enter')
    await composer(page).fill('third')
    await composer(page).press('Enter')

    // Both reach the model, together and in order: as one waiting batch, or, if
    // the turn ended first, as the message of the next turn.
    await expect(log(page).locator('.ch-msg--user').filter({ hasText: 'second' })).toBeVisible({ timeout: 30_000 })
    await expect(log(page).locator('.ch-msg--user').filter({ hasText: 'third' })).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText('Working…')).toBeHidden({ timeout: 30_000 })
    await expect(page.getByTestId('pending-list')).toBeHidden()
  })

  test('another browser tab sees the same chat', async ({ browser, page }) => {
    await startChat(page, 'shared chat')
    await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
    const url = page.url()
    await expect(log(page).locator('.ch-msg--user')).toHaveText('shared chat')

    const second = await browser.newPage()
    await offerMockModel(second)
    await second.goto(url)
    await expect(log(second).locator('.ch-msg--user')).toHaveText('shared chat')
    await second.close()
  })

  test('a file attached to a message lands in the workspace, where the files page finds it', async ({ page }) => {
    const name = `report-${Date.now()}.txt`
    await offerMockModel(page)
    await page.goto('/app/chats')
    test.skip(await page.getByRole('note').filter({ hasText: 'files are off' }).isVisible(), 'files are off on this server build')
    await page.getByTestId('chat-file-input').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from('orbital mechanics notes') })
    await composer(page).fill('summarize the attachment')
    await page.getByRole('button', { name: 'Send' }).click()
    await expect(log(page).locator('.ch-msg--user').first()).toContainText('summarize the attachment', { timeout: 20_000 })
    await expect(page.getByText('Working…')).toBeHidden({ timeout: 30_000 })

    await page.goto('/app/files')
    await page.getByRole('searchbox', { name: 'Search your files' }).fill('orbital mechanics')
    await page.getByRole('searchbox', { name: 'Search your files' }).press('Enter')
    await expect(page.getByTestId('ws-hit').filter({ hasText: name }).first()).toBeVisible()
  })
})
