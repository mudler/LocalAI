import { test, expect } from '@playwright/test'
import { ADMIN, app, installedAddon, sessionFor, mockAddons } from './addons-fixtures.js'

// The host controls on an add-on's Settings tab are drawn with the same rows
// and styles as the Settings page: human labels, no raw setting key as a
// label, and the theme tokens applied.
const SHOTS = process.env.UI_SHOTS_DIR

test('the host controls of an add-on are styled like the Settings page and show no raw key as a label', async ({ page }) => {
  const controlled = app({ id: 'sandbox', name: 'sandbox', title: 'Sandbox', description: 'Run commands.', shadows: ['bash'], ui: { entry: `/api/addons/sandbox/assets/${'a'.repeat(64)}/ui.js`, sdk: { min: 2, max: 2 }, controls: ['network-policy'] } })
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
  await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: true } }))
  await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin: [] } }))
  await mockAddons(page, { apps: [controlled], installed: [installedAddon({ id: 'sandbox', title: 'Sandbox' })], hostSettings: { chats_egress_ceiling: 'registries' } })
  await page.route('**/api/settings', route => route.fulfill({ json: { chats_egress_ceiling: 'registries', chats_egress_audit_days: 14 } }))
  await page.route('**/api/chats/admin/egress', route => route.fulfill({ json: { ceiling: 'registries', default: 'off', ports: [80, 443], audit_days: 14, registries: [], allowed_24h: 0, blocked_24h: 0 } }))
  await page.setViewportSize({ width: 1360, height: 1400 })
  await page.goto('/app/addons?detail=sandbox&view=settings')
  const form = page.getByTestId('addon-host-controls')
  await expect(form.getByTestId('host-control-network-policy')).toBeVisible()
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${process.env.UI_SHOT_NAME || 'host-settings'}.png` })

  // Every row has a human label, never the raw snake_case key.
  const labels = await form.locator('.st-row__label').allTextContents()
  expect(labels.length).toBeGreaterThan(3)
  for (const text of labels) expect(text).not.toMatch(/^[a-z0-9]+(_[a-z0-9]+)+$/)
  // No raw setting key is drawn anywhere in the block, and the fieldset that
  // groups the controls has no browser border.
  await expect(form.locator('.st-key')).toHaveCount(0)
  for (const key of ['chats_egress_ceiling', 'chats_egress_default', 'chats_egress_ports', 'chats_egress_allow_private']) await expect(form).not.toContainText(key)
  expect(await form.locator('fieldset').evaluate(el => getComputedStyle(el).borderTopWidth)).toBe('0px')
  // The rows are styled: a row is a grid, not a plain block.
  const display = await form.locator('.st-row').first().evaluate(el => getComputedStyle(el).display)
  expect(display).toBe('grid')
})
