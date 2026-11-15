import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { mockChat } from './chat-fixtures.js'

// The background of the chat view, kept in chatBackground of ui.json: written
// by the assistant (a plain file write) or by the Appearance editor, shown
// behind the chat only, validated by the server, and live through the change
// feed. It has the same shape as homeBackground (home-background.spec.js) and
// the two do not touch each other.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/ui.json', '/LocalAI/home.json', '/Pictures/sky.png', '/Pictures/notes.txt']
// A 2x2 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8Dwn+E/A8N/BiBgAAAWBgIBoFcdhgAAAABJRU5ErkJggg==', 'base64')
const AT = '2026-10-25T10:00:00Z'
const HISTORY = [
  { seq: 1, turn: 1, role: 'user', text: 'Compare the quotes.', at: AT },
  { seq: 2, turn: 1, role: 'assistant', text: 'The second one is cheapest.', at: AT },
]

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
const read = (request, name) => request.get(`/api/chats/wellknown/${name}`).then(r => r.json())
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const chatBg = (page) => page.getByTestId('chat-bg-art')

// A saved chat with a short history, the rest of the chat API stubbed; ui.json
// is the real one of the test server.
async function openChat(page) {
  // The stream of this chat is faked; the change feed of the files stays real,
  // because the background arrives through it.
  await page.addInitScript(() => {
    const Real = window.EventSource
    window.EventSource = function (url, opts) {
      if (!String(url).includes('/api/chats/c1/events')) return new Real(url, opts)
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
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true } }))
  await page.route('**/api/chats/agents*', route => route.fulfill({ json: { agents: [] } }))
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-thread')).toBeVisible()
}

test.beforeEach(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

test('chat background: a gradient the assistant writes shows behind the chat, changes live and is not on Home or other pages', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', x_mood: 'calm', chatBackground: { gradient: { from: '#0f172a', to: '#3b82f6', angle: 135 } } })
  await openChat(page)
  await expect(chatBg(page)).toHaveCSS('background-image', /linear-gradient\(135deg/)
  // A chat is long text: the veil defaults stronger than the one of Home (0.35).
  await expect(page.getByTestId('chat-bg-scrim')).toHaveCSS('opacity', '0.6')
  // The messages sit on a panel in the page colour, over the layer.
  await expect(page.getByTestId('chat-thread')).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')

  // The file changes while the chat is open: the change feed brings it in.
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', chatBackground: { color: '#102030', overlay: 0 } })
  await expect(chatBg(page)).toHaveCSS('background-color', 'rgb(16, 32, 48)')
  await expect(page.getByTestId('chat-bg-scrim')).toHaveCSS('opacity', '0')

  // Only the chat draws it.
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible()
  await expect(page.getByTestId('chat-bg')).toHaveCount(0)
  await expect(page.getByTestId('home-bg')).toHaveCount(0)
  await page.goto('/app/account?tab=appearance')
  await expect(page.getByTestId('chat-bg')).toHaveCount(0)

  // Without the key the chat has no background and no panel.
  await upload(request, '/LocalAI/ui.json', { theme: 'dark' })
  await openChat(page)
  await expect(page.getByTestId('chat-bg')).toHaveCount(0)
  await expect(page.getByTestId('chat-thread')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
})

test('chat background: Home and the chat each draw their own key', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { homeBackground: { color: '#112233' }, chatBackground: { color: '#334455' } })
  await page.goto('/app')
  await expect(page.getByTestId('home-bg-art')).toHaveCSS('background-color', 'rgb(17, 34, 51)')
  await expect(page.getByTestId('chat-bg')).toHaveCount(0)
  await openChat(page)
  await expect(chatBg(page)).toHaveCSS('background-color', 'rgb(51, 68, 85)')
  await expect(page.getByTestId('home-bg')).toHaveCount(0)
})

test('chat background: a picture from the files is served with the user own rights and shown', async ({ page, request }) => {
  await upload(request, '/Pictures/sky.png', PNG)
  await upload(request, '/LocalAI/ui.json', { chatBackground: { image: 'Pictures/sky.png', fit: 'contain', blur: 3 } })
  await openChat(page)
  const art = chatBg(page)
  await expect(art).toHaveCSS('background-image', /\/api\/chats\/files\/content\?path=%2FPictures%2Fsky\.png/)
  await expect(art).toHaveCSS('background-size', 'contain')
  await expect(art).toHaveCSS('filter', 'blur(3px)')
  await expect(page.getByTestId('chat-bg-scrim')).toHaveCSS('opacity', '0.8')
  const url = (await art.evaluate(el => getComputedStyle(el).backgroundImage)).match(/url\("([^"]+)"\)/)[1]
  const res = await request.get(url)
  expect(res.ok()).toBeTruthy()
  expect(res.headers()['content-type']).toContain('image/png')
})

