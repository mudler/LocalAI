import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openPane } from './connector-fixtures.js'

// The page of the Home Assistant add-on (the real built bundle, inside the
// add-on shell) with the core API and the add-on's tools stubbed: the token
// help, the address, the write mode cards, the Test result drawn in the row,
// and the policy editor with its physical-effects warning.

const PAGE = '/app/addons/home-assistant/apps'
const BINDING = { id: 'b1', name: 'Home', kind: 'home-assistant', agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: ['https://ha.example.org:8123'], write_mode: 'propose', has_token: true }
const KIND = { capabilities: [], kindExtra: { title: 'Home Assistant', files: undefined, agent_optional: true, tool_only: true } }
const api = (page, initial = []) => connectorApi(page, initial, 'home-assistant', [], undefined, KIND)

const POLICY = { allow: ['light.turn_on', 'light.turn_off', 'climate.set_temperature', 'script.turn_on'], excludeDomains: ['lock', 'camera'], excludeAreas: [], excludePatterns: [], excludeLabel: 'sensitive', maxTargets: 5, pins: [] }
const HARD = { domains: ['lock', 'alarm_control_panel', 'siren', 'valve', 'water_heater', 'camera', 'homeassistant'], serviceNames: ['restart', 'stop'], servicePrefixes: ['reload'], coverClasses: ['blind'] }

// The tools of the add-on answer through the add-on gateway of the page.
function tools({ admin = true, config = { ok: true, version: '2026.11.0', name: 'Home', integrations: 12 } } = {}) {
  const state = { policy: structuredClone(POLICY), saved: [] }
  const dispatch = (body) => {
    const a = body.arguments || {}
    const ok = (data) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ payload: { data } }) })
    if (a.tool === 'get-policy') return ok({ policy: state.policy, defaults: POLICY, admin, broken: false, hard: HARD, physical: ['climate', 'script'] })
    if (a.tool === 'set-policy') {
      const { tool, ...rest } = a
      state.saved.push(rest)
      state.policy = { ...state.policy, ...rest }
      return ok({ saved: true, policy: state.policy, physical: ['climate', 'script'] })
    }
    if (a.tool === 'get-config') return config.failed ? { status: 400, contentType: 'application/json', body: JSON.stringify({ error: { message: config.failed } }) } : ok(config)
    return null
  }
  return { state, dispatch }
}

