import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { mockChat } from './chat-fixtures.js'

// The assistant identity (persona.json avatar, greeting, emptyChat and the
// headline of home.json), the look of ui.json (look, accent contrast) and
// the text overrides of ui.json (strings): written by the assistant (a plain
// file write) or by the settings tabs, checked by the server, shown in Home,
// the chat and the empty chat, and live through the change feed. A broken
// value falls back to the last good version.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/ui.json', '/LocalAI/home.json', '/LocalAI/persona.json', '/LocalAI/user.json', '/LocalAI/shortcuts.json', '/Pictures/avatars/juniper.png', '/Pictures/notes.txt']
// A 2x2 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8Dwn+E/A8N/BiBgAAAWBgIBoFcdhgAAAABJRU5ErkJggg==', 'base64')
const AT = '2026-10-25T10:00:00Z'
const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Compare the quotes.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'The second one is cheapest.\n\n```python\nwith open("a") as f:\n    pass\n```', at: AT },
]
const PERSONA = {
  name: 'Juniper',
  avatar: 'Pictures/avatars/juniper.png',
  greeting: 'Hi {user.name}, what are we working on?',
  emptyChat: { title: 'Ask Juniper anything', subtitle: 'I remember your projects.', showShortcuts: false },
}

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
const read = (request, name) => request.get(`/api/chats/wellknown/${name}`).then(r => r.json())
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const put = (request, name, values) => request.put(`/api/chats/wellknown/${name}`, { data: { values } })
const rootAttr = (page, name) => page.evaluate((n) => document.documentElement.getAttribute(n), name)
const rootVar = (page, name) => page.evaluate((n) => document.documentElement.style.getPropertyValue(n), name)

async function stubChat(page) {
  await page.addInitScript(() => {
    const Real = window.EventSource
    window.EventSource = function (url, opts) {
      if (!/\/api\/chats\/c[12]\/events/.test(String(url))) return new Real(url, opts)
      const fake = { addEventListener() {}, close() {}, onopen: null }
      setTimeout(() => fake.onopen && fake.onopen(), 0)
      return fake
    }
    window.EventSource.prototype = Real.prototype
  })
  await mockChat(page)
  const detail = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: AT, updated: AT, running: false, last_turn: 'done', pending: [] }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: HISTORY } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  // c2 is a new chat with no message yet: the empty chat.
  const empty = { ...detail, id: 'c2', title: 'New chat', last_turn: '' }
  await page.route(/\/api\/chats\/c2\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c2', route => route.fulfill({ json: empty }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }, { ...empty, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
}

