import { test, expect } from './coverage-fixtures.js'

// Backends admin page (src/pages/Backends.jsx).
const PANE = '[data-testid="backends-pane"]'
const railItem = (page, name) => page.locator(`[data-entity="${name}"]`)

test.describe('Backends management page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/backends')
  })

  test('renders the management header and gallery tabs', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/backends$/)
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Manual Install' })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^All$/ })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^Image$/ })).toBeVisible()
  })

  test('search field accepts input', async ({ page }) => {
    const search = page.getByPlaceholder(/search backends/i)
    await expect(search).toBeVisible()
    await search.fill('whisper')
    await expect(search).toHaveValue('whisper')
  })

  test('Manual Install reveals the OCI install form', async ({ page }) => {
    await page.getByRole('button', { name: 'Manual Install' }).click()
    await expect(page.getByPlaceholder('oci://quay.io/example/backend:latest')).toBeVisible()
  })
})

// Backend gallery descriptions are Markdown too: 40 of the entries in
// backend/index.yaml carry headings, inline code, lists or links, and they used
// to be dumped raw into the truncated table cell.
const MARKDOWN_DESCRIPTION =
  '# InsightFace\n\nUse `insightface` for face analysis. See [the docs](https://example.com/docs) for **details**.'
const STRIPPED_DESCRIPTION =
  'InsightFace Use insightface for face analysis. See the docs for details.'

test.describe('Backends management page - Markdown descriptions', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/backends*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          backends: [
            { name: 'markdown-backend', description: MARKDOWN_DESCRIPTION, installed: false },
            { name: 'plain-backend', description: '', installed: false },
          ],
        }),
      })
    })
    await page.goto('/app/backends')
    // Rendered means the rail has entries. The old gate waited on a column
    // header, and there are no columns now.
    await expect(railItem(page, 'markdown-backend')).toBeVisible({ timeout: 10_000 })
  })

  test('the pane lede shows the description as clean text, not raw Markdown', async ({ page }) => {
    await railItem(page, 'markdown-backend').click()
    const cell = page.locator('.detail-pane__lede')

    await expect(cell).toHaveText(STRIPPED_DESCRIPTION)
    // The syntax itself must be gone, not merely rendered somewhere.
    await expect(cell).not.toContainText('#')
    await expect(cell).not.toContainText('`')
    await expect(cell).not.toContainText('**')
    await expect(cell).not.toContainText('https://example.com/docs')
    // A block element here would blow up the row height.
    await expect(cell.locator('h1')).toHaveCount(0)
  })

  test("the lede's tooltip carries the stripped text, not raw Markdown", async ({ page }) => {
    await railItem(page, 'markdown-backend').click()
    await expect(page.locator('.detail-pane__lede')).toHaveAttribute('title', STRIPPED_DESCRIPTION)
  })

  test('a backend with no description renders no lede rather than a blank one', async ({ page }) => {
    // The table needed a placeholder because an empty cell in a grid of full
    // ones reads as a fault. The pane has no grid to keep aligned, so it omits
    // the line - but must never print "undefined".
    await railItem(page, 'plain-backend').click()
    await expect(page.locator(PANE)).toContainText('plain-backend')
    await expect(page.locator('.detail-pane__lede')).toHaveCount(0)
    await expect(page.locator(PANE)).not.toContainText('undefined')
  })
})

test.describe('Backends gallery - split view', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/backends*', (route) => {
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          backends: [
            { name: 'llama-cpp', description: 'GGUF inference', installed: true, version: '1.52.0', license: 'MIT', tags: ['chat'] },
            { name: 'whisper', description: 'Speech to text', installed: true, version: '1.8.2', license: 'MIT', tags: ['transcript'] },
            { name: 'diffusers', description: 'Image generation', installed: false, license: 'Apache-2.0', tags: ['image'] },
          ],
        }),
      })
    })
    await page.goto('/app/backends')
    await expect(railItem(page, 'llama-cpp')).toBeVisible({ timeout: 10_000 })
  })

  test('the gallery renders no table', async ({ page }) => {
    await expect(page.locator('[data-testid="backends"]')).toBeVisible()
    await expect(page.locator('table thead th')).toHaveCount(0)
  })

  test('with nothing selected the pane describes the host', async ({ page }) => {
    await expect(page.locator(PANE)).toContainText('This host')
    await expect(page.locator('[data-testid="backends-back"]')).toHaveCount(0)
  })

  test('choosing a backend turns the pane into its detail, and back returns', async ({ page }) => {
    await railItem(page, 'llama-cpp').click()
    await expect(page.locator(PANE)).toContainText('llama-cpp')
    await expect(page.locator(PANE)).toContainText('MIT')
    await expect(page.locator(PANE)).not.toContainText('This host')

    await page.locator('[data-testid="backends-back"]').click()
    await expect(page.locator(PANE)).toContainText('This host')
  })

  test('the selection lives in the URL and survives a reload', async ({ page }) => {
    await railItem(page, 'whisper').click()
    await expect(page).toHaveURL(/[?&]backend=whisper/)
    await page.reload()
    await expect(railItem(page, 'whisper')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('[data-testid="backends-back"]')).toBeVisible()
  })


  test('the rail groups while browsing and flattens on a query', async ({ page }) => {
    await expect(page.locator('[data-testid^="backends-rail-group-"]').first()).toBeVisible()
    await page.locator('input[placeholder*="Search backends"]').fill('llama')
    await expect(page.locator('[data-testid^="backends-rail-group-"]')).toHaveCount(0)
  })

  test('an installed backend states its version, an absent one says so', async ({ page }) => {
    await expect(railItem(page, 'llama-cpp')).toContainText('v1.52.0')
    await expect(railItem(page, 'diffusers')).toContainText('not installed')
  })
})
