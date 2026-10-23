import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat, storedChats, answerWith } from './chat-fixtures.js'

// Chat settings are a sheet: the system prompt, sampling, the context size,
// the behaviour switches and, for admins, the model's facts. They apply from
// the next message.

const sheet = (page) => page.getByTestId('chat-settings')

async function open(page) {
  await page.getByTestId('chat-settings-button').click()
  await expect(sheet(page)).toBeVisible()
}

test.describe('Chat settings sheet', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await page.route('**/api/models/config-json/qwen3-8b', route => route.fulfill({ json: {
      name: 'qwen3-8b', backend: 'llama-cpp', context_size: 8192, threads: 8, gpu_layers: 99,
      parameters: { model: 'qwen3-8b-q4_k_m.gguf' }, template: { chat_message: 'x' },
    } }))
  })

  test('opens as a kit sheet, says when it applies, and closes on Esc, the veil and the button', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'))])
    await open(page)
    await expect(sheet(page)).toHaveClass(/dk-sheet/)
    await expect(sheet(page)).toContainText('Applies from the next message.')
    await expect(sheet(page).getByRole('button', { name: 'Close' })).toBeFocused()

    await page.keyboard.press('Escape')
    await expect(sheet(page)).toHaveCount(0)
    // Focus returns to the button that opened it.
    await expect(page.getByTestId('chat-settings-button')).toBeFocused()

    await open(page)
    await page.locator('.cx-sheet-veil').click({ position: { x: 20, y: 300 } })
    await expect(sheet(page)).toHaveCount(0)

    await open(page)
    await sheet(page).getByRole('button', { name: 'Close' }).click()
    await expect(sheet(page)).toHaveCount(0)
  })

  test('each sampling value says "model default" until changed, and Reset puts it back', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'))])
    await open(page)
    const temp = sheet(page).locator('.cx-field', { hasText: 'Temperature' })
    await expect(temp.locator('.cx-field__value')).toHaveText('model default')
    await expect(temp.getByRole('button', { name: 'Reset' })).toHaveCount(0)

    await temp.getByRole('slider').fill('0.4')
    await expect(temp.locator('.cx-field__value')).toHaveText('0.4')
    await expect.poll(async () => (await storedChats(page)).chats[0].temperature).toBe(0.4)

    await temp.getByRole('button', { name: 'Reset' }).click()
    await expect(temp.locator('.cx-field__value')).toHaveText('model default')
    await expect.poll(async () => (await storedChats(page)).chats[0].temperature).toBeNull()

    // Top P and Top K are there too and change their own fields.
    await sheet(page).locator('.cx-field', { hasText: 'Top P' }).getByRole('slider').fill('0.5')
    await sheet(page).locator('.cx-field', { hasText: 'Top K' }).getByRole('slider').fill('20')
    await expect.poll(async () => {
      const c = (await storedChats(page)).chats[0]
      return [c.topP, c.topK]
    }).toEqual([0.5, 20])
  })

  test('the system prompt is stored and goes out before the next message', async ({ page }) => {
    const answer = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', [])])
    await open(page)
    await sheet(page).getByTestId('chat-system-prompt').fill('Answer in one line.')
    await expect.poll(async () => (await storedChats(page)).chats[0].systemPrompt).toBe('Answer in one line.')
    await page.keyboard.press('Escape')

    await page.getByTestId('chat-input').fill('hello')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-role="assistant"]')).toContainText('ok')
    expect(answer.request().messages[0]).toEqual({ role: 'system', content: 'Answer in one line.' })
  })

  test('the context size has quick sizes, drives the header meter and is not sent', async ({ page }) => {
    const answer = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'), { tokenUsage: { prompt: 1000, completion: 96, total: 1096 } })])
    // The admin's model configuration fills the size in: 8192.
    await expect.poll(async () => (await storedChats(page)).chats[0].contextSize).toBe(8192)
    await open(page)
    const size = sheet(page).getByRole('group', { name: 'Context Size' })
    await expect(size.getByRole('button', { name: '8k' })).toHaveAttribute('aria-pressed', 'true')
    await expect(sheet(page)).toContainText('Using 1096 of 8192 tokens (13%)')
    await size.getByRole('button', { name: '4k' }).click()
    await expect(sheet(page)).toContainText('(27%)')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-context')).toContainText('27%')

    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-role="assistant"]').last()).toContainText('ok')
    expect(JSON.stringify(answer.request())).not.toContain('4096')
  })

  test('Manage mode turns on from the sheet, marks the header and is sent with the message', async ({ page }) => {
    const answer = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', [])])
    await open(page)
    const manage = sheet(page).getByRole('switch', { name: 'Manage mode' })
    await expect(manage).toHaveAttribute('aria-checked', 'false')
    await manage.click()
    await expect(manage).toHaveAttribute('aria-checked', 'true')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-manage-badge')).toBeVisible()
    await page.getByTestId('chat-input').fill('what is installed')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-role="assistant"]')).toContainText('ok')
    expect(answer.request().metadata).toMatchObject({ localai_assistant: 'true' })
  })

  test('Focus mode is a switch that is remembered', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'))])
    await open(page)
    const focus = sheet(page).getByRole('switch', { name: 'Focus mode' })
    await expect(focus).toHaveAttribute('aria-checked', 'true')
    await focus.click()
    await expect(focus).toHaveAttribute('aria-checked', 'false')
    expect(await page.evaluate(() => localStorage.getItem('localai_chat_focus_mode'))).toBe('false')
  })

  test('admins see the model facts and can open its config', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'))])
    await open(page)
    const info = page.getByTestId('chat-model-info')
    await expect(info).toContainText('llama-cpp')
    await expect(info).toContainText('qwen3-8b-q4_k_m.gguf')
    await expect(info).toContainText('99')
    await info.getByRole('button', { name: 'Edit config' }).click()
    await expect(page).toHaveURL(/\/app\/model-editor\/qwen3-8b/)
  })

  test('Clear conversation asks for a confirmation, then empties the chat but keeps its settings', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'), { temperature: 0.4 })])
    await open(page)
    await sheet(page).getByRole('button', { name: 'Clear chat history' }).click()
    await expect(sheet(page)).toHaveCount(0)
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Clear this conversation')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('chat-message')).toHaveCount(2)

    await open(page)
    await sheet(page).getByRole('button', { name: 'Clear chat history' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Clear conversation' }).click()
    await expect(page.getByTestId('chat-empty')).toBeVisible()
    await expect.poll(async () => {
      const c = (await storedChats(page)).chats[0]
      return [c.history.length, c.temperature]
    }).toEqual([0, 0.4])
  })

  test('on a phone the sheet rises from the bottom', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openChat(page, [chatOf('c1', 'Settings', 'qwen3-8b', pair('hi', 'hello'))])
    await page.getByTestId('chat-settings-button').click()
    const box = await sheet(page).boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeGreaterThanOrEqual(843)
    expect(box.y).toBeGreaterThan(20)
    await expect(sheet(page).locator('.dk-sheet-grip')).toBeVisible()
  })
})
