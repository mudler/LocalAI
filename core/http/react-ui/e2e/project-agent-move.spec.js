import { test, expect } from './coverage-fixtures.js'
import { installMove } from './project-agent-move-fixtures.js'

// The Project field of an agent: the three choices in the create wizard, the
// move of a saved agent with its confirm dialog, the way out of a project, and
// the agent of a deleted project with its one banner and Detach action.

const fillPurpose = async (page) => {
  await page.getByLabel('Name', { exact: false }).first().fill('helper')
  await page.getByLabel('Instructions', { exact: false }).first().fill('Be brief.')
}
const next = (page) => page.getByTestId('wizard-next').click()
const creates = (s) => s.agents.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/agents')
const toReview = async (page) => { for (let i = 0; i < 3; i++) await next(page) }

test.describe('Project field: the create wizard', () => {
  test('lists No project, the active projects and Create a new project; archived ones are left out', async ({ page }) => {
    await installMove(page)
    await page.goto('/app/agents/new')
    const select = page.getByTestId('agent-project-select')
    await expect(select).toHaveValue('')
    await expect(select.locator('option')).toHaveText(['No project', 'Client site redesign', 'Home lab notes', 'Create a new project...'])
  })

  test('with no project the agent is created as before: no project_id', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await toReview(page)
    await page.getByTestId('wizard-create').click()
    await expect.poll(() => creates(s).length).toBe(1)
    expect(creates(s)[0].body).not.toHaveProperty('project_id')
  })

  test('an existing project makes a project agent: Files turns into the files mode of the project', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await page.getByTestId('agent-project-select').selectOption('p2')
    await expect(page.getByTestId('wizard-rail-project')).toHaveText('Home lab notes')
    await next(page)
    await expect(page.getByTestId('agent-project-files')).toBeVisible()
    await expect(page.getByTestId('agent-folder-scope')).toHaveCount(0)
    await next(page); await next(page)
    await expect(page.getByTestId('wizard-review-project')).toContainText('Home lab notes')
    await page.getByTestId('wizard-create').click()
    await expect.poll(() => creates(s).length).toBe(1)
    const body = creates(s)[0].body
    expect(body.project_id).toBe('p2')
    expect(body).not.toHaveProperty('folders')
  })

  test('a read only files mode sends the two project folders', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await page.getByTestId('agent-project-select').selectOption('p2')
    await next(page)
    await page.getByTestId('agent-project-files-mode').selectOption('read')
    await next(page); await next(page)
    await page.getByTestId('wizard-create').click()
    await expect.poll(() => creates(s).length).toBe(1)
    expect(creates(s)[0].body.folders).toEqual([
      { path: '/Projects/home-lab-notes/files', mode: 'read' }, { path: '/Projects/home-lab-notes/memory', mode: 'read' },
    ])
  })

  test('Create a new project opens the project sheet and picks the new project', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await page.getByTestId('agent-project-select').selectOption('__create__')
    const sheet = page.getByTestId('project-sheet')
    await expect(sheet).toBeVisible()
    await sheet.getByLabel('Name').fill('Garden plan')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(sheet).toHaveCount(0)
    // The sheet's form did not submit the wizard: nothing was created yet.
    expect(creates(s)).toHaveLength(0)
    await expect(page.getByTestId('agent-project-select')).toHaveValue('p4')
    await expect(page.getByTestId('wizard-rail-project')).toHaveText('Garden plan')
    await toReview(page)
    await page.getByTestId('wizard-create').click()
    await expect.poll(() => creates(s).length).toBe(1)
    expect(creates(s)[0].body.project_id).toBe('p4')
  })
})

