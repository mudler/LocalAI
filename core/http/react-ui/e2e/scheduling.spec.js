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
  // Node labels are only ever needed while writing a rule's node selector, so
  // they live in that field rather than in a card standing open above the
  // rules whether or not anyone is writing one.
  test('keeps no standing label browser on the page', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')
    await expect(page.getByText('llama-3.3')).toBeVisible()

    await expect(page.getByTestId('node-label-reference')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /node labels/i })).toHaveCount(0)
    await expect(page.locator('.scheduling-node-card')).toHaveCount(0)
    // Falcon GPU is a node name, and nothing on this page has a reason to
    // enumerate node names until a selector is being filled.
    await expect(page.getByText('Falcon GPU')).toHaveCount(0)
  })

  test('suggests the cluster\'s own label keys and values as the selector is typed', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()

    const keyInput = page.getByRole('combobox', { name: 'Selector key' })
    await keyInput.click()
    const suggestions = page.getByTestId('label-suggestions')
    // Every key the cluster reports, before a single character is typed.
    await expect(suggestions.getByRole('option', { name: 'gpu.vendor' })).toBeVisible()
    await expect(suggestions.getByRole('option', { name: 'zone' })).toBeVisible()

    await keyInput.fill('vend')
    await expect(suggestions.getByRole('option')).toHaveCount(1)
    await suggestions.getByRole('option', { name: 'gpu.vendor' }).click()
    await expect(keyInput).toHaveValue('gpu.vendor')

    // Values are scoped to the key being filled, so a selector cannot be built
    // out of a pair no node matches.
    const valueInput = page.getByRole('combobox', { name: 'Selector value' })
    await valueInput.click()
    await expect(suggestions.getByRole('option', { name: 'NVIDIA' })).toBeVisible()
    await expect(suggestions.getByRole('option', { name: 'amd' })).toBeVisible()
    await expect(suggestions.getByRole('option', { name: 'east' })).toHaveCount(0)

    await valueInput.fill('nvi')
    await suggestions.getByRole('option', { name: 'NVIDIA' }).click()
    await expect(valueInput).toHaveValue('NVIDIA')
  })

  test('picks a suggestion from the keyboard', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()

    const keyInput = page.getByRole('combobox', { name: 'Selector key' })
    await keyInput.fill('zon')
    await keyInput.press('ArrowDown')
    await keyInput.press('Enter')
    await expect(keyInput).toHaveValue('zone')
    // Enter picked the suggestion rather than committing the chip, so the
    // half-built pair is still in the inputs.
    await expect(page.getByLabel('Node selector').getByText('zone=', { exact: true })).toHaveCount(0)
  })

  // The cluster's vocabulary is a suggestion, never a constraint: an admin
  // labelling nodes for a rule they are about to write must still be able to
  // type a key no node reports yet.
  test('still accepts a label the cluster has never reported', async ({ page }) => {
    await mockScheduling(page)
    await page.goto('/app/scheduling')
    await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()

    await page.getByRole('combobox', { name: 'Selector key' }).fill('tenant')
    await page.getByRole('combobox', { name: 'Selector value' }).fill('acme')
    await page.getByRole('button', { name: 'Add selector' }).click()

    await expect(page.getByLabel('Node selector').getByText('tenant=acme', { exact: true })).toBeVisible()
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

  // The roster feeds suggestions and nothing else now, so failing to load it
  // must cost the admin nothing but the hints.
  test('leaves the selector fully usable when the node roster fails to load', async ({ page }) => {
    await page.route('**/api/nodes/scheduling', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([rule]) }))
    await page.route('**/api/nodes', route => route.fulfill({ status: 500, body: 'failed' }))
    await page.goto('/app/scheduling')

    // The rules still render: the roster is not on their path.
    await expect(page.getByText('llama-3.3')).toBeVisible()

    await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()
    await page.getByRole('combobox', { name: 'Selector key' }).fill('gpu.vendor')
    await page.getByRole('combobox', { name: 'Selector value' }).fill('nvidia')
    await page.getByRole('button', { name: 'Add selector' }).click()

    await expect(page.getByLabel('Node selector').getByText('gpu.vendor=nvidia', { exact: true })).toBeVisible()
  })

  // A rule may be keyed by an alias, in which case it governs whichever model
  // the alias points at. The page has to say which model that is, because the
  // rule's own name no longer tells you.
  test.describe('rules keyed by a model alias', () => {
    const aliasRule = {
      model_name: 'production',
      target_model: 'llama-3.3',
      model_is_alias: true,
      node_selector: { tier: 'gpu' },
      min_replicas: 2,
      max_replicas: 4,
    }

    async function mockAliases(page, aliases = [{ name: 'production', target: 'llama-3.3' }]) {
      await page.route('**/api/aliases', route => route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(aliases),
      }))
      await page.route('**/api/models/capabilities', route => route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ object: 'list', data: [{ id: 'llama-3.3' }, { id: 'production' }] }),
      }))
    }

    test('names the model an alias rule governs', async ({ page }) => {
      await mockScheduling(page, { rules: [aliasRule] })
      await mockAliases(page)
      await page.goto('/app/scheduling')

      await expect(page.getByText('production')).toBeVisible()
      await expect(page.locator('.scheduling-rule-target')).toHaveText(/llama-3\.3/)
    })

    test('marks a rule another rule already governs as shadowed', async ({ page }) => {
      await mockScheduling(page, { rules: [{ ...aliasRule, shadowed: true }, rule] })
      await mockAliases(page)
      await page.goto('/app/scheduling')

      await expect(page.locator('.scheduling-rule-shadowed')).toHaveCount(1)
      await expect(page.locator('.scheduling-rule-shadowed')).toContainText('Shadowed')
    })

    test('flags an alias rule that no longer resolves', async ({ page }) => {
      await mockScheduling(page, {
        rules: [{ model_name: 'orphan', target_model: 'orphan', model_is_alias: true, min_replicas: 1 }],
      })
      await mockAliases(page, [])
      await page.goto('/app/scheduling')

      await expect(page.locator('.scheduling-rule-target--broken')).toBeVisible()
    })

    test('offers aliases in the model picker, tagged with their target', async ({ page }) => {
      await mockScheduling(page)
      await mockAliases(page)
      await page.goto('/app/scheduling')
      await page.getByRole('button', { name: 'Add Scheduling Rule' }).click()

      const picker = page.locator('.searchable-model-select input')
      await picker.click()
      await expect(page.locator('.sms-hint')).toHaveText('alias of llama-3.3')

      await page.getByRole('option', { name: /production/ }).click()
      await expect(page.getByText(/production is an alias for llama-3\.3/)).toBeVisible()
    })
  })

  test('keeps rule actions reachable on a narrow viewport', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockScheduling(page, { nodeList: nodes.slice(0, 2) })
    await page.goto('/app/scheduling')

    const actions = page.locator('.scheduling-rule-actions')
    await expect(actions.getByRole('button', { name: 'Edit llama-3.3' })).toBeVisible()
    await expect(actions.getByRole('button', { name: 'Delete llama-3.3' })).toBeVisible()
    expect((await actions.boundingBox()).width).toBeGreaterThan(200)
  })
})
