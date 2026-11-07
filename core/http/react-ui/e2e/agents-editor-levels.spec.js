import { test, expect } from './coverage-fixtures.js'
import { installAgents, DRAFT_TOOLS } from './agents-fixtures.js'

// The agent editor, part 2: tool levels and templates that can be undone, the
// Connections section, the model picker with search and capabilities, and the
// two layout fixes of part 1 (Advanced collapses, the phone nav sticks).

const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT', 'FLAG_COMPLETION'] },
  { id: 'gemma-3-12b-it', capabilities: ['FLAG_CHAT', 'FLAG_VISION'] },
  { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'] },
]
const WORK = { id: 'b1', name: 'Work account' }
const HOME = { id: 'b2', name: 'Home account' }

async function open(page, url = '/app/agents/a2/edit', { bindings = [] } = {}) {
  const s = await installAgents(page, { models: MODELS, bindings })
  await page.route('**/api/chats/draft/tools*', route => route.fulfill({
    json: {
      ...DRAFT_TOOLS, servers: [], off: [], connectors: [],
      addons: [{ id: 'tracker', tools: 14, off: false, title: 'Tracker', credentialed: true, connections: [WORK, HOME], enabled_connections: [] }, ...DRAFT_TOOLS.addons],
    },
  }))
  await page.goto(url)
  await expect(page.getByTestId('agent-edit')).toBeVisible()
  return s
}

const tools = (page) => page.getByTestId('agent-tools-panel')

test.describe('Tool levels', () => {
  test('a level sets the switches, says so, and can be undone before saving', async ({ page }) => {
    const s = await open(page)
    const files = tools(page).getByRole('switch', { name: /Your files/ })
    await expect(files).toBeChecked()
    await expect(page.getByTestId('agent-preset-state')).toContainText('Custom mix')

    await page.getByTestId('agent-preset-readonly').click()
    await expect(files).not.toBeChecked()
    await expect(page.getByTestId('agent-preset-readonly').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-preset-state')).toContainText('Level: Look only')
    // Look only means No files, and the Files choice says so.
    await expect(page.getByTestId('agent-folder-scope-none')).toHaveAttribute('aria-pressed', 'true')
    const note = page.getByTestId('agent-preset-applied')
    await expect(note).toContainText('Applied the Look only level')
    await expect(note).toContainText('Nothing is saved until you save')

    await page.getByTestId('agent-preset-applied-undo').click()
    await expect(note).toHaveCount(0)
    await expect(files).toBeChecked()
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-preset-state')).toContainText('Custom mix')
    expect(s.calls.some(c => c.method === 'POST' || c.method === 'PUT')).toBe(false)
  })

  test('a level is sent as the stored switches and grants, and a change by hand makes it custom', async ({ page }) => {
    const s = await open(page)
    await page.getByTestId('agent-preset-assistant').click()
    await expect(page.getByTestId('agent-preset-state')).toContainText('Level: Assistant')
    await tools(page).getByRole('switch', { name: /Look things up online/ }).click()
    await expect(page.getByTestId('agent-preset-state')).toContainText('Custom mix')
    await page.getByTestId('agent-preset-assistant').click()
    await page.getByLabel('Name', { exact: false }).first().fill('Helper')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'PUT').body
    expect(body.builtins_off).toContain('files')
    expect(body.builtins_off).not.toContain('web')
    expect(body.builtins_on).toContain('saved_agents')
    expect(body.builtins).toEqual(expect.arrayContaining(['triggers.propose', 'talkback.channels']))
  })
})

// The starting points belong to the new-agent wizard now (agents-wizard.spec.js).

