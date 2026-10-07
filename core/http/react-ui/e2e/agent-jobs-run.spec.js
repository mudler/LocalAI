import { test, expect } from './coverage-fixtures.js'
import { mockJobs } from './jobs-fixtures.js'

test.describe('A job run', () => {
  test('a finished job reads as a document: task, outcome, delivery and steps', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-done-0002')
    const doc = page.getByTestId('job-page')
    await expect(doc.getByRole('heading', { level: 1 })).toContainText('repo-triage, today')
    await expect(doc).toContainText('Done')
    await expect(doc).toContainText('Started by the schedule')
    await expect(doc).toContainText('Took 52 s')
    await expect(doc).toContainText('job-done-0002')
    await expect(doc.getByRole('heading', { name: 'Task' })).toBeVisible()
    await expect(doc.locator('.aj-prompt')).toContainText('Read the new issues in example/app and label each one.')
    await expect(doc.getByRole('heading', { name: 'Outcome' })).toBeVisible()
    await expect(doc).toContainText('Labelled 12 issues and routed 3 of them to the person on call.')
    await expect(doc.locator('.cx-prose table')).toBeVisible()
    await expect(doc.getByTestId('job-webhook')).toContainText('Webhook delivered')
    await expect(doc.getByRole('button', { name: 'Run again' })).toBeEnabled()
    await expect(doc.getByRole('link', { name: 'Open task' })).toHaveAttribute('href', '/app/agent-jobs/tasks/t2')
  })

  test('the steps the server recorded are rows that open into what they hold', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-done-0002')
    const rows = page.getByTestId('job-traces').locator('li')
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(1)).toContainText('list_issues')
    await expect(rows.nth(1)).toContainText('Tool call')
    await expect(rows.nth(1).locator('.aj-trace__body')).toHaveCount(0)
    await rows.nth(1).getByRole('button').click()
    await expect(rows.nth(1).locator('.aj-trace__body')).toContainText('example/app')
    await expect(rows.nth(1).locator('.aj-trace__body')).toContainText('Listing issues')
  })

  test('a job with no recorded steps says so', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-done-0004')
    await expect(page.getByText('The server recorded no steps for this job.')).toBeVisible()
    await expect(page.getByTestId('job-traces')).toHaveCount(0)
  })

  test('a failed job says what happened in plain words and offers one next action', async ({ page }) => {
    const { seen } = await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-fail-0001')
    const fail = page.getByTestId('job-failed')
    await expect(fail).toContainText('This job failed.')
    await expect(fail).toContainText('read_page: timeout after 20 s (3 of 3 attempts)')
    await expect(page.locator('.aj-prompt .aj-gap[data-filled]')).toHaveCount(3)
    await fail.getByRole('button', { name: 'Run again now' }).click()
    expect(seen.jobExecutes).toEqual([{ task_id: 't1', parameters: { topic: 'local inference', format: 'bullet points', items: '8' } }])
    await expect(page).toHaveURL(/\/app\/agent-jobs\/jobs\/job-new-0006$/)
    await expect(page.getByTestId('job-working')).toContainText('waiting to start')
  })

  test('a running job says it is working and Cancel stops it', async ({ page }) => {
    const { seen } = await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-run-0003')
    await expect(page.getByTestId('job-working')).toContainText('still running')
    await expect(page.getByRole('button', { name: 'Run again' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByText('Job cancelled')).toBeVisible()
    expect(seen.cancels).toEqual(['job-run-0003'])
    await expect(page.getByText('This job was cancelled before it finished.')).toBeVisible({ timeout: 6000 })
  })

  test('does not invent a duration when the job has no end time', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-stop-0005')
    await expect(page.getByTestId('job-page')).not.toContainText('Took')
    await expect(page.getByTestId('job-page')).toContainText('Cancelled')
    await expect(page.getByTestId('job-page')).toContainText('Started by the API')
  })

  test('says so when the job does not exist', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/nope')
    await expect(page.getByTestId('job-missing')).toContainText('Job not found')
  })

  test('copies its address', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-done-0002')
    await page.getByRole('button', { name: 'Copy link' }).click()
    await expect(page.getByText('Link copied')).toBeVisible()
  })
})

test.describe('A job run on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('fits the width', async ({ page }) => {
    await mockJobs(page)
    await page.goto('/app/agent-jobs/jobs/job-fail-0001')
    await expect(page.getByTestId('job-failed')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
