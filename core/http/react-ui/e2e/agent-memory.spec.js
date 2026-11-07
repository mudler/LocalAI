import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS, GRANTS } from './agents-fixtures.js'

// The Memory choice of an agent: the section of the agent form, what the form
// sends, the question on deleting an agent that has notes, and the tabs of the
// notes list. The API is stubbed.

const SHOTS = process.env.MEMORY_SHOTS || ''
async function shot(page, name, options = {}) {
  if (!SHOTS) return
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(400)
    await page.screenshot({ path: `${SHOTS}/${name}-${theme}-${page.viewportSize().width}.png`, ...options })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

const fold = (page, id) => page.locator(`[data-section="${id}"]`)
const LIMITS = { index_lines: 40, index_bytes: 2048, profile_bytes: 4096, turn_bytes: 6144 }
const f = (p, extra = {}) => ({ path: p, name: p.split('/').pop(), size: 20, ...extra })
const GROUPS = [
  { source: 'memory', scope: 'user', max_bytes: 2048, max_lines: 40, files: [f('Memory/index.md', { index: true, lines: 1 })] },
  { source: 'memory', scope: 'agent', key: 'a1', label: 'research-assistant', max_bytes: 2048, max_lines: 40, files: [f('Memory/agents/a1/index.md', { index: true, lines: 1 }), f('Memory/agents/a1/style.md')] },
  { source: 'memory', scope: 'agent', key: 'gone', deleted: true, max_bytes: 2048, max_lines: 40, files: [f('Memory/agents/gone/index.md', { index: true, lines: 1 })] },
]

// An agent as the wizard creates it (the blank editor form of a new agent): it
// reads yours and its own notes, saves to its own, and keeps the connector grants.
const DEFAULT_AGENT = { ...AGENTS[1], memory: 'mine_and_own', builtins: ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload', 'memory.write'] }

async function serve(page, { enabled = true, agents = AGENTS, groups = GROUPS, save = 'ask' } = {}) {
  const s = await installAgents(page, { agents })
  await page.route('**/api/chats/agents/builtin-grants', route => route.fulfill({ json: { grants: GRANTS } }))
  await page.route('**/api/chats/memory', route => route.fulfill({ json: { enabled, enabled_set: true, save_mode: save, messaging_save: 'ask', save_mode_set: false, provider: 'builtin', available: true } }))
  await page.route('**/api/chats/context-files', route => route.fulfill({ json: { limits: LIMITS, groups } }))
  await page.route('**/api/chats/memory/export*', route => route.fulfill({ status: 200, contentType: 'application/zip', body: 'PK' }))
  // A delete that says what to do with the notes carries a query, which the shared stub does not match.
  await page.route(/\/api\/chats\/agents\/[^/?]+\?memory=/, (route) => {
    const u = new URL(route.request().url())
    s.calls.push({ method: route.request().method(), url: u.pathname, search: u.search, body: null })
    return route.fulfill({ status: 204, body: '' })
  })
  return s
}

test.describe('Memory section of the agent form', () => {
  // The old "Let it save notes" checkbox (agent-memory-saves) and the None card
  // (agent-memory-none) are the level control now: Off, Reads notes, Reads and
  // saves notes (agent-memory-level-off / -reads / -saves).
  test('an agent with the default notes is at Reads and saves notes with Mine and its own, and sends them', async ({ page }) => {
    const s = await serve(page, { agents: [DEFAULT_AGENT] })
    await page.goto('/app/agents/a2/edit')
    const choice = page.getByTestId('agent-memory-choice')
    await expect(choice.getByTestId('agent-memory-level-saves').locator('input')).toBeChecked()
    await expect(choice.getByTestId('agent-memory-mine_and_own').locator('input')).toBeChecked()
    await expect(choice.getByTestId('agent-memory-mine_and_own')).toContainText('Recommended')
    await expect(page.getByTestId('agent-memory-before')).toHaveCount(0)
    await expect(fold(page, 'Notes')).toContainText('Reads and saves notes, Mine and its own')
    await shot(page, 'agent-memory-new', { fullPage: true })
    await page.getByLabel('Name', { exact: false }).first().fill('Helper')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'PUT').body
    expect(body.memory).toBe('mine_and_own')
    expect(body.builtins).toContain('memory.write')
  })

  test('an agent saved before shows As before, and picking a choice sends it with the grant', async ({ page }) => {
    const s = await serve(page)
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-before')).toContainText('As before')
    await expect(fold(page, 'Notes')).toContainText('As before')
    await expect(page.getByTestId('agent-memory-whose').locator('input[type=radio]:checked')).toHaveCount(0)
    await shot(page, 'agent-memory-as-before', { fullPage: true })
    // Saving without choosing sends no choice: the server keeps the stored one.
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect('memory' in s.calls.find(c => c.method === 'PUT').body).toBe(false)

    await page.goto('/app/agents/a2/edit')
    await page.getByTestId('agent-memory-level-saves').click()
    await page.getByTestId('agent-memory-mine').click()
    await expect(page.getByTestId('agent-memory-mine-warn')).toContainText('You confirm each one')
    await expect(page.getByTestId('agent-memory-before')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const puts = s.calls.filter(c => c.method === 'PUT')
    expect(puts[puts.length - 1].body.memory).toBe('mine')
    expect(puts[puts.length - 1].body.builtins).toContain('memory.write')
  })

  test('choosing whose notes changes only that, not the level', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['skills.write'] }] })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-level-reads').locator('input')).toBeChecked()
    await page.getByTestId('agent-memory-mine').click()
    await expect(page.getByTestId('agent-memory-level-reads').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-mine-warn')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const put = s.calls.find(c => c.method === 'PUT').body
    expect(put.memory).toBe('mine')
    expect(put.builtins).toEqual(['skills.write'])
  })

  test('says that memory is off in the settings of the person', async ({ page }) => {
    await serve(page, { enabled: false })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-user-off')).toContainText('Memory is off in your settings')
    await shot(page, 'agent-memory-user-off', { fullPage: true })
  })
})

