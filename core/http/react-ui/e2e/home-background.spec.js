import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// The background of the Home page, kept in homeBackground of ui.json: written
// by the assistant (a plain file write) or by the Appearance editor, shown on
// Home only, validated by the server, and live through the change feed.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/ui.json', '/LocalAI/home.json', '/Pictures/sky.png', '/Pictures/notes.txt']
// A 2x2 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8Dwn+E/A8N/BiBgAAAWBgIBoFcdhgAAAABJRU5ErkJggg==', 'base64')

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
const read = (request, name) => request.get(`/api/chats/wellknown/${name}`).then(r => r.json())
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})
const homeBg = (page) => page.getByTestId('home-bg-art')

test.beforeEach(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

test('home background: a gradient the assistant writes shows on Home, changes live and is not on other pages', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', x_mood: 'calm', homeBackground: { gradient: { from: '#0f172a', to: '#3b82f6', angle: 135 } } })
  await page.goto('/app')
  await expect(homeBg(page)).toHaveCSS('background-image', /linear-gradient\(135deg/)
  await expect(page.getByTestId('home-bg-scrim')).toHaveCSS('opacity', '0.35')

  // The file changes while Home is open: the change feed brings it in.
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', homeBackground: { color: '#102030', overlay: 0 } })
  await expect(homeBg(page)).toHaveCSS('background-color', 'rgb(16, 32, 48)')
  await expect(page.getByTestId('home-bg-scrim')).toHaveCSS('opacity', '0')

  await page.goto('/app/account?tab=appearance')
  await expect(page.getByTestId('home-bg')).toHaveCount(0)

  // Without the key Home has no background.
  await upload(request, '/LocalAI/ui.json', { theme: 'dark' })
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible()
  await expect(page.getByTestId('home-bg')).toHaveCount(0)
})

test('home background: a picture from the files is served with the user own rights and shown', async ({ page, request }) => {
  await upload(request, '/Pictures/sky.png', PNG)
  await upload(request, '/LocalAI/ui.json', { homeBackground: { image: 'Pictures/sky.png', fit: 'contain', blur: 3 } })
  await page.goto('/app')
  const art = homeBg(page)
  await expect(art).toHaveCSS('background-image', /\/api\/chats\/files\/content\?path=%2FPictures%2Fsky\.png/)
  await expect(art).toHaveCSS('background-size', 'contain')
  await expect(art).toHaveCSS('filter', 'blur(3px)')
  const url = (await art.evaluate(el => getComputedStyle(el).backgroundImage)).match(/url\("([^"]+)"\)/)[1]
  const res = await request.get(url)
  expect(res.ok()).toBeTruthy()
  expect(res.headers()['content-type']).toContain('image/png')
})

test('home background: a path that climbs out of the home, an address or a non-picture is refused and never shown', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { homeBackground: { color: '#102030' } })
  for (const image of ['../../etc/passwd.png', 'Pictures/../../a.png', '/Pictures/sky.png', 'https://example.com/a.png', 'data:image/png;base64,AAAA', 'Pictures/notes.txt']) {
    const res = await request.put('/api/chats/wellknown/ui', { data: { values: { homeBackground: { image } } } })
    expect(res.status(), image).toBe(400)
  }
  expect((await read(request, 'ui')).data.homeBackground).toEqual({ color: '#102030' })

  // The assistant writes the bad file directly: the server keeps the last
  // good version, so Home does not load the path.
  await upload(request, '/LocalAI/ui.json', { homeBackground: { image: '../../etc/passwd.png' } })
  const doc = await read(request, 'ui')
  expect(doc.valid).toBe(false)
  expect(doc.fallback).toBe(true)
  expect(doc.data.homeBackground).toEqual({ color: '#102030' })
  const requested = []
  page.on('request', r => { if (/passwd|etc/.test(r.url())) requested.push(r.url()) })
  await page.goto('/app')
  await expect(homeBg(page)).toHaveCSS('background-color', 'rgb(16, 32, 48)')
  expect(requested).toEqual([])
})

test('home background: the Appearance editor picks a gradient and a picture, keeps other keys, and clears the background', async ({ page, request }) => {
  await upload(request, '/Pictures/sky.png', PNG)
  await upload(request, '/LocalAI/ui.json', { theme: 'dark', x_mood: 'calm' })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  const field = card.getByTestId('wk-field-ui-homeBackground')
  await expect(field).toBeVisible()

  await field.getByTestId('wk-bg-kind-gradient').click()
  await field.getByTestId('wk-bg-angle').fill('90')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.homeBackground?.gradient?.angle).toBe(90)
  let saved = (await read(request, 'ui')).data
  expect(saved.x_mood).toBe('calm')
  expect(saved.theme).toBe('dark')

  await field.getByTestId('wk-bg-kind-image').click()
  await expect(field.getByTestId('wk-bg-picker')).toBeVisible()
  await field.getByTestId('wk-bg-picker').getByRole('button', { name: /Pictures/ }).click()
  await field.getByTestId('wk-bg-pick-sky.png').click()
  await expect(field.getByTestId('wk-bg-image')).toHaveValue('Pictures/sky.png')
  await field.getByTestId('wk-bg-fit').selectOption('contain')
  // The preview draws the same layers as Home, so it must be laid out on this page too.
  await expect(field.getByTestId('wk-bg-preview').locator('.bg-layer__art')).toHaveCSS('position', 'absolute')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.homeBackground?.image).toBe('Pictures/sky.png')
  saved = (await read(request, 'ui')).data
  expect(saved.homeBackground.fit).toBe('contain')
  expect(saved.homeBackground.gradient).toBeUndefined()
  expect(saved.x_mood).toBe('calm')

  // A typed path that is not allowed says so and is not saved.
  await field.getByTestId('wk-bg-image').fill('../secret.png')
  await expect(field.getByTestId('wk-bg-image-error')).toBeVisible()
  await card.getByTestId('wk-save-ui').click()
  await expect(card.getByTestId('wk-problem-ui')).toBeVisible()
  expect((await read(request, 'ui')).data.homeBackground.image).toBe('Pictures/sky.png')

  await field.getByTestId('wk-bg-clear').click()
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.homeBackground).toBeUndefined()
  expect((await read(request, 'ui')).data.x_mood).toBe('calm')
})

test('home background: the editor shows what the assistant wrote and keeps unknown keys inside the background', async ({ page, request }) => {
  await upload(request, '/LocalAI/ui.json', { homeBackground: { color: '#445566', x_future: 1 } })
  await page.goto('/app/account?tab=appearance')
  const card = page.getByTestId('wk-card-ui')
  await expect(card.getByTestId('wk-bg-color')).toHaveValue('#445566')
  await card.getByTestId('wk-bg-opacity').fill('0.5')
  await card.getByTestId('wk-save-ui').click()
  await expect.poll(async () => (await read(request, 'ui')).data.homeBackground?.opacity).toBe(0.5)
  expect((await read(request, 'ui')).data.homeBackground).toEqual({ color: '#445566', x_future: 1, opacity: 0.5 })
})
