import { test, expect } from './coverage-fixtures.js'
import { mockTraffic } from './traffic-fixtures.js'

test.describe('Prometheus', () => {
  test('documents the real endpoint, checks it against this server and lists what it exposes', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/prometheus')
    await expect(page.getByTestId('endpoint-url')).toContainText('/metrics')
    await expect(page.getByTestId('prometheus-endpoint')).toContainText('admin only')
    await expect(page.getByTestId('scrape-state')).toContainText('exposes 4 metric families')
    const table = page.getByTestId('prometheus-metrics')
    await expect(table.locator('tr[data-metric="api_call"]')).toContainText('method, path')
    await expect(table.locator('tr[data-metric="api_call"]')).toContainText('present')
    await expect(table.locator('tr[data-metric="localai_tokens_total"]')).toContainText('present')
    await expect(table.locator('tr[data-metric="localai_pii_events_total"]')).toContainText('not seen yet')
    await expect(page.getByTestId('other-metrics')).toContainText('2 other metric families')
    await expect(page.getByTestId('prometheus-missing')).toContainText('api_call carries the method and the route only')
    await expect(page.getByTestId('scrape-config')).toContainText('metrics_path: /metrics')
    await expect(page.getByTestId('scrape-config')).toContainText('credentials: <admin API key>')
  })

  test('the copy buttons put the text on the clipboard', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await mockTraffic(page)
    await page.goto('/app/traffic/prometheus')
    await page.getByTestId('scrape-config').getByRole('button').click()
    await expect(page.getByTestId('scrape-config').getByRole('button')).toContainText('Copied')
    const text = await page.evaluate(() => navigator.clipboard.readText())
    expect(text).toContain('job_name: localai')
    await page.getByTestId('example-query').getByRole('button').click()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('histogram_quantile(0.95')
  })

  test('says when metrics are switched off, and when the route refuses the caller', async ({ page }) => {
    await mockTraffic(page, { metrics: 'off' })
    await page.goto('/app/traffic/prometheus')
    await expect(page.getByTestId('scrape-state')).toContainText('does not serve /metrics')
    await expect(page.getByTestId('prometheus-metrics').locator('tr[data-metric="api_call"]')).toContainText('not checked')
    await mockTraffic(page, { metrics: 'denied' })
    await page.reload()
    await expect(page.getByTestId('scrape-state')).toContainText('needs an admin account')
  })
})

const STATUS = {
  pii: { default_enabled_for_backends: ['cloud-proxy'], models: [{ name: 'qwen-7b', backend: 'llama-cpp', enabled: true, explicit: true, detectors: ['ner'] }], detector_models: [] },
  router: { configured: false },
  mitm: { running: true, listen_addr: ':8443', configured_addr: ':8443', ca_available: false, models: [] },
}
const EVENTS = { events: [{ id: 'a1', kind: 'admission', created_at: '14:27:55', host: 'ci-bot', status_code: 429, duration_ms: 60000, action: 'block' }] }

test.describe('Middleware pipeline', () => {
  test.beforeEach(async ({ page }) => {
    await mockTraffic(page)
    await page.route('**/api/middleware/status', route => route.fulfill({ json: STATUS }))
    await page.route('**/api/pii/events?*', route => route.fulfill({ json: EVENTS }))
    await page.route('**/api/router/decisions?*', route => route.fulfill({ json: { decisions: [] } }))
  })

  test('shows the five steps in the server order, each with how it stands', async ({ page }) => {
    await page.goto('/app/middleware')
    const steps = page.getByTestId('pipeline').locator('[data-step]')
    await expect(steps).toHaveCount(5)
    await expect(steps.nth(0)).toContainText('Proxy')
    await expect(steps.nth(0)).toContainText('TLS proxy on :8443')
    await expect(steps.nth(1)).toContainText('Admission')
    await expect(steps.nth(1)).toContainText('1 request refused recently')
    await expect(steps.nth(2)).toContainText('1 of 1 models on')
    await expect(steps.nth(3)).toContainText('no router set up')
    await expect(steps.nth(4)).toContainText('Model')
    await expect(page.getByText('cannot be rearranged')).toBeVisible()
    // The filtering step is the one open, as before.
    await expect(steps.nth(2)).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('step-filtering')).toBeVisible()
  })

  test('selecting a step shows only its rules and keeps the step in the URL', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByTestId('pipeline').locator('[data-step="proxy"]').click()
    await expect(page).toHaveURL(/tab=proxy/)
    await expect(page.getByTestId('step-proxy')).toBeVisible()
    await expect(page.getByTestId('step-filtering')).toHaveCount(0)
    await page.getByTestId('pipeline').locator('[data-step="admission"]').click()
    await expect(page.getByTestId('step-admission')).toContainText('1 refusal in the recent events')
    await expect(page.getByTestId('step-admission')).toContainText('has no list of admission rules')
  })

  test('an old ?tab=events link opens the filtering step, with the events below it', async ({ page }) => {
    await page.goto('/app/middleware?tab=events')
    await expect(page.getByTestId('step-filtering')).toBeVisible()
    await expect(page.getByTestId('events-section')).toContainText('ci-bot')
  })

  test('the model step is the end of the pipeline, not a button', async ({ page }) => {
    await page.goto('/app/middleware')
    await expect(page.getByTestId('pipeline').locator('[data-step="model"]')).not.toHaveJSProperty('tagName', 'BUTTON')
  })
})

test.describe('Middleware on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('stacks the steps and has no sideways scroll', async ({ page }) => {
    await mockTraffic(page)
    await page.route('**/api/middleware/status', route => route.fulfill({ json: STATUS }))
    await page.route('**/api/pii/events?*', route => route.fulfill({ json: EVENTS }))
    await page.route('**/api/router/decisions?*', route => route.fulfill({ json: { decisions: [] } }))
    await page.goto('/app/middleware')
    await expect(page.getByTestId('pipeline')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
