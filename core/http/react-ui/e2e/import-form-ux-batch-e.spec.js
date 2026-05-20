import { test, expect } from '@playwright/test'

// Batch E — modality chip row. A horizontal chip row sits above the Backend
// dropdown (inside the Simple-mode Options disclosure and in Power >
// Preferences) and filters the dropdown's options to the selected modality.
//
// Default chip is "Any" (no filter). Clicking a modality chip:
//   - Scopes buildBackendOptions to the chosen modality.
//   - If the currently-selected backend doesn't match the new filter, clears
//     prefs.backend and surfaces a toast.
//
// When the server returns an ambiguity alert with a modality, the matching
// chip becomes active automatically so users see only relevant backends
// even if they dismiss the alert.
//
// Routes for /backends/known are mocked to keep the dropdown stable in CI.

const MOCK_BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'vllm', modality: 'text', auto_detect: true, installed: false },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: false },
  { name: 'bark', modality: 'tts', auto_detect: true, installed: false },
  { name: 'kokoro', modality: 'tts', auto_detect: true, installed: true },
  { name: 'whisper', modality: 'asr', auto_detect: true, installed: true },
  { name: 'diffusers', modality: 'image', auto_detect: true, installed: false },
  { name: 'sentencetransformers', modality: 'embeddings', auto_detect: true, installed: true },
  { name: 'rerankers', modality: 'reranker', auto_detect: true, installed: true },
  { name: 'rfdetr', modality: 'detection', auto_detect: true, installed: true },
  { name: 'silero-vad', modality: 'vad', auto_detect: true, installed: true },
]

async function mockBackends(page) {
  await page.route('**/backends/known', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify(MOCK_BACKENDS),
    })
  })
}

async function clearFormStorage(page) {
  await page.goto('/app/import-model')
  await page.evaluate(() => {
    try {
      window.localStorage.removeItem('import-form-mode')
      window.localStorage.removeItem('import-form-power-tab')
    } catch {
      // ignore
    }
  })
}

function chips(page) {
  return page.locator('[data-testid="modality-chips"]')
}

function chip(page, key) {
  return page.locator(`[data-testid="modality-chip-${key}"]`)
}

function backendTrigger(page) {
  // Scope to <main>: the LanguageSwitcher in the sidebar also uses
  // aria-haspopup="listbox", so an unscoped .first() selector picks it
  // instead of the backend dropdown.
  return page.locator('main button[aria-haspopup="listbox"]').first()
}

