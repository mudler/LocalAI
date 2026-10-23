import { test, expect } from './coverage-fixtures.js'
import { installFakeStream, mockAgents, seedRuns, storedRun, emit, AGENTS } from './agents-fixtures.js'

const TASK = 'Find two sources on speculative decoding and summarise them.'
const OUTCOME = 'I read two sources.\n\n| A | B | C | D |\n| - | - | - | - |\n| 1 | 2 | 3 | 4 |'

async function startRun(page, seen) {
  await page.goto('/app/agents/research-assistant')
  await expect(page.getByTestId('agent-page')).toBeVisible()
  await page.locator('#ag-task-input').fill(TASK)
  await page.getByRole('button', { name: 'Start run' }).click()
  await expect(page).toHaveURL(/\/app\/agents\/research-assistant\/runs\/r_[a-z0-9]{6}$/)
  await expect(page.getByTestId('run-live')).toBeVisible()
  await expect.poll(() => seen.chats.length).toBe(1)
}

test.describe('Agent page', () => {
  test('shows what the agent is: model, tools, memory, skills, instructions', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/research-assistant')
    const facts = page.getByTestId('agent-facts')
    await expect(facts).toContainText('qwen3-14b-instruct')
    await expect(facts).toContainText('web_search')
    await expect(facts).toContainText('mcp: filesystem')
    await expect(facts.getByRole('link', { name: 'Knowledge base' })).toHaveAttribute('href', '/app/collections')
    await expect(facts.getByRole('link', { name: 'citations' })).toHaveAttribute('href', '/app/skills')
    await expect(facts).toContainText('You are a careful research assistant')
    await expect(page.getByText('No runs yet')).toBeVisible()
  })

  test('an agent with no memory or skills shows None, not a guess', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/code-reviewer')
    const facts = page.getByTestId('agent-facts')
    await expect(facts.getByText('None')).toHaveCount(3)
    await expect(facts.getByRole('link')).toHaveCount(0)
  })

  test('lists runs newest first and opens one', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    const t = Date.now()
    await seedRuns(page, 'research-assistant', [
      storedRun('research-assistant', { id: 'r_new', task: 'Newest task', startedAt: t - 1000, outcome: 'Wrote the digest.' }),
      storedRun('research-assistant', { id: 'r_old', task: 'Older task', startedAt: t - 90000, status: 'failed', error: 'The file is not a valid PDF.' }),
    ])
    await page.goto('/app/agents/research-assistant')
    const rows = page.getByTestId('agent-runs').locator('a')
    await expect(rows).toHaveCount(2)
    await expect(rows.first()).toContainText('Newest task')
    await expect(rows.last()).toContainText('The file is not a valid PDF.')
    await rows.last().click()
    await expect(page).toHaveURL(/\/runs\/r_old$/)
    await expect(page.getByTestId('run-report')).toBeVisible()
  })

  test('a paused agent cannot start a run and offers Resume', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await page.goto('/app/agents/daily-digest')
    await expect(page.locator('#ag-task-input')).toBeDisabled()
    await page.getByTestId('agent-page').getByRole('button', { name: 'Resume', exact: true }).first().click()
    await expect.poll(() => seen.resumes).toEqual(['daily-digest'])
  })

  test('an unknown agent says so', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/nobody')
    await expect(page.getByTestId('agent-missing')).toContainText('No agent named "nobody"')
  })

  test('Clear run record removes this browser\'s runs after a confirmation', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await seedRuns(page, 'research-assistant', [storedRun('research-assistant', { id: 'r_x', task: 'One', startedAt: Date.now() - 1000, outcome: 'ok' })])
    await page.goto('/app/agents/research-assistant')
    await expect(page.getByTestId('agent-runs')).toBeVisible()
    await page.getByTestId('agent-page').getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Clear run record' }).click()
    await page.getByRole('button', { name: 'Clear', exact: true }).click()
    await expect(page.getByText('No runs yet')).toBeVisible()
  })
})

