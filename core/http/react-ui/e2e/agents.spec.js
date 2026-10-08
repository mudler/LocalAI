import { test, expect } from './coverage-fixtures.js'
import { installFakeStream, mockAgents, seedRuns, storedRun, emit, AGENTS } from './agents-fixtures.js'

// Agents launcher (src/pages/Agents.jsx) and the routes around it.
test.describe('Agents page', () => {
  test('renders the agents list and empty state', async ({ page }) => {
    await page.goto('/app/agents')
    await expect(page).toHaveURL(/\/app\/agents$/)
    await expect(page.getByRole('heading', { name: 'Agents', exact: true })).toBeVisible()
    await expect(page.getByText(/No agents configured/i)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Create Agent' }).first()).toBeVisible()
  })

  test('the empty state teaches with starting points', async ({ page }) => {
    await page.goto('/app/agents')
    const empty = page.getByTestId('agents-empty')
    await expect(empty.getByRole('link', { name: 'Research assistant' })).toHaveAttribute('href', '/app/agents/new?template=researcher')
    await expect(empty.getByRole('link', { name: 'Code reviewer' })).toBeVisible()
  })

  test('keeps Import Agent visible when agents exist', async ({ page }) => {
    await page.route('**/api/agents', route => route.fulfill({
      json: { agents: ['existing-agent'], statuses: { 'existing-agent': true } },
    }))
    await page.route('**/api/agents/existing-agent/observables', route => route.fulfill({
      json: { History: [] },
    }))
    await page.goto('/app/agents')
    await expect(page.locator('.header-actions label', { hasText: 'Import' })).toBeVisible()
  })

  test('Create Agent navigates to the agent creation form', async ({ page }) => {
    await page.goto('/app/agents')
    const create = page.getByRole('button', { name: 'Create Agent' }).last()
    await create.scrollIntoViewIfNeeded()
    await Promise.all([
      page.waitForURL(/\/app\/agents\/new$/),
      create.click(),
    ])
    // Wait for AgentCreate.jsx to actually render, not just for the URL to
    // change, so the coverage teardown does not race the component mount.
    await expect(page.getByRole('heading', { name: 'Create Agent' })).toBeVisible()
  })

  test('opens agent status without leaving an active run', async ({ page }) => {
    await mockAgents(page)
    await installFakeStream(page)
    await seedRuns(page, 'research-assistant', [storedRun('research-assistant', { id: 'r_a1', task: 'Do it', startedAt: Date.now() - 5000, outcome: 'Done.' })], 'test-user')
    await page.goto('/app/agents/research-assistant/runs/r_a1?user_id=test-user')
    const status = page.getByRole('link', { name: 'Status' })
    await expect(status).toHaveAttribute('href', '/app/agents/research-assistant/status?user_id=test-user')
    await expect(status).toHaveAttribute('target', '_blank')
    await expect(page).toHaveURL(/\/runs\/r_a1\?user_id=test-user$/)
  })

  test('an old chat link opens the agent page and keeps the user', async ({ page }) => {
    await mockAgents(page)
    await page.goto('/app/agents/research-assistant/chat?user_id=u7')
    await expect(page).toHaveURL(/\/app\/agents\/research-assistant\?user_id=u7$/)
    await expect(page.getByTestId('agent-page')).toBeVisible()
  })
})

test.describe('Launcher with agents', () => {
  const now = Date.now()
  const hours = h => now - h * 3600_000

  async function open(page, opts = {}) {
    await installFakeStream(page)
    const seen = await mockAgents(page, {
      observables: { 'research-assistant': [{ id: 7, name: 'read_page', creation: { function_definition: { name: 'read_page' } } }] },
      ...opts,
    })
    const runs = []
    for (let i = 0; i < 16; i++) {
      runs.push(storedRun('code-reviewer', {
        id: `r_c${i}`, task: `Review ${i}`, startedAt: hours(60 - i), status: i === 3 ? 'failed' : i === 5 ? 'stopped' : 'done', outcome: 'ok', error: i === 3 ? 'boom' : '',
      }))
    }
    await seedRuns(page, 'code-reviewer', runs.reverse())
    await seedRuns(page, 'daily-digest', [storedRun('daily-digest', { id: 'r_d1', task: 'Collect news', startedAt: hours(2), status: 'failed', error: 'A page did not answer.' })])
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list')).toBeVisible()
    return seen
  }

  test('shows each agent with its model, attached chips and run strip', async ({ page }) => {
    await open(page)
    const row = page.locator('[data-agent="research-assistant"]')
    await expect(row.getByText('qwen3-14b-instruct')).toBeVisible()
    await expect(row.getByRole('link', { name: 'Knowledge base' })).toHaveAttribute('href', '/app/collections')
    await expect(row.getByRole('link', { name: 'citations' })).toHaveAttribute('href', '/app/skills')
    const strip = page.locator('[data-agent="code-reviewer"] [data-testid="agent-run-strip"]')
    await expect(strip.locator('.ag-pip')).toHaveCount(14)
    await expect(strip.locator('.ag-pip[data-state="failed"]')).toHaveCount(1)
    await expect(strip.locator('.ag-pip[data-state="stopped"]')).toHaveCount(1)
    await expect(page.locator('[data-agent="code-reviewer"]')).toContainText('of 16 runs finished')
  })

  test('an agent with no runs says so instead of showing a record', async ({ page }) => {
    await open(page)
    await expect(page.locator('[data-agent="research-assistant"]')).toContainText('No runs recorded here yet')
  })

  test('the Now strip lists work in flight and a run that failed in the last day', async ({ page }) => {
    await open(page)
    const now = page.getByTestId('agents-now')
    await expect(now).toContainText('research-assistant')
    await expect(now).toContainText('Working')
    await expect(now).toContainText('daily-digest')
    await expect(now).toContainText('Failed')
    await expect(now).toContainText('A page did not answer.')
    await now.getByRole('link', { name: 'Open' }).last().click()
    await expect(page).toHaveURL(/\/app\/agents\/daily-digest\/runs\/r_d1$/)
  })

  test('Run again from the Now strip puts the task in the agent page box', async ({ page }) => {
    await open(page)
    await page.getByTestId('agents-now').getByRole('link', { name: 'Run again' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/daily-digest$/)
    await expect(page.locator('#ag-task-input')).toHaveValue('Collect news')
  })

  test('search narrows the list and says when nothing matches', async ({ page }) => {
    await open(page)
    await page.getByLabel('Search agents...').fill('review')
    await expect(page.locator('[data-agent]')).toHaveCount(1)
    await page.getByLabel('Search agents...').fill('zzz')
    await expect(page.getByText('No matching agents')).toBeVisible()
  })

  test('the row menu pauses an agent through the API', async ({ page }) => {
    const seen = await open(page)
    await page.locator('[data-agent="code-reviewer"]').getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Pause' }).click()
    await expect.poll(() => seen.pauses).toEqual(['code-reviewer'])
  })

  test('no Now strip when nothing needs a look', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page, { agents: { 'code-reviewer': AGENTS['code-reviewer'] } })
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list')).toBeVisible()
    await expect(page.getByTestId('agents-now')).toHaveCount(0)
  })
})
