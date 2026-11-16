import { test, expect } from './coverage-fixtures.js'
import { CHATS, mockProjects, writes } from './projects-fixtures.js'

// The Projects pages with the API stubbed: the list with its filters and a
// broken row, the New project sheet, a project's tabs, starting a chat in it,
// editing memory, archive and restore, and a failed refresh that keeps rows.

const row = (page, name) => page.getByTestId('project-row').filter({ hasText: name })

// The files API for the folder of the first project: a list that uploads and
// new files change, and a record of the writes.
async function stubProjectFiles(page, entries = [
  { path: '/Projects/client-site-redesign/files/brief.md', name: 'brief.md', kind: 'file', size: 120, version: 1, rev: 1, modified: new Date(Date.now() - 3600_000).toISOString() },
  { path: '/Projects/client-site-redesign/files/assets', name: 'assets', kind: 'dir', size: 0, version: 1, rev: 2, modified: new Date(Date.now() - 7200_000).toISOString() },
]) {
  const fs = { entries: [...entries], puts: [] }
  await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
  await page.route(/\/api\/chats\/files\/content(\?.*)?$/, (route) => {
    const req = route.request()
    const path = new URL(req.url()).searchParams.get('path')
    if (req.method() === 'PUT') {
      const e = { path, name: path.split('/').pop(), kind: 'file', size: (req.postDataBuffer() || []).length, version: 1, rev: 10 + fs.puts.length, modified: new Date().toISOString() }
      fs.puts.push(path)
      fs.entries.push(e)
      return route.fulfill({ json: e })
    }
    return route.fulfill({ status: 200, headers: { 'Content-Type': 'text/markdown' }, body: '# Brief\n' })
  })
  await page.route(/\/api\/chats\/files(\?.*)?$/, route => route.fulfill({ json: { path: new URL(route.request().url()).searchParams.get('path'), entries: fs.entries } }))
  return fs
}

