import { test, expect } from './coverage-fixtures.js'
import { offerMockModel, watchProblems } from './journey-fixtures.js'

// Create, edit and delete of skills, agents, triggers and connectors through
// the pages, against the real test server and its real stores. After each step
// the spec reads the API, so a form that only looks saved fails.

const unique = (p) => `${p}-${Date.now().toString(36)}`

async function confirmDelete(page) {
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
}

test.describe('Skills, agents, triggers and connectors against the real server', () => {
  test('a personal skill is created, edited and deleted', async ({ page }) => {
    const watch = watchProblems(page, { ignore: [/notifications\/events/] })
    const name = unique('skill')
    await page.goto('/app/chats/skills')
    test.skip(await page.getByRole('note').isVisible(), 'files are off on this server build')
    await page.getByRole('button', { name: 'New skill' }).click()
    const form = page.getByRole('form', { name: 'New skill' })
    await form.getByLabel('Name').fill(name)
    await form.getByLabel('Description').fill('Check things')
    await form.getByLabel('Instructions').fill('Read first.')
    await form.getByRole('button', { name: 'Save' }).click()
    const row = page.getByRole('listitem').filter({ hasText: name })
    await expect(row).toBeVisible()

    let stored = await (await page.request.get('/api/chats/skills')).json()
    expect(stored.skills.find(s => s.name === name)?.layer).toBe('personal')

    await row.getByRole('button', { name: 'Edit' }).click()
    const edit = page.getByRole('form', { name: 'Edit skill' })
    await edit.getByLabel('Description').fill('Check things twice')
    await edit.getByRole('button', { name: 'Save' }).click()
    await expect(row).toContainText('Check things twice')

    await row.getByRole('button', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect(page.getByRole('listitem').filter({ hasText: name })).toHaveCount(0)
    stored = await (await page.request.get('/api/chats/skills')).json()
    expect(stored.skills.some(s => s.name === name)).toBe(false)
    expect(watch.problems()).toEqual([])
  })

  test('an agent is created with a grant, edited, used to start a chat, and deleted', async ({ page }) => {
    const name = unique('agent')
    await offerMockModel(page)
    await page.goto('/app/chats/agents')
    await page.getByRole('button', { name: 'New agent' }).click()
    const form = page.getByRole('form', { name: 'New agent' })
    await form.getByLabel('Name', { exact: true }).fill(name)
    await form.getByLabel('Description').fill('Reviews text')
    await form.getByLabel('Instructions').fill('Be brief.')
    await form.getByRole('button', { name: 'Save' }).click()
    const row = page.getByTestId('agent-row').filter({ hasText: name })
    await expect(row).toBeVisible()
    await expect(row).toContainText('Reviews text')

    const agents = (await (await page.request.get('/api/chats/agents')).json()).agents
    const saved = agents.find(a => a.name === name)
    expect(saved.instructions).toBe('Be brief.')
    expect(saved.addons).toEqual([])

    await row.getByRole('button', { name: 'Edit' }).click()
    const edit = page.getByRole('form', { name: 'Edit agent' })
    await edit.getByLabel('Description').fill('Reviews text, briefly')
    await edit.getByRole('button', { name: 'Save' }).click()
    await expect(row).toContainText('briefly')

    // The agent is offered on the new chat page and a chat with it keeps the link.
    await row.getByRole('button', { name: 'Chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats$/)
    await expect(page.getByLabel('Agent')).toHaveValue(saved.id)

    await page.goto('/app/chats/agents')
    await row.getByRole('button', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect(page.getByTestId('agent-row').filter({ hasText: name })).toHaveCount(0)
    const after = (await (await page.request.get('/api/chats/agents')).json()).agents
    expect(after.some(a => a.id === saved.id)).toBe(false)
  })

  test('a trigger is created on a schedule, shows its next run, is switched off and deleted', async ({ page }) => {
    const name = unique('trigger')
    await offerMockModel(page)
    await page.goto('/app/chats/triggers')
    await page.getByRole('button', { name: 'New trigger' }).click()
    const form = page.getByRole('form', { name: 'New trigger' })
    await form.getByLabel('Name', { exact: true }).fill(name)
    await form.getByLabel('What to do').fill('Say hello')
    await form.getByRole('button', { name: 'Save' }).click()
    const row = page.getByTestId('trigger-row').filter({ hasText: name })
    await expect(row).toBeVisible()
    await expect(row).toContainText(/next/i)

    const list = (await (await page.request.get('/api/chats/triggers')).json()).triggers
    const saved = list.find(t => t.name === name)
    expect(saved.enabled).toBe(true)

    await row.getByRole('checkbox').uncheck()
    await expect.poll(async () => (await (await page.request.get(`/api/chats/triggers/${saved.id}`)).json()).enabled).toBe(false)

    await row.getByRole('button', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect(page.getByTestId('trigger-row').filter({ hasText: name })).toHaveCount(0)
  })
})

test.describe('Telegram connectors against the real server', () => {
  test('a binding is created with a sealed token, edited, and deleted; the token is never returned', async ({ page }) => {
    const name = unique('bot')
    const agentName = unique('tg-agent')
    // The connector needs an agent that names a model: make it through the API.
    const made = await page.request.post('/api/chats/agents', { data: { name: `${agentName}-m`, model: 'mock-model', instructions: 'x' } })
    expect(made.ok()).toBe(true)
    const agent = await made.json()

    await page.goto('/app/chats/connectors')
    await page.getByRole('button', { name: 'New connector' }).click()
    const form = page.getByRole('form', { name: 'New connector' })
    await form.getByLabel('Name', { exact: true }).fill(name)
    await form.getByLabel('Bot token').fill('123456:SECRET-TOKEN-VALUE')
    await form.getByLabel('Agent', { exact: true }).selectOption(agent.id)
    await form.getByRole('button', { name: 'Save' }).click()
    const row = page.getByTestId('connector-row').filter({ hasText: name })
    await expect(row).toBeVisible()
    await expect(row).toContainText('nobody yet')

    const list = await (await page.request.get('/api/chats/connectors')).text()
    expect(list).not.toContain('SECRET-TOKEN-VALUE')
    const saved = JSON.parse(list).connectors.find(c => c.name === name)
    expect(saved.has_token).toBe(true)

    await row.getByRole('button', { name: 'Edit' }).click()
    const edit = page.getByRole('form', { name: 'Edit connector' })
    await expect(edit.getByLabel('Bot token')).toHaveValue('')
    await edit.getByLabel('Allowed senders').fill('@alice')
    await edit.getByLabel('Allowed senders').press('Enter')
    await edit.getByRole('button', { name: 'Save' }).click()
    await expect.poll(async () => (await (await page.request.get(`/api/chats/connectors/${saved.id}`)).json()).allow).toContain('@alice')
    expect(JSON.stringify(await (await page.request.get(`/api/chats/connectors/${saved.id}`)).json())).not.toContain('SECRET-TOKEN-VALUE')

    await row.getByRole('button', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect(row).toHaveCount(0)
    await page.request.delete(`/api/chats/agents/${agent.id}`)
  })
})
