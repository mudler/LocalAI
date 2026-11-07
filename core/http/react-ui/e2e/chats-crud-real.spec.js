import { test, expect } from './coverage-fixtures.js'
import { offerMockModel, watchProblems } from './journey-fixtures.js'

// Create, edit and delete of skills, agents, triggers and connector bindings
// through the pages, against the real test server and its real stores. After
// each step the spec reads the API, so a form that only looks saved fails.

const unique = (p) => `${p}-${Date.now().toString(36)}`

async function confirmDelete(page) {
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
}

const json = async (page, path) => (await page.request.get(path)).json()

test.describe('Skills, agents, triggers and connectors against the real server', () => {
  test('a personal skill is created, edited and deleted', async ({ page }) => {
    const watch = watchProblems(page, { ignore: [/notifications\/events/] })
    const name = unique('skill')
    await page.goto('/app/skills')
    test.skip(await page.getByTestId('files-off').isVisible(), 'files are off on this server build')

    await page.goto('/app/skills/new')
    await page.getByLabel(/^Name/).fill(name)
    await page.getByLabel(/^Description/).fill('Check things')
    await page.locator('.skilledit-sidebar-item', { hasText: 'Content' }).click()
    await page.getByLabel('Skill content (markdown)').fill('Read first.')
    await page.getByRole('button', { name: 'Create skill' }).click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    await expect(page.getByTestId(`skill-row-${name}`)).toBeVisible()

    let stored = await json(page, '/api/chats/skills')
    expect(stored.skills.find(s => s.name === name)?.layer).toBe('personal')

    await page.getByTestId(`skill-row-${name}`).click()
    await page.getByRole('button', { name: 'Edit' }).click()
    await expect(page).toHaveURL(new RegExp(`/app/skills/edit/personal/${name}$`))
    await page.getByLabel(/^Description/).fill('Check things twice')
    await page.getByRole('button', { name: /^Save/ }).click()
    await expect.poll(async () => (await json(page, '/api/chats/skills')).skills.find(s => s.name === name)?.description).toBe('Check things twice')

    await page.goto('/app/skills')
    await page.getByTestId(`skill-row-${name}`).click()
    await page.getByRole('button', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect(page.getByTestId(`skill-row-${name}`)).toHaveCount(0)
    stored = await json(page, '/api/chats/skills')
    expect(stored.skills.some(s => s.name === name)).toBe(false)
    expect(watch.problems()).toEqual([])
  })

  test('an agent is created, edited, used to start a chat, and deleted', async ({ page }) => {
    const name = unique('agent')
    await offerMockModel(page)
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill(name)
    await page.getByLabel('Description').fill('Reviews text')
    await page.getByLabel('Instructions').fill('Be brief.')
    await page.getByRole('button', { name: 'Create Agent' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    const row = page.locator(`[data-agent="${name}"]`)
    await expect(row).toBeVisible()
    await expect(row).toContainText('Reviews text')

    const saved = (await json(page, '/api/chats/agents')).agents.find(a => a.name === name)
    expect(saved.instructions).toBe('Be brief.')
    expect(saved.addons).toEqual([])

    await page.goto(`/app/agents/${saved.id}/edit`)
    await page.getByLabel('Description').fill('Reviews text, briefly')
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect(page).toHaveURL(/\/app\/agents$/)
    await expect(row).toContainText('briefly')
    expect((await json(page, `/api/chats/agents/${saved.id}`)).description).toBe('Reviews text, briefly')

    // The agent is offered on the new chat page and a chat with it keeps the link.
    await row.getByRole('button', { name: 'Chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats/)
    await expect(page.getByTestId('chat-agent-picker')).toContainText(name)

    await page.goto('/app/agents')
    await row.getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    // No question: the row goes at once and the delete is sent when the
    // Undo window (8 s) closes.
    await expect(row).toHaveCount(0)
    await expect.poll(async () => (await json(page, '/api/chats/agents')).agents.some(a => a.id === saved.id), { timeout: 15_000 }).toBe(false)
  })

  test('a trigger is created on a schedule, is switched off and deleted', async ({ page }) => {
    const name = unique('trigger')
    await offerMockModel(page)
    await page.goto('/app/triggers/new')
    await page.getByLabel('Name', { exact: true }).fill(name)
    await page.getByLabel('What to do', { exact: true }).fill('Say hello')
    await expect(page.getByTestId('trigger-preview')).toContainText('Next:')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect(page).toHaveURL(/\/app\/triggers$/)

    const saved = (await json(page, '/api/chats/triggers')).triggers.find(t => t.name === name)
    expect(saved.enabled).toBe(true)
    expect(saved.prompt).toBe('Say hello')

    await page.goto(`/app/triggers/${saved.id}`)
    await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
    await page.getByTestId('task-facts').getByRole('switch').click()
    await expect.poll(async () => (await json(page, `/api/chats/triggers/${saved.id}`)).enabled).toBe(false)

    await page.getByRole('button', { name: /More actions/ }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await confirmDelete(page)
    await expect.poll(async () => (await page.request.get(`/api/chats/triggers/${saved.id}`)).status()).toBe(404)
  })
})

test.describe('Connector bindings against the real server', () => {
  // A binding is made for a kind that a running add-on serves. The test server
  // runs none, so the server refuses the binding and says so; listing, switching
  // and token handling of a binding are covered by the add-on specs.
  test('a binding for a kind no add-on serves is refused, and nothing is stored or leaked', async ({ page }) => {
    const name = unique('bot')
    const made = await page.request.post('/api/chats/agents', { data: { name: `${name}-agent`, model: 'mock-model', instructions: 'x' } })
    expect(made.ok()).toBe(true)
    const agent = await made.json()
    const before = await (await page.request.get('/api/chats/connectors')).json()
    const bound = await page.request.post('/api/chats/connectors', { data: { kind: 'telegram', name, token: '123456:SECRET-TOKEN-VALUE', agent_id: agent.id, enabled: false } })
    expect(bound.status()).toBe(400)
    const body = await bound.text()
    expect(body).toContain('no installed add-on serves the kind')
    expect(body).not.toContain('SECRET-TOKEN-VALUE')
    const text = await (await page.request.get('/api/chats/connectors')).text()
    expect(text).not.toContain(name)
    expect(text).not.toContain('SECRET-TOKEN-VALUE')
    expect(JSON.stringify(before)).not.toContain(name)
    await page.request.delete(`/api/chats/agents/${agent.id}`)
  })
})
