import { test, expect } from './coverage-fixtures.js'
import { mockSwarm, clusterNodes, clusterRules } from './swarm-fixtures.js'

// The Swarm Placement rules page: rules written as sentences, where each model
// is loaded now, a side sheet with a preview of what the draft would do, and a
// delete that can be taken back. The preview is worked out in the browser from
// the node list, the labels and the loaded replicas, and says so.

const rule = page => page.getByTestId('rule-row')

test.describe('Placement rules: the list', () => {
  test('writes each kind of rule as a sentence', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/scheduling')
    await expect(rule(page)).toHaveCount(5, { timeout: 15_000 })
    await expect(rule(page).nth(0)).toContainText('qwen3-8b-instruct keeps 2 to 3 replicas on nodes with gpu=4090. It is routed by prefix cache.')
    await expect(rule(page).nth(1)).toContainText('llama-3.3-70b-q4 runs as one replica on a node with gpu=4090.')
    await expect(rule(page).nth(3)).toContainText('bge-m3 runs on every node with role=edge.')
    await expect(rule(page).nth(0)).toContainText('Auto-scaling')
    await expect(rule(page).nth(3)).toContainText('Spread')
  })

  test('words the other shapes of replica count', async ({ page }) => {
    await mockSwarm(page, {
      rules: [
        { id: '1', model_name: 'a', min_replicas: 2, max_replicas: 0 },
        { id: '2', model_name: 'b', min_replicas: 3, max_replicas: 3, node_selector: { zone: 'x' } },
        { id: '3', model_name: 'c', min_replicas: 0, max_replicas: 4 },
        { id: '4', model_name: 'd', spread_all: true },
        { id: '5', model_name: 'e', route_policy: 'round_robin' },
      ],
    })
    await page.goto('/app/scheduling')
    await expect(rule(page).nth(0)).toContainText('a keeps at least 2 replicas on any healthy node.')
    await expect(rule(page).nth(1)).toContainText('b keeps 3 replicas on nodes with zone=x.')
    await expect(rule(page).nth(2)).toContainText('c runs up to 4 replicas on any healthy node.')
    await expect(rule(page).nth(3)).toContainText('d runs on every healthy node.')
    await expect(rule(page).nth(4)).toContainText('e has no placement of its own')
    await expect(rule(page).nth(4)).toContainText('Round robin routing')
  })

  test('shows where each model is loaded now, from the loaded replicas', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/scheduling')
    await expect(rule(page).nth(0)).toContainText('Now placed on gpu-box-1, gpu-box-2', { timeout: 15_000 })
    await expect(rule(page).nth(3)).toContainText('Now placed on edge-cpu')
  })

  test('says a model is not loaded when no replica is', async ({ page }) => {
    await mockSwarm(page, { replicas: [] })
    await page.goto('/app/scheduling')
    await expect(rule(page).first()).toContainText('Not loaded now', { timeout: 15_000 })
  })

  test('shows an empty state with the next step', async ({ page }) => {
    await mockSwarm(page, { rules: [] })
    await page.goto('/app/scheduling')
    await expect(page.getByTestId('rules-empty')).toContainText('No placement rules yet', { timeout: 15_000 })
    await expect(page.getByTestId('rules-empty')).toContainText('Add a rule')
  })

  test('labels the placement preview as a preview and reads loaded replicas for it', async ({ page }) => {
    await mockSwarm(page, { nodes: clusterNodes().map(node => (node.id === 'n-gpu3' ? { ...node, status: 'healthy', labels: { gpu: '4090' } } : node)) })
    await page.goto('/app/scheduling')
    const preview = page.getByTestId('placement-preview')
    await expect(preview).toContainText('Preview', { timeout: 15_000 })
    await expect(preview).toContainText('loaded now')
    await expect(preview).toContainText('planned')
    await expect(preview).toContainText('Worked out in this browser')
    // Agent workers host no models, so they are not a column.
    await expect(preview.getByRole('columnheader', { name: 'agent-host' })).toHaveCount(0)
    const qwen = preview.getByRole('row', { name: /qwen3-8b-instruct/ })
    await expect(qwen.getByRole('cell').filter({ hasText: 'loaded now' })).toHaveCount(2)
  })

  test('marks a shadowed rule and one that cannot be met', async ({ page }) => {
    const until = new Date(Date.now() + 10 * 60_000).toISOString()
    await mockSwarm(page, {
      rules: [
        { id: '1', model_name: 'a', node_selector: { gpu: 'x' }, min_replicas: 1, shadowed: true },
        { id: '2', model_name: 'b', node_selector: { gpu: 'x' }, min_replicas: 5, unsatisfiable_until: until },
      ],
    })
    await page.goto('/app/scheduling')
    await expect(rule(page).nth(0).locator('.scheduling-rule-shadowed')).toContainText('Shadowed', { timeout: 15_000 })
    await expect(rule(page).nth(1)).toContainText('Unsatisfiable until')
  })
})

