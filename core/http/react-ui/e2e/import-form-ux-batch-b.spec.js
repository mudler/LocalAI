import { test, expect } from './coverage-fixtures.js'

// Batch B — the two surfaces of the import page and the options disclosure.
//
// This suite replaces the Simple/Power mode switch it used to cover. Simple
// and Power were ~80% the same surface: both a URI field plus preference
// fields, differing only in how many. The overlap cost a mode switch, a
// localStorage key and a three-button Keep/Discard/Cancel dialog whose only
// job was protecting state that switching modes would hide. One form with a
// collapsible options panel hides nothing, so none of that is needed.
//
// What genuinely differs is the kind of input: a source to resolve, or a YAML
// document to write. Those are the two tabs.

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
  // Visit once, clear, then let each test drive its own navigation — using
  // addInitScript would wipe storage on every navigation and defeat the
  // persistence tests.
  await page.goto('/app/import-model')
  await page.evaluate(() => {
    try {
      window.localStorage.removeItem('import-form-tab')
      window.localStorage.removeItem('import-form-options')
    } catch {
      // ignore quota / privacy mode
    }
  })
}

test.describe('Import form UX — Batch B1 (source / YAML tabs)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B1 — the source tab is active by default', async ({ page }) => {
    await page.goto('/app/import-model')
    const tabs = page.locator('[data-testid="import-tabs"]')
    await expect(tabs).toBeVisible({ timeout: 15_000 })
    await expect(tabs.locator('[data-testid="import-tab-source"]')).toHaveClass(/is-active/)
    await expect(tabs.locator('[data-testid="import-tab-yaml"]')).not.toHaveClass(/is-active/)
    await expect(page.locator('[data-testid="import-source-input"]')).toBeVisible()
  })

  test('B1 — the YAML tab swaps the surface and persists across reload', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-tab-yaml"]').click()

    await expect(page.locator('[data-testid="import-source-input"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="import-yaml"]')).toBeVisible()
    await expect(page.locator('[data-testid="import-create"]')).toBeVisible()

    const stored = await page.evaluate(() => window.localStorage.getItem('import-form-tab'))
    expect(stored).toBe('yaml')

    await page.reload()
    await expect(page.locator('[data-testid="import-tab-yaml"]')).toHaveClass(/is-active/)
    await expect(page.locator('[data-testid="import-yaml"]')).toBeVisible()
  })

  test('B1 — the mode switch and its confirmation dialog are gone', async ({ page }) => {
    await page.goto('/app/import-model')
    await expect(page.locator('[data-testid="simple-power-switch"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="power-tabs"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="switch-mode-dialog"]')).toHaveCount(0)

    // Setting a preference that used to be "advanced" and collapsing the panel
    // must not prompt: collapsed is not discarded.
    await page.locator('[data-testid="import-options-toggle"]').click()
    await page.locator('input[placeholder*="q4_k_m"]').fill('q5_k_m')
    await page.locator('[data-testid="import-options-toggle"]').click()
    await expect(page.locator('[data-testid="switch-mode-dialog"]')).toHaveCount(0)

    // And the value survives the round trip.
    await page.locator('[data-testid="import-options-toggle"]').click()
    await expect(page.locator('input[placeholder*="q4_k_m"]')).toHaveValue('q5_k_m')
  })
})

test.describe('Import form UX — Batch B2 (options disclosure)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('B2 — options are collapsed by default', async ({ page }) => {
    await page.goto('/app/import-model')
    const toggle = page.locator('[data-testid="import-options-toggle"]')
    await expect(toggle).toBeVisible({ timeout: 15_000 })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.locator('[data-testid="import-options-panel"]')).toHaveCount(0)
  })

  test('B2 — expanding reveals every preference in one panel', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-options-toggle"]').click()
    const panel = page.locator('[data-testid="import-options-panel"]')
    await expect(panel).toBeVisible()

    // The three that Simple mode used to show...
    await expect(panel.locator('button[aria-haspopup="listbox"]').first()).toBeVisible()
    await expect(panel.locator('input[placeholder*="Leave empty to use filename"]')).toBeVisible()
    await expect(panel.locator('textarea[placeholder*="Leave empty to use default"]')).toBeVisible()
    // ...and the ones that used to need a mode switch to reach.
    await expect(panel.locator('input[placeholder*="q4_k_m"]')).toBeVisible()
    await expect(panel.locator('input[placeholder*="fp16"]')).toBeVisible()
    await expect(panel.locator('input[placeholder*="AutoModelForCausalLM"]')).toBeVisible()
    await expect(panel.locator('button', { hasText: /Add custom/i })).toBeVisible()
  })

  test('B2 — the open/closed state persists across reload', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-options-toggle"]').click()
    await expect(page.locator('[data-testid="import-options-panel"]')).toBeVisible()

    await page.reload()
    await expect(page.locator('[data-testid="import-options-panel"]')).toBeVisible()
    await expect(page.locator('[data-testid="import-options-toggle"]')).toHaveAttribute('aria-expanded', 'true')
  })

  test('B2 — source, name and description survive a tab round trip', async ({ page }) => {
    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-source-input"]').fill('hf://Example/Model')
    await page.locator('[data-testid="import-options-toggle"]').click()
    const panel = page.locator('[data-testid="import-options-panel"]')
    await panel.locator('input[placeholder*="Leave empty to use filename"]').fill('my-model')
    await panel.locator('textarea[placeholder*="Leave empty to use default"]').fill('A description')

    await page.locator('[data-testid="import-tab-yaml"]').click()
    await page.locator('[data-testid="import-tab-source"]').click()

    await expect(page.locator('[data-testid="import-source-input"]')).toHaveValue('hf://Example/Model')
    await expect(page.locator('input[placeholder*="Leave empty to use filename"]')).toHaveValue('my-model')
    await expect(page.locator('textarea[placeholder*="Leave empty to use default"]')).toHaveValue('A description')
  })
})
