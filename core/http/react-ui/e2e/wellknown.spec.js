import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { offerMockModel, composer, openNewChat } from './journey-fixtures.js'

// The well-known files of the home (persona.json, user.json, shortcuts.json,
// home.json, ui.json) and their editors: the Account tabs, the shortcuts row of
// the chat and the Home widgets. Against the real test server: every step is
// checked against the file the server keeps.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

// Other spec files write the same Home files: hold them while this file runs.
holdHomeFiles(test)

const FILES = ['/LocalAI/persona.json', '/LocalAI/user.json', '/LocalAI/shortcuts.json', '/LocalAI/home.json', '/LocalAI/ui.json', '/data/tasks.json', '/data/tokens.json', '/data/broken.json']

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
const read = (request, name) => request.get(`/api/chats/wellknown/${name}`).then(r => r.json())
const write = (request, name, values) => request.put(`/api/chats/wellknown/${name}`, { data: { values, replace: true } })
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})

test.beforeEach(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

test('about you: the assistant and the user cards save their files, fill in from the browser and keep unknown keys', async ({ page, request }) => {
  await upload(request, '/LocalAI/persona.json', { name: 'Ada', x_mood: 'calm' })
  await page.goto('/app/account?tab=about')
  const persona = page.getByTestId('wk-card-persona')
  await expect(persona.getByTestId('wk-field-persona-name')).toHaveValue('Ada')
  // The card speaks of the setting, not of the file behind it.
  await expect(persona).not.toContainText('LocalAI/persona.json')

  await persona.getByTestId('wk-field-persona-system_prompt').fill('Keep answers short.')
  await persona.getByTestId('wk-save-persona').click()
  await expect(persona.getByRole('status').filter({ hasText: 'Saved at' })).toBeVisible()
  const saved = await read(request, 'persona')
  expect(saved.data).toEqual({ name: 'Ada', system_prompt: 'Keep answers short.', x_mood: 'calm' })

  // The language and the time zone come from the browser until saved. A
  // user.json that holds only LocalAI's seen record (the pages and the server
  // write it on their own) is not saved yet either.
  const seen = { home_examples: { hidden: '2026-10-29' } }
  await upload(request, '/LocalAI/user.json', { seen })
  await page.reload()
  const user = page.getByTestId('wk-card-user')
  await expect(user.getByText('From your browser').first()).toBeVisible()
  await expect(user.getByText('Not saved yet.', { exact: false })).toBeVisible()
  await user.getByTestId('wk-field-user-name').fill('Marta')
  await user.getByTestId('wk-field-user-bio').fill('Product designer in Milan.')
  await user.getByTestId('wk-save-user').click()
  await expect.poll(async () => (await read(request, 'user')).data.name).toBe('Marta')
  const u = (await read(request, 'user')).data
  expect(u.bio).toBe('Product designer in Milan.')
  expect(u.timezone).toBeTruthy()
  expect(u.seen).toEqual(seen)
})

test('about you: the source view is validated, a broken file shows its last good version, and revert restores it', async ({ page, request }) => {
  await write(request, 'persona', { name: 'Ada' })
  await write(request, 'persona', { name: 'Bea' })
  await page.goto('/app/account?tab=about')
  const persona = page.getByTestId('wk-card-persona')
  await expect(persona.getByTestId('wk-field-persona-name')).toHaveValue('Bea')

  await persona.getByTestId('wk-source-persona').click()
  await persona.getByTestId('wk-source-text-persona').fill('{"name": 42}')
  await persona.getByTestId('wk-save-persona').click()
  const problem = persona.getByTestId('wk-problem-persona')
  await expect(problem).toContainText('Some values are not valid.')
  // The server's words stay folded under Details until asked for.
  await expect(problem.locator('details')).not.toHaveAttribute('open', '')
  await problem.getByText('Details').click()
  await expect(problem.locator('details li')).toContainText('name')
  expect((await read(request, 'persona')).data.name).toBe('Bea')

  // The assistant writes a file that breaks the schema.
  await upload(request, '/LocalAI/persona.json', '{"name": ["broken"]}')
  await page.reload()
  await expect(persona.getByTestId('wk-invalid-persona')).toContainText('last valid version')
  await expect(persona.getByTestId('wk-field-persona-name')).toHaveValue('Bea')
  await persona.getByTestId('wk-revert-persona.json').click()
  await expect(persona.getByTestId('wk-invalid-persona')).toHaveCount(0)
  const back = await read(request, 'persona')
  expect(back.valid).toBe(true)
  expect(back.data.name).toBe('Bea')
})

test('about you: a change by someone else while editing is a conflict that shows the newer values', async ({ page, request }) => {
  await write(request, 'persona', { name: 'Ada' })
  await page.goto('/app/account?tab=about')
  const persona = page.getByTestId('wk-card-persona')
  await expect(persona.getByTestId('wk-field-persona-name')).toHaveValue('Ada')
  await persona.getByTestId('wk-field-persona-name').fill('Mine')
  // The change feed brings the other write to this tab while the form holds
  // the edit: the save must still start from the version the edit started on.
  const reread = page.waitForResponse(r => /\/api\/chats\/wellknown\/persona$/.test(r.url()) && r.request().method() === 'GET')
  await write(request, 'persona', { name: 'Theirs' })
  await reread
  await expect(persona.getByTestId('wk-field-persona-name')).toHaveValue('Mine')
  await persona.getByTestId('wk-save-persona').click()
  const conflict = persona.getByTestId('wk-conflict-persona')
  await expect(conflict).toContainText('Theirs')
  await conflict.getByRole('button', { name: 'Keep my changes' }).click()
  await expect.poll(async () => (await read(request, 'persona')).data.name).toBe('Mine')
})

test('appearance: the theme applies at once and is kept in ui.json', async ({ page, request }) => {
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  await card.getByTestId('wk-field-ui-theme').selectOption('light')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await card.getByTestId('wk-field-ui-density').selectOption('compact')
  await expect(page.locator('html')).toHaveAttribute('data-density', 'compact')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data).toEqual({ theme: 'light', density: 'compact' })
  await write(request, 'ui', { theme: 'dark' })
  await page.goto('/app/chats')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.locator('html')).not.toHaveAttribute('data-density', 'compact')
})

