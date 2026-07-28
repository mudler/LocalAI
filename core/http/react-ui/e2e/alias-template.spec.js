import { test, expect } from './coverage-fixtures.js'

// Alias / Routing template + Manage alias badge regression tests.
//
// An alias is a model config with `alias: <target>` that redirects traffic to
// the target model. This covers the two discoverability surfaces:
//   - the create-flow template gallery exposes an "Alias / Routing" card that
//     seeds a minimal name + alias config
//   - the Manage Models tab renders a read-only "alias -> target" badge on
//     rows that resolve to an alias (looked up via GET /api/aliases, since the
//     capabilities row payload doesn't carry the alias field)

// Minimal metadata so the editor renders the alias field once the template
// loads. Mirrors the Task 7 config-meta registry, which surfaces `alias` as a
// model-select component.
const ALIAS_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'other', label: 'Other', icon: 'more-horizontal', order: 100 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string',
      section: 'general', label: 'Model Name', component: 'input', order: 0 },
    { path: 'alias', yaml_key: 'alias', go_type: 'string', ui_type: 'string',
      section: 'general', label: 'Alias', component: 'model-select', autocomplete_provider: 'models',
      description: 'Redirect this model name to another configured model.', order: 1 },
  ],
}

test.describe('Alias template - create flow', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }) }))
    await page.route('**/api/models/config-metadata*', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(ALIAS_METADATA) }))
    await page.route('**/api/models/config-metadata/autocomplete/**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ values: [] }) }))

    page.on('pageerror', (err) => {
      throw new Error(`uncaught page error: ${err.message}`)
    })
  })

  test('template gallery exposes the Alias / Routing card', async ({ page }) => {
    await page.goto('/app/model-editor')
    await expect(page.getByRole('button', { name: /Alias \/ Routing/i })).toBeVisible({ timeout: 10_000 })
  })

  test('alias template loads the editor with the alias field', async ({ page }) => {
    await page.goto('/app/model-editor?template=alias')
    await expect(page.getByText(/Unexpected Application Error/i)).toHaveCount(0)
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Alias').first()).toBeVisible()
  })
})

test.describe('Manage - alias badge', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }) }))
    await page.route('**/api/models/capabilities', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data: [
        { id: 'fast-llm', capabilities: ['chat'], backend: 'llama-cpp' },
        { id: 'gpt-4', capabilities: ['chat'], backend: 'llama-cpp' },
      ] }) }))
    await page.route('**/api/aliases', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify([{ name: 'gpt-4', target: 'fast-llm' }]) }))
  })

  test('renders a read-only alias -> target badge on aliased rows', async ({ page }) => {
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    // The aliased row shows the target; the plain model row does not.
    await expect(page.getByText('alias -> fast-llm')).toBeVisible({ timeout: 10_000 })
  })
})
