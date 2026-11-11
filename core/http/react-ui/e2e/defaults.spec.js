import { test, expect } from './coverage-fixtures.js'
import { holdHomeFiles, unlockHomeFiles } from './home-files-lock.js'

// Account > Defaults: LocalAI/defaults.json, the model for each kind of work.
// Against the real test server and its real files; only the list of installed
// models is stubbed, because the test server has one mock model.

test.describe.configure({ mode: 'serial', timeout: 60_000 })

holdHomeFiles(test)

const FILES = ['/LocalAI/defaults.json', '/LocalAI/ui.json']

const MODELS = [
  { id: 'qwen3-8b-instruct', capabilities: ['FLAG_CHAT', 'FLAG_VISION'], backend: 'llama-cpp' },
  { id: 'llama-3.2-3b', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'flux.1-schnell', capabilities: ['FLAG_IMAGE'], backend: 'stablediffusion' },
  { id: 'sd-3.5-medium', capabilities: ['FLAG_IMAGE'], backend: 'stablediffusion' },
  { id: 'kokoro-82m', capabilities: ['FLAG_TTS'], backend: 'kokoro' },
  { id: 'whisper-large-v3-turbo', capabilities: ['FLAG_TRANSCRIPT'], backend: 'whisper' },
  { id: 'bge-m3', capabilities: ['FLAG_EMBEDDINGS'], backend: 'llama-cpp' },
  { id: 'bge-reranker-v2-m3', capabilities: ['FLAG_RERANK'], backend: 'llama-cpp' },
  { id: 'off-image', capabilities: ['FLAG_IMAGE'], backend: 'stablediffusion', disabled: true },
]

async function clean(request) {
  for (const p of FILES) await request.delete(`/api/chats/files?path=${encodeURIComponent(p)}`)
}
const read = (request, name) => request.get(`/api/chats/wellknown/${name}`).then(r => r.json())
const write = (request, name, values) => request.put(`/api/chats/wellknown/${name}`, { data: { values, replace: true } })
const upload = (request, path, body) => request.put(`/api/chats/files/content?path=${encodeURIComponent(path)}&overwrite=1`, {
  data: typeof body === 'string' ? body : JSON.stringify(body), headers: { 'Content-Type': 'application/octet-stream' },
})

async function openDefaults(page, models = MODELS) {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models.map(m => ({ pinned: false, disabled: false, ...m })) } }))
  await page.goto('/app/account?tab=defaults')
  await expect(page.getByTestId('account-defaults')).toBeVisible()
  await expect(page.getByTestId('df-row-chat')).toBeVisible()
}
const row = (page, key) => page.getByTestId(`df-row-${key}`)

test.beforeEach(async ({ request }) => { await clean(request) })
test.afterAll(async ({ request }) => { await clean(request) })
test.afterAll(() => unlockHomeFiles())

test('defaults: one row per kind of work, with only the models that can do it', async ({ page }) => {
  await openDefaults(page)
  for (const key of ['chat', 'image', 'tts', 'stt', 'embeddings', 'video', 'rerank', 'vision']) await expect(row(page, key)).toBeVisible()
  // The picker of images lists the two enabled image models, not the disabled one or the chat models.
  const options = await page.getByTestId('df-select-image').locator('option').allTextContents()
  expect(options).toEqual(['Automatic (first installed)', 'flux.1-schnell', 'sd-3.5-medium'])
  // Nothing is installed for video: the row offers to install one.
  await expect(row(page, 'video')).toHaveAttribute('data-state', 'none')
  await expect(page.getByTestId('df-install-video')).toHaveAttribute('href', /\/app\/models/)
  // The footer says where the choices are kept, and nothing is saved yet.
  await expect(page.getByTestId('wk-stored-defaults')).toContainText('Stored in LocalAI/defaults.json')
  expect((await page.request.get('/api/chats/wellknown/defaults').then(r => r.json())).exists).toBe(false)
})

test('defaults: a pick is saved at once to defaults.json, and keeps the keys it does not show', async ({ page, request }) => {
  await upload(request, '/LocalAI/defaults.json', { note: 'mine', tts: 'kokoro-82m' })
  await openDefaults(page)
  await expect(page.getByTestId('df-select-tts')).toHaveValue('kokoro-82m')

  await page.getByTestId('df-select-image').selectOption('flux.1-schnell')
  await expect(page.getByTestId('df-saved')).toBeVisible()
  let saved = (await read(request, 'defaults')).data
  expect(saved).toEqual({ note: 'mine', tts: 'kokoro-82m', image: 'flux.1-schnell' })
  // The backend that runs the model is shown, and the file preview follows the pick.
  await expect(page.getByTestId('df-backend-image')).toHaveText('stablediffusion')
  await expect(page.getByTestId('df-json')).toContainText('"image": "flux.1-schnell"')

  // Automatic removes the key.
  await page.getByTestId('df-select-tts').selectOption('')
  await expect.poll(async () => (await read(request, 'defaults')).data.tts).toBeUndefined()
  saved = (await read(request, 'defaults')).data
  expect(saved).toEqual({ note: 'mine', image: 'flux.1-schnell' })
})

