import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat, storedChats } from './chat-fixtures.js'

// The composer has two reasoning controls, stored per chat: an effort select
// and a "No reasoning" toggle. Both are also slash actions.

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

  test('the toggle asks for "none" and hides reasoning the model still sends', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello'))])
    const toggle = page.getByTestId('chat-reasoning-off')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('chat-effort-chip')).toBeDisabled()
    const body = await sendWith(page, stream({ reasoning: 'secret plan' }, { content: 'The answer.' }))
    expect(body.reasoning_effort).toBe('none')
    await expect(page.getByText('The answer.')).toBeVisible()
    await expect(page.getByText('secret plan')).toHaveCount(0)
    const history = (await storedChats(page)).chats[0].history
    expect(history.some(m => m.role === 'thinking')).toBe(false)
  })

  test('the slash actions toggle reasoning and open the effort select', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'R', 'qwen3-8b', pair('hi', 'hello'))])
    await page.getByTestId('chat-input').fill('/reasoning')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-reasoning-off')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('chat-input').fill('/reasoning')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-reasoning-off')).toHaveAttribute('aria-pressed', 'false')
    await page.getByTestId('chat-input').fill('/effort')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByTestId('chat-effort-menu')).toBeVisible()
    await expect(page.getByTestId('chat-effort-default')).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-effort-menu')).toHaveCount(0)
    await expect(page.getByTestId('chat-effort-chip')).toBeFocused()
  })
})
