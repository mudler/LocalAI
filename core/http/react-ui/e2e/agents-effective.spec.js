import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'

// The agent editor, part 3: the Effective view (what the agent can do right
// now, from the form as it is, with the reason and the fix of anything
// blocked), Try it (a test chat with the unsaved settings) and the More menu.
// The API is stubbed.

const SHOTS = process.env.EDITOR_SHOTS || ''
async function shot(page, name, options = {}) {
  if (!SHOTS) return
  const width = page.viewportSize().width
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(400)
    await page.screenshot({ path: `${SHOTS}/${name}-${theme}-${width}.png`, ...options })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

const eff = (page) => page.getByTestId('agent-effective')
const line = (page, id) => page.locator(`[data-testid=agent-effective-line][data-line="${id}"]`)
const memory = (page, enabled) => page.route('**/api/chats/memory', route => route.fulfill({
  json: { enabled, enabled_set: true, save_mode: 'ask', messaging_save: 'ask', save_mode_set: false, provider: 'builtin', available: true },
}))

const VIEW = {
  binding: 'b1',
  subjects: [
    {
      id: 'anyone', title: 'Anyone on the list',
      decisions: [
        { capability: 'files.upload', allowed: true },
        { capability: 'memory.write', allowed: false, code: 'agent_permission_off', blocked_by: 'agent', message: 'The agent has not been given this right.', fix: { kind: 'open_agent', target: 'a2', field: 'memory.write' } },
        { capability: 'triggers.run', allowed: false, code: 'connection_permission_off', blocked_by: 'connection', message: 'The connection does not allow this.', fix: { kind: 'open_connection', target: 'caps', field: 'triggers.run' } },
      ],
    },
  ],
  recent: [], preview: true,
}

test.describe('Effective view', () => {
  test('follows the form as it is, before anything is saved', async ({ page }) => {
    const s = await installAgents(page)
    await memory(page, true)
    await page.goto('/app/agents/new')
    await expect(eff(page)).toBeVisible()
    await expect(line(page, 'tools')).toContainText('Your files')
    await expect(page.getByTestId('agent-effective-summary')).toContainText('Nothing is blocked')
    await expect(line(page, 'memory')).toContainText('Reads your notes and its own')
    await expect(line(page, 'memory-level')).toContainText('Saves notes (asks first)')
    await expect(line(page, 'knowledge')).toContainText('No knowledge collections')
    await expect(line(page, 'connections')).toContainText('Save the agent')

    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: /Your files/ }).click()
    await expect(line(page, 'tools')).not.toContainText('Your files')
    await page.getByRole('combobox', { name: 'Collections +' }).fill('handbook')
    await page.getByRole('combobox', { name: 'Collections +' }).press('Enter')
    await expect(line(page, 'knowledge')).toContainText('handbook')
    // The None card is the level Off now.
    await page.getByTestId('agent-memory-level-off').click()
    await expect(line(page, 'memory')).toContainText('Memory is off for this agent')
    await expect(page.getByTestId('agent-effective-unsaved')).toBeVisible()
    expect(s.calls.some(c => c.method === 'POST' || c.method === 'PUT')).toBe(false)
  })

  test('says memory is off when the person turned it off in Settings, and links to the fix', async ({ page }) => {
    await installAgents(page)
    await memory(page, false)
    await page.goto('/app/agents/new')
    const row = line(page, 'memory-off')
    await expect(row).toContainText('Memory is off in your Settings')
    await expect(row).toHaveAttribute('data-code', 'memory_off_user')
    await expect(row.getByTestId('agent-effective-fix')).toHaveAttribute('href', /\/app\/account\?tab=memory/)
    await expect(line(page, 'memory')).toHaveCount(0)
  })

  test('a right under a switch that is off is blocked, and the fix moves to the switch', async ({ page }) => {
    await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['triggers.propose'], builtins_off: ['triggers'] }] })
    await memory(page, true)
    await page.goto('/app/agents/a2/edit')
    const row = line(page, 'grant-triggers.propose')
    await expect(row).toHaveAttribute('data-state', 'blocked')
    await expect(row).toHaveAttribute('data-code', 'tools_switch_off')
    await expect(page.getByTestId('agent-effective-summary')).toContainText('blocked')
    await row.getByTestId('agent-effective-fix').click()
    await expect(page.locator('#aed-sec-capabilities')).toBeInViewport()
    // Turning the switch on clears the line, without saving.
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: 'Create and manage triggers' }).click()
    await expect(line(page, 'grant-triggers.propose')).toHaveCount(0)
  })

  test('a saved connection is checked with the access preview and what is blocked is said with its fix', async ({ page }) => {
    await installAgents(page, { bindings: [{ id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a2', enabled: true, caps: [] }] })
    await memory(page, true)
    const asked = []
    await page.route(/\/api\/chats\/connectors\/b1\/access\/preview$/, route => { asked.push(route.request().postDataJSON()); return route.fulfill({ json: VIEW }) })
    await page.goto('/app/agents/a2/edit')
    await expect(line(page, 'conn-b1')).toContainText('Slack Bender: 1 allowed, 2 not allowed')
    expect(asked).toEqual([{ agent_id: 'a2' }])
    const agentSide = line(page, 'conn-b1-memory.write')
    await expect(agentSide).toContainText('The agent has not been given this right.')
    await expect(agentSide).toHaveAttribute('data-code', 'agent_permission_off')
    await expect(agentSide.getByTestId('agent-effective-fix')).toHaveText('Fix it here')
    const connSide = line(page, 'conn-b1-triggers.run')
    await expect(connSide.getByTestId('agent-effective-fix')).toHaveAttribute('href', /apps\?binding=b1|chats\/connectors/)
    // A change in the form does not ask again; the lines are for the saved connection.
    await page.getByLabel('Description').fill('Changed')
    await expect(page.getByTestId('agent-effective-unsaved')).toContainText('saved connection')
    expect(asked).toHaveLength(1)
  })

  test('a connection that cannot be checked says so', async ({ page }) => {
    await installAgents(page, { bindings: [{ id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a2', enabled: true, caps: [] }] })
    await memory(page, true)
    await page.route(/\/api\/chats\/connectors\/b1\/access\/preview$/, route => route.fulfill({ status: 500, json: { error: { message: 'boom' } } }))
    await page.goto('/app/agents/a2/edit')
    await expect(line(page, 'conn-b1')).toContainText('Could not check Slack Bender')
  })

  test('every line names the section it explains', async ({ page }) => {
    await installAgents(page)
    await memory(page, true)
    await page.goto('/app/agents/new')
    await line(page, 'knowledge').getByTestId('agent-effective-goto').click()
    await expect(page.locator('#aed-sec-memory')).toBeInViewport()
    await expect(page.getByTestId('agent-nav-effective')).toBeVisible()
  })
})

test.describe('Try it and More', () => {
  async function chatStub(page) {
    const seen = { create: null, patch: null, sent: null, deleted: false, running: 1 }
    await page.route(/\/api\/chats(\?.*)?$/, route => {
      if (route.request().method() !== 'POST') return route.fallback()
      seen.create = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: { id: 't1', title: 'Test', model: seen.create.model } })
    })
    await page.route(/\/api\/chats\/t1$/, route => {
      const m = route.request().method()
      if (m === 'PATCH') { seen.patch = route.request().postDataJSON(); return route.fulfill({ json: { id: 't1' } }) }
      if (m === 'DELETE') { seen.deleted = true; return route.fulfill({ status: 204, body: '' }) }
      seen.running -= 1
      return route.fulfill({ json: { id: 't1', running: seen.running > 0 } })
    })
    await page.route(/\/api\/chats\/t1\/messages(\?.*)?$/, route => {
      if (route.request().method() === 'POST') { seen.sent = route.request().postDataJSON(); return route.fulfill({ json: { ok: true } }) }
      return route.fulfill({ json: { messages: [
        { seq: 1, role: 'user', text: seen.sent?.text || '' },
        { seq: 2, role: 'tool', text: 'web_search\n{"q":"x"}' },
        { seq: 3, role: 'assistant', text: 'Bonjour, voici la réponse.' },
      ] } })
    })
    await page.route(/\/api\/chats\/t1\/stop$/, route => route.fulfill({ json: {} }))
    return seen
  }

  test('sends a message with the settings in the form, saves nothing, and deletes the test chat on close', async ({ page }) => {
    const s = await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['memory.write'] }] })
    await memory(page, true)
    const seen = await chatStub(page)
    await page.goto('/app/agents/a2/edit')
    await page.getByLabel('Instructions').fill('Answer in French.')
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: /Your files/ }).click()
    await page.getByTestId('agent-try-open').click()
    const sheet = page.getByTestId('agent-try')
    await expect(sheet).toBeVisible()
    await expect(page.getByTestId('agent-try-skipped')).toContainText('permissions')
    await page.getByTestId('agent-try-input').fill('Hello')
    await page.getByTestId('agent-try-send').click()
    await expect(page.getByTestId('agent-try-log')).toContainText('Bonjour, voici la réponse.')
    await expect(page.getByTestId('agent-try-log')).toContainText('Used a tool: web_search')
    expect(seen.create.model).toBeTruthy()
    expect(seen.create.switches.builtins_off).toEqual(['files'])
    expect(seen.patch).toEqual({ settings: { system_prompt: 'Answer in French.' } })
    expect(seen.sent.text).toBe('Hello')
    expect(s.calls.some(c => c.method === 'POST' || c.method === 'PUT')).toBe(false)
    await shot(page, 'try-it')
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    await expect.poll(() => seen.deleted).toBe(true)
    // The form still holds its unsaved changes.
    await expect(page.getByLabel('Instructions')).toHaveValue('Answer in French.')
  })

  test('the More menu exports a saved agent and is not offered for a new one', async ({ page }) => {
    await installAgents(page)
    await memory(page, true)
    await page.goto('/app/agents/new')
    await expect(page.getByRole('button', { name: 'More actions' })).toHaveCount(0)
    await page.goto('/app/agents/a2/edit')
    await page.getByRole('button', { name: 'More actions' }).click()
    const download = page.waitForEvent('download')
    await page.getByRole('menuitem', { name: 'Export agent' }).click()
    expect((await download).suggestedFilename()).toBe('code-reviewer.json')
  })

  test('the sheet and the Effective view stay inside a 320 px screen', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 })
    await installAgents(page, { agents: [{ ...AGENTS[1], name: `reviewer_${'x'.repeat(60)}`, collections: ['a'.repeat(70)] }] })
    await memory(page, false)
    await page.goto('/app/agents/a2/edit')
    await expect(eff(page)).toBeVisible()
    const wide = () => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(await wide()).toBeLessThanOrEqual(0)
    await page.getByTestId('agent-try-open').click()
    await expect(page.getByTestId('agent-try')).toBeVisible()
    expect(await wide()).toBeLessThanOrEqual(0)
    const box = await page.getByTestId('agent-try').boundingBox()
    expect(box.x + box.width).toBeLessThanOrEqual(320)
  })
})

test.describe('Screenshots', () => {
  test.skip(!SHOTS, 'set EDITOR_SHOTS to take them')
  for (const width of [1280, 768, 375]) {
    test(`effective view at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await installAgents(page, {
        agents: [{ ...AGENTS[1], builtins: ['triggers.propose', 'act.send'], builtins_off: ['triggers'], collections: ['handbook'] }],
        bindings: [{ id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a2', enabled: true, caps: [] }],
      })
      await memory(page, false)
      await page.route(/\/api\/chats\/connectors\/b1\/access\/preview$/, route => route.fulfill({ json: VIEW }))
      await page.goto('/app/agents/a2/edit')
      await expect(line(page, 'conn-b1-memory.write')).toBeVisible()
      await page.getByTestId('agent-nav-effective').click()
      await page.waitForTimeout(700)
      await shot(page, 'effective')
      await page.evaluate(() => window.scrollTo(0, 0))
      await page.waitForTimeout(300)
      await shot(page, 'top')
      await page.getByTestId('agent-try-open').click()
      await shot(page, 'try-empty')
    })
  }
})
