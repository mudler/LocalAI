import { test, expect } from './coverage-fixtures.js'
import { AGENTS, GRANTS } from './agents-fixtures.js'
import { installUses } from './agents-uses-fixtures.js'

// The memory level of an agent: one control with Off, Reads notes and Reads
// and saves notes, mapped onto the saved fields (the choice of notes and the
// right to remember things). The API is stubbed; the host's rules are covered
// by Go specs.

const SHOTS = process.env.MEMORY_LEVEL_SHOTS || ''
async function shot(page, name, locator) {
  if (!SHOTS) return
  const width = page.viewportSize().width
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(300)
    await locator.screenshot({ path: `${SHOTS}/${name}-${theme}-${width}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

const level = (page, l) => page.getByTestId(`agent-memory-level-${l}`)
const line = (page, id) => page.locator(`[data-testid=agent-effective-line][data-line="${id}"]`)
const memorySection = (page) => page.locator('#aed-sec-memory')

// An agent as the wizard creates it (the blank editor form of a new agent): it
// reads yours and its own notes, saves to its own, and keeps the connector grants.
const DEFAULT_AGENT = { ...AGENTS[1], memory: 'mine_and_own', builtins: ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload', 'memory.write'] }

async function serve(page, { agents = AGENTS, enabled = true, save = 'ask', bindings = [] } = {}) {
  const s = await installUses(page, { agents, bindings })
  await page.route('**/api/chats/agents/builtin-grants', route => route.fulfill({ json: { grants: GRANTS } }))
  await page.route('**/api/chats/memory', route => route.fulfill({ json: { enabled, enabled_set: true, save_mode: save, messaging_save: 'ask', save_mode_set: false, provider: 'builtin', available: true } }))
  return s
}
const saved = (page) => page.getByRole('button', { name: /Create agent|Save changes/i }).first().click()
const putBody = (s) => s.calls.filter(c => c.method === 'PUT' || c.method === 'POST').at(-1)?.body

test.describe('Memory level: defaults and what an agent has today', () => {
  test('an agent with the default notes reads and saves notes, with Mine and its own, and sends both', async ({ page }) => {
    const s = await serve(page, { agents: [DEFAULT_AGENT] })
    await page.goto('/app/agents/a2/edit')
    await expect(level(page, 'saves').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-mine_and_own').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-applied')).toHaveCount(0)
    await page.getByLabel('Name', { exact: false }).first().fill('Helper')
    await saved(page)
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(putBody(s)).toMatchObject({ memory: 'mine_and_own' })
    expect(putBody(s).builtins).toContain('memory.write')
  })

  test('an existing agent shows the level its fields give and a save changes nothing', async ({ page }) => {
    const cases = [
      { memory: 'own', builtins: ['skills.write'], level: 'reads' },
      { memory: 'mine', builtins: ['memory.write'], level: 'saves' },
      { memory: 'none', builtins: ['memory.write'], level: 'off' },
    ]
    for (const c of cases) {
      const s = await serve(page, { agents: [{ ...AGENTS[1], memory: c.memory, builtins: c.builtins }] })
      await page.goto('/app/agents/a2/edit')
      await expect(level(page, c.level).locator('input')).toBeChecked()
      await expect(page.getByTestId('agent-memory-applied')).toHaveCount(0)
      await page.getByLabel('Description').fill('Touched')
      await saved(page)
      await expect(page).toHaveURL(/\/app\/agents$/)
      expect(putBody(s)).toMatchObject({ memory: c.memory, builtins: c.builtins })
      await page.unroute('**/api/chats/**').catch(() => {})
    }
  })

  test('an agent saved before the choice keeps no choice and goes by its right', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], builtins: ['memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    await expect(level(page, 'saves').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-before')).toBeVisible()
    await saved(page)
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect('memory' in putBody(s)).toBe(false)
  })
})

test.describe('Memory level: the three states', () => {
  test('Off sets None and removes the right, hides the choice of notes and the save level, and says why', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['skills.write', 'memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    await level(page, 'off').click()
    await expect(page.getByTestId('agent-memory-whose')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-whose-off')).toContainText('Memory is off for this agent')
    await expect(page.getByTestId('agent-memory-rule')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-rule-off')).toContainText('does not apply while memory is off')
    await expect(page.getByTestId('agent-memory-applied')).toContainText('Memory: Off')
    await expect(line(page, 'memory')).toContainText('Memory is off for this agent')
    await shot(page, 'memory-off', memorySection(page))
    await saved(page)
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(putBody(s)).toMatchObject({ memory: 'none', builtins: ['skills.write'] })
  })

  test('Reads notes keeps the choice, removes the right, and says it cannot save', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'mine', builtins: ['memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    await level(page, 'reads').click()
    await expect(page.getByTestId('agent-memory-mine').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-mine-warn')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-rule')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-rule-off')).toContainText('reads notes but cannot save')
    await expect(line(page, 'memory-level')).toContainText('Reads notes but cannot save')
    await expect(page.getByTestId('agent-effective-summary')).toContainText('Nothing is blocked')
    await shot(page, 'memory-reads', memorySection(page))
    await saved(page)
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(putBody(s)).toMatchObject({ memory: 'mine', builtins: [] })
  })

  test('Reads and saves notes adds the right and shows the save level; coming back from Off restores the choice', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: [] }] }, { save: 'ask' })
    await page.goto('/app/agents/a2/edit')
    await level(page, 'off').click()
    await level(page, 'saves').click()
    await expect(page.getByTestId('agent-memory-own').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-rule')).toBeVisible()
    await expect(line(page, 'memory-level')).toContainText('Saves notes (asks first)')
    await shot(page, 'memory-saves', memorySection(page))
    await saved(page)
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(putBody(s)).toMatchObject({ memory: 'own' })
    expect(putBody(s).builtins).toContain('memory.write')
  })

  test('says Saves notes at once when the agent saves at once, and Undo puts the fields back', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['memory.write'], memory_save: 'auto' }] })
    await page.goto('/app/agents/a2/edit')
    await expect(line(page, 'memory-level')).toContainText('Saves notes at once')
    await level(page, 'reads').click()
    await expect(page.getByTestId('agent-memory-applied')).toBeVisible()
    await page.getByTestId('agent-memory-applied-undo').click()
    await expect(page.getByTestId('agent-memory-applied')).toHaveCount(0)
    await expect(level(page, 'saves').locator('input')).toBeChecked()
    await level(page, 'off').click()
    await page.getByTestId('agent-memory-applied-keep').click()
    await expect(page.getByTestId('agent-memory-applied')).toHaveCount(0)
    await expect(level(page, 'off').locator('input')).toBeChecked()
    expect(s.calls.some(c => c.method === 'PUT')).toBe(false)
  })

  test('the save level and the Notes chip show only while it saves', async ({ page }) => {
    await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['memory.write'], memory_save: 'auto' }] })
    await page.goto('/app/agents/a2/edit')
    await expect(page.locator('.aed-chip__v').filter({ hasText: 'saves at once' })).toBeVisible()
    await level(page, 'reads').click()
    await expect(page.locator('.aed-chip__v').filter({ hasText: 'Reads notes' })).toBeVisible()
    await expect(page.locator('.aed-chip__v').filter({ hasText: 'saves at once' })).toHaveCount(0)
  })
})

test.describe('Memory level: the skills that come with memory', () => {
  test('are locked rows, on with Reads and Reads and saves, off with Off', async ({ page }) => {
    await serve(page)
    await page.goto('/app/agents/a2/edit')
    const box = page.getByTestId('agent-memory-skills')
    const inList = page.getByTestId('agent-skills-panel').getByTestId('agent-skills-item').filter({ has: page.getByRole('switch', { name: 'memory', exact: true }) })
    for (const l of ['saves', 'reads']) {
      await level(page, l).click()
      await expect(box.getByRole('switch', { name: 'memory', exact: true })).toBeChecked()
      await expect(box.getByRole('switch', { name: 'memory', exact: true })).toBeDisabled()
      await expect(inList.getByRole('switch')).toBeChecked()
    }
    await level(page, 'off').click()
    await expect(box.getByRole('switch', { name: 'memory', exact: true })).not.toBeChecked()
    await expect(box).toContainText('Off while this agent')
    await expect(inList.getByRole('switch')).not.toBeChecked()
    await expect(inList).toContainText('Off while this agent')
    await expect(inList.getByRole('switch')).toBeDisabled()
    await shot(page, 'memory-skills-off', page.getByTestId('agent-skills-panel'))
    await level(page, 'saves').click()
    await shot(page, 'memory-skills-on', page.getByTestId('agent-skills-panel'))
  })

  test('are off with the reason when memory is off in Settings, whatever the agent has', async ({ page }) => {
    await serve(page, { enabled: false, agents: [DEFAULT_AGENT] })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-user-off')).toContainText('Memory is off in your settings')
    const box = page.getByTestId('agent-memory-skills')
    await expect(level(page, 'saves').locator('input')).toBeChecked()
    await expect(box.getByRole('switch', { name: 'memory', exact: true })).not.toBeChecked()
    await expect(box).toContainText('memory is off in your Settings')
    await expect(line(page, 'memory-off')).toContainText('Memory is off in your Settings')
    await expect(line(page, 'memory-level')).toHaveCount(0)
    await shot(page, 'memory-settings-off', memorySection(page))
  })
})

test.describe('Memory level: a messaging connection without the permission', () => {
  const view = (code) => ({ binding: 'b1', subjects: [{ id: 'anyone', title: 'Anyone', decisions: [{ capability: 'memory.write', allowed: false, code, blocked_by: 'connection', message: 'The connection does not allow this.', fix: { kind: 'open_connection', target: 'caps', field: 'memory.write' } }] }], recent: [], preview: true })
  const bindings = [{ id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a2', enabled: true, caps: [] }]

  test('the Effective view names the connection where notes are not saved', async ({ page }) => {
    await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['memory.write'] }], bindings })
    await page.route(/\/api\/chats\/connectors\/b1\/access\/preview$/, route => route.fulfill({ json: view('connection_permission_off') }))
    await page.goto('/app/agents/a2/edit')
    await expect(line(page, 'memory-connection')).toContainText('Bender')
    await expect(line(page, 'conn-b1-memory.write')).toHaveAttribute('data-code', 'connection_permission_off')
  })

  test('an agent that only reads is not reported as blocked by the missing right', async ({ page }) => {
    await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: [] }], bindings })
    await page.route(/\/api\/chats\/connectors\/b1\/access\/preview$/, route => route.fulfill({ json: view('agent_permission_off') }))
    await page.goto('/app/agents/a2/edit')
    await expect(line(page, 'memory-level')).toContainText('Reads notes but cannot save')
    await expect(line(page, 'conn-b1-memory.write')).toHaveCount(0)
    await expect(line(page, 'memory-connection')).toHaveCount(0)
  })
})

test.describe('Memory level: narrow screens', () => {
  for (const width of [320, 375]) {
    test(`has no horizontal overflow at ${width} px in any state`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 })
      await serve(page)
      await page.goto('/app/agents/a2/edit')
      for (const l of ['saves', 'reads', 'off']) {
        await level(page, l).click()
        await expect(level(page, l).locator('input')).toBeChecked()
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
        const over = await memorySection(page).evaluate(el => [...el.querySelectorAll('*')].filter(n => n.getBoundingClientRect().right > window.innerWidth + 1).length)
        expect(over).toBe(0)
        await shot(page, `memory-${l}`, memorySection(page))
        await shot(page, `skills-${l}`, page.getByTestId('agent-skills-panel'))
      }
    })
  }
})