test.describe('The save rule of the agent', () => {
  const open = async (page, path) => {
    await page.goto(path)
  }

  test('offers four cards, says what is in force, and sends nothing until it is changed', async ({ page }) => {
    // The rule shows only while the agent saves notes: this one holds the right.
    const s = await serve(page, { save: 'auto', agents: [{ ...AGENTS[1], builtins: ['memory.write'] }] })
    await open(page, '/app/agents/a2/edit')
    const rule = page.getByTestId('agent-memory-rule')
    await expect(rule.getByRole('radio')).toHaveCount(4)
    await expect(rule.locator('.am__title')).toHaveText(['Use my setting', 'Save it and tell me', 'Always confirm', 'Never'])
    await expect(page.getByTestId('agent-memory-rule-use_mine')).toContainText('Your setting is Save it and tell me. Change it in Settings > Memory.')
    await expect(page.getByTestId('agent-memory-user-level')).toHaveText('Save it and tell me')
    await expect(page.getByTestId('agent-memory-settings-link')).toHaveAttribute('href', '/app/account?tab=memory')
    await expect(page.getByTestId('agent-memory-rule-use_mine').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-rule-auto').locator('input')).not.toBeChecked()
    await expect(page.getByTestId('agent-memory-rule-hint')).toContainText('verified person')
    await shot(page, 'agent-memory-rule', { fullPage: true })
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect('memory_save' in s.calls.find(c => c.method === 'PUT').body).toBe(false)
  })

  test('sends always, then never, then use_mine to clear it', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', memory_save: 'always', builtins: ['memory.write'] }] })
    await open(page, '/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-rule-always').locator('input')).toBeChecked()
    await page.getByTestId('agent-memory-rule-never').click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.filter(c => c.method === 'PUT').at(-1).body.memory_save).toBe('never')
    await open(page, '/app/agents/a2/edit')
    await page.getByTestId('agent-memory-rule-use_mine').click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.filter(c => c.method === 'PUT').at(-1).body.memory_save).toBe('use_mine')
  })

  test('sends auto for Save it and tell me, and shows it in the Effective view and the Notes chip', async ({ page }) => {
    const s = await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: ['memory.write'] }], save: 'ask' })
    await open(page, '/app/agents/a2/edit')
    await page.getByTestId('agent-memory-rule-auto').click()
    await expect(page.getByTestId('agent-memory-rule-auto').locator('input')).toBeChecked()
    await expect(page.getByTestId('agent-memory-rule-auto')).toContainText('even when your setting is Ask me first')
    await expect(page.getByTestId('agent-memory-rule-use_mine')).toContainText('Your setting is Ask me first.')
    await expect(page.getByText('Saves notes at once and tells you, with an Undo. This is the agent\'s own choice.')).toBeVisible()
    await expect(page.locator('.aed-chip__v').filter({ hasText: 'saves at once' })).toBeVisible()
    await shot(page, 'agent-memory-rule-auto', { fullPage: true })
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.filter(c => c.method === 'PUT').at(-1).body.memory_save).toBe('auto')
  })

  test('is hidden when the agent has no memory, with the reason', async ({ page }) => {
    await serve(page, { agents: [{ ...AGENTS[1], memory: 'none' }] })
    await open(page, '/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-rule')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-rule-off')).toContainText('memory is off')
  })

  test('is hidden when the agent only reads, with the reason', async ({ page }) => {
    await serve(page, { agents: [{ ...AGENTS[1], memory: 'own', builtins: [] }] })
    await open(page, '/app/agents/a2/edit')
    await expect(page.getByTestId('agent-memory-rule')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-rule-off')).toContainText('reads notes but cannot save')
  })
})

