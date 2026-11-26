import { test, expect } from './coverage-fixtures.js'
import { installStudioAddons } from './studio-addon.js'

// Talking Head runs one turn as four core calls. The spec answers each route
// the way LocalAI does and checks what the page sends and shows.

const MODELS = [
  { id: 'qwen-small', capabilities: ['FLAG_CHAT'], backend: 'llama-cpp' },
  { id: 'piper-amy', capabilities: ['FLAG_TTS'], backend: 'piper' },
  { id: 'whisper-tiny', capabilities: ['FLAG_TRANSCRIPT'], backend: 'whisper' },
  { id: 'avatarforcing', capabilities: ['FLAG_VIDEO'], backend: 'avatarforcing-cpp' },
  { id: 'wan-video', capabilities: ['FLAG_VIDEO'], backend: 'diffusers' },
]
// A 1x1 PNG: enough for the crop sheet to read and draw.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64')

async function setup(page, { models = MODELS, holdVideo = false } = {}) {
  await installStudioAddons(page, { ids: ['talking-head'] })
  const calls = []
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models } }))
  await page.route('**/api/pipeline-models', route => route.fulfill({
    json: [
      { name: 'voice-pipe', voice: 'amy', transcription: 'whisper-tiny', llm: 'qwen-small', tts: 'piper-amy' },
      { name: 's2s', transcription: 's2s', llm: 's2s', tts: 's2s', any_to_any: true },
    ],
  }))
  await page.route('**/v1/chat/completions', route => {
    calls.push({ path: 'chat', body: route.request().postDataJSON() })
    return route.fulfill({
      contentType: 'text/event-stream',
      body: 'data: {"choices":[{"delta":{"content":"Hello, "}}]}\n\ndata: {"choices":[{"delta":{"content":"**friend**."}}]}\n\ndata: [DONE]\n\n',
    })
  })
  await page.route('**/tts', route => {
    calls.push({ path: 'tts', body: route.request().postDataJSON() })
    return route.fulfill({ contentType: 'audio/wav', body: Buffer.from('RIFF0000WAVE') })
  })
  await page.route('**/video', async route => {
    calls.push({ path: 'video', body: route.request().postDataJSON() })
    if (holdVideo) return
    return route.fulfill({ json: { data: [{ b64_json: Buffer.from('not really an mp4').toString('base64') }] } })
  })
  return calls
}

async function addPortrait(page) {
  await page.goto('/app/addons/talking-head/talking-head')
  await page.getByTestId('head-upload-input').setInputFiles({ name: 'me.png', mimeType: 'image/png', buffer: PNG })
  await expect(page.getByTestId('head-crop')).toBeVisible()
  await page.getByTestId('head-crop-save').click()
  await expect(page.getByTestId('head-crop')).toHaveCount(0)
}

