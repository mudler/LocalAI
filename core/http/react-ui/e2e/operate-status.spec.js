import { test, expect } from './coverage-fixtures.js'
import {
  FAILED_OP, NODES, RUNNING_OP, TRACES_ERRORS, UPGRADE_LLAMA, CATALOG, gpuHost, mockOperate,
} from './operate-fixtures.js'

// The Status page: one sentence, then four rows (Needs you, Capacity, Running
// now, Recent failures). Only a row with a problem opens by itself, and the
// button in it does the thing it says.

const row = (page, id) => page.getByTestId(`operate-row-${id}`)
const head = (page, id) => row(page, id).locator('.op-row__head')
const headline = page => page.getByTestId('operate-headline')

test.describe('Status headline and ledger', () => {
  test('a healthy installation says so and keeps every row closed', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/operate')

    await expect(headline(page)).toHaveText('Everything is running.')
    await expect(headline(page)).toHaveAttribute('data-kind', 'ok')
    for (const id of ['needs', 'capacity', 'running', 'failures']) {
      await expect(head(page, id)).toHaveAttribute('aria-expanded', 'false')
    }
    await expect(page.getByTestId('operate-attention-clear')).toHaveText('Nothing needs you')
    await expect(row(page, 'capacity')).toContainText('GPU memory 18.0 GB of 24.0 GB in use (75%)')
    await expect(row(page, 'running')).toContainText('3 models loaded')
    await expect(row(page, 'failures')).toContainText('1,204 requests in the last 24 hours, none failed')
  })

  test('counts what needs a person, and opens only the rows with a problem', async ({ page }) => {
    await mockOperate(page, {
      resources: gpuHost({ usedGB: 22.5 }),
      upgrades: UPGRADE_LLAMA,
      operations: [FAILED_OP],
      traces: TRACES_ERRORS,
    })
    await page.goto('/app/operate')

    await expect(headline(page)).toHaveText('2 things need you.')
    await expect(page.getByTestId('operate-attention-item')).toHaveCount(2)
    await expect(head(page, 'needs')).toHaveAttribute('aria-expanded', 'true')
    await expect(head(page, 'capacity')).toHaveAttribute('aria-expanded', 'true')
    await expect(head(page, 'failures')).toHaveAttribute('aria-expanded', 'true')
    // Work in flight is not a problem.
    await expect(head(page, 'running')).toHaveAttribute('aria-expanded', 'false')
    await expect(row(page, 'failures')).toContainText('37 failed of 18,402 requests')
  })

  test('says "thing" for one', async ({ page }) => {
    await mockOperate(page, { upgrades: UPGRADE_LLAMA })
    await page.goto('/app/operate')
    await expect(headline(page)).toHaveText('1 thing needs you.')
  })

  test('a full memory pool alone is something to look at, not something to decide', async ({ page }) => {
    await mockOperate(page, { resources: gpuHost({ usedGB: 23 }) })
    await page.goto('/app/operate')
    await expect(headline(page)).toHaveAttribute('data-kind', 'looking')
    await expect(headline(page)).toHaveText('Running, with 1 thing to look at.')
    await expect(head(page, 'capacity')).toHaveAttribute('aria-expanded', 'true')
    await expect(row(page, 'capacity')).toContainText('Memory is 96% full')
  })

  test('says it is reading until every answer is in', async ({ page }) => {
    await mockOperate(page, { delayMs: 1500 })
    await page.goto('/app/operate')
    await expect(headline(page)).toHaveAttribute('data-kind', 'loading')
    await expect(page.getByTestId('operate-overview')).toHaveAttribute('aria-busy', 'true')
    await expect(head(page, 'needs')).toBeDisabled()
    await expect(headline(page)).toHaveAttribute('data-kind', 'ok', { timeout: 10_000 })
    await expect(page.getByTestId('operate-overview')).toHaveAttribute('aria-busy', 'false')
  })

  test('a first run offers a backend and the model list instead of a report', async ({ page }) => {
    await mockOperate(page, {
      catalog: CATALOG.map(b => ({ ...b, installed: false })),
      installed: [],
      models: { data: [] },
      loaded: [],
      resources: gpuHost({ usedGB: 0.4 }),
    })
    await page.goto('/app/operate')

    await expect(headline(page)).toHaveText('Nothing is running yet.')
    await expect(page.getByTestId('operate-first-run').getByRole('link', { name: 'Install a backend' }))
      .toHaveAttribute('href', '/app/backends?view=catalog')
    await expect(page.getByTestId('operate-first-run').getByRole('link', { name: 'Browse models' }))
      .toHaveAttribute('href', '/app/models')
    // The rows are still there, saying what they would say.
    await expect(row(page, 'running')).toContainText('Nothing is running.')
  })

  test('a cluster says so, sums memory across the workers and names the node that is down', async ({ page }) => {
    await mockOperate(page, {
      distributed: true,
      nodes: NODES.map(n => (n.id === 'n3' ? { ...n, status: 'unhealthy', healthy: false } : n)),
    })
    await page.goto('/app/operate')

    await expect(page.locator('.op-status__scope')).toHaveText('Cluster, 4 nodes')
    await expect(headline(page)).toHaveText('1 thing needs you.')
    const item = page.getByTestId('operate-attention-item')
    await expect(item).toContainText('n3 is not answering')
    await expect(item.getByRole('link', { name: 'Open nodes' })).toHaveAttribute('href', '/app/nodes')
    await expect(row(page, 'capacity')).toContainText('GPU memory 36.0 GB of 72.0 GB in use (50%) · across 3 nodes')
    await expect(row(page, 'running')).toContainText('3 of 4 nodes healthy')

    await head(page, 'capacity').click()
    await expect(row(page, 'capacity').getByRole('img', { name: /gpu-box-1: 18.0 GB of 24.0 GB in use/ })).toBeVisible()
    await expect(page.getByTestId('operate-capacity')).toContainText('GPU memory in use, all nodes')
  })
})

