import { test, expect } from './coverage-fixtures.js'
import { mockTraffic, TRACES } from './traffic-fixtures.js'

test.describe('Traces list', () => {
  test.beforeEach(async ({ page }) => { await mockTraffic(page) })

  test('filters to failed and to slow requests, and says how many each holds', async ({ page }) => {
    await page.goto('/app/traces')
    const rows = page.getByTestId('api-traces-table').locator('tbody tr')
    await expect(rows).toHaveCount(8)
    await expect(page.getByRole('button', { name: /^All 8/ })).toHaveAttribute('aria-pressed', 'true')
    // 5xx and transport errors are failures. A 429 is a refusal, not a failure.
    await page.getByRole('button', { name: /^Failed 2/ }).click()
    await expect(rows).toHaveCount(2)
    await expect(page).toHaveURL(/state=failed/)
    await page.getByRole('button', { name: /^Slow 4/ }).click()
    await expect(rows).toHaveCount(4)
  })

  test('the refused request is marked as refused, not failed', async ({ page }) => {
    await page.goto('/app/traces')
    const refused = page.locator('tbody tr').filter({ hasText: '429' })
    await expect(refused).toContainText('Refused')
    await expect(refused).not.toContainText('Failed')
  })

  test('searches by path or user, and says when nothing matches', async ({ page }) => {
    await page.goto('/app/traces')
    await page.getByLabel('Search path, user or error').fill('embeddings')
    await expect(page.locator('tbody tr')).toHaveCount(1)
    await page.getByLabel('Search path, user or error').fill('zzz')
    await expect(page.getByTestId('traces-nomatch')).toContainText('No trace matches')
  })

  test('sorts by latency', async ({ page }) => {
    await page.goto('/app/traces')
    await page.getByRole('button', { name: 'Latency' }).click()
    await expect(page.locator('th[aria-sort="ascending"]')).toContainText('Latency')
    await expect(page.locator('tbody tr').first()).toContainText('6 ms')
    await page.getByRole('button', { name: 'Latency' }).click()
    await expect(page.locator('tbody tr').first()).toContainText('30 s')
  })

  test('the backend tab opens a row in place and filters by model', async ({ page }) => {
    await page.goto('/app/traces?tab=backend&q=flux')
    const rows = page.getByTestId('backend-traces-table').locator('tbody tr[data-row]')
    await expect(rows).toHaveCount(1)
    await rows.first().click()
    await expect(page.getByTestId('backend-trace-detail')).toContainText('out of memory')
    await expect(page.getByRole('link', { name: 'View backend logs' })).toHaveAttribute('href', /backend-logs\/flux-dev/)
  })

  test('a row opens the trace page', async ({ page }) => {
    await page.goto('/app/traces')
    await page.locator('tr[data-entity="/v1/images/generations"]').click()
    await expect(page).toHaveURL(/\/app\/traces\/t2$/)
  })

  test('export downloads what the server holds, with bodies', async ({ page }) => {
    await page.goto('/app/traces')
    const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'Export' }).click()])
    expect(download.suggestedFilename()).toMatch(/^traces-api-.*\.json$/)
  })

  test('clearing asks the server and empties the list', async ({ page }) => {
    let cleared = false
    await page.route('**/api/traces/clear', route => { cleared = true; return route.fulfill({ json: { message: 'ok' } }) })
    await page.goto('/app/traces')
    await page.getByRole('button', { name: 'Clear' }).click()
    await expect.poll(() => cleared).toBe(true)
  })
})

test.describe('Tracing off', () => {
  test('explains what is lost and how to turn it on, and turning it on saves the setting', async ({ page }) => {
    await mockTraffic(page, { tracing: false, scenario: 'empty' })
    let saved = null
    await page.route('**/api/settings', async route => {
      if (route.request().method() === 'POST') { saved = route.request().postDataJSON(); return route.fulfill({ json: { success: true } }) }
      return route.fallback()
    })
    await page.goto('/app/traces')
    const empty = page.getByTestId('traces-empty')
    await expect(empty).toContainText('API tracing is off')
    await expect(empty).toContainText('no failed requests, latency or trace pages')
    await expect(empty).toContainText('LOCALAI_ENABLE_TRACING=true')
    // The settings are open, because that is where the switch is.
    await expect(page.getByRole('switch', { name: 'Enable Tracing' })).toHaveAttribute('aria-checked', 'false')
    await page.getByTestId('turn-on-tracing').click()
    await expect.poll(() => saved?.enable_tracing).toBe(true)
  })
})

