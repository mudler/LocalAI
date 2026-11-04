import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS, HOME_FOLDERS } from './agents-fixtures.js'
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
    await page.getByTestId('agent-skills-panel').getByRole('switch', { name: /citations/ }).click()

    await fold(page, 'Tools').locator('.ag-fold__button').click()
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: /Docs/ }).click()

    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body).toEqual({
      name: 'Reviewer', description: 'Checks a change', instructions: 'Answer in French.', model: '',
      mcp_servers: ['connector:m1'], addons: [], collections: ['handbook'], skills: ['citations'], builtins_off: [], builtins_on: [], builtins: [], network: 'off',
    })
  })

  test('warns when the first permission turns off the messaging defaults, and adds the missing ones in one click', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Grants').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-grants-notice')).toHaveCount(0)
    await page.getByTestId('agent-grant').filter({ hasText: 'Remember things' }).getByRole('checkbox').check()
    const notice = page.getByTestId('agent-grants-notice')
    await expect(notice).toContainText('triggers.propose, triggers.run, talkback.channels, files.upload')
    await page.getByTestId('agent-grants-add-missing').click()
    await expect(notice).toHaveCount(0)
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const put = s.calls.find(c => c.method === 'PUT')
    expect(put.body.builtins.sort()).toEqual(['files.upload', 'memory.write', 'talkback.channels', 'triggers.propose', 'triggers.run'])
  })

  test('shows no warning for an agent that already had permissions', async ({ page }) => {
    await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Grants').locator('.ag-fold__button').click()
    await page.getByTestId('agent-grant').filter({ hasText: 'Propose triggers' }).getByRole('checkbox').check()
    await expect(page.getByTestId('agent-grants-notice')).toHaveCount(0)
  })

  test('sets the network of the commands of unattended runs', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Builder')
    await fold(page, 'Tools').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-network')).toHaveValue('off')
    await page.getByTestId('agent-network').selectOption('registries')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body.network).toBe('registries')
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

  test('the tools list is the list of the chat: connectors by name, one that was deleted stays, and the servers are free text', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    await fold(page, 'Tools').locator('.ag-fold__button').click()
    const picker = page.getByTestId('agent-tools-panel')
    await expect(picker.getByRole('switch', { name: /Docs/ })).toBeChecked()
    await expect(picker.getByRole('switch', { name: /Parked/ })).not.toBeChecked()
    await fold(page, 'MCP').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-mcp').getByRole('listitem')).toHaveText(['crm'])
    await expect(page.locator('datalist option[value="mail"]')).toHaveCount(1)
    await expect(picker.getByTestId('agent-tools-item').filter({ hasText: 'Parked' })).toContainText('off')
    await picker.getByRole('switch', { name: /Parked/ }).click()
    await page.getByRole('combobox', { name: 'MCP servers +' }).fill('mail')
    await page.getByRole('combobox', { name: 'MCP servers +' }).press('Enter')
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.mcp_servers).toEqual(['crm', 'mail', 'connector:m1', 'connector:m2'])
  })

  test('every switch of the tools list says what it does, and the switches are saved with the agent', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    await fold(page, 'Tools').locator('.ag-fold__button').click()
    const panel = page.getByTestId('agent-tools-panel')
    // The same words as the Tools menu of a chat: a title, a description, and
    // the add-on's declared title instead of its id. Eight rows are the built-in
    // groups and add-ons; the others are the six grants the agent may hold.
    const rows = panel.getByTestId('agent-tools-item')
    await expect(rows).toHaveCount(14)
    for (const row of await rows.all()) await expect(row.getByTestId('agent-tools-blurb')).not.toBeEmpty()
    await expect(panel.getByRole('switch', { name: 'Memory and profile changes' })).toBeChecked()
    await expect(panel.getByRole('switch', { name: 'Office' })).toBeChecked()
    await expect(panel).toContainText('Read and write documents and spreadsheets.')
    await expect(panel.getByRole('switch', { name: 'Saved agents' })).not.toBeChecked()

    await panel.getByRole('switch', { name: 'Triggers', exact: true }).click()
    // Once off, the row also says what the model loses.
    await expect(panel.getByTestId('agent-tools-group-builtins').getByTestId('agent-tools-item').filter({ hasText: 'Triggers' })).toContainText('The model cannot create or manage triggers.')
    await panel.getByRole('switch', { name: 'Saved agents' }).click()
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body).toMatchObject({ builtins_off: ['triggers'], builtins_on: ['saved_agents'], addons: ['office'] })
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

  test('imports an exported agent and the export of an older agent through the server, and names what was dropped', async ({ page }) => {
    const s = await installAgents(page)
    const dir = await mkdtemp(join(tmpdir(), 'agent-import-'))
    const exported = join(dir, 'new.json')
    const file = { version: 1, kind: 'localai-agent', agent: { name: 'Imported', description: 'd', instructions: 'Be brief.', model: '', mcp_servers: [], addons: [], collections: ['handbook'], skills: ['citations'] } }
    await writeFile(exported, JSON.stringify(file))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(exported)
    // The server makes the agent, and the page opens it.
    await expect(page).toHaveURL(/\/app\/agents\/a3$/)
    await expect(page.getByRole('heading', { name: 'Imported', level: 1 })).toBeVisible()
    await expect(page.getByText('Agent "Imported" imported')).toBeVisible()
    expect(s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/agents/import').body).toEqual(file)

    // An export of the old agents is turned into a definition first, and what
    // a saved agent has no place for is named.
    const legacy = join(dir, 'old.json')
    await writeFile(legacy, JSON.stringify({ name: 'helper', model: 'qwen3-8b', system_prompt: 'Be kind.', selected_skills: ['citations'], actions: [{ name: 'web_search' }], enable_kb: true }))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(legacy)
    await expect(page).toHaveURL(/\/app\/agents\/a4$/)
    await expect(page.getByText('Not imported: actions, enable_kb.')).toBeVisible()
    const sent = s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/agents/import')[1].body
    expect(sent).toMatchObject({ name: 'helper', model: 'qwen3-8b', instructions: 'Be kind.', skills: ['citations'] })
    expect(sent).not.toHaveProperty('system_prompt')
  })

  test('says what the server changed in an imported agent', async ({ page }) => {
    await installAgents(page, { importWarnings: ['the model "model-from-elsewhere" is not available to you here, so the agent has no model'] })
    const dir = await mkdtemp(join(tmpdir(), 'agent-import-'))
    const path = join(dir, 'shared.json')
    await writeFile(path, JSON.stringify({ version: 1, kind: 'localai-agent', agent: { name: 'Shared', instructions: 'Hi', model: 'model-from-elsewhere' } }))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(path)
    await expect(page).toHaveURL(/\/app\/agents\/a3$/)
    await expect(page.getByText(/is not available to you here/)).toBeVisible()
  })

  test('a file that is not an agent is refused with a message', async ({ page }) => {
    const s = await installAgents(page)
    const dir = await mkdtemp(join(tmpdir(), 'agent-import-'))
    const bad = join(dir, 'bad.json')
    await writeFile(bad, JSON.stringify({ hello: 'world' }))
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(bad)
    // The server reads the file and refuses it; nothing is made.
    await expect(page.getByText(/the body is not a valid agent file/)).toBeVisible()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.agents).toHaveLength(2)
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

// The folder of an agent: the Files fold, the picker, the mode and the
// missing state. The listing follows the folder asked for (HOME_FOLDERS).
const ACME_READ = { path: '/Projects/Acme', name: 'Acme', mode: 'read', state: 'ok' }
const withFolder = (folder) => AGENTS.map(a => (a.id === 'a2' ? { ...a, folder } : a))

test.describe('Agent folder', () => {
  test('choose a folder and save shows it on the card', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS })
    await page.goto('/app/agents/a2/edit')
    await expect(fold(page, 'Files')).toContainText('All my files')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-scope-one').click()
    // Nothing chosen yet: the fold needs the person, and Save says so.
    await expect(fold(page, 'Files')).toContainText('Choose a folder, or pick All my files.')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    expect(s.calls.some(c => c.method === 'PUT')).toBe(false)

    await page.getByTestId('agent-folder-choose').click()
    const dialog = page.getByTestId('agent-folder-dialog')
    await expect(dialog.getByTestId('folder-picker')).toBeVisible()
    await dialog.getByRole('button', { name: 'Projects' }).click()
    await dialog.getByRole('button', { name: 'Acme' }).click()
    // An empty folder says so, and is still the one chosen.
    await expect(dialog).toContainText('No folders here')
    await dialog.getByTestId('agent-folder-use').click()
    await expect(page.getByTestId('agent-folder-path')).toHaveText('/Projects/Acme')
    await expect(fold(page, 'Files')).toContainText('/Projects/Acme, can read')

    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body.folders).toEqual([{ path: '/Projects/Acme', mode: 'read' }])
    await expect(page.locator('[data-agent="code-reviewer"]').getByTestId('agent-folder-chip')).toContainText('Acme, read only')
  })

  test('read only shows the lock chip, and the mode switch sends write', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2')
    const chip = page.getByTestId('agent-facts').getByTestId('agent-folder-chip')
    await expect(chip).toContainText('Acme, read only')
    await expect(chip.locator('[data-icon="lock"]')).toHaveCount(1)

    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-folder-mode-0-read')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('agent-folder-mode-0-write').click()
    await expect(fold(page, 'Files')).toContainText('1 changed')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body.folders).toEqual([{ path: '/Projects/Acme', mode: 'write' }])
    const row = page.locator('[data-agent="code-reviewer"]').getByTestId('agent-folder-chip')
    await expect(row).toContainText('Acme, read and change')
    await expect(row.locator('[data-icon="folder"]')).toHaveCount(1)
  })

  test('an unchanged folder is not sent, and All my files clears it', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2/edit')
    await page.getByLabel('Description').fill('Reviews and lists.')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body).not.toHaveProperty('folders')

    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-scope-all').click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.filter(c => c.method === 'PUT')[1].body.folders).toEqual([])
    await expect(page.locator('[data-agent="code-reviewer"]').getByTestId('agent-folder-chip')).toHaveCount(0)
  })

  test('a missing folder shows the inline error and the warning chip', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder({ mode: 'write', state: 'missing' }) })
    await page.goto('/app/agents')
    const chip = page.locator('[data-agent="code-reviewer"]').getByTestId('agent-folder-chip')
    await expect(chip).toHaveText('Folder missing')

    await page.goto('/app/agents/a2')
    await expect(page.getByTestId('agent-folder-warning')).toContainText('This folder is gone. Choose another folder to use this agent.')
    await page.getByTestId('agent-facts').getByTestId('agent-folder-chip').click()
    await expect(page).toHaveURL(/\/app\/agents\/a2\/edit$/)

    // The fold says so; the save of another field keeps working and keeps it.
    await expect(fold(page, 'Files')).toContainText('Folder missing')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-folder-missing')).toContainText('This folder is gone. Choose another folder to use this agent.')
    await page.getByLabel('Description').fill('Still saves.')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body).not.toHaveProperty('folders')

    // Choosing another folder sends it with the mode the agent had.
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-missing').getByRole('button', { name: 'Choose another folder' }).click()
    await page.getByTestId('agent-folder-dialog').getByRole('button', { name: 'Projects' }).click()
    await page.getByTestId('agent-folder-use').click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.filter(c => c.method === 'PUT')[1].body.folders).toEqual([{ path: '/Projects', mode: 'write' }])
  })

  test('the picker cannot choose Chats or LocalAI, and all the files can be chosen', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS })
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Reader')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-scope-one').click()
    await page.getByTestId('agent-folder-choose').click()
    const dialog = page.getByTestId('agent-folder-dialog')
    for (const name of ['Chats', 'LocalAI']) {
      const row = dialog.getByRole('button', { name: new RegExp(`^${name}`) })
      await expect(row).toBeDisabled()
      await expect(row).toContainText('LocalAI manages this folder')
    }
    await expect(dialog.getByRole('button', { name: /^Projects/ })).toBeEnabled()
    // A file is no folder: the picker lists folders only.
    await expect(dialog).not.toContainText('notes.txt')
    await dialog.getByTestId('agent-folder-use').click()
    await expect(page.getByTestId('agent-folder-path')).toHaveText('Home')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body.folders).toEqual([{ path: '/', mode: 'read' }])
  })

  test('several folders: add, per-row mode, remove, and the cap of eight', async ({ page }) => {
    const names = ['DirA', 'DirB', 'DirC', 'DirD', 'DirE', 'DirF', 'DirG', 'DirH']
    const files = { ...HOME_FOLDERS }
    files['/'] = [...HOME_FOLDERS['/'], ...names.map(n => ({ path: `/${n}`, name: n, kind: 'dir' }))]
    for (const n of names) files[`/${n}`] = []
    const s = await installAgents(page, { files })
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Many')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-scope-one').click()
    const pick = async (name, choose) => {
      await choose.click()
      const dialog = page.getByTestId('agent-folder-dialog')
      await dialog.getByRole('button', { name: new RegExp(`^${name}`) }).click()
      await dialog.getByTestId('agent-folder-use').click()
    }
    await pick('DirA', page.getByTestId('agent-folder-choose'))
    await page.getByTestId('agent-folder-mode-0-write').click()
    await page.getByTestId('agent-folder-add').click()
    {
      const d = page.getByTestId('agent-folder-dialog')
      await d.getByRole('button', { name: /^DirB/ }).click()
      await d.getByTestId('agent-folder-use').click()
    }
    await expect(page.getByTestId('agent-folder-row')).toHaveCount(2)
    await expect(page.getByTestId('agent-folder-count')).toHaveText('2 of 8 folders')
    // The second row keeps its own mode.
    await expect(page.getByTestId('agent-folder-mode-1-read')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-folder-mode-0-write')).toHaveAttribute('aria-pressed', 'true')
    for (const n of ['DirC', 'DirD', 'DirE', 'DirF', 'DirG', 'DirH']) {
      await page.getByTestId('agent-folder-add').click()
      const d = page.getByTestId('agent-folder-dialog')
      await d.getByRole('button', { name: new RegExp(`^${n}`) }).click()
      await d.getByTestId('agent-folder-use').click()
    }
    await expect(page.getByTestId('agent-folder-row')).toHaveCount(8)
    await expect(page.getByTestId('agent-folder-add')).toBeDisabled()
    await page.getByTestId('agent-folder-remove').last().click()
    await expect(page.getByTestId('agent-folder-add')).toBeEnabled()
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'POST').body
    expect(body.folders).toHaveLength(7)
    expect(body.folders[0]).toEqual({ path: '/DirA', mode: 'write' })
    expect(body.folders[1]).toEqual({ path: '/DirB', mode: 'read' })
    await expect(page.locator('[data-agent="Many"]').getByTestId('agent-folder-chip')).toContainText('+6')
  })

  test('a row added and cancelled goes away, and a folder inside another shows the outer mode', async ({ page }) => {
    await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-add').click()
    await page.getByTestId('agent-folder-dialog').getByRole('button', { name: 'Cancel' }).first().click()
    await expect(page.getByTestId('agent-folder-row')).toHaveCount(1)

    // /Projects holds /Projects/Acme: the inner folder works at the outer mode.
    await page.getByTestId('agent-folder-add').click()
    const dialog = page.getByTestId('agent-folder-dialog')
    await dialog.getByRole('button', { name: /^Projects/ }).click()
    await dialog.getByTestId('agent-folder-use').click()
    await page.getByTestId('agent-folder-mode-1-write').click()
    await expect(page.getByTestId('agent-folder-effective')).toHaveText('Inside a folder that can be changed, so this one can be changed too.')
  })

  test('an agent with several folders reads them back, and a not-chosen row blocks the save', async ({ page }) => {
    const rows = [ACME_READ, { path: '/Projects/Beta', name: 'Beta', mode: 'write', state: 'ok', effective: 'write' }]
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ).map(a => (a.id === 'a2' ? { ...a, folders: rows } : a)) })
    await page.goto('/app/agents/a2/edit')
    await expect(fold(page, 'Files')).toContainText('2 folders')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-folder-row')).toHaveCount(2)
    await page.getByTestId('agent-folder-add').click()
    await page.getByTestId('agent-folder-dialog').getByRole('button', { name: 'Cancel' }).first().click()
    await page.getByTestId('agent-folder-remove').first().click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body.folders).toEqual([{ path: '/Projects/Beta', mode: 'write' }])
  })

  test('a folder that cannot be read says so and tries again', async ({ page }) => {
    let fail = true
    await installAgents(page, { files: HOME_FOLDERS })
    await page.route('**/api/chats/files*', route => {
      if (fail) { fail = false; return route.fulfill({ status: 500, json: { error: { message: 'boom' } } }) }
      return route.fallback()
    })
    await page.goto('/app/agents/new')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder-scope-one').click()
    await page.getByTestId('agent-folder-choose').click()
    const dialog = page.getByTestId('agent-folder-dialog')
    await expect(dialog.getByRole('alert')).toContainText('Could not read this folder.')
    await dialog.getByRole('button', { name: 'Try again' }).click()
    await expect(dialog.getByRole('button', { name: /^Projects/ })).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel' }).first().click()
    await expect(dialog).toHaveCount(0)
  })

  test('the Export fold says that the file names the folder', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'export').locator('.ag-fold__button').click()
    await expect(page.getByTestId('agent-export-folder')).toContainText('The file names the folder /Projects/Acme.')
    const download = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export agent' }).click()
    expect((await download).suggestedFilename()).toBe('code-reviewer.json')
    expect(s.calls.some(c => c.url === '/api/chats/agents/a2/export')).toBe(true)
    await expect(page.getByText(/The file names its folder, \/Projects\/Acme/)).toBeVisible()
  })

  test('no copy of the folder fold names an internal path', async ({ page }) => {
    await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2/edit')
    await fold(page, 'Files').locator('.ag-fold__button').click()
    await page.getByTestId('agent-folder').getByRole('button', { name: 'Change', exact: true }).click()
    await expect(page.getByTestId('folder-picker')).toBeVisible()
    const text = await page.locator('body').innerText()
    expect(text).not.toContain('/workspace/')
    expect(text).not.toMatch(/\bnode\b/i)
  })
})

