import { test, expect } from './coverage-fixtures.js'

// Regression coverage for issue #9904:
// - /api/operations was polled every 1s and *always* re-rendered the Chat
//   page, even when the response was unchanged. The reconciliation would
//   collapse any text selection inside an assistant message.
// - The copy button next to each assistant message used navigator.clipboard
//   without any fallback, which is undefined when the page is served over
//   plain http (non-secure context) from a remote host.

async function setupChatPage(page) {
  await page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }],
      }),
    })
  })

  // Poll-tracking mock: assert the hook is hammering /api/operations every
  // ~1s, and always return an empty list so its contents never change.
  let operationsHits = 0
  await page.route('**/api/operations', (route) => {
    operationsHits++
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ operations: [] }),
    })
  })

  await page.route('**/v1/chat/completions', (route) => {
    // One short SSE stream so the chat finishes streaming quickly and we
    // can interact with a stable assistant message.
    const body = [
      'data: {"choices":[{"delta":{"content":"Hello world this is a long assistant reply that we can try to select."},"index":0}]}\n\n',
      'data: {"choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\n',
      'data: [DONE]\n\n',
    ].join('')
    route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
      body,
    })
  })

  return { getOperationsHits: () => operationsHits }
}

test.describe('Chat - /api/operations polling (#9904)', () => {
  test('text selection inside an assistant message survives polling', async ({ page }) => {
    const { getOperationsHits } = await setupChatPage(page)

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hi')
    await page.locator('.chat-send-btn').click()

    const assistantContent = page.locator('.chat-message-assistant .chat-message-content').first()
    await expect(assistantContent).toContainText('Hello world', { timeout: 10_000 })

    // Sanity check: the polling we're regressing against is actually firing.
    await page.waitForTimeout(2_500)
    expect(getOperationsHits()).toBeGreaterThan(1)

    // Sanity check that the bug we're guarding against is structurally
    // possible: count how many times the assistant content node gets
    // *touched* by React (childList / characterData mutations) over a
    // 3-second window. Before the fix, every poll re-rendered Chat and
    // re-set dangerouslySetInnerHTML, triggering a mutation cascade that
    // collapsed the user's text selection. After the fix, polling with
    // identical contents must not mutate the DOM at all.
    const mutationCount = await assistantContent.evaluate((el) => new Promise((resolve) => {
      let count = 0
      const obs = new MutationObserver((records) => { count += records.length })
      obs.observe(el, { childList: true, subtree: true, characterData: true })
      setTimeout(() => { obs.disconnect(); resolve(count) }, 3_000)
    }))
    expect(mutationCount).toBe(0)

    // Same sanity check translated to a user-observable property: a
    // programmatically created selection survives the polling window.
    await assistantContent.evaluate((el) => {
      const range = document.createRange()
      range.selectNodeContents(el)
      const sel = window.getSelection()
      sel.removeAllRanges()
      sel.addRange(range)
    })

    const initialSelection = await page.evaluate(() => window.getSelection().toString())
    expect(initialSelection).toContain('Hello world')

    await page.waitForTimeout(2_500)

    const selectionAfterPolling = await page.evaluate(() => window.getSelection().toString())
    expect(selectionAfterPolling).toBe(initialSelection)
  })
})

test.describe('Chat - copy button (#9904)', () => {
  test('copy button works when navigator.clipboard is unavailable (plain http)', async ({ page }) => {
    await setupChatPage(page)

    // Simulate a non-secure context: hide navigator.clipboard before any of
    // our app code touches it. This mirrors what browsers do over plain
    // http from a remote host.
    await page.addInitScript(() => {
      Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
      try {
        Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
      } catch { /* some browsers refuse — the secure-context flag is enough */ }
    })

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hi')
    await page.locator('.chat-send-btn').click()

    const assistantBubble = page.locator('.chat-message-assistant .chat-message-bubble').first()
    await expect(assistantBubble).toContainText('Hello world', { timeout: 10_000 })

    // Spy on document.execCommand so we can confirm the fallback path ran.
    await page.evaluate(() => {
      window.__execCommandCalls = []
      const original = document.execCommand?.bind(document)
      document.execCommand = (cmd, ...rest) => {
        window.__execCommandCalls.push(cmd)
        // execCommand('copy') in a headless browser may return false because
        // there is no real clipboard, but the fact that we tried is what we
        // care about for this regression.
        return original ? original(cmd, ...rest) : false
      }
    })

    await assistantBubble.locator('.chat-message-actions button').first().click()

    const execCommandCalls = await page.evaluate(() => window.__execCommandCalls)
    expect(execCommandCalls).toContain('copy')
  })
})
