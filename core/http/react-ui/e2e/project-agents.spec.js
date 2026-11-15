import { test, expect } from './coverage-fixtures.js'
import { installProjectAgents, AGENT_FILE } from './project-agents-fixtures.js'

// The agents of a project: the Agents tab, the editor with the two fixed
// folders and the project's tool limits, the paused states, Reconnect, the
// import that asks for a project, and the project chip in the agents list.

const row = (page, name) => page.getByTestId('project-agent-row').filter({ hasText: name })
const puts = (s, id) => s.calls.filter(c => c.method === 'PUT' && c.id === id)
const posts = (s) => s.agents.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/agents')

test.describe('Project agents: the Agents tab', () => {
  test('lists the agents with their state, files mode and memory chips', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/projects/p1?tab=agents')
    await expect(page.getByTestId('project-agent-row')).toHaveCount(3)
    const editor = row(page, 'Copy editor')
    await expect(editor.getByTestId('project-agent-state')).toHaveText('Ready')
    await expect(editor.getByTestId('project-agent-files')).toHaveText('Can edit files')
    await expect(editor.getByTestId('project-agent-memory')).toContainText('Remembers in the project')
    await expect(editor.getByTestId('project-agent-chat')).toBeEnabled()
    const checker = row(page, 'Asset checker')
    await expect(checker.getByTestId('project-agent-files')).toHaveText('Read only')
    await expect(checker.getByTestId('project-agent-memory')).toHaveCount(0)
    const lost = row(page, 'Client questions')
    await expect(lost.getByTestId('project-agent-state')).toHaveText('Unavailable')
    await expect(lost.getByTestId('project-agent-reason')).toContainText('project folders changed')
    await expect(lost.getByTestId('project-agent-reconnect')).toBeVisible()
    await expect(lost.getByTestId('project-agent-chat')).toHaveCount(0)
    // One primary action in the region.
    await expect(page.getByTestId('project-agent-new')).toHaveCount(1)
  })

  test('the overview counts the agents of the project', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-stat-agents')).toContainText('3')
  })

  test('a project with no agents shows the empty state and one New agent', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/projects/p2?tab=agents')
    const empty = page.getByTestId('project-agents-empty')
    await expect(empty).toContainText('No agents in this project yet')
    await expect(page.getByTestId('project-agent-new')).toHaveCount(1)
    await expect(page.getByTestId('project-agent-new')).toBeEnabled()
  })

  test('an archived project paused its agents: New agent is off and the reason says to restore', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/projects/p3?tab=agents')
    await expect(page.getByTestId('project-agent-new')).toBeDisabled()
    const paused = row(page, 'Podcast notes')
    await expect(paused.getByTestId('project-agent-reason')).toContainText('Restore the project')
    await expect(paused.getByTestId('project-agent-chat')).toBeDisabled()
  })

  test('Reconnect saves the agent again without folders and without a project, then reloads', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/projects/p1?tab=agents')
    await row(page, 'Client questions').getByTestId('project-agent-reconnect').click()
    await expect(row(page, 'Client questions').getByTestId('project-agent-state')).toHaveText('Ready')
    const sent = puts(s, 'ap3')
    expect(sent).toHaveLength(1)
    expect(sent[0].body).not.toHaveProperty('folders')
    expect(sent[0].body).not.toHaveProperty('project_id')
    expect(sent[0].body.name).toBe('Client questions')
    await expect(row(page, 'Client questions').getByTestId('project-agent-chat')).toBeEnabled()
  })
})

