import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { clusterSettingsFixture, mockSwarm } from './swarm-fixtures.js'

// The cluster settings are one form, drawn twice: in the Cluster group of
// Settings and in the side sheet of the Workers page.

const puts = log => log.filter(entry => entry.method === 'PUT' && entry.path === '/api/cluster/settings').map(entry => entry.body)

async function openSettings(page, options = {}) {
  const log = []
  await mockSettings(page)
  await mockSwarm(page, { log, ...options })
  await page.goto('/app/settings')
  await expect(page.getByTestId('settings-page')).toBeVisible()
  return log
}

test.describe('Settings: the Cluster group', () => {
  test('sits after Swarm and sharing and shows the four sections', async ({ page }) => {
    await openSettings(page)
    const names = await page.locator('.st-group .st-group__label').allTextContents()
    expect(names.indexOf('Cluster')).toBe(names.indexOf('Swarm and sharing') + 1)
    await page.locator('.st-group[data-group="cluster"]').click()
    const form = page.getByTestId('settings-cluster')
    await expect(form.getByTestId('cluster-bus')).toHaveText('NATS connected')
    await expect(form.getByTestId('cluster-replica')).toHaveCount(2)
    await expect(form.getByLabel('NATS address')).toHaveValue('nats://nats.example.org:4222')
    await expect(form.getByTestId('cluster-nats-state')).toHaveText('Reachable')
    for (const key of ['nats', 'websocket', 'p2p']) await expect(form.getByTestId(`cluster-weight-${key}-slider-input`)).toBeVisible()
    await expect(form.getByTestId('cluster-weights-sentence')).toContainText('If two workers fit equally, NATS wins over WebSocket')
    await expect(form.getByTestId('cluster-auto-approve')).toHaveAttribute('aria-checked', 'false')
    await expect(form).not.toContainText(/carrier/i)
  })

  test('is absent when the cluster is not enabled, and the link falls back to the first group', async ({ page }) => {
    await mockSettings(page)
    await mockSwarm(page, { features: { distributed: false }, clusterStatus: 503, nodes: null })
    await page.goto('/app/settings?group=cluster')
    await expect(page.getByTestId('settings-page')).toBeVisible()
    await expect(page.locator('.st-group[data-group="cluster"]')).toHaveCount(0)
    await expect(page.locator('.st-group[aria-current="true"]')).toHaveText(/Models in memory/)
  })

  test('saves the NATS addresses and shows the result', async ({ page }) => {
    const log = await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    await page.getByLabel('NATS address').fill('nats://bus.example.org:4222')
    await page.getByLabel('Address given to workers').fill('nats://bus.internal:4222')
    await page.getByTestId('cluster-nats-save').click()
    await expect(page.getByTestId('cluster-nats-result')).toContainText('This frontend reaches the NATS server')
    expect(puts(log)).toEqual([{ nats_url: 'nats://bus.example.org:4222', nats_worker_url: 'nats://bus.internal:4222' }])
  })

  test('shows the reason when a save is refused', async ({ page }) => {
    await mockSettings(page)
    await mockSwarm(page)
    await page.route('**/api/cluster/settings', route => (route.request().method() === 'PUT'
      ? route.fulfill({ status: 422, contentType: 'application/json', body: JSON.stringify({ error: 'nats_url: this replica cannot reach the NATS server' }) })
      : route.fallback()))
    await page.goto('/app/settings?group=cluster')
    await page.getByLabel('NATS address').fill('nats://nowhere:4222')
    await page.getByTestId('cluster-nats-save').click()
    await expect(page.getByTestId('cluster-nats-result')).toContainText('cannot reach the NATS server')
  })

  test('the sliders change the live sentence and Apply sends only the changed weights', async ({ page }) => {
    const log = await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    const apply = page.getByTestId('cluster-weights-apply')
    await expect(apply).toBeDisabled()
    await page.getByTestId('cluster-weight-websocket-slider-input').fill('300')
    await expect(page.getByTestId('cluster-weights-sentence')).toContainText('WebSocket wins over NATS')
    await page.getByTestId('cluster-weight-p2p-slider-input').fill('300')
    await expect(page.getByTestId('cluster-weights-sentence')).toContainText('WebSocket and P2P tie')
    await apply.click()
    await expect(page.getByTestId('cluster-weights-result')).toContainText('Every frontend uses the new weights')
    expect(puts(log)).toEqual([{ carrier_weights: { websocket: 300, p2p: 300 } }])
    await page.getByTestId('cluster-weights-defaults').click()
    await expect(page.getByTestId('cluster-weights-result')).toContainText('default weights are back')
    expect(puts(log)[1]).toEqual({ carrier_weights: { websocket: null, p2p: null } })
    await expect(page.getByTestId('cluster-weight-websocket-slider-input')).toHaveValue('50')
  })

  test('a weight outside 0 to 1000 is refused before a request', async ({ page }) => {
    const log = await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    await page.getByTestId('cluster-weight-nats-slider-input').fill('5000')
    await expect(page.getByTestId('cluster-weight-error-nats')).toContainText('0 to 1000')
    await expect(page.getByTestId('cluster-weights-apply')).toBeDisabled()
    expect(puts(log)).toEqual([])
  })

  test('the auto-approve switch applies at once and is off by default', async ({ page }) => {
    const log = await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    const toggle = page.getByTestId('cluster-auto-approve')
    await expect(toggle).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('cluster-auto-approve-warning')).toHaveCount(0)
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('cluster-auto-approve-warning')).toBeVisible()
    expect(puts(log)).toEqual([{ join_auto_approve: true }])
  })

  test('has no token lifetime setting', async ({ page }) => {
    await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    await expect(page.getByTestId('settings-cluster')).not.toContainText(/lifetime|expire|ttl/i)
  })

  test('search finds the cluster fields, and only the sections that match', async ({ page }) => {
    await openSettings(page)
    const search = page.getByRole('searchbox')
    await search.fill('weight')
    const results = page.getByTestId('settings-cluster-results')
    await expect(results.getByTestId('cluster-section-weights')).toBeVisible()
    await expect(results.getByTestId('cluster-section-nats')).toHaveCount(0)
    await search.fill('nats')
    await expect(results.getByTestId('cluster-section-nats')).toBeVisible()
    await expect(results.getByTestId('cluster-section-weights')).toHaveCount(0)
    await search.fill('approve')
    await expect(results.getByTestId('cluster-section-joining')).toBeVisible()
    await search.fill('zzzzzz')
    await expect(page.getByTestId('settings-results-title')).toHaveText('No settings found')
  })

  test('on a phone the group has no horizontal scroll', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await openSettings(page)
    await page.locator('.st-group[data-group="cluster"]').click()
    await expect(page.getByTestId('settings-cluster')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})

