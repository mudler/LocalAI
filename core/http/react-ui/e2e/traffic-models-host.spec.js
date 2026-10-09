import { test, expect } from './coverage-fixtures.js'
import { mockTraffic, gpuHost } from './traffic-fixtures.js'

test.describe('Traffic models', () => {
  test('lists every model with what the ledger, the buffer and the loader know', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/models')
    await expect(page.locator('.hub-subnav').getByRole('link', { name: 'Models' })).toHaveAttribute('aria-current', 'page')
    const rows = page.getByTestId('models-table').locator('tbody tr[data-row]')
    await expect(rows.first()).toContainText('qwen3-8b-instruct')
    await expect(rows.first()).toContainText('In memory')
    // Memory held is the backend process's host memory, as reported.
    await expect(rows.first()).toContainText('9.2 GB')
    // A model that is not loaded has no memory to report.
    const bge = page.locator('tr[data-entity="bge-m3"]')
    await expect(bge).toContainText('Not loaded')
    await expect(bge.locator('td.dk-num').last()).toHaveText('-')
    // The failed load of flux-dev is in the backend-operation buffer.
    await expect(page.locator('tr[data-entity="flux-dev"]')).toContainText('1')
    // A row's concatenated text can contain llama-cpp followed by 99 requests.
    await expect(page.getByText(/\bp(?:50|95|99)\b/i)).toHaveCount(0)
  })

  test('a row opens in place with the split, the buffer and the links', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/models')
    await page.getByRole('button', { name: 'Show details for flux-dev' }).click()
    const detail = page.getByTestId('model-detail')
    await expect(detail).toContainText('out of memory')
    await expect(detail.getByRole('link', { name: 'Backend logs' })).toHaveAttribute('href', /backend-logs\/flux-dev/)
    await expect(detail.getByRole('link', { name: 'Backend operations' })).toHaveAttribute('href', /traces\?tab=backend&q=flux-dev/)
    await expect(detail).toContainText('GPU memory per model is not reported')
  })

  test('searches by name', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/models')
    await page.getByLabel('Search models').fill('kokoro')
    await expect(page.getByTestId('models-table').locator('tbody tr[data-row]')).toHaveCount(1)
  })

  test('with nothing installed or used it names the next step', async ({ page }) => {
    await mockTraffic(page, { scenario: 'empty' })
    await page.route('**/system', route => route.fulfill({ json: { backends: [], loaded_models: [] } }))
    await page.goto('/app/traffic/models')
    await expect(page.getByTestId('models-empty')).toContainText('No models yet')
  })
})

test.describe('GPU and host', () => {
  test('shows the current snapshot and says what LocalAI does not report', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/host')
    const snap = page.getByTestId('host-snapshot')
    await expect(snap).toContainText('NVIDIA RTX 4090')
    await expect(snap).toContainText('18 GB / 24 GB')
    await expect(snap).toContainText('System memory')
    await expect(snap).toContainText('Models disk')
    await expect(snap).toContainText('41%')
    await expect(page.getByTestId('host-not-reported')).toContainText('GPU utilisation, GPU temperature')
    await expect(page.getByText(/temperature \d|°C/)).toHaveCount(0)
  })

  test('the history is labelled as taken since the page opened, and is bounded', async ({ page }) => {
    await mockTraffic(page)
    await page.goto('/app/traffic/host')
    await expect(page.getByTestId('since-open')).toHaveText(/since you opened this page/i)
    await expect(page.getByTestId('operate-capacity')).toContainText('Since you opened this page')
    // The first reading cannot draw a line; the chart says when it starts.
    await expect(page.getByTestId('operate-capacity-wait')).toBeVisible()
    await expect(page.getByTestId('cpu-chart')).toContainText('at most 240 readings')
    // The second reading comes with the next 5 second poll.
    await expect(page.getByTestId('cpu-chart').locator('svg.dk-chart-plot')).toBeVisible({ timeout: 15_000 })
    const chart = page.getByTestId('operate-capacity').locator('svg.dk-chart-plot')
    await expect(chart).toBeVisible()
    await chart.focus()
    await page.keyboard.press('End')
    await expect(page.getByTestId('operate-capacity').locator('.dk-chart-readout')).toContainText('GB')
    const details = page.getByTestId('cpu-chart').locator('details.dk-chart-data')
    await details.locator('summary').click()
    await expect(details.locator('tbody tr').first()).toBeVisible()
  })

  test('a host with no GPU says models run on the CPU', async ({ page }) => {
    const res = { type: 'ram', available: true, aggregate: { total_memory: 64 * 1024 ** 3, used_memory: 30 * 1024 ** 3 }, ram: { total: 64 * 1024 ** 3, used: 30 * 1024 ** 3, free: 10 * 1024 ** 3, available: 34 * 1024 ** 3 } }
    await mockTraffic(page, { resources: res })
    await page.goto('/app/traffic/host')
    await expect(page.getByTestId('host-snapshot')).toContainText('No GPU is reported')
  })

  test('a cluster shows each node with its memory, and a node that stopped answering is marked', async ({ page }) => {
    const GB = 1024 ** 3
    const nodes = [
      { id: 'n1', name: 'gpu-box-1', status: 'healthy', node_type: 'backend', total_vram: 24 * GB, available_vram: 6 * GB, total_ram: 64 * GB, available_ram: 30 * GB, cpu_usage_percent: 40 },
      { id: 'n2', name: 'gpu-box-2', status: 'unhealthy', node_type: 'backend', total_vram: 24 * GB, available_vram: 12 * GB, total_ram: 64 * GB, available_ram: 40 * GB },
      { id: 'n3', name: 'cpu-only', status: 'healthy', node_type: 'backend' },
    ]
    await mockTraffic(page, { distributed: true, nodes })
    await page.goto('/app/traffic/host')
    const table = page.getByTestId('host-nodes')
    await expect(table.locator('tr[data-entity="gpu-box-1"]')).toContainText('18 GB / 24 GB')
    await expect(table.locator('tr[data-entity="gpu-box-1"]')).toContainText('40%')
    await expect(table.locator('tr[data-entity="gpu-box-2"]')).toContainText('Not answering')
    await expect(table.locator('tr[data-entity="cpu-only"]')).toContainText('no reading')
    // The controller's CPU says nothing about the workers: no CPU chart.
    await expect(page.getByTestId('cpu-chart')).toHaveCount(0)
  })

  test('says so when the host cannot be read', async ({ page }) => {
    await mockTraffic(page, { resources: null })
    await page.goto('/app/traffic/host')
    await expect(page.getByTestId('host-error')).toBeVisible()
    await expect(page.getByTestId('host-snapshot')).toBeHidden()
  })
})

test.describe('GPU and host on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('has no sideways scroll', async ({ page }) => {
    await mockTraffic(page, { resources: gpuHost() })
    await page.goto('/app/traffic/host')
    await expect(page.getByTestId('host-snapshot')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
