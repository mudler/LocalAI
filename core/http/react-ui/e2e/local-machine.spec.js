import { test, expect } from './coverage-fixtures.js'

// "This machine": the Nodes route on a single-node install, and the Operate
// overview's "Running now" preview. Both read GET /system (loaded models and
// their backend process) and GET /api/resources (host capacity).

const GB = 1024 ** 3

function model(id, backend, rssGB, cpu) {
  return {
    id,
    backend,
    process: {
      pid: 4000 + rssGB,
      rss_bytes: rssGB * GB,
      memory_percent: rssGB / 64 * 100,
      ...(cpu == null ? {} : { cpu_percent: cpu }),
      started_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
    },
  }
}

const RESOURCES = {
  type: 'gpu',
  available: true,
  gpus: [{ total_vram: 24 * GB, used_vram: 18 * GB, free_vram: 6 * GB }],
  ram: { total: 64 * GB, used: 32 * GB, free: 8 * GB, available: 32 * GB },
  cpu: { logical_cores: 16, usage_percent: 50, load_1: 7.5 },
  disk: { total: 1000 * GB, used: 400 * GB, available: 600 * GB },
  aggregate: {},
}

// The cluster API is not mounted on a single node, so it answers 404.
async function mockSingleNode(page, loaded) {
  const state = { loaded: [...loaded], shutdowns: [] }
  await page.route('**/api/features', route => route.fulfill({ json: { distributed: false, agents: true, mcp: true } }))
  await page.route('**/api/nodes', route => route.fulfill({ status: 404, json: { message: 'Not Found' } }))
  await page.route('**/api/resources', route => route.fulfill({ json: RESOURCES }))
  await page.route('**/system', route => route.fulfill({ json: { backends: [], loaded_models: state.loaded } }))
  await page.route('**/backend/shutdown', async route => {
    const body = route.request().postDataJSON()
    state.shutdowns.push(body.model)
    state.loaded = state.loaded.filter(m => m.id !== body.model)
    await route.fulfill({ json: {} })
  })
  return state
}