test.describe('Project agents: the editor', () => {
  test('New agent opens the editor with the project chosen and the two fixed folders', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/projects/p1?tab=agents')
    await page.getByTestId('project-agent-new').click()
    await expect(page).toHaveURL(/\/app\/agents\/new\?project=p1$/)
    await expect(page.getByTestId('agent-project-chip')).toContainText('Project: Client site redesign')
    await expect(page.getByTestId('agent-project-chip')).toHaveAttribute('href', '/app/projects/p1')
    // Two fixed rows, no folder picker.
    await expect(page.getByTestId('agent-project-files')).toBeVisible()
    await expect(page.getByTestId('agent-project-files-mode')).toHaveValue('write')
    await expect(page.getByTestId('agent-project-memory-mode')).toContainText('Read only')
    await expect(page.getByTestId('agent-folder-add')).toHaveCount(0)
    await expect(page.getByTestId('agent-folder-choose')).toHaveCount(0)
    await page.getByLabel('Name', { exact: false }).first().fill('Page helper')
    await page.getByRole('button', { name: /^Create agent/ }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const sent = posts(s)
    expect(sent).toHaveLength(1)
    expect(sent[0].body.project_id).toBe('p1')
    // The server fills the two roots: no folder list of the page.
    expect(sent[0].body).not.toHaveProperty('folders')
    expect(sent[0].body).not.toHaveProperty('folder')
  })

  test('Read only sends only the two project roots', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents/new?project=p1')
    await page.getByLabel('Name', { exact: false }).first().fill('Reader')
    await page.getByTestId('agent-project-files-mode').selectOption('read')
    await page.getByRole('button', { name: /^Create agent/ }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const body = posts(s)[0].body
    expect(body.project_id).toBe('p1')
    expect(body.folders).toEqual([
      { path: '/Projects/client-site-redesign/files', mode: 'read' },
      { path: '/Projects/client-site-redesign/memory', mode: 'read' },
    ])
  })

  test('the pickers show only what a project with lists allows', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/agents/ap1/edit')
    await expect(page.getByTestId('agent-edit')).toBeVisible()
    const skills = page.getByTestId('agent-skills-panel')
    await expect(skills.getByRole('switch', { name: /citations/ })).toBeVisible()
    await expect(skills.getByRole('switch', { name: /house-style/ })).toHaveCount(0)
    await expect(skills.getByRole('switch', { name: /pdf/ })).toHaveCount(0)
    const mcp = page.getByTestId('agent-mcp-picker')
    await expect(mcp.getByRole('switch', { name: /Docs/ })).toBeVisible()
    await expect(mcp.getByRole('switch', { name: /crm/ })).toBeVisible()
    await expect(mcp.getByRole('switch', { name: /Parked/ })).toHaveCount(0)
    await expect(mcp.getByRole('switch', { name: /mail/ })).toHaveCount(0)
    const collections = page.getByTestId('agent-collections')
    await expect(collections.getByTestId('agent-collection-row')).toHaveCount(1)
    await expect(collections.getByRole('switch', { name: /handbook/ })).toBeVisible()
    await expect(page.getByTestId('agent-limited').first()).toHaveText('Limited by the project')
  })

  test('a project with no lists shows the normal pickers', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/agents/new?project=p2')
    await expect(page.getByTestId('agent-project-chip')).toContainText('Project: Home lab notes')
    const skills = page.getByTestId('agent-skills-panel')
    await expect(skills.getByRole('switch', { name: /citations/ })).toBeVisible()
    await expect(skills.getByRole('switch', { name: /house-style/ })).toBeVisible()
    await expect(page.getByTestId('agent-mcp-picker').getByRole('switch', { name: /Parked/ })).toBeVisible()
    await expect(page.getByTestId('agent-limited')).toHaveCount(0)
    await expect(page.getByRole('combobox', { name: 'Collections +' })).toBeVisible()
  })

  test('saving a project agent never sends a project or folders unless the mode changed', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents/ap1/edit')
    await page.getByLabel('Description').fill('Tightens page copy fast.')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    let sent = s.agents.calls.filter(c => c.method === 'PUT' && c.url.endsWith('/ap1'))
    expect(sent).toHaveLength(1)
    expect(sent[0].body).not.toHaveProperty('project_id')
    expect(sent[0].body).not.toHaveProperty('folders')
    expect(sent[0].body.description).toBe('Tightens page copy fast.')

    await page.goto('/app/agents/ap1/edit')
    await page.getByTestId('agent-project-files-mode').selectOption('read')
    await page.getByRole('button', { name: 'Save changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    sent = s.agents.calls.filter(c => c.method === 'PUT' && c.url.endsWith('/ap1'))
    expect(sent).toHaveLength(2)
    expect(sent[1].body).not.toHaveProperty('project_id')
    expect(sent[1].body.folders.map(f => f.path)).toEqual(['/Projects/client-site-redesign/files', '/Projects/client-site-redesign/memory'])
    expect(sent[1].body.folders.map(f => f.mode)).toEqual(['read', 'read'])
  })

  test('sharing is replaced by a note and export stays', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/agents/ap1/edit')
    await expect(page.getByTestId('agent-share-note')).toContainText('Project agents cannot be shared')
    await expect(page.getByRole('button', { name: 'Export' }).first()).toBeVisible()
    await page.goto('/app/agents/ap1')
    await expect(page.getByTestId('agent-share')).toHaveCount(0)
    await expect(page.getByTestId('agent-no-share')).toContainText('cannot be shared')
    await page.goto('/app/agents/a9')
    await expect(page.getByTestId('agent-share')).toBeVisible()
  })
})

test.describe('Project agents: paused agents', () => {
  test('an agent of an archived project says so and cannot chat', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/agents/ap4')
    await expect(page.getByTestId('agent-paused')).toContainText('This agent is paused because its project is archived')
    await expect(page.getByTestId('agent-chat')).toBeDisabled()
    await expect(page.getByTestId('agent-open-project')).toHaveAttribute('href', '/app/projects/p3')
    await expect(page.getByTestId('agent-project-chip')).toContainText('Podcast episode 14')
    // The editor says the same and does not offer Try it.
    await page.goto('/app/agents/ap4/edit')
    await expect(page.getByTestId('agent-paused')).toContainText('archived')
    await expect(page.getByTestId('agent-try-open')).toBeDisabled()
  })

  test('an agent whose folders changed offers Reconnect on its page', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents/ap3')
    await expect(page.getByTestId('agent-paused')).toContainText('its project is changed')
    await expect(page.getByTestId('agent-chat')).toBeDisabled()
    await page.getByTestId('agent-reconnect').click()
    await expect(page.getByTestId('agent-paused')).toHaveCount(0)
    await expect(page.getByTestId('agent-chat')).toBeEnabled()
    const sent = puts(s, 'ap3')
    expect(sent).toHaveLength(1)
    expect(sent[0].body).not.toHaveProperty('folders')
    expect(sent[0].body).not.toHaveProperty('project_id')
  })

  test('a chat refused with project_unavailable says the agent is paused', async ({ page }) => {
    await installProjectAgents(page)
    await page.route(/\/api\/chats(\?.*)?$/, (route) => {
      if (route.request().method() !== 'POST') return route.fallback()
      return route.fulfill({ status: 409, json: { error: { type: 'project_unavailable', message: 'the project of this agent is not available', code: 409 } } })
    })
    await page.goto('/app/agents/ap1')
    await page.getByTestId('agent-chat').click()
    await expect(page).toHaveURL(/\/app\/chats/)
    const box = page.locator('textarea').first()
    await box.fill('hello')
    await box.press('Enter')
    await expect(page.getByText(/This agent is paused because its project is archived or deleted/)).toBeVisible()
  })
})

