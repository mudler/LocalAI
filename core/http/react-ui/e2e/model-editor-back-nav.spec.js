import { test, expect } from './coverage-fixtures.js'

// Exercises the "Back to <page>" navigation convention: whichever page links
// into the Model Editor stamps its origin as react-router location state, and
// the editor's Back button returns there (captioned with the origin) instead
// of a hardcoded route. Also covers the Middleware page's ?tab= persistence,
// which is what lets the editor return you to the exact tab you came from.

const MOCK_METADATA = {
  sections: [{ id: 'general', label: 'General', icon: 'settings', order: 0 }],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', description: 'id', component: 'input', order: 0 },
  ],
}
const MOCK_YAML = 'name: mock-model\nbackend: mock-backend\n'

// Router config with one model, so the Routing tab renders an editable model
// link we can click through to the editor.
const MOCK_MIDDLEWARE_STATUS = {
  pii: { enabled_globally: false, default_enabled_for_backends: [], patterns: [], models: [], recent_event_count: 0 },
  router: {
    configured: true,
    models: [{ name: 'smart-router', classifier: 'score', fallback: 'qwen-7b', policies: [], candidates: [] }],
    recent_decision_count: 0,
    available_classifiers: ['score'],
  },
}

// Make the editor render for any model name (the header — and thus the Back
// button — only appears once metadata + config have loaded).
async function mockEditorEndpoints(page) {
  await page.route('**/api/models/config-metadata*', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_METADATA) }))
  await page.route('**/api/models/edit/**', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ config: MOCK_YAML, name: 'mock-model' }) }))
  await page.route('**/api/models/config-json/**', (route) =>
    route.fulfill({ contentType: 'application/json', body: '{}' }))
}

test.describe('Model Editor — Back navigation', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }) }))
    await mockEditorEndpoints(page)
  })

  test('Back returns to Manage with a "Back to System" caption', async ({ page }) => {
    await page.goto('/app/manage')
    await expect(page.locator('.table')).toBeVisible({ timeout: 10_000 })

    // Open the first row's action menu and pick "Edit configuration".
    const trigger = page.locator('button.action-menu__trigger').first()
    await expect(trigger).toBeVisible()
    await trigger.click()
    await page.getByRole('menuitem', { name: 'Edit configuration' }).click()

    await expect(page).toHaveURL(/\/app\/model-editor\//)
    const back = page.getByRole('button', { name: /Back to System/ })
    await expect(back).toBeVisible({ timeout: 10_000 })

    await back.click()
    await expect(page).toHaveURL(/\/app\/manage/)
  })

  test('returns to the originating Middleware tab (?tab=routing) it was opened from', async ({ page }) => {
    await page.route('**/api/middleware/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_MIDDLEWARE_STATUS) }))
    await page.route('**/api/pii/events?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ events: [] }) }))
    await page.route('**/api/router/decisions?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ decisions: [] }) }))

    await page.goto('/app/middleware')
    // Switching to Routing must push the tab into the URL.
    await page.getByRole('button', { name: /Routing/i }).click()
    await expect(page).toHaveURL(/[?&]tab=routing/)

    // Click through to the router model's config, then back.
    await page.getByRole('link', { name: 'smart-router' }).click()
    await expect(page).toHaveURL(/\/app\/model-editor\/smart-router/)
    const back = page.getByRole('button', { name: /Back to Middleware/ })
    await expect(back).toBeVisible({ timeout: 10_000 })

    await back.click()
    // Returns to the exact tab, not the default Filtering tab.
    await expect(page).toHaveURL(/\/app\/middleware\?tab=routing/)
    await expect(page.getByText('smart-router').first()).toBeVisible()
  })

  test('falls back to "Back to Manage" on a direct visit with no origin state', async ({ page }) => {
    await page.goto('/app/model-editor/mock-model')
    await expect(page.getByRole('button', { name: /Back to System/ })).toBeVisible({ timeout: 10_000 })
  })
})