test.describe('Projects', () => {
  test('the list renders its header, rows, counts and the New project action', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects')
    await expect(page.getByRole('heading', { name: 'Projects', level: 1 })).toBeVisible()
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    await expect(page.getByTestId('projects-filter-all')).toContainText('4')
    await expect(page.getByTestId('projects-filter-archived')).toContainText('1')
    await expect(page.getByTestId('project-new')).toHaveCount(1)
    await page.getByTestId('projects-filter-archived').click()
    await expect(page.getByTestId('project-row')).toHaveCount(1)
    await expect(row(page, 'Podcast episode 14')).toBeVisible()
    await page.getByTestId('projects-filter-all').click()
    await page.getByRole('searchbox', { name: 'Search projects' }).fill('home lab')
    await expect(page.getByTestId('project-row')).toHaveCount(1)
  })

  test('an empty list shows the empty state with the one New project action', async ({ page }) => {
    await mockProjects(page, { projects: [] })
    await page.goto('/app/projects')
    const empty = page.getByTestId('projects-empty')
    await expect(empty.getByRole('heading', { name: 'No projects yet' })).toBeVisible()
    await expect(page.getByTestId('project-new')).toHaveCount(1)
  })

  test('a broken project shows its error and a Remove with Undo, and does not crash the page', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects')
    const broken = row(page, 'Kitchen renovation')
    await expect(broken.getByTestId('project-row-error')).toContainText('missing its manifest')
    await expect(broken.locator('a[href^="/app/projects/"]')).toHaveCount(0)
    // The row is honest about what Remove does: the folder goes to the Trash.
    await expect(broken.getByRole('button', { name: 'Remove Kitchen renovation' })).toHaveCount(0)
    await expect(broken.getByTestId('project-row-files')).toHaveAttribute('href', '/app/files?path=/Projects/kitchen-renovation')
    await expect(broken.getByTestId('project-row-files')).toHaveText('Open in Files')
    await expect(broken.getByRole('button', { name: 'Move Kitchen renovation to Trash' })).toHaveText('Move to Trash')
    await broken.getByRole('button', { name: 'Move Kitchen renovation to Trash' }).click()
    await expect(page.getByTestId('project-row')).toHaveCount(3)
    await expect(page.getByTestId('project-undo-toast')).toContainText('Moved “Kitchen renovation” and its files to the Trash. Restore the folder from the Trash in Files.')
    await expect(page.getByTestId('project-undo-toast')).not.toContainText('Deleted')
    await page.getByTestId('project-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    expect(writes(s, 'DELETE', '/projects/p4')).toHaveLength(0)
  })

  test('a failed first load says so with Try again, never an empty list', async ({ page }) => {
    const s = await mockProjects(page)
    s.failList = true
    await page.goto('/app/projects')
    await expect(page.getByRole('alert')).toContainText('Could not load your projects.')
    await expect(page.getByTestId('projects-empty')).toHaveCount(0)
    s.failList = false
    await page.getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('project-row')).toHaveCount(4)
  })

  test('a failed poll keeps the rows it has and says the list may be out of date', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects')
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    s.failList = true
    await expect(page.getByRole('alert')).toContainText('Could not refresh your projects', { timeout: 10_000 })
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    await expect(page.getByTestId('projects-empty')).toHaveCount(0)
  })

  test('projects_off shows the notice instead of the list', async ({ page }) => {
    const s = await mockProjects(page)
    s.off = true
    await page.goto('/app/projects')
    await expect(page.getByRole('note')).toContainText('Projects are off on this server.')
    await expect(page.getByTestId('project-row')).toHaveCount(0)
  })

  test('New project: the sheet needs a name, saves with Save and opens the new project', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    await expect(sheet.getByRole('heading', { name: 'New project' })).toBeVisible()
    await expect(sheet.getByRole('button', { name: 'Save' })).toBeVisible()
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(sheet.getByRole('alert')).toContainText('Enter a name.')
    await sheet.getByLabel('Name', { exact: true }).fill('Thesis reading list')
    await sheet.getByLabel('Description').fill('Papers and a running outline.')
    await sheet.getByTestId('project-defaults-section-skills').locator('summary').click()
    await sheet.getByLabel('Add to Skills').fill('summarize')
    await sheet.getByLabel('Add to Skills').press('Enter')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { name: 'Thesis reading list', level: 1 })).toBeVisible()
    const post = writes(s, 'POST', '/api/chats/projects')[0]
    expect(post.body).toEqual({ name: 'Thesis reading list', description: 'Papers and a running outline.', defaults: { skills: ['summarize'] }, attached: {} })
  })

  test('a project page has its header, status and the nine tabs', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p1')
    await expect(page.getByRole('heading', { name: 'Client site redesign', level: 1 })).toBeVisible()
    await expect(page.getByTestId('project-overview')).toBeVisible()
    await expect(page.getByRole('tab')).toHaveText(['Overview', 'Files', 'Chats', 'Agents', 'Connections', 'Automations', 'Memory', 'Skills', 'Tool defaults'])
    await expect(page.getByTestId('project-stat-chats')).toContainText('2')
    await expect(page.getByTestId('project-chat-row')).toHaveCount(2)
    await stubProjectFiles(page)
    await page.getByTestId('project-tab-files').click()
    await expect(page.getByTestId('project-files-path')).toHaveText('Projects/client-site-redesign/files')
    await expect(page.getByTestId('project-files-open')).toHaveAttribute('href', '/app/files?path=%2FProjects%2Fclient-site-redesign%2Ffiles')
    await page.getByTestId('project-tab-chats').click()
    await expect(page.getByTestId('project-chat-row').first().getByRole('link', { name: 'Pricing page copy' })).toHaveAttribute('href', '/app/chats/c1')
    await page.getByTestId('project-tab-tools').click()
    await expect(page.getByTestId('project-defaults-skills')).toContainText('brand-voice')
    await expect(page.getByTestId('project-defaults-mcp_servers')).toContainText('docs')
    await expect(page.getByTestId('project-tools')).toContainText('Empty means no limit.')
    await expect(page.getByTestId('project-defaults-connectors')).toContainText('Work mail')
    await expect(page.getByTestId('project-defaults-collections')).toContainText('papers')
    await expect(page.getByTestId('project-defaults-section-connectors')).toContainText('Only these MCP connectors')
    await expect(page.getByTestId('project-defaults-section-collections')).toContainText('Only these knowledge collections are available in new chats')
    await expect(page.getByTestId('project-tools-stored')).toHaveCount(0)
    await expect(page.getByTestId('project-tools')).not.toContainText('Stored for later')
    await expect(page.getByTestId('project-tools')).not.toContainText('do not apply them yet')
  })

  test('Start chat in this project creates the chat with project_id and opens it', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-start-chat').click()
    await expect(page).toHaveURL(/\/app\/chats\/c-new$/)
    const create = writes(s, 'POST', '/api/chats')[0]
    expect(create.body.project_id).toBe('p1')
    expect(create.body.model).toBeTruthy()
    // The server applies the defaults, so the call carries no list.
    expect(Object.keys(create.body).sort()).toEqual(['model', 'project_id'])
  })

  test('the project sheet explains the connector and collection defaults and no longer says they are stored for later', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-edit').click()
    const sheet = page.getByTestId('project-sheet')
    await sheet.getByTestId('project-defaults-section-connectors').locator('summary').click()
    await sheet.getByTestId('project-defaults-section-collections').locator('summary').click()
    await expect(sheet.getByTestId('project-defaults-section-connectors')).toContainText('Only these MCP connectors')
    await expect(sheet.getByTestId('project-defaults-section-collections')).toContainText('Only these knowledge collections are available in new chats')
    await expect(sheet).not.toContainText('chats do not apply them yet')
    await expect(sheet).not.toContainText('Stored for later')
  })

  test('Memory draws the index as text: an image in it is not an element and fetches nothing', async ({ page }) => {
    const hits = []
    await page.route('**://evil.example/**', (route) => { hits.push(route.request().url()); route.abort() })
    await mockProjects(page, { memory: { index: 'Safe text stays visible.\n\n<img src="https://evil.example/x.png">\n\n![md](https://evil.example/m.png)\n', files: [] } })
    await page.goto('/app/projects/p1?tab=memory')
    const index = page.getByTestId('project-memory-index')
    await expect(index).toContainText('Safe text stays visible.')
    await expect(index.locator('img, picture, source, video, svg, iframe, style')).toHaveCount(0)
    await page.waitForTimeout(500)
    expect(hits).toEqual([])
  })

  test('Memory shows the index as Markdown, and Edit saves a file with a PUT', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p1?tab=memory')
    await expect(page.getByTestId('project-memory-index').locator('strong')).toHaveText('plain')
    await expect(page.getByTestId('project-memory-proposals')).toContainText('You confirm each one')
    await page.getByTestId('project-memory-edit').click()
    const sheet = page.getByTestId('project-memory-sheet')
    await sheet.getByLabel('Content').fill('# Client site\n\nNew note.\n')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('project-memory-index')).toContainText('New note.')
    const put = writes(s, 'PUT', '/memory/index.md')[0]
    expect(put.body).toEqual({ content: '# Client site\n\nNew note.\n' })

    // A topic file is read, then replaced; a bad new name is refused before any request.
    await page.getByTestId('project-memory-edit').click()
    await sheet.getByLabel('File', { exact: true }).selectOption('brand-voice.md')
    await expect(sheet.getByLabel('Content')).toHaveValue('Plain words.\n')
    await sheet.getByLabel('Content').fill('Plain words, always.\n')
    await sheet.getByRole('button', { name: 'Save' }).click()
    expect(writes(s, 'PUT', '/memory/brand-voice.md')[0].body).toEqual({ content: 'Plain words, always.\n' })
    await page.getByTestId('project-memory-edit').click()
    await sheet.getByLabel('New topic file').fill('Bad Name.txt')
    await sheet.getByRole('button', { name: 'Add' }).click()
    await expect(sheet.getByRole('alert')).toContainText('ending in .md')
    expect(s.calls.filter(c => c.path.includes('Bad'))).toHaveLength(0)
    await sheet.getByLabel('New topic file').fill('launch-plan.md')
    await sheet.getByRole('button', { name: 'Add' }).click()
    await sheet.getByLabel('Content').fill('Ship it.\n')
    await sheet.getByRole('button', { name: 'Save' }).click()
    expect(writes(s, 'PUT', '/memory/launch-plan.md')).toHaveLength(1)
  })

  test('Edit changes the name and the tool defaults with a PATCH', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-edit').click()
    const sheet = page.getByTestId('project-sheet')
    await expect(sheet.getByRole('heading', { name: 'Edit project' })).toBeVisible()
    await sheet.getByLabel('Name', { exact: true }).fill('Client site v2')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { name: 'Client site v2', level: 1 })).toBeVisible()
    const patch = writes(s, 'PATCH', '/projects/p1')[0]
    expect(patch.body.name).toBe('Client site v2')
    expect(patch.body.defaults).toEqual({ skills: ['brand-voice'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers', 'faq'] })
  })

  test('Archive and Restore from the menu change the status', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByRole('button', { name: 'More actions for Client site redesign' }).click()
    await page.getByRole('menuitem', { name: 'Archive' }).click()
    await expect(page.getByTestId('project-archived')).toBeVisible()
    await expect(page.getByTestId('project-start-chat')).toBeDisabled()
    expect(writes(s, 'POST', '/projects/p1/archive')).toHaveLength(1)
    await page.getByRole('button', { name: 'More actions for Client site redesign' }).click()
    await page.getByRole('menuitem', { name: 'Restore' }).click()
    await expect(page.getByTestId('project-archived')).toHaveCount(0)
    await expect(page.getByTestId('project-start-chat')).toBeEnabled()
    expect(writes(s, 'POST', '/projects/p1/restore')).toHaveLength(1)
  })

  test('Delete leaves the page at once and sends the DELETE only after the Undo window', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p2')
    await page.getByRole('button', { name: 'More actions for Home lab notes' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page).toHaveURL(/\/app\/projects$/)
    await expect(row(page, 'Home lab notes')).toHaveCount(0)
    expect(writes(s, 'DELETE', '/projects/p2')).toHaveLength(0)
    await page.getByTestId('project-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(row(page, 'Home lab notes')).toHaveCount(1)
    expect(writes(s, 'DELETE', '/projects/p2')).toHaveLength(0)
  })

  test('a broken project page shows its error and no tabs; a missing one says so', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p4')
    await expect(page.getByRole('heading', { name: 'Kitchen renovation', level: 1 })).toBeVisible()
    await expect(page.getByTestId('project-broken')).toContainText('missing its manifest')
    await expect(page.getByRole('tab')).toHaveCount(0)
    await page.goto('/app/projects/nope')
    await expect(page.getByTestId('project-missing')).toContainText('This project does not exist.')
  })

  test('the sidebar lists Projects after Files', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects')
    await expect(page.locator('.sidebar-nav').first().getByRole('link', { name: 'Projects' })).toBeVisible()
    const labels = await page.locator('.sidebar-nav').first().getByRole('link').allInnerTexts()
    const trimmed = labels.map(l => l.trim())
    expect(trimmed.indexOf('Projects')).toBe(trimmed.indexOf('Files') + 1)
  })

  test('Start chat uses the last chat model, and waits while the models load', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai_last_model:FLAG_CHAT', 'phi-4-mini'))
    const s = await mockProjects(page)
    let release
    const held = new Promise((r) => { release = r })
    await page.route('**/api/models/capabilities', async (r) => {
      await held
      await r.fulfill({ json: { data: [{ id: 'qwen3-8b', capabilities: ['FLAG_CHAT'] }, { id: 'phi-4-mini', capabilities: ['FLAG_CHAT'] }] } })
    })
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-start-chat')).toBeDisabled()
    release()
    await expect(page.getByTestId('project-start-chat')).toBeEnabled()
    await page.getByTestId('project-start-chat').click()
    await expect(page).toHaveURL(/\/app\/chats\/c-new$/)
    expect(writes(s, 'POST', '/api/chats')[0].body.model).toBe('phi-4-mini')
  })

  test('the tabs move with Home and End as well as the arrows', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-tab-overview').focus()
    await page.keyboard.press('End')
    await expect(page.getByTestId('project-tab-tools')).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('project-tab-tools')).toBeFocused()
    await page.keyboard.press('Home')
    await expect(page.getByTestId('project-tab-overview')).toHaveAttribute('aria-selected', 'true')
  })
})

