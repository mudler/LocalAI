import { test, expect } from './coverage-fixtures.js'

// Live failover chain health in the Model Editor. A model that is a failover
// chain shows a strip under the editor header with the chain state, the
// active target, and a per-target health table fed by GET /api/failover plus
// the /api/failover/events SSE stream. Pin controls are admin-only.

const MOCK_METADATA = {
  sections: [{ id: 'general', label: 'General', icon: 'settings', order: 0 }],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', description: 'id', component: 'input', order: 0 },
  ],
}
const MOCK_YAML = 'name: chain\nfailover:\n  targets: [a, b]\n'

const CHAIN = {
  name: 'chain',
  state: 'primary',
  active: 'a',
  active_since: '2026-09-26T09:00:00Z',
  pinned: null,
  targets: [
    { model: 'a', kind: 'local', warm: true, state: 'healthy', consecutive_ok: 5, last_probe: '2026-09-26T09:59:00Z' },
    { model: 'b', kind: 'remote', warm: false, state: 'healthy', consecutive_ok: 3, last_error: 'dial tcp: connection refused while probing upstream' },
  ],
}

// The stream replays a snapshot (primary on a) then a switch to b. The body
// ends after the two frames; EventSource reconnects and replays them, so the
// settled state stays fallback on b.
const SSE_BODY =
  `event: snapshot\ndata: ${JSON.stringify({ chains: [CHAIN] })}\n\n` +
  'event: chain.switched\ndata: {"type":"chain.switched","chain":"chain","from":"a","to":"b","state":"fallback","reason":"trip","at":"2026-09-26T10:00:00Z"}\n\n'

async function mockEditor(page, authStatus) {
  await page.route('**/api/auth/status', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(authStatus) }))
  await page.route('**/api/models/config-metadata*', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_METADATA) }))
  await page.route('**/api/models/edit/**', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ config: MOCK_YAML, name: 'chain' }) }))
  await page.route('**/api/models/config-json/**', (route) =>
    route.fulfill({ contentType: 'application/json', body: '{}' }))
  await page.route('**/api/failover', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ chains: [CHAIN] }) }))
  await page.route('**/api/failover/chain', (route) =>
    route.fulfill({ contentType: 'application/json', body: JSON.stringify(CHAIN) }))
  await page.route('**/api/failover/events', (route) =>
    route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: SSE_BODY }))
}

const NO_AUTH = { authEnabled: false, staticApiKeyRequired: false, providers: [] }
const NON_ADMIN = {
  authEnabled: true,
  staticApiKeyRequired: false,
  providers: ['local'],
  user: { id: 'user-uuid', name: 'User', role: 'user', provider: 'local' },
}

test.describe('Model Editor — failover chain health', () => {
  test('shows the chain strip and follows chain.switched to fallback', async ({ page }) => {
    await mockEditor(page, NO_AUTH)
    await page.goto('/app/model-editor/chain')

    const strip = page.locator('.failover-status')
    await expect(strip).toBeVisible({ timeout: 10_000 })
    const chainPill = strip.locator('.failover-status__state .status-pill')
    await expect(chainPill).toHaveText(/fallback/i)
    await expect(chainPill).toHaveClass(/status-pill--warning/)
    await expect(strip.locator('.failover-status__active')).toHaveText('b')

    // Both targets are listed with their health.
    const rows = strip.locator('tbody tr')
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toContainText('a')
    await expect(rows.nth(1).locator('.status-pill')).toHaveClass(/status-pill--success/)
    // Long errors are truncated but kept whole in the title.
    await expect(rows.nth(1).locator('.failover-status__error')).toHaveAttribute('title', /connection refused/)
  })

  test('admin can pin a target after confirming', async ({ page }) => {
    await mockEditor(page, NO_AUTH)
    let pinBody = null
    await page.route('**/api/failover/chain/pin', async (route) => {
      if (route.request().method() === 'POST') pinBody = route.request().postDataJSON()
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ ...CHAIN, pinned: 'b' }) })
    })
    await page.goto('/app/model-editor/chain')

    const strip = page.locator('.failover-status')
    await expect(strip).toBeVisible({ timeout: 10_000 })
    const pinB = strip.locator('tbody tr').nth(1).getByRole('button', { name: /pin/i })
    await expect(pinB).toBeVisible()
    await pinB.click()

    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /^pin$/i }).click()
    await expect.poll(() => pinBody).toEqual({ target: 'b' })
  })

  // The editor route itself is admin-only (RequireAdmin), so a non-admin never
  // reaches the strip or its pin controls; the component additionally gates
  // pinning on isAdmin for surfaces that are not admin-gated.
  test('non-admin users get no pin controls', async ({ page }) => {
    await mockEditor(page, NON_ADMIN)
    await page.route('**/api/auth/me', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(NON_ADMIN.user) }))
    await page.goto('/app/model-editor/chain')

    await page.waitForURL(/\/app(?!\/model-editor)/, { timeout: 5000 })
    await expect(page.locator('.failover-status')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /^pin/i })).toHaveCount(0)
  })

  test('models that are not failover chains show no strip', async ({ page }) => {
    await mockEditor(page, NO_AUTH)
    await page.route('**/api/models/edit/**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ config: 'name: plain\n', name: 'plain' }) }))
    await page.goto('/app/model-editor/plain')
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('.failover-status')).toHaveCount(0)
  })
})