test.describe('A run', () => {
  test('starts from the task box, shows the thread live and settles into the report', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    expect(seen.chats[0]).toMatchObject({ name: 'research-assistant', message: TASK, history: [] })

    await expect(page.getByTestId('run-task')).toContainText(TASK)
    await emit(page, 'stream_event', { type: 'reasoning', content: 'Search first.' })
    await emit(page, 'stream_event', { type: 'done' })
    await emit(page, 'stream_event', { type: 'tool_call', tool_name: 'web_search', tool_args: '{"q":"x"}' })
    await expect(page.getByTestId('run-working')).toContainText('Using web_search')
    await emit(page, 'stream_event', { type: 'tool_result', tool_name: 'web_search', tool_result: '8 results' })
    await expect(page.getByTestId('run-fold')).toContainText('1 steps so far')

    await emit(page, 'json_message', { sender: 'agent', content: OUTCOME })
    await expect(page.getByTestId('agent-run')).toHaveAttribute('data-status', 'done')
    await expect(page.getByTestId('run-settling')).toContainText('Report ready')
    await expect(page.getByTestId('run-report')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: 'Outcome' })).toBeVisible()
    await expect(page.getByTestId('run-evidence')).toContainText('8 results')
    await expect(page.getByRole('heading', { name: 'Steps' })).toBeVisible()
    // Wide content opens wider on demand.
    const wideBtn = page.locator('.ag-wide__btn').first()
    await expect(wideBtn).toHaveText('Open wider')
    await wideBtn.click()
    await expect(page.locator('.ag-wide[data-wide]')).toHaveCount(1)
    await expect(wideBtn).toHaveText('Back to column')
  })

  test('choosing a view stops the page from moving by itself', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    await page.getByRole('button', { name: 'Live' }).click()
    await emit(page, 'json_message', { sender: 'agent', content: 'Done.' })
    await page.waitForTimeout(2200)
    await expect(page.getByTestId('run-live')).toBeVisible()
    await expect(page.getByTestId('run-report')).toHaveCount(0)
  })

  test('has its own address that survives a reload', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    await emit(page, 'json_message', { sender: 'agent', content: 'The answer.' })
    await expect(page.getByTestId('run-report')).toBeVisible({ timeout: 5000 })
    const url = page.url()
    await page.reload()
    await expect(page).toHaveURL(url)
    await expect(page.getByTestId('run-report')).toContainText('The answer.')
    // The reload did not send the task again.
    expect(seen.chats.length).toBe(1)
  })

  test('a run from another browser opens a notice, not an error', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await page.goto('/app/agents/research-assistant/runs/r_nope12')
    await expect(page.getByTestId('run-missing')).toContainText('not in this browser')
    await expect(page.getByRole('link', { name: 'Open research-assistant' }).last()).toBeVisible()
  })

  test('a follow-up sits under the outcome and carries the earlier turns', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    await emit(page, 'json_message', { sender: 'agent', content: 'First answer.' })
    await expect(page.getByTestId('run-report')).toBeVisible({ timeout: 5000 })
    await page.getByTestId('run-followup').locator('textarea').fill('Add one more source')
    await page.getByRole('button', { name: 'Send' }).click()
    await expect.poll(() => seen.chats.length).toBe(2)
    expect(seen.chats[1].history).toEqual([
      { role: 'user', content: TASK },
      { role: 'assistant', content: 'First answer.' },
    ])
    await emit(page, 'json_message', { sender: 'agent', content: 'Second answer.' })
    await expect(page.getByTestId('run-report')).toBeVisible({ timeout: 5000 })
    await expect(page.getByTestId('run-followups')).toContainText('Add one more source')
    await expect(page.getByTestId('run-followups')).toContainText('Second answer.')
  })

  test('the agent name switches to another agent', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    await seedRuns(page, 'research-assistant', [storedRun('research-assistant', { id: 'r_s', task: 'T', startedAt: Date.now() - 1000, outcome: 'ok' })])
    await page.goto('/app/agents/research-assistant/runs/r_s')
    await page.getByTestId('agent-switcher').click()
    await page.getByRole('menuitem', { name: 'code-reviewer' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/code-reviewer$/)
  })

  test('there is no Stop, approval or steer control the API cannot honour', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    await expect(page.getByRole('button', { name: /^Stop$/ })).toHaveCount(0)
    await expect(page.getByRole('button', { name: /Approve|Allow/ })).toHaveCount(0)
    await expect(page.getByTestId('run-dock-note')).toContainText('Follow-ups open when it answers')
  })
})

test.describe('A run that fails', () => {
  test('a task the server refuses says so in plain words with one next action', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page, { chatStatus: 500, chatError: 'model is not loaded' })
    await page.goto('/app/agents/research-assistant')
    await page.locator('#ag-task-input').fill(TASK)
    await page.keyboard.press('Enter')
    const failed = page.getByTestId('run-failed')
    await expect(failed).toContainText('The task did not reach the agent')
    await expect(failed).toContainText('model is not loaded')
    await expect(failed.getByRole('button')).toHaveCount(1)
    await expect(page.getByTestId('agent-run')).toHaveAttribute('data-status', 'failed')
    await failed.getByRole('button', { name: 'Run again' }).click()
    await expect.poll(() => seen.chats.length).toBe(2)
    await expect(page).toHaveURL(/\/runs\/r_[a-z0-9]{6}$/)
  })

  test('an error the agent reports stays in the live view and is the outcome in the report', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page)
    await startRun(page, seen)
    await emit(page, 'json_error', { error: 'A page did not answer' })
    await expect(page.getByTestId('run-failed')).toContainText('The run stopped')
    await expect(page.getByTestId('run-live')).toBeVisible()
    await page.getByRole('button', { name: 'Report' }).click()
    await expect(page.getByTestId('run-report').getByTestId('run-failed')).toContainText('A page did not answer')
  })

  test('a run cut off before it answered reads as stopped', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page)
    const started = Date.now() - 20 * 60_000
    await seedRuns(page, 'research-assistant', [storedRun('research-assistant', { id: 'r_cut', task: 'Cut off', startedAt: started, status: 'running' })])
    await page.goto('/app/agents/research-assistant/runs/r_cut')
    await expect(page.getByTestId('agent-run')).toHaveAttribute('data-status', 'stopped')
    await expect(page.getByTestId('run-report')).toContainText('No answer was recorded')
  })
})