test.describe('Project field: the editor of a saved agent', () => {
  test('a plain agent moves into a project after the dry run dialog', async ({ page }) => {
    const s = await installMove(page, {
      report: { chats: 2, loops: 1, memory_grant_dropped: true, dropped_tools: { skills: ['brand'], addons: [], mcp_servers: ['crm'], collections: [] } },
    })
    await page.goto('/app/agents/a9/edit')
    const move = page.getByTestId('agent-project-move')
    await expect(move).toBeDisabled()
    await page.getByTestId('agent-project-select').selectOption('p2')
    await expect(move).toBeEnabled()
    // Nothing is sent until the person asks for the move.
    expect(s.moves).toHaveLength(0)
    await move.click()
    const dialog = page.getByTestId('agent-move-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByTestId('agent-move-effects')).toContainText('2 chats of this agent stay readable')
    await expect(dialog.locator('[data-effect="loops"]')).toContainText('1 loop')
    await expect(dialog.locator('[data-effect="dropped"]')).toContainText('brand, crm')
    await expect(dialog.locator('[data-effect="grant"]')).toBeVisible()
    await expect(dialog.locator('[data-effect="notes"]')).toContainText('start empty')
    expect(s.moves.every(m => m.body.dry_run)).toBe(true)
    expect(s.moves.at(-1).body).toEqual({ project_id: 'p2', dry_run: true, files_mode: 'write' })
    await dialog.getByTestId('agent-move-confirm').click()
    await expect.poll(() => s.moves.filter(m => !m.body.dry_run).length).toBe(1)
    expect(s.moves.at(-1).body).toEqual({ project_id: 'p2', dry_run: false, files_mode: 'write' })
    await expect(dialog).toHaveCount(0)
    await expect(page.getByTestId('agent-project-chip')).toContainText('Home lab notes')
  })

  test('the files mode of the dialog is sent', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await page.getByTestId('agent-project-move').click()
    await page.getByTestId('agent-project-files-mode').selectOption('read')
    await expect.poll(() => s.moves.at(-1)?.body.files_mode).toBe('read')
  })

  test('a move waits while the form has unsaved changes', async ({ page }) => {
    await installMove(page)
    await page.goto('/app/agents/a9/edit')
    await page.locator('#ag-description').fill('changed')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await expect(page.getByTestId('agent-project-move')).toBeDisabled()
    await expect(page.getByTestId('agent-project-dirty')).toBeVisible()
  })

  test('shares and file triggers block the move until their switches are on', async ({ page }) => {
    const s = await installMove(page, {
      report: { shares: 2, triggers_outside: [{ id: 't1', name: 'Arrivals' }], blockers: ['shared', 'triggers_outside'] },
    })
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await page.getByTestId('agent-project-move').click()
    const confirm = page.getByTestId('agent-move-confirm')
    await expect(page.getByTestId('agent-move-revoke')).toContainText('shared with 2 people')
    await expect(page.getByTestId('agent-move-disable')).toContainText('Arrivals')
    await expect(confirm).toBeDisabled()
    await page.getByTestId('agent-move-revoke-switch').click()
    await expect(confirm).toBeDisabled()
    await page.getByTestId('agent-move-disable-switch').click()
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect.poll(() => s.moves.filter(m => !m.body.dry_run).length).toBe(1)
    expect(s.moves.at(-1).body).toMatchObject({ project_id: 'p2', revoke_shares: true, disable_triggers: true })
  })

  test('a running turn blocks the move for good', async ({ page }) => {
    await installMove(page, { report: { blockers: ['busy'] } })
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await page.getByTestId('agent-project-move').click()
    await expect(page.getByTestId('agent-move-busy')).toContainText('running a turn')
    await expect(page.getByTestId('agent-move-confirm')).toBeDisabled()
  })

  test('leaving a project starts on the project files folder and sends the folder list', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/ap2/edit')
    await page.getByTestId('agent-project-select').selectOption('')
    await page.getByTestId('agent-project-move').click()
    const dialog = page.getByTestId('agent-move-dialog')
    await expect(dialog.getByTestId('agent-move-folder')).toHaveText('/Projects/client-site-redesign/files')
    await expect.poll(() => s.moves.at(-1)?.body).toEqual({ project_id: '', dry_run: true, folders: [{ path: '/Projects/client-site-redesign/files', mode: 'read' }] })
    await dialog.getByTestId('agent-move-confirm').click()
    await expect.poll(() => s.moves.filter(m => !m.body.dry_run).length).toBe(1)
    await expect(page.getByTestId('agent-project-chip')).toHaveCount(0)
  })

  test('all my files is an explicit choice', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/ap2/edit')
    await page.getByTestId('agent-project-select').selectOption('')
    await page.getByTestId('agent-project-move').click()
    await page.getByTestId('agent-move-scope-all').click()
    await expect.poll(() => s.moves.at(-1)?.body).toEqual({ project_id: '', dry_run: true, whole_home: true })
  })

  test('a refused move shows the server message and keeps the dialog open', async ({ page }) => {
    const s = await installMove(page, { report: {} })
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await page.getByTestId('agent-project-move').click()
    await expect(page.getByTestId('agent-move-confirm')).toBeEnabled()
    // The server blocks it after the dry run (a trigger appeared).
    s.report.blockers = ['triggers_outside']
    await page.getByTestId('agent-move-confirm').click()
    await expect(page.getByTestId('agent-move-error')).toContainText('file trigger')
    await expect(page.getByTestId('agent-move-dialog')).toBeVisible()
  })
})

test.describe('An agent whose project was deleted', () => {
  test('shows one banner with Detach, and no second banner about a missing folder', async ({ page }) => {
    await installMove(page)
    await page.goto('/app/agents/ap9')
    await expect(page.getByTestId('agent-paused')).toHaveCount(1)
    await expect(page.getByTestId('agent-paused')).toContainText('project is deleted')
    await expect(page.getByTestId('agent-folder-warning')).toHaveCount(0)
    await expect(page.getByTestId('agent-open-project')).toHaveCount(0)
    await expect(page.getByTestId('agent-detach')).toBeVisible()
  })

  test('Detach moves the agent out with the folders the person chose', async ({ page }) => {
    const s = await installMove(page)
    await page.goto('/app/agents/ap9')
    await page.getByTestId('agent-detach').click()
    const dialog = page.getByTestId('agent-move-dialog')
    await expect(dialog).toContainText('Detach from the project')
    // No folder yet, so no request until one is chosen or all files are allowed.
    await expect(dialog.getByTestId('agent-move-nofolder')).toBeVisible()
    await expect(dialog.getByTestId('agent-move-confirm')).toBeDisabled()
    expect(s.moves).toHaveLength(0)
    await dialog.getByTestId('agent-move-scope-all').click()
    await expect(dialog.getByTestId('agent-move-confirm')).toBeEnabled()
    await dialog.getByTestId('agent-move-confirm').click()
    await expect.poll(() => s.moves.filter(m => !m.body.dry_run).length).toBe(1)
    expect(s.moves.at(-1)).toEqual({ id: 'ap9', body: { project_id: '', dry_run: false, whole_home: true } })
    await expect(page.getByTestId('agent-paused')).toHaveCount(0)
  })

  test('the editor of the agent shows the same single banner with Detach', async ({ page }) => {
    await installMove(page)
    await page.goto('/app/agents/ap9/edit')
    await expect(page.getByTestId('agent-paused')).toHaveCount(1)
    await expect(page.getByTestId('agent-detach')).toBeVisible()
  })
})