test.describe('Talking Head', () => {
  test('without an avatar model the page says what to install and links the gallery', async ({ page }) => {
    await setup(page, { models: MODELS.filter(m => m.backend !== 'avatarforcing-cpp') })
    await page.goto('/app/addons/talking-head/talking-head')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Talking Head')
    const card = page.getByTestId('head-missing')
    await expect(card).toContainText('avatarforcing-cpp backend')
    await expect(card).toContainText('CC BY-NC 4.0')
    await expect(page.getByTestId('head-text')).toHaveCount(0)
  })

  test('a typed message runs reply, speech and video, then lists the clip', async ({ page }) => {
    const calls = await setup(page)
    await addPortrait(page)
    await expect(page.getByTestId('head-status')).toContainText('Ready')
    await page.getByTestId('head-text').fill('Hi there')
    await page.getByTestId('head-send').click()

    await expect(page.getByTestId('head-replay')).toBeVisible()
    const stages = page.getByTestId('head-stages').locator('li')
    await expect(stages).toHaveCount(3)
    for (const stage of ['chat', 'speech', 'video']) await expect(stages.and(page.locator(`[data-stage="${stage}"]`))).toHaveAttribute('data-state', 'done')
    await expect(page.getByTestId('head-transcript')).toContainText('Hi there')
    await expect(page.getByTestId('head-transcript')).toContainText('Hello, friend.')
    await expect(page.getByTestId('head-clip')).toBeAttached()

    expect(calls.map(c => c.path)).toEqual(['chat', 'tts', 'video'])
    expect(calls[0].body.model).toBe('qwen-small')
    expect(calls[0].body.stream).toBe(true)
    expect(calls[1].body).toEqual({ model: 'piper-amy', input: 'Hello, friend.' })
    expect(calls[2].body.model).toBe('avatarforcing')
    expect(calls[2].body.start_image).toMatch(/^\/9j\//) // a JPEG, base64
    expect(calls[2].body.audio).toBe(Buffer.from('RIFF0000WAVE').toString('base64'))
    expect(calls[2].body.params).toEqual({ nfe: '6' })
    expect(calls[2].body.response_format).toBe('b64_json')
  })

  test('"Say this" speaks the typed text with no chat call', async ({ page }) => {
    const calls = await setup(page)
    await addPortrait(page)
    await page.getByTestId('head-text').fill('Good morning')
    await page.getByTestId('head-say').click()
    await expect(page.getByTestId('head-replay')).toBeVisible()
    expect(calls.map(c => c.path)).toEqual(['tts', 'video'])
    await expect(page.getByTestId('head-transcript')).toContainText('Said')
  })

  test('Cancel stops a turn in flight and marks it in the conversation', async ({ page }) => {
    await setup(page, { holdVideo: true })
    await addPortrait(page)
    await page.getByTestId('head-text').fill('Hi')
    await page.getByTestId('head-send').click()
    await expect(page.getByTestId('head-stages').locator('[data-stage="video"]')).toHaveAttribute('data-state', 'running')
    await expect(page.getByTestId('head-status')).toContainText('Making the clip')
    await page.getByTestId('head-cancel').click()
    await expect(page.getByTestId('head-stages').locator('[data-stage="video"]')).toHaveAttribute('data-state', 'cancelled')
    await expect(page.getByTestId('head-transcript')).toContainText('Turn cancelled')
    await expect(page.getByTestId('head-send')).toBeVisible()
  })

  test('a server error names the stage that failed', async ({ page }) => {
    await setup(page)
    await page.route('**/tts', route => route.fulfill({ status: 500, json: { error: { message: 'voice not found' } } }))
    await addPortrait(page)
    await page.getByTestId('head-text').fill('Hi')
    await page.getByTestId('head-send').click()
    await expect(page.getByTestId('head-status')).toHaveAttribute('data-state', 'error')
    await expect(page.getByTestId('head-status')).toContainText('Speech failed: voice not found')
  })

  test('the settings pick a pipeline, list only talking-head models, and keep the add-on styles', async ({ page }) => {
    const calls = await setup(page)
    await addPortrait(page)
    await page.getByTestId('head-settings-button').click()
    const sheet = page.getByTestId('head-settings')
    await expect(sheet).toBeVisible()
    // The sheet is drawn outside the page; it still carries the add-on scope.
    await expect(page.locator('[data-addon="talking-head"] [data-testid="head-settings"]')).toHaveCount(1)
    await expect(sheet.getByLabel('Avatar model').locator('option')).toHaveText(['avatarforcing'])
    await expect(sheet.getByLabel('Pipeline').locator('option')).toHaveText(['None: pick each model', 'voice-pipe'])
    await sheet.getByLabel('Pipeline').selectOption('voice-pipe')
    await expect(sheet.getByLabel('Voice', { exact: true })).toHaveValue('amy')
    await sheet.getByLabel('Quality (nfe)').fill('4')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('head-avatar-chip')).toContainText('avatarforcing')
    await page.getByTestId('head-text').fill('Hi')
    await page.getByTestId('head-send').click()
    await expect(page.getByTestId('head-replay')).toBeVisible()
    expect(calls.find(c => c.path === 'tts').body.voice).toBe('amy')
    expect(calls.find(c => c.path === 'video').body.params).toEqual({ nfe: '4' })
  })

  test('a link from a video model that is not a talking-head model says so', async ({ page }) => {
    await setup(page)
    await page.goto('/app/addons/talking-head/talking-head?model=wan-video')
    await expect(page.getByText('wan-video is not a talking-head model')).toBeVisible()
  })
})
