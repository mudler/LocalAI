import { test, expect } from '@playwright/test'

const MOCK_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'parameters', label: 'Parameters', icon: 'sliders', order: 20 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', description: 'Unique identifier for this model', component: 'input', order: 0 },
    { path: 'backend', yaml_key: 'backend', go_type: 'string', ui_type: 'string', section: 'general', label: 'Backend', description: 'Inference backend to use', component: 'select', autocomplete_provider: 'backends', order: 10 },
    { path: 'context_size', yaml_key: 'context_size', go_type: '*int', ui_type: 'int', section: 'general', label: 'Context Size', description: 'Maximum context window in tokens', component: 'number', vram_impact: true, order: 20 },
    { path: 'cuda', yaml_key: 'cuda', go_type: 'bool', ui_type: 'bool', section: 'general', label: 'CUDA', description: 'Enable CUDA GPU acceleration', component: 'toggle', order: 30 },
    { path: 'parameters.temperature', yaml_key: 'temperature', go_type: '*float64', ui_type: 'float', section: 'parameters', label: 'Temperature', description: 'Sampling temperature', component: 'slider', min: 0, max: 2, step: 0.1, order: 0 },
    { path: 'parameters.top_p', yaml_key: 'top_p', go_type: '*float64', ui_type: 'float', section: 'parameters', label: 'Top P', description: 'Nucleus sampling threshold', component: 'slider', min: 0, max: 1, step: 0.05, order: 10 },
  ],
}

// Mock raw YAML (what the edit endpoint returns) — only fields actually in the file
const MOCK_YAML = `name: mock-model
backend: mock-backend
parameters:
  model: mock-model.bin
`

const MOCK_AUTOCOMPLETE_BACKENDS = { values: ['mock-backend', 'llama-cpp', 'vllm'] }

test.describe('Model Editor - Interactive Tab', () => {
  test.beforeEach(async ({ page }) => {
    // Mock config metadata
    await page.route('**/api/models/config-metadata*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_METADATA),
      })
    })

    // Mock raw YAML edit endpoint (GET for loading, POST for saving)
    await page.route('**/api/models/edit/mock-model', (route) => {
      if (route.request().method() === 'POST') {
        route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ message: 'Configuration file saved' }),
        })
      } else {
        route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ config: MOCK_YAML, name: 'mock-model' }),
        })
      }
    })

    // Mock PATCH config-json for interactive save
    await page.route('**/api/models/config-json/mock-model', (route) => {
      if (route.request().method() === 'PATCH') {
        route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ success: true, message: "Model 'mock-model' updated successfully" }),
        })
      } else {
        route.fulfill({ contentType: 'application/json', body: '{}' })
      }
    })

    // Mock autocomplete for backends
    await page.route('**/api/models/config-metadata/autocomplete/backends', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_AUTOCOMPLETE_BACKENDS),
      })
    })

    await page.goto('/app/model-editor/mock-model')
    // Wait for the page to load
    await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 10_000 })
  })

  test('page loads and shows model name in header', async ({ page }) => {
    await expect(page.locator('text=mock-model')).toBeVisible()
    await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible()
  })

  test('interactive tab is active by default', async ({ page }) => {
    // The field browser should be visible (interactive tab content)
    await expect(page.locator('input[placeholder="Search fields to add..."]')).toBeVisible()
  })

  test('existing config fields from YAML are populated', async ({ page }) => {
    // The mock YAML has name and backend — they should be active fields
    await expect(page.locator('text=Model Name')).toBeVisible()
    await expect(page.locator('span', { hasText: /^Backend$/ }).first()).toBeVisible()
  })

  test('section sidebar shows sections with active fields', async ({ page }) => {
    const sidebar = page.locator('nav')
    await expect(sidebar.locator('text=General')).toBeVisible()
  })

  test('typing in field browser shows matching fields', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Temperature')
    await expect(page.locator('text=Temperature').first()).toBeVisible()
  })

  test('clicking a field result adds it to the config', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Temperature')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Temperature' }).first().click()
    await expect(page.locator('h3', { hasText: 'Parameters' })).toBeVisible()
  })

  test('toggle field renders a toggle switch', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('CUDA')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'CUDA' }).first().click()
    await expect(page.locator('text=CUDA').first()).toBeVisible()
    const cudaSection = page.locator('div', { has: page.locator('span', { hasText: /^CUDA$/ }) }).first()
    await expect(cudaSection.locator('input[type="checkbox"]')).toHaveCount(1)
  })

  test('number field renders a numeric input', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Context Size')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Context Size' }).first().click()
    await expect(page.locator('input[type="number"]')).toBeVisible()
  })

  test('changing a field value enables the Save button', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Context Size')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Context Size' }).first().click()
    const numberInput = page.locator('input[type="number"]')
    await numberInput.fill('4096')
    await expect(page.locator('button', { hasText: 'Save Changes' })).toBeVisible()
  })

  test('removing a field with X button removes it from the form', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Temperature')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Temperature' }).first().click()
    const paramsHeader = page.locator('h3', { hasText: 'Parameters' })
    await expect(paramsHeader).toBeVisible()
    const paramsSection = paramsHeader.locator('..')
    await paramsSection.locator('button[title="Remove field"]').first().click()
    await expect(paramsHeader).not.toBeVisible()
  })

  test('save sends PATCH and shows success toast', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Context Size')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Context Size' }).first().click()
    const numberInput = page.locator('input[type="number"]')
    await numberInput.fill('8192')
    await page.locator('button', { hasText: 'Save Changes' }).click()
    await expect(page.locator('text=Configuration saved')).toBeVisible({ timeout: 5_000 })
  })

  test('added field is no longer shown in field browser results', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="Search fields to add..."]')
    await searchInput.fill('Temperature')
    const dropdown = searchInput.locator('..').locator('..')
    await dropdown.locator('div', { hasText: 'Temperature' }).first().click()
    await searchInput.fill('Temperature')
    await page.waitForTimeout(200)
    const results = dropdown.locator('div[style*="cursor: pointer"]', { hasText: 'Temperature' })
    await expect(results).toHaveCount(0)
  })

  test('switching to YAML tab shows code editor', async ({ page }) => {
    await page.locator('button', { hasText: 'YAML' }).click()
    // The CodeMirror editor should be visible
    await expect(page.locator('.cm-editor').first()).toBeVisible()
    // The field browser should NOT be visible
    await expect(page.locator('input[placeholder="Search fields to add..."]')).not.toBeVisible()
  })

  test('switching back to Interactive tab restores fields', async ({ page }) => {
    // Go to YAML tab
    await page.locator('button', { hasText: 'YAML' }).click()
    await expect(page.locator('input[placeholder="Search fields to add..."]')).not.toBeVisible()
    // Go back to Interactive tab
    await page.locator('button', { hasText: 'Interactive' }).click()
    await expect(page.locator('input[placeholder="Search fields to add..."]')).toBeVisible()
    await expect(page.locator('text=Model Name')).toBeVisible()
  })
})