test.describe('Project agents: the agents list', () => {
  test('a project agent shows its project, an unavailable one the reason and a disabled Chat', async ({ page }) => {
    await installProjectAgents(page)
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list')).toBeVisible()
    const item = (name) => page.locator(`[data-agent="${name}"]`)
    await expect(item('Copy editor').getByTestId('agent-project-chip')).toContainText('Project: Client site redesign')
    await expect(item('Copy editor').getByTestId('agent-project-chip')).toHaveAttribute('href', '/app/projects/p1')
    await expect(item('Copy editor').getByRole('button', { name: 'Chat' })).toBeEnabled()
    await expect(item('Client questions').getByTestId('agent-unavailable-chip')).toBeVisible()
    await expect(item('Client questions').getByTestId('agent-paused-reason')).toContainText('project folders changed')
    await expect(item('Client questions').getByRole('button', { name: 'Chat' })).toBeDisabled()
    await expect(item('Podcast notes').getByTestId('agent-paused-reason')).toContainText('archived')
    await expect(item('Daily digest').getByTestId('agent-project-chip')).toHaveCount(0)
    await expect(item('Daily digest').getByRole('button', { name: 'Chat' })).toBeEnabled()
  })

  test('with the projects feature off the list shows no chip and no error', async ({ page }) => {
    const s = await installProjectAgents(page)
    s.projects.off = true
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list')).toBeVisible()
    await expect(page.getByTestId('agent-project-chip')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })
})

test.describe('Project agents: importing a file', () => {
  const file = (data = AGENT_FILE) => ({ name: 'triager.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(data)) })

  test('a file of a project agent asks for a project and resends the same file with the choice in the query', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(file())
    const dialog = page.getByTestId('agent-import-project')
    await expect(dialog).toContainText('This agent belongs to a project')
    await expect(dialog).toContainText('Triager')
    // Only active projects are offered.
    const select = dialog.getByTestId('agent-import-project-select')
    await expect(select.locator('option')).toHaveText(['Choose a project', 'Client site redesign', 'Home lab notes', "Create a project named 'Triage'"])
    await select.selectOption('p1')
    await dialog.getByTestId('agent-import-project-go').click()
    await expect(page).toHaveURL(/\/app\/agents\/ai2$/)
    expect(s.imports).toHaveLength(2)
    expect(s.imports[0].search).toBe('')
    expect(s.imports[1].query).toEqual({ project_id: 'p1' })
    // The file is the same text and never carries the mapping.
    expect(s.imports[1].raw).toBe(s.imports[0].raw)
    expect(s.imports[1].raw).not.toContain('project_id')
  })

  test('choosing to create a project sends create_project=true', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(file())
    const dialog = page.getByTestId('agent-import-project')
    await dialog.getByTestId('agent-import-project-select').selectOption({ label: "Create a project named 'Triage'" })
    await dialog.getByTestId('agent-import-project-go').click()
    await expect(page).toHaveURL(/\/app\/agents\/ai2$/)
    expect(s.imports[1].query).toEqual({ create_project: 'true' })
    expect(s.imports[1].raw).toBe(s.imports[0].raw)
  })

  test('Import with no choice shows the error and sends nothing', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(file())
    const dialog = page.getByTestId('agent-import-project')
    await dialog.getByTestId('agent-import-project-go').click()
    await expect(dialog.getByTestId('agent-import-project-error')).toContainText('Choose a project, or create one')
    expect(s.imports).toHaveLength(1)
    await dialog.locator('.dk-dialog-foot').getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(s.imports).toHaveLength(1)
  })

  test('another 4xx error shows as before, with no project dialog', async ({ page }) => {
    const s = await installProjectAgents(page)
    await page.goto('/app/agents')
    await page.locator('input[type=file]').first().setInputFiles(file({ version: 1, kind: 'localai-agent', agent: { description: 'no name' } }))
    await expect(page.getByText(/the body is not a valid agent file/)).toBeVisible()
    await expect(page.getByTestId('agent-import-project')).toHaveCount(0)
    expect(s.imports).toHaveLength(1)
  })
})