test.describe('Status page', () => {
  test('is a quiet panel with the raw record and a way back', async ({ page }) => {
    await installFakeStream(page)
    await mockAgents(page, { observables: { 'research-assistant': [
      { id: 1, name: 'web_search', creation: { function_definition: { name: 'web_search' } }, completion: { action_result: '8 results' } },
      { id: 2, name: 'read_page', completion: { error: 'page did not answer' } },
    ] } })
    await page.goto('/app/agents/research-assistant/status')
    const panel = page.getByTestId('agent-status')
    await expect(panel.getByRole('heading', { name: 'Status' })).toBeVisible()
    await expect(panel.getByTestId('status-observable')).toHaveCount(2)
    await panel.getByRole('button', { name: 'Failed', exact: true }).click()
    await expect(panel.getByTestId('status-observable')).toHaveCount(1)
    await panel.getByRole('link', { name: 'research-assistant' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/research-assistant$/)
  })

  test('Clear removes the records through the API', async ({ page }) => {
    await installFakeStream(page)
    const seen = await mockAgents(page, { observables: { 'research-assistant': [{ id: 1, name: 'x', completion: {} }] } })
    await page.goto('/app/agents/research-assistant/status')
    await page.getByRole('button', { name: 'Clear' }).click()
    await expect.poll(() => seen.clears).toEqual(['research-assistant'])
  })
})

test.describe('Layouts', () => {
  async function seeded(page) {
    await installFakeStream(page)
    await mockAgents(page)
    await seedRuns(page, 'research-assistant', [storedRun('research-assistant', { id: 'r_l', task: TASK, startedAt: Date.now() - 4000, outcome: OUTCOME, steps: [{ kind: 'tool', name: 'web_search', args: '{}', result: 'r', ts: Date.now() - 3000 }] })])
  }
  const noOverflow = page => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)

  test('phone: no sideways scroll on the launcher, agent page, report and editor', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await seeded(page)
    for (const path of ['/app/agents', '/app/agents/research-assistant', '/app/agents/research-assistant/runs/r_l', '/app/agents/research-assistant/edit', '/app/agents/research-assistant/status']) {
      await page.goto(path)
      await page.waitForTimeout(400)
      expect(await noOverflow(page), path).toBe(true)
    }
  })

  test('phone: the preview opens as a sheet that fits the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await seeded(page)
    await page.goto('/app/agents/research-assistant/edit')
    await page.getByTestId('agent-preview-open').click()
    await page.waitForTimeout(600) // the sheet rises into place
    const box = await page.getByTestId('agent-preview').boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(box.y + box.height).toBeLessThanOrEqual(844 + 1)
  })

  test('2560 wide: the report keeps a reading column and opens a table wider on demand', async ({ page }) => {
    await page.setViewportSize({ width: 2560, height: 1200 })
    await seeded(page)
    await page.goto('/app/agents/research-assistant/runs/r_l')
    const col = page.locator('.ag-doc__in')
    const colBox = await col.boundingBox()
    expect(colBox.width).toBeLessThanOrEqual(830)
    // Centred in the area beside the sidebar.
    expect(colBox.x).toBeGreaterThan(500)
    await page.locator('.ag-wide__btn').first().click()
    const wide = await page.locator('.ag-wide[data-wide]').boundingBox()
    expect(wide.width).toBeGreaterThan(colBox.width)
    expect(wide.x + wide.width).toBeLessThanOrEqual(2560)
  })

  test('1440: the run page uses the page height and docks the follow-up box', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await seeded(page)
    await page.goto('/app/agents/research-assistant/runs/r_l')
    const dock = await page.getByTestId('run-followup').boundingBox()
    expect(dock.y + dock.height).toBeGreaterThan(780)
    expect(await noOverflow(page)).toBe(true)
  })

  test('reduced motion: the working dot does not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await installFakeStream(page)
    await mockAgents(page, { observables: { 'research-assistant': [{ id: 1, name: 'x' }] } })
    await page.goto('/app/agents/research-assistant')
    const anim = await page.locator('.ag-state[data-state="running"] .dk-dot').first().evaluate(el => getComputedStyle(el).animationName)
    expect(anim).toBe('none')
  })
})
