import { test, expect } from './coverage-fixtures.js'
import { chatOf, pair, mockChat, openChat, GO_BLOCK } from './chat-fixtures.js'

// The canvas opens on demand beside the thread, find marks matches in the
// messages that are loaded, and a long thread has a way back to the end.

const CODE_THREAD = chatOf('c1', 'Code', 'qwen3-8b', [
  { role: 'user', content: 'write the handler and a page' },
  {
    role: 'assistant',
    content: 'First the handler:\n\n```go\n' + GO_BLOCK + '\n```\n\nThen the page:\n\n```html\n<h1>Hello canvas</h1>\n```\n\nThat is all.',
  },
])

const canvas = (page) => page.getByTestId('canvas-panel')

test.describe('Chat canvas', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('the Canvas button on a code block opens that block in the panel', async ({ page }) => {
    await openChat(page, [CODE_THREAD])
    await expect(canvas(page)).toHaveCount(0)
    await page.locator('.code-block').nth(1).getByRole('button', { name: 'Canvas' }).click()
    await expect(canvas(page)).toBeVisible()
    // Canvas mode is on now, and the block that was clicked is the one shown.
    await expect(page.getByTestId('chat-canvas-chip')).toHaveAttribute('aria-pressed', 'true')
    await expect(canvas(page).getByRole('tab', { selected: true })).toContainText('.html')
    await expect(canvas(page).getByRole('button', { name: 'Preview' })).toHaveAttribute('aria-pressed', 'true')
    await expect(canvas(page).frameLocator('iframe').getByRole('heading', { name: 'Hello canvas' })).toBeVisible()
  })

  test('with Canvas on, code blocks are cards and a card opens the panel with tabs', async ({ page }) => {
    await openChat(page, [CODE_THREAD])
    await page.getByTestId('chat-canvas-chip').click()
    // Turning it on opens the panel on the newest block; the blocks are cards now.
    await expect(canvas(page)).toBeVisible()
    await expect(page.locator('.artifact-card')).toHaveCount(2)
    await expect(canvas(page).getByRole('tab')).toHaveCount(2)

    await canvas(page).getByRole('tab', { name: /main.go/ }).click()
    await expect(canvas(page)).toContainText('context.WithCancel')
    // Code and Preview switch for html.
    await canvas(page).getByRole('tab', { name: /\.html/ }).click()
    await canvas(page).getByRole('button', { name: 'Code' }).click()
    await expect(canvas(page)).toContainText('<h1>Hello canvas</h1>')
    await expect(canvas(page).getByRole('button', { name: 'Copy' })).toBeVisible()
    await expect(canvas(page).getByRole('button', { name: 'Download' })).toBeVisible()

    await canvas(page).getByRole('button', { name: 'Close canvas' }).click()
    await expect(canvas(page)).toHaveCount(0)
    // The chip carries the count and opens it again.
    await page.getByRole('button', { name: 'Open canvas panel' }).click()
    await expect(canvas(page)).toBeVisible()
  })

  test('Esc closes the canvas', async ({ page }) => {
    await openChat(page, [CODE_THREAD])
    await page.getByTestId('chat-canvas-chip').click()
    await expect(canvas(page)).toBeVisible()
    await page.locator('.cx-body').click({ position: { x: 5, y: 5 } })
    await page.keyboard.press('Escape')
    await expect(canvas(page)).toHaveCount(0)
  })

  test('the panel is wide beside the thread on a desktop window', async ({ page }) => {
    await openChat(page, [CODE_THREAD])
    await page.getByTestId('chat-canvas-chip').click()
    const panel = await canvas(page).boundingBox()
    const view = page.viewportSize()
    expect(panel.width).toBeGreaterThan(view.width * 0.3)
    // The thread keeps its own room beside it.
    const thread = await page.getByTestId('chat-thread').boundingBox()
    expect(thread.x + thread.width).toBeLessThanOrEqual(panel.x + 1)
  })

  test('on a phone the canvas takes the whole page', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openChat(page, [CODE_THREAD])
    await page.getByTestId('chat-canvas-chip').click()
    const box = await canvas(page).boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(389)
    expect(box.height).toBeGreaterThan(500)
    await canvas(page).getByRole('button', { name: 'Close canvas' }).click()
    await expect(canvas(page)).toHaveCount(0)
  })
})