test.describe('This machine (single node)', () => {
  test('draws the host gauges and one row per loaded model', async ({ page }) => {
    await mockSingleNode(page, [model('qwen3-8b', 'llama-cpp', 12, 35.5), model('whisper-1', 'whisper', 2)])
    await page.goto('/app/nodes')

    const overview = page.getByTestId('host-overview')
    await expect(overview).toBeVisible({ timeout: 15_000 })
    await expect(overview.getByLabel('VRAM capacity', { exact: true })).toContainText('75%')
    await expect(overview.getByLabel('RAM capacity', { exact: true })).toContainText('50%')
    await expect(overview.getByLabel('CPU capacity', { exact: true })).toContainText('8 busy / 16 cores')
    await expect(overview.getByLabel('Models disk capacity', { exact: true })).toContainText('40%')
    // A host is not a fleet: no "N nodes unavailable" coverage line.
    await expect(overview.locator('.fleet-gauge__coverage')).toHaveCount(0)
    await expect(overview.getByLabel('Running models summary')).toContainText('2 running')
    await expect(overview.getByRole('img', { name: /qwen3-8b 12 GB, whisper-1 2 GB/ })).toBeVisible()

    const rows = page.getByTestId('local-model-row')
    await expect(rows).toHaveCount(2)
    const qwen = rows.filter({ hasText: 'qwen3-8b' })
    await expect(qwen).toContainText('llama-cpp')
    await expect(qwen).toContainText('12 GB')
    await expect(qwen).toContainText('35.5%')
    await expect(qwen).toContainText('2h 0m')
    // No CPU reading yet reads as unmeasured, not as idle.
    await expect(rows.filter({ hasText: 'whisper-1' }).getByLabel('CPU not measured yet')).toBeVisible()
  })

  test('stops a model after confirmation and drops it from the list', async ({ page }) => {
    const state = await mockSingleNode(page, [model('qwen3-8b', 'llama-cpp', 12, 10), model('whisper-1', 'whisper', 2, 1)])
    await page.goto('/app/nodes')
    await expect(page.getByTestId('local-model-row')).toHaveCount(2, { timeout: 15_000 })

    await page.getByRole('button', { name: 'Actions for qwen3-8b' }).click()
    await page.getByRole('menuitem', { name: 'Stop model…' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Stop qwen3-8b?')
    await expect(dialog).toContainText('llama-cpp')
    await dialog.getByRole('button', { name: 'Stop model' }).click()

    await expect(page.getByTestId('local-model-row')).toHaveCount(1)
    await expect(page.getByTestId('local-model-row')).toContainText('whisper-1')
    expect(state.shutdowns).toEqual(['qwen3-8b'])
  })

  test('cancelling the stop leaves the model running', async ({ page }) => {
    const state = await mockSingleNode(page, [model('qwen3-8b', 'llama-cpp', 12, 10)])
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: 'Actions for qwen3-8b' }).click({ timeout: 15_000 })
    await page.getByRole('menuitem', { name: 'Stop model…' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    await expect(page.getByTestId('local-model-row')).toHaveCount(1)
    expect(state.shutdowns).toEqual([])
  })

  test('searches by model or backend and sorts by memory', async ({ page }) => {
    await mockSingleNode(page, [model('alpha', 'llama-cpp', 2, 1), model('beta', 'whisper', 12, 1), model('gamma', 'llama-cpp', 6, 1)])
    await page.goto('/app/nodes')
    const rows = page.getByTestId('local-model-row')
    await expect(rows).toHaveCount(3, { timeout: 15_000 })

    await page.getByRole('searchbox', { name: 'Search running models' }).fill('whisper')
    await expect(rows).toHaveCount(1)
    await expect(rows).toContainText('beta')
    await page.getByRole('searchbox', { name: 'Search running models' }).fill('')

    await page.getByRole('button', { name: /Sort by memory/ }).click()
    await page.getByRole('button', { name: /Sort by memory/ }).click()
    await expect(rows.first()).toContainText('beta')
    await expect(rows.last()).toContainText('alpha')
  })

  test('opens the model logs', async ({ page }) => {
    await mockSingleNode(page, [model('qwen3-8b', 'llama-cpp', 12, 10)])
    await page.goto('/app/nodes')
    await page.getByRole('button', { name: 'Actions for qwen3-8b' }).click({ timeout: 15_000 })
    await page.getByRole('menuitem', { name: 'View logs' }).click()
    await expect(page).toHaveURL(/\/app\/backend-logs\/qwen3-8b$/)
  })

  test('says how models get here when nothing is loaded', async ({ page }) => {
    await mockSingleNode(page, [])
    await page.goto('/app/nodes')
    await expect(page.getByTestId('local-running-empty')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('local-running-empty').getByRole('link', { name: 'Models' })).toHaveAttribute('href', '/app/models?view=installed')
    await expect(page.getByLabel('VRAM capacity', { exact: true })).toContainText('75%')
  })

  test('says so when a CPU-only host has no GPU', async ({ page }) => {
    await mockSingleNode(page, [])
    await page.route('**/api/resources', route => route.fulfill({ json: { ...RESOURCES, type: 'ram', gpus: [] } }))
    await page.goto('/app/nodes')
    await expect(page.getByLabel('VRAM capacity', { exact: true })).toContainText('No GPU detected', { timeout: 15_000 })
  })

  test('the Operate overview previews the heaviest five and links to the full view', async ({ page }) => {
    await mockSingleNode(page, [1, 2, 3, 4, 5, 6].map(n => model(`m${n}`, 'llama-cpp', n, 1)))
    await page.goto('/app/operate')

    const preview = page.getByTestId('local-running-models')
    await expect(preview.getByTestId('local-model-row')).toHaveCount(5, { timeout: 15_000 })
    await expect(preview.getByTestId('local-model-row').first()).toContainText('m6')
    await expect(preview).toContainText('1 more not shown')
    // The preview is not a second search surface.
    await expect(preview.getByRole('searchbox')).toHaveCount(0)

    const rail = page.locator('.console-rail a.nav-item[href="/app/nodes"]')
    await expect(rail).toContainText('This machine')
    await expect(rail.locator('.nav-signal')).toContainText('6')

    await preview.getByRole('link', { name: /Open this machine/ }).click()
    await expect(page).toHaveURL(/\/app\/nodes$/)
    await expect(page.getByTestId('local-machine')).toBeVisible()
  })
})

test.describe('This machine (distributed)', () => {
  test('the overview points at the cluster instead of polling the controller', async ({ page }) => {
    const systemCalls = []
    await page.route('**/api/features', route => route.fulfill({ json: { distributed: true } }))
    await page.route('**/api/nodes', route => route.fulfill({ json: [{ id: 'n1', name: 'atlas', status: 'healthy' }] }))
    await page.route('**/system', route => { systemCalls.push(route.request().url()); return route.fulfill({ json: { loaded_models: [] } }) })
    await page.goto('/app/operate')

    await expect(page.getByRole('link', { name: /Running models/ })).toHaveAttribute('href', '/app/nodes', { timeout: 15_000 })
    await expect(page.getByTestId('local-running-models')).toHaveCount(0)
    await expect(page.locator('.console-rail a.nav-item', { hasText: 'This machine' })).toHaveCount(0)
    expect(systemCalls).toEqual([])
  })
})
