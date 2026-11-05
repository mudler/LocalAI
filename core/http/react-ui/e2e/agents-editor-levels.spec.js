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

async function open(page, url = '/app/agents/new', { bindings = [] } = {}) {
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
    const note = page.getByTestId('agent-preset-applied')
    await expect(note).toContainText('Applied the Look only level')
    await expect(note).toContainText('Nothing is saved until you save')

    await page.getByTestId('agent-preset-applied-undo').click()
    await expect(note).toHaveCount(0)
    await expect(files).toBeChecked()
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
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'POST').body
    expect(body.builtins_off).toContain('files')
    expect(body.builtins_off).not.toContain('web')
    expect(body.builtins_on).toContain('saved_agents')
    expect(body.builtins).toEqual(expect.arrayContaining(['triggers.propose', 'memory.write']))
  })
})

test.describe('Templates', () => {
  test('a template fills the identity and starts at a level, and one click takes it back', async ({ page }) => {
    await open(page)
    const name = page.getByLabel('Name', { exact: false }).first()
    await page.locator('[data-template="researcher"]').click()
    await expect(name).toHaveValue('research-assistant')
    await expect(page.locator('[data-template="researcher"]')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-template-applied')).toContainText('Started from Research assistant')
    await expect(page.getByTestId('agent-preset-state')).toContainText('Level: Look only')
    await page.getByTestId('agent-template-applied-undo').click()
    await expect(name).toHaveValue('')
    await expect(page.getByLabel('Instructions')).toHaveValue('')
    await expect(page.getByTestId('agent-preset-state')).toContainText('Custom mix')
  })
})

test.describe('Connections', () => {
  test('the section names the connections an add-on may use, and lists where the agent is connected', async ({ page }) => {
    const bindings = [{ id: 'b9', name: 'Team', kind: 'slack', agent_id: 'a1', enabled: true, caps: ['triggers.propose', 'memory.write'] }]
    const s = await open(page, '/app/agents/a1/edit', { bindings })
    const section = page.locator('#aed-sec-connections')
    await expect(section.getByTestId('agent-connections-hint')).toContainText('each chat chooses its own')
    await section.getByLabel('Home account').check()
    await expect(section.getByTestId('agent-connections-hint')).toContainText('only the ticked connections')
    await expect(section.getByTestId('agent-connections-messaging')).toContainText('Slack Team')
    await expect(section.getByTestId('agent-connections-messaging')).toContainText('allows 2 rights')
    // The choice is drawn here and not a second time in the Tools list.
    await expect(tools(page).getByTestId('agent-tools-pick')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body.connections).toEqual({ tracker: ['b2'] })
  })

  test('with nothing to connect the section says so', async ({ page }) => {
    await installAgents(page, { models: MODELS })
    await page.goto('/app/agents/new')
    const section = page.locator('#aed-sec-connections')
    await expect(section.getByTestId('agent-connections-addons-empty')).toBeVisible()
    await expect(section.getByTestId('agent-connections-messaging-empty')).toBeVisible()
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
  test('Advanced collapses and opens again, and a jump from the nav opens it', async ({ page }) => {
    await open(page, '/app/agents/a1/edit')
    const toggle = page.getByTestId('agent-toggle-advanced')
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('agent-mcp')).toBeVisible()
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('agent-mcp')).toBeHidden()
    await page.getByTestId('agent-nav-advanced').click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('agent-mcp')).toBeVisible()
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