test.describe('Home Assistant add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page, t = tools()) => installStudioAddons(page, { ids: ['home-assistant'], dispatch: t.dispatch })

  test('a first connection sees the token steps open and the limits shown', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('home-assistant-limits')).toContainText('does not answer messages')
    await expect(page.getByTestId('home-assistant-setup')).toHaveAttribute('open', '')
    await expect(page.getByTestId('home-assistant-setup')).toContainText('Long-lived access tokens')
  })

  test('with a connection the help is closed and the row shows the address and the mode', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row).toContainText('Home')
    await expect(row.getByTestId('home-assistant-row-address')).toContainText('https://ha.example.org:8123')
    await expect(row.getByTestId('home-assistant-row-write')).toHaveText('Asks first')
    await expect(page.getByTestId('home-assistant-setup')).not.toHaveAttribute('open', '')
  })

  test('errors sit under their field and the name follows the address', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Home Assistant connection' }).click()
    await page.getByLabel('Long-lived access token').fill('short')
    await expect(page.getByLabel('Long-lived access token')).toHaveAttribute('aria-invalid', 'true')
    await expect(page.getByLabel('Long-lived access token')).toHaveAccessibleDescription(/too short/)
    await page.getByLabel('Address of your Home Assistant').fill('http://ha.example.org:8123')
    await expect(page.getByLabel('Address of your Home Assistant')).toHaveAccessibleDescription(/Plain http is accepted only for an address on your own network/)
    await page.getByLabel('Address of your Home Assistant').fill('https://Ha.Example.org:8123/')
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Home Assistant: ha.example.org:8123')
    await page.getByLabel('Long-lived access token').fill('x'.repeat(40))
    await expect(page.getByTestId('connector-save')).toBeEnabled()
  })

  test('each write mode says what it does, the default is read only, and auto still asks for physical effects', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New Home Assistant connection' }).click()
    const group = page.getByRole('radiogroup', { name: 'What the agent may change in your home' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await group.getByRole('radio', { name: /Runs at once/ }).check()
    await expect(page.getByTestId('home-assistant-write-note')).toContainText('still waits for your confirmation every time')
  })

  test('the Test result is drawn in the row from the instance itself', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-test').click()
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row.getByTestId('home-assistant-checks')).toContainText('Reachable. Home Assistant 2026.11.0, 12 integrations.')
    await expect(row.locator('.ha-check--ok')).toHaveCount(1)
  })

  test('a certificate that is not trusted says to pin it', async ({ page }) => {
    await install(page, tools({ config: { failed: 'the certificate of home assistant is not trusted: pin its fingerprint' } }))
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-test').click()
    await openPane(page)
    await expect(page.getByTestId('connector-pane').getByTestId('home-assistant-checks')).toContainText('pin its fingerprint below')
  })

  test('the policy editor warns about physical effects and marks the services that always ask', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('home-assistant-physical-warning')).toContainText('Physical effects.')
    await expect(page.getByTestId('home-assistant-physical-warning')).toContainText('waits for your confirmation each time')
    const summary = page.getByTestId('home-assistant-allow-summary')
    await expect(summary.locator('li', { hasText: 'light.turn_on' })).toContainText('Acts when the write mode allows')
    await expect(summary.locator('li', { hasText: 'climate.set_temperature' })).toContainText('Always asks you')
    await expect(page.getByTestId('home-assistant-confirm-note')).toContainText('climate, script')
    await expect(page.getByTestId('home-assistant-hard')).toContainText('lock.*')
    await expect(page.getByTestId('home-assistant-hard')).toContainText('*.reload*')
  })

  test('an administrator saves the limits, and a hard-excluded service is named before saving', async ({ page }) => {
    const t = tools()
    await install(page, t)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    const allow = page.getByLabel('Allowlist')
    await allow.fill('light.turn_on\nlock.unlock')
    await expect(allow).toHaveAccessibleDescription(/"lock.unlock" can never be allowed/)
    await expect(page.getByTestId('home-assistant-policy-save')).toBeDisabled()
    await allow.fill('light.turn_on\ncover.open_cover')
    await page.getByLabel('Most entities in one call').fill('3')
    await page.getByTestId('home-assistant-exclude-areas').fill('Bathroom')
    await page.getByTestId('home-assistant-policy-save').click()
    await expect(page.getByTestId('home-assistant-policy-saved')).toBeVisible()
    expect(t.state.saved).toHaveLength(1)
    expect(t.state.saved[0].allow).toEqual(['light.turn_on', 'cover.open_cover'])
    expect(t.state.saved[0].maxTargets).toBe(3)
    expect(t.state.saved[0].excludeAreas).toEqual(['Bathroom'])
  })

  test('someone who is not an administrator sees the limits and cannot change them', async ({ page }) => {
    await install(page, tools({ admin: false }))
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('home-assistant-policy-readonly')).toContainText('Only an administrator')
    await expect(page.getByLabel('Allowlist')).toBeDisabled()
    await expect(page.getByTestId('home-assistant-policy-save')).toBeDisabled()
  })

  for (const scheme of ['light', 'dark']) {
    test(`the page fits a phone in the ${scheme} theme`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: 360, height: 740 })
      await install(page)
      await api(page, [BINDING]).install()
      await page.goto(PAGE)
      await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), scheme)
      await expect(page.getByTestId('home-assistant-policy')).toBeVisible()
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
      await page.screenshot({ path: `${process.env.HA_SHOTS || '/tmp'}/ha-${scheme}.png`, fullPage: true })
    })
  }
})