test('chat background: a path that climbs out of the home, an address or a non-picture is refused and never shown', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { chatBackground: { color: '#102030' } })
  for (const image of ['../../etc/passwd.png', 'Pictures/../../a.png', '/Pictures/sky.png', 'https://example.com/a.png', 'data:image/png;base64,AAAA', 'Pictures/notes.txt', 'Pictures/a.svg']) {
    const res = await request.put('/api/chats/wellknown/ui', { data: { values: { chatBackground: { image } } } })
    expect(res.status(), image).toBe(400)
  }
  expect((await read(request, 'ui')).data.chatBackground).toEqual({ color: '#102030' })

  // The assistant writes the bad file directly: the server keeps the last
  // good version, so the chat does not load the path.
  await upload(request, '/LocalAI/ui.json', { chatBackground: { image: '../../etc/passwd.png' } })
  const doc = await read(request, 'ui')
  expect(doc.valid).toBe(false)
  expect(doc.fallback).toBe(true)
  expect(doc.data.chatBackground).toEqual({ color: '#102030' })
  const requested = []
  page.on('request', r => { if (/passwd|etc/.test(r.url())) requested.push(r.url()) })
  await openChat(page)
  await expect(chatBg(page)).toHaveCSS('background-color', 'rgb(16, 32, 48)')
  expect(requested).toEqual([])
})

test('chat background: the Appearance editor has a second section that picks a picture, keeps other keys, and clears only the chat background', async ({ page, request }) => {
  await upload(request, '/Pictures/sky.png', PNG)
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', x_mood: 'calm', homeBackground: { color: '#112233' } })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  const field = card.getByTestId('wk-field-ui-chatBackground')
  await expect(field).toBeVisible()
  await expect(card.getByTestId('wk-field-ui-homeBackground')).toBeVisible()

  await field.getByTestId('wk-chatBackground-kind-gradient').click()
  await field.getByTestId('wk-chatBackground-angle').fill('90')
  // The veil slider starts at the chat default for a gradient.
  await expect(field.getByTestId('wk-chatBackground-overlay')).toHaveValue('0.6')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.chatBackground?.gradient?.angle).toBe(90)
  let saved = (await read(request, 'ui')).data
  expect(saved.x_mood).toBe('calm')
  expect(saved.theme).toBe('dark')
  expect(saved.homeBackground).toEqual({ color: '#112233' })

  await field.getByTestId('wk-chatBackground-kind-image').click()
  await expect(field.getByTestId('wk-chatBackground-picker')).toBeVisible()
  await field.getByTestId('wk-chatBackground-picker').getByRole('button', { name: /Pictures/ }).click()
  await field.getByTestId('wk-chatBackground-pick-sky.png').click()
  await expect(field.getByTestId('wk-chatBackground-image')).toHaveValue('Pictures/sky.png')
  await expect(field.getByTestId('wk-chatBackground-overlay')).toHaveValue('0.8')
  await field.getByTestId('wk-chatBackground-fit').selectOption('contain')
  await expect(field.getByTestId('wk-chatBackground-preview').locator('.bg-layer__art')).toHaveCSS('position', 'absolute')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.chatBackground?.image).toBe('Pictures/sky.png')
  saved = (await read(request, 'ui')).data
  expect(saved.chatBackground.fit).toBe('contain')
  expect(saved.chatBackground.gradient).toBeUndefined()
  expect(saved.x_mood).toBe('calm')
  expect(saved.homeBackground).toEqual({ color: '#112233' })

  // A typed path that is not allowed says so and is not saved.
  await field.getByTestId('wk-chatBackground-image').fill('../secret.png')
  await expect(field.getByTestId('wk-chatBackground-image-error')).toBeVisible()
  await card.getByTestId('wk-save-ui').click()
  await expect(card.getByTestId('wk-problem-ui')).toBeVisible()
  expect((await read(request, 'ui')).data.chatBackground.image).toBe('Pictures/sky.png')

  await field.getByTestId('wk-chatBackground-clear').click()
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.chatBackground).toBeUndefined()
  saved = (await read(request, 'ui')).data
  expect(saved.x_mood).toBe('calm')
  expect(saved.homeBackground).toEqual({ color: '#112233' })
})

test('chat background: the editor shows what the assistant wrote and keeps unknown keys inside the background', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { chatBackground: { color: '#445566', x_future: 1 } })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  await expect(card.getByTestId('wk-chatBackground-color')).toHaveValue('#445566')
  await card.getByTestId('wk-chatBackground-opacity').fill('0.5')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.chatBackground?.opacity).toBe(0.5)
  expect((await read(request, 'ui')).data.chatBackground).toEqual({ color: '#445566', x_future: 1, opacity: 0.5 })
})
