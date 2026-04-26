import { test, expect } from '@playwright/test'

// Batch D — progressive disclosure of preference fields. Power > Preferences
// tab gates Quantizations, MMProj Quantizations, and Model Type so they only
// render when the selected backend can actually use them. Hidden fields must
// preserve their state (no reset on hide) so users don't lose input when
// flipping backends back and forth.
//
// Routes for /backends/known are mocked to keep the Backend dropdown stable
// across browsers and CI. The list is intentionally broad enough to exercise
// every visibility rule.

const MOCK_BACKENDS = [
  { name: 'llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'ik-llama-cpp', modality: 'text', auto_detect: true, installed: true },
  { name: 'turboquant', modality: 'text', auto_detect: true, installed: true },
  { name: 'stablediffusion-ggml', modality: 'image', auto_detect: true, installed: true },
  { name: 'transformers', modality: 'text', auto_detect: true, installed: true },
  { name: 'sentencetransformers', modality: 'embeddings', auto_detect: true, installed: true },
  { name: 'rerankers', modality: 'reranker', auto_detect: true, installed: true },
  { name: 'rfdetr', modality: 'detection', auto_detect: true, installed: true },
  { name: 'piper', modality: 'tts', auto_detect: true, installed: true },
  { name: 'diffusers', modality: 'image', auto_detect: true, installed: true },
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

async function enterPowerPreferences(page) {
  await page.goto('/app/import-model')
  await page.locator('[data-testid="mode-power"]').click()
  // Preferences tab is active by default in Power mode.
  await expect(page.locator('[data-testid="power-tab-preferences"]')).toHaveClass(/is-active/)
}

// Backend dropdown trigger — the SearchableSelect button has
// `aria-haspopup="listbox"`. There's exactly one on the Power/Preferences
// page so the first match is stable.
function backendTrigger(page) {
  return page.locator('button[aria-haspopup="listbox"]').first()
}

async function selectBackend(page, name) {
  await backendTrigger(page).click()
  await page.getByRole('option', { name, exact: true }).click()
}

// Locators that identify each gated field — using the placeholder that was
// already present in the component so we don't need to add new test IDs.
const quantizationsInput = (page) => page.locator('input[placeholder*="q4_k_m"]')
const mmprojInput = (page) => page.locator('input[placeholder*="fp16"]')
const modelTypeInput = (page) => page.locator('input[placeholder*="AutoModelForCausalLM"]')

test.describe('Import form UX — Batch D (progressive disclosure)', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
    await clearFormStorage(page)
  })

  test('D1 — default (backend unset) shows all three conditional fields', async ({ page }) => {
    await enterPowerPreferences(page)
    await expect(quantizationsInput(page)).toBeVisible()
    await expect(mmprojInput(page)).toBeVisible()
    await expect(modelTypeInput(page)).toBeVisible()
  })

  test('D1 — selecting llama-cpp shows Quantizations + MMProj, hides Model Type', async ({ page }) => {
    await enterPowerPreferences(page)
    await selectBackend(page, 'llama-cpp')
    await expect(quantizationsInput(page)).toBeVisible()
    await expect(mmprojInput(page)).toBeVisible()
    // Model Type is scoped to transformers / sentencetransformers / rerankers
    // / rfdetr — llama-cpp doesn't consume it.
    await expect(modelTypeInput(page)).toHaveCount(0)
  })

  test('D1 — selecting transformers hides Quantizations + MMProj, shows Model Type', async ({ page }) => {
    await enterPowerPreferences(page)
    await selectBackend(page, 'transformers')
    await expect(quantizationsInput(page)).toHaveCount(0)
    await expect(mmprojInput(page)).toHaveCount(0)
    await expect(modelTypeInput(page)).toBeVisible()
  })

  test('D1 — selecting sentencetransformers hides Quantizations + MMProj, shows Model Type', async ({ page }) => {
    await enterPowerPreferences(page)
    await selectBackend(page, 'sentencetransformers')
    await expect(quantizationsInput(page)).toHaveCount(0)
    await expect(mmprojInput(page)).toHaveCount(0)
    await expect(modelTypeInput(page)).toBeVisible()
  })

  test('D1 — selecting stablediffusion-ggml shows Quantizations, hides MMProj + Model Type', async ({ page }) => {
    await enterPowerPreferences(page)
    await selectBackend(page, 'stablediffusion-ggml')
    await expect(quantizationsInput(page)).toBeVisible()
    await expect(mmprojInput(page)).toHaveCount(0)
    await expect(modelTypeInput(page)).toHaveCount(0)
  })

  test('D1 — selecting piper hides all three conditional fields', async ({ page }) => {
    await enterPowerPreferences(page)
    await selectBackend(page, 'piper')
    await expect(quantizationsInput(page)).toHaveCount(0)
    await expect(mmprojInput(page)).toHaveCount(0)
    await expect(modelTypeInput(page)).toHaveCount(0)
  })

  test('D1 — quantization typed in default state is preserved through piper -> llama-cpp', async ({ page }) => {
    await enterPowerPreferences(page)
    // Type a quantization while backend is still unset.
    await quantizationsInput(page).fill('q5_k_m')
    await expect(quantizationsInput(page)).toHaveValue('q5_k_m')

    // Select piper — quantization field is hidden but state must survive.
    await selectBackend(page, 'piper')
    await expect(quantizationsInput(page)).toHaveCount(0)

    // Switch back to llama-cpp — the field should re-appear with the value.
    await selectBackend(page, 'llama-cpp')
    await expect(quantizationsInput(page)).toBeVisible()
    await expect(quantizationsInput(page)).toHaveValue('q5_k_m')
  })

  test('D2 — Description textarea uses rows=2', async ({ page }) => {
    await enterPowerPreferences(page)
    const textarea = page.locator('textarea[placeholder*="Leave empty to use default"]')
    await expect(textarea).toHaveAttribute('rows', '2')
  })
})
