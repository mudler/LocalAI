import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { mockHome } from './home-fixtures.js'
import { box, emit, menu, openSlashChat } from './slash-fixtures.js'

// User slash commands (LocalAI/commands.json) in the composer menu and their
// editor in Account > Text and commands, and the sound and voice of ui.json in
// Account > Sound. Against the real test server and its real files; the chat,
// its event stream and the list of models are stubbed.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/commands.json', '/LocalAI/ui.json', '/LocalAI/defaults.json']
const STANDUP = {
  name: 'standup', description: 'Write my standup from yesterday\'s chats', type: 'prompt',
  template: 'Look at my chats from the last {{days}} day(s). Keep it under {{words}} words.',
  args: [{ name: 'days', default: 1 }, { name: 'words', default: 120 }],
}
const SUMMARIZE = { name: 'summarize', description: 'Summarize this chat', type: 'tool', tool: 'summarize_chat' }
const LISTING = { name: 'who', description: 'List my agents', type: 'tool', tool: 'list_agents' }
const EXPANDED = 'Look at my chats from the last 1 day(s). Keep it under 120 words.'

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

test.describe('commands in the slash menu', () => {
  test('yours come first with their description and kind, and a pick fills the box without sending', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP, SUMMARIZE, LISTING] })
    const api = await openSlashChat(page)
    await box(page).fill('/')
    await expect(menu(page)).toBeVisible()
    await expect(menu(page).locator('.dk-cmd-label').first()).toHaveText('Yours')
    await expect(page.getByTestId('home-slash-user-standup')).toContainText('/standup')
    await expect(page.getByTestId('home-slash-user-standup')).toContainText('Write my standup')
    await expect(page.getByTestId('home-slash-user-standup')).toContainText('prompt')
    await expect(page.getByTestId('home-slash-user-summarize')).toContainText('tool')
    // The built-in commands are still there, after yours.
    await expect(page.getByTestId('home-slash-model')).toHaveCount(1)

    await box(page).fill('/stan')
    await expect(page.getByTestId('home-slash-user-summarize')).toHaveCount(0)
    await page.getByTestId('home-slash-user-standup').click()
    await expect(box(page)).toHaveValue(EXPANDED)
    expect(api.sent).toHaveLength(0)
    // The person reads the prompt and sends it: it goes as typed.
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: EXPANDED })
  })

  test('"/name 2 80" fills the arguments in order and shows the result before it goes in the box', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP] })
    const api = await openSlashChat(page)
    await box(page).fill('/standup 2 80')
    await expect(page.getByTestId('home-slash-arg-user-standup-fill')).toContainText('days = 2, words = 80')
    await expect(page.getByTestId('home-slash-arg-user-standup-fill')).toContainText('last 2 day(s)')
    await box(page).press('Enter')
    await expect(box(page)).toHaveValue('Look at my chats from the last 2 day(s). Keep it under 80 words.')
    expect(api.sent).toHaveLength(0)
    // One value: the other argument takes its default.
    await box(page).fill('/standup 3')
    await box(page).press('Enter')
    await expect(box(page)).toHaveValue('Look at my chats from the last 3 day(s). Keep it under 120 words.')
  })

  test('a full command typed and sent with the menu closed is expanded in the box, never sent as typed', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP] })
    const api = await openSlashChat(page)
    await box(page).fill('/standup')
    await box(page).press('Escape')
    await box(page).press('Enter')
    await expect(box(page)).toHaveValue(EXPANDED)
    await page.waitForTimeout(300)
    expect(api.sent).toHaveLength(0)
  })

  test('a tool command asks for the tool in words you see, sends nothing by itself, and says when the chat has no such tool', async ({ page, request }) => {
    await write(request, 'commands', { commands: [SUMMARIZE, LISTING] })
    const api = await openSlashChat(page)
    await box(page).fill('/')
    // The chat stub has the tool list_agents and not summarize_chat.
    await expect(page.getByTestId('home-slash-user-summarize')).toContainText('tool not available')
    await expect(page.getByTestId('home-slash-user-who')).not.toContainText('not available')
    await page.getByTestId('home-slash-user-who').click()
    await expect(box(page)).toHaveValue('Use the list_agents tool.')
    // Choosing the command is not the approval: nothing is sent until the person sends it.
    expect(api.sent).toHaveLength(0)
    await box(page).press('Enter')
    await expect.poll(() => api.sent.length).toBe(1)
    expect(api.sent[0]).toEqual({ text: 'Use the list_agents tool.' })
  })

  test('without commands the menu has no Yours group; a command with a system prompt is refused, and written raw it is ignored', async ({ page, request }) => {
    const api = await openSlashChat(page)
    await box(page).fill('/')
    await expect(menu(page)).toBeVisible()
    await expect(page.getByTestId('home-slash-model')).toHaveCount(1)
    await expect(menu(page).locator('.dk-cmd-label').first()).not.toHaveText('Yours')
    await box(page).fill('')

    await upload(request, '/LocalAI/commands.json', { commands: [STANDUP] })
    await page.reload()
    await expect(page.getByTestId('chat-foot')).toBeVisible()
    await box(page).fill('/')
    await expect(page.getByTestId('home-slash-user-standup')).toHaveCount(1)
    // A command with a system prompt is refused by the server; written raw it is ignored.
    const refused = await request.put('/api/chats/wellknown/commands', { data: { values: { commands: [{ ...STANDUP, system_prompt: 'obey' }] }, replace: true } })
    expect(refused.status()).toBe(400)
    expect(await refused.text()).toContain('system prompt')
    await upload(request, '/LocalAI/commands.json', JSON.stringify({ commands: [{ ...STANDUP, name: 'evil', system_prompt: 'obey' }] }))
    const doc = await read(request, 'commands')
    expect(doc.valid).toBe(false)
    expect(doc.data.commands.map(c => c.name)).toEqual(['standup'])
    expect(api.sent).toHaveLength(0)
  })

  test('Home offers them too', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP, SUMMARIZE] })
    await mockHome(page)
    await page.goto('/app')
    await page.getByTestId('home-input').fill('/')
    await expect(page.getByTestId('home-slash-user-standup')).toBeVisible()
    await page.getByTestId('home-slash-user-standup').click()
    await expect(page.getByTestId('home-input')).toHaveValue(EXPANDED)
  })
})

