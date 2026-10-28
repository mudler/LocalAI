import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// The triggers pages: the list with its history, a trigger, and the form.

const row = (page, name) => page.locator(`tr[data-task="${name}"]`)

test.describe('Triggers list', () => {
  test('lists the triggers with their schedule, agent and last runs, and the history of every run', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers')
    await expect(page.getByTestId('jobs-page')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Triggers', level: 1 })).toBeVisible()

    await expect(row(page, 'Morning digest')).toContainText('Every weekday at 09:00 (Europe/Rome)')
    await expect(row(page, 'Morning digest')).toContainText('research-assistant')
    await expect(row(page, 'Morning digest')).toContainText('last run failed')
    await expect(row(page, 'New contracts')).toContainText('When a file arrives in /Contracts')
    await expect(row(page, 'New contracts').getByRole('switch')).toHaveAttribute('aria-checked', 'false')

    const history = page.getByTestId('jobs-history')
    await expect(history.locator('tr[data-job]')).toHaveCount(4)
    await expect(history.locator('tr[data-job="r1"]')).toContainText('the agent was deleted')
    await expect(history.locator('tr[data-job="rs"]')).toContainText('daily cap reached')
    await expect(page.getByTestId('jobs-summary')).toContainText('runs in the last 7 days')
  })

  test('filters the history by status and by trigger', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers')
    const history = page.getByTestId('jobs-history')
    await page.locator('[data-filter="failed"]').click()
    await expect(history.locator('tr[data-job]')).toHaveCount(1)
    await page.locator('[data-filter="all"]').click()
    await page.getByLabel('Trigger', { exact: true }).selectOption('t2')
    await expect(history.locator('tr[data-job]')).toHaveCount(1)
    await expect(history.locator('tr[data-job="rf"]')).toBeVisible()
  })

  test('a history row opens to the reason and a link to the chat it started', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers')
    await page.getByRole('button', { name: /Details for the run of Morning digest/ }).first().click()
    const detail = page.getByTestId('job-detail').first()
    await expect(detail).toContainText('the agent was deleted')
    await detail.getByRole('link', { name: 'Open the chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c2$/)
  })

  test('turns a trigger on and off, and runs it now', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers')
    await row(page, 'New contracts').getByRole('switch').click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/t2/enable')).toBe(true)
    await row(page, 'Morning digest').getByRole('button', { name: 'Run now' }).click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/t1/run')).toBe(true)
    await row(page, 'Morning digest').getByRole('switch').click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/t1/disable')).toBe(true)
  })

  test('deletes a trigger after asking', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers')
    await row(page, 'New contracts').getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete New contracts?')
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(row(page, 'New contracts')).toHaveCount(0)
    expect(s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/triggers/t2')).toBe(true)
  })

  test('confirms and dismisses a trigger a chat proposed', async ({ page }) => {
    const s = await installAgents(page, { proposals: [{ id: 'p1', name: 'Weekly digest', prompt: 'Summarise the week.', kind: 'schedule', summary: 'Every Friday at 17:00' }] })
    await page.goto('/app/triggers')
    const proposal = page.getByTestId('proposal-row')
    await expect(proposal).toContainText('Weekly digest')
    await expect(proposal).toContainText('Every Friday at 17:00')
    await proposal.getByRole('button', { name: 'Confirm' }).click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/proposals/p1/confirm')).toBe(true)
    await expect(page.getByTestId('proposals')).toHaveCount(0)
  })

  test('dismisses a proposal without creating a trigger', async ({ page }) => {
    const s = await installAgents(page, { proposals: [{ id: 'p1', name: 'Weekly digest', prompt: 'Go.', kind: 'schedule', summary: 'Every Friday at 17:00' }] })
    await page.goto('/app/triggers')
    await page.getByTestId('proposal-row').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(() => s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/triggers/proposals/p1')).toBe(true)
  })

  test('with no triggers it explains what one is', async ({ page }) => {
    await installAgents(page, { triggers: [] })
    await page.goto('/app/triggers')
    await expect(page.getByTestId('jobs-empty')).toBeVisible()
    await page.getByRole('button', { name: 'Create trigger' }).click()
    await expect(page).toHaveURL(/\/app\/triggers\/new$/)
  })

  test('says why the page is unavailable when triggers are off', async ({ page }) => {
    await installAgents(page)
    await page.route(/\/api\/chats\/triggers(\?.*)?$/, route => route.fulfill({ status: 409, json: { error: { message: 'Triggers are off on this server.', type: 'triggers_off', code: 409 } } }))
    await page.goto('/app/triggers')
    await expect(page.getByRole('note')).toContainText('Triggers are off on this server.')
  })

  test('the old addresses redirect', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/chats/triggers')
    await expect(page).toHaveURL(/\/app\/triggers$/)
    await page.goto('/app/agent-jobs/tasks/t1')
    await expect(page).toHaveURL(/\/app\/triggers$/)
  })
})

