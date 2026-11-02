import { test, expect } from './coverage-fixtures.js'

// Skills against the real test server: write one on the page, find it in the
// list, and delete it.

test('a skill written on the page shows in the list and can be deleted', async ({ page }) => {
  const name = `checklist-${Date.now()}`
  await page.goto('/app/skills')
  test.skip(await page.getByTestId('files-off').isVisible(), 'files are off on this server build')

  await page.goto('/app/skills/new')
  await page.getByLabel(/^Name/).fill(name)
  await page.getByLabel(/^Description/).fill('Go through the release checklist')
  await page.locator('.skilledit-sidebar-item', { hasText: 'Content' }).click()
  await page.getByLabel('Skill content (markdown)').fill('Tick every box.')
  await page.getByRole('button', { name: 'Create skill' }).click()
  await expect(page).toHaveURL(/\/app\/skills$/)

  await page.getByTestId(`skill-row-${name}`).click()
  await expect(page.getByTestId('skill-title')).toHaveText(name)
  await page.getByRole('tab', { name: 'Instructions' }).click()
  await expect(page.getByTestId('skill-content')).toContainText('Tick every box.')

  await page.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByTestId(`skill-row-${name}`)).toHaveCount(0)
})

// No route interception: these checks cover the application's actual skills,
// saved-agent and preference APIs on a server without an authoring provider.
test('document names keep agent selections and normalize OFF without a core provider', async ({ request }) => {
  const names = ['docx', 'pptx', 'pdf', 'xlsx']
  const list = await request.get('/api/chats/skills')
  expect(list.ok()).toBeTruthy()
  const { skills } = await list.json()
  expect(skills.filter(skill => names.includes(skill.name))).toEqual([])

  const saved = await request.post('/api/chats/agents', {
    data: { name: `document-selection-${Date.now()}`, model: 'mock-model', skills: names, addons: [] },
  })
  expect(saved.status()).toBe(201)
  const agent = await saved.json()
  let chat
  try {
    expect(agent.skills).toEqual(names)
    expect(agent.addons).toEqual([])
    const created = await request.post('/api/chats', { data: { model: 'mock-model', agent_id: agent.id } })
    expect(created.status()).toBe(201)
    chat = await created.json()
    const off = await request.put(`/api/chats/${chat.id}/skills`, { data: { off: ['builtin:docx'] } })
    expect(off.ok()).toBeTruthy()
    expect((await off.json()).off).toEqual(['addon:docx'])
    const reload = await request.get(`/api/chats/${chat.id}`)
    expect(reload.ok()).toBeTruthy()
    expect((await reload.json()).agent_id).toBe(agent.id)
    const picker = await request.get(`/api/chats/skills?chat=${chat.id}`)
    expect(picker.ok()).toBeTruthy()
    expect((await picker.json()).skills.filter(skill => names.includes(skill.name))).toEqual([])
    const enabled = await request.put(`/api/chats/${chat.id}/skills`, { data: { off: [] } })
    expect(enabled.ok()).toBeTruthy()
    expect((await enabled.json()).off).toEqual([])
    const reread = await request.get(`/api/chats/agents/${agent.id}`)
    expect((await reread.json()).skills).toEqual(names)
  } finally {
    if (chat) expect((await request.delete(`/api/chats/${chat.id}`)).ok()).toBeTruthy()
    expect((await request.delete(`/api/chats/agents/${agent.id}`)).ok()).toBeTruthy()
  }
})
