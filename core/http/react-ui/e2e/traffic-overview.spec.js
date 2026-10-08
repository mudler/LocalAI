import { test, expect } from './coverage-fixtures.js'
import { mockTraffic } from './traffic-fixtures.js'

test.describe('Traffic overview', () => {
  test('opens on the overview, with the five figures and no tiles', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="traffic"]')).toHaveAttribute('aria-current', 'page')
    const sub = page.locator('.hub-subnav')
    await expect(sub.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    await expect(sub.getByRole('link', { name: 'Usage' })).not.toHaveAttribute('aria-current', 'page')
    // Every page of the hub is one link away. Alerts is not among them.
    for (const name of ['Usage', 'Models', 'GPU and host', 'Traces', 'Middleware', 'Prometheus']) {
      await expect(sub.getByRole('link', { name })).toBeVisible()
    }
    await expect(sub.getByRole('link', { name: 'Alerts' })).toHaveCount(0)

    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Traffic, last 24 hours')
    const figures = page.getByTestId('traffic-figures')
    await expect(figures.getByTestId('figure-requests')).toContainText('Requests')
    await expect(figures.getByTestId('figure-failed')).toContainText('7')
    await expect(figures.getByTestId('figure-failed')).toContainText('1.0% of traced requests')
    await expect(figures.getByTestId('figure-p95')).toContainText('1.4 s')
    await expect(figures.getByTestId('figure-tokens-in')).toContainText('usage ledger')
    await expect(figures.getByTestId('figure-tokens-out')).toBeVisible()
    // No invented percentiles.
    await expect(page.getByText(/p50|p99/)).toHaveCount(0)
  })

  test('each chart says its source and keeps a data table behind it', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic')
    for (const id of ['chart-requests', 'chart-failed', 'chart-tokens']) {
      const chart = page.getByTestId(id)
      await expect(chart).toBeVisible()
      await expect(chart.locator('.dk-chart-sub')).not.toBeEmpty()
      const details = chart.locator('details.dk-chart-data')
      await expect(details.locator('table')).toBeHidden()
      await details.locator('summary').click()
      await expect(details.locator('table tbody tr').first()).toBeVisible()
    }
    // A chart with two series names both of them.
    const legend = page.getByTestId('chart-failed').locator('.dk-chart-legend')
    await expect(legend).toContainText('Succeeded')
    await expect(legend).toContainText('Failed')
  })

  test('the arrow keys read a value into a text readout', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic')
    const chart = page.getByTestId('chart-tokens')
    const readout = page.getByTestId('chart-tokens-readout')
    await expect(readout).toContainText('Hover the chart')
    await chart.locator('svg.dk-chart-plot').focus()
    await page.keyboard.press('End')
    await expect(readout).toContainText('In (prompt)')
    await expect(readout).toContainText('Out (completion)')
    const last = await readout.textContent()
    await page.keyboard.press('ArrowLeft')
    await expect(readout).not.toHaveText(last)
    await page.keyboard.press('Escape')
    await expect(readout).toContainText('Hover the chart')
  })

  test('failed requests show in error colour with a way to open them', async ({ page }) => {
    await mockTraffic(page, { scenario: 'errors' })
    await page.goto('/app/traffic')
    const failed = page.getByTestId('figure-failed')
    await expect(failed).toContainText('37')
    await expect(failed).toHaveAttribute('data-level', 'error')
    await failed.getByRole('link', { name: 'Open failed requests' }).click()
    await expect(page).toHaveURL(/\/app\/traces\?state=failed/)
    await expect(page.getByRole('button', { name: /^Failed/ })).toHaveAttribute('aria-pressed', 'true')
  })

  test('a new install with no traffic gets a first-run message with next steps', async ({ page }) => {
    await mockTraffic(page, { scenario: 'empty' })
    await page.goto('/app/traffic')
    const empty = page.getByTestId('traffic-empty')
    await expect(empty).toContainText('No requests yet')
    await expect(empty.getByRole('link', { name: 'Open Chat' })).toBeVisible()
    await expect(page.getByTestId('traffic-figures')).toHaveCount(0)
  })

  test('with tracing off the failed figure says so and links to the switch', async ({ page }) => {
    await mockTraffic(page, { tracing: false })
    await page.goto('/app/traffic')
    const failed = page.getByTestId('figure-failed')
    await expect(failed).toContainText('Tracing is off')
    await expect(failed.locator('dd')).toHaveText('-')
    await expect(page.getByTestId('figure-p95').locator('dd')).toHaveText('-')
    await expect(page.getByTestId('chart-failed-off')).toContainText('known only while tracing is on')
    // The ledger figures do not depend on tracing.
    await expect(page.getByTestId('figure-requests').locator('dd')).not.toHaveText('-')
    await failed.getByRole('link', { name: 'Turn on tracing' }).click()
    await expect(page).toHaveURL(/\/app\/traces$/)
  })

  test('shows a loading state while the usage ledger answers', async ({ page }) => {
    await mockTraffic(page, { delayMs: 800 })
    await page.goto('/app/traffic')
    await expect(page.getByTestId('traffic-loading')).toBeVisible()
    await expect(page.getByTestId('traffic-figures')).toBeVisible()
  })

  test('a failed usage read says so and offers a retry, not zeroes', async ({ page }) => {
    await mockTraffic(page)
    await page.route('**/api/usage/all?*', route => route.fulfill({ status: 500, json: { error: 'boom' } }))
    await page.goto('/app/traffic')
    await expect(page.getByRole('alert')).toContainText('Usage could not be read')
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible()
  })

  test('the window is shared: 7 d here is 7 d on Usage, and asks the API for a week', async ({ page }) => {
    await mockTraffic(page)
    const periods = []
    await page.route('**/api/usage/all?*', route => {
      periods.push(new URL(route.request().url()).searchParams.get('period'))
      return route.fallback()
    })
    await page.goto('/app/traffic')
    await page.getByRole('radio', { name: '7 d' }).click()
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Traffic, last 7 days')
    await page.locator('.hub-subnav').getByRole('link', { name: 'Usage' }).click()
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Usage, last 7 days')
    await expect(page.getByRole('radio', { name: '7 d' })).toHaveAttribute('aria-checked', 'true')
    expect(periods).toContain('week')
  })

  test('a longer window says the trace buffer covers at most 7 days', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic')
    await page.getByRole('radio', { name: '30 d' }).click()
    await expect(page.locator('.tf-source')).toContainText('at most 7 days')
  })
})

test.describe('Traffic overview on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('has no sideways scroll and the charts fit the width', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic')
    await expect(page.getByTestId('chart-tokens')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    const box = await page.getByTestId('chart-tokens').locator('svg').boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
  })
})
