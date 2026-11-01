import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'
import { readFile } from 'node:fs/promises'

// The agents list and the page of one agent, with the saved-agents API stubbed.

test.describe('Agents list', () => {
  test('lists the agents with their model, their record and what ran with them', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents')

    await expect(page.getByRole('heading', { name: 'Agents', level: 1 })).toBeVisible()
    const rows = page.getByTestId('agents-list').locator('.ag-row')
    await expect(rows).toHaveCount(2)
    const research = page.locator('[data-agent="research-assistant"]')
    await expect(research).toContainText('Searches the web and writes short briefs.')
    await expect(research).toContainText('qwen3-8b')
    await expect(research).toContainText('handbook')
    await expect(research).toContainText('citations')
    // Two chats ran with it: one finished, one failed.
    await expect(research.getByTestId('agent-run-strip')).toBeVisible()
    await expect(research).toContainText('50% of 2 runs finished')
    await expect(page.locator('[data-agent="code-reviewer"]')).toContainText('No runs yet')
    // The failed run of the last day is in Now.
    await expect(page.getByTestId('agents-now')).toContainText('research-assistant')
  })

  test('searches by name, description and model', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents')
    await page.getByLabel('Search agents').fill('reviews a change')
    await expect(page.getByTestId('agents-list').locator('.ag-row')).toHaveCount(1)
    await expect(page.locator('[data-agent="code-reviewer"]')).toBeVisible()
    await page.getByLabel('Search agents').fill('nothing like it')
    await expect(page.getByText('No matching agents')).toBeVisible()
  })

  test('with no agents it offers the starting points and a link to create one', async ({ page }) => {
    await installAgents(page, { agents: [] })
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-empty')).toBeVisible()
    await page.getByRole('link', { name: 'Research assistant' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/new\?template=researcher$/)
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('research-assistant')
  })

  test('Chat opens the chat page with this agent chosen, and the first message uses it', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents')
    await page.locator('[data-agent="code-reviewer"]').getByRole('button', { name: 'Chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats$/)
    await expect(page.getByTestId('chat-agent-picker')).toContainText('code-reviewer')
    await page.getByTestId('chat-input').fill('hello')
    await page.getByTestId('chat-send').click()
    await expect.poll(() => s.createdChats.length).toBe(1)
    expect(s.createdChats[0]).toMatchObject({ agent_id: 'a2' })
  })

  test('deletes an agent at once with an Undo; Undo brings it back and sends nothing', async ({ page }) => {
    // Delete is reversible for the Undo window (8 s), so it asks nothing and
    // the server hears about it only when the window closes.
    await page.clock.install()
    const s = await installAgents(page)
    await page.goto('/app/agents')
    const del = async () => {
      await page.locator('[data-agent="code-reviewer"]').getByRole('button', { name: /More actions/ }).click()
      await page.getByRole('menuitem', { name: 'Delete' }).click()
    }
    await del()
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    await expect(page.locator('[data-agent="code-reviewer"]')).toHaveCount(0)
    const toast = page.getByTestId('agent-undo-toast')
    await expect(toast).toContainText('Deleted “code-reviewer”.')
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page.locator('[data-agent="code-reviewer"]')).toBeVisible()
    await page.clock.runFor(9000)
    expect(s.calls.some(c => c.method === 'DELETE')).toBe(false)

    await del()
    await expect(page.locator('[data-agent="code-reviewer"]')).toHaveCount(0)
    await page.clock.runFor(9000)
    await expect.poll(() => s.calls.filter(c => c.method === 'DELETE').length).toBe(1)
    await expect(page.locator('[data-agent="code-reviewer"]')).toHaveCount(0)
  })

  test('exports an agent as the file the server writes, without id, dates or secrets', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents')
    await page.locator('[data-agent="research-assistant"]').getByRole('button', { name: /More actions/ }).click()
    const download = page.waitForEvent('download')
    await page.getByRole('menuitem', { name: 'Export' }).click()
    const file = await download
    expect(file.suggestedFilename()).toBe('research-assistant.json')
    expect(s.calls.some(c => c.method === 'GET' && c.url === '/api/chats/agents/a1/export')).toBe(true)
    const body = JSON.parse(await readFile(await file.path(), 'utf8'))
    expect(body).toMatchObject({ version: 1, kind: 'localai-agent' })
    expect(body.agent).toMatchObject({ name: 'research-assistant', skills: ['citations'], collections: ['handbook'], addons: ['office'] })
    expect(body.agent.id).toBeUndefined()
    expect(body.agent.created).toBeUndefined()
  })

  test('says why the list is unavailable when agents are off', async ({ page }) => {
    await installAgents(page)
    await page.route(/\/api\/chats\/agents(\?.*)?$/, route => route.fulfill({ status: 409, json: { error: { message: 'Agents are off on this server.', type: 'agents_off', code: 409 } } }))
    await page.goto('/app/agents')
    await expect(page.getByRole('note')).toContainText('Agents are off on this server.')
  })
})

test.describe('Agent page', () => {
  test('shows what the agent is, its triggers and the chats that ran with it', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents/a1')

    await expect(page.getByRole('heading', { name: 'research-assistant', level: 1 })).toBeVisible()
    const facts = page.getByTestId('agent-facts')
    await expect(facts).toContainText('qwen3-8b')
    await expect(facts).toContainText('office')
    await expect(facts).toContainText('mcp: crm')
    await expect(facts).toContainText('handbook')
    await expect(facts).toContainText('citations')
    await expect(page.getByTestId('agent-triggers')).toContainText('Morning digest')
    const runs = page.getByTestId('agent-runs')
    await expect(runs.locator('.ag-run-row')).toHaveCount(2)
    await expect(runs).toContainText('Find sources on speculative decoding')
    await expect(runs).toContainText('Started by a trigger')
    // A run opens its chat.
    await runs.locator('[data-run="c1"]').click()
    await expect(page).toHaveURL(/\/app\/chats\/c1$/)
  })

  test('the old address by name finds the agent, and the old status page redirects', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/agents/code-reviewer')
    await expect(page.getByRole('heading', { name: 'code-reviewer', level: 1 })).toBeVisible()
    await page.goto('/app/agents/a2/status')
    await expect(page).toHaveURL(/\/app\/agents\/a2$/)
    await page.goto('/app/agents/nobody')
    await expect(page.getByTestId('agent-missing')).toBeVisible()
  })

  test('Chat opens the chat page with the agent chosen; Edit and Delete work', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/agents/a2')
    await page.getByTestId('agent-chat').click()
    await expect(page).toHaveURL(/\/app\/chats$/)
    await expect(page.getByTestId('chat-agent-picker')).toContainText('code-reviewer')

    await page.goto('/app/agents/a2')
    await page.getByRole('link', { name: 'Edit' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/a2\/edit$/)

    // Delete goes back to the list, where the agent is gone at once and the
    // server is asked when the Undo window closes.
    await page.clock.install()
    await page.goto('/app/agents/a2')
    await page.getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    await expect(page.getByTestId('agent-undo-toast')).toBeVisible()
    await expect(page.locator('[data-agent="code-reviewer"]')).toHaveCount(0)
    await page.clock.runFor(9000)
    await expect.poll(() => s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/agents/a2')).toBe(true)
  })
})

test('the old addresses of the agents page redirect', async ({ page }) => {
  await installAgents(page)
  await page.goto('/app/chats/agents')
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(AGENTS.length).toBe(2)
})
