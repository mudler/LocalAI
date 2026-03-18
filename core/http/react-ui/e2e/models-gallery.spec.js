import { test, expect } from '@playwright/test'

const MOCK_MODELS_RESPONSE = {
  models: [
    { name: 'llama-model', description: 'A llama model', backend: 'llama-cpp', installed: false, tags: ['llm'] },
    { name: 'whisper-model', description: 'A whisper model', backend: 'whisper', installed: true, tags: ['stt'] },
    { name: 'stablediffusion-model', description: 'An image model', backend: 'stablediffusion', installed: false, tags: ['sd'] },
    { name: 'unknown-model', description: 'No backend', backend: '', installed: false, tags: [] },
  ],
  allBackends: ['llama-cpp', 'stablediffusion', 'whisper'],
  allTags: ['llm', 'sd', 'stt'],
  availableModels: 4,
  installedModels: 1,
  totalPages: 1,
  currentPage: 1,
}

test.describe('Models Gallery - Backend Features', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/models*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      })
    })
    await page.goto('/app/models')
    // Wait for the table to render
    await expect(page.locator('th', { hasText: 'Backend' })).toBeVisible({ timeout: 10_000 })
  })

  test('backend column header is visible', async ({ page }) => {
    await expect(page.locator('th', { hasText: 'Backend' })).toBeVisible()
  })

  test('backend badges shown in table rows', async ({ page }) => {
    const table = page.locator('table')
    await expect(table.locator('.badge', { hasText: 'llama-cpp' })).toBeVisible()
    await expect(table.locator('.badge', { hasText: /^whisper$/ })).toBeVisible()
  })

  test('backend dropdown is visible', async ({ page }) => {
    await expect(page.locator('button', { hasText: 'All Backends' })).toBeVisible()
  })

  test('clicking backend dropdown opens searchable panel', async ({ page }) => {
    await page.locator('button', { hasText: 'All Backends' }).click()
    await expect(page.locator('input[placeholder="Search backends..."]')).toBeVisible()
  })

  test('typing in search filters dropdown options', async ({ page }) => {
    await page.locator('button', { hasText: 'All Backends' }).click()
    const searchInput = page.locator('input[placeholder="Search backends..."]')
    await searchInput.fill('llama')

    // llama-cpp option should be visible, whisper should not
    const dropdown = page.locator('input[placeholder="Search backends..."]').locator('..')  .locator('..')
    await expect(dropdown.locator('text=llama-cpp')).toBeVisible()
    await expect(dropdown.locator('text=whisper')).not.toBeVisible()
  })

  test('selecting a backend updates the dropdown label', async ({ page }) => {
    await page.locator('button', { hasText: 'All Backends' }).click()
    // Click the llama-cpp option within the dropdown (not the table badge)
    const dropdown = page.locator('input[placeholder="Search backends..."]').locator('..').locator('..')
    await dropdown.locator('text=llama-cpp').click()

    // The dropdown button should now show the selected backend instead of "All Backends"
    await expect(page.locator('button span', { hasText: 'llama-cpp' })).toBeVisible()
  })

  test('expanded row shows backend in detail', async ({ page }) => {
    // Click the first model row to expand it
    await page.locator('tr', { hasText: 'llama-model' }).click()

    // The detail view should show Backend label and value
    const detail = page.locator('td[colspan="8"]')
    await expect(detail.locator('text=Backend')).toBeVisible()
    await expect(detail.locator('text=llama-cpp')).toBeVisible()
  })
})
