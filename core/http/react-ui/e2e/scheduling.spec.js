import { test, expect } from './coverage-fixtures.js'

const rule = {
  model_name: 'llama-3.3',
  node_selector: { 'gpu.vendor': 'nvidia' },
  min_replicas: 2,
  max_replicas: 8,
  spread_all: false,
  route_policy: 'prefix_cache',
  balance_abs_threshold: 4,
  balance_rel_threshold: 1.5,
  min_prefix_match: 0.75,
}

const nodes = Array.from({ length: 27 }, (_, index) => ({
  id: `node-${index + 1}`,
  name: index === 0 ? 'Falcon GPU' : `Worker ${index + 1}`,
  status: index === 1 ? 'offline' : 'online',
  labels: index === 2 ? {} : {
    'gpu.vendor': index === 0 ? 'NVIDIA' : 'amd',
    zone: index % 2 ? 'west' : 'east',
  },
}))

async function mockScheduling(page, { rules = [rule], nodeList = nodes } = {}) {
  await page.route('**/api/nodes/scheduling', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(rules),
  }))
  await page.route('**/api/nodes', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(nodeList),
  }))
}

test.describe('Scheduling page', () => {
  test('groups node labels, collapses the reference, filters forgivingly, and expands results', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')

    const reference = page.getByTestId('node-label-reference')
    await expect(reference.getByText('Falcon GPU')).toBeVisible()
    await expect(reference.getByText('No labels')).toBeVisible()
    await expect(reference.locator('.scheduling-node-card')).toHaveCount(5)
    await expect(reference.getByText('5 of 27 nodes')).toBeVisible()

    const toggle = page.getByRole('button', { name: /node labels/i })
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(reference.getByRole('searchbox')).toBeHidden()
    await toggle.click()

    await reference.getByRole('searchbox').fill('GPU.VENDOR=nvi')
    await expect(reference.locator('.scheduling-node-card')).toHaveCount(1)
    await expect(reference.getByText('Falcon GPU')).toBeVisible()

    await reference.getByRole('searchbox').fill('flcn')
    await expect(reference.locator('.scheduling-node-card')).toHaveCount(1)
    await expect(reference.getByText('Falcon GPU')).toBeVisible()

    await reference.getByRole('searchbox').fill('')
    await reference.getByRole('button', { name: 'Show 20 more nodes' }).click()
    await expect(reference.locator('.scheduling-node-card')).toHaveCount(25)
    await expect(reference.getByText('25 of 27 nodes')).toBeVisible()
  })

  test('edits all fields with a locked model and preserves values after a failed save', async ({ page }) => {
    await mockScheduling(page)
    let submitted
    await page.route('**/api/nodes/scheduling', async route => {
      if (route.request().method() === 'POST') {
        submitted = route.request().postDataJSON()
        await route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"nope"}' })
      } else {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([rule]) })
      }
    })
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Edit llama-3.3' }).click()

    await expect(page.getByLabel('Model')).toHaveValue('llama-3.3')
    await expect(page.getByLabel('Model')).toHaveAttribute('readonly', '')
    await expect(page.getByRole('radio', { name: 'Auto-scale' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByLabel('Node selector').getByText('gpu.vendor=nvidia', { exact: true })).toBeVisible()
    await expect(page.getByLabel('Min replicas')).toHaveValue('2')
    await expect(page.getByLabel('Max replicas')).toHaveValue('8')
    await expect(page.getByLabel('Routing policy')).toHaveValue('prefix_cache')
    await expect(page.getByLabel('Min prefix match')).toHaveValue('0.75')
    await expect(page.getByLabel('Balance abs threshold')).toHaveValue('4')
    await expect(page.getByLabel('Balance rel threshold')).toHaveValue('1.5')

    await page.getByLabel('Min replicas').fill('3')
    await page.getByRole('button', { name: 'Save rule' }).click()
    await expect.poll(() => submitted?.min_replicas).toBe(3)
    await expect(page.getByLabel('Min replicas')).toHaveValue('3')

    await page.getByRole('button', { name: 'Cancel' }).click()
    await page.getByRole('button', { name: 'Edit llama-3.3' }).click()
    await expect(page.getByLabel('Min replicas')).toHaveValue('2')
  })

  test('keeps a single add or edit form open and leaves Add blank', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Edit llama-3.3' }).click()
    await expect(page.locator('.scheduling-form')).toHaveCount(1)
    await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()
    await expect(page.locator('.scheduling-form')).toHaveCount(1)
    await expect(page.getByRole('combobox', { name: '' }).first()).toHaveValue('')
    await expect(page.getByRole('combobox', { name: '' }).first()).toBeEnabled()
  })

  test('shows node loading, empty, no-match, and retry states independently from rules', async ({ page }) => {
    let attempts = 0
    await page.route('**/api/nodes/scheduling', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([rule]) }))
    await page.route('**/api/nodes', async route => {
      attempts++
      if (attempts === 1) {
        await new Promise(resolve => setTimeout(resolve, 250))
        await route.fulfill({ status: 500, body: 'failed' })
      } else {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
      }
    })
    await page.goto('/app/scheduling')
    await expect(page.getByText('Loading node labels…')).toBeVisible()
    await expect(page.getByText('llama-3.3')).toBeVisible()
    await expect(page.getByText('Could not load node labels.')).toBeVisible()
    await page.getByRole('button', { name: 'Retry loading node labels' }).click()
    await expect(page.getByText('No nodes are available yet.')).toBeVisible()

    await page.unroute('**/api/nodes')
    await page.route('**/api/nodes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(nodes) }))
    await page.reload()
    await page.getByRole('searchbox', { name: 'Search node labels' }).fill('not-a-real-label')
    await expect(page.getByText('No nodes match your search.')).toBeVisible()
  })

  test('uses one node column and accessible rule actions on a narrow viewport', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockScheduling(page, { nodeList: nodes.slice(0, 2) })
    await page.goto('/app/scheduling')

    const cards = page.locator('.scheduling-node-card')
    const first = await cards.nth(0).boundingBox()
    const second = await cards.nth(1).boundingBox()
    expect(second.y).toBeGreaterThan(first.y + first.height - 1)

    const actions = page.locator('.scheduling-rule-actions')
    await expect(actions.getByRole('button', { name: 'Edit llama-3.3' })).toBeVisible()
    await expect(actions.getByRole('button', { name: 'Delete llama-3.3' })).toBeVisible()
    expect((await actions.boundingBox()).width).toBeGreaterThan(200)
  })
})