test.describe('Import form UX — Batch E (modality chip row)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('E — chips visible inside Simple-mode expanded Options', async ({ page }) => {
    await page.goto('/app/import-model')
    // Chips are hidden inside the Options disclosure by default.
    await expect(chips(page)).toHaveCount(0)
    await page.locator('[data-testid="simple-options-toggle"]').click()
    await expect(chips(page)).toBeVisible()
    // Full set of chips renders.
    for (const key of ['', 'text', 'asr', 'tts', 'image', 'embeddings', 'reranker', 'detection', 'vad']) {
      await expect(chip(page, key)).toBeVisible()
    }
    // "Any" is active by default.
    await expect(chip(page, '')).toHaveAttribute('aria-checked', 'true')
  })

  test('E — chips visible inside Power > Preferences', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await expect(chips(page)).toBeVisible()
    await expect(chip(page, '')).toHaveAttribute('aria-checked', 'true')
  })

  test('E — chips NOT rendered inside Power > YAML', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('[data-testid="power-tab-yaml"]').click()
    await expect(chips(page)).toHaveCount(0)
  })

  test('E — clicking TTS filters the Backend dropdown to TTS backends only', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()

    // Baseline: default chip is Any, dropdown contains llama-cpp + piper.
    const baselineOption = (text) =>
      page.locator('[role="option"] > span').filter({ hasText: new RegExp(`^${text}$`) })
    await backendTrigger(page).click()
    await expect(baselineOption('llama-cpp').first()).toBeVisible()
    await expect(baselineOption('piper').first()).toBeVisible()
    // Close dropdown before clicking the chip.
    await page.keyboard.press('Escape')

    await chip(page, 'tts').click()
    await expect(chip(page, 'tts')).toHaveAttribute('aria-checked', 'true')
    await expect(chip(page, '')).toHaveAttribute('aria-checked', 'false')

    await backendTrigger(page).click()
    // TTS backends remain; non-TTS backends disappear. Match on the
    // option's label <span> (first flex:1 child) — the currently-focused
    // row carries a trailing "↵" keybind hint, so accessible-name
    // matching is unstable across reruns.
    const optionText = (name) =>
      page.locator('[role="option"] > span').filter({ hasText: new RegExp(`^${name}$`) })
    await expect(optionText('piper')).toHaveCount(1)
    await expect(optionText('bark')).toHaveCount(1)
    await expect(optionText('kokoro')).toHaveCount(1)
    await expect(optionText('llama-cpp')).toHaveCount(0)
    await expect(optionText('vllm')).toHaveCount(0)
    await expect(optionText('diffusers')).toHaveCount(0)
  })

  test('E — clicking Any after a filter restores the full list', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await chip(page, 'tts').click()
    await chip(page, '').click()
    await expect(chip(page, '')).toHaveAttribute('aria-checked', 'true')

    await backendTrigger(page).click()
    const optionByText = (text) =>
      page.locator('[role="option"] > span').filter({ hasText: new RegExp(`^${text}$`) })
    await expect(optionByText('llama-cpp').first()).toBeVisible()
    await expect(optionByText('piper').first()).toBeVisible()
    await expect(optionByText('diffusers').first()).toBeVisible()
  })

  test('E — switching filter clears a mismatched backend selection and shows a toast', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()

    // Pick llama-cpp (text modality) first.
    await backendTrigger(page).click()
    await page.getByRole('option', { name: 'llama-cpp', exact: true }).click()
    await expect(backendTrigger(page)).toContainText('llama-cpp')

    // Switching to TTS must drop llama-cpp and toast.
    await chip(page, 'tts').click()
    await expect(backendTrigger(page)).toContainText(/Auto-detect/)

    // Toast copy anchored to the doc — match prefix so we don't depend on
    // exact modality casing.
    const toast = page.locator('text=/Cleared backend selection/i').first()
    await expect(toast).toBeVisible({ timeout: 5_000 })
  })

  test('E — ambiguity alert with modality=tts auto-activates the TTS chip', async ({ page }) => {
    await page.route('**/models/import-uri', (route) => {
      route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'ambiguous import',
          detail: 'ambiguous',
          modality: 'tts',
          candidates: ['piper', 'bark', 'kokoro'],
          hint: 'pick one',
        }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('input[placeholder*="huggingface://"]').fill('https://huggingface.co/nari-labs/Dia-1.6B')
    await page.locator('button', { hasText: /Import Model/ }).click()

    // Wait for the alert then expand Options to find the chip row.
    await expect(page.locator('[data-testid="ambiguity-alert"]')).toBeVisible({ timeout: 5_000 })
    await page.locator('[data-testid="simple-options-toggle"]').click()
    await expect(chip(page, 'tts')).toHaveAttribute('aria-checked', 'true')
  })

  test('E — keyboard: focus first chip with Tab, arrow keys move, Space selects', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    // Focus the Any chip directly to avoid counting every tab stop in the
    // Power mode header — radiogroup roving focus is what we're testing.
    await chip(page, '').focus()
    await expect(chip(page, '')).toBeFocused()

    // Arrow right moves focus to the next chip.
    await page.keyboard.press('ArrowRight')
    await expect(chip(page, 'text')).toBeFocused()
    await page.keyboard.press('ArrowRight')
    await expect(chip(page, 'asr')).toBeFocused()
    // ArrowLeft goes back.
    await page.keyboard.press('ArrowLeft')
    await expect(chip(page, 'text')).toBeFocused()

    // Space selects the currently-focused chip.
    await page.keyboard.press(' ')
    await expect(chip(page, 'text')).toHaveAttribute('aria-checked', 'true')
  })
})
