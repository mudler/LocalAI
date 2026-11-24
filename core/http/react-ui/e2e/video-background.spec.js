import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'
import { mockChat } from './chat-fixtures.js'

// A video as the background of Home and of the chat (video of homeBackground
// and chatBackground in ui.json): a muted loop under the page, served from
// the user's files with byte ranges, paused while the tab is hidden and still
// under reduced motion.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/ui.json', '/LocalAI/home.json', '/Videos/loop.webm', '/Videos/notes.txt', '/Pictures/sky.png']
// A 2 second, 160x90 VP8 clip.
const WEBM = Buffer.from('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', 'base64')
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

async function openChat(page) {
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

test('video background: Home plays a muted looping video from the files, with the video veil', async ({ page, request }) => {
  await upload(request, '/Videos/loop.webm', WEBM)
  await upload(request, '/LocalAI/ui.json', { homeBackground: { video: 'Videos/loop.webm', blur: 2, opacity: 0.8 } })
  await page.goto('/app')
  const video = page.getByTestId('home-bg-video')
  await expect(video).toBeAttached()
  await expect(video).toHaveJSProperty('muted', true)
  await expect(video).toHaveJSProperty('loop', true)
  await expect(video).toHaveJSProperty('controls', false)
  await expect(video).toHaveAttribute('playsinline', '')
  await expect(video).toHaveAttribute('aria-hidden', 'true')
  await expect(video).toHaveAttribute('src', /\/api\/chats\/files\/content\?path=%2FVideos%2Floop\.webm&inline=1/)
  await expect(video).toHaveCSS('object-fit', 'cover')
  await expect(page.getByTestId('home-bg-art')).toHaveCSS('opacity', '0.8')
  await expect(page.getByTestId('home-bg-art')).toHaveCSS('filter', 'blur(2px)')
  await expect(page.getByTestId('home-bg-scrim')).toHaveCSS('opacity', '0.55')
  await expect.poll(() => video.evaluate(el => el.readyState >= 2 && !el.paused)).toBe(true)
  await page.screenshot({ path: process.env.VIDEO_BG_SHOTS ? `${process.env.VIDEO_BG_SHOTS}/home.png` : undefined })
})

test('video background: the chat has its own video and a stronger veil, and the picture is the poster', async ({ page, request }) => {
  await upload(request, '/Videos/loop.webm', WEBM)
  await upload(request, '/Pictures/sky.png', PNG)
  await upload(request, '/LocalAI/ui.json', { chatBackground: { video: 'Videos/loop.webm', image: 'Pictures/sky.png', fit: 'contain' } })
  await openChat(page)
  const video = page.getByTestId('chat-bg-video')
  await expect(video).toBeAttached()
  await expect(video).toHaveAttribute('poster', /path=%2FPictures%2Fsky\.png/)
  await expect(video).toHaveCSS('object-fit', 'contain')
  await expect(page.getByTestId('chat-bg-art')).toHaveCSS('background-image', 'none')
  await expect(page.getByTestId('chat-bg-scrim')).toHaveCSS('opacity', '0.8')
  await expect(page.getByTestId('home-bg')).toHaveCount(0)
  await page.screenshot({ path: process.env.VIDEO_BG_SHOTS ? `${process.env.VIDEO_BG_SHOTS}/chat.png` : undefined })
})

test('video background: the video pauses while the tab is hidden and plays again when it is back', async ({ page, request }) => {
  await upload(request, '/Videos/loop.webm', WEBM)
  await upload(request, '/LocalAI/ui.json', { homeBackground: { video: 'Videos/loop.webm' } })
  await page.goto('/app')
  const video = page.getByTestId('home-bg-video')
  await expect.poll(() => video.evaluate(el => !el.paused)).toBe(true)
  const hide = (hidden) => page.evaluate((h) => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => h })
    document.dispatchEvent(new Event('visibilitychange'))
  }, hidden)
  await hide(true)
  await expect.poll(() => video.evaluate(el => el.paused)).toBe(true)
  await hide(false)
  await expect.poll(() => video.evaluate(el => !el.paused)).toBe(true)
})

