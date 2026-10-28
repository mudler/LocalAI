import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'
import { writeFile, mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// The agent form: folds, preview, import and export, with the saved-agents API stubbed.

const fold = (page, id) => page.locator(`[data-section="${id}"]`)

test.describe('Agent form', () => {
  test('creates an agent from the folds and sends the definition', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/new')
    await expect(page.getByTestId('agent-edit')).toBeVisible()

    await page.getByLabel('Name', { exact: false }).first().fill('Reviewer')
    await page.getByLabel('Description').fill('Checks a change')

    await fold(page, 'PromptsGoals').locator('.ag-fold__button').click()
    await page.getByLabel('Instructions').fill('Answer in French.')

    await fold(page, 'MemorySettings').locator('.ag-fold__button').click()
    await page.getByRole('combobox', { name: 'Collections +' }).fill('handbook')
    await page.getByRole('combobox', { name: 'Collections +' }).press('Enter')
    await expect(page.getByTestId('agent-collections').getByRole('listitem')).toHaveText(['handbook'])

    await fold(page, 'Skills').locator('.ag-fold__button').click()
    await page.getByTestId('agent-skills').getByRole('checkbox', { name: /citations/ }).check()

    await fold(page, 'MCP').locator('.ag-fold__button').click()
    await page.getByTestId('agent-connectors').getByRole('checkbox', { name: /Docs/ }).check()

    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body).toEqual({
      name: 'Reviewer', description: 'Checks a change', instructions: 'Answer in French.', model: '',
      mcp_servers: ['connector:m1'], addons: [], collections: ['handbook'], skills: ['citations'],
    })
  })

  test('needs a name, and says so on the fold', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/new')
    await expect(fold(page, 'BasicInfo')).toContainText('Needs a name')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    expect(s.calls.some(c => c.method === 'POST')).toBe(false)
  })

  test('a starting point fills the name, the description and the instructions', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents/new')
    await page.locator('[data-template="reviewer"]').click()
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('code-reviewer')
    await expect(page.getByLabel('Instructions')).toContainText('You review code changes')
  })

  test('the preview shows the definition and what changed from the saved agent', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('research-assistant')
    await page.getByLabel('Description').fill('Searches and cites.')
    await expect(fold(page, 'BasicInfo')).toContainText('1 changed')

    await page.getByTestId('agent-preview-open').click()
    const sheet = page.getByTestId('agent-preview')
    await expect(sheet.getByTestId('agent-preview-config')).toContainText('"description": "Searches and cites."')
    await sheet.getByRole('tab', { name: /Changes/ }).click()
    const changes = sheet.getByTestId('agent-preview-changes')
    await expect(changes).toContainText('description')
    await expect(changes.locator('[data-side="before"]')).toContainText('Searches the web and writes short briefs.')
    await expect(changes.locator('[data-side="after"]')).toContainText('Searches and cites.')

    await sheet.getByRole('button', { name: 'Save Changes' }).click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT')).toMatchObject({ url: '/api/chats/agents/a1', body: { name: 'research-assistant', description: 'Searches and cites.', mcp_servers: ['crm', 'connector:m1'] } })
  })

  test('the connector picker names connectors, keeps one that was deleted, and the servers are free text', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    await fold(page, 'MCP').locator('.ag-fold__button').click()
    const picker = page.getByTestId('agent-connectors')
    await expect(picker.getByRole('checkbox', { name: /Docs/ })).toBeChecked()
    await expect(picker.getByRole('checkbox', { name: /Parked/ })).not.toBeChecked()
    await expect(picker).toContainText('off')
    await expect(page.getByTestId('agent-mcp').getByRole('listitem')).toHaveText(['crm'])
    await expect(page.locator('datalist option[value="mail"]')).toHaveCount(1)
    await picker.getByRole('checkbox', { name: /Parked/ }).check()
    await page.getByRole('combobox', { name: 'MCP servers +' }).fill('mail')
    await page.getByRole('combobox', { name: 'MCP servers +' }).press('Enter')
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.mcp_servers).toEqual(['crm', 'mail', 'connector:m1', 'connector:m2'])
  })

  test('shows the reason when the server refuses the agent', async ({ page }) => {
    await installAgents(page)
    await page.route(/\/api\/chats\/agents(\?.*)?$/, route => (route.request().method() === 'POST'
      ? route.fulfill({ status: 409, json: { error: { message: 'you already have an agent with this name', type: 'conflict', code: 409 } } })
      : route.fulfill({ json: { agents: [] } })))
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Dup')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page.getByText('you already have an agent with this name')).toBeVisible()
    await expect(page).toHaveURL(/\/app\/agents\/new$/)
  })

  test('imports an exported agent and the export of an older agent, and names what was dropped', async ({ page }) => {
    const s = await installAgents(page)
    const dir = await mkdtemp(join(tmpdir(), 'agent-import-'))
    const exported = join(dir, 'new.json')
    await writeFile(exported, JSON.stringify({ kind: 'localai.chats.agent', version: 1, name: 'Imported', description: 'd', instructions: 'Be brief.', model: '', mcp_servers: [], addons: [], collections: ['handbook'], skills: ['citations'] }))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(exported)
    await expect(page).toHaveURL(/\/app\/agents\/new$/)
    await expect(page.getByTestId('agent-import-note')).toContainText('exported agent')
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('Imported')
    await expect(page.getByLabel('Instructions')).toHaveValue('Be brief.')
    await page.getByRole('button', { name: 'Import Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body).toMatchObject({ name: 'Imported', collections: ['handbook'], skills: ['citations'] })

    const legacy = join(dir, 'old.json')
    await writeFile(legacy, JSON.stringify({ name: 'helper', model: 'qwen3-8b', system_prompt: 'Be kind.', selected_skills: ['citations'], actions: [{ name: 'web_search' }], enable_kb: true }))
    await page.locator('input[type=file]').first().setInputFiles(legacy)
    await expect(page.getByTestId('agent-import-note')).toContainText('older agent')
    await expect(page.getByTestId('agent-import-note')).toContainText('actions, enable_kb')
    await expect(page.getByLabel('Instructions')).toHaveValue('Be kind.')
  })

  test('a file that is not an agent is refused with a message', async ({ page }) => {
    await installAgents(page)
    const dir = await mkdtemp(join(tmpdir(), 'agent-import-'))
    const bad = join(dir, 'bad.json')
    await writeFile(bad, JSON.stringify({ hello: 'world' }))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(bad)
    await expect(page.getByText(/not an exported agent/)).toBeVisible()
    await expect(page).toHaveURL(/\/app\/agents$/)
  })

  test('the Export fold downloads the definition of a saved agent', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    await fold(page, 'export').locator('.ag-fold__button').click()
    const download = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export agent' }).click()
    expect((await download).suggestedFilename()).toBe('research-assistant.json')
  })
})
