import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat, storedChats } from './chat-fixtures.js'

// The composer has one reasoning control, stored per chat: the effort select.
// "None" asks for no reasoning and hides what the model still sends. It is
// also the /effort slash action.

const sendWith = async (page, answer) => {
  let body = null
  await page.route('**/v1/chat/completions', (route) => {
    body = route.request().postDataJSON()
    route.fulfill({ status: 200, contentType: 'text/event-stream', body: answer })
  })
  await page.getByTestId('chat-input').fill('hello')
  await page.getByTestId('chat-send').click()
  await expect.poll(() => body).not.toBeNull()
  return body
}

const stream = (...deltas) => deltas.map(d => `data: ${JSON.stringify({ choices: [{ delta: d }] })}\n\n`).join('')
  + `data: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`

test.describe('Chat reasoning controls', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('sends no reasoning_effort until a level is picked, then sends it', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello'))])
    const chip = page.getByTestId('chat-effort-chip')
    await expect(chip).toContainText('Model default')
    let body = await sendWith(page, stream({ content: 'ok' }))
    expect(body.reasoning_effort).toBeUndefined()

    await chip.click()
    await page.getByTestId('chat-effort-high').click()
    await expect(chip).toContainText('High')
    await expect.poll(async () => (await storedChats(page)).chats[0].reasoningEffort).toBe('high')
    await page.unroute('**/v1/chat/completions')
    body = await sendWith(page, stream({ content: 'ok' }))
    expect(body.reasoning_effort).toBe('high')
  })

  test('"None" asks for "none" and hides reasoning the model still sends', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello'))])
    const chip = page.getByTestId('chat-effort-chip')
    await chip.click()
    await page.getByTestId('chat-effort-none').click()
    await expect(chip).toContainText('None')
    await expect(chip).toBeEnabled()
    const body = await sendWith(page, stream({ reasoning: 'secret plan' }, { content: 'The answer.' }))
    expect(body.reasoning_effort).toBe('none')
    await expect(page.getByText('The answer.')).toBeVisible()
    await expect(page.getByText('secret plan')).toHaveCount(0)
    const history = (await storedChats(page)).chats[0].history
    expect(history.some(m => m.role === 'thinking')).toBe(false)
  })

  test('a chat saved with the old reasoningOff flag still hides reasoning', async ({ page }) => {
    const chat = { ...chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello')), reasoningOff: true }
    await openChat(page, [chat])
    await expect(page.getByTestId('chat-effort-chip')).toContainText('None')
    const body = await sendWith(page, stream({ reasoning: 'secret plan' }, { content: 'The answer.' }))
    expect(body.reasoning_effort).toBe('none')
    await expect(page.getByText('secret plan')).toHaveCount(0)
  })

  test('the /effort slash action opens the effort select', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello'))])
    await page.getByTestId('chat-input').fill('/effort')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-effort-menu')).toBeVisible()
    await expect(page.getByTestId('chat-effort-default')).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-effort-menu')).toHaveCount(0)
    await expect(page.getByTestId('chat-effort-chip')).toBeFocused()
  })
})
