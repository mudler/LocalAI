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

  test.describe('setup', () => {
    const GALLERY = {
      avatarforcing: { license: 'cc-by-nc-4.0', size: 1.2e9 },
      'llama-3.2-1b-instruct:q4_k_m': { size: 1.3e9 },
      'whisper-tiny-en-q5_1': { size: 31e6 },
      'vits-piper-en_US-amy-sherpa': { size: 79e6 },
      'silero-vad': { size: 2e6 },
    }

    // A server with nothing installed: the setup installs every part, each
    // job reports 40 % once and then finishes, and the listing then shows it.
    async function setupServer(page, { failJob, refuse } = {}) {
      const state = { installed: new Set(), installs: [], imports: [], cancels: [], jobs: {} }
      const capabilities = () => [...state.installed].map(name => ({
        id: name,
        backend: name === 'avatarforcing' ? 'avatarforcing-cpp' : 'x',
        capabilities: [{ avatarforcing: 'FLAG_VIDEO', 'llama-3.2-1b-instruct:q4_k_m': 'FLAG_CHAT', 'whisper-tiny-en-q5_1': 'FLAG_TRANSCRIPT', 'vits-piper-en_US-amy-sherpa': 'FLAG_TTS', 'silero-vad': 'FLAG_VAD' }[name]],
      }))
      await installStudioAddons(page, { ids: ['talking-head'] })
      await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: capabilities() } }))
      await page.route('**/api/pipeline-models', route => route.fulfill({ json: state.imports.map(c => ({ name: c.name, ...c.pipeline })) }))
      await page.route(/\/api\/models\?/, route => {
        if (refuse) return route.fulfill({ status: 403, json: { error: { message: 'forbidden' } } })
        const term = new URL(route.request().url()).searchParams.get('term')
        return route.fulfill({ json: { models: Object.keys(GALLERY).filter(n => n.includes(term)).map(n => ({ id: `localai@${n}`, name: n, license: GALLERY[n].license || '', installed: state.installed.has(n) })) } })
      })
      await page.route('**/api/models/estimate/**', route => {
        const name = decodeURIComponent(route.request().url().split('/').pop()).split('@')[1]
        return route.fulfill({ json: { sizeBytes: GALLERY[name].size } })
      })
      await page.route('**/api/models/install/**', route => {
        const name = decodeURIComponent(route.request().url().split('/').pop()).split('@')[1]
        const id = `job-${name}`
        state.installs.push(name)
        state.jobs[id] = { name, polls: 0 }
        return route.fulfill({ json: { jobID: id } })
      })
      await page.route('**/models/jobs/**', route => {
        const id = decodeURIComponent(route.request().url().split('/').pop())
        const job = state.jobs[id]
        job.polls++
        if (failJob === job.name && !state.failedOnce) { state.failedOnce = true; return route.fulfill({ json: { processed: true, error: 'download failed: 404' } }) }
        if (job.polls === 1) return route.fulfill({ json: { progress: 40, processed: false } })
        state.installed.add(job.name)
        return route.fulfill({ json: { progress: 100, processed: true } })
      })
      await page.route('**/api/operations/*/cancel', route => { state.cancels.push(route.request().url()); return route.fulfill({ json: { success: true } }) })
      await page.route('**/models/import', route => {
        const body = route.request().postDataJSON()
        state.imports.push(body)
        state.installed.add(body.name)
        return route.fulfill({ json: { success: true } })
      })
      return state
    }

    test('the setup shows what it downloads and the licence, installs each part and selects them', async ({ page }) => {
      const state = await setupServer(page)
      await page.goto('/app/addons/talking-head/talking-head')
      await page.getByTestId('head-setup-button').click()
      const sheet = page.getByTestId('head-setup')
      await expect(sheet.getByTestId('head-setup-plan').locator('li')).toHaveCount(6)
      await expect(sheet.getByTestId('head-setup-total')).toHaveText('Models to download: 5. Size: 2.6 GB.')
      await expect(sheet.getByTestId('head-setup-avatar')).toContainText('cc-by-nc-4.0')
      await expect(sheet.getByTestId('head-setup-licence')).toContainText('CC BY-NC 4.0')
      // The install waits for the licence answer.
      await expect(sheet.getByTestId('head-setup-start')).toBeDisabled()
      await sheet.getByTestId('head-setup-agree').check()
      await sheet.getByTestId('head-setup-start').click()
      await expect(sheet.getByTestId('head-setup-done')).toBeVisible({ timeout: 20000 })
      expect(state.installs).toEqual(['avatarforcing', 'llama-3.2-1b-instruct:q4_k_m', 'whisper-tiny-en-q5_1', 'vits-piper-en_US-amy-sherpa', 'silero-vad'])
      expect(state.imports).toEqual([{ name: 'talking-head', pipeline: { transcription: 'whisper-tiny-en-q5_1', llm: 'llama-3.2-1b-instruct:q4_k_m', tts: 'vits-piper-en_US-amy-sherpa', vad: 'silero-vad' } }])
      for (const part of ['avatar', 'chat', 'stt', 'tts', 'vad', 'pipeline']) await expect(sheet.getByTestId(`head-setup-${part}`)).toHaveAttribute('data-state', 'done')
      await sheet.getByTestId('head-setup-close').click()
      // The page now uses what it installed.
      await expect(page.getByTestId('head-missing')).toHaveCount(0)
      await expect(page.getByTestId('head-avatar-chip')).toContainText('avatarforcing')
      await expect(page.getByRole('button', { name: /talking-head/ })).toBeVisible()
    })

    test('a changed pick is installed instead, and installed parts are skipped', async ({ page }) => {
      const state = await setupServer(page)
      state.installed.add('vits-piper-en_US-amy-sherpa')
      await page.goto('/app/addons/talking-head/talking-head')
      await page.getByTestId('head-setup-button').click()
      const sheet = page.getByTestId('head-setup')
      await sheet.getByLabel('Voice detection').selectOption('')
      await expect(sheet.getByTestId('head-setup-tts')).toHaveAttribute('data-state', 'skipped')
      await expect(sheet.getByTestId('head-setup-vad')).toHaveCount(0)
      await expect(sheet.getByTestId('head-setup-total')).toHaveText('Models to download: 3. Size: 2.5 GB.')
      await sheet.getByTestId('head-setup-agree').check()
      await sheet.getByTestId('head-setup-start').click()
      await expect(sheet.getByTestId('head-setup-done')).toBeVisible({ timeout: 20000 })
      expect(state.installs).toEqual(['avatarforcing', 'llama-3.2-1b-instruct:q4_k_m', 'whisper-tiny-en-q5_1'])
      expect(state.imports[0].pipeline.vad).toBeUndefined()
    })

    test('a failed part shows its error, and Retry installs only what is missing', async ({ page }) => {
      const state = await setupServer(page, { failJob: 'whisper-tiny-en-q5_1' })
      await page.goto('/app/addons/talking-head/talking-head')
      await page.getByTestId('head-setup-button').click()
      const sheet = page.getByTestId('head-setup')
      await sheet.getByTestId('head-setup-agree').check()
      await sheet.getByTestId('head-setup-start').click()
      await expect(sheet.getByTestId('head-setup-stt')).toHaveAttribute('data-state', 'failed', { timeout: 20000 })
      await expect(sheet.getByTestId('head-setup-stt')).toContainText('download failed: 404')
      await expect(sheet.getByTestId('head-setup-start')).toHaveText(/Retry/)
      await sheet.getByTestId('head-setup-start').click()
      await expect(sheet.getByTestId('head-setup-done')).toBeVisible({ timeout: 20000 })
      expect(state.installs).toEqual(['avatarforcing', 'llama-3.2-1b-instruct:q4_k_m', 'whisper-tiny-en-q5_1', 'whisper-tiny-en-q5_1', 'vits-piper-en_US-amy-sherpa', 'silero-vad'])
    })

    test('a person the server refuses is told that only an administrator can install', async ({ page }) => {
      await setupServer(page, { refuse: true })
      await page.goto('/app/addons/talking-head/talking-head')
      await page.getByTestId('head-setup-button').click()
      await expect(page.getByTestId('head-setup-error')).toHaveText(/only an administrator can install models/)
      await expect(page.getByTestId('head-setup-start')).toBeDisabled()
    })
  })
})
