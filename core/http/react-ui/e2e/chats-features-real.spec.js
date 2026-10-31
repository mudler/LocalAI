import { test, expect } from './coverage-fixtures.js'
import { readFileSync } from 'node:fs'
import { startChat, transcript, composer, offerMockModel, openNewChat, b64 } from './journey-fixtures.js'

// The chat features of the API, driven from the chat page against the real
// test server and the mock model. After each step the spec reads the API, so
// what the page shows is what the server keeps.

// The mock model answers with tool calls until the loop's step limit, so a turn
// takes a few seconds.
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const api = (request, path) => request.get(path).then(r => r.json())
const lastAnswer = (page) => transcript(page).locator('[data-testid="chat-message"][data-role="assistant"]').last()
const settled = async (page, id) => {
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}`)).running, { timeout: 60_000 }).toBe(false)
  await expect(page.getByTestId('chat-streaming')).toHaveCount(0, { timeout: 15_000 })
}

test('the settings sheet saves the system prompt and sampling of the chat, and they are kept', async ({ page }) => {
  const id = await startChat(page, 'settings please')
  await expect(lastAnswer(page)).toBeVisible({ timeout: 20_000 })
  await settled(page, id)
  await page.getByRole('button', { name: 'Chat settings' }).click()
  const sheet = page.getByTestId('chat-settings')
  await expect(sheet).toBeVisible()
  await sheet.getByLabel('System prompt').fill('Answer in one line.')
  await sheet.getByLabel('Temperature').fill('3')
  await expect(sheet.getByText('Use a number from 0 to 2.')).toBeVisible()
  await expect(sheet.getByTestId('chat-settings-save')).toBeDisabled()
  await sheet.getByLabel('Temperature').fill('0.3')
  await sheet.getByRole('button', { name: 'Low' }).click()
  await sheet.getByTestId('chat-settings-save').click()
  await expect(sheet.getByRole('status')).toContainText('Saved')
  await page.screenshot({ path: 'test-results/wiring-settings.png' })
  const conv = await api(page.request, `/api/chats/${id}`)
  expect(conv.settings).toEqual({ system_prompt: 'Answer in one line.', temperature: 0.3, reasoning_effort: 'low' })
  // A rename goes through the same call.
  const r = await page.request.patch(`/api/chats/${id}`, { data: { title: 'Kept settings' } })
  expect(r.ok()).toBe(true)
  expect((await api(page.request, `/api/chats/${id}`)).settings.temperature).toBe(0.3)
})

test('the server refuses a title change while a turn runs', async ({ page }) => {
  const id = await startChat(page, 'MOCK_LONG_STREAM MOCK_LONG_STREAM_SHORT busy')
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}`)).running).toBe(true)
  const r = await page.request.patch(`/api/chats/${id}`, { data: { title: 'nope' } })
  expect(r.status()).toBe(409)
  await settled(page, id)
})

