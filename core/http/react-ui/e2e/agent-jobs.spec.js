import { test, expect } from './coverage-fixtures.js'
import { mockJobs, fixtureData } from './jobs-fixtures.js'

async function open(page, options) {
  const mocked = await mockJobs(page, options)
  await page.goto('/app/agent-jobs')
  await expect(page.getByTestId('jobs-page')).toBeVisible()
  return mocked
}

test.describe('Jobs page: the week and the tasks', () => {
  test('one sentence says what the week held, from the jobs the API returned', async ({ page }) => {
    await open(page)
    const sum = page.getByTestId('jobs-summary')
    await expect(sum).toContainText('5 runs in the last 7 days.')
    await expect(sum).toContainText('2 finished, 1 failed, 1 cancelled, 1 still going.')
    await expect(sum).toContainText('The last run of daily-digest failed.')
  })

  test('a week with no runs says so, and a page with no jobs at all says nothing ran', async ({ page }) => {
    const old = fixtureData().jobs.filter(j => j.id === 'job-stop-0005').map(j => ({ ...j, created_at: new Date(Date.now() - 10 * 86400000).toISOString(), started_at: undefined }))
    await open(page, { jobs: old })
    await expect(page.getByTestId('jobs-summary')).toHaveText('No runs in the last 7 days.')
    const none = await page.context().newPage()
    await mockJobs(none, { jobs: [] })
    await none.goto('/app/agent-jobs')
    await expect(none.getByTestId('jobs-summary')).toHaveText('Nothing has run yet.')
    await expect(none.getByText('No runs yet')).toBeVisible()
  })

  test('each task shows its model, its schedule in words, its last runs and its last outcome', async ({ page }) => {
    await open(page)
    const digest = page.locator('[data-task="daily-digest"]')
    await expect(digest).toContainText('qwen3-8b-instruct')
    await expect(digest).toContainText('Every day at 07:00')
    await expect(digest.locator('.aj-sched__cron')).toHaveText('0 7 * * *')
    await expect(digest).toContainText('last run failed')
    await expect(digest.locator('.ag-pip[data-state="failed"]')).toHaveCount(1)
    await expect(digest.locator('.ag-pip[data-state="done"]')).toHaveCount(1)
    await expect(page.locator('[data-task="repo-triage"]')).toContainText('Every 30 minutes')
    await expect(page.locator('[data-task="weekly-report"]')).toContainText('Every Monday at 09:00')
    const manual = page.locator('[data-task="ad-hoc-research"]')
    await expect(manual).toContainText('Runs when you start it')
    await expect(manual).toContainText('Not run yet')
    await expect(manual.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  })

  test('a task name opens the task, and the model opens its editor', async ({ page }) => {
    await open(page)
    await expect(page.locator('[data-task="daily-digest"] .aj-model')).toHaveAttribute('href', '/app/model-editor/qwen3-8b-instruct')
    await page.locator('[data-task="daily-digest"]').getByRole('link', { name: 'daily-digest' }).click()
    await expect(page).toHaveURL(/\/app\/agent-jobs\/tasks\/t1$/)
  })

  test('the task list folds away and comes back', async ({ page }) => {
    await open(page)
    await page.getByRole('button', { name: 'Hide' }).click()
    await expect(page.getByTestId('jobs-tasks')).toHaveCount(0)
    await page.getByRole('button', { name: 'Show' }).click()
    await expect(page.getByTestId('jobs-tasks')).toBeVisible()
  })

  test('the enabled switch saves the task and says so', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-task="daily-digest"]').getByRole('switch').click()
    await expect(page.getByText('daily-digest is off')).toBeVisible()
    expect(seen.puts).toHaveLength(1)
    expect(seen.puts[0].id).toBe('t1')
    expect(seen.puts[0].body.enabled).toBe(false)
    expect(seen.puts[0].body.cron).toBe('0 7 * * *')
    expect(seen.puts[0].body.cron_parameters).toEqual({ topic: 'local inference', format: 'bullet points', items: '8' })
  })

  test('the switch goes back when the save fails', async ({ page }) => {
    await open(page, { updateStatus: 500 })
    const toggle = page.locator('[data-task="daily-digest"]').getByRole('switch')
    await toggle.click()
    await expect(page.getByText(/Could not change the task/)).toBeVisible()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
  })

  test('with no tasks the page teaches what a task is and how to make one', async ({ page }) => {
    await open(page, { tasks: [], jobs: [] })
    const empty = page.getByTestId('jobs-empty')
    await expect(empty).toContainText('A task is a prompt a model runs for you')
    await expect(empty.locator('li')).toHaveCount(3)
    await expect(empty).toContainText('{{.name}}')
    await empty.getByRole('button', { name: 'Create task' }).click()
    await expect(page).toHaveURL(/\/app\/agent-jobs\/tasks\/new$/)
  })

  test('without a model it says Jobs need one, and without MCP it shows how to add it', async ({ page }) => {
    await mockJobs(page, { models: [] })
    await page.goto('/app/agent-jobs')
    await expect(page.getByTestId('jobs-no-models')).toContainText('No models installed')
    const other = await page.context().newPage()
    await mockJobs(other, { tasks: [], jobs: [], mcp: false })
    await other.goto('/app/agent-jobs')
    await expect(other.getByTestId('jobs-no-mcp')).toContainText('MCP is not set up')
    await expect(other.getByTestId('jobs-no-mcp')).toContainText('command: /path/to/tool')
  })
})