test.describe('Workers: the cluster settings sheet', () => {
  test('opens from the header, shows the same form and links to Settings', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/nodes')
    await page.getByTestId('cluster-settings-open').click()
    const sheet = page.getByTestId('cluster-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.getByTestId('cluster-bus')).toHaveText('NATS connected')
    await expect(sheet.getByTestId('cluster-weights-sentence')).toContainText('NATS wins over WebSocket')
    await expect(sheet.getByTestId('cluster-open-settings')).toHaveAttribute('href', /\/app\/settings\?group=cluster/)
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
  })

  test('a change made in the sheet is what the Settings group shows', async ({ page }) => {
    await mockSettings(page)
    await mockSwarm(page)
    await page.goto('/app/nodes')
    await page.getByTestId('cluster-settings-open').click()
    await page.getByTestId('cluster-weight-p2p-slider-input').fill('80')
    await page.getByTestId('cluster-weights-apply').click()
    await expect(page.getByTestId('cluster-weights-result')).toBeVisible()
    await page.getByTestId('cluster-open-settings').click()
    await expect(page).toHaveURL(/group=cluster/)
    await expect(page.getByTestId('settings-cluster').getByTestId('cluster-weight-p2p-slider-input')).toHaveValue('80')
  })

  test('says so when NATS is configured but out of reach', async ({ page }) => {
    await mockSwarm(page, { cluster: clusterSettingsFixture({ bus: { bus: 'postgres', nats_configured: true, nats_connected: false } }) })
    await page.goto('/app/nodes')
    await page.getByTestId('cluster-settings-open').click()
    await expect(page.getByTestId('cluster-bus')).toHaveText('PostgreSQL')
    await expect(page.getByTestId('cluster-bus-note')).toContainText('does not reach it')
    await expect(page.getByTestId('cluster-nats-state')).toHaveText('Not reachable')
  })

  test('on a phone the sheet fits the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await mockSwarm(page)
    await page.goto('/app/nodes')
    await page.getByTestId('cluster-settings-open').click()
    const box = await page.getByTestId('cluster-sheet').boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    const overflow = await page.getByTestId('cluster-sheet').evaluate(el => el.scrollWidth - el.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
