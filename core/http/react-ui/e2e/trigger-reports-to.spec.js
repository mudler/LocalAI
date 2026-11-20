import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'
import { REPORT_TRIGGERS } from './trigger-reports-fixtures.js'

// Where the runs of a trigger report: on the list, on the trigger page and in
// the open row of the history.

const row = (page, name) => page.locator(`[data-task="${name}"]`)

test.describe('Reports to', () => {
  test('the list says where each trigger reports, and warns when the destination cannot receive', async ({ page }) => {
    await installAgents(page, { triggers: REPORT_TRIGGERS, runs: {} })
    await page.goto('/app/triggers')
    await expect(row(page, 'To the Inbox').getByTestId('reports-to')).toContainText('Inbox')
    await expect(row(page, 'To the Inbox').getByTestId('reports-to')).toHaveAttribute('data-kind', 'inbox')
    const channel = row(page, 'To a channel').getByTestId('reports-to')
    await expect(channel).toContainText('Channel General')
    await expect(channel).toContainText('Team bot (Telegram)')
    await expect(channel.getByTestId('reports-to-problem')).toHaveCount(0)
    await expect(row(page, 'To a chat').getByTestId('reports-to')).toContainText('Chat 123…789')
    await expect(row(page, 'To a chat').getByTestId('reports-to')).toContainText('Slack')
    await expect(row(page, 'Connection off').getByTestId('reports-to-problem')).toContainText('Connection is off')
    await expect(row(page, 'Connection deleted').getByTestId('reports-to-problem')).toContainText('Connection deleted')
  })

  test('the trigger page shows the destination, the mode, the fallback and a link to the connections', async ({ page }) => {
    await installAgents(page, { triggers: REPORT_TRIGGERS, runs: {} })
    await page.goto('/app/triggers/d4')
    const facts = page.getByTestId('task-reports')
    await expect(facts).toContainText('Channel Alerts')
    await expect(facts).toContainText('Only when a run fails')
    await expect(facts.getByTestId('reports-to-problem')).toContainText('Connection is off')
    await expect(facts).toContainText('reports go to the Inbox')
    await expect(facts.getByRole('link', { name: 'Manage connection' })).toHaveAttribute('href', /connectors\?tab=messaging/)

    await page.goto('/app/triggers/d1')
    await expect(page.getByTestId('task-reports')).toContainText('Inbox')
    await expect(page.getByTestId('task-reports').getByRole('link')).toHaveCount(0)
  })

  test('never shows a token or a whole webhook address', async ({ page }) => {
    await installAgents(page, { triggers: REPORT_TRIGGERS, runs: {} })
    await page.goto('/app/triggers')
    await expect(row(page, 'To a chat')).toBeVisible()
    await expect(page.locator('body')).not.toContainText(/https?:\/\/hooks|xox[bp]-|bot\d+:/)
  })
})
