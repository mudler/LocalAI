import { test, expect } from './coverage-fixtures.js'
import {
  chatOf, pair, mockChat, openChat, storedChats, answerWith, PNG,
} from './chat-fixtures.js'

// The Chat composer is the Home command bar: the model chip, the MCP chip, a
// Canvas chip, attach buttons, a solid Send and the slash menu. These specs
// check that it is the same object and that what it adds for a conversation
// (Stop, Up to edit, the token line) works.

const input = (page) => page.getByTestId('chat-input')

test.describe('Chat composer', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('is the Home command bar, with the Canvas chip and the hint line', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Bar', 'qwen3-8b', pair('hi', 'hello'))])
    const bar = page.getByTestId('chat-composer')
    await expect(bar).toHaveClass(/home-cmd/)
    await expect(bar.getByTestId('home-model-chip')).toContainText('qwen3-8b')
    await expect(bar.locator('.chat-mcp-dropdown')).toBeVisible()
    await expect(bar.getByTestId('chat-canvas-chip')).toHaveAttribute('aria-pressed', 'false')
    await expect(bar.getByRole('button', { name: 'Attach image' })).toBeVisible()
    await expect(bar.getByRole('button', { name: 'Attach audio' })).toBeVisible()
    await expect(bar.getByRole('button', { name: 'Attach file' })).toBeVisible()
    await expect(bar).toContainText('Enter to send, Shift+Enter for a new line')
    await expect(bar).toContainText('for actions')
  })

  test('Send is quiet and disabled when empty, active with text', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Send', 'qwen3-8b', [])])
    const send = page.getByTestId('chat-send')
    await expect(send).toBeDisabled()
    await expect(send).toHaveAttribute('data-empty', 'true')
    await input(page).fill('hello')
    await expect(send).toBeEnabled()
    await expect(send).not.toHaveAttribute('data-empty', 'true')
  })

  test('Enter sends, Shift+Enter adds a line, and Ctrl, Cmd and Alt Enter do not send', async ({ page }) => {
    const answer = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Keys', 'qwen3-8b', [])])
    await input(page).fill('one')
    await input(page).press('Shift+Enter')
    await expect(input(page)).toHaveValue('one\n')
    for (const combo of ['Control+Enter', 'Meta+Enter', 'Alt+Enter']) await input(page).press(combo)
    await expect(page.locator('[data-role="user"]')).toHaveCount(0)
    expect(answer.request()).toBeNull()

    await input(page).press('Enter')
    await expect(page.locator('[data-role="user"]')).toContainText('one')
    expect(answer.request().messages.at(-1).content).toBe('one')
  })

  test('the settings the chat holds go out with the message', async ({ page }) => {
    const answer = await answerWith(page, 'ok')
    await openChat(page, [chatOf('c1', 'Params', 'qwen3-8b', [], { temperature: 0.3, topP: 0.8, topK: 20, systemPrompt: 'Be brief.' })])
    await input(page).fill('go')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-role="assistant"]')).toContainText('ok')
    const body = answer.request()
    expect(body).toMatchObject({ model: 'qwen3-8b', temperature: 0.3, top_p: 0.8, top_k: 20, stream: true })
    expect(body.messages[0]).toEqual({ role: 'system', content: 'Be brief.' })
  })

  test('the slash menu lists the chat actions in two groups and filters as you type', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await input(page).fill('/')
    const list = page.getByRole('listbox', { name: 'Actions' })
    await expect(list).toBeVisible()
    await expect(list.locator('.dk-cmd-label')).toHaveText(['Chat', 'View'])
    const names = await list.locator('.dk-cmd-name').allTextContents()
    expect(names).toEqual(['/model', '/new', '/chats', '/assistant', '/canvas', '/find', '/settings', '/export', '/clear'])

    await input(page).fill('/set')
    await expect(list.locator('.dk-cmd-name')).toHaveText(['/settings'])
    await input(page).fill('/zzz')
    await expect(list).toHaveCount(0)

    // A message that merely starts with a slash is a message.
    await input(page).fill('/etc/hosts how do I read it')
    await expect(list).toHaveCount(0)
  })

  test('arrow keys, Enter, Tab and Esc work in the slash menu', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await input(page).fill('/')
    await input(page).press('ArrowDown')
    await expect(page.getByRole('option', { selected: true })).toContainText('/new')
    await input(page).press('ArrowUp')
    await expect(page.getByRole('option', { selected: true })).toContainText('/model')
    await input(page).press('Escape')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toHaveCount(0)
    // Escape closes the menu and leaves the text.
    await expect(input(page)).toHaveValue('/')
  })

  test('/new starts an empty chat, /canvas toggles Canvas, /settings opens the sheet', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await input(page).fill('/canvas')
    await input(page).press('Enter')
    await expect(page.getByTestId('chat-canvas-chip')).toHaveAttribute('aria-pressed', 'true')
    await expect(input(page)).toHaveValue('')

    await input(page).fill('/settings')
    await input(page).press('Enter')
    await expect(page.getByTestId('chat-settings')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-settings')).toHaveCount(0)

    await input(page).fill('/new')
    await input(page).press('Enter')
    await expect(page.getByTestId('chat-empty')).toBeVisible()
    await expect.poll(async () => (await storedChats(page)).chats.length).toBe(2)
  })

  test('/chats opens the conversations, /find opens the search, /clear asks first', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await input(page).fill('/chats')
    await input(page).press('Enter')
    await expect(page.getByTestId('chats-menu')).toBeVisible()
    await page.keyboard.press('Escape')

    await input(page).fill('/find')
    await input(page).press('Enter')
    await expect(page.getByTestId('chat-find')).toBeVisible()
    await page.getByTestId('chat-find-input').press('Escape')

    await input(page).fill('/clear')
    await input(page).press('Enter')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Clear this conversation')
    await dialog.getByRole('button', { name: 'Clear conversation' }).click()
    await expect(page.getByTestId('chat-empty')).toBeVisible()
    await expect.poll(async () => (await storedChats(page)).chats[0].history.length).toBe(0)
  })

  test('/export downloads the chat as Markdown', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash export', 'qwen3-8b', pair('hi', 'hello'))])
    const download = page.waitForEvent('download')
    await input(page).fill('/export')
    await input(page).press('Enter')
    expect((await download).suggestedFilename()).toBe('Slash_export.md')
  })

  test('the actions that need a thread are hidden in an empty chat', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Empty', 'qwen3-8b', [])])
    await input(page).fill('/')
    const names = await page.getByRole('listbox', { name: 'Actions' }).locator('.dk-cmd-name').allTextContents()
    expect(names).toEqual(['/model', '/new', '/chats', '/assistant', '/canvas', '/settings'])
  })

  test('"/" from outside a text field starts a command', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Slash', 'qwen3-8b', pair('hi', 'hello'))])
    await page.locator('.cx-body').click({ position: { x: 5, y: 5 } })
    await page.keyboard.press('/')
    await expect(input(page)).toBeFocused()
    await expect(input(page)).toHaveValue('/')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
  })

  test('an attached image shows as a thumbnail tag and can be removed', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Attach', 'gemma-3-12b', [])])
    await page.locator('input[type=file][accept="image/*"]').setInputFiles({
      name: 'pixel.png', mimeType: 'image/png', buffer: Buffer.from(PNG.split(',')[1], 'base64'),
    })
    const tag = page.locator('.home-file-tag', { hasText: 'pixel.png' })
    await expect(tag).toBeVisible()
    await expect(tag.locator('img')).toBeVisible()
    await expect(page.getByTestId('chat-send')).toBeEnabled()
    await tag.getByRole('button', { name: /Remove pixel.png/ }).click()
    await expect(tag).toHaveCount(0)
  })

  test('a pasted image attaches as a file', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Paste', 'gemma-3-12b', [])])
    await input(page).focus()
    await input(page).evaluate((el, b64) => {
      const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0))
      const dt = new DataTransfer()
      dt.items.add(new File([bytes], 'image.png', { type: 'image/png' }))
      el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
    }, PNG.split(',')[1])
    await expect(page.locator('.home-file-tag', { hasText: 'pasted-image-1.png' })).toBeVisible()
  })

  test('the line under the box shows the token count against the context size', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Tokens', 'qwen3-8b', pair('hi', 'hello'), {
      contextSize: 8192, tokenUsage: { prompt: 2600, completion: 540, total: 3140 },
    })])
    await expect(page.getByTestId('chat-foot')).toContainText('3140 / 8192 tokens')
    await expect(page.getByTestId('chat-context')).toContainText('38%')
  })

  test('the foot warns when the context is nearly full', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Full', 'qwen3-8b', pair('hi', 'hello'), {
      contextSize: 8192, tokenUsage: { prompt: 7000, completion: 400, total: 7400 },
    })])
    await expect(page.getByTestId('chat-foot')).toContainText('The context is nearly full')
    await expect(page.getByTestId('chat-context')).toHaveAttribute('data-warn', 'true')
  })

  test('Up in an empty box edits your last message', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Up', 'qwen3-8b', [...pair('first question', 'first answer'), ...pair('second question', 'second answer')])])
    await input(page).focus()
    await input(page).press('ArrowUp')
    const user = page.locator('[data-role="user"]').last()
    await expect(user.getByRole('textbox')).toHaveValue('second question')
    await user.getByRole('textbox').press('Escape')

    // With text in the box the key does what it always did.
    await input(page).fill('draft')
    await input(page).press('ArrowUp')
    await expect(page.getByRole('textbox', { name: 'Edit message' })).toHaveCount(0)
  })

  test('the model chip changes the model of this chat', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Model', 'qwen3-8b', [])])
    await page.getByTestId('home-model-chip').click()
    await page.getByRole('option', { name: /phi-4-mini/ }).click()
    await expect(page.getByTestId('home-model-chip')).toContainText('phi-4-mini')
    await expect.poll(async () => (await storedChats(page)).chats[0].model).toBe('phi-4-mini')
  })
})