test.describe('A trigger', () => {
  test('shows its facts, prompt and recent runs; a run with a chat opens it', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers/t1')
    await expect(page.getByRole('heading', { name: 'Morning digest', level: 1 })).toBeVisible()
    const facts = page.getByTestId('task-facts')
    await expect(facts).toContainText('research-assistant')
    await expect(facts).toContainText('Every weekday at 09:00')
    await expect(page.locator('.aj-prompt')).toContainText('Summarise yesterday.')
    const runs = page.getByTestId('task-runs')
    await expect(runs.locator('[data-job]')).toHaveCount(3)
    await runs.locator('[data-job="r0"]').click()
    await expect(page).toHaveURL(/\/app\/chats\/c7$/)
  })

  test('a file trigger names the folder it watches', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers/t2')
    await expect(page.getByTestId('task-facts')).toContainText('Watches the folder /Contracts')
    await expect(page.getByTestId('task-runs')).toContainText('/Contracts/a.pdf')
  })

  test('runs now, turns off and deletes', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/t1')
    await page.getByTestId('task-run').click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/t1/run')).toBe(true)
    await page.getByTestId('task-facts').getByRole('switch').click()
    await expect.poll(() => s.calls.some(c => c.url === '/api/chats/triggers/t1/disable')).toBe(true)
    await page.getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page).toHaveURL(/\/app\/triggers$/)
  })

  test('a trigger that does not exist says so', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers/nope')
    await expect(page.getByTestId('task-missing')).toBeVisible()
  })
})

test.describe('Trigger form', () => {
  test('creates a schedule trigger with a preview of its next times', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/new')
    await page.getByLabel('Name', { exact: true }).fill('Evening recap')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByLabel('What to do', { exact: true }).fill('Recap the day.')
    await expect(page.getByTestId('trigger-preview')).toContainText('Every day at 08:00')
    await expect(page.getByTestId('trigger-preview')).toContainText('Next:')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect(page).toHaveURL(/\/app\/triggers$/)
    expect(s.calls.find(c => c.method === 'POST').body).toMatchObject({
      name: 'Evening recap', prompt: 'Recap the day.', kind: 'schedule', schedule: 'daily 08:00', agent_id: 'a1', enabled: true,
    })
  })

  test('a file trigger asks for a folder', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/new')
    await page.getByLabel('Name', { exact: true }).fill('Inbox')
    await page.getByLabel('When', { exact: true }).selectOption('file')
    await page.getByLabel('Folder in your files').fill('/Contracts')
    await page.getByLabel('Quiet period (seconds)').fill('45')
    await page.getByLabel('What to do', { exact: true }).fill('Read it.')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'POST')?.body).toMatchObject({ kind: 'file', folder: '/Contracts', quiet_seconds: 45, prompt: 'Read it.' })
  })

  test('shows the reason of a schedule the server cannot read', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/triggers/new')
    await page.getByLabel('When', { exact: true }).selectOption('cron')
    await page.getByLabel('Cron (five fields)').fill('25:00')
    await expect(page.getByRole('alert')).toContainText('hour must be 0 to 23')
  })

  test('edits a trigger, shows what changed in the preview, and saves it with PUT', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/t1/edit')
    await page.locator('[data-section="basics"] .ag-fold__button').click()
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Morning digest')
    await page.getByLabel('Name', { exact: true }).fill('Morning digest 2')
    await page.getByTestId('task-preview-open').click()
    await page.getByTestId('task-preview').getByRole('tab', { name: /Changes/ }).click()
    await expect(page.getByTestId('task-preview-changes')).toContainText('Morning digest 2')
    await page.getByTestId('task-preview').getByRole('button', { name: 'Save trigger' }).click()
    await expect(page).toHaveURL(/\/app\/triggers$/)
    expect(s.calls.find(c => c.method === 'PUT')).toMatchObject({ url: '/api/chats/triggers/t1', body: { name: 'Morning digest 2', schedule: 'weekdays 09:00', agent_id: 'a1' } })
  })

  test('needs a name and something to do', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/new')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    expect(s.calls.some(c => c.method === 'POST')).toBe(false)
    await expect(page.getByText('A trigger needs a name')).toBeVisible()
  })
})
