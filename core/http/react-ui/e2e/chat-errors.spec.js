import { test, expect } from '@playwright/test'

async function setupChatPage(page) {
  // Mock capabilities endpoint so ModelSelector auto-selects a model
  await page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }],
      }),
    })
  })
}

test.describe('Chat - Error Handling', () => {
  test('shows backend error message on HTTP error', async ({ page }) => {
    await setupChatPage(page)

    await page.route('**/v1/chat/completions', (route) => {
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({
          error: { message: 'Model failed to load', type: 'server_error', code: 500 },
        }),
      })
    })

    await page.goto('/app/chat')
    // Wait for the model to be auto-selected (ModelSelector shows model name in button)
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hello')
    await page.locator('.chat-send-btn').click()

    await expect(page.getByRole('paragraph').filter({ hasText: 'Model failed to load' })).toBeVisible({ timeout: 10_000 })
  })

  test('shows error with trace link on HTTP error', async ({ page }) => {
    await setupChatPage(page)

    await page.route('**/v1/chat/completions', (route) => {
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({
          error: { message: 'Backend crashed unexpectedly', type: 'server_error', code: 500 },
        }),
      })
    })

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hello')
    await page.locator('.chat-send-btn').click()

    await expect(page.getByRole('paragraph').filter({ hasText: 'Backend crashed unexpectedly' })).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('.chat-error-trace-link')).toBeVisible()
  })

  test('shows error from SSE error event during streaming', async ({ page }) => {
    await setupChatPage(page)

    await page.route('**/v1/chat/completions', (route) => {
      const body = [
        'data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}\n\n',
        'data: {"error":{"message":"Backend crashed mid-stream","type":"server_error"}}\n\n',
        'data: [DONE]\n\n',
      ].join('')
      route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
        body,
      })
    })

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hello')
    await page.locator('.chat-send-btn').click()

    await expect(page.getByRole('paragraph').filter({ hasText: 'Backend crashed mid-stream' })).toBeVisible({ timeout: 10_000 })
  })

  test('shows generic HTTP error when no error body', async ({ page }) => {
    await setupChatPage(page)

    await page.route('**/v1/chat/completions', (route) => {
      route.fulfill({
        status: 502,
        contentType: 'text/plain',
        body: 'Bad Gateway',
      })
    })

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hello')
    await page.locator('.chat-send-btn').click()

    await expect(page.getByRole('paragraph').filter({ hasText: 'HTTP 502' })).toBeVisible({ timeout: 10_000 })
  })
})