const aliasModels = [
  { id: 'production', capabilities: ['FLAG_CHAT'] },
  { id: 'alternate', capabilities: ['FLAG_CHAT'] },
  { id: 'embedding', capabilities: ['FLAG_EMBEDDINGS'] },
  { id: 'disabled', capabilities: ['FLAG_CHAT'], disabled: true },
  { id: 'broken', capabilities: [] },
  { id: 'chained', capabilities: [] },
]
const openModel = async page => {
  await fold(page, 'ModelSettings').locator('.ag-fold__button').click()
  return page.getByTestId('home-model-chip')
}

test('create uses the chat picker, filters unusable models and persists the alias ID', async ({ page }) => {
  const s = await installAgents(page, { models: aliasModels })
  const aliasRequests = []
  page.on('request', r => { if (new URL(r.url()).pathname.startsWith('/api/aliases')) aliasRequests.push(r.url()) })
  await page.goto('/app/agents/new')
  await page.getByLabel('Name', { exact: false }).first().fill('Alias user')
  const chip = await openModel(page)
  await expect(chip).toContainText('Any model')
  await chip.click()
  const options = page.getByRole('listbox').getByRole('option')
  await expect(options.locator('code')).toHaveText(['Any model (the chat picks one)', 'production', 'alternate'])
  await expect(options.locator('small')).toHaveText(['not loaded', 'not loaded'])
  await options.filter({ has: page.getByText('production', { exact: true }) }).click()
  await page.getByRole('button', { name: 'Create Agent' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  const created = s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/agents')
  expect(created.body.model).toBe('production')
  const id = s.agents.find(a => a.name === 'Alias user').id
  await page.goto(`/app/agents/${id}/edit`)
  await expect(await openModel(page)).toContainText('production')
  await page.reload()
  await expect(await openModel(page)).toContainText('production')
  expect(aliasRequests).toEqual([])
})

test('create can pick an alias, shows what it stands for, and saves the alias name', async ({ page }) => {
  const models = [
    { id: 'production', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
    { id: 'daily', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp', alias_of: 'production' },
    { id: 'lost', capabilities: [], alias_of: 'gone', alias_error: 'alias "lost" points to unknown model "gone"' },
  ]
  const s = await installAgents(page, { models })
  await page.goto('/app/agents/new')
  await page.getByLabel('Name', { exact: false }).first().fill('Alias picker')
  const chip = await openModel(page)
  await chip.click()
  const options = page.getByRole('listbox').getByRole('option')
  await expect(options.filter({ hasText: 'daily' })).toContainText('alias of production')
  await expect(options.filter({ hasText: 'lost' })).toHaveAttribute('aria-disabled', 'true')
  if (process.env.PICKER_SHOTS) await page.waitForTimeout(600)
  if (process.env.PICKER_SHOTS) await page.screenshot({ path: `${process.env.PICKER_SHOTS}/agent-create-picker.png` })
  await options.filter({ hasText: 'daily' }).click()
  await expect(chip).toContainText('daily')
  await page.getByRole('button', { name: 'Create Agent' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/agents').body.model).toBe('daily')
})

test('edit retains the stored alias while capabilities load and through unrelated saves, then can change or clear it', async ({ page }) => {
  const s = await installAgents(page, { models: aliasModels, agents: [{ ...AGENTS[0], model: 'production' }] })
  let release
  const loading = new Promise(resolve => { release = resolve })
  await page.route('**/api/models/capabilities', async route => { await loading; await route.fulfill({ json: { data: aliasModels } }) })
  await page.goto('/app/agents/a1/edit')
  const chip = await openModel(page)
  await expect(chip).toContainText('production')
  release()
  await page.getByLabel('Description').fill('Unrelated edit')
  await page.getByRole('button', { name: 'Save Changes' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(s.calls.find(c => c.method === 'PUT').body.model).toBe('production')
  await page.goto('/app/agents/a1/edit')
  await (await openModel(page)).click()
  await page.getByRole('listbox').getByRole('option').filter({ has: page.getByText('alternate', { exact: true }) }).click()
  await page.getByRole('button', { name: 'Save Changes' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(s.agents[0].model).toBe('alternate')
  await page.goto('/app/agents/a1/edit')
  await expect(await openModel(page)).toContainText('alternate')
  await page.getByTestId('home-model-chip').click()
  await page.getByRole('option', { name: 'Any model (the chat picks one)', exact: true }).click()
  await page.getByRole('button', { name: 'Save Changes' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(s.agents[0].model).toBe('')
  await page.goto('/app/agents/a1/edit')
  await expect(await openModel(page)).toContainText('Any model')
})

test('an unavailable stored alias is not silently replaced', async ({ page }) => {
  const s = await installAgents(page, { models: aliasModels, agents: [{ ...AGENTS[0], model: 'unavailable-alias' }] })
  await page.goto('/app/agents/a1/edit')
  await expect(await openModel(page)).toContainText('unavailable-alias')
  await page.getByLabel('Description').fill('Keep the model ID')
  await page.getByRole('button', { name: 'Save Changes' }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(s.calls.find(c => c.method === 'PUT').body.model).toBe('unavailable-alias')
})
