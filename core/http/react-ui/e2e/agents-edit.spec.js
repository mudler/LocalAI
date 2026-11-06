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

    await page.getByLabel('Instructions').fill('Answer in French.')

    await page.getByRole('combobox', { name: 'Collections +' }).fill('handbook')
    await page.getByRole('combobox', { name: 'Collections +' }).press('Enter')
    await expect(page.getByTestId('agent-collections').getByRole('listitem')).toHaveText(['handbook'])

    await page.getByTestId('agent-skills-panel').getByRole('switch', { name: /citations/ }).click()

    // Docs is an MCP connector: it is a row of the MCP servers picker in "What it can use", not in the built-in list.
    await page.getByTestId('agent-mcp-picker').getByRole('switch', { name: /Docs/ }).click()

    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body).toEqual({
      name: 'Reviewer', description: 'Checks a change', instructions: 'Answer in French.', model: '',
      mcp_servers: ['connector:m1'], addons: [], collections: ['handbook'], skills: ['citations'], builtins_off: [], builtins_on: [],
      // A new agent reads yours and its own notes and saves to its own; the connector defaults stay.
      builtins: ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload', 'memory.write'], memory: 'mine_and_own', network: 'off',
    })
  })

  test('warns when the first option turns off the messaging defaults, and adds the missing ones in one click', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-grants-notice')).toHaveCount(0)
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: 'Remember things' }).click()
    const notice = page.getByTestId('agent-grants-notice')
    await expect(notice).toContainText('Also propose from messaging apps, Let triggers start from messaging apps, Reply on known channels, Receive files from messaging apps')
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
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: 'Also propose from messaging apps' }).click()
    await expect(page.getByTestId('agent-grants-notice')).toHaveCount(0)
  })

  test('each capability is one switch with its options under it, greyed with a reason while the switch is off', async ({ page }) => {
    const s = await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['triggers.propose', 'act.files'], builtins_off: ['triggers'] }] })
    await page.goto('/app/agents/a2/edit')
    const panel = page.getByTestId('agent-tools-panel')
    // The three trigger switches have names that cannot be mixed up.
    const triggers = panel.getByRole('switch', { name: 'Create and manage triggers' })
    await expect(triggers).not.toBeChecked()
    const propose = panel.getByRole('switch', { name: 'Also propose from messaging apps' })
    const run = panel.getByRole('switch', { name: 'Let triggers start from messaging apps' })
    await expect(propose).toBeDisabled()
    await expect(propose).toBeChecked()
    await expect(propose).toHaveAccessibleDescription(/Turn on .Create and manage triggers. to use this/)
    await expect(run).toBeDisabled()
    await expect(panel.getByRole('group', { name: 'Options of Create and manage triggers' })).toBeVisible()
    await triggers.click()
    await expect(propose).toBeEnabled()
    await run.click()
    // A grant is listed once: files on its own is under Your files, not in a second place.
    await expect(panel.getByRole('switch', { name: 'Write files on its own' })).toHaveCount(1)
    await expect(panel.getByRole('switch', { name: 'Write files on its own' })).toBeChecked()
    // The permissions that apply with nobody watching have a section of their own.
    const quiet = panel.getByTestId('agent-tools-group-unattended')
    await expect(quiet).toContainText('When nobody is watching')
    await expect(quiet.getByRole('switch')).toHaveCount(3)
    await expect(quiet.getByTestId('agent-tools-note').first()).toContainText('Sensitive')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body).toMatchObject({ builtins_off: [], builtins: ['triggers.propose', 'act.files', 'triggers.run'] })
  })

  test('adding to knowledge is a sensitive option under the Knowledge switch, off until the owner turns it on', async ({ page }) => {
    const s = await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    const panel = page.getByTestId('agent-tools-panel')
    const options = panel.getByRole('group', { name: 'Options of Knowledge' })
    await expect(options).toBeVisible()
    const add = options.getByRole('switch', { name: 'Add to knowledge' })
    await expect(add).not.toBeChecked()
    await expect(options).toContainText('Sensitive')
    // It is not also listed in the Suggested changes options or a section of its own.
    await expect(panel.getByRole('switch', { name: 'Add to knowledge' })).toHaveCount(1)
    await add.click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.builtins).toEqual(['memory.write', 'knowledge.write'])
  })

  test('saved agents is one switch: turning it off takes the grant away too, turning it on adds only the group', async ({ page }) => {
    const s = await installAgents(page, { agents: [{ ...AGENTS[1], builtins: ['memory.write', 'saved_agents'] }, { ...AGENTS[0], id: 'a9', name: 'plain' }] })
    await page.goto('/app/agents/a2/edit')
    const sw = page.getByTestId('agent-tools-panel').getByRole('switch', { name: 'Saved agents' })
    await expect(sw).toBeChecked()
    await expect(page.getByTestId('agent-tools-panel').getByText('Call your saved agents')).toHaveCount(0)
    await sw.click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body).toMatchObject({ builtins: ['memory.write'], builtins_on: [] })
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-tools-panel').getByRole('switch', { name: 'Saved agents' }).click()
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect.poll(() => s.calls.filter(c => c.method === 'PUT').at(-1)?.body).toMatchObject({ builtins: [], builtins_on: ['saved_agents'] })
  })

  test('a messaging connection that does not allow what the agent holds is named under that option, with a link', async ({ page }) => {
    const bindings = [
      { id: 'b1', name: 'Bender', kind: 'slack', agent_id: 'a2', enabled: true, caps: [] },
      { id: 'b2', name: 'Other', kind: 'slack', agent_id: 'a1', enabled: true, caps: [] },
      { id: 'b3', name: 'Ready', kind: 'slack', agent_id: 'a2', enabled: true, caps: ['triggers.propose'] },
    ]
    await installAgents(page, { bindings, agents: [{ ...AGENTS[1], builtins: ['triggers.propose', 'memory.write'] }] })
    await page.goto('/app/agents/a2/edit')
    const panel = page.getByTestId('agent-tools-panel')
    const notes = panel.getByTestId('agent-tools-conn-note')
    await expect(notes).toHaveCount(3)
    const propose = panel.getByTestId('agent-tools-item-box').filter({ has: page.getByRole('switch', { name: 'Also propose from messaging apps' }) }).last()
    await expect(propose.getByTestId('agent-tools-conn-note')).toHaveCount(1)
    await expect(propose.getByTestId('agent-tools-conn-note')).toContainText('Slack Bender does not allow this yet.')
    await expect(propose.getByRole('link', { name: 'Open its permissions' })).toHaveAttribute('href', /\/app\/addons\/slack\/apps\?binding=b1$/)
    await expect(propose.getByRole('switch', { name: 'Also propose from messaging apps' })).toHaveAccessibleDescription(/does not allow this yet/)
    await expect(panel).not.toContainText('Slack Other')
    await expect(propose).not.toContainText('Slack Ready')
  })

  test('sets the network of the commands of unattended runs', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Builder')
    await expect(page.getByTestId('agent-network')).toHaveValue('off')
    await page.getByTestId('agent-network').selectOption('registries')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'POST').body.network).toBe('registries')
  })

  test('needs a name, and says so on the fold', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/new')
    await expect(page.locator('[data-group="identity"]')).toContainText('Needs a name')
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
    await expect(page.locator('[data-group="identity"]')).toContainText('1 changed')

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

  test('MCP servers are a picker over what is defined: connectors and the servers of the model, with no free text', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    const picker = page.getByTestId('agent-mcp-picker')
    await expect(picker.getByRole('switch', { name: /Docs/ })).toBeChecked()
    await expect(picker.getByRole('switch', { name: /Parked/ })).not.toBeChecked()
    await expect(picker.getByRole('switch', { name: 'crm' })).toBeChecked()
    await expect(picker.getByRole('switch', { name: 'mail' })).not.toBeChecked()
    // A connector that is switched off says so; nothing can be typed.
    await expect(picker.getByTestId('agent-mcp-row').filter({ hasText: 'Parked' })).toContainText('off')
    await expect(page.getByRole('combobox', { name: 'MCP servers +' })).toHaveCount(0)
    await picker.getByRole('switch', { name: /Parked/ }).click()
    await picker.getByRole('switch', { name: 'mail' }).click()
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.mcp_servers).toEqual(['crm', 'mail', 'connector:m1', 'connector:m2'])
  })

  test('every switch of the tools list says what it does, and the switches are saved with the agent', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a1/edit')
    const panel = page.getByTestId('agent-tools-panel')
    // The same words as the Tools menu of a chat: a title, a description, and
    // the add-on's declared title instead of its id. The built-in list holds the
    // groups and the options and permissions under them; the add-on and the
    // connectors have their own sources now (21 rows, 3 fewer than when one list held all).
    const rows = panel.getByTestId('agent-tools-item')
    await expect(rows).toHaveCount(21)
    for (const row of await rows.all()) await expect(row.getByTestId('agent-tools-blurb')).not.toBeEmpty()
    await expect(panel.getByRole('switch', { name: 'Suggested changes' })).toBeChecked()
    const addons = page.getByTestId('agent-uses-addons')
    await expect(addons.getByRole('switch', { name: 'Office' })).toBeChecked()
    await expect(addons).toContainText('Read and write documents and spreadsheets.')
    await expect(panel.getByRole('switch', { name: 'Saved agents' })).not.toBeChecked()

    await panel.getByRole('switch', { name: 'Create and manage triggers', exact: true }).click()
    // Once off, the row also says what the model loses.
    await expect(panel.getByTestId('agent-tools-group-builtins').getByTestId('agent-tools-item').filter({ hasText: 'Create and manage triggers' }).first()).toContainText('The model cannot propose or manage triggers.')
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

  test('Files is one explicit choice: No files, All my files or Only these folders', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS })
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('Reader')
    const choice = page.getByTestId('agent-folder-scope')
    await expect(choice.getByRole('button')).toHaveText(['No files', 'All my files', 'Only these folders'])
    // A new agent keeps All my files, and no folder is added for it.
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-folder')).toHaveCount(0)

    await page.getByTestId('agent-folder-scope-none').click()
    await expect(page.getByTestId('agent-folder-scope-none')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-folder-none-help')).toContainText('cannot reach any of your files')
    await expect(page.getByTestId('agent-folder')).toHaveCount(0)
    await expect(fold(page, 'Files')).toContainText('No files')
    // The Tools list says the same: the Files switch is off.
    await expect(page.getByTestId('agent-tools-panel').getByRole('switch', { name: /Your files/ })).not.toBeChecked()

    await page.getByTestId('agent-folder-scope-one').click()
    await expect(page.getByTestId('agent-folder')).toBeVisible()
    await expect(page.getByTestId('agent-tools-panel').getByRole('switch', { name: /Your files/ })).toBeChecked()
    // Back to None: the unchosen row does not block the save.
    await page.getByTestId('agent-folder-scope-none').click()
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'POST').body
    expect(body.builtins_off).toContain('files')
    expect(body.folders).toBeUndefined()
  })

  test('No files shows in the Effective view and the Try it list, and clears the folders of a saved agent', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-folder-scope-one')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('agent-try-open').click()
    await expect(page.getByTestId('agent-try-skipped')).toContainText('shared folders')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('agent-try')).toHaveCount(0)
    await page.getByTestId('agent-folder-scope-none').click()
    await expect(page.getByTestId('agent-effective')).toContainText('Cannot reach any of your files.')
    await page.getByTestId('agent-try-open').click()
    await expect(page.getByTestId('agent-try')).toBeVisible()
    await expect(page.getByTestId('agent-try-skipped')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('agent-try')).toHaveCount(0)
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = s.calls.find(c => c.method === 'PUT').body
    expect(body.folders).toEqual([])
    expect(body.builtins_off).toContain('files')
    await expect(page.locator('[data-agent="code-reviewer"]').getByTestId('agent-folder-chip')).toContainText('No files')
  })

  test('existing agents read as before: Files off is No files, a folder list is Only these folders, the rest All my files', async ({ page }) => {
    await installAgents(page, { files: HOME_FOLDERS, agents: AGENTS.map(a => (a.id === 'a2' ? { ...a, builtins_off: ['files'] } : a)) })
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-folder-scope-none')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('agent-folder-scope-all').click()
    await expect(page.getByTestId('agent-folder')).toHaveCount(0)
    await page.goto('/app/agents/a1/edit')
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
  })

  test('No files says that watching a folder needs file access', async ({ page }) => {
    await installAgents(page, { files: HOME_FOLDERS })
    await page.goto('/app/agents/new')
    await page.getByTestId('agent-folder-scope-none').click()
    await expect(page.getByTestId('agent-folder-none-help')).toContainText('Watching a folder needs file access')
  })

  test('read only shows the lock chip, and the mode switch sends write', async ({ page }) => {
    const s = await installAgents(page, { files: HOME_FOLDERS, agents: withFolder(ACME_READ) })
    await page.goto('/app/agents/a2')
    const chip = page.getByTestId('agent-facts').getByTestId('agent-folder-chip')
    await expect(chip).toContainText('Acme, read only')
    await expect(chip.locator('[data-icon="lock"]')).toHaveCount(1)

    await page.goto('/app/agents/a2/edit')
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
    await page.getByTestId('agent-folder-scope-all').click()
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('agent-folder')).toHaveCount(0)
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
    await expect(page.getByTestId('agent-folder-missing')).toContainText('This folder is gone. Choose another folder to use this agent.')
    await page.getByLabel('Description').fill('Still saves.')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    expect(s.calls.find(c => c.method === 'PUT').body).not.toHaveProperty('folders')

    // Choosing another folder sends it with the mode the agent had.
    await page.goto('/app/agents/a2/edit')
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
  // The agent picker now draws the rich row (state and capabilities), so the state is the bold line.
  await expect(options.locator('.home-menu__state b')).toHaveText(['not loaded', 'not loaded'])
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