test.describe('Placement rules: the sheet', () => {
  test('previews where a draft rule would put its model, from the node labels', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Edit flux-dev' }).click()
    const preview = page.getByTestId('rule-preview')
    await expect(preview).toContainText('Preview')
    // zone=b is a node that stopped answering, so nothing can take the rule.
    await expect(preview).toContainText('No healthy node matches zone=b')
    await expect(preview).toContainText('free memory and disk')

    // Swap the label for one a healthy node carries.
    await page.getByLabel('Node selector').getByRole('button', { name: /remove/i }).first().click()
    await page.getByRole('combobox', { name: 'Selector key' }).fill('role')
    await page.getByRole('combobox', { name: 'Selector value' }).fill('edge')
    await page.getByRole('button', { name: 'Add selector' }).click()
    await expect(preview).toContainText('would load on demand on one of 1 matching nodes: edge-cpu')
  })

  test('previews a spread and a shortfall', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Edit bge-m3' }).click()
    await expect(page.getByRole('radio', { name: 'Spread' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('rule-preview')).toContainText('would run on all 1 matching nodes: edge-cpu')

    await page.getByRole('radio', { name: 'Auto-scaling' }).click()
    await page.getByLabel('Min replicas').fill('3')
    await expect(page.getByTestId('rule-preview')).toContainText('would keep 3 replicas on edge-cpu')
    await expect(page.getByTestId('rule-preview')).toContainText('2 more replicas than these nodes can hold')
  })

  test('saves a new rule with the fields it was given', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'New rule' }).click()
    await expect(page.getByRole('button', { name: 'Save rule' })).toBeDisabled()
    await page.getByRole('combobox', { name: 'Model' }).fill('new-model')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('rule-sheet')).toBeVisible()
    await page.getByRole('radio', { name: 'Auto-scaling' }).click()
    await page.getByLabel('Min replicas').fill('2')
    await page.getByRole('combobox', { name: 'Selector key' }).fill('zone')
    await page.getByRole('combobox', { name: 'Selector value' }).fill('a')
    await page.getByRole('button', { name: 'Add selector' }).click()
    await page.getByRole('button', { name: 'Save rule' }).click()
    await expect.poll(() => log.find(entry => entry.path === '/api/nodes/scheduling' && entry.method === 'POST')?.body).toMatchObject({
      model_name: 'new-model', node_selector: { zone: 'a' }, min_replicas: 2, max_replicas: 0, spread_all: false,
    })
    await expect(page.getByText('Placement rule saved')).toBeVisible()
    await expect(page.getByTestId('rule-sheet')).toHaveCount(0)
  })

  test('opens on a rule that asks for two replicas when Failover sends a model here', async ({ page }) => {
    await mockSwarm(page)
    await page.goto('/app/scheduling?new=kokoro-82m&min=2')
    await expect(page.getByTestId('rule-sheet')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('radio', { name: 'Auto-scaling' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByLabel('Min replicas')).toHaveValue('2')
    await expect(page.getByRole('combobox', { name: 'Model' })).toHaveValue('kokoro-82m')
    await expect(page).not.toHaveURL(/new=/)
  })

  test('is a bottom sheet on a phone and fits the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Edit qwen3-8b-instruct' }).click()
    const sheet = page.getByTestId('rule-sheet')
    await expect(sheet).toBeVisible()
    // The sheet rises into place; measure once it has stopped.
    await expect.poll(async () => { const b = await sheet.boundingBox(); return b.y + b.height }).toBeLessThanOrEqual(844)
    const box = await sheet.boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(box.y).toBeGreaterThan(0)
    await expect(sheet.getByRole('button', { name: 'Save rule' })).toBeInViewport()
  })
})

test.describe('Placement rules: delete with undo', () => {
  test('takes a rule back with Undo and sends nothing', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Delete kokoro-82m' }).click()
    await expect(rule(page)).toHaveCount(4)
    await expect(page.getByTestId('rule-undo-toast')).toContainText('Rule for kokoro-82m deleted.')
    await page.getByTestId('rule-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(rule(page)).toHaveCount(5)
    await page.waitForTimeout(500)
    expect(log.filter(entry => entry.method === 'DELETE')).toEqual([])
  })

  test('deletes when the toast is dismissed', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Delete kokoro-82m' }).click()
    await page.getByTestId('rule-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => log.filter(entry => entry.method === 'DELETE').map(entry => entry.path)).toEqual(['/api/nodes/scheduling/kokoro-82m'])
    await expect(page.getByText('Placement rule removed')).toBeVisible()
  })

  test('finishes a waiting delete when another is asked for', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Delete kokoro-82m' }).click()
    await page.getByRole('button', { name: 'Delete bge-m3' }).click()
    await expect.poll(() => log.filter(entry => entry.method === 'DELETE').map(entry => entry.path)).toEqual(['/api/nodes/scheduling/kokoro-82m'])
    await expect(rule(page)).toHaveCount(3)
  })

  test('finishes a waiting delete when the page is left', async ({ page }) => {
    const log = await mockSwarm(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Delete kokoro-82m' }).click()
    await page.getByRole('link', { name: 'Nodes', exact: true }).click()
    await expect.poll(() => log.filter(entry => entry.method === 'DELETE').length).toBe(1)
  })

  test('puts the rule back when it cannot be deleted', async ({ page }) => {
    await mockSwarm(page)
    await page.route('**/api/nodes/scheduling/*', route => (route.request().method() === 'DELETE'
      ? route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"database unavailable"}' })
      : route.fallback()))
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Delete kokoro-82m' }).click()
    await page.getByTestId('rule-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByText(/Failed to remove rule/)).toBeVisible()
    await expect(rule(page)).toHaveCount(clusterRules().length)
  })
})