test('defaults: Ask each time saves ask, greys the picker, and Default brings the model back', async ({ page, request }) => {
  await write(request, 'defaults', { image: 'sd-3.5-medium' })
  await openDefaults(page)
  await page.getByTestId('df-mode-image-ask').click()
  await expect.poll(async () => (await read(request, 'defaults')).data.image).toBe('ask')
  await expect(row(page, 'image')).toHaveAttribute('data-state', 'ask')
  await expect(page.getByTestId('df-select-image')).toBeDisabled()

  await page.getByTestId('df-mode-image-default').click()
  await expect.poll(async () => (await read(request, 'defaults')).data.image).toBe('sd-3.5-medium')
  await expect(page.getByTestId('df-select-image')).toBeEnabled()
})

test('defaults: a model that is not installed any more is marked, and nothing else is switched in', async ({ page, request }) => {
  await write(request, 'defaults', { image: 'gone-model', tts: 'kokoro-82m' })
  await openDefaults(page)
  await expect(row(page, 'image')).toHaveAttribute('data-state', 'missing')
  await expect(page.getByTestId('df-missing-image')).toContainText('not installed any more')
  await expect(page.getByTestId('df-select-image')).toHaveValue('gone-model')
  // Opening the page changed nothing in the file.
  expect((await read(request, 'defaults')).data).toEqual({ image: 'gone-model', tts: 'kokoro-82m' })
})

test('defaults: the old defaultModel of ui.json shows for chat and is never written by this editor', async ({ page, request }) => {
  await write(request, 'ui', { theme: 'dark', defaultModel: 'llama-3.2-3b' })
  await openDefaults(page)
  await expect(page.getByTestId('df-select-chat')).toHaveValue('llama-3.2-3b')
  await expect(page.getByTestId('df-old-chat')).toBeVisible()
  const before = (await read(request, 'ui')).version

  await page.getByTestId('df-select-chat').selectOption('qwen3-8b-instruct')
  await expect.poll(async () => (await read(request, 'defaults')).data.chat).toBe('qwen3-8b-instruct')
  const ui = await read(request, 'ui')
  expect(ui.data).toEqual({ theme: 'dark', defaultModel: 'llama-3.2-3b' })
  expect(ui.version).toBe(before)
  await expect(page.getByTestId('df-old-chat')).toHaveCount(0)
})

test('defaults: Appearance no longer offers a default model', async ({ page }) => {
  await page.goto('/app/account?tab=appearance')
  await expect(page.getByTestId('wk-card-ui')).toBeVisible()
  await expect(page.getByTestId('wk-field-ui-theme')).toBeVisible()
  await expect(page.getByTestId('wk-field-ui-defaultModel')).toHaveCount(0)
})

test('defaults: a change by the assistant shows at once, and a broken file keeps its last good version', async ({ page, request }) => {
  await write(request, 'defaults', { image: 'flux.1-schnell' })
  await openDefaults(page)
  await expect(page.getByTestId('df-select-image')).toHaveValue('flux.1-schnell')

  // The assistant changes the file with a plain file write.
  await upload(request, '/LocalAI/defaults.json', { image: 'sd-3.5-medium' })
  await expect(page.getByTestId('df-select-image')).toHaveValue('sd-3.5-medium')

  // It writes a value that breaks the schema: the page keeps the last good one and says so.
  await upload(request, '/LocalAI/defaults.json', '{"image": 7}')
  await expect(page.getByTestId('wk-invalid-defaults')).toContainText('last valid version')
  await expect(page.getByTestId('df-select-image')).toHaveValue('sd-3.5-medium')
})

test('defaults: Source edits the whole file with the same validation, and Revert goes back one version', async ({ page, request }) => {
  await write(request, 'defaults', { image: 'flux.1-schnell' })
  await write(request, 'defaults', { image: 'sd-3.5-medium' })
  await openDefaults(page)

  await page.getByTestId('wk-stored-source-defaults').click()
  await page.getByTestId('df-source-text').fill('{"image": 42}')
  await page.getByTestId('df-source-save').click()
  await expect(page.getByTestId('wk-problem-defaults')).toContainText('Some values are not valid.')
  expect((await read(request, 'defaults')).data.image).toBe('sd-3.5-medium')

  await page.getByTestId('df-source-text').fill('{"image": "flux.1-schnell", "stt": "ask"}')
  await page.getByTestId('df-source-save').click()
  await expect.poll(async () => (await read(request, 'defaults')).data).toEqual({ image: 'flux.1-schnell', stt: 'ask' })

  await page.getByTestId('wk-stored-revert-defaults').click()
  await expect.poll(async () => (await read(request, 'defaults')).data).toEqual({ image: 'sd-3.5-medium' })
})

