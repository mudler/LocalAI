import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'
import { installTasks } from './agent-tasks-fixtures.js'

// The agent tasks page against stubbed routes: the task list with its filters,
// the create and run sheets, the details pane and the run history.

async function setup(page, opts = {}) {
  await installAgents(page, { agents: [...AGENTS, { id: 'a4', name: 'release-notes-writer', description: '', model: 'phi-4-mini', instructions: 'Write notes.', mcp_servers: [], addons: [], collections: [], skills: [] }] })
  await page.route('**/api/features', route => route.fulfill({ json: { agents: true, triggers: true, mcp_jobs: true, agent_jobs: true, mcp: true } }))
  const k = await installTasks(page, opts)
  await page.goto('/app/agent-tasks')
  return k
}
const taskRow = (page, name) => page.locator(`[data-testid="agent-task-row"][data-task="${name}"]`)

test.describe('Agent tasks', () => {
  test('lists the tasks with who runs them, what they ask for and how the last run went', async ({ page }) => {
    await setup(page)
    await expect(page.getByRole('heading', { name: 'Agent tasks', level: 1 })).toBeVisible()
    const first = taskRow(page, 'Summarise a support ticket')
    await expect(first).toContainText('qwen3-8b')
    await expect(first).toContainText('Asks for ticket')
    await expect(first).toContainText('Running')
    await expect(first).toContainText('1 webhook')
    await expect(taskRow(page, 'Translate release notes')).toContainText('release-notes-writer')
    await expect(taskRow(page, 'Translate release notes')).toContainText('Failed')
    await expect(taskRow(page, 'Nightly test report').getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  })

  test('filters the tasks by on and off and by a search', async ({ page }) => {
    await setup(page)
    await page.getByRole('group', { name: 'Show tasks' }).getByRole('button', { name: /^Off/ }).click()
    await expect(page.getByTestId('agent-task-row')).toHaveCount(1)
    await page.getByRole('group', { name: 'Show tasks' }).getByRole('button', { name: /^All/ }).click()
    await page.getByRole('searchbox', { name: 'Search tasks' }).fill('customer')
    await expect(page.getByTestId('agent-task-row')).toHaveCount(1)
    await expect(taskRow(page, 'Draft a reply')).toBeVisible()
    await page.getByRole('searchbox', { name: 'Search tasks' }).fill('nothing like this')
    await expect(page.getByText('No tasks match.')).toBeVisible()
    await page.getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.getByTestId('agent-task-row')).toHaveCount(4)
  })

  test('creates a task in the sheet and says what is missing before it saves', async ({ page }) => {
    const k = await setup(page)
    await page.getByRole('button', { name: 'New task' }).click()
    const form = page.getByTestId('agent-task-form')
    await page.getByRole('button', { name: 'Save task' }).click()
    await expect(form.getByText('Give the task a name.')).toBeVisible()
    await expect(form.getByText('Choose a model.')).toBeVisible()
    await expect(form.getByLabel('Name')).toHaveAttribute('aria-invalid', 'true')
    await form.getByLabel('Name').fill('weekly-summary')
    await form.getByLabel('Model').selectOption('qwen3-8b')
    await form.getByLabel('Prompt').fill('Summarise the week of {{.team}}')
    await expect(page.getByTestId('agent-task-inputs')).toContainText('team')
    await page.getByRole('button', { name: 'Save task' }).click()
    await expect(form).toHaveCount(0)
    const post = k.calls.find(c => c.m === 'POST')
    expect(post.b).toMatchObject({ name: 'weekly-summary', model: 'qwen3-8b', prompt: 'Summarise the week of {{.team}}', enabled: true, agent_id: '' })
    await expect(taskRow(page, 'weekly-summary')).toBeVisible()
  })

  test('runs a task with the values its prompt asks for, and previews the prompt', async ({ page }) => {
    const k = await setup(page)
    await taskRow(page, 'Translate release notes').getByRole('button', { name: 'Run Translate release notes' }).click()
    const sheet = page.getByTestId('agent-task-run')
    await sheet.getByLabel('version').fill('4.2.0')
    await sheet.getByLabel('language').fill('German')
    await expect(page.getByTestId('agent-task-preview')).toContainText('version 4.2.0 into German')
    await page.getByRole('button', { name: 'Run now' }).click()
    await expect.poll(() => k.calls.find(c => c.m === 'RUN')?.b).toEqual({ task_id: 'k2', parameters: { version: '4.2.0', language: 'German' } })
  })

  test('a task that asks for nothing starts at once; a switch turns a task off', async ({ page }) => {
    const k = await setup(page, { tasks: [{ id: 'k9', name: 'Plain', prompt: 'Say hi.', model: 'qwen3-8b', enabled: true }], jobs: [] })
    await taskRow(page, 'Plain').getByRole('button', { name: 'Run Plain' }).click()
    await expect.poll(() => k.calls.find(c => c.m === 'RUN')?.b).toEqual({ task_id: 'k9', parameters: {} })
    await taskRow(page, 'Plain').getByRole('switch').click()
    await expect.poll(() => k.calls.find(c => c.m === 'PUT')?.b?.enabled).toBe(false)
    await expect(taskRow(page, 'Plain').getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  })

  test('the details pane shows the prompt and the API call, the runs, and deletes after asking', async ({ page }) => {
    const k = await setup(page)
    await page.getByRole('button', { name: 'Open Summarise a support ticket' }).click()
    const pane = page.getByTestId('agent-task-pane')
    await expect(pane).toContainText('Summarise ticket {{.ticket}}')
    await expect(pane).toContainText('/api/agent/tasks/Summarise%20a%20support%20ticket/execute')
    await pane.getByRole('tab', { name: /Runs/ }).click()
    await expect(pane.getByTestId('agent-job-row')).toHaveCount(k.jobs.filter(j => j.task_id === 'k1').length)
    await pane.getByRole('tab', { name: 'Settings' }).click()
    await pane.getByRole('button', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(taskRow(page, 'Summarise a support ticket')).toHaveCount(0)
    await expect(pane).toHaveCount(0)
  })

  test('the run history filters by state and task, opens a run to its values and answer, and runs it again', async ({ page }) => {
    const k = await setup(page)
    const history = page.getByTestId('agent-jobs')
    await page.getByRole('group', { name: 'Show runs' }).getByRole('button', { name: /^Failed/ }).click()
    await expect(history.getByTestId('agent-job-row')).toHaveCount(k.jobs.filter(j => j.status === 'failed').length)
    await page.getByRole('group', { name: 'Show runs' }).getByRole('button', { name: /^All/ }).click()
    await page.getByLabel('Task', { exact: true }).selectOption('k2')
    await expect(history.getByTestId('agent-job-row')).toHaveCount(1)
    await page.getByLabel('Task', { exact: true }).selectOption('all')

    const done = history.locator('[data-job="j3"]')
    await done.getByRole('button', { name: /Details of the run/ }).click()
    const detail = done.getByTestId('agent-job-detail')
    await expect(detail).toContainText('SUP-4817')
    await expect(detail).toContainText('Workaround: clear the session cookie.')
    await expect(detail.getByRole('button', { name: 'Copy' })).toBeVisible()
    await expect(detail.getByRole('link', { name: 'Open the chat' })).toHaveAttribute('href', /\/app\/chats\/j3$/)
    await done.getByRole('button', { name: /again/ }).click()
    await expect.poll(() => k.calls.some(c => c.m === 'rerun' && c.id === 'j3')).toBe(true)

    await history.locator('[data-job="j1"]').getByRole('button', { name: 'Cancel' }).click()
    await expect.poll(() => k.calls.some(c => c.m === 'cancel' && c.id === 'j1')).toBe(true)

    await history.locator('[data-job="j4"]').getByRole('button', { name: /More actions for this run/ }).click()
    await page.getByRole('menuitem', { name: 'Delete run' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(history.locator('[data-job="j4"]')).toHaveCount(0)
  })

  test('with no tasks it explains what one is and offers to create one', async ({ page }) => {
    await setup(page, { tasks: [], jobs: [] })
    await expect(page.getByTestId('agent-tasks-empty')).toContainText('No tasks yet')
    await page.getByTestId('agent-tasks-empty').getByRole('button', { name: 'New task' }).click()
    await expect(page.getByTestId('agent-task-form')).toBeVisible()
  })

  test('a failed load says so with a way to try again', async ({ page }) => {
    await setup(page, { fail: true })
    await expect(page.getByRole('alert')).toContainText('Could not load the tasks.')
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible()
  })
})
