import { test, expect } from './coverage-fixtures.js'

// A model that is cold-loading onto a worker answers 503 with a `loading`
// object rather than an error. The chat must show that as progress and retry
// itself once the load finishes — the whole point of the change: an operator
// watching a 35 GB model stage normally used to see only repeated failures.
test.describe('Chat - model loading', () => {
  test('renders staging progress on 503 and retries when the model is ready', async ({ page }) => {
    await page.route('**/api/models/capabilities', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }] }),
      })
    })

    let completions = 0
    await page.route('**/v1/chat/completions', (route) => {
      completions++
      if (completions === 1) {
        route.fulfill({
          status: 503,
          contentType: 'application/json',
          headers: { 'Retry-After': '5' },
          body: JSON.stringify({
            error: {
              message: 'model test-model is staging on node nvidia-thor (41%)',
              type: 'model_loading',
              code: 'model_loading',
            },
            loading: {
              model: 'test-model',
              state: 'staging',
              node: 'nvidia-thor',
              progress: 41.2,
              bytes_sent: 14730000000,
              total_bytes: 35776484480,
              file_index: 1,
              total_files: 2,
              eta_seconds: 660,
            },
          }),
        })
        return
      }
      route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body:
          'data: {"choices":[{"delta":{"content":"loaded at last"}}]}\n\n' +
          'data: [DONE]\n\n',
      })
    })

    // First poll still staging, then the job is gone: the model is ready.
    let polls = 0
    await page.route('**/api/models/*/load-status', (route) => {
      polls++
      if (polls === 1) {
        route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            model: 'test-model', state: 'staging', node: 'nvidia-thor',
            progress: 62.5, bytes_sent: 22000000000, total_bytes: 35776484480,
            file_index: 2, total_files: 2, eta_seconds: 300,
          }),
        })
        return
      }
      route.fulfill({ status: 404, contentType: 'application/json', body: '{}' })
    })

    await page.goto('/app/chat')
    await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.chat-input').fill('Hello')
    await page.locator('.chat-send-btn').click()

    // The 503 is shown as progress, not as an error.
    await expect(page.locator('.chat-staging-progress')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('.chat-staging-label')).toContainText('nvidia-thor')
    await expect(page.locator('.chat-staging-pct')).toContainText('41%')

    // ...and the request retries itself once the load reports ready.
    await expect(page.getByText('loaded at last')).toBeVisible({ timeout: 25_000 })
    expect(completions).toBeGreaterThan(1)
  })
})
