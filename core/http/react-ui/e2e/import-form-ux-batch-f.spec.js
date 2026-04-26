import { test, expect } from '@playwright/test'

// Batch F3 — Enter-to-submit on the URI input in Simple mode. Wrapping the
// URI input + ambiguity alert + Options disclosure in a <form> means pressing
// Enter while focus is in the URI field submits via handleSimpleImport. This
// test asserts the POST is issued and that the Description textarea still
// inserts a newline on Enter instead of submitting.

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

test.describe('Import form UX — Batch F3 (Enter-to-submit in Simple mode)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    // Reset the persisted mode so every test starts in Simple.
    await page.goto('/app/import-model')
    await page.evaluate(() => {
      try {
        window.localStorage.removeItem('import-form-mode')
        window.localStorage.removeItem('import-form-power-tab')
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
    const uri = page.locator('input[placeholder*="huggingface://"]')
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
    await page.locator('input[placeholder*="huggingface://"]').fill('hf://Example/Model')
    await page.locator('[data-testid="simple-options-toggle"]').click()

    const panel = page.locator('[data-testid="simple-options-panel"]')
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
