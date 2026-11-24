import { test, expect } from './coverage-fixtures.js'
import { installProjectLinks } from './project-links-fixtures.js'

// Triggers and the project: the chip, the filter, the agent list, the folder of
// a file trigger, "New trigger" on the project page, and a person without the
// permission of a list.

const FILES = '/Projects/client-site-redesign/files'
const MEMORY = '/Projects/client-site-redesign/memory'

const row = (page, name) => page.locator('[data-row]').filter({ hasText: name })

const trigger = (over) => ({
  prompt: 'Do the thing.', daily_cap: 24, effective_run_limit: '15m', enabled: true, model: 'qwen3-8b', summary: 'Every day at 08:00', ...over,
})

// Two triggers of project agents and one of an agent of no project.
async function install(page, opts) {
  const s = await installProjectLinks(page, opts)
  s.agents.triggers = [
    trigger({ id: 'pt1', name: 'Morning copy review', kind: 'schedule', schedule: 'daily 08:00', agent_id: 'ap1' }),
    trigger({ id: 'pt2', name: 'Check new images', kind: 'file', folder: `${FILES}/incoming`, quiet_seconds: 30, agent_id: 'ap2', summary: `When a file arrives in ${FILES}/incoming` }),
    trigger({ id: 'pt3', name: 'Daily digest run', kind: 'schedule', schedule: 'daily 07:00', agent_id: 'a9' }),
    trigger({ id: 'pt4', name: 'Podcast cleanup', kind: 'schedule', schedule: 'daily 10:00', agent_id: 'ap4' }),
  ]
  return s
}

test.describe('Triggers list', () => {
  test('a trigger of a project agent shows the project as a chip that opens the project, and one of no project shows none', async ({ page }) => {
    await install(page)
    await page.goto('/app/triggers')
    await expect(page.getByTestId('jobs-tasks')).toBeVisible()
    const chip = row(page, 'Morning copy review').getByTestId('trigger-project')
    await expect(chip).toHaveText('Client site redesign')
    await expect(chip).toHaveAttribute('href', '/app/projects/p1')
    await expect(row(page, 'Daily digest run').getByTestId('trigger-project')).toHaveCount(0)
  })

  test('the project filter keeps the triggers of one project, or of none, and narrows the history with them', async ({ page }) => {
    await install(page)
    await page.goto('/app/triggers')
    const filter = page.getByTestId('jobs-project-filter')
    await expect(filter.locator('option')).toHaveText(['All projects', 'Client site redesign', 'Podcast episode 14', 'No project'])
    await expect(page.locator('[data-row]')).toHaveCount(4)
    await filter.selectOption({ label: 'Client site redesign' })
    await expect(page.locator('[data-row]')).toHaveText([/Morning copy review/, /Check new images/])
    await filter.selectOption({ label: 'No project' })
    await expect(page.locator('[data-row]')).toHaveText([/Daily digest run/])
    await filter.selectOption({ label: 'All projects' })
    await expect(page.locator('[data-row]')).toHaveCount(4)
  })

  test('with no trigger of a project agent there is no project filter', async ({ page }) => {
    const s = await install(page)
    s.agents.triggers = s.agents.triggers.filter(t => t.id === 'pt3')
    await page.goto('/app/triggers')
    await expect(page.locator('[data-row]')).toHaveCount(1)
    await expect(page.getByTestId('jobs-project-filter')).toHaveCount(0)
  })

  test('the detail page of a trigger of a project agent has a Project row that opens the project', async ({ page }) => {
    await install(page)
    await page.goto('/app/triggers/pt2')
    const facts = page.getByTestId('task-facts')
    await expect(facts.getByTestId('task-project-chip')).toHaveText('Client site redesign')
    await expect(facts.getByTestId('task-project-chip')).toHaveAttribute('href', '/app/projects/p1')
    await page.goto('/app/triggers/pt3')
    await expect(page.getByTestId('task-facts')).toBeVisible()
    await expect(page.getByTestId('task-project')).toHaveCount(0)
  })
})

