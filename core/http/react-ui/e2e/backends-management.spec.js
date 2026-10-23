import { test, expect } from './coverage-fixtures.js'

// Backends admin page (src/pages/Backends.jsx): one list with an Installed and
// a Catalog view. A row is a name, a version, a state and the one action; it
// opens in place into the rest.
const row = (page, name) => page.locator(`[data-entity="${name}"]`)

test.describe('Backends management page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/app/backends')
  })

  test('renders the management header and gallery tabs', async ({ page }) => {
    await expect(page).toHaveURL(/\/app\/backends$/)
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'From URL' })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^All$/ })).toBeVisible()
    await expect(page.getByRole('button').filter({ hasText: /^Image$/ })).toBeVisible()
  })

  test('search field accepts input', async ({ page }) => {
    const search = page.getByPlaceholder(/search backends/i)
    await expect(search).toBeVisible()
    await search.fill('whisper')
    await expect(search).toHaveValue('whisper')
  })

  test('From URL reveals the OCI install form', async ({ page }) => {
    await page.getByRole('button', { name: 'From URL' }).click()
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
    await expect(row(page, 'markdown-backend')).toBeVisible({ timeout: 10_000 })
  })

  test('the row shows the description as clean text, not raw Markdown', async ({ page }) => {
    const cell = row(page, 'markdown-backend').locator('.dk-table-sub')

    await expect(cell).toHaveText(STRIPPED_DESCRIPTION)
    // The syntax itself must be gone, not merely rendered somewhere.
    await expect(cell).not.toContainText('#')
    await expect(cell).not.toContainText('`')
    await expect(cell).not.toContainText('**')
    await expect(cell).not.toContainText('https://example.com/docs')
    // A block element here would blow up the row height.
    await expect(cell.locator('h1')).toHaveCount(0)
  })

  test("the row's tooltip carries the stripped text, not raw Markdown", async ({ page }) => {
    await expect(row(page, 'markdown-backend').locator('.dk-table-sub')).toHaveAttribute('title', STRIPPED_DESCRIPTION)
  })

  test('opening the row renders the Markdown, with its link', async ({ page }) => {
    await row(page, 'markdown-backend').click()
    const detail = page.getByTestId('backend-detail')
    await expect(detail.locator('.bk-detail__desc h1')).toHaveText('InsightFace')
    await expect(detail.locator('.bk-detail__desc a[href="https://example.com/docs"]')).toBeVisible()
  })

  test('a backend with no description renders no blank line and never "undefined"', async ({ page }) => {
    await expect(row(page, 'plain-backend')).toContainText('plain-backend')
    await expect(row(page, 'plain-backend')).not.toContainText('undefined')
    await row(page, 'plain-backend').click()
    await expect(page.getByTestId('backend-detail')).not.toContainText('undefined')
    await expect(page.locator('.bk-detail__desc')).toHaveCount(0)
  })
})

test.describe('Backends gallery - list', () => {
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
    await expect(row(page, 'llama-cpp')).toBeVisible({ timeout: 10_000 })
  })

  test('is a table with columns, not a rail and a pane', async ({ page }) => {
    await expect(page.locator('[data-testid="backends"]')).toBeVisible()
    await expect(page.locator('table thead th').first()).toBeVisible()
    await expect(page.locator('[data-testid="backends-pane"]')).toHaveCount(0)
  })

  test('opening a row shows its facts, and the chevron closes it', async ({ page }) => {
    await row(page, 'llama-cpp').click()
    const detail = page.getByTestId('backend-detail')
    await expect(detail).toContainText('MIT')
    await expect(detail).toContainText('chat')
    await row(page, 'llama-cpp').getByRole('button', { name: /Hide details for llama-cpp/ }).click()
    await expect(page.getByTestId('backend-detail')).toHaveCount(0)
  })

  test('only one row is open at a time', async ({ page }) => {
    await row(page, 'llama-cpp').click()
    await row(page, 'whisper').click()
    await expect(page.getByTestId('backend-detail')).toHaveCount(1)
    await expect(page.getByTestId('backend-detail')).toContainText('transcript')
    await expect(page).toHaveURL(/[?&]backend=whisper/)
  })

  test('the open row lives in the URL and survives a reload', async ({ page }) => {
    await row(page, 'whisper').click()
    await expect(page).toHaveURL(/[?&]backend=whisper/)
    await page.reload()
    await expect(row(page, 'whisper')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('backend-detail')).toContainText('transcript')
  })

  test('a filter chip narrows the list', async ({ page }) => {
    await page.getByRole('button', { name: 'Image', exact: true }).click()
    await expect(page).toHaveURL(/[?&]state=image/)
    await expect(row(page, 'diffusers')).toBeVisible()
    await expect(row(page, 'llama-cpp')).toHaveCount(0)
  })

  test('an installed backend states its version, an absent one says so', async ({ page }) => {
    await expect(row(page, 'llama-cpp')).toContainText('v1.52.0')
    await expect(row(page, 'llama-cpp')).toContainText('Current')
    await expect(row(page, 'diffusers')).toContainText('Not installed')
  })
})