// The Connections card is gone: the account of an add-on is a row of "What
// it can use", and the list of messaging connections of the agent sits under
// Identity.
test.describe('Accounts of an add-on and where the agent is connected', () => {
  test('the add-on row names the account it uses, and the messaging connections are listed under Identity', async ({ page }) => {
    const bindings = [{ id: 'b9', name: 'Team', kind: 'slack', agent_id: 'a1', enabled: true, caps: ['triggers.propose', 'memory.write'] }]
    const s = await open(page, '/app/agents/a1/edit', { bindings })
    const row = page.getByTestId('agent-addon-box').filter({ hasText: 'Tracker' })
    const now = row.getByTestId('agent-addon-now')
    // The add-on is off: the accounts are there to tick, and the sentence says it is off.
    await expect(now).toContainText('Off')
    await row.getByLabel('Home account').check()
    // Ticking an account switches the add-on on, as it does in a chat.
    await expect(row.getByRole('switch', { name: 'Tracker' })).toBeChecked()
    await expect(now).toContainText('using Home account')
    const identity = page.locator('#aed-sec-identity')
    await expect(identity.getByTestId('agent-connections-messaging')).toContainText('Slack Team')
    await expect(identity.getByTestId('agent-connections-messaging')).toContainText('allows 2 rights')
    // The choice is drawn in the row and not a second time in the built-in list.
    await expect(tools(page).getByTestId('agent-tools-pick')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'PUT').body
    expect(body.connections).toEqual({ tracker: ['b2'] })
    expect(body.addons).toEqual(['office', 'tracker'])
  })

  test('with nothing to connect the section says so', async ({ page }) => {
    await installAgents(page, { models: MODELS })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-connections-messaging-empty')).toBeVisible()
    await expect(page.getByTestId('agent-uses-addons').getByTestId('agent-addon-accounts')).toHaveCount(0)
    await expect(page.locator('#aed-sec-connections')).toHaveCount(0)
  })
})

test.describe('Model picker', () => {
  test('the list can be searched and each model shows what it can do', async ({ page }) => {
    await open(page, '/app/agents/a1/edit')
    await expect(page.getByTestId('agent-model-caps')).toContainText('Chat')
    await page.getByTestId('home-model-chip').first().click()
    const filter = page.getByRole('combobox', { name: 'Filter models' })
    await expect(filter).toBeVisible()
    await expect(page.getByRole('option', { name: /qwen3-8b/ })).toContainText('completion')
    await expect(page.getByRole('option', { name: /gemma-3-12b-it/ }).locator('[data-icon="eye"]')).toHaveCount(1)
    await filter.fill('gemma')
    await expect(page.getByRole('option', { name: /phi-4-mini/ })).toHaveCount(0)
    await page.getByRole('option', { name: /gemma-3-12b-it/ }).click()
    await expect(page.getByTestId('agent-model-caps')).toContainText('Understands images')
  })
})

test.describe('Layout', () => {
  test('Advanced holds the export of a saved agent, collapses and opens again, and a jump from the nav opens it', async ({ page }) => {
    await open(page, '/app/agents/a1/edit')
    const toggle = page.getByTestId('agent-toggle-advanced')
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    // The MCP servers moved to "What it can use"; Advanced keeps only the export.
    const exportButton = page.getByRole('button', { name: 'Export agent' }).first()
    await expect(page.getByTestId('agent-mcp-picker')).toBeVisible()
    await expect(page.locator('#aed-sec-advanced').getByTestId('agent-mcp-picker')).toHaveCount(0)
    await expect(page.locator('#aed-sec-advanced').getByRole('button', { name: 'Export agent' })).toBeVisible()
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.locator('#aed-sec-advanced').getByRole('button', { name: 'Export agent' })).toBeHidden()
    await page.getByTestId('agent-nav-advanced').click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(page.locator('#aed-sec-advanced').getByRole('button', { name: 'Export agent' })).toBeVisible()
    await expect(exportButton).toBeVisible()
  })

  test('on a phone the section nav stays under the header while the page scrolls', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 })
    await open(page, '/app/agents/a1/edit')
    await page.evaluate(() => window.scrollTo(0, 1400))
    await page.waitForTimeout(200)
    const box = async (sel) => page.locator(sel).evaluate(el => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom } })
    const bar = await box('.aed-bar')
    const nav = await box('.aed-nav')
    expect(bar.top).toBeGreaterThanOrEqual(-1)
    expect(bar.top).toBeLessThanOrEqual(1)
    expect(nav.top).toBeGreaterThanOrEqual(bar.bottom - 3)
    expect(nav.top).toBeLessThanOrEqual(bar.bottom + 3)
  })
})