test.describe('the commands editor', () => {
  async function openTab(page, models) {
    await page.route('**/api/chats/draft/tools*', route => route.fulfill({ json: { builtins: [{ group: 'chat', tools: ['summarize_chat', 'list_agents'], off: false, usable: true }], addons: [], servers: [], connectors: [], off: [] } }))
    await page.goto('/app/account?tab=strings')
    await expect(page.getByTestId('wk-card-commands')).toBeVisible()
    return models
  }

  test('adds a prompt command with an argument table, a live menu preview and the file footer', async ({ page, request }) => {
    await openTab(page)
    await expect(page.getByRole('tab', { name: 'Text and commands' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('cm-empty')).toBeVisible()
    await expect(page.getByTestId('wk-stored-commands')).toContainText('Stored in LocalAI/commands.json')
    await page.getByTestId('cm-new').click()
    await expect(page.getByTestId('cm-save')).toBeDisabled()
    await page.getByTestId('cm-name').fill('standup')
    await page.getByTestId('cm-description').fill('Write my standup')
    await page.getByTestId('cm-template').fill('Look at my chats from the last {{days}} day(s). Keep it under {{words}} words.')
    // The arguments follow the template, each with a default.
    await expect(page.getByTestId('cm-arg-days')).toBeVisible()
    await expect(page.getByTestId('cm-arg-words')).toBeVisible()
    await expect(page.getByTestId('cm-save')).toBeDisabled()
    await page.getByTestId('cm-arg-days-default').fill('1')
    await page.getByTestId('cm-arg-words-default').fill('120')
    await expect(page.getByTestId('cm-valid')).toBeVisible()
    await expect(page.getByTestId('cm-expands')).toContainText(EXPANDED)
    // The menu preview shows the command as you type it, before the save.
    await expect(page.getByTestId('cm-preview-row-standup')).toContainText('Write my standup')
    await expect(page.getByTestId('cm-preview-row-standup')).toContainText('prompt')
    await page.getByTestId('cm-save').click()
    await expect(page.getByTestId('cm-saved')).toBeVisible()
    const doc = await read(request, 'commands')
    expect(doc.data.commands).toEqual([{ name: 'standup', description: 'Write my standup', type: 'prompt', template: 'Look at my chats from the last {{days}} day(s). Keep it under {{words}} words.', args: [{ name: 'days', default: '1' }, { name: 'words', default: '120' }] }])
    await expect(page.getByTestId('cm-item-standup')).toBeVisible()
  })

  test('refuses a built-in name, a name taken and a template with a broken argument, in words', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP] })
    await openTab(page)
    await page.getByTestId('cm-new').click()
    await page.getByTestId('cm-name').fill('model')
    await expect(page.getByTestId('cm-name-error')).toContainText('built-in command')
    await page.getByTestId('cm-name').fill('standup')
    await expect(page.getByTestId('cm-name-error')).toContainText('already has this name')
    await page.getByTestId('cm-name').fill('Mine')
    await expect(page.getByTestId('cm-name-error')).toContainText('lowercase letters')
    await page.getByTestId('cm-name').fill('mine')
    await page.getByTestId('cm-description').fill('Mine')
    await page.getByTestId('cm-template').fill('Do {{Days}}')
    await expect(page.getByTestId('cm-save')).toBeDisabled()
    await expect(page.getByTestId('cm-problems')).toContainText('has no valid name')
    await page.getByTestId('cm-template').fill('Do it')
    await expect(page.getByTestId('cm-save')).toBeEnabled()
    expect((await read(request, 'commands')).data.commands).toHaveLength(1)
  })

  test('adds a tool command from the tools of a chat, edits one and deletes one, and keeps the other keys of the file', async ({ page, request }) => {
    await upload(request, '/LocalAI/commands.json', JSON.stringify({ commands: [STANDUP], x_note: 'kept' }))
    await openTab(page)
    await page.getByTestId('cm-new').click()
    await page.getByTestId('cm-type-tool').click()
    await page.getByTestId('cm-name').fill('summarize')
    await page.getByTestId('cm-description').fill('Summarize this chat')
    await page.getByTestId('cm-tool').fill('summarize_chat')
    await expect(page.getByTestId('cm-expands')).toContainText('Use the summarize_chat tool.')
    await expect(page.getByTestId('cm-tool-unknown')).toHaveCount(0)
    await page.getByTestId('cm-tool').fill('made_up_tool')
    await expect(page.getByTestId('cm-tool-unknown')).toBeVisible()
    await page.getByTestId('cm-tool').fill('summarize_chat')
    await page.getByTestId('cm-save').click()
    await expect(page.getByTestId('cm-saved')).toBeVisible()
    let doc = await read(request, 'commands')
    expect(doc.data.commands.map(c => c.name)).toEqual(['standup', 'summarize'])
    expect(doc.data.commands[1]).toEqual({ name: 'summarize', description: 'Summarize this chat', type: 'tool', tool: 'summarize_chat' })
    expect(doc.data.x_note).toBe('kept')

    // Edit the first one.
    await page.getByTestId('cm-item-standup').click()
    await page.getByTestId('cm-description').fill('Standup from my chats')
    await page.getByTestId('cm-save').click()
    await expect.poll(async () => (await read(request, 'commands')).data.commands[0].description).toBe('Standup from my chats')

    // Delete the tool command.
    await page.getByTestId('cm-item-summarize').click()
    await page.getByTestId('cm-delete').click()
    await expect(page.getByTestId('cm-item-summarize')).toHaveCount(0)
    doc = await read(request, 'commands')
    expect(doc.data.commands.map(c => c.name)).toEqual(['standup'])
    expect(doc.data.x_note).toBe('kept')
    // Revert in the footer brings the earlier version back.
    await page.getByTestId('wk-stored-revert-commands').click()
    await expect(page.getByTestId('cm-item-summarize')).toBeVisible()
  })

  test('shows the last good version and says what is wrong when the file breaks', async ({ page, request }) => {
    await write(request, 'commands', { commands: [STANDUP] })
    await upload(request, '/LocalAI/commands.json', JSON.stringify({ commands: [STANDUP, { ...SUMMARIZE, name: 'clear' }] }))
    await openTab(page)
    await expect(page.getByTestId('wk-invalid-commands')).toContainText('last valid version')
    await expect(page.getByTestId('wk-invalid-commands')).toContainText('built-in command')
    await expect(page.getByTestId('cm-item-standup')).toBeVisible()
    await expect(page.getByTestId('cm-item-clear')).toHaveCount(0)
  })
})