test('appearance: a pin made elsewhere while the form holds an edit is kept by the save', async ({ page, request }) => {
  await write(request, 'ui', { theme: 'dark' })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  await expect(card.getByTestId('wk-field-ui-theme')).toHaveValue('dark')
  await card.getByTestId('wk-field-ui-density').selectOption('compact')
  // Another tab pins a page: only pinnedPages changes.
  const reread = page.waitForResponse(r => /\/api\/chats\/wellknown\/ui$/.test(r.url()) && r.request().method() === 'GET')
  expect((await request.put('/api/chats/wellknown/ui', { data: { values: { pinnedPages: ['tasks'] } } })).ok()).toBeTruthy()
  await reread
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data).toEqual({ theme: 'dark', density: 'compact', pinnedPages: ['tasks'] })
  await expect(card.getByTestId('wk-conflict-ui')).toHaveCount(0)
})

test('shortcuts: the starters are the default shortcuts, a click fills the composer, and the editor writes shortcuts.json', async ({ page, request }) => {
  await offerMockModel(page)
  // The shortcuts row is over the message box of a chat; /new on Home opens an empty one.
  await openNewChat(page)
  const row = page.getByTestId('chat-shortcuts')
  await expect(row.getByTestId('chat-shortcut')).toHaveCount(4)
  await expect(row).toContainText('Default shortcuts')
  await row.getByTestId('chat-shortcut').first().click()
  await expect(composer(page)).toHaveValue('Explain how this works')

  await row.getByTestId('chat-shortcuts-edit').click()
  const editor = page.getByTestId('wk-shortcuts-editor')
  await expect(editor.getByTestId('wk-shortcut-row')).toHaveCount(4)
  await editor.getByRole('button', { name: 'Remove' }).nth(3).click()
  await editor.getByTestId('wk-shortcut-add').click()
  const last = editor.getByTestId('wk-shortcut-row').last()
  await last.getByLabel('Label').fill('Plan my day')
  await last.getByLabel('Prompt').fill('Look at my open tasks and plan today.')
  await editor.getByRole('button', { name: 'Move up' }).last().click()
  await editor.getByTestId('wk-shortcut-save').click()
  await expect(editor).toHaveCount(0)

  const saved = (await read(request, 'shortcuts')).data.shortcuts
  expect(saved.map(s => s.label)).toEqual(['Explain how this works', 'Help me write code', 'Plan my day', 'Summarize a document'])
  // Own shortcuts carry no note; only the defaults are labelled as such.
  await expect(row).not.toContainText('Default shortcuts')
  await expect(row).not.toContainText('.json')
  await expect(row.getByTestId('chat-shortcut').nth(2)).toHaveText('Plan my day')

  // A file the assistant writes shows at the next read.
  await write(request, 'shortcuts', { shortcuts: [{ label: 'Summarise my inbox', prompt: 'Summarise my inbox', icon: 'mail' }] })
  await openNewChat(page)
  await expect(row.getByTestId('chat-shortcut')).toHaveText(['Summarise my inbox'])

  await row.getByTestId('chat-shortcuts-assistant').click()
  await expect(composer(page)).toHaveValue('Change my shortcuts: ')
})

