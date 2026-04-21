import { test, expect } from '@playwright/test'

// Mock /backends/known shape mirrors schema.KnownBackend. The suite covers
// three concerns from Batch A: manual-pick badge (A1), inline ambiguity
// alert with candidate chips (A2), auto-install warning (A3). Import form
// lives at /app/import-model and loads its backend list from
// GET /api/backends/known on mount — routes are mocked before navigation.

const MOCK_BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'vllm', modality: 'text', auto_detect: true, installed: false },
  { name: 'mlx-vlm', modality: 'text', auto_detect: false, installed: false,
    description: 'MLX vision-language models (preference-only)' },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: false },
  { name: 'bark', modality: 'tts', auto_detect: true, installed: false },
  { name: 'kokoro', modality: 'tts', auto_detect: true, installed: true },
  { name: 'diffusers', modality: 'image', auto_detect: true, installed: false },
]

test.describe('Import form UX — Batch A1 (manual-pick badge)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/backends/known', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_BACKENDS),
      })
    })
  })

  test('A1 — manual-pick badge renders for preference-only backends with tooltip', async ({ page }) => {
    await page.goto('/app/import-model')
    // Simple mode hides preferences behind the Options disclosure (Batch B).
    await page.locator('[data-testid="simple-options-toggle"]').click()
    // Open the Backend dropdown
    const backendButton = page.locator('button', { hasText: /Auto-detect/ }).first()
    await backendButton.click()

    // The mlx-vlm row (auto_detect=false) should carry the "manual pick" badge.
    // Scope by option row to avoid matching stray occurrences elsewhere.
    const mlxRow = page.locator('[role="option"]', { hasText: 'mlx-vlm' })
    await expect(mlxRow).toBeVisible()
    const badge = mlxRow.locator('text=manual pick')
    await expect(badge).toBeVisible()
    await expect(badge).toHaveAttribute('title', /Auto-detect won't route/i)

    // Auto-detectable backends (e.g. llama-cpp) must NOT carry the badge.
    const llamaRow = page.locator('[role="option"]', { hasText: /^llama-cpp/ })
    await expect(llamaRow).toBeVisible()
    await expect(llamaRow.locator('text=manual pick')).toHaveCount(0)

    // Labels must NOT contain the legacy " (preference-only)" suffix.
    await expect(page.locator('text=(preference-only)')).toHaveCount(0)
  })
})

test.describe('Import form UX — Batch A2 (inline ambiguity picker)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/backends/known', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_BACKENDS),
      })
    })
  })

  test('A2 — ambiguity alert with TTS candidate chips, clicking sets backend and resubmits', async ({ page }) => {
    let hits = 0
    await page.route('**/models/import-uri', (route) => {
      hits += 1
      if (hits === 1) {
        route.fulfill({
          status: 400,
          contentType: 'application/json',
          body: JSON.stringify({
            error: 'ambiguous import',
            detail: 'importer: ambiguous — detected modality "tts" (pipeline_tag="text-to-speech")',
            modality: 'tts',
            candidates: ['piper', 'bark', 'kokoro'],
            hint: 'Pass preferences.backend to pick one of the candidates.',
          }),
        })
      } else {
        route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ uuid: 'test-job-123', ID: 'test-job-123' }),
        })
      }
    })
    // Job polling endpoint — reply completed so the second submit settles.
    await page.route('**/models/jobs/**', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ completed: true, message: 'done' }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('input[placeholder*="huggingface://"]').fill('https://huggingface.co/nari-labs/Dia-1.6B')
    await page.locator('button', { hasText: /Import Model/ }).click()

    const alert = page.locator('[data-testid="ambiguity-alert"]')
    await expect(alert).toBeVisible({ timeout: 5_000 })
    await expect(alert).toContainText(/text-to-speech/i)
    await expect(alert.locator('[data-testid="ambiguity-chip-piper"]')).toBeVisible()
    await expect(alert.locator('[data-testid="ambiguity-chip-bark"]')).toBeVisible()
    await expect(alert.locator('[data-testid="ambiguity-chip-kokoro"]')).toBeVisible()

    // Click piper — the form should update the dropdown + auto-resubmit.
    await alert.locator('[data-testid="ambiguity-chip-piper"]').click()

    await expect(alert).toHaveCount(0)
    // The Backend dropdown now lives behind the Simple-mode Options disclosure.
    // Expand it to assert the picked backend landed in the dropdown.
    await page.locator('[data-testid="simple-options-toggle"]').click()
    await expect(page.locator('button', { hasText: 'piper' }).first()).toBeVisible()
    await expect.poll(() => hits, { timeout: 5_000 }).toBeGreaterThanOrEqual(2)
  })

  test('A2 — dismissing the ambiguity alert clears it without setting a backend', async ({ page }) => {
    await page.route('**/models/import-uri', (route) => {
      route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'ambiguous import',
          detail: 'ambiguous',
          modality: 'tts',
          candidates: ['piper', 'bark'],
          hint: 'pick one',
        }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('input[placeholder*="huggingface://"]').fill('https://huggingface.co/nari-labs/Dia-1.6B')
    await page.locator('button', { hasText: /Import Model/ }).click()

    const alert = page.locator('[data-testid="ambiguity-alert"]')
    await expect(alert).toBeVisible({ timeout: 5_000 })

    await alert.locator('[data-testid="ambiguity-dismiss"]').click()
    await expect(alert).toHaveCount(0)
    // Backend dropdown now lives under the Options disclosure in Simple mode.
    await page.locator('[data-testid="simple-options-toggle"]').click()
    await expect(page.locator('button', { hasText: /Auto-detect/ }).first()).toBeVisible()
  })
})

test.describe('Import form UX — Batch A3 (auto-install warning)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/backends/known', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_BACKENDS),
      })
    })
  })

  test('A3 — picking a not-installed backend shows the auto-install note', async ({ page }) => {
    await page.goto('/app/import-model')
    // Simple mode hides the Backend dropdown behind Options (Batch B).
    await page.locator('[data-testid="simple-options-toggle"]').click()

    const backendButton = page.locator('button', { hasText: /Auto-detect/ }).first()
    await backendButton.click()
    // vllm is installed: false in the mock.
    await page.locator('[role="option"]', { hasText: /^vllm/ }).click()

    const note = page.locator('[data-testid="auto-install-note"]')
    await expect(note).toBeVisible()
    await expect(note).toContainText(/isn.t installed yet/i)
  })

  test('A3 — picking an installed backend does not show the auto-install note', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="simple-options-toggle"]').click()
    const backendButton = page.locator('button', { hasText: /Auto-detect/ }).first()
    await backendButton.click()
    // llama-cpp is installed: true in the mock.
    await page.locator('[role="option"]', { hasText: /^llama-cpp/ }).click()
    await expect(page.locator('[data-testid="auto-install-note"]')).toHaveCount(0)
  })

  test('A3 — Auto-detect (empty backend) does not show the note', async ({ page }) => {
    await page.goto('/app/import-model')
    // Default state is Auto-detect; the note must not be present.
    await expect(page.locator('[data-testid="auto-install-note"]')).toHaveCount(0)
  })
})
