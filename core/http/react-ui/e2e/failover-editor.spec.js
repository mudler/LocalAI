import { test, expect } from './coverage-fixtures.js'

// Failover Chain template + FailoverTargetsEditor regression tests.
//
// A failover chain is a model config whose `failover.targets` field lists,
// in order, the downstream models that answer for one name — the first
// healthy target serves each request, the rest take over when it fails.
// This covers:
//   - the create-flow template gallery exposes a "Failover Chain" card that
//     seeds a minimal name + two empty targets
//   - the dedicated FailoverTargetsEditor renders {model, warm} rows with
//     add/remove/move controls
//   - inline warnings for a single-target chain, a duplicated target model,
//     and a target that names the chain itself
//   - saving an edited chain sends the updated failover.targets array in
//     the PATCH body

const FAILOVER_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'failover', label: 'Failover', icon: 'shuffle', order: 90 },
  ],
  fields: [
    {
      path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string',
      section: 'general', label: 'Model Name', component: 'input', order: 0,
    },
    {
      path: 'failover.targets', yaml_key: 'targets', go_type: '[]FailoverTarget', ui_type: 'object',
      section: 'failover', label: 'Failover targets', component: 'failover-targets',
      description: 'Ordered list of models that serve this chain.',
      order: 1,
    },
  ],
}

async function mockCommon(page) {
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }) }))
  await page.route('**/api/models/config-metadata*', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(FAILOVER_METADATA) }))
  await page.route('**/api/models/config-metadata/autocomplete/**', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ values: [] }) }))

  page.on('pageerror', (err) => {
    throw new Error(`uncaught page error: ${err.message}`)
  })
}

test.describe('Failover Chain template - create flow', () => {
  test.beforeEach(async ({ page }) => {
    await mockCommon(page)
  })

  test('template gallery exposes the Failover Chain card', async ({ page }) => {
    await page.goto('/app/model-editor')
    await expect(page.getByRole('button', { name: /Failover Chain/i })).toBeVisible({ timeout: 10_000 })
  })

  test('failover template loads the editor with two target rows', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    await expect(page.getByText(/Unexpected Application Error/i)).toHaveCount(0)
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Failover targets').first()).toBeVisible()

    // Two empty {model: ''} rows seeded by the template.
    await expect(page.locator('input[placeholder="target model..."]')).toHaveCount(2)
    await expect(page.getByRole('button', { name: /Add target/i }).first()).toBeVisible()
  })

  test('Add target adds a row', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    await page.getByRole('button', { name: /Add target/i }).first().click()
    await expect(page.locator('input[placeholder="target model..."]')).toHaveCount(3)
  })

  test('removing a row removes it', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    await page.locator('button[title="Remove target"]').first().click()
    await expect(page.locator('input[placeholder="target model..."]')).toHaveCount(1)
  })

  test('move down/up reorders the target rows', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    const rows = page.locator('input[placeholder="target model..."]')
    await rows.nth(0).fill('model-a')
    await rows.nth(1).fill('model-b')

    // Move the first row down — it should now be the second row's value.
    await page.locator('button[title="Move down"]').first().click()
    await expect(rows.nth(0)).toHaveValue('model-b')
    await expect(rows.nth(1)).toHaveValue('model-a')

    // Move it back up with the second row's "Move up" control.
    await page.locator('button[title="Move up (tried earlier)"]').nth(1).click()
    await expect(rows.nth(0)).toHaveValue('model-a')
    await expect(rows.nth(1)).toHaveValue('model-b')
  })

  test('a single remaining target shows a too-few warning', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    await page.locator('button[title="Remove target"]').first().click()
    await expect(page.locator('input[placeholder="target model..."]')).toHaveCount(1)
    await expect(page.getByText(/nothing to fail over to/i)).toBeVisible()
  })

  test('duplicate target models flag both rows', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    const rows = page.locator('input[placeholder="target model..."]')
    await rows.nth(0).fill('same-model')
    await rows.nth(1).fill('same-model')
    await expect(page.getByText(/Duplicate target/i)).toHaveCount(2)
  })

  test('a target naming the chain itself is flagged', async ({ page }) => {
    await page.goto('/app/model-editor?template=failover')
    // Create mode renders the model name through a dedicated input (not the
    // generic field renderer), whose placeholder comes from the modelEditor
    // i18n namespace rather than the field registry.
    await page.locator('input[placeholder="my-model-name"]').fill('my-chain')
    const rows = page.locator('input[placeholder="target model..."]')
    await rows.nth(0).fill('my-chain')
    await expect(page.getByText(/chain's own name/i)).toBeVisible()
  })
})

test.describe('Failover Chain - saving an edited chain', () => {
  const MOCK_YAML = 'name: my-chain\nfailover:\n    targets:\n        - model: model-a\n          warm: true\n        - model: model-b\n          warm: false\n'

  test.beforeEach(async ({ page }) => {
    await mockCommon(page)
    await page.route('**/api/models/edit/my-chain', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ config: MOCK_YAML, name: 'my-chain' }) }))
    // ModelFailoverStatus mounts unconditionally in edit mode; keep its
    // fetch + SSE subscription harmless for a chain the test doesn't care
    // about the live status of.
    await page.route('**/api/failover', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ chains: [] }) }))
    await page.route('**/api/failover/events', (route) =>
      route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: '' }))
  })

  test('saving sends the updated failover.targets array in the PATCH body', async ({ page }) => {
    let patchBody = null
    await page.route('**/api/models/config-json/my-chain', (route) => {
      if (route.request().method() === 'PATCH') {
        patchBody = route.request().postDataJSON()
        route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, message: "Model 'my-chain' updated successfully" }) })
      } else {
        route.fulfill({ contentType: 'application/json', body: '{}' })
      }
    })

    await page.goto('/app/model-editor/my-chain')
    await expect(page.locator('h1', { hasText: 'Model Editor' })).toBeVisible({ timeout: 10_000 })

    // Existing targets loaded from YAML.
    const rows = page.locator('input[placeholder="target model..."]')
    await expect(rows).toHaveCount(2)

    // Add a third target, then save.
    await page.getByRole('button', { name: /Add target/i }).first().click()
    await rows.nth(2).fill('model-c')

    await page.locator('button', { hasText: 'Save Changes' }).click()
    await expect(page.locator('text=Configuration saved')).toBeVisible({ timeout: 5_000 })

    expect(patchBody).toBeTruthy()
    expect(patchBody.failover.targets).toEqual([
      { model: 'model-a', warm: true },
      { model: 'model-b', warm: false },
      { model: 'model-c', warm: false },
    ])
  })
})
