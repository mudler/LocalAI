import { test, expect } from './coverage-fixtures.js'

// Batch F3 — Enter-to-submit on the source input. The field, the ambiguity
// alert, the options disclosure and the Import button all live in one <form>,
// so Enter submits natively — no hidden submit button standing in for an
// action that sits outside the form. This test asserts the POST is issued and
// that the Description textarea still inserts a newline instead of submitting.

const MOCK_BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: true },
]

async function mockBackends(page) {
  await page.route('**/backends/known', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify(MOCK_BACKENDS),
    })
  })
}

test.describe('Import form UX — Batch F3 (Enter-to-submit on the source field)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    // Reset persisted UI state so every test starts on the source tab.
    await page.goto('/app/import-model')
    await page.evaluate(() => {
      try {
        window.localStorage.removeItem('import-form-tab')
        window.localStorage.removeItem('import-form-options')
      } catch {
        // ignore
      }
    })
  })

  test('F3 — pressing Enter in the URI input POSTs /models/import-uri', async ({ page }) => {
    let posted = false
    await page.route('**/models/import-uri', (route, request) => {
      if (request.method() === 'POST') posted = true
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ uuid: 'test-job-f3', ID: 'test-job-f3' }),
      })
    })
    // Stub the polling endpoint so the submit settles deterministically.
    await page.route('**/models/jobs/**', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ completed: true, message: 'done' }),
      })
    })

    await page.goto('/app/import-model')
    const uri = page.locator('[data-testid="import-source-input"]')
    await uri.fill('hf://TheBloke/Llama-2-7B-Chat-GGUF')
    await uri.press('Enter')

    await expect.poll(() => posted, { timeout: 5_000 }).toBe(true)
  })

  test('F3 — Enter in the Description textarea inserts a newline and does not submit', async ({ page }) => {
    let posted = false
    await page.route('**/models/import-uri', (route, request) => {
      if (request.method() === 'POST') posted = true
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ uuid: 'test-job-f3-desc', ID: 'test-job-f3-desc' }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-source-input"]').fill('hf://Example/Model')
    await page.locator('[data-testid="import-options-toggle"]').click()

    const panel = page.locator('[data-testid="import-options-panel"]')
    const textarea = panel.locator('textarea[placeholder*="Leave empty to use default"]')
    await textarea.focus()
    await textarea.type('first line')
    await textarea.press('Enter')
    await textarea.type('second line')

    await expect(textarea).toHaveValue('first line\nsecond line')
    // The textarea must not have triggered a submit.
    expect(posted).toBe(false)
  })
})