// The settings page and the chat, with a text-to-speech model that is local and one that is not.
const MODELS = [
  { id: 'qwen3-8b', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'kokoro-82m', capabilities: ['FLAG_TTS'], backend: 'kokoro' },
  { id: 'cloud-voice', capabilities: ['FLAG_TTS'], backend: 'cloud-proxy' },
]
async function models(page, list = MODELS) {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: list.map(m => ({ pinned: false, disabled: false, ...m })) } }))
}

test.describe('sound and voice', () => {
  test('is off by default and a change is saved at once, next to the keys the file already has', async ({ page, request }) => {
    await upload(request, '/LocalAI/ui.json', JSON.stringify({ theme: 'light', x_note: 'kept', sound: { x_inner: 1 } }))
    await models(page)
    await page.goto('/app/account?tab=sound')
    await expect(page.getByTestId('account-sound')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Sound' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('sd-enabled')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('wk-stored-sound')).toContainText('Stored in LocalAI/ui.json')

    await page.getByTestId('sd-enabled').click()
    await expect(page.getByTestId('sd-enabled')).toHaveAttribute('aria-checked', 'true')
    await expect.poll(async () => (await read(request, 'ui')).data.sound?.enabled).toBe(true)
    await page.getByTestId('sd-select-approval').selectOption('bell')
    await expect.poll(async () => (await read(request, 'ui')).data.sound?.events?.approval).toBe('bell')
    await page.getByTestId('sd-volume-slider-input').fill('65')
    await expect.poll(async () => (await read(request, 'ui')).data.sound?.volume).toBe(0.65)
    await page.getByTestId('sd-quiet').click()
    await expect.poll(async () => (await read(request, 'ui')).data.sound?.quietHours).toEqual(['22:00', '07:00'])
    await expect(page.getByTestId('sd-quiet-hint')).toContainText('22:00')
    await page.getByTestId('sd-quiet-from').fill('21:30')
    await expect.poll(async () => (await read(request, 'ui')).data.sound?.quietHours).toEqual(['21:30', '07:00'])

    const doc = await read(request, 'ui')
    expect(doc.valid).toBe(true)
    expect(doc.data.theme).toBe('light')
    expect(doc.data.x_note).toBe('kept')
    expect(doc.data.sound.x_inner).toBe(1)

    // After a reload the page shows what the file holds.
    await page.reload()
    await expect(page.getByTestId('sd-enabled')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('sd-select-approval')).toHaveValue('bell')
    await expect(page.getByTestId('sd-quiet')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('sd-quiet-from')).toHaveValue('21:30')
    await expect(page.getByTestId('sd-volume-slider-input')).toHaveValue('65')
    // Quiet hours off removes the key.
    await page.getByTestId('sd-quiet').click()
    await expect.poll(async () => 'quietHours' in ((await read(request, 'ui')).data.sound || {})).toBe(false)
  })

  test('names the text-to-speech default, and says when it is not local or not usable', async ({ page, request }) => {
    await models(page)
    await write(request, 'defaults', { tts: 'kokoro-82m' })
    await page.goto('/app/account?tab=sound')
    await expect(page.getByTestId('sd-model-line')).toContainText('kokoro-82m')
    await expect(page.getByTestId('sd-model-line')).toContainText('your text-to-speech default')
    await expect(page.getByTestId('sd-model-remote')).toHaveCount(0)
    await expect(page.getByTestId('sd-sample')).toBeEnabled()

    await write(request, 'defaults', { tts: 'cloud-voice' })
    await expect(page.getByTestId('sd-model-remote')).toContainText('not a local model')
    await expect(page.getByTestId('sd-model-remote')).toContainText('cloud-voice')

    await write(request, 'defaults', { tts: 'ask' })
    await expect(page.getByTestId('sd-model-missing')).toContainText('ask each time')
    await expect(page.getByTestId('sd-sample')).toBeDisabled()

    await write(request, 'defaults', { tts: 'gone-model' })
    await expect(page.getByTestId('sd-model-missing')).toContainText('gone-model')
    await write(request, 'defaults', {})
    await expect(page.getByTestId('sd-model-line')).toContainText('first text-to-speech model installed')
  })

  test('the voice settings are saved at once; speed and the voice name are optional', async ({ page, request }) => {
    await models(page)
    await page.goto('/app/account?tab=sound')
    await page.getByTestId('sd-read-button').click()
    await expect.poll(async () => (await read(request, 'ui')).data.voice?.readAloud).toBe('button')
    await page.getByTestId('sd-skip-code').click()
    await expect.poll(async () => (await read(request, 'ui')).data.voice?.skipCode).toBe(false)
    await page.getByTestId('sd-voice-name').fill('aria')
    await expect.poll(async () => (await read(request, 'ui')).data.voice?.voice).toBe('aria')
    await page.getByTestId('sd-speed-slider-input').fill('1.5')
    await expect.poll(async () => (await read(request, 'ui')).data.voice?.speed).toBe(1.5)
    await page.getByTestId('sd-voice-name').fill('')
    await expect.poll(async () => 'voice' in ((await read(request, 'ui')).data.voice || {})).toBe(false)
    expect((await read(request, 'ui')).valid).toBe(true)
  })

  test('an invalid sound in the file is refused by the server and the last good version stays', async ({ request }) => {
    await write(request, 'ui', { sound: { enabled: true, volume: 0.4 } })
    const refused = await request.put('/api/chats/wellknown/ui', { data: { values: { sound: { volume: 3 } } } })
    expect(refused.status()).toBe(400)
    const same = await request.put('/api/chats/wellknown/ui', { data: { values: { sound: { quietHours: ['22:00', '22:00'] } } } })
    expect(same.status()).toBe(400)
    expect(await same.text()).toContain('no time at all')
    await upload(request, '/LocalAI/ui.json', JSON.stringify({ sound: { enabled: true, volume: 9 } }))
    const doc = await read(request, 'ui')
    expect(doc.valid).toBe(false)
    expect(doc.data.sound.volume).toBe(0.4)
  })
})

// The player, with a faked clock of the tab: a sound plays after a gesture, for
// an event that is on, outside quiet hours; each play is recorded in
// window.__localaiSounds.
test.describe('the sounds of events', () => {
  async function backgroundTab(page) {
    await page.addInitScript(() => { Document.prototype.hasFocus = () => false })
  }
  const played = (page) => page.evaluate(() => (window.__localaiSounds || []).map(s => [s.event, s.name]))

  test('nothing plays by default; with sound on it plays after a gesture, not in quiet hours, not for none', async ({ page, request }) => {
    await backgroundTab(page)
    await openSlashChat(page)
    // No ui.json key: silent, even after a gesture.
    await page.getByTestId('chat-input').click()
    await emit(page, 'turn_end', {})
    await page.waitForTimeout(300)
    expect(await played(page)).toEqual([])

    await write(request, 'ui', { sound: { enabled: true, volume: 0.5, events: { approval: 'none' } } })
    await expect.poll(async () => { await emit(page, 'turn_end', {}); return (await played(page)).length }, { timeout: 15_000 }).toBeGreaterThan(0)
    expect((await played(page))[0]).toEqual(['replyDone', 'soft-chime'])
    const before = (await played(page)).length
    // An event set to none is silent.
    await emit(page, 'trigger_proposed', {})
    await page.waitForTimeout(300)
    expect((await played(page)).length).toBe(before)
    // The error sound.
    await emit(page, 'error', { text: 'boom', code: 'upstream' })
    await expect.poll(async () => (await played(page)).some(s => s[0] === 'error')).toBe(true)

    // Quiet hours that cover now silence it.
    const now = await page.evaluate(() => { const d = new Date(); return d.getHours() * 60 + d.getMinutes() })
    const hhmm = (m) => `${String(Math.floor(((m % 1440) + 1440) % 1440 / 60)).padStart(2, '0')}:${String(((m % 60) + 60) % 60).padStart(2, '0')}`
    await write(request, 'ui', { sound: { enabled: true, quietHours: [hhmm(now - 30), hhmm(now + 30)] } })
    await page.waitForTimeout(1200)
    const quietFrom = (await played(page)).length
    await expect.poll(async () => { await emit(page, 'turn_end', {}); await page.waitForTimeout(200); return (await played(page)).length }, { timeout: 5_000 }).toBe(quietFrom)
  })

  test('a reply stays silent while the tab is in front', async ({ page, request }) => {
    await write(request, 'ui', { sound: { enabled: true } })
    await page.addInitScript(() => { Document.prototype.hasFocus = () => true })
    await openSlashChat(page)
    await page.getByTestId('chat-input').click()
    await page.waitForTimeout(800)
    await emit(page, 'turn_end', {})
    await page.waitForTimeout(400)
    expect(await played(page)).toEqual([])
  })

  test('the play buttons of the Sound tab play the chosen sound', async ({ page, request }) => {
    await write(request, 'ui', { sound: { events: { replyDone: 'pop' } } })
    await models(page)
    await page.goto('/app/account?tab=sound')
    await page.getByTestId('sd-play-replyDone').click()
    await expect.poll(() => played(page)).toEqual([['preview', 'pop']])
    await page.getByTestId('sd-select-error').selectOption('none')
    await expect(page.getByTestId('sd-play-error')).toBeDisabled()
  })
})

test.describe('reading replies aloud', () => {
  test('the speaker under a reply sends the text to the default text-to-speech model, and leaves code out', async ({ page, request }) => {
    await write(request, 'ui', { voice: { readAloud: 'button', voice: 'aria', speed: 1.25, skipCode: true } })
    await write(request, 'defaults', { tts: 'kokoro-82m' })
    let speech = null
    await page.route('**/v1/audio/speech', (route) => {
      speech = route.request().postDataJSON()
      return route.fulfill({ status: 200, contentType: 'audio/wav', body: Buffer.from('UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YQAAAAA=', 'base64') })
    })
    await openSlashChat(page, { messages: [
      { seq: 1, turn: 1, role: 'user', text: 'Show me code.', at: '2026-10-25T10:00:00Z' },
      { seq: 2, turn: 1, role: 'assistant', text: 'Here it is.\n\n```js\nconst a = 1\n```\n\nDone.', at: '2026-10-25T10:00:00Z' },
    ] })
    // After the chat stubs, which list chat models only.
    await models(page)
    const speaker = page.getByTestId('message-read-aloud')
    await expect(speaker).toHaveCount(1)
    await speaker.click()
    await expect.poll(() => speech).not.toBeNull()
    expect(speech.model).toBe('kokoro-82m')
    expect(speech.voice).toBe('aria')
    expect(speech.input).toContain('Here it is.')
    expect(speech.input).toContain('Code block omitted.')
    expect(speech.input).not.toContain('const a')
    expect(speech.input).toContain('Done.')
  })

  test('no speaker unless read aloud is on; with ask as the default the speaker says why it cannot read', async ({ page, request }) => {
    await openSlashChat(page)
    await models(page)
    await expect(page.getByTestId('message-read-aloud')).toHaveCount(0)
    await write(request, 'ui', { voice: { readAloud: 'button' } })
    await write(request, 'defaults', { tts: 'ask' })
    // The chat stub fakes every event stream, the change feed too: read the files again.
    await page.reload()
    await expect(page.getByTestId('message-read-aloud')).toHaveCount(1)
    await page.getByTestId('message-read-aloud').click()
    await expect(page.getByTestId('message-read-aloud-problem')).toContainText('ask each time')
  })
})
