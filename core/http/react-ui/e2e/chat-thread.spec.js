import { test, expect } from './coverage-fixtures.js'
import {
  chatOf, pair, mockChat, openChat, storedChats, controlStream, answerWith, sse, PNG, GO_BLOCK,
} from './chat-fixtures.js'

// The thread's parts: the one-line activity fold, code blocks, attachments,
// per-message actions and their keys, a failed reply and a streaming reply.

const msg = (page, role) => page.locator(`[data-testid="chat-message"][data-role="${role}"]`)

const ACTIVITY = [
  { role: 'user', content: 'Which file has the leak?' },
  { role: 'thinking', content: 'The handler starts one reader per request.' },
  { role: 'tool_call', content: JSON.stringify({ type: 'tool_call', name: 'read_file', arguments: { path: 'stream_handler.go' } }) },
  { role: 'tool_result', content: JSON.stringify({ type: 'tool_result', name: 'read_file', result: '148 lines' }) },
  { role: 'assistant', content: 'It is stream_handler.go.' },
]

test.describe('Chat thread', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('reasoning and tool calls fold into one quiet line that opens inline', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Activity', 'qwen3-8b', ACTIVITY)])
    const fold = page.getByTestId('chat-activity')
    const head = fold.getByRole('button')
    await expect(head).toHaveText('Thought · read_file')
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    // Closed: the steps are not in the page at all.
    await expect(page.locator('.cx-steps')).toHaveCount(0)

    await head.click()
    await expect(head).toHaveAttribute('aria-expanded', 'true')
    const steps = page.locator('.cx-steps .cx-step')
    await expect(steps).toHaveCount(3)
    await expect(steps.nth(0)).toContainText('The handler starts one reader per request.')
    await expect(steps.nth(1)).toContainText('Tool call')
    await expect(steps.nth(1)).toContainText('stream_handler.go')
    await expect(steps.nth(2)).toContainText('148 lines')

    await head.click()
    await expect(page.locator('.cx-steps')).toHaveCount(0)
  })

  test('the activity line sits inside the answer it belongs to', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Activity', 'qwen3-8b', ACTIVITY)])
    // One assistant turn, and the fold is part of it, between the name and the text.
    await expect(msg(page, 'assistant')).toHaveCount(1)
    await expect(msg(page, 'assistant').getByTestId('chat-activity')).toBeVisible()
    await expect(msg(page, 'assistant').locator('.cx-prose')).toContainText('It is stream_handler.go.')
  })

  test('a code block has Copy and Canvas buttons', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await openChat(page, [chatOf('c1', 'Code', 'qwen3-8b', pair('Show me', 'Here:\n\n```go\n' + GO_BLOCK + '\n```\n\nDone.'))])
    const block = page.locator('.code-block')
    await expect(block.locator('.code-block__lang')).toHaveText('go')
    const copy = block.getByRole('button', { name: 'Copy code' })
    await expect(copy).toContainText('Copy')
    await expect(block.getByRole('button', { name: 'Canvas' })).toBeVisible()
    await copy.click()
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toContain('context.WithCancel')
  })

  test('an image is a thumbnail that opens in the viewer, and a file is a chip', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Attachments', 'gemma-3-12b', [
      {
        role: 'user',
        content: [{ type: 'text', text: 'What is in this?' }, { type: 'image_url', image_url: { url: PNG } }],
        files: [{ name: 'shot.png', type: 'image' }, { name: 'notes.txt', type: 'file', content: 'x' }],
      },
      { role: 'assistant', content: 'A single pixel.' },
    ])])
    const user = msg(page, 'user')
    await expect(user.locator('.cx-thumb img')).toBeVisible()
    // The image is shown once: as a thumbnail, not also as a chip.
    await expect(user.locator('.cx-chip')).toHaveCount(1)
    await expect(user.locator('.cx-chip')).toContainText('notes.txt')

    await user.locator('.cx-thumb').click()
    await expect(page.locator('.lightbox')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('.lightbox')).toHaveCount(0)
  })

  test('message actions show on hover, on focus and on the last turn', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Actions', 'qwen3-8b', [...pair('one', 'first'), ...pair('two', 'second')])])
    const opacity = (loc) => loc.getByTestId('message-actions').evaluate(el => getComputedStyle(el).opacity)
    const first = msg(page, 'user').first()
    // Away from the pointer the first turn's actions are hidden; the last turn keeps them.
    await page.mouse.move(5, 5)
    await expect.poll(() => opacity(first)).toBe('0')
    await expect.poll(() => opacity(msg(page, 'assistant').last())).toBe('1')
    await first.hover()
    await expect.poll(() => opacity(first)).toBe('1')
    await page.mouse.move(5, 5)
    await first.focus()
    await expect.poll(() => opacity(first)).toBe('1')
  })

  test('the buttons are Copy, Edit, Regenerate and Branch on an answer, and only Copy and Edit on yours', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Actions', 'qwen3-8b', pair('one', 'first'))])
    await expect(msg(page, 'user').getByTestId('message-actions').locator('button')).toHaveCount(2)
    await expect(msg(page, 'assistant').getByTestId('message-actions').getByTitle('Regenerate')).toBeVisible()
    await expect(msg(page, 'assistant').getByTestId('message-actions').getByTitle('Branch from here')).toBeVisible()
  })

  test('arrow keys move between messages and C, E and B act on the focused one', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await openChat(page, [chatOf('c1', 'Keys', 'qwen3-8b', [...pair('first question', 'first answer'), ...pair('second question', 'second answer')])])
    const all = page.getByTestId('chat-message')
    await all.first().focus()
    await page.keyboard.press('ArrowDown')
    await expect(all.nth(1)).toBeFocused()
    await page.keyboard.press('ArrowUp')
    await expect(all.first()).toBeFocused()

    // C copies the focused turn.
    await page.keyboard.press('c')
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('first question')

    // E opens the editor on your own turn; Esc leaves it as it was.
    await page.keyboard.press('e')
    await expect(all.first().getByRole('textbox')).toHaveValue('first question')
    await page.keyboard.press('Escape')
    await expect(all.first().getByRole('textbox')).toHaveCount(0)

    // B branches from an answer into a new chat.
    await all.nth(1).focus()
    await page.keyboard.press('b')
    await expect(page.getByTestId('chat-title')).toHaveText('Keys (fork)')
    await expect(msg(page, 'assistant')).toHaveCount(1)
  })

  test('R regenerates the focused answer', async ({ page }) => {
    const answer = await answerWith(page, 'a fresh answer')
    await openChat(page, [chatOf('c1', 'Regen', 'qwen3-8b', pair('the question', 'the old answer'))])
    await msg(page, 'assistant').focus()
    await page.keyboard.press('r')
    await expect(msg(page, 'assistant')).toContainText('a fresh answer')
    expect(JSON.stringify(answer.request().messages)).toContain('the question')
  })

  test('Edit and Regenerate are not offered while a reply is streaming', async ({ page }) => {
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Busy', 'qwen3-8b', pair('one', 'first'))])
    await page.getByTestId('chat-input').fill('two')
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('chat-streaming')).toBeVisible()
    await expect(msg(page, 'assistant').first().getByTitle('Regenerate')).toHaveCount(0)
    await expect(msg(page, 'assistant').first().getByTitle('Edit')).toHaveCount(0)
  })

  test('a failed reply keeps the text written so far, says why, and Retry asks again', async ({ page }) => {
    let calls = 0
    await page.route('**/v1/chat/completions', (route) => {
      calls++
      const body = calls === 1
        ? `data: ${JSON.stringify({ choices: [{ delta: { content: 'Half an answer' } }] })}\n\n`
          + `data: ${JSON.stringify({ error: { message: 'out of memory after 214 tokens' } })}\n\n`
          + 'data: [DONE]\n\n'
        : sse('A whole answer')
      route.fulfill({ status: 200, contentType: 'text/event-stream', body })
    })
    await openChat(page, [chatOf('c1', 'Fail', 'qwen3-8b', [])])
    await page.getByTestId('chat-input').fill('Tell me')
    await page.keyboard.press('Enter')

    const card = page.getByTestId('chat-error')
    await expect(card).toBeVisible()
    await expect(card.getByRole('heading')).toHaveText('The reply failed')
    await expect(card).toContainText('out of memory after 214 tokens')
    await expect(card).toContainText('What was written so far is kept above.')
    await expect(msg(page, 'assistant')).toContainText('Half an answer')
    // One action to take, and the way to the traces.
    await expect(card.getByRole('button')).toHaveCount(1)
    await expect(card.getByRole('link', { name: /traces/i })).toBeVisible()
    await card.locator('details summary').click()
    await expect(card.locator('pre')).toContainText('out of memory')

    await card.getByRole('button', { name: 'Retry' }).click()
    await expect(msg(page, 'assistant')).toContainText('A whole answer')
    await expect(page.getByTestId('chat-error')).toHaveCount(0)
    expect(calls).toBe(2)
  })

  test('a reply that quotes "Error:" in a sentence is not an error', async ({ page }) => {
    await openChat(page, [chatOf('c1', 'Quote', 'qwen3-8b', pair('what does it print', 'It prints Error: file not found when the path is wrong.'))])
    await expect(msg(page, 'assistant')).toContainText('Error: file not found')
    await expect(page.getByTestId('chat-error')).toHaveCount(0)
  })

  test('while a reply streams, Stop ends it and Esc does the same', async ({ page }) => {
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Stop', 'qwen3-8b', [])])
    const input = page.getByTestId('chat-input')

    await input.fill('first')
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('chat-stop')).toBeVisible()
    await page.evaluate(() => window.__sse.push({ choices: [{ delta: { content: 'Partial' } }] }))
    await expect(page.getByTestId('chat-streaming')).toContainText('Partial')
    await page.getByTestId('chat-stop').click()
    await expect(page.getByTestId('chat-stop')).toHaveCount(0)
    await expect(page.getByTestId('chat-send')).toBeVisible()

    await input.fill('second')
    await page.keyboard.press('Enter')
    await expect(page.getByTestId('chat-stop')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-stop')).toHaveCount(0)
    await expect.poll(() => page.evaluate(() => window.__sse.calls)).toBe(2)
  })

  test('the reply streams under the model name, with its reasoning open until the answer starts', async ({ page }) => {
    await controlStream(page)
    await openChat(page, [chatOf('c1', 'Stream', 'qwen3-8b', [])])
    await page.getByTestId('chat-input').fill('go')
    await page.keyboard.press('Enter')
    const turn = page.getByTestId('chat-streaming')
    await expect(turn.locator('.cx-who b')).toHaveText('qwen3-8b')

    await page.evaluate(() => window.__sse.push({ choices: [{ delta: { reasoning: 'Weighing the options.' } }] }))
    await expect(turn.getByRole('button', { name: /Thinking/ })).toHaveAttribute('aria-expanded', 'true')
    await expect(turn).toContainText('Weighing the options.')

    await page.evaluate(() => window.__sse.push({ choices: [{ delta: { content: 'The answer.' } }] }))
    await expect(turn.locator('.cx-prose')).toContainText('The answer.')
    // Once the answer is coming, the reasoning folds to one line.
    await expect(turn.getByRole('button', { name: /Thought/ })).toHaveAttribute('aria-expanded', 'false')

    await page.evaluate(() => window.__sse.end())
    await expect(page.getByTestId('chat-streaming')).toHaveCount(0)
    await expect.poll(async () => (await storedChats(page)).chats[0].history.map(m => m.role)).toEqual(['user', 'thinking', 'assistant'])
  })
})