test.beforeEach(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

test('identity: what the assistant writes shows on Home, in the empty chat and above its replies, and changes live', async ({ page, request }) => {
  await upload(request, '/Pictures/avatars/juniper.png', PNG)
  await upload(request, '/LocalAI/user.json', { name: 'Marta' })
  await upload(request, '/LocalAI/persona.json', { ...PERSONA, x_mood: 'calm' })
  await upload(request, '/LocalAI/home.json', { headline: '{time_of_day}, {user.name}', widgets: [] })
  await stubChat(page)

  await page.goto('/app')
  await expect(page.getByTestId('home-headline')).toHaveText(/^Good (morning|afternoon|evening), Marta$|^Working late, Marta$/)
  await expect(page.getByTestId('home-assistant-avatar').locator('img')).toHaveAttribute('src', /path=%2FPictures%2Favatars%2Fjuniper\.png/)
  await expect(page.getByTestId('home-input')).toHaveAttribute('placeholder', 'Message Juniper...')

  await page.goto('/app/chats/c2')
  await expect(page.getByTestId('chat-empty-title')).toHaveText('Ask Juniper anything')
  await expect(page.getByTestId('chat-empty-subtitle')).toHaveText('I remember your projects.')
  await expect(page.getByTestId('chat-greeting')).toContainText('Hi Marta, what are we working on?')
  await expect(page.getByTestId('chat-empty-avatar').locator('img')).toBeVisible()
  // showShortcuts false: the empty chat has no shortcuts row.
  await expect(page.getByTestId('chat-shortcuts')).toHaveCount(0)

  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-assistant-name')).toHaveText('Juniper')
  await expect(page.locator('[data-role="assistant"] .cx-who__model')).toHaveText('qwen3-8b')
  // The greeting is not part of the transcript.
  await expect(page.getByTestId('chat-greeting')).toHaveCount(0)

  // The assistant renames itself while the chat is open: the feed brings it in.
  await upload(request, '/LocalAI/persona.json', { ...PERSONA, name: 'Juno' })
  await expect(page.getByTestId('chat-assistant-name')).toHaveText('Juno')
})

test('identity: a bad avatar is refused, and a broken greeting falls back to the last good one with Revert', async ({ page, request }) => {
  for (const avatar of ['../../etc/passwd.png', '/Pictures/a.png', 'https://example.com/a.png', 'Pictures/a.svg', 'Pictures/notes.txt']) {
    const res = await put(request, 'persona', { avatar })
    expect(res.status(), avatar).toBe(400)
  }
  expect((await put(request, 'persona', { greeting: 'Hi {user.nmae}' })).status()).toBe(400)
  expect((await put(request, 'persona', PERSONA)).status()).toBe(200)

  // The assistant writes a broken placeholder: the last good version is used.
  await upload(request, '/LocalAI/persona.json', { ...PERSONA, greeting: 'Hi {usr' })
  const doc = await read(request, 'persona')
  expect(doc.valid).toBe(false)
  expect(doc.fallback).toBe(true)
  expect(doc.data.greeting).toBe(PERSONA.greeting)
  await stubChat(page)
  await page.goto('/app/chats/c2')
  await expect(page.getByTestId('chat-greeting')).toContainText('what are we working on?')

  await page.goto('/app/account?tab=about')
  await expect(page.getByTestId('wk-invalid-persona')).toBeVisible()
  await page.getByTestId('wk-card-persona').getByTestId('wk-revert-persona.json').click()
  await expect.poll(async () => (await read(request, 'persona')).valid).toBe(true)
})

test('identity: the About tab edits the avatar, greeting, empty chat and headline with a live preview and keeps other keys', async ({ page, request }) => {
  await upload(request, '/Pictures/avatars/juniper.png', PNG)
  await upload(request, '/LocalAI/persona.json', { name: 'Juniper', system_prompt: 'Be brief.', x_mood: 'calm' })
  await upload(request, '/LocalAI/home.json', { widgets: [{ id: 'recent', type: 'chats', title: 'Recent' }] })
  await page.goto('/app/account?tab=about')
  const card = page.getByTestId('wk-card-persona')
  await expect(card.getByTestId('wk-stored-persona')).toContainText('LocalAI/persona.json')

  await card.getByTestId('wk-avatar-browse').click()
  await card.getByTestId('wk-avatar-picker').getByRole('button', { name: /Pictures/ }).click()
  await card.getByTestId('wk-avatar-picker').getByRole('button', { name: /avatars/ }).click()
  await card.getByTestId('wk-avatar-pick-juniper.png').click()
  await expect(card.getByTestId('wk-avatar-path')).toHaveValue('Pictures/avatars/juniper.png')

  // A broken placeholder shows at once and blocks the save.
  const greeting = card.getByTestId('wk-field-persona-greeting')
  await greeting.fill('Hi {user.nmae}')
  await expect(card.getByTestId('wk-persona-greeting-ph-error')).toContainText('{user.nmae}')
  await expect(card.getByTestId('wk-save-persona')).toBeDisabled()
  await greeting.fill('Hi ')
  await card.getByRole('button', { name: '{user.name}' }).first().click()
  await expect(greeting).toHaveValue('Hi {user.name}')
  await expect(page.getByTestId('wk-preview-greeting')).toContainText('Hi')

  await card.getByTestId('wk-field-persona-emptyChat-title').fill('Ask Juniper anything')
  await expect(page.getByTestId('wk-preview-empty-title')).toHaveText('Ask Juniper anything')
  await card.getByTestId('wk-emptychat-shortcuts').uncheck()
  await card.getByTestId('wk-save-persona').click()
  await expect.poll(async () => (await read(request, 'persona')).data.greeting).toBe('Hi {user.name}')
  const saved = (await read(request, 'persona')).data
  expect(saved.avatar).toBe('Pictures/avatars/juniper.png')
  expect(saved.emptyChat).toEqual({ title: 'Ask Juniper anything', showShortcuts: false })
  expect(saved.x_mood).toBe('calm')
  expect(saved.system_prompt).toBe('Be brief.')

  const headline = page.getByTestId('wk-card-home-headline')
  await headline.getByTestId('wk-field-home-headline').fill('Rise and shine, {user.name}')
  await expect(page.getByTestId('wk-preview-headline')).toContainText('Rise and shine')
  await headline.getByTestId('wk-save-home').click()
  await expect.poll(async () => (await read(request, 'home')).data.headline).toBe('Rise and shine, {user.name}')
  expect((await read(request, 'home')).data.widgets).toHaveLength(1)
})

test('look: the values the assistant writes reach the page as tokens, and a value outside the lists is refused', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { theme: 'light', look: { font: 'serif', radius: 'round', bubble: 'tinted', messageWidth: 'wide', codeTheme: 'ink' } })
  await stubChat(page)
  await page.goto('/app/chats/c1')
  await expect.poll(() => rootAttr(page, 'data-look-font')).toBe('serif')
  expect(await rootAttr(page, 'data-look-radius')).toBe('round')
  expect(await rootAttr(page, 'data-look-messagewidth')).toBe('wide')
  await expect(page.getByTestId('chat-thread')).toHaveCSS('max-width', '920px')
  await expect(page.locator('.cx-bubble').first()).toHaveCSS('font-family', /serif/)
  const bubbleBg = await page.locator('.cx-bubble').first().evaluate(el => getComputedStyle(el).backgroundColor)
  const washBg = await page.evaluate(() => { const d = document.createElement('div'); d.style.background = 'var(--dk-accent-wash)'; document.body.appendChild(d); const c = getComputedStyle(d).backgroundColor; d.remove(); return c })
  expect(bubbleBg).toBe(washBg)

  // The look changes while the chat is open; back to the default leaves no attribute.
  await upload(request, '/LocalAI/ui.json', { theme: 'light' })
  await expect.poll(() => rootAttr(page, 'data-look-font')).toBe(null)
  await expect(page.getByTestId('chat-thread')).toHaveCSS('max-width', '760px')

  for (const look of [{ font: 'Comic Sans MS' }, { radius: '12px' }, { codeTheme: 'solarized' }, { messageWidth: 760 }]) {
    const res = await put(request, 'ui', { look })
    expect(res.status(), JSON.stringify(look)).toBe(400)
  }
})

