import { test, expect } from './coverage-fixtures.js'

// Backends admin page (src/pages/Backends.jsx).
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
    await expect(page.locator('th', { hasText: 'Description' })).toBeVisible({ timeout: 10_000 })
  })

  test('table cell shows the description as clean text, not raw Markdown', async ({ page }) => {
    const cell = page.locator('tr', { hasText: 'markdown-backend' }).locator('span[title]', { hasText: 'InsightFace' })

    await expect(cell).toHaveText(STRIPPED_DESCRIPTION)
    // The syntax itself must be gone, not merely rendered somewhere.
    await expect(cell).not.toContainText('#')
    await expect(cell).not.toContainText('`')
    await expect(cell).not.toContainText('**')
    await expect(cell).not.toContainText('https://example.com/docs')
    // A block element here would blow up the row height.
    await expect(cell.locator('h1')).toHaveCount(0)
  })

  test('title tooltip carries the stripped text, not raw Markdown', async ({ page }) => {
    const cell = page.locator('tr', { hasText: 'markdown-backend' }).locator('span[title]', { hasText: 'InsightFace' })

    await expect(cell).toHaveAttribute('title', STRIPPED_DESCRIPTION)
  })

  test('a backend with no description still shows the placeholder', async ({ page }) => {
    const row = page.locator('tr', { hasText: 'plain-backend' })

    await expect(row.locator('span[title=""]')).toHaveText('-')
  })
})