test.describe('Jobs page: run history', () => {
  test('is grouped by day, newest first, one outcome sentence per run', async ({ page }) => {
    await open(page)
    const days = page.locator('.aj-day')
    await expect(days.nth(0)).toHaveText('Today')
    await expect(days.nth(1)).toHaveText('Yesterday')
    await expect(days).toHaveCount(3)
    const rows = page.locator('[data-testid="jobs-history"] tr[data-job]')
    await expect(rows).toHaveCount(5)
    await expect(rows.nth(0)).toHaveAttribute('data-job', 'job-run-0003')
    await expect(rows.nth(1)).toHaveAttribute('data-job', 'job-fail-0001')
    await expect(rows.nth(2)).toHaveAttribute('data-job', 'job-done-0002')
    await expect(rows.nth(3)).toHaveAttribute('data-job', 'job-done-0004')
    await expect(rows.nth(4)).toHaveAttribute('data-job', 'job-stop-0005')
    await expect(page.locator('[data-job="job-fail-0001"]')).toContainText('read_page: timeout after 20 s')
    await expect(page.locator('[data-job="job-fail-0001"]')).toContainText('1 min 06 s')
    await expect(page.locator('[data-job="job-done-0002"]')).toContainText('Labelled 12 issues and routed 3 of them')
    await expect(page.locator('[data-job="job-run-0003"]')).toContainText('Working since')
    await expect(page.locator('[data-job="job-stop-0005"]')).toContainText('Cancelled before it finished')
  })

  test('shows no duration for a run the server has no end time for', async ({ page }) => {
    await open(page)
    const cells = page.locator('[data-job="job-run-0003"] td.dk-num')
    await expect(cells.nth(1)).toHaveText('')
  })

  test('status chips filter, with the counts, and a second press clears', async ({ page }) => {
    await open(page)
    await expect(page.locator('[data-filter="all"]')).toContainText('5')
    await expect(page.locator('[data-filter="failed"]')).toContainText('1')
    await expect(page.locator('[data-filter="completed"]')).toContainText('2')
    await expect(page.locator('[data-filter="pending"]')).toHaveCount(0)
    await page.locator('[data-filter="completed"]').click()
    await expect(page.locator('[data-testid="jobs-history"] tr[data-job]')).toHaveCount(2)
    await page.locator('[data-filter="completed"]').click()
    await expect(page.locator('[data-testid="jobs-history"] tr[data-job]')).toHaveCount(5)
  })

  test('the task filter narrows the history and the counts follow it', async ({ page }) => {
    await open(page)
    await page.getByLabel('Task', { exact: true }).selectOption({ label: 'repo-triage' })
    await expect(page.locator('[data-testid="jobs-history"] tr[data-job]')).toHaveCount(2)
    await expect(page.locator('[data-filter="all"]')).toContainText('2')
    await expect(page.locator('[data-filter="failed"]')).toHaveCount(0)
  })

  test('a filter with no match says so and clears', async ({ page }) => {
    await open(page)
    await page.locator('[data-filter="failed"]').click()
    await page.getByLabel('Task', { exact: true }).selectOption({ label: 'weekly-report' })
    await expect(page.getByTestId('jobs-history-empty')).toContainText('No runs match these filters.')
    await page.getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.locator('[data-testid="jobs-history"] tr[data-job]')).toHaveCount(5)
  })

  test('a row opens into its error and one next action', async ({ page }) => {
    const { seen } = await open(page)
    const toggle = page.getByRole('button', { name: 'Details for the run of daily-digest' }).first()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const detail = page.getByTestId('job-detail')
    await expect(detail.getByTestId('job-excerpt')).toContainText('read_page: timeout after 20 s (3 of 3 attempts)')
    await detail.getByRole('button', { name: 'Run again' }).click()
    await expect(page.getByText('Started a new run')).toBeVisible()
    expect(seen.jobExecutes).toEqual([{ task_id: 't1', parameters: { topic: 'local inference', format: 'bullet points', items: '8' } }])
    await expect(detail.getByRole('link', { name: 'Open the run' })).toHaveAttribute('href', '/app/agent-jobs/jobs/job-fail-0001')
  })

  test('a run in flight offers Cancel, and Cancel calls the API', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-job="job-run-0003"]').getByRole('button', { name: /Details for/ }).click()
    await page.getByTestId('job-detail').getByRole('button', { name: 'Cancel' }).click()
    await expect.poll(() => seen.cancels).toEqual(['job-run-0003'])
  })

  test('the page menu stops every running job after a confirm', async ({ page }) => {
    const { seen } = await open(page)
    await page.getByRole('button', { name: 'More job actions' }).click()
    await page.getByRole('menuitem', { name: 'Stop running jobs' }).click()
    await expect(page.getByRole('alertdialog')).toContainText('Finished jobs stay in the history')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop jobs' }).click()
    await expect.poll(() => seen.cancels).toEqual(['job-run-0003'])
  })

  test('the Jobs tab stays lit around the page', async ({ page }) => {
    await open(page)
    await expect(page.locator('.dk-hubtabs [data-hub-tab="jobs"]')).toHaveAttribute('aria-current', 'page')
  })
})