test('look: an accent too faint for the page is not used and the editor says so; a readable one is', async ({ page, request }) => {
  // Dark teal: readable on the light page, too faint on the dark one.
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', accent: '#0f766e', x_mood: 'calm' })
  await page.goto('/app/account?tab=appearance')
  await expect.poll(() => rootAttr(page, 'data-accent-ignored')).toBe('#0f766e')
  expect(await rootVar(page, '--dk-accent')).toBe('')
  const card = page.getByTestId('wk-card-ui')
  await expect(card.getByTestId('wk-contrast-fail')).toBeVisible()

  // The light theme makes the same accent readable: it applies.
  await card.getByTestId('wk-field-ui-theme').selectOption('light')
  await expect(card.getByTestId('wk-contrast-ok')).toBeVisible()
  await expect.poll(() => rootVar(page, '--dk-accent')).toBe('#0f766e')
  expect(await rootVar(page, '--dk-on-accent')).toBe('var(--dk-canvas)')

  // A pale accent fails on the light page and cannot be saved.
  await card.getByTestId('wk-field-ui-accent').fill('#ffe08a')
  await expect(card.getByTestId('wk-contrast-fail')).toBeVisible()
  await expect(card.getByTestId('wk-save-ui')).toBeDisabled()
  await expect(card.getByTestId('wk-blocked-ui')).toBeVisible()

  await card.getByTestId('wk-field-ui-accent').fill('#0f766e')
  await card.getByTestId('wk-look-font-serif').click()
  await card.getByTestId('wk-look-bubble-filled').click()
  await card.getByTestId('wk-look-code-vivid').click()
  await card.getByTestId('wk-look-width-narrow').click()
  // The preview follows the edit before the save.
  await expect(page.getByTestId('wk-lookprev-bubble')).toHaveCSS('font-family', /serif/)
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.look).toEqual({ font: 'serif', bubble: 'filled', codeTheme: 'vivid', messageWidth: 'narrow' })
  const saved = (await read(request, 'ui')).data
  expect(saved.theme).toBe('light')
  expect(saved.accent).toBe('#0f766e')
  expect(saved.x_mood).toBe('calm')
  // The Appearance form does not show the text overrides.
  await expect(card.getByTestId('wk-field-ui-strings')).toHaveCount(0)

  // Picking the default again removes the key.
  await card.getByTestId('wk-look-font-default').click()
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.look?.font).toBeUndefined()
})

