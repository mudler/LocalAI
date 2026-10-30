import { test, expect } from './coverage-fixtures.js'
import { composer, transcript, startChat, watchProblems, b64, offerMockModel } from './journey-fixtures.js'

// Chat journeys against the real test server: the browser, the chats API, the
// conversation service, the agent loop and the mock inference backend. Nothing
// of LocalAI is stubbed. The mock model streams a word every 200 ms for a
// prompt with MOCK_LONG_STREAM, which leaves a window to stop, type or reload.
// The assertions use roles and visible text, not class names.

const working = (page) => page.getByText('Working…')

test.describe('Chat journeys', () => {
  test('a first message streams its answer live and the journey is quiet', async ({ page }) => {
    const watch = watchProblems(page)
    await startChat(page, 'hello journey')
    await expect(transcript(page).getByText('hello journey')).toBeVisible()
    await expect(transcript(page)).toContainText('mocked', { timeout: 20_000 })
    await expect(working(page)).toBeHidden({ timeout: 20_000 })
    expect(watch.problems()).toEqual([])
  })

  test('Stop ends a running turn, keeps the text so far and a queued message waits for Send now', async ({ page }) => {
    await startChat(page, 'MOCK_LONG_STREAM go')
    await expect(working(page)).toBeVisible()
    await expect(transcript(page)).toContainText('tick', { timeout: 15_000 })

    await composer(page).fill('queued while running')
    await composer(page).press('Enter')
    await expect(page.getByTestId('pending-row')).toHaveCount(1)

    await page.getByRole('button', { name: 'Stop' }).click()
    await expect(working(page)).toBeHidden({ timeout: 15_000 })
    // A stop is final until the user decides: nothing starts by itself.
    await expect(page.getByTestId('pending-row')).toHaveCount(1)
    await expect(transcript(page)).toContainText('tick')
    await expect(page.getByRole('button', { name: /Send now/i })).toBeVisible()
  })

  test('a waiting message can be edited, then Send now delivers the edited text as the next turn', async ({ page }) => {
    await startChat(page, 'MOCK_LONG_STREAM go')
    await expect(working(page)).toBeVisible()
    await composer(page).fill('first draft')
    await composer(page).press('Enter')
    const row = page.getByTestId('pending-row')
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: /Edit/i }).click()
    const edit = page.getByRole('textbox', { name: /Edit/i })
    await edit.fill('edited text')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('pending-row')).toContainText('edited text')

    await page.getByRole('button', { name: 'Stop' }).click()
    await expect(working(page)).toBeHidden({ timeout: 15_000 })
    await page.getByRole('button', { name: /Send now/i }).click()
    await expect(transcript(page).getByText('edited text')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByTestId('pending-row')).toHaveCount(0)
  })

  test('a reload in the middle of a turn shows the same turn still running, then its end', async ({ page }) => {
    await startChat(page, 'MOCK_LONG_STREAM MOCK_LONG_STREAM_SHORT reload me')
    await expect(working(page)).toBeVisible()
    await page.reload()
    await expect(transcript(page).getByText('reload me')).toBeVisible()
    await expect(working(page)).toBeVisible()
    await expect(working(page)).toBeHidden({ timeout: 25_000 })
    await expect(transcript(page)).toContainText('tick')
    // The chat takes a new message after the resumed turn.
    await composer(page).fill('and again')
    await page.getByTestId('chat-send').click()
    await expect(transcript(page).getByText('and again')).toBeVisible()
  })

  test('a tool the model calls shows as a line in the transcript and its result reaches the answer', async ({ page }) => {
    const args = b64({ path: '/' })
    await startChat(page, `list my files ADDON_TOOL_CALL:list_files:${args}`)
    await expect(transcript(page).getByText(/list_files/)).toBeVisible({ timeout: 20_000 })
    await expect(working(page)).toBeHidden({ timeout: 20_000 })
  })

  test('a file the model writes lands in Files, in the history, and Undo removes it', async ({ page }) => {
    const name = `journey-${Date.now()}.txt`
    await startChat(page, `write it ADDON_TOOL_CALL:write_file:${b64({ path: `/${name}`, content: 'written by the model' })}`)
    await expect(transcript(page).getByText(/write_file/).first()).toBeVisible({ timeout: 20_000 })
    test.skip(await page.getByRole('note').filter({ hasText: 'files are off' }).isVisible(), 'files are off on this server build')
    // The user's own message names the tool too, so wait for the file itself.
    await expect.poll(async () => (await page.request.get('/api/chats/files?path=/')).text(), { timeout: 20_000 }).toContain(name)

    await page.goto('/app/files')
    await expect(page.getByTestId('ws-row').filter({ hasText: name })).toBeVisible()
    await page.getByRole('tab', { name: 'History' }).click()
    const entry = page.getByTestId('ws-history-row').filter({ hasText: name })
    await expect(entry).toBeVisible()
    await entry.getByRole('button', { name: 'Undo' }).click()
    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(page.getByTestId('ws-row').filter({ hasText: name })).toHaveCount(0)
  })

  test('deleting a chat leaves it, offers Undo and removes it for good when the offer ends', async ({ page }) => {
    const id = await startChat(page, 'delete me soon')
    await expect(working(page)).toBeHidden({ timeout: 20_000 })
    const deleteFromMenu = async () => {
      await page.getByRole('button', { name: 'More' }).click()
      await page.getByRole('menuitem', { name: 'Delete chat' }).click()
    }
    await deleteFromMenu()
    await expect(page).toHaveURL(/\/app\/chats$/)
    // Undo brings the chat back and nothing was deleted on the server.
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page).toHaveURL(new RegExp(`${id}$`))
    expect((await page.request.get(`/api/chats/${id}`)).status()).toBe(200)

    await deleteFromMenu()
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(async () => (await page.request.get(`/api/chats/${id}`)).status()).toBe(404)
  })

  test('a chat gets a title from its first message', async ({ page }) => {
    const id = await startChat(page, 'plan my trip to Lisbon')
    await expect.poll(async () => (await (await page.request.get(`/api/chats/${id}`)).json()).title, { timeout: 20_000 }).toContain('Lisbon')
    await page.keyboard.press('Control+k')
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Lisbon' }).first()).toBeVisible()
  })

  test('the old chat addresses open Home, where a chat starts, and keep the model', async ({ page }) => {
    await offerMockModel(page)
    await page.goto('/app/chat')
    await expect(page).toHaveURL(/\/app$/)
    await page.goto('/app/chat/mock-model')
    await expect(page).toHaveURL(/\/app\?model=mock-model$/)
    await expect(page.getByTestId('home-model-chip')).toContainText('mock-model')
    await page.goto('/app/chats?model=mock-model')
    await expect(page).toHaveURL(/\/app\?model=mock-model$/)
    await expect(page.getByTestId('home-model-chip')).toContainText('mock-model')
  })
})