test.describe('Chat find', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  const THREAD = chatOf('c1', 'Find', 'qwen3-8b', [
    ...pair('Why do goroutines leak?', 'Because nothing watches the context. Each goroutine waits forever, and the goroutine count grows.'),
    ...pair('And the fix?', 'Select on ctx.Done() in every goroutine.'),
  ])

  test('Ctrl+Shift+F searches the loaded messages, shows n of m and steps with Enter', async ({ page }) => {
    let completions = 0
    await page.route('**/v1/chat/completions', (route) => { completions++; route.abort() })
    await openChat(page, [THREAD])
    await page.keyboard.press('Control+Shift+F')
    const input = page.getByTestId('chat-find-input')
    await expect(input).toBeFocused()
    await input.fill('goroutine')
    const count = page.getByTestId('chat-find-count')
    await expect(count).toHaveText('1 of 4')
    await expect(page.locator('mark.cx-hit')).toHaveCount(4)
    await expect(page.locator('mark.cx-hit[data-cur]')).toHaveCount(1)
    await expect(page.locator('mark.cx-hit').first()).toHaveAttribute('data-cur', '')

    await input.press('Enter')
    await expect(count).toHaveText('2 of 4')
    await expect(page.locator('mark.cx-hit').nth(1)).toHaveAttribute('data-cur', '')
    await input.press('Shift+Enter')
    await input.press('Shift+Enter')
    await expect(count).toHaveText('4 of 4')

    // Case does not matter, and a miss says so.
    await input.fill('CTX.DONE')
    await expect(count).toHaveText('1 of 1')
    await input.fill('nothing like this')
    await expect(count).toHaveText('No matches')
    await expect(page.locator('mark.cx-hit')).toHaveCount(0)

    // The search never leaves the page.
    expect(completions).toBe(0)
  })

  test('Esc closes the search, clears the marks and puts the cursor back in the box', async ({ page }) => {
    await openChat(page, [THREAD])
    await page.getByTestId('chat-find-button').click()
    await page.getByTestId('chat-find-input').fill('context')
    await expect(page.locator('mark.cx-hit')).not.toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-find')).toHaveCount(0)
    await expect(page.locator('mark.cx-hit')).toHaveCount(0)
    await expect(page.getByTestId('chat-input')).toBeFocused()
    // The text of the messages is whole again.
    await expect(page.locator('[data-role="assistant"]').first()).toContainText('Because nothing watches the context.')
  })

  test('it finds text inside code blocks and in your own messages', async ({ page }) => {
    await openChat(page, [CODE_THREAD])
    await page.keyboard.press('Control+Shift+F')
    await page.getByTestId('chat-find-input').fill('write the')
    await expect(page.locator('[data-role="user"] mark.cx-hit')).toHaveCount(1)
    await page.getByTestId('chat-find-input').fill('WithCancel')
    await expect(page.locator('.code-block mark.cx-hit')).toHaveCount(1)
  })
})

test.describe('Chat long threads', () => {
  test('Jump to latest appears when you leave the end and takes you back', async ({ page }) => {
    await mockChat(page)
    const history = []
    for (let i = 1; i <= 40; i++) history.push(...pair(`Question ${i} about context sizes`, `Answer ${i}. The same advice applies each time, so keep going.`))
    await openChat(page, [chatOf('c1', 'Long', 'qwen3-8b', history)])
    const body = page.locator('.cx-body')
    await expect(page.getByTestId('chat-message')).toHaveCount(80)
    await expect(page.getByTestId('chat-jump-latest')).toHaveCount(0)

    await body.evaluate(el => { el.scrollTop = 0 })
    const jump = page.getByTestId('chat-jump-latest')
    await expect(jump).toBeVisible()
    await jump.click()
    await expect(jump).toHaveCount(0)
    await expect.poll(() => body.evaluate(el => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(80)
  })

  test('a long thread does not push the page: the thread scrolls inside the window', async ({ page }) => {
    await mockChat(page)
    const history = []
    for (let i = 1; i <= 40; i++) history.push(...pair(`Question ${i}`, `Answer ${i}`))
    await openChat(page, [chatOf('c1', 'Long', 'qwen3-8b', history)])
    await expect(page.getByTestId('chat-message')).toHaveCount(80)
    const overflow = await page.evaluate(() => document.documentElement.scrollHeight - window.innerHeight)
    expect(overflow).toBeLessThanOrEqual(1)
    // The composer stays in view.
    await expect(page.getByTestId('chat-input')).toBeInViewport()
  })
})
