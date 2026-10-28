import { test, expect } from './coverage-fixtures.js'
import { readFileSync } from 'node:fs'
import { offerMockModel } from './journey-fixtures.js'

// The agent files, the starter agents, the agent tasks with their jobs and the
// administrator's view, against the real test server. After each step the
// spec reads the API.

test.describe.configure({ mode: 'serial', timeout: 90_000 })

const api = (request, path) => request.get(path).then(r => r.json())
const uniq = (p) => `${p}-${Date.now().toString(36)}`

test('an agent exports to the server file and imports back as a new agent', async ({ page }) => {
  const name = uniq('exp')
  const made = await (await page.request.post('/api/chats/agents', { data: { name, instructions: 'Be kind', model: 'mock-model' } })).json()
  await page.goto('/app/agents')
  const row = page.locator('.ag-row').filter({ hasText: name }).first()
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: /more|actions/i }).first().click()
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('menuitem', { name: 'Export' }).click()])
  const file = JSON.parse(readFileSync(await download.path(), 'utf8'))
  expect(file.kind).toBe('localai-agent')
  expect(file.agent.name).toBe(name)

  await page.locator('input[type=file][accept=".json"]').setInputFiles({ name: 'a.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(file)) })
  await page.waitForURL(/\/app\/agents\/[^/]+$/)
  const list = (await api(page.request, '/api/chats/agents')).agents.filter(a => a.instructions === 'Be kind' && a.name.startsWith(name))
  expect(list.length).toBe(2)
  const copy = list.find(a => a.id !== made.id)
  expect(page.url()).toContain(copy.id)
  for (const a of list) await page.request.delete(`/api/chats/agents/${a.id}`)
})

test('a starter agent of the server is saved and opened for editing', async ({ page }) => {
  const before = (await api(page.request, '/api/chats/agents')).agents
  for (const a of before) await page.request.delete(`/api/chats/agents/${a.id}`)
  const templates = (await api(page.request, '/api/chats/agents/templates')).templates
  expect(templates.length).toBeGreaterThan(0)
  await page.goto('/app/agents')
  const tpl = page.getByTestId('agent-template').first()
  await expect(tpl).toBeVisible()
  await expect(tpl).toContainText(templates[0].name)
  await tpl.getByRole('button').click()
  await page.waitForURL(/\/app\/agents\/[^/]+\/edit$/)
  const after = (await api(page.request, '/api/chats/agents')).agents
  expect(after.map(a => a.name)).toContain(templates[0].name)
  for (const a of after) await page.request.delete(`/api/chats/agents/${a.id}`)
})

test('an agent task is created, run with a parameter, and its job is listed with its result', async ({ page }) => {
  await offerMockModel(page)
  const name = uniq('task')
  await page.goto('/app/agents')
  await page.getByRole('link', { name: 'Tasks' }).click()
  await page.waitForURL(/\/app\/agent-tasks$/)
  await page.getByRole('button', { name: 'New task' }).click()
  const form = page.getByTestId('agent-task-form')
  await form.getByLabel('Name').fill(name)
  await form.getByLabel('Model').selectOption('mock-model')
  await form.getByLabel('Prompt').fill('Say hello to {{.who}}')
  await form.getByRole('button', { name: 'Save task' }).click()
  await expect(form).toHaveCount(0)
  const task = (await api(page.request, '/api/agent/tasks')).find(t => t.name === name)
  expect(task.model).toBe('mock-model')
  expect(task.prompt).toBe('Say hello to {{.who}}')

  const row = page.getByTestId('agent-task-row').filter({ hasText: name })
  await row.getByRole('button', { name: 'Run' }).click()
  await page.getByTestId('agent-task-run').getByLabel('who').fill('Ada')
  await page.getByRole('button', { name: 'Run now' }).click()
  await expect.poll(async () => (await api(page.request, `/api/agent/jobs?task_id=${task.id}`)).length).toBe(1)
  const job = (await api(page.request, `/api/agent/jobs?task_id=${task.id}`))[0]
  expect(job.parameters).toEqual({ who: 'Ada' })
  const jobRow = page.getByTestId('agent-job-row').first()
  await expect(jobRow).toBeVisible()
  await expect.poll(async () => (await api(page.request, `/api/agent/jobs/${job.id}`)).status, { timeout: 60_000 }).toMatch(/completed|failed/)
  await expect(jobRow).toHaveAttribute('data-status', /completed|failed/, { timeout: 10_000 })
  await jobRow.getByRole('button', { name: 'Details' }).click()
  await expect(page.getByTestId('agent-job-detail')).toContainText('who=Ada')
  await page.screenshot({ path: 'test-results/wiring-tasks.png', fullPage: true })

  await jobRow.getByRole('button', { name: 'Run again' }).click()
  await expect.poll(async () => (await api(page.request, `/api/agent/jobs?task_id=${task.id}`)).length).toBe(2)

  await row.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect.poll(async () => (await api(page.request, '/api/agent/tasks')).some(t => t.id === task.id)).toBe(false)
})

test('the administrator sees every user\'s agents and triggers, switches a trigger off and deletes it', async ({ page }) => {
  const agent = await (await page.request.post('/api/chats/agents', { data: { name: uniq('adm'), model: 'mock-model' } })).json()
  const trig = await (await page.request.post('/api/chats/triggers', { data: { name: uniq('admtrig'), prompt: 'x', kind: 'schedule', schedule: 'daily 08:00', enabled: true, model: 'mock-model' } })).json()
  expect(trig.id).toBeTruthy()
  await page.goto('/app/settings')
  await page.getByRole('link', { name: 'Chats of all users' }).first().click()
  await page.waitForURL(/\/app\/chats-admin$/)
  await expect(page.getByTestId('admin-agent-row').filter({ hasText: agent.name })).toBeVisible()
  const row = page.getByTestId('admin-trigger-row').filter({ hasText: trig.name })
  await expect(row).toBeVisible()
  await page.screenshot({ path: 'test-results/wiring-admin.png', fullPage: true })
  await row.getByRole('button', { name: 'Switch off' }).click()
  await expect.poll(async () => (await api(page.request, '/api/chats/admin/triggers')).triggers.find(t => t.id === trig.id).enabled).toBe(false)
  await row.getByRole('button', { name: 'Runs' }).click()
  await row.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect.poll(async () => (await api(page.request, '/api/chats/admin/triggers')).triggers.some(t => t.id === trig.id)).toBe(false)
  await page.request.delete(`/api/chats/agents/${agent.id}`)
})