test('video background: with reduced motion the video does not play and shows the still frame', async ({ page, request }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await upload(request, '/Videos/loop.webm', WEBM)
  await upload(request, '/LocalAI/ui.json', { homeBackground: { video: 'Videos/loop.webm' }, chatBackground: { video: 'Videos/loop.webm', image: 'Pictures/sky.png' } })
  await page.goto('/app')
  const video = page.getByTestId('home-bg-video')
  await expect(video).toBeAttached()
  await expect(video).toHaveJSProperty('autoplay', false)
  await expect(video).toHaveAttribute('preload', 'metadata')
  await expect.poll(() => video.evaluate(el => el.readyState >= 1)).toBe(true)
  expect(await video.evaluate(el => el.paused)).toBe(true)
  expect(await video.evaluate(el => el.currentTime)).toBe(0)
})

test('video background: the files endpoint serves the video inline with its type and byte ranges', async ({ request }) => {
  await upload(request, '/Videos/loop.webm', WEBM)
  const url = '/api/chats/files/content?path=%2FVideos%2Floop.webm&inline=1'
  const full = await request.get(url)
  expect(full.status()).toBe(200)
  expect(full.headers()['content-type']).toBe('video/webm')
  expect(full.headers()['accept-ranges']).toBe('bytes')
  const part = await request.get(url, { headers: { Range: 'bytes=0-9' } })
  expect(part.status()).toBe(206)
  expect(part.headers()['content-range']).toBe(`bytes 0-9/${WEBM.length}`)
  expect((await part.body()).length).toBe(10)
})

test('video background: a bad video path is refused by the server and never requested', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { homeBackground: { color: '#102030' } })
  for (const video of ['../../etc/passwd.mp4', 'Videos/../../a.webm', '/Videos/loop.webm', 'https://example.com/a.mp4', 'data:video/mp4;base64,AAAA', 'Videos/notes.txt', 'Pictures/sky.png', 'Videos/a.avi']) {
    const res = await request.put('/api/chats/wellknown/ui', { data: { values: { homeBackground: { video } } } })
    expect(res.status(), video).toBe(400)
  }
  expect((await read(request, 'ui')).data.homeBackground).toEqual({ color: '#102030' })
  await upload(request, '/LocalAI/ui.json', { homeBackground: { video: '../../etc/passwd.mp4' } })
  const requested = []
  page.on('request', r => { if (/passwd|etc/.test(r.url())) requested.push(r.url()) })
  await page.goto('/app')
  await expect(page.getByTestId('home-bg-art')).toHaveCSS('background-color', 'rgb(16, 32, 48)')
  await expect(page.getByTestId('home-bg-video')).toHaveCount(0)
  expect(requested).toEqual([])
})

test('video background: the Appearance editor picks a video, shows an error for a bad path and saves', async ({ page, request }) => {
  await upload(request, '/Videos/loop.webm', WEBM)
  await upload(request, '/Videos/notes.txt', 'x')
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', x_mood: 'calm' })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  const field = card.getByTestId('wk-field-ui-homeBackground')
  await field.getByTestId('wk-bg-kind-video').click()
  const picker = field.getByTestId('wk-bg-picker')
  await expect(picker).toBeVisible()
  await picker.getByRole('button', { name: /Videos/ }).click()
  await expect(picker.getByTestId('wk-bg-pick-notes.txt')).toHaveCount(0)
  await picker.getByTestId('wk-bg-pick-loop.webm').click()
  await expect(field.getByTestId('wk-bg-video')).toHaveValue('Videos/loop.webm')
  await expect(field.getByTestId('wk-bg-preview-video')).toBeAttached()
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.homeBackground?.video).toBe('Videos/loop.webm')
  expect((await read(request, 'ui')).data.x_mood).toBe('calm')

  await field.getByTestId('wk-bg-video').fill('../secret.mp4')
  await expect(field.getByTestId('wk-bg-video-error')).toBeVisible()
  await card.getByTestId('wk-save-ui').click()
  await expect(card.getByTestId('wk-problem-ui')).toBeVisible()
  expect((await read(request, 'ui')).data.homeBackground.video).toBe('Videos/loop.webm')
})
