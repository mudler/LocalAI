import { test, expect } from '@playwright/test'

// Batch B — Simple / Power mode with quick switch. This suite exercises the
// mode switch itself, the collapsible Options disclosure in Simple mode,
// the Preferences/YAML tabs inside Power mode, and the confirmation dialog
// that fires when switching Power -> Simple with custom prefs set.
//
// Routes for /backends/known are mocked to keep the Backend dropdown stable
// across browsers and CI.

const MOCK_BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'vllm', modality: 'text', auto_detect: true, installed: false },
  { name: 'diffusers', modality: 'image', auto_detect: true, installed: false },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: false },
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
  // We use goto('about:blank') then manipulate localStorage on the test
  // origin via page.addInitScript — but only once, before the first real
  // navigation. Using addInitScript directly would wipe storage on every
  // navigation (including reloads), which defeats the persistence tests.
  // Instead, we visit the app once, clear storage, then let each test drive
  // navigation itself.
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

test.describe('Import form UX — Batch B1 (Simple/Power switch)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B1 — default mode is Simple and the segmented control shows it active', async ({ page }) => {
    await page.goto('/app/import-model')
    const control = page.locator('[data-testid="simple-power-switch"]')
    await expect(control).toBeVisible()
    await expect(control.locator('[data-testid="mode-simple"]')).toHaveClass(/is-active/)
    await expect(control.locator('[data-testid="mode-power"]')).not.toHaveClass(/is-active/)
  })

  test('B1 — clicking Power activates Power mode and persists to localStorage', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    const control = page.locator('[data-testid="simple-power-switch"]')
    await expect(control.locator('[data-testid="mode-power"]')).toHaveClass(/is-active/)
    await expect(control.locator('[data-testid="mode-simple"]')).not.toHaveClass(/is-active/)
    const stored = await page.evaluate(() => window.localStorage.getItem('import-form-mode'))
    expect(stored).toBe('power')
  })

  test('B1 — localStorage mode persists across reload', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.reload()
    const control = page.locator('[data-testid="simple-power-switch"]')
    await expect(control.locator('[data-testid="mode-power"]')).toHaveClass(/is-active/)
  })
})

test.describe('Import form UX — Batch B2 (Simple mode Options disclosure)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B2 — Options disclosure is collapsed by default and hides Backend/Name/Description', async ({ page }) => {
    await page.goto('/app/import-model')
    const toggle = page.locator('[data-testid="simple-options-toggle"]')
    await expect(toggle).toBeVisible()
    // The content region must not be visible before click.
    await expect(page.locator('[data-testid="simple-options-panel"]')).toHaveCount(0)
  })

  test('B2 — expanding Options reveals exactly Backend, Model Name, Description', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="simple-options-toggle"]').click()
    const panel = page.locator('[data-testid="simple-options-panel"]')
    await expect(panel).toBeVisible()
    // Backend dropdown button reachable
    await expect(panel.locator('button', { hasText: /Auto-detect/ }).first()).toBeVisible()
    // Model Name + Description visible
    await expect(panel.locator('input[placeholder*="Leave empty to use filename"]')).toBeVisible()
    await expect(panel.locator('textarea[placeholder*="Leave empty to use default"]')).toBeVisible()
    // Fields NOT in Simple mode must be absent
    await expect(panel.locator('input[placeholder*="q4_k_m"]')).toHaveCount(0)
    await expect(panel.locator('input[placeholder*="fp16"]')).toHaveCount(0)
    await expect(panel.locator('input[placeholder*="AutoModelForCausalLM"]')).toHaveCount(0)
    // No Custom Preferences button in Simple mode
    await expect(page.locator('button', { hasText: /Add Custom/ })).toHaveCount(0)
  })
})