test('defaults: the server refuses a value that is not a model name, ask or null', async ({ request }) => {
  const bad = await request.put('/api/chats/wellknown/defaults', { data: { values: { image: 7 } } })
  expect(bad.status()).toBe(400)
  const blank = await request.put('/api/chats/wellknown/defaults', { data: { values: { tts: '  ' } } })
  expect(blank.status()).toBe(400)
  expect((await blank.json()).errors.join(' ')).toContain('installed model')
  const ok = await request.put('/api/chats/wellknown/defaults', { data: { values: { image: 'ask', tts: 'kokoro-82m', video: null } } })
  expect(ok.status()).toBe(200)
  expect((await ok.json()).data).toEqual({ image: 'ask', tts: 'kokoro-82m' })
  const schema = await request.get('/api/chats/wellknown/defaults/schema').then(r => r.json())
  expect(Object.keys(schema.properties).sort()).toEqual(['chat', 'embeddings', 'image', 'rerank', 'stt', 'tts', 'video', 'vision'])
})

test('defaults: the tab is a phone layout without sideways scroll', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await openDefaults(page)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
  await expect(page.getByTestId('df-select-image')).toBeVisible()
})

// The picker of a new chat on Home starts as defaults.json says. DEFAULTS_SHOTS
// names a folder for screenshots.
const shots = process.env.DEFAULTS_SHOTS || ''
async function homeWith(page, { theme = 'light', last = '' } = {}) {
  await page.addInitScript(([t, m]) => {
    try {
      localStorage.setItem('localai-theme', t)
      if (m) localStorage.setItem('localai_last_model:FLAG_CHAT', m)
      else localStorage.removeItem('localai_last_model:FLAG_CHAT')
    } catch { /* private mode */ }
  }, [theme, last])
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: MODELS.map(m => ({ pinned: false, disabled: false, ...m })) } }))
  await page.goto('/app')
  await expect(page.getByTestId('home-model-chip')).toBeVisible()
}

for (const theme of ['light', 'dark']) {
  test(`defaults: with ask, a new chat on Home chooses no model and says so (${theme})`, async ({ page, request }) => {
    expect((await write(request, 'defaults', { chat: 'ask' })).ok()).toBeTruthy()
    await homeWith(page, { theme, last: 'llama-3.2-3b' })
    const chip = page.getByTestId('home-model-chip')
    await expect(chip.getByTestId('model-picker-note')).toHaveText('Choose a model')
    await expect(chip).toHaveAttribute('data-note', 'ask')
    await expect(chip).not.toContainText('llama-3.2-3b')
    if (shots) await page.screenshot({ path: `${shots}/picker-ask-${theme}.png` })
    // A pick is the person's: the note goes and the model stays.
    await chip.click()
    await page.getByRole('option', { name: /qwen3-8b-instruct/ }).click()
    await expect(chip).toContainText('qwen3-8b-instruct')
    await expect(chip.getByTestId('model-picker-note')).toHaveCount(0)
  })

  test(`defaults: a default that is not installed is named, and no other model is switched in (${theme})`, async ({ page, request }) => {
    expect((await write(request, 'defaults', { chat: 'gone-model-7b' })).ok()).toBeTruthy()
    await homeWith(page, { theme, last: 'llama-3.2-3b' })
    const chip = page.getByTestId('home-model-chip')
    await expect(chip.getByTestId('model-picker-note')).toHaveText('Your default gone-model-7b is not installed. Choose a model.')
    await expect(chip).toHaveAttribute('data-note', 'missing')
    await expect(chip).not.toContainText('llama-3.2-3b')
    if (shots) await page.screenshot({ path: `${shots}/picker-missing-${theme}.png` })
  })
}

test('defaults: with no chat default, a new chat starts with the last model used, as before', async ({ page }) => {
  await homeWith(page, { last: 'llama-3.2-3b' })
  const chip = page.getByTestId('home-model-chip')
  await expect(chip).toContainText('llama-3.2-3b')
  await expect(chip.getByTestId('model-picker-note')).toHaveCount(0)
})

test('defaults: an installed chat default is what a new chat starts with', async ({ page, request }) => {
  expect((await write(request, 'defaults', { chat: 'qwen3-8b-instruct' })).ok()).toBeTruthy()
  await homeWith(page, { last: 'llama-3.2-3b' })
  await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
})
