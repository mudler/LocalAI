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
    await page.route('**/api/backends/known', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_BACKENDS),
      })
    })
  })

  test('manual-pick badge renders for preference-only backends with tooltip', async ({ page }) => {
    await page.goto('/app/import-model')
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