test('home widgets: list, metric and chats widgets read their data, a broken file is marked stale, and the layout editor writes home.json', async ({ page, request }) => {
  await offerMockModel(page)
  await upload(request, '/data/tasks.json', { title: 'Open tasks', items: [{ label: 'Send the print invoice', meta: 'Wed' }, { label: 'Call the dentist', meta: 'Fri', done: true }] })
  await upload(request, '/data/tokens.json', { value: 184000, note: '12% less than last week', series: [3, 4, 3, 5, 4, 6, 5] })
  await upload(request, '/data/broken.json', { items: [{ label: 'Kept row' }] })
  await upload(request, '/data/broken.json', '{"items": "nope"}')
  await write(request, 'home', {
    widgets: [
      { id: 'tasks', type: 'list', title: 'Open tasks', source: 'data/tasks.json' },
      { id: 'tokens', type: 'metric', title: 'Tokens this week', source: 'data/tokens.json' },
      { id: 'old', type: 'list', title: 'Broken', source: 'data/broken.json' },
      { id: 'missing', type: 'list', title: 'Missing', source: 'data/none.json' },
    ],
  })
  await page.goto('/app')
  await expect(page.getByTestId('home-widget-tasks')).toContainText('Send the print invoice')
  await expect(page.getByTestId('home-widget-tasks')).toContainText('1 left')
  await expect(page.getByTestId('home-widget-tokens')).toContainText('184k')
  await expect(page.getByTestId('home-widget-old')).toHaveAttribute('data-state', 'stale')
  await expect(page.getByTestId('home-widget-old')).toContainText('Kept row')
  await expect(page.getByTestId('home-widget-missing')).toHaveAttribute('data-state', 'empty')

  await page.getByTestId('home-edit-layout').click()
  const editor = page.getByTestId('home-layout-editor')
  await expect(editor.getByTestId('home-layout-row')).toHaveCount(4)
  await editor.getByRole('button', { name: 'Remove' }).nth(3).click()
  await editor.getByRole('button', { name: 'Remove' }).nth(2).click()
  await editor.getByTestId('home-layout-add').click()
  const added = editor.getByTestId('home-layout-row').last()
  await added.getByLabel('Type').selectOption('chats')
  await added.getByLabel('Title').fill('Last chats')
  await editor.getByTestId('home-layout-save').click()
  await expect(editor).toHaveCount(0)
  const home = (await read(request, 'home')).data.widgets
  expect(home.map(w => w.type)).toEqual(['list', 'metric', 'chats'])
  await expect(page.getByTestId('home-widget-last-chats')).toBeVisible()

  await page.getByTestId('home-ask-layout').click()
  await page.waitForURL(/\/app\/chats/)
  await expect(composer(page)).toHaveValue('Change my Home page: ')
})

test('the editors fit a phone screen without a sideways scroll', async ({ page, request }) => {
  await offerMockModel(page)
  await write(request, 'home', { widgets: [{ id: 'tasks', type: 'list', title: 'Open tasks with a very long title that has to be cut', source: 'data/tasks.json' }] })
  await upload(request, '/data/tasks.json', { items: [{ label: 'A task with a long, long label that does not fit a narrow card at all', meta: 'Wed' }] })
  await page.setViewportSize({ width: 390, height: 844 })
  for (const [path, ready] of [['/app/account?tab=about', 'wk-card-user'], ['/app/account?tab=appearance', 'wk-card-ui'], ['/app', 'home-widget-tasks'], ['/app/chats', 'chat-shortcuts']]) {
    // /app/chats as an address opens Home; the empty chat is /new on Home.
    if (path === '/app/chats') await openNewChat(page)
    else await page.goto(path)
    await expect(page.getByTestId(ready)).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, path).toBeLessThanOrEqual(1)
  }
})
