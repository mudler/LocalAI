import { test, expect } from '@playwright/test'

const MOCK_MODELS_RESPONSE = {
  models: [
    { name: 'llama-model', description: 'A llama model', backend: 'llama-cpp', installed: false, tags: ['chat'] },
    { name: 'whisper-model', description: 'A whisper model', backend: 'whisper', installed: true, tags: ['transcript'] },
    { name: 'stablediffusion-model', description: 'An image model', backend: 'stablediffusion', installed: false, tags: ['sd'] },
    { name: 'unknown-model', description: 'No backend', backend: '', installed: false, tags: [] },
  ],
  allBackends: ['llama-cpp', 'stablediffusion', 'whisper'],
  allTags: ['chat', 'sd', 'transcript'],
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

const BACKEND_USECASES_MOCK = {
  'llama-cpp': ['chat', 'embeddings', 'vision'],
  'whisper': ['transcript'],
  'stablediffusion': ['image'],
}

test.describe('Models Gallery - Multi-select Filters', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/models*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      })
    })
    await page.route('**/api/backends/usecases', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(BACKEND_USECASES_MOCK),
      })
    })
    await page.goto('/app/models')
    await expect(page.locator('th', { hasText: 'Backend' })).toBeVisible({ timeout: 10_000 })
  })

  test('multi-select toggle: click Chat, TTS, then Chat again', async ({ page }) => {
    const chatBtn = page.locator('.filter-btn', { hasText: 'Chat' })
    const ttsBtn = page.locator('.filter-btn', { hasText: 'TTS' })

    await chatBtn.click()
    await expect(chatBtn).toHaveClass(/active/)

    await ttsBtn.click()
    await expect(chatBtn).toHaveClass(/active/)
    await expect(ttsBtn).toHaveClass(/active/)

    // Click Chat again to deselect it
    await chatBtn.click()
    await expect(chatBtn).not.toHaveClass(/active/)
    await expect(ttsBtn).toHaveClass(/active/)
  })

  test('"All" clears selection', async ({ page }) => {
    const chatBtn = page.locator('.filter-btn', { hasText: 'Chat' })
    const allBtn = page.locator('.filter-btn', { hasText: 'All' })

    await chatBtn.click()
    await expect(chatBtn).toHaveClass(/active/)

    await allBtn.click()
    await expect(allBtn).toHaveClass(/active/)
    await expect(chatBtn).not.toHaveClass(/active/)
  })

  test('query param sent correctly with multiple filters', async ({ page }) => {
    const chatBtn = page.locator('.filter-btn', { hasText: 'Chat' })
    const ttsBtn = page.locator('.filter-btn', { hasText: 'TTS' })

    // Click Chat and wait for its request to settle
    await chatBtn.click()
    await page.waitForResponse(resp => resp.url().includes('/api/models'))

    // Now click TTS and capture the resulting request
    const [request] = await Promise.all([
      page.waitForRequest(req => {
        if (!req.url().includes('/api/models')) return false
        const u = new URL(req.url())
        const tag = u.searchParams.get('tag')
        return tag && tag.split(',').length >= 2
      }),
      ttsBtn.click(),
    ])

    const url = new URL(request.url())
    const tags = url.searchParams.get('tag').split(',').sort()
    expect(tags).toEqual(['chat', 'tts'])
  })

  test('backend greys out unavailable filters', async ({ page }) => {
    // Select llama-cpp backend via dropdown
    await page.locator('button', { hasText: 'All Backends' }).click()
    const dropdown = page.locator('input[placeholder="Search backends..."]').locator('..').locator('..')
    await dropdown.locator('text=llama-cpp').click()

    // Wait for filter state to update
    const ttsBtn = page.locator('.filter-btn', { hasText: 'TTS' })
    const sttBtn = page.locator('.filter-btn', { hasText: 'STT' })
    const imageBtn = page.locator('.filter-btn', { hasText: 'Image' })

    // TTS, STT, Image should be disabled for llama-cpp
    await expect(ttsBtn).toBeDisabled()
    await expect(sttBtn).toBeDisabled()
    await expect(imageBtn).toBeDisabled()

    // Chat, Embeddings, Vision should remain enabled
    const chatBtn = page.locator('.filter-btn', { hasText: 'Chat' })
    const embBtn = page.locator('.filter-btn', { hasText: 'Embeddings' })
    const visBtn = page.locator('.filter-btn', { hasText: 'Vision' })
    await expect(chatBtn).toBeEnabled()
    await expect(embBtn).toBeEnabled()
    await expect(visBtn).toBeEnabled()
  })

  test('backend clears incompatible filters', async ({ page }) => {
    // Select TTS filter first
    const ttsBtn = page.locator('.filter-btn', { hasText: 'TTS' })
    await ttsBtn.click()
    await expect(ttsBtn).toHaveClass(/active/)

    // Now select llama-cpp backend (which doesn't support TTS)
    await page.locator('button', { hasText: 'All Backends' }).click()
    const dropdown = page.locator('input[placeholder="Search backends..."]').locator('..').locator('..')
    await dropdown.locator('text=llama-cpp').click()

    // TTS should be auto-removed from selection
    await expect(ttsBtn).not.toHaveClass(/active/)
  })
})
