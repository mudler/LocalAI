import { test, expect } from './coverage-fixtures.js'

// Layout and hierarchy of the import form. The page was left on the narrow
// archetype after the style transfer, which gave a form holding a URI field, a
// six-section format guide, ten modality chips and nine preference fields a
// single 760px column. These tests pin the shape it should have instead:
// medium width, a two-column split whose second column is the format
// reference, and a primary action that lives inside the form it submits.

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

test.describe('Import form — page width', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
  })

  test('the page uses the medium archetype, not narrow', async ({ page }) => {
    await page.goto('/app/import-model')
    const shell = page.locator('.import-page')
    await expect(shell).toBeVisible({ timeout: 15_000 })
    await expect(shell).toHaveClass(/page--medium/)
    await expect(shell).not.toHaveClass(/page--narrow/)

    // --page-max-medium is 1080px. Asserting the resolved cap rather than the
    // class alone means renaming the token cannot silently re-narrow the page.
    const maxWidth = await shell.evaluate((el) => getComputedStyle(el).maxWidth)
    expect(parseFloat(maxWidth)).toBeGreaterThanOrEqual(1000)
  })
})

test.describe('Import form — the source field owns its action', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
  })

  test('Import is a styled primary button, not a bare user-agent button', async ({ page }) => {
    await page.goto('/app/import-model')
    const submit = page.locator('[data-testid="import-submit"]')
    await expect(submit).toBeVisible({ timeout: 15_000 })
    await expect(submit).toHaveClass(/btn/)
    await expect(submit).toHaveClass(/btn-primary/)

    // The regression this replaces was a <button> with no class at all, which
    // renders in the user-agent's own chrome. Checking the computed background
    // catches that even if the class list is later restored by accident.
    const background = await submit.evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(background).not.toBe('rgba(0, 0, 0, 0)')
    expect(background).not.toBe('transparent')
    // buttonface is what a UA-styled button computes to.
    expect(background.toLowerCase()).not.toContain('buttonface')
  })

  test('no control on the page sets Font Awesome as its own font family', async ({ page }) => {
    await page.goto('/app/import-model')
    await expect(page.locator('[data-testid="import-submit"]')).toBeVisible({ timeout: 15_000 })

    // `class="btn btn-primary fas fa-save"` puts the icon font on the button
    // itself, so its label inherits it. Icons belong in a child <i>.
    const offenders = await page.locator('main button').evaluateAll((buttons) =>
      buttons
        .filter((el) => getComputedStyle(el).fontFamily.toLowerCase().includes('font awesome'))
        .map((el) => el.className)
    )
    expect(offenders).toEqual([])
  })

  test('Import sits inside the form, so Enter submits without a hidden button', async ({ page }) => {
    await page.goto('/app/import-model')
    const submit = page.locator('[data-testid="import-submit"]')
    await expect(submit).toBeVisible({ timeout: 15_000 })
    await expect(submit).toHaveAttribute('type', 'submit')

    // The old layout put Import in the page header, outside the <form>, and
    // added an aria-hidden submit button so Enter still worked. That crutch
    // must be gone.
    await expect(page.locator('form button[aria-hidden="true"]')).toHaveCount(0)

    const form = page.locator('[data-testid="import-form"]')
    await expect(form.locator('[data-testid="import-submit"]')).toHaveCount(1)
  })

  test('Import is disabled until a source is entered', async ({ page }) => {
    await page.goto('/app/import-model')
    const submit = page.locator('[data-testid="import-submit"]')
    await expect(submit).toBeDisabled()
    await page.locator('[data-testid="import-source-input"]').fill('hf://Example/Model')
    await expect(submit).toBeEnabled()
  })
})

test.describe('Import form — the format reference', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
  })

  test('is visible without interaction on a wide viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/app/import-model')
    const formats = page.locator('[data-testid="import-formats"]')
    await expect(formats).toBeVisible({ timeout: 15_000 })
    // Every scheme the importer accepts, answered before it is asked.
    await expect(formats).toContainText('huggingface://')
    await expect(formats).toContainText('oci://')
    await expect(formats).toContainText('ollama://')
    await expect(formats).toContainText('file://')
  })

  test('collapses behind a disclosure on a narrow viewport', async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 900 })
    await page.goto('/app/import-model')
    const toggle = page.locator('[data-testid="import-formats-toggle"]')
    await expect(toggle).toBeVisible({ timeout: 15_000 })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')

    const formats = page.locator('[data-testid="import-formats"]')
    await expect(formats).not.toBeVisible()
    await toggle.click()
    await expect(formats).toBeVisible()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })
})

test.describe('Import form — the estimate reports next to the field', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
  })

  test('the size estimate renders below the source field, not above the header', async ({ page }) => {
    await page.route('**/models/import-uri', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          uuid: 'job-estimate',
          ID: 'job-estimate',
          estimated_size_display: '4.08 GB',
          estimated_vram_display: '5.21 GB',
        }),
      })
    })
    // Hold the job open so the page stays on the in-flight state.
    await page.route('**/models/jobs/**', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ processed: false, message: 'downloading', progress: 12 }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-source-input"]').fill('hf://TheBloke/Llama-2-7B-Chat-GGUF')
    await page.locator('[data-testid="import-submit"]').click()

    const estimate = page.locator('[data-testid="import-estimate"]')
    await expect(estimate).toBeVisible({ timeout: 5_000 })
    await expect(estimate).toContainText('4.08 GB')
    await expect(estimate).toContainText('5.21 GB')

    // Ordering is the point: the estimate answers for the field above it.
    const fieldBox = await page.locator('[data-testid="import-source-input"]').boundingBox()
    const estimateBox = await estimate.boundingBox()
    expect(estimateBox.y).toBeGreaterThan(fieldBox.y)

    const headerBox = await page.locator('.page-title').first().boundingBox()
    expect(estimateBox.y).toBeGreaterThan(headerBox.y)
  })
})

test.describe('Import form — an import in flight', () => {
  test.beforeEach(async ({ page }) => {
    await mockBackends(page)
  })

  test('renders a progress bar with a percentage, not a bare message', async ({ page }) => {
    await page.route('**/models/import-uri', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ uuid: 'job-progress', ID: 'job-progress' }),
      })
    })
    await page.route('**/models/jobs/**', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          processed: false,
          message: 'downloading',
          progress: 37.5,
          file_name: 'llama-2-7b-chat.Q4_K_M.gguf',
          downloaded_size: '1.5 GB',
          file_size: '4.08 GB',
        }),
      })
    })

    await page.goto('/app/import-model')
    await page.locator('[data-testid="import-source-input"]').fill('hf://TheBloke/Llama-2-7B-Chat-GGUF')
    await page.locator('[data-testid="import-submit"]').click()

    const progress = page.locator('[data-testid="import-progress"]')
    await expect(progress).toBeVisible({ timeout: 5_000 })

    const bar = progress.locator('[role="progressbar"]')
    await expect(bar).toHaveAttribute('aria-valuenow', '38')
    await expect(bar).toHaveAttribute('aria-valuemin', '0')
    await expect(bar).toHaveAttribute('aria-valuemax', '100')

    // The bytes the poller already returns and the old card threw away.
    await expect(progress).toContainText('1.5 GB')
    await expect(progress).toContainText('4.08 GB')
    await expect(progress).toContainText('llama-2-7b-chat.Q4_K_M.gguf')
  })
})