test.describe('Jobs page: run a task now', () => {
  test('the dialog asks for the values the prompt uses, filled from the schedule', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-task="daily-digest"]').getByRole('button', { name: 'Run now' }).click()
    const dialog = page.getByTestId('run-task-dialog')
    await expect(dialog).toContainText('Run daily-digest now')
    await expect(dialog.getByLabel('topic')).toHaveValue('local inference')
    await expect(dialog.getByLabel('format')).toHaveValue('bullet points')
    await dialog.getByLabel('topic').fill('open models')
    await dialog.getByLabel('More parameters, one key=value per line').fill('tone=dry')
    await dialog.getByRole('button', { name: 'Start run' }).click()
    await expect(page.getByText('Task "daily-digest" started')).toBeVisible()
    expect(seen.executes).toEqual([{ name: 'daily-digest', body: { topic: 'open models', format: 'bullet points', items: '8', tone: 'dry' } }])
    await expect(dialog).toHaveCount(0)
  })

  test('a task with no gaps says so, and Escape closes the dialog', async ({ page }) => {
    await open(page)
    await page.locator('[data-task="weekly-report"]').getByRole('button', { name: 'Run now' }).click()
    await expect(page.getByTestId('run-task-dialog')).toContainText('This prompt has no gaps to fill.')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('run-task-dialog')).toHaveCount(0)
  })

  test('attached media goes with the run through the job call', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-task="weekly-report"]').getByRole('button', { name: 'Run now' }).click()
    const dialog = page.getByTestId('run-task-dialog')
    await dialog.getByText('Media', { exact: true }).click()
    const chooser = page.waitForEvent('filechooser')
    await dialog.getByRole('button', { name: 'Add' }).first().click()
    await (await chooser).setFiles({ name: 'board.png', mimeType: 'image/png', buffer: Buffer.from('png') })
    await expect(dialog.getByText('board.png')).toBeVisible()
    await dialog.getByRole('button', { name: 'Start run' }).click()
    await expect.poll(() => seen.jobExecutes.length).toBe(1)
    expect(seen.jobExecutes[0].task_id).toBe('t3')
    expect(seen.jobExecutes[0].images).toHaveLength(1)
    expect(seen.jobExecutes[0].images[0]).toMatch(/^data:image\/png;base64,/)
    expect(seen.executes).toEqual([])
  })
})

test.describe('Jobs page: delete a task', () => {
  test('hides the task, offers undo and sends nothing until the time ends; undo brings it back', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-task="repo-triage"]').getByRole('button', { name: /More actions for/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.locator('[data-task="repo-triage"]')).toHaveCount(0)
    const toast = page.getByTestId('task-undo-toast')
    await expect(toast).toContainText('Task "repo-triage" deleted')
    expect(seen.deletes).toEqual([])
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page.locator('[data-task="repo-triage"]')).toBeVisible()
    await expect(toast).toHaveCount(0)
    expect(seen.deletes).toEqual([])
  })

  test('without undo the delete is sent when the time ends', async ({ page }) => {
    await page.clock.install()
    const { seen } = await open(page)
    await page.locator('[data-task="repo-triage"]').getByRole('button', { name: /More actions for/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('task-undo-toast')).toBeVisible()
    expect(seen.deletes).toEqual([])
    await page.clock.runFor(30500)
    await expect.poll(() => seen.deletes).toEqual(['t2'])
    await expect(page.getByText('Task deleted')).toBeVisible()
  })

  test('leaving the page during the wait deletes nothing', async ({ page }) => {
    const { seen } = await open(page)
    await page.locator('[data-task="repo-triage"]').getByRole('button', { name: /More actions for/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('task-undo-toast')).toBeVisible()
    await page.getByRole('link', { name: 'Skills' }).first().click()
    await expect(page.getByText('Nothing was deleted, because the page was closed first.')).toBeVisible()
    expect(seen.deletes).toEqual([])
  })
})

test.describe('Jobs page on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('fits the width, keeps the essentials and drops the wide columns', async ({ page }) => {
    await open(page)
    await expect(page.locator('[data-task="daily-digest"]')).toBeVisible()
    await expect(page.locator('[data-task="daily-digest"] .aj-sched')).toBeHidden()
    await expect(page.locator('[data-task="daily-digest"]').getByRole('button', { name: 'Run now' })).toBeVisible()
    await expect(page.locator('[data-job="job-fail-0001"]')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