test.describe('Import form UX — Batch B3 (Power mode tabs)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B3 — Power mode shows Preferences and YAML tabs with Preferences active', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    const tabs = page.locator('[data-testid="power-tabs"]')
    await expect(tabs).toBeVisible()
    await expect(tabs.locator('[data-testid="power-tab-preferences"]')).toHaveClass(/is-active/)
    await expect(tabs.locator('[data-testid="power-tab-yaml"]')).not.toHaveClass(/is-active/)
    // Full preferences panel is visible — Quantizations input should exist.
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toBeVisible()
  })

  test('B3 — YAML tab swaps to the CodeEditor and button reads Create', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('[data-testid="power-tab-yaml"]').click()
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toHaveCount(0)
    await expect(page.locator('button', { hasText: /^\s*Create$/ })).toBeVisible()
  })

  test('B3 — powerTab persists across reload', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('[data-testid="power-tab-yaml"]').click()
    await page.reload()
    const tabs = page.locator('[data-testid="power-tabs"]')
    await expect(tabs.locator('[data-testid="power-tab-yaml"]')).toHaveClass(/is-active/)
  })

  test('B3 — URI + Name + Description typed in Simple carry over to Power', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('input[placeholder*="huggingface://"]').fill('hf://Example/Model')
    await page.locator('[data-testid="simple-options-toggle"]').click()
    const panel = page.locator('[data-testid="simple-options-panel"]')
    await panel.locator('input[placeholder*="Leave empty to use filename"]').fill('my-model')
    await panel.locator('textarea[placeholder*="Leave empty to use default"]').fill('A description')

    await page.locator('[data-testid="mode-power"]').click()
    await expect(page.locator('input[placeholder*="huggingface://"]')).toHaveValue('hf://Example/Model')
    await expect(page.locator('input[placeholder*="Leave empty to use filename"]')).toHaveValue('my-model')
    await expect(page.locator('textarea[placeholder*="Leave empty to use default"]')).toHaveValue('A description')
  })
})

test.describe('Import form UX — Batch B4 (switch-mode dialog)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B4 — Power -> Simple with no custom prefs switches silently', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('[data-testid="mode-simple"]').click()
    await expect(page.locator('[data-testid="switch-mode-dialog"]')).toHaveCount(0)
    const control = page.locator('[data-testid="simple-power-switch"]')
    await expect(control.locator('[data-testid="mode-simple"]')).toHaveClass(/is-active/)
  })

  test('B4 — Power -> Simple with a custom quantization opens the dialog', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('input[placeholder*="q4_k_m"]').fill('q5_k_m')
    await page.locator('[data-testid="mode-simple"]').click()

    const dialog = page.locator('[data-testid="switch-mode-dialog"]')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText(/Keep your custom preferences/i)
    await expect(dialog.locator('[data-testid="switch-mode-keep"]')).toBeVisible()
    await expect(dialog.locator('[data-testid="switch-mode-discard"]')).toBeVisible()
    await expect(dialog.locator('[data-testid="switch-mode-cancel"]')).toBeVisible()
  })

  test('B4 — Keep & switch preserves quantization across a round trip', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('input[placeholder*="q4_k_m"]').fill('q5_k_m')
    await page.locator('[data-testid="mode-simple"]').click()
    await page.locator('[data-testid="switch-mode-keep"]').click()
    await expect(page.locator('[data-testid="switch-mode-dialog"]')).toHaveCount(0)
    // Round-trip back to Power — quantization should still be populated.
    await page.locator('[data-testid="mode-power"]').click()
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toHaveValue('q5_k_m')
  })

  test('B4 — Discard & switch resets prefs back to defaults', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('input[placeholder*="q4_k_m"]').fill('q5_k_m')
    await page.locator('[data-testid="mode-simple"]').click()
    await page.locator('[data-testid="switch-mode-discard"]').click()
    await page.locator('[data-testid="mode-power"]').click()
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toHaveValue('')
  })

  test('B4 — Cancel leaves the user in Power mode with prefs intact', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="mode-power"]').click()
    await page.locator('input[placeholder*="q4_k_m"]').fill('q5_k_m')
    await page.locator('[data-testid="mode-simple"]').click()
    await page.locator('[data-testid="switch-mode-cancel"]').click()
    await expect(page.locator('[data-testid="switch-mode-dialog"]')).toHaveCount(0)
    const control = page.locator('[data-testid="simple-power-switch"]')
    await expect(control.locator('[data-testid="mode-power"]')).toHaveClass(/is-active/)
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toHaveValue('q5_k_m')
  })
})