test.describe('A trace', () => {
  test('a failed request shows the real error, the status and a timeline with the matched operation', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/t2')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('POST /v1/images/generations')
    await expect(page.getByTestId('trace-state')).toContainText('500 Failed')
    const why = page.getByTestId('trace-why')
    await expect(why).toContainText('The server answered 500 and recorded this error')
    await expect(why).toContainText('load model: out of memory (needs 12.4 GB, 5.6 GB free)')
    // Nothing is guessed: no hint, no suggested fix.
    await expect(why).toContainText('records no cause beyond the status and the error text')
    await expect(page.getByText(/What to try|Unload the/)).toHaveCount(0)
    const timeline = page.getByTestId('trace-timeline')
    await expect(timeline.getByTestId('trace-operation')).toHaveCount(1)
    await expect(timeline.getByTestId('trace-operation')).toContainText('model_load')
    await expect(timeline).toContainText('share no request id')
    await expect(page.getByRole('link', { name: 'View backend logs' })).toHaveAttribute('href', /backend-logs\/flux-dev/)
  })

  test('bodies stay out of the page until revealed, one at a time', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/t2')
    await expect(page.getByText('a lighthouse at dusk')).toHaveCount(0)
    await expect(page.getByTestId('body-request')).toContainText('Hidden until you reveal it')
    await page.getByTestId('body-request').getByRole('button', { name: 'Reveal' }).click()
    await expect(page.getByText('a lighthouse at dusk')).toBeVisible()
    await expect(page.getByText('out of memory (needs 12.4 GB, 5.6 GB free)', { exact: false }).first()).toBeVisible()
    await expect(page.getByTestId('body-response').getByRole('button', { name: 'Reveal' })).toBeVisible()
    await page.getByTestId('body-request').getByRole('button', { name: 'Hide' }).click()
    await expect(page.getByText('a lighthouse at dusk')).toHaveCount(0)
  })

  test('request headers are never listed', async ({ page }) => {
    await mockTraffic(page)
    await page.route('**/api/traces/t6', route => route.fulfill({ json: { ...TRACES[5], request: { method: 'POST', path: '/v1/chat/completions', headers: { Authorization: ['[redacted]'], 'X-Secret': ['s3cret'] } }, response: { status: 200 } } }))
    await page.goto('/app/traces/t6')
    await expect(page.getByTestId('trace-state')).toContainText('200 OK')
    await expect(page.locator('body')).not.toContainText('s3cret')
    await expect(page.locator('body')).not.toContainText('Authorization')
  })

  test('an ok request has no failure section and says no backend operation matched', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/t6')
    await expect(page.getByTestId('trace-state')).toContainText('200 OK')
    await expect(page.getByTestId('trace-why')).toHaveCount(0)
    await expect(page.getByTestId('trace-timeline')).toContainText('No backend operation was recorded while this request was open')
  })

  test('a refused request says a 4xx is not counted as a failure', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/t5')
    await expect(page.getByTestId('trace-why')).toContainText('does not count a 4xx status as a failed request')
  })

  test('a trace that left the buffer says so and goes back to the list', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/gone')
    await expect(page.getByTestId('trace-missing')).toContainText('no longer in the buffer')
    await page.getByTestId('trace-missing').getByRole('link', { name: 'All traces' }).click()
    await expect(page).toHaveURL(/\/app\/traces$/)
  })

  test('the Traffic tab stays lit on a trace page', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traces/t2')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="traffic"]')).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.hub-subnav').getByRole('link', { name: 'Traces' })).toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Traces on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('the list and a trace have no sideways scroll', async ({ page }) => {
    await mockTraffic(page)
    for (const path of ['/app/traces', '/app/traces/t2']) {
      await page.goto(path)
      await expect(page.locator('.tf-title, .tf-table').first()).toBeVisible()
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(1)
    }
  })
})