test('regenerate answers the last message again, edit replaces a message and runs again, branch opens a copy', async ({ page }) => {
  const id = await startChat(page, 'first question')
  await expect(lastAnswer(page)).toBeVisible({ timeout: 20_000 })
  await settled(page, id)
  const before = (await api(page.request, `/api/chats/${id}/messages`)).messages
  const answers = (list) => list.filter(m => m.role === 'assistant').map(m => m.seq)
  await lastAnswer(page).hover()
  await lastAnswer(page).getByRole('button', { name: 'Regenerate' }).click()
  await expect.poll(async () => answers((await api(page.request, `/api/chats/${id}/messages`)).messages).join(','), { timeout: 20_000 })
    .not.toBe(answers(before).join(','))
  await settled(page, id)
  const after = (await api(page.request, `/api/chats/${id}/messages`)).messages
  expect(after.filter(m => m.role === 'user')).toHaveLength(1)

  const mine = transcript(page).locator('[data-testid="chat-message"][data-role="user"]').first()
  await mine.hover()
  await mine.getByRole('button', { name: 'Edit message' }).click()
  await page.getByLabel('Edit message').fill('edited question')
  await page.getByRole('button', { name: 'Send edit' }).click()
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}/messages`)).messages.filter(m => m.role === 'user').map(m => m.text))
    .toEqual(['edited question'])
  await settled(page, id)
  await expect(transcript(page)).toContainText('edited question')

  const answer = lastAnswer(page)
  await answer.hover()
  await answer.getByRole('button', { name: 'Branch from here' }).click()
  await page.waitForURL(url => !url.pathname.endsWith(id) && /\/app\/chats\/[0-9a-f]+$/.test(url.pathname))
  const fork = page.url().split('/').pop()
  const copy = (await api(page.request, `/api/chats/${fork}/messages`)).messages
  expect(copy.filter(m => m.role === 'user').map(m => m.text)).toEqual(['edited question'])
  await expect(transcript(page)).toContainText('edited question')
})

test('the chats menu finds a chat by a word of its messages', async ({ page }) => {
  const word = `zebra${Date.now().toString(36)}`
  const id = await startChat(page, `remember the ${word} please`)
  await settled(page, id)
  await page.request.patch(`/api/chats/${id}`, { data: { title: 'Unrelated title' } })
  const hits = await api(page.request, `/api/chats/search?q=${word}`)
  expect(hits.hits.map(h => h.chat.id)).toContain(id)
  // The menu is in the header of a transcript; /app/chats as an address opens Home.
  await page.goto(`/app/chats/${id}`)
  await page.getByTestId('chats-trigger').click()
  await page.getByRole('combobox', { name: /search/i }).fill(word)
  const row = page.getByTestId('chats-row').filter({ hasText: 'Unrelated title' })
  await expect(row).toBeVisible()
  await expect(row).toContainText(word)
  await page.screenshot({ path: 'test-results/wiring-search.png' })
})

test('a chat exported as JSON imports as a new chat with the same messages', async ({ page }) => {
  const id = await startChat(page, 'export me')
  await settled(page, id)
  await page.getByRole('button', { name: 'More' }).click()
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('menuitem', { name: 'Export as JSON' }).click(),
  ])
  const file = await download.path()
  const exported = JSON.parse(readFileSync(file, 'utf8'))
  expect(exported.messages.map(m => m.text)).toContain('export me')
  await page.getByTestId('chat-import-input').setInputFiles(file)
  await page.waitForURL(url => !url.pathname.endsWith(id) && /\/app\/chats\/[0-9a-f]+$/.test(url.pathname))
  const copy = page.url().split('/').pop()
  const lines = (await api(page.request, `/api/chats/${copy}/messages`)).messages
  expect(lines.filter(m => m.role === 'user').map(m => m.text)).toEqual(['export me'])
})

test('the chats the old page left in this browser are offered once and imported', async ({ page }) => {
  const title = `old-${Date.now().toString(36)}`
  const created = Date.now() - 3600_000
  await page.addInitScript(([t, c]) => {
    if (sessionStorage.getItem('seeded')) return
    sessionStorage.setItem('seeded', '1')
    localStorage.setItem('localai_chats_data', JSON.stringify({ chats: [{ id: 'x1', name: t, model: 'mock-model', createdAt: c, updatedAt: c, history: [{ role: 'user', content: 'hi from before' }, { role: 'assistant', content: 'hello' }] }] }))
  }, [title, created])
  await offerMockModel(page)
  await openNewChat(page)
  const note = page.getByTestId('browser-import')
  await expect(note).toContainText('1 chat from the old chat page')
  await page.screenshot({ path: 'test-results/wiring-browser-import.png' })
  await note.getByRole('button', { name: 'Import them' }).click()
  await expect(note).toHaveCount(0)
  await expect.poll(async () => (await api(page.request, '/api/chats?limit=200')).conversations.some(c => c.title === title)).toBe(true)
  expect(await page.evaluate(() => localStorage.getItem('localai_chats_data'))).toBeNull()
})

test('the context meter shows what the last request used', async ({ page }) => {
  const id = await startChat(page, 'measure me')
  await settled(page, id)
  await composer(page).fill('again')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByTestId('chat-context-meter')).toBeVisible({ timeout: 20_000 })
  await expect(page.getByTestId('chat-context-meter')).toContainText('tokens')
})

test('a file attached while a turn runs waits with its message', async ({ page }) => {
  const id = await startChat(page, 'MOCK_LONG_STREAM MOCK_LONG_STREAM_SHORT working')
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}`)).running).toBe(true)
  await page.getByTestId('chat-file-input').setInputFiles({ name: 'later.txt', mimeType: 'text/plain', buffer: Buffer.from('later') })
  await composer(page).fill('with a file')
  await page.getByRole('button', { name: /send|queue/i }).first().click()
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}`)).pending?.length || 0).toBeGreaterThan(0)
  await settled(page, id)
  await expect.poll(async () => (await api(page.request, `/api/chats/${id}/messages`)).messages.some(m => m.role === 'user' && /with a file/.test(m.text)), { timeout: 20_000 }).toBe(true)
})

test('a trigger the model proposes shows a card that creates it', async ({ page }) => {
  const name = `digest-${Date.now().toString(36)}`
  const args = { name, prompt: 'Sum up the day', when: 'daily 08:00' }
  const id = await startChat(page, `ADDON_TOOL_CALL:propose_trigger:${b64(args)}`)
  const card = page.getByTestId('trigger-proposal').first()
  await expect(card).toBeVisible({ timeout: 20_000 })
  await settled(page, id)
  const props = await api(page.request, '/api/chats/triggers/proposals')
  const proposal = props.triggers.find(t => t.name === name)
  expect(proposal).toBeTruthy()
  await expect(card).toHaveAttribute('data-state', 'proposed')
  await card.getByRole('button', { name: 'Edit' }).click()
  await card.getByLabel(/^When/).fill('weekdays 09:30')
  await card.getByRole('button', { name: 'Save' }).click()
  await expect.poll(async () => (await api(page.request, '/api/chats/triggers/proposals')).triggers.find(t => t.id === proposal.id)?.schedule).toBe('weekdays 09:30')
  await page.screenshot({ path: 'test-results/wiring-proposal.png' })
  await card.getByRole('button', { name: 'Create trigger' }).click()
  // The card turns into a receipt in place, and the server keeps the answer.
  await expect(card).toHaveAttribute('data-state', 'accepted')
  await expect(card).toContainText('Scheduled:')
  await expect(card.getByTestId('receipt-title')).toHaveText(name)
  const trig = (await api(page.request, '/api/chats/triggers')).triggers.find(t => t.id === proposal.id)
  expect(trig.enabled).toBe(true)
  expect((await api(page.request, `/api/chats/triggers/proposals/${proposal.id}`)).state).toBe('accepted')
  await page.reload()
  await expect(page.getByTestId('trigger-proposal').first()).toHaveAttribute('data-state', 'accepted', { timeout: 20_000 })
  await page.screenshot({ path: 'test-results/wiring-proposal-receipt.png' })

  // Undo removes the trigger and keeps the receipt; it can be scheduled again.
  await page.getByTestId('trigger-proposal').first().getByRole('button', { name: 'Undo' }).click()
  await expect(page.getByTestId('trigger-proposal').first()).toHaveAttribute('data-state', 'undone')
  expect((await api(page.request, '/api/chats/triggers')).triggers.find(t => t.id === proposal.id)).toBeUndefined()
  await page.reload()
  await expect(page.getByTestId('trigger-proposal').first()).toHaveAttribute('data-state', 'undone', { timeout: 20_000 })
  await page.getByTestId('trigger-proposal').first().getByRole('button', { name: 'Schedule it again' }).click()
  await expect(page.getByTestId('trigger-proposal').first()).toHaveAttribute('data-state', 'proposed')

  // Once the trigger has run or been edited, the server refuses the undo.
  await page.getByTestId('trigger-proposal').first().getByRole('button', { name: 'Create trigger' }).click()
  await expect(page.getByTestId('trigger-proposal').first()).toHaveAttribute('data-state', 'accepted')
  const cur = (await api(page.request, '/api/chats/triggers')).triggers.find(t => t.id === proposal.id)
  const edited = await page.request.put(`/api/chats/triggers/${proposal.id}`, { data: { ...cur, name: `${name} edited`, enabled: true } })
  expect(edited.ok()).toBe(true)
  const refused = await page.request.post(`/api/chats/triggers/proposals/${proposal.id}/undo`)
  expect(refused.status()).toBe(409)
  expect((await refused.json()).error.message).toContain('You edited it since')
  await page.reload()
  await expect(page.getByTestId('trigger-proposal').first().getByTestId('receipt-blocked')).toBeVisible({ timeout: 20_000 })
  await page.request.delete(`/api/chats/triggers/${proposal.id}`)
})

test('a background sub-agent is listed in the chat and can be stopped', async ({ page }) => {
  const id = await startChat(page, `ADDON_TOOL_CALL:spawn_agent:${b64({ task: 'MOCK_LONG_STREAM keep going', wait: false })}`)
  const chip = page.getByTestId('chat-subagents-button')
  await expect(chip).toBeVisible({ timeout: 20_000 })
  const list = await api(page.request, `/api/chats/${id}/agents`)
  expect(list.sub_agents.length).toBe(1)
  await chip.click()
  const row = page.getByTestId('chat-subagent').first()
  await expect(row).toBeVisible()
  await page.screenshot({ path: 'test-results/wiring-subagents.png' })
  if (list.sub_agents[0].running) {
    await row.getByRole('button', { name: 'Stop' }).click()
    await expect.poll(async () => (await api(page.request, `/api/chats/${id}/agents`)).sub_agents[0].running, { timeout: 20_000 }).toBe(false)
  }
  await row.getByRole('button', { name: 'Show' }).click()
  await expect(row.getByTestId('chat-subagent-log')).toContainText('keep going')
})