test.describe('Deleting an agent that has notes', () => {
  const del = async (page, choice) => {
    await page.goto('/app/agents')
    await page.getByRole('button', { name: /research-assistant/ }).first().waitFor({ state: 'attached' }).catch(() => {})
    await page.getByRole('button', { name: 'Actions for research-assistant' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    const dialog = page.getByTestId('agent-delete-notes')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('This agent has notes of its own (2)')
    await expect(dialog.getByTestId('agent-delete-notes-keep').locator('input')).toBeChecked()
    return dialog
  }

  test('asks, keeps the notes by default, and sends memory=keep after the Undo window', async ({ page }) => {
    const s = await serve(page)
    const dialog = await del(page, 'keep')
    await shot(page, 'agent-delete-notes', {})
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete agent' }).click()
    await expect(page.getByTestId('agent-undo-toast')).toBeVisible()
    await expect(dialog).toHaveCount(0)
    await expect.poll(() => s.calls.find(c => c.method === 'DELETE')?.search, { timeout: 15_000 }).toBe('?memory=keep')
  })

  test('delete the notes too sends memory=delete', async ({ page }) => {
    const s = await serve(page)
    const dialog = await del(page, 'delete')
    await dialog.getByTestId('agent-delete-notes-delete').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete agent' }).click()
    await expect.poll(() => s.calls.find(c => c.method === 'DELETE')?.search, { timeout: 15_000 }).toBe('?memory=delete')
  })

  test('export downloads the notes first, then deletes them', async ({ page }) => {
    const s = await serve(page)
    const dialog = await del(page, 'export')
    await dialog.getByTestId('agent-delete-notes-export').click()
    const download = page.waitForEvent('download')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete agent' }).click()
    expect((await download).url()).toContain('/api/chats/memory/export?scope=agent&key=a1')
    await expect.poll(() => s.calls.find(c => c.method === 'DELETE')?.search, { timeout: 15_000 }).toBe('?memory=delete')
  })

  test('Cancel deletes nothing', async ({ page }) => {
    const s = await serve(page)
    await del(page, 'keep')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('agent-delete-notes')).toHaveCount(0)
    expect(s.calls.some(c => c.method === 'DELETE')).toBe(false)
  })

  test('an agent with no notes is deleted as before, with no question', async ({ page }) => {
    await serve(page, { groups: [GROUPS[0]] })
    await page.goto('/app/agents')
    await page.getByRole('button', { name: 'Actions for research-assistant' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await expect(page.getByTestId('agent-undo-toast')).toBeVisible()
    await expect(page.getByTestId('agent-delete-notes')).toHaveCount(0)
  })
})

test.describe('Notes list tabs', () => {
  test('has a tab for yours, each agent and the deleted agents', async ({ page }) => {
    await serve(page)
    await page.goto('/app/account?tab=memory')
    const tabs = page.getByTestId('memory-tabs')
    await expect(tabs.getByRole('tab')).toHaveText(['Mine', 'research-assistant', 'Deleted agents'])
    await expect(tabs.getByRole('tab', { name: 'Mine' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('memory-group')).toHaveCount(1)
    await tabs.getByRole('tab', { name: 'research-assistant' }).click()
    await expect(page.getByTestId('memory-group').getByRole('heading')).toHaveText('Notes of agent research-assistant')
    await expect(page.getByTestId('memory-file')).toHaveCount(2)
    await shot(page, 'memory-tabs-agent', { fullPage: true })
    const exported = page.waitForEvent('download')
    await page.getByTestId('memory-tab-export').click()
    expect((await exported).url()).toContain('/api/chats/memory/export?scope=agent&key=a1')
    await tabs.getByRole('tab', { name: 'Deleted agents' }).click()
    await expect(page.getByTestId('memory-deleted-note')).toContainText('These agents were deleted')
    await expect(page.getByTestId('memory-group').getByRole('heading')).toHaveText('Notes of agent gone')
    await shot(page, 'memory-tabs-deleted', { fullPage: true })
    // Arrow keys move between the tabs.
    await tabs.getByRole('tab', { name: 'Deleted agents' }).focus()
    await page.keyboard.press('ArrowLeft')
    await expect(tabs.getByRole('tab', { name: 'research-assistant' })).toBeFocused()
  })

  test('shows no tabs when there are only your notes', async ({ page }) => {
    await serve(page, { groups: [GROUPS[0]] })
    await page.goto('/app/account?tab=memory')
    await expect(page.getByTestId('memory-group')).toHaveCount(1)
    await expect(page.getByTestId('memory-tabs')).toHaveCount(0)
  })
})