test.describe('New trigger form', () => {
  const open = async (page) => {
    const s = await install(page)
    await page.goto('/app/triggers/new')
    await expect(page.getByTestId('task-edit')).toBeVisible()
    return s
  }

  test('the agent list says which agents belong to a project', async ({ page }) => {
    await open(page)
    const options = page.locator('#aj-agent option')
    await expect(options.filter({ hasText: 'Copy editor' })).toHaveText('Copy editor (project Client site redesign)')
    await expect(options.filter({ hasText: 'Daily digest' })).toHaveText('Daily digest')
    await page.locator('#aj-agent').selectOption({ label: 'Copy editor (project Client site redesign)' })
    await expect(page.getByTestId('aj-agent-project-chip')).toHaveText('Client site redesign')
  })

  test('a file trigger of a project agent offers the folders of the agent, starting with the project files', async ({ page }) => {
    await open(page)
    await page.locator('#aj-agent').selectOption({ label: 'Asset checker (project Client site redesign)' })
    await page.locator('#aj-when').selectOption('file')
    const base = page.getByTestId('aj-folder-base')
    await expect(base).toHaveValue(FILES)
    await expect(base.locator('option')).toHaveText([FILES, MEMORY])
    await expect(page.getByTestId('aj-folder-problem')).toHaveCount(0)
  })

  test('a subfolder joins the chosen folder, and ".." is refused before Save', async ({ page }) => {
    const s = await open(page)
    await page.locator('#aj-name').fill('Watch inbox')
    await page.locator('#aj-agent').selectOption({ label: 'Asset checker (project Client site redesign)' })
    await page.locator('#aj-prompt').fill('Read the new file.')
    await page.locator('#aj-when').selectOption('file')
    await page.getByTestId('aj-folder-sub').fill('../../other')
    await expect(page.getByTestId('aj-folder-problem')).toContainText('cannot contain')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect(page.getByTestId('aj-folder-problem')).toBeVisible()
    expect(s.agents.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/triggers')).toHaveLength(0)
    await page.getByTestId('aj-folder-sub').fill('incoming/2026')
    await expect(page.getByTestId('aj-folder-problem')).toHaveCount(0)
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect(page).toHaveURL(/\/app\/triggers$/)
    const sent = s.agents.calls.find(c => c.method === 'POST' && c.url === '/api/chats/triggers')
    expect(sent.body).toMatchObject({ kind: 'file', agent_id: 'ap2', folder: `${FILES}/incoming/2026` })
  })

  test('changing the agent replaces a folder that the new agent does not hold', async ({ page }) => {
    await open(page)
    await page.locator('#aj-agent').selectOption({ label: 'Asset checker (project Client site redesign)' })
    await page.locator('#aj-when').selectOption('file')
    await expect(page.getByTestId('aj-folder-base')).toHaveValue(FILES)
    // An agent of no project takes any folder: a text field, and the folder stays.
    await page.locator('#aj-agent').selectOption({ label: 'Daily digest' })
    await expect(page.locator('input#aj-folder')).toHaveValue(FILES)
    await expect(page.getByTestId('aj-folder-base')).toHaveCount(0)
  })

  test('a saved trigger whose folder is outside its agent says so and cannot be saved', async ({ page }) => {
    const s = await install(page)
    s.agents.triggers[1].folder = '/Contracts'
    await page.goto('/app/triggers/pt2/edit')
    await expect(page.getByTestId('task-edit')).toBeVisible()
    const problem = page.getByTestId('aj-folder-problem')
    await expect(problem).toContainText('only watch folders inside its own')
    await expect(problem).toContainText(FILES)
    await page.getByRole('button', { name: 'Save trigger' }).first().click()
    expect(s.agents.calls.filter(c => c.method === 'PUT' && c.url === '/api/chats/triggers/pt2')).toHaveLength(0)
    // Choosing the project folder clears the problem and saves.
    await page.getByTestId('aj-folder-base').selectOption(FILES)
    await expect(problem).toHaveCount(0)
    await page.getByRole('button', { name: 'Save trigger' }).first().click()
    await expect(page).toHaveURL(/\/app\/triggers$/)
  })
})

test.describe('New trigger from a project', () => {
  test('opens the form with the first agent that can work and the project files folder', async ({ page }) => {
    await install(page)
    await page.goto('/app/projects/p1?tab=automations')
    const add = page.getByTestId('project-trigger-new')
    await expect(add).toHaveAttribute('href', '/app/triggers/new?project=p1')
    await add.click()
    await expect(page.getByTestId('task-edit')).toBeVisible()
    // ap3 (Client questions) is flagged unavailable; Asset checker comes first by name.
    await expect(page.locator('#aj-agent')).toHaveValue('ap2')
    await page.locator('#aj-when').selectOption('file')
    await expect(page.getByTestId('aj-folder-base')).toHaveValue(FILES)
  })

  test('is off for an archived project and says why', async ({ page }) => {
    await install(page)
    await page.goto('/app/projects/p3?tab=automations')
    await expect(page.getByTestId('project-trigger-new')).toHaveAttribute('aria-disabled', 'true')
    await expect(page.getByTestId('project-trigger-new-hint')).toHaveText('Restore the project to add a trigger.')
  })

  test('with no agent in the project there is no New trigger, and the empty state points to Agents', async ({ page }) => {
    await install(page)
    await page.goto('/app/projects/p2?tab=automations')
    const empty = page.getByTestId('project-triggers-empty')
    await expect(empty).toContainText('this project has none yet')
    await expect(empty.getByRole('link', { name: 'Go to Agents' })).toHaveAttribute('href', '/app/projects/p2?tab=agents')
    await expect(page.getByTestId('project-trigger-new')).toHaveCount(0)
  })

  test('keeps the listed triggers and offers New trigger above them', async ({ page }) => {
    await install(page)
    await page.goto('/app/projects/p1?tab=automations')
    await expect(page.getByTestId('project-trigger')).toHaveCount(4)
    await expect(page.getByTestId('project-trigger-new')).toBeVisible()
  })
})

test.describe('A person without the permission of a list', () => {
  const asMember = async (page, permissions) => {
    await page.route('**/api/auth/status', route => route.fulfill({
      json: { authEnabled: true, providers: ['local'], hasUsers: true, user: { id: 'u1', email: 'm@example.test', name: 'm', role: 'user', provider: 'local', permissions } },
    }))
  }
  const deny = (page, kind) => page.route(new RegExp(`/api/chats/projects/[^/?]+/${kind}(\\?.*)?$`), route => route.fulfill({
    status: 403, json: { error: { message: `feature not enabled for your account: ${kind}`, type: 'authorization_error', code: 403 } },
  }))

  test('sees no Open link and a plain note on the Automations tab, and still sees the loops', async ({ page }) => {
    await install(page)
    await asMember(page, { chats: true, agents: true })
    await deny(page, 'triggers')
    await page.goto('/app/projects/p1?tab=automations')
    await expect(page.getByTestId('project-triggers-denied')).toContainText('Triggers are not available to your account')
    await expect(page.getByTestId('project-trigger-open')).toHaveCount(0)
    await expect(page.getByTestId('project-trigger-new')).toHaveCount(0)
    await expect(page.getByTestId('project-loop')).toHaveCount(2)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('with the triggers permission sees the Open link', async ({ page }) => {
    await install(page)
    await asMember(page, { chats: true, agents: true, triggers: true })
    await page.goto('/app/projects/p1?tab=automations')
    await expect(page.getByTestId('project-trigger-open')).toHaveCount(4)
  })

  test('the Agents and Connections tabs say what is missing and do not show a load error', async ({ page }) => {
    await install(page)
    await asMember(page, { chats: true })
    await deny(page, 'agents')
    await deny(page, 'connections')
    await page.goto('/app/projects/p1?tab=agents')
    await expect(page.getByTestId('project-agents-denied')).toContainText('Agents are not available to your account')
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connections-denied')).toContainText('Connections are not available to your account')
    await expect(page.getByRole('alert')).toHaveCount(0)
  })
})