// The controls of a chat: the Home composer chip, the chat header chips, the
// Project section of the chat settings and the All chats filter.
test.describe('Projects in chats', () => {
  const header = (page) => page.getByTestId('chat-header')

  test('the composer chip lists active projects, and the pick goes out as project_id', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app')
    const chip = page.getByTestId('project-picker-button')
    await expect(chip).toContainText('Project')
    await chip.click()
    const menu = page.getByTestId('project-picker-menu')
    await expect(menu.getByRole('option')).toHaveText([/No project/, /Client site redesign\s*2 chats/, /Home lab notes\s*0 chats/])
    await expect(menu).toContainText('Archived projects are hidden')
    await expect(menu.getByRole('link', { name: 'Manage projects' })).toHaveAttribute('href', '/app/projects')
    await menu.getByRole('combobox', { name: 'Find a project' }).fill('lab')
    await expect(menu.getByRole('option')).toHaveCount(1)
    await menu.getByRole('option', { name: /Home lab notes/ }).click()
    await expect(page.getByTestId('project-picker-menu')).toHaveCount(0)
    await expect(chip).toContainText('Home lab notes')
    await expect(page.getByTestId('project-start-note')).toContainText('This chat starts in Home lab notes')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect.poll(() => writes(s, 'POST', '/api/chats').length).toBe(1)
    expect(writes(s, 'POST', '/api/chats')[0].body.project_id).toBe('p2')
  })

  test('with no pick the chat is created without a project, and No project clears a pick', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app')
    await page.getByTestId('project-picker-button').click()
    await page.getByRole('option', { name: /Client site redesign/ }).click()
    await page.getByTestId('project-picker-button').click()
    await page.getByRole('option', { name: /No project/ }).click()
    await expect(page.getByTestId('project-picker-button')).toContainText('Project')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect.poll(() => writes(s, 'POST', '/api/chats').length).toBe(1)
    expect(writes(s, 'POST', '/api/chats')[0].body).not.toHaveProperty('project_id')
  })

  test('the composer shows no project chip when projects are off', async ({ page }) => {
    const s = await mockProjects(page)
    s.off = true
    await page.goto('/app')
    await expect(page.getByTestId('home-input')).toBeVisible()
    await expect(page.getByTestId('project-picker')).toHaveCount(0)
  })

  test('the chat header shows the home project, a lock chip for each pulled project and the callout', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats[0].pulled_projects = ['p2']
    await page.goto('/app/chats/c1')
    await expect(header(page).getByTestId('chat-project-chip')).toHaveAttribute('href', '/app/projects/p1')
    await expect(header(page).getByTestId('chat-project-chip')).toContainText('Client site redesign')
    const pulled = header(page).getByTestId('chat-pulled-chip')
    await expect(pulled).toContainText('Home lab notes')
    await expect(pulled).toContainText('Pulled in')
    await expect(pulled.locator('[data-icon="lock"]')).toHaveCount(1)
    await expect(page.getByTestId('chat-pulled-callout')).toContainText('Home lab notes is pulled in.')
    await expect(page.getByTestId('chat-pulled-callout')).toContainText('Its files and memory are mounted read-only at the pulled path. This chat can still reach the same files through your home folder.')
  })

  test('an archived or broken pulled project says it is not mounted and the callout does not claim it is read-only', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats[0].pulled_projects = ['p3', 'p4']
    await page.goto('/app/chats/c1')
    const chips = header(page).getByTestId('chat-pulled-chip')
    await expect(chips).toHaveCount(2)
    await expect(chips.filter({ hasText: 'Podcast episode 14' })).toContainText('Archived, not mounted')
    await expect(chips.filter({ hasText: 'Kitchen renovation' })).toContainText('Unavailable')
    await expect(header(page).locator('[data-testid="chat-pulled-chip"] [data-icon="lock"]')).toHaveCount(0)
    await expect(page.getByTestId('chat-pulled-callout')).toHaveCount(0)
    await page.getByTestId('chat-settings-button').click()
    const list = page.getByTestId('chat-project-settings').getByTestId('chat-pulled-list')
    await expect(list).toContainText('Podcast episode 14')
    await expect(list.getByTestId('chat-pulled-state')).toHaveText(['Archived, not mounted', 'Unavailable'])
  })

  test('with one mounted and one archived pulled project the callout names only the mounted one', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats[0].pulled_projects = ['p2', 'p3']
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-pulled-callout')).toContainText('Home lab notes is pulled in.')
    await expect(page.getByTestId('chat-pulled-callout')).not.toContainText('Podcast episode 14')
    await expect(header(page).getByTestId('chat-pulled-chip').filter({ hasText: 'Podcast episode 14' })).toContainText('Archived, not mounted')
  })

  test('the composer chip is replaced by a disabled one when the agent has a folder, and no project is sent', async ({ page }) => {
    const s = await mockProjects(page)
    await page.route('**/api/chats/agents*', r => r.fulfill({ status: 200, json: { agents: [{ id: 'a1', name: 'Scoped', model: '', folder: { path: '/Work', name: 'Work', mode: 'write', state: 'ok' } }] } }))
    await page.goto('/app')
    await page.getByTestId('project-picker-button').click()
    await page.getByRole('option', { name: /Client site redesign/ }).click()
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'Scoped' }).click()
    await expect(page.getByTestId('project-picker-button')).toHaveCount(0)
    const chip = page.getByTestId('project-picker-fenced')
    await expect(chip).toHaveAttribute('aria-disabled', 'true')
    await expect(chip).toHaveAttribute('title', /works only in that folder, so it cannot be in a project/)
    await expect(page.getByTestId('project-start-note')).toHaveCount(0)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect.poll(() => writes(s, 'POST', '/api/chats').length).toBe(1)
    expect(writes(s, 'POST', '/api/chats')[0].body).not.toHaveProperty('project_id')
  })

  test('a chat with no project shows no chips and no callout', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats[0].project_id = ''
    await page.goto('/app/chats/c1')
    await expect(header(page)).toBeVisible()
    await expect(page.getByTestId('chat-project-chip')).toHaveCount(0)
    await expect(page.getByTestId('chat-pulled-callout')).toHaveCount(0)
  })

  test('chat settings: change the home project, pull projects in and out', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-settings-button').click()
    const sec = page.getByTestId('chat-project-settings')
    await expect(sec.getByRole('heading', { name: 'Project' })).toBeVisible()
    const home = sec.getByLabel('Home project')
    await expect(home).toHaveValue('p1')
    await home.selectOption('p2')
    await expect.poll(() => writes(s, 'PUT', '/api/chats/c1/project').length).toBe(1)
    expect(writes(s, 'PUT', '/api/chats/c1/project')[0].body).toEqual({ project_id: 'p2' })
    await expect(header(page).getByTestId('chat-project-chip')).toContainText('Home lab notes')
    await home.selectOption('')
    await expect.poll(() => writes(s, 'PUT', '/api/chats/c1/project').length).toBe(2)
    expect(writes(s, 'PUT', '/api/chats/c1/project')[1].body).toEqual({ project_id: '' })
    await expect(header(page).getByTestId('chat-project-chip')).toHaveCount(0)

    await sec.getByLabel('Add a project').selectOption('p1')
    await sec.getByRole('button', { name: 'Add', exact: true }).click()
    await expect.poll(() => writes(s, 'PUT', '/api/chats/c1/projects').length).toBe(1)
    expect(writes(s, 'PUT', '/api/chats/c1/projects')[0].body).toEqual({ project_ids: ['p1'] })
    await expect(sec.getByTestId('chat-pulled-list')).toContainText('Client site redesign')
    await sec.getByRole('button', { name: 'Remove Client site redesign' }).click()
    await expect.poll(() => writes(s, 'PUT', '/api/chats/c1/projects').length).toBe(2)
    expect(writes(s, 'PUT', '/api/chats/c1/projects')[1].body).toEqual({ project_ids: [] })
  })

  test('chat settings: a restricted chat lists its collections, and a chat without a restriction shows no row', async ({ page }) => {
    const chats = CHATS.map(c => (c.id === 'c1' ? { ...c, collections: ['papers', 'faq'] } : c))
    await mockProjects(page, { chats })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-settings-button').click()
    const row = page.getByTestId('chat-settings').getByTestId('chat-settings-collections')
    await expect(row).toContainText('Collections')
    await expect(row).toContainText('papers')
    await expect(row).toContainText('faq')
    await page.goto('/app/chats/c2')
    await page.getByTestId('chat-settings-button').click()
    await expect(page.getByTestId('chat-settings')).toBeVisible()
    await expect(page.getByTestId('chat-settings-collections')).toHaveCount(0)
  })

  test('chat settings: an empty collections list shows no row', async ({ page }) => {
    const chats = CHATS.map(c => (c.id === 'c1' ? { ...c, collections: [] } : c))
    await mockProjects(page, { chats })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-settings-button').click()
    await expect(page.getByTestId('chat-settings')).toBeVisible()
    await expect(page.getByTestId('chat-settings-collections')).toHaveCount(0)
  })

  test('chat settings: a busy refusal shows as a message and keeps the page working', async ({ page }) => {
    const s = await mockProjects(page)
    s.busy = true
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-settings-button').click()
    const sec = page.getByTestId('chat-project-settings')
    await sec.getByLabel('Home project').selectOption('p2')
    await expect(sec.getByTestId('chat-project-error')).toContainText('A turn is running. Try again when it finishes.')
    await expect(sec.getByLabel('Home project')).toHaveValue('p1')
    await expect(header(page).getByTestId('chat-project-chip')).toContainText('Client site redesign')
  })

  test('All chats: the Project filter narrows the rows, and rows carry the project name', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats.push({ id: 'c3', title: 'Fix the nightly build', model: 'qwen3-8b', kind: 'chat', project_id: '', updated: new Date().toISOString() })
    s.listChats = true
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('chats-row')).toHaveCount(3)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Pricing page copy' }).getByTestId('chats-project-label')).toHaveText('Client site redesign')
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Fix the nightly build' }).getByTestId('chats-project-label')).toHaveCount(0)
    const filter = page.getByTestId('all-chats-project-filter')
    await filter.selectOption('p1')
    await expect(page.getByTestId('chats-row')).toHaveCount(2)
    await filter.selectOption('__none__')
    await expect(page.getByTestId('chats-row')).toHaveCount(1)
    await expect(page.getByTestId('chats-row')).toContainText('Fix the nightly build')
    await filter.selectOption('')
    await expect(page.getByTestId('chats-row')).toHaveCount(3)
  })

  test('All chats: a chat of a deleted project counts as No project and shows no label', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats.push({ id: 'c3', title: 'Orphan of a deleted project', model: 'qwen3-8b', kind: 'chat', project_id: 'deleted-id', updated: new Date().toISOString() })
    s.listChats = true
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('chats-row')).toHaveCount(3)
    const orphan = page.getByTestId('chats-row').filter({ hasText: 'Orphan of a deleted project' })
    await expect(orphan.getByTestId('chats-project-label')).toHaveCount(0)
    await page.getByTestId('all-chats-project-filter').selectOption('__none__')
    await expect(page.getByTestId('chats-row')).toHaveCount(1)
    await expect(page.getByTestId('chats-row')).toContainText('Orphan of a deleted project')
    await page.getByTestId('all-chats-project-filter').selectOption('p1')
    await expect(page.getByTestId('chats-row')).toHaveCount(2)
  })

  test('All chats: with projects off there is no filter, no label and no error', async ({ page }) => {
    const s = await mockProjects(page)
    s.off = true
    s.listChats = true
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('chats-row')).toHaveCount(2)
    await expect(page.getByTestId('all-chats-project-filter')).toHaveCount(0)
    await expect(page.getByTestId('chats-project-label')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('chat settings: making a pulled project the home project drops it from the pulled list', async ({ page }) => {
    const s = await mockProjects(page)
    s.chats[0].pulled_projects = ['p2']
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-settings-button').click()
    const sec = page.getByTestId('chat-project-settings')
    await expect(sec.getByTestId('chat-pulled-list')).toContainText('Home lab notes')
    await sec.getByLabel('Home project').selectOption('p2')
    await expect.poll(() => writes(s, 'PUT', '/api/chats/c1/project').length).toBe(1)
    await expect(sec.getByTestId('chat-pulled-list')).toHaveCount(0)
    await expect(sec.getByText('No projects pulled in.')).toBeVisible()
    await expect(header(page).getByTestId('chat-project-chip')).toContainText('Home lab notes')
    await expect(header(page).getByTestId('chat-pulled-chip')).toHaveCount(0)
    await expect(page.getByTestId('chat-pulled-callout')).toHaveCount(0)
  })

  // Two chats of project p1 and one with no project; each one has its routes.
  const mixedChats = [...CHATS, { id: 'c3', title: 'Fix the nightly build', model: 'qwen3-8b', kind: 'chat', project_id: '', updated: new Date().toISOString() }]

  test('All chats: Clear all with no filter says delete all and deletes every chat', async ({ page }) => {
    const s = await mockProjects(page, { chats: mixedChats })
    s.listChats = true
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('chats-row')).toHaveCount(3)
    await page.getByRole('button', { name: 'Clear all' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete all chats? This cannot be undone.')
    await dialog.getByRole('button', { name: 'Delete all' }).click()
    await expect.poll(() => writes(s, 'DELETE', '/api/chats/c1').length + writes(s, 'DELETE', '/api/chats/c2').length + writes(s, 'DELETE', '/api/chats/c3').length).toBe(3)
  })

  test('All chats: with a project filter, Clear shown names the count and project and deletes only those chats', async ({ page }) => {
    const s = await mockProjects(page, { chats: mixedChats })
    s.listChats = true
    await page.goto('/app/chats/all')
    await page.getByTestId('all-chats-project-filter').selectOption('p1')
    await expect(page.getByTestId('chats-row')).toHaveCount(2)
    await expect(page.getByRole('button', { name: 'Clear all' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Clear shown' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete the 2 chats shown in Client site redesign? This cannot be undone.')
    await dialog.getByRole('button', { name: 'Delete shown' }).click()
    await expect.poll(() => writes(s, 'DELETE', '/api/chats/c1').length + writes(s, 'DELETE', '/api/chats/c2').length).toBe(2)
    expect(writes(s, 'DELETE', '/api/chats/c3')).toHaveLength(0)
  })

  test('All chats: a filter that leaves one row still offers Clear shown', async ({ page }) => {
    const s = await mockProjects(page, { chats: mixedChats })
    s.listChats = true
    await page.goto('/app/chats/all')
    await page.getByTestId('all-chats-project-filter').selectOption('__none__')
    await expect(page.getByTestId('chats-row')).toHaveCount(1)
    await page.getByRole('button', { name: 'Clear shown' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete the 1 chat shown in No project?')
  })
  test('the Files tab says what the folder is for, lists the files and takes uploads and a new file', async ({ page }) => {
    await mockProjects(page)
    const fs = await stubProjectFiles(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-tab-files').click()
    await expect(page.getByTestId('project-files-note')).toHaveText("Files you add here are what this project's chats and agents can use.")
    await expect(page.getByTestId('project-files-drop')).toContainText('Drop files here')
    await expect(page.getByTestId('project-file')).toHaveCount(2)
    await expect(page.getByTestId('project-file').first()).toContainText('assets')
    await expect(page.getByTestId('project-file').nth(1)).toContainText('brief.md')
    await expect(page.getByTestId('project-file').nth(1)).toContainText('120 B')

    await page.getByTestId('project-files-input').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') })
    await expect(page.getByTestId('project-file').filter({ hasText: 'notes.txt' })).toHaveCount(1)
    expect(fs.puts).toEqual(['/Projects/client-site-redesign/files/notes.txt'])

    await page.getByTestId('project-files-new').click()
    await expect.poll(() => fs.puts.at(-1)).toBe('/Projects/client-site-redesign/files/Untitled.md')
    await expect(page.getByTestId('file-preview')).toBeVisible()

    await page.getByTestId('project-files-open').click()
    await expect(page).toHaveURL(/\/app\/files\?path=%2FProjects%2Fclient-site-redesign%2Ffiles$/)
  })

  test('an archived project lists its files read-only: no upload, no new file, no drop target', async ({ page }) => {
    await mockProjects(page)
    const fs = await stubProjectFiles(page, [
      { path: '/Projects/podcast-episode-14/files/notes.md', name: 'notes.md', kind: 'file', size: 80, version: 1, rev: 1, modified: new Date(Date.now() - 3600_000).toISOString() },
    ])
    await page.goto('/app/projects/p3?tab=files')
    await expect(page.getByTestId('project-files-archived')).toContainText('Restore it to add or create files')
    await expect(page.getByTestId('project-file')).toHaveCount(1)
    await expect(page.getByTestId('project-files-upload')).toHaveCount(0)
    await expect(page.getByTestId('project-files-new')).toHaveCount(0)
    await expect(page.getByTestId('project-files-input')).toHaveCount(0)
    await expect(page.getByTestId('project-files-drop')).not.toContainText('Drop files here')
    // A file still opens in the preview, and the folder link stays.
    await page.getByRole('button', { name: 'Open notes.md' }).click()
    await expect(page.getByTestId('file-preview')).toContainText('notes.md')
    await expect(page.getByTestId('project-files-open')).toBeVisible()
    expect(fs.puts).toEqual([])
  })

  test('the Files tab opens a file in the preview pane and shows an empty folder and a failed load plainly', async ({ page }) => {
    await mockProjects(page)
    await stubProjectFiles(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-tab-files').click()
    await page.getByRole('button', { name: 'Open brief.md' }).click()
    await expect(page.getByTestId('file-preview')).toContainText('brief.md')
    await page.keyboard.press('Escape')

    const empty = await page.context().newPage()
    await mockProjects(empty)
    await stubProjectFiles(empty, [])
    await empty.goto('/app/projects/p1')
    await empty.getByTestId('project-tab-files').click()
    await expect(empty.getByTestId('project-files-empty')).toContainText('No files yet')

    const failing = await page.context().newPage()
    await mockProjects(failing)
    await failing.route(/\/api\/chats\/files\/trash(\?.*)?$/, r => r.fulfill({ json: { entries: [] } }))
    await failing.route(/\/api\/chats\/files(\?.*)?$/, r => r.fulfill({ status: 500, json: { error: { message: 'boom' } } }))
    await failing.goto('/app/projects/p1')
    await failing.getByTestId('project-tab-files').click()
    await expect(failing.getByTestId('project-files-error')).toContainText('Could not load the files of this project.')
    await expect(failing.getByTestId('project-files-empty')).toHaveCount(0)
  })
})