test('strings: the overrides on the list show in the sidebar, Home and the chat; others and broken placeholders are refused', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { strings: {
    'nav.newChat': 'New thread', 'nav.home': 'Start', 'nav.recentChats': 'Lately', 'nav.allChats': 'Every chat',
    'home.headline': 'Rise and shine', 'composer.placeholder': 'Ask {model} anything', 'chat.empty.title': 'Ready when you are',
  } })
  await stubChat(page)
  await page.goto('/app')
  await expect(page.getByTestId('home-headline')).toHaveText('Rise and shine')
  await expect(page.getByTestId('sidebar-new-chat')).toHaveAttribute('aria-label', 'New thread')
  await expect(page.locator('#sidebar-chats-title')).toHaveText('Lately')
  await expect(page.locator('.sidebar-nav .nav-item').filter({ hasText: 'Start' })).toHaveCount(1)
  await expect(page.getByTestId('home-input')).toHaveAttribute('placeholder', /^Ask .+ anything$/)

  await page.goto('/app/chats/c2')
  await expect(page.getByTestId('chat-empty-title')).toHaveText('Ready when you are')

  // home.json headline wins over the override.
  await upload(request, '/LocalAI/home.json', { headline: 'From home.json' })
  await page.goto('/app')
  await expect(page.getByTestId('home-headline')).toHaveText('From home.json')

  for (const strings of [{ 'permission.allow': 'Always allow' }, { 'consent.share': 'Sure' }, { 'composer.placeholder': 'Ask {modle}' }, { 'nav.newChat': 'New {model}' }, { 'composer.placeholder': 'Ask {{model}}' }]) {
    const res = await put(request, 'ui', { strings })
    expect(res.status(), JSON.stringify(strings)).toBe(400)
    const body = await res.json()
    expect(JSON.stringify(body.errors)).toMatch(/strings/)
  }
  expect((await read(request, 'ui')).data.strings['nav.newChat']).toBe('New thread')
})

test('strings: the Text tab edits only the listed keys, flags a broken placeholder and keeps the other keys of ui.json', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', look: { font: 'mono' }, x_mood: 'calm', strings: { 'nav.home': 'Start' } })
  await page.goto('/app/account?tab=strings')
  const card = page.getByTestId('wk-card-strings')
  await expect(card.getByTestId('wk-strings-changed')).toContainText('1')
  await expect(card.getByTestId('wk-strings-row-permission.allow')).toHaveCount(0)
  await expect(card.getByTestId('wk-stored-ui')).toContainText('LocalAI/ui.json')

  await card.getByTestId('wk-strings-input-composer.placeholder').fill('Ask {modle}')
  await expect(card.getByTestId('wk-strings-error-composer.placeholder')).toContainText('{modle}')
  await expect(card.getByTestId('wk-save-ui')).toBeDisabled()
  await card.getByTestId('wk-strings-input-composer.placeholder').fill('Ask {model}')
  await card.getByTestId('wk-strings-input-nav.newChat').fill('New thread')
  await expect(page.getByTestId('wk-preview-nav-new')).toHaveText('New thread')
  await expect(page.getByTestId('wk-preview-placeholder')).toHaveText('Ask qwen3-8b')
  await card.getByTestId('wk-strings-input-nav.home').fill('')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.strings).toEqual({ 'composer.placeholder': 'Ask {model}', 'nav.newChat': 'New thread' })
  const saved = (await read(request, 'ui')).data
  expect(saved.look).toEqual({ font: 'mono' })
  expect(saved.x_mood).toBe('calm')
  expect(saved.theme).toBe('dark')

  await page.getByTestId('wk-stored-revert-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.strings).toEqual({ 'nav.home': 'Start' })
})
