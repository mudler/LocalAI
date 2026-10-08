import fs from 'node:fs'
import { test, expect } from './coverage-fixtures.js'
import { mockTraffic, sourcesPayload, usagePayload } from './traffic-fixtures.js'

async function authAs(page, role) {
  const user = { id: 'alice-uuid', name: 'Alice', role, provider: 'local' }
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], user } }))
  await page.route('**/api/auth/me', route => route.fulfill({ json: { user, permissions: {} } }))
  await page.route('**/api/auth/quota', route => route.fulfill({ json: { quotas: [] } }))
}

test.describe('Usage', () => {
  test.beforeEach(async ({ page }) => { await mockTraffic(page) })

  test('groups by model first, with the chart, a sortable table and the source named', async ({ page }) => {
    await page.goto('/app/usage')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Usage, last 24 hours')
    await expect(page.getByRole('radio', { name: 'Model' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.locator('.tf-source')).toContainText('does not record status, endpoint or node')
    await expect(page.getByTestId('usage-chart')).toBeVisible()
    const rows = page.getByTestId('usage-table').locator('tbody tr[data-row]')
    await expect(rows).toHaveCount(6)
    await expect(rows.first()).toContainText('qwen3-8b-instruct')
    // Endpoint and node are not groups: the ledger does not hold them.
    await expect(page.getByRole('radio', { name: /Endpoint|Node/ })).toHaveCount(0)
  })

  test('sorts a column both ways', async ({ page }) => {
    await page.goto('/app/usage')
    const names = () => page.getByTestId('usage-table').locator('tbody tr[data-row] .dk-table-name').allTextContents()
    await page.getByRole('button', { name: 'Model', exact: true }).last().click()
    await expect(page.getByTestId('usage-table').locator('th[aria-sort]')).toHaveAttribute('aria-sort', 'descending')
    const desc = await names()
    await page.getByRole('button', { name: 'Model', exact: true }).last().click()
    const asc = await names()
    expect(asc).toEqual([...desc].reverse())
    expect(asc).toEqual([...asc].sort())
  })

  test('groups by user, filters by model and searches', async ({ page }) => {
    await page.goto('/app/usage')
    await page.getByRole('radio', { name: 'User' }).click()
    const rows = page.getByTestId('usage-table').locator('tbody tr[data-row]')
    await expect(rows).toHaveCount(3)
    await page.getByLabel('Search').fill('bo')
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText('bob')
    await page.getByLabel('Search').fill('zzz')
    await expect(page.getByTestId('usage-empty')).toContainText('Nothing matches')
    await page.getByLabel('Search').fill('')
    await page.locator('select.dk-select').selectOption('bge-m3')
    await expect(page.getByTestId('usage-summary')).toBeVisible()
    await expect(rows).toHaveCount(3)
  })

  test('a row opens in place on its own time series', async ({ page }) => {
    await page.goto('/app/usage')
    await page.getByRole('button', { name: 'Show details for bge-m3' }).click()
    await expect(page.getByTestId('row-chart')).toBeVisible()
    await page.getByRole('button', { name: 'Show details for bge-m3' }).click()
    await expect(page.getByTestId('row-chart')).toHaveCount(0)
  })

  test('compact density narrows the rows', async ({ page }) => {
    await page.goto('/app/usage')
    const table = page.getByTestId('usage-table').locator('table')
    await expect(table).not.toHaveClass(/dk-table--compact/)
    await page.getByRole('switch', { name: 'Compact' }).click()
    await expect(table).toHaveClass(/dk-table--compact/)
  })

  test('exports the rows it holds, as CSV and as JSON, in the browser', async ({ page }) => {
    await page.goto('/app/usage')
    const [csv] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'Export CSV' }).click()])
    expect(csv.suggestedFilename()).toMatch(/^usage-model-24h-.*\.csv$/)
    const text = fs.readFileSync(await csv.path(), 'utf8')
    const lines = text.trim().split('\n')
    expect(lines[0]).toBe('Model,Requests,Tokens in,Tokens out,Tokens')
    expect(lines).toHaveLength(7)
    expect(lines[1]).toMatch(/^qwen3-8b-instruct,/)
    const [json] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'Export JSON' }).click()])
    const rows = JSON.parse(fs.readFileSync(await json.path(), 'utf8'))
    expect(rows).toHaveLength(6)
    expect(rows[0].Model).toBe('qwen3-8b-instruct')
  })

  test('estimated cost is opt in, local, and adds a column and the export field', async ({ page }) => {
    await page.goto('/app/usage')
    await expect(page.getByRole('columnheader', { name: 'Est. cost' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Set pricing' }).click()
    await page.getByLabel('Prompt $ per 1M tokens').fill('1')
    await expect(page.getByRole('columnheader', { name: 'Est. cost' })).toBeVisible()
    await expect(page.getByTestId('pricing-panel')).toContainText('stay in this browser')
    await page.getByRole('button', { name: 'Clear' }).click()
    await expect(page.getByRole('columnheader', { name: 'Est. cost' })).toHaveCount(0)
  })

  test('with no usage it says so and names the next step', async ({ page }) => {
    await mockTraffic(page, { scenario: 'empty' })
    await page.goto('/app/usage')
    await expect(page.getByTestId('usage-empty')).toContainText('No usage yet')
    await expect(page.getByTestId('usage-empty')).toContainText('as API requests are made')
  })

  test('shows a loading state, then the rows', async ({ page }) => {
    await mockTraffic(page, { delayMs: 700 })
    await page.goto('/app/usage')
    await expect(page.getByTestId('usage-loading')).toBeVisible()
    await expect(page.getByTestId('usage-table')).toBeVisible()
  })
})

test.describe('Usage with auth on', () => {
  test('an admin can group by API key, with the owner named and no prompt split invented', async ({ page }) => {
    await mockTraffic(page)
    await authAs(page, 'admin')
    await page.route('**/api/auth/admin/usage?*', route => route.fulfill({ json: usagePayload('day') }))
    await page.route('**/api/auth/admin/usage/sources?*', route => route.fulfill({ json: sourcesPayload('day') }))
    await page.goto('/app/usage')
    await page.getByRole('radio', { name: 'API key' }).click()
    const rows = page.getByTestId('usage-table').locator('tbody tr[data-row]')
    await expect(rows.first()).toContainText('ci-bot')
    await expect(rows.first()).toContainText('alice')
    await expect(rows.first().locator('td.dk-num').nth(1)).toHaveText('-')
    await expect(page.getByTestId('usage-table').getByRole('columnheader', { name: 'Last used' })).toBeVisible()
    await expect(page.getByRole('radio', { name: 'User' })).toBeVisible()
  })

  test('a non-admin sees their own numbers only: no user group, the own-usage endpoint', async ({ page }) => {
    await mockTraffic(page)
    await authAs(page, 'user')
    const hits = []
    await page.route('**/api/auth/usage?*', route => { hits.push('own'); return route.fulfill({ json: usagePayload('day', { scale: 0.2 }) }) })
    await page.route('**/api/auth/usage/sources?*', route => route.fulfill({ json: sourcesPayload('day') }))
    await page.route('**/api/auth/admin/usage*', route => { hits.push('admin'); return route.fulfill({ status: 403, json: {} }) })
    await page.goto('/app/usage')
    await expect(page.locator('.tf-source')).toContainText('Your own requests and tokens')
    await expect(page.getByRole('radio', { name: 'User' })).toHaveCount(0)
    await expect(page.getByRole('radio', { name: 'API key' })).toBeVisible()
    await expect(page.getByTestId('usage-table')).toBeVisible()
    expect(hits).toEqual(['own'])
  })

  test('a quota is drawn with its pace', async ({ page }) => {
    await mockTraffic(page)
    await authAs(page, 'user')
    await page.route('**/api/auth/quota', route => route.fulfill({ json: { quotas: [{ model: 'qwen3-8b-instruct', window: '1d', max_requests: 100000, current_requests: 20, resets_at: new Date(Date.now() + 3600_000).toISOString() }] } }))
    await page.route('**/api/auth/usage?*', route => route.fulfill({ json: usagePayload('day', { scale: 0.2 }) }))
    await page.route('**/api/auth/usage/sources?*', route => route.fulfill({ json: sourcesPayload('day') }))
    await page.goto('/app/usage')
    await expect(page.getByTestId('quota-forecast')).toContainText('Within limits at this pace')
  })
})

test.describe('Usage on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('has no sideways scroll', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/usage')
    await expect(page.getByTestId('usage-table')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