test.describe('Status actions', () => {
  test('Update starts the backend update', async ({ page }) => {
    await mockOperate(page, { upgrades: UPGRADE_LLAMA })
    const calls = []
    await page.route('**/api/backends/upgrade/llama-cpp', route => { calls.push(route.request().method()); return route.fulfill({ json: { status: 'ok' } }) })
    await page.goto('/app/operate')

    await page.getByTestId('operate-attention-item').getByRole('button', { name: 'Update' }).click()
    await expect.poll(() => calls).toEqual(['POST'])
  })

  test('Retry installs the failed model again, after moving the failure to the record', async ({ page }) => {
    await mockOperate(page, { operations: [FAILED_OP] })
    const calls = []
    await page.route('**/api/operations/job-gemma27/dismiss', route => { calls.push('dismiss'); return route.fulfill({ json: {} }) })
    await page.route('**/api/models/install/**', route => { calls.push(new URL(route.request().url()).pathname); return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')

    const item = page.getByTestId('operate-attention-item')
    await expect(item).toContainText('gemma-3-27b did not finish')
    await expect(item).toContainText('Connection reset by the registry')
    await item.getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => calls).toEqual(['dismiss', '/api/models/install/localai@gemma-3-27b'])
  })

  test('Dismiss moves a failure out of the list', async ({ page }) => {
    await mockOperate(page, { operations: [FAILED_OP] })
    const calls = []
    await page.route('**/api/operations/job-gemma27/dismiss', route => { calls.push('dismiss'); return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')
    await page.getByTestId('operate-attention-item').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => calls).toEqual(['dismiss'])
  })

  test('Unload asks first, then stops the model', async ({ page }) => {
    await mockOperate(page, { resources: gpuHost({ usedGB: 22.5 }) })
    const bodies = []
    await page.route('**/backend/shutdown', route => { bodies.push(route.request().postDataJSON()); return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')

    await page.getByRole('button', { name: 'Unload gemma-3-12b-it' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Stop gemma-3-12b-it?')
    expect(bodies).toEqual([])
    await dialog.getByRole('button', { name: 'Stop model' }).click()
    await expect.poll(() => bodies).toEqual([{ model: 'gemma-3-12b-it' }])
  })

  test('leaving the dialog keeps the model loaded', async ({ page }) => {
    await mockOperate(page, { resources: gpuHost({ usedGB: 22.5 }) })
    let calls = 0
    await page.route('**/backend/shutdown', route => { calls += 1; return route.fulfill({ json: {} }) })
    await page.goto('/app/operate')
    await page.getByRole('button', { name: 'Unload gemma-3-12b-it' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    expect(calls).toBe(0)
  })
})

test.describe('Status capacity chart', () => {
  test('is drawn from readings taken while the page is open, and says so', async ({ page }) => {
    await page.clock.install()
    await mockOperate(page)
    await page.goto('/app/operate')

    const capacity = page.getByTestId('operate-capacity')
    await expect(capacity).toContainText('Since you opened Operate. LocalAI keeps no memory history.')
    // One reading is a bar, not a history.
    await expect(page.getByTestId('operate-capacity-wait')).toBeVisible()
    await expect(capacity.locator('.dk-chart-plot')).toHaveCount(0)

    await page.clock.fastForward(16_000)
    await expect(capacity.locator('.dk-chart-plot')).toBeVisible()
    await expect(capacity.locator('.dk-chart-threshold-label')).toHaveText('capacity 24.0 GB')
    await expect(capacity.locator('.dk-chart-label')).toHaveText('18.0 GB now')
    // The data table behind it lists the same readings.
    await capacity.locator('.dk-chart-data summary').click()
    await expect(capacity.locator('.dk-chart-data tbody tr')).toHaveCount(2)
  })

  test('reads a value with the arrow keys', async ({ page }) => {
    await page.clock.install()
    await mockOperate(page)
    await page.goto('/app/operate')
    await page.clock.fastForward(16_000)
    const plot = page.getByTestId('operate-capacity').locator('.dk-chart-plot')
    await plot.focus()
    await page.keyboard.press('ArrowLeft')
    await expect(page.getByTestId('operate-capacity').locator('.dk-chart-readout')).toContainText('18.0 GB')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('operate-capacity').locator('.dk-chart-readout')).toContainText('Readings over')
  })

  test('keeps no more than a bounded number of readings', async ({ page }) => {
    await page.clock.install()
    await mockOperate(page)
    await page.goto('/app/operate')
    for (let i = 0; i < 12; i += 1) await page.clock.fastForward(16_000)
    await page.getByTestId('operate-capacity').locator('.dk-chart-data summary').click()
    const rows = await page.getByTestId('operate-capacity').locator('.dk-chart-data tbody tr').count()
    expect(rows).toBeGreaterThan(5)
    expect(rows).toBeLessThanOrEqual(240)
  })
})

test.describe('Status layout', () => {
  test('fits a phone without sideways scrolling and keeps every row reachable', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockOperate(page, { upgrades: UPGRADE_LLAMA, operations: [FAILED_OP] })
    await page.goto('/app/operate')
    await expect(headline(page)).toHaveText('2 things need you.')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    await expect(page.getByTestId('operate-attention-item').first().getByRole('button', { name: 'Update' })).toBeVisible()
    await head(page, 'running').click()
    await expect(head(page, 'running')).toHaveAttribute('aria-expanded', 'true')
  })

  test('has no coloured rail on the edge of a row or an item', async ({ page }) => {
    await mockOperate(page, { upgrades: UPGRADE_LLAMA, operations: [FAILED_OP] })
    await page.goto('/app/operate')
    await expect(page.getByTestId('operate-attention-item').first()).toBeVisible()
    const widths = await page.evaluate(() => [...document.querySelectorAll('.op-row, .op-row__head, .op-item')]
      .map(el => parseFloat(getComputedStyle(el).borderLeftWidth)))
    expect(widths.length).toBeGreaterThan(4)
    expect(Math.max(...widths)).toBe(0)
  })

  test('moves nothing when motion is reduced', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockOperate(page)
    await page.goto('/app/operate')
    await expect(headline(page)).toBeVisible()
    const duration = await page.evaluate(() => {
      const el = document.querySelector('.op-row__toggle .lai-icon')
      return parseFloat(getComputedStyle(el).transitionDuration)
    })
    expect(duration).toBeLessThan(0.001)
  })
})

test('a running operation shows in the Running now row with its progress', async ({ page }) => {
  await mockOperate(page, { operations: [RUNNING_OP] })
  await page.goto('/app/operate')
  await expect(row(page, 'running')).toContainText('1 operation in progress')
  await head(page, 'running').click()
  await expect(row(page, 'running')).toContainText('sglang')
  await expect(row(page, 'running')).toContainText('38%')
  await expect(row(page, 'running').getByRole('link', { name: 'Open Activity' })).toHaveAttribute('href', '/app/activity')
})
