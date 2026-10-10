import { test, expect } from './coverage-fixtures.js'

const VOICE_ID = '00000000-0000-0000-0000-000000000001'

function pcmWav(seconds = 2) {
  const sampleRate = 16000
  const dataSize = sampleRate * seconds * 2
  const buffer = Buffer.alloc(44 + dataSize)
  buffer.write('RIFF', 0)
  buffer.writeUInt32LE(36 + dataSize, 4)
  buffer.write('WAVEfmt ', 8)
  buffer.writeUInt32LE(16, 16)
  buffer.writeUInt16LE(1, 20)
  buffer.writeUInt16LE(1, 22)
  buffer.writeUInt32LE(sampleRate, 24)
  buffer.writeUInt32LE(sampleRate * 2, 28)
  buffer.writeUInt16LE(2, 32)
  buffer.writeUInt16LE(16, 34)
  buffer.write('data', 36)
  buffer.writeUInt32LE(dataSize, 40)
  return buffer
}

const profile = {
  id: VOICE_ID,
  name: 'Documentary narrator',
  description: 'Measured and clear',
  language: 'en-US',
  transcript: 'The exact words spoken in this reference.',
  voice: `localai://voice-profiles/${VOICE_ID}`,
  consent_confirmed_at: '2026-07-01T12:00:00Z',
  created_at: '2026-07-01T12:00:00Z',
  updated_at: '2026-07-01T12:00:00Z',
  audio: { duration_ms: 2000, sample_rate: 16000, channels: 1, bit_depth: 16, size_bytes: 64044, mime_type: 'audio/wav' },
}

async function mockVoiceAPIs(page) {
  const state = { createdMultipart: null }
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false } }))
  await page.route('**/api/models/capabilities', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: [
      { id: 'qwen-base', capabilities: ['FLAG_TTS'], backend: 'qwen3-tts-cpp', voice_cloning: { reference_transcript_required: true, accepted_audio_formats: ['audio/wav'] } },
      { id: 'piper-default', capabilities: ['FLAG_TTS'], backend: 'piper' },
    ] }),
  }))
  await page.route('**/api/voice-profiles', async route => {
    if (route.request().method() === 'POST') {
      state.createdMultipart = route.request().postDataBuffer()
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify(profile) })
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [profile] }) })
  })
  await page.route(`**/api/voice-profiles/${VOICE_ID}/audio`, route => route.fulfill({
    status: 200,
    contentType: 'audio/wav',
    body: pcmWav(),
  }))
  return state
}

test.describe('Speech voices', () => {
  let apiState

  test.beforeEach(async ({ page }) => {
    apiState = await mockVoiceAPIs(page)
  })

  test('lists the voices and opens one in a sheet', async ({ page }) => {
    await page.goto('/app/voice-library')
    await expect(page.getByRole('heading', { name: 'Voices', level: 1 })).toBeVisible()
    await expect(page.getByRole('heading', { name: /Speech voices/i, level: 2 })).toBeVisible()
    await expect(page.locator('.idn-tabs').getByRole('tab', { name: /Speech voices/ })).toHaveAttribute('aria-current', 'page')
    const row = page.locator('.voice-row', { hasText: 'Documentary narrator' })
    await expect(row).toBeVisible()
    // Nothing opens by itself: the detail is a sheet the person asks for.
    await expect(page.getByTestId('speech-voice-sheet')).toHaveCount(0)
    await row.click()
    await expect(page.getByTestId('speech-voice-sheet')).toBeVisible()
    await expect(page.locator('.voice-library-detail')).toContainText('The exact words spoken in this reference.')
    await expect(page.locator('.voice-library-detail')).toContainText('Consent confirmed')
    await expect(page.getByRole('button', { name: /Use in Text to Speech/i })).toBeEnabled()
  })

  test('shows inline API usage and installed model compatibility', async ({ page }) => {
    await page.goto(`/app/voice-library?selected=${VOICE_ID}`)
    await page.getByText('API usage and compatible models').click()
    const apiHelp = page.locator('.voice-detail__api')
    await expect(apiHelp).toContainText('qwen-base')
    await expect(apiHelp).toContainText('qwen3-tts-cpp')
    await expect(apiHelp.locator('code')).toContainText('/v1/audio/speech')
    await expect(apiHelp.locator('code')).toContainText(`localai://voice-profiles/${VOICE_ID}`)
  })

  test('offers server-declared gallery models when none are installed', async ({ page }) => {
    let galleryCapability = null
    let installedModel = null
    await page.route('**/api/models/capabilities', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: [] }),
    }))
    await page.route('**/api/models?**', route => {
      galleryCapability = new URL(route.request().url()).searchParams.get('capability')
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ models: [{
          id: 'localai@omnivoice-cpp',
          name: 'omnivoice-cpp',
          backend: 'omnivoice-cpp',
          installed: false,
          voice_cloning: { reference_transcript_required: true, accepted_audio_formats: ['audio/wav'] },
        }] }),
      })
    })
    await page.route('**/api/models/install/**', route => {
      installedModel = decodeURIComponent(route.request().url().split('/').pop())
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ jobID: 'voice-install' }) })
    })

    await page.goto(`/app/voice-library?selected=${VOICE_ID}`)
    await expect(page.getByRole('heading', { name: 'Install a voice-cloning model' })).toBeVisible()
    await expect(page.locator('.voice-detail__model-list')).toContainText('omnivoice-cpp')
    expect(galleryCapability).toBe('voice_cloning')
    await page.locator('.voice-detail__model-list').getByRole('button', { name: 'Install' }).click()
    await expect.poll(() => installedModel).toBe('localai@omnivoice-cpp')
    await expect(page.getByText(/Installing omnivoice-cpp/)).toBeVisible()
  })

  test('keeps the Build tab bar compact and scrollable on small screens', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/app/voice-library')
    const bar = page.locator('.dk-hubtabs')
    await expect(bar).toBeVisible()
    expect((await bar.boundingBox()).width).toBeLessThanOrEqual(390)
    const voices = bar.locator('[data-hub-tab="voices"]')
    await voices.scrollIntoViewIfNeeded()
    await expect(voices).toBeInViewport()
    await expect(voices).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.idn-tabs').getByRole('tab', { name: /Speech voices/ })).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('.hub-subnav')).toHaveCount(0)
  })

  test('passes the stable voice URI from the library into TTS', async ({ page }) => {
    let ttsBody = null
    await page.route('**/tts', async route => {
      ttsBody = route.request().postDataJSON()
      await route.fulfill({
        status: 200,
        contentType: 'audio/wav',
        headers: { 'Content-Disposition': 'attachment; filename="speech.wav"' },
        body: pcmWav(1),
      })
    })

    await page.goto(`/app/tts?voice=${VOICE_ID}`)
    await expect(page.locator('#tts-voice')).toHaveValue(VOICE_ID)
    await page.getByPlaceholder('Enter text to synthesize...').fill('Hello from the saved voice.')
    await page.getByRole('button', { name: /Generate$/ }).click()
    await expect.poll(() => ttsBody?.voice).toBe(`localai://voice-profiles/${VOICE_ID}`)
    expect(ttsBody.model).toBe('qwen-base')
  })

  test('sends trimmed speech instructions and omits blank instructions', async ({ page }) => {
    const ttsBodies = []
    await page.route('**/tts', async route => {
      if (route.request().method() !== 'POST') {
        await route.fallback()
        return
      }
      ttsBodies.push(route.request().postDataJSON())
      await route.fulfill({
        status: 200,
        contentType: 'audio/wav',
        headers: { 'Content-Disposition': 'attachment; filename="speech.wav"' },
        body: pcmWav(1),
      })
    })

    await page.goto('/app/tts')
    await page.getByPlaceholder('Enter text to synthesize...').fill('Read this sentence.')
    // Delivery instructions sit in the Advanced fold of the workspace.
    await page.getByRole('button', { name: /^Instructions/ }).click()
    await page.getByLabel('Instructions').fill('  Speak slowly and warmly.  ')
    await page.getByRole('button', { name: /Generate$/ }).click()
    await expect.poll(() => ttsBodies.length).toBe(1)
    expect(ttsBodies[0].instructions).toBe('Speak slowly and warmly.')

    await expect(page.getByRole('button', { name: /Generate$/ })).toBeEnabled()
    await page.getByLabel('Instructions').fill('   \n  ')
    await page.getByRole('button', { name: /Generate$/ }).click()
    await expect.poll(() => ttsBodies.length).toBe(2)
    expect(ttsBodies[1]).not.toHaveProperty('instructions')
  })

  test('normalizes an upload and creates a consented profile', async ({ page }) => {
    await page.goto('/app/voice-library/new')
    await page.locator('#voice-profile-audio-file').setInputFiles({
      name: 'reference.wav',
      mimeType: 'audio/wav',
      buffer: pcmWav(2),
    })
    await expect(page.getByText('Normalized reference')).toBeVisible()
    await page.locator('#voice-profile-name').fill('Documentary narrator')
    await page.locator('#voice-profile-transcript').fill('The exact words spoken in this reference.')
    await page.getByText('I confirm that this voice may be cloned').click()
    await page.getByRole('button', { name: /Save voice/i }).click()
    await expect(page).toHaveURL(new RegExp(`/app/voice-library\\?selected=${VOICE_ID}$`))

    const wavOffset = apiState.createdMultipart.indexOf(Buffer.from('RIFF'))
    expect(wavOffset).toBeGreaterThanOrEqual(0)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 22)).toBe(1)
    expect(apiState.createdMultipart.readUInt32LE(wavOffset + 24)).toBe(24000)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 34)).toBe(16)
  })
})

test.describe('Voice design', () => {
  let apiState
  test.beforeEach(async ({ page }) => { apiState = await mockVoiceAPIs(page) })

  async function openDesign(page) {
    await page.goto('/app/voice-library/new')
    await page.getByRole('button', { name: 'Design a voice', exact: true }).click()
  }

  test('previews generated audio and saves its matching transcript', async ({ page }) => {
    let speechRequest
    await page.route('**/tts', route => {
      speechRequest = route.request().postDataJSON()
      return route.fulfill({ status: 200, contentType: 'audio/wav', body: pcmWav(2) })
    })
    await openDesign(page)
    const generate = page.getByRole('button', { name: 'Generate reference', exact: true })
    await expect(generate).toBeDisabled()
    await page.getByLabel('Voice instructions').fill('  A warm narrator.  ')
    await expect(generate).toBeDisabled()
    await page.getByLabel('Sample text').fill('  Welcome to the documentary.  ')
    await generate.click()
    await expect(page.getByText('Normalized reference')).toBeVisible()
    expect(speechRequest).toEqual({ model: 'qwen-base', input: 'Welcome to the documentary.', instructions: 'A warm narrator.' })
    await expect(page.getByLabel('Exact transcript')).toHaveValue(speechRequest.input)
    expect(apiState.createdMultipart).toBeNull()
    await page.getByLabel('Voice name', { exact: true }).fill('Designed narrator')
    await expect(page.getByRole('button', { name: 'Save voice', exact: true })).toBeDisabled()
    await page.getByText('I confirm that this voice may be cloned').click()
    await page.getByRole('button', { name: 'Save voice', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/app/voice-library\\?selected=${VOICE_ID}$`))
    expect(apiState.createdMultipart.toString()).toContain('name="transcript"\r\n\r\nWelcome to the documentary.')
    const wavOffset = apiState.createdMultipart.indexOf(Buffer.from('RIFF'))
    expect(wavOffset).toBeGreaterThanOrEqual(0)
    expect(apiState.createdMultipart.readUInt32LE(wavOffset + 24)).toBe(24000)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 22)).toBe(1)
    expect(apiState.createdMultipart.readUInt16LE(wavOffset + 34)).toBe(16)
  })

  test('requires regeneration after edits and disables save during generation', async ({ page }) => {
    let releaseSpeech
    await page.route('**/tts', async route => {
      await new Promise(resolve => { releaseSpeech = resolve })
      await route.fulfill({ status: 200, contentType: 'audio/wav', body: pcmWav(2) })
    })
    await openDesign(page)
    await page.getByLabel('Voice instructions').fill('Warm narrator')
    await page.getByLabel('Sample text').fill('First sample.')
    await page.getByLabel('Voice name', { exact: true }).fill('Narrator')
    await page.getByText('I confirm that this voice may be cloned').click()
    const generate = page.getByRole('button', { name: 'Generate reference', exact: true })
    const save = page.getByRole('button', { name: 'Save voice', exact: true })
    await generate.click()
    await expect.poll(() => !!releaseSpeech).toBe(true)
    await expect(save).toBeDisabled()
    await expect(page.getByLabel('Sample text')).toBeDisabled()
    releaseSpeech()
    await expect(save).toBeEnabled()
    await page.getByLabel('Sample text').fill('Second sample.')
    await expect(save).toBeDisabled()
    await expect(page.getByText('Normalized reference')).toBeHidden()
    releaseSpeech = null
    await generate.click()
    await expect.poll(() => !!releaseSpeech).toBe(true)
    releaseSpeech()
    await expect(save).toBeEnabled()
    await expect(page.getByLabel('Exact transcript')).toHaveValue('Second sample.')
    await page.getByRole('group', { name: 'Voice design model', exact: true }).getByRole('button').click()
    await page.getByRole('option', { name: 'piper-default', exact: true }).click()
    await expect(save).toBeDisabled()
    await expect(page.getByText('Normalized reference')).toBeHidden()
    releaseSpeech = null
    await generate.click()
    await expect.poll(() => !!releaseSpeech).toBe(true)
    releaseSpeech()
    await expect(save).toBeEnabled()
    await page.getByLabel('Voice instructions').fill('Bright narrator')
    await expect(save).toBeDisabled()
    await page.getByRole('button', { name: 'Upload or record', exact: true }).click()
    await expect(page.locator('#voice-profile-audio-file')).toBeAttached()
    await expect(save).toBeDisabled()
  })

  test('reports generation failures and permits retry', async ({ page }) => {
    let attempts = 0
    await page.route('**/tts', route => {
      attempts += 1
      return attempts === 1
        ? route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: { message: 'Voice design unavailable' } }) })
        : route.fulfill({ status: 200, contentType: 'audio/wav', body: pcmWav(2) })
    })
    await openDesign(page)
    await page.getByLabel('Voice instructions').fill('Warm narrator')
    await page.getByLabel('Sample text').fill('Try this sample.')
    await page.getByRole('button', { name: 'Generate reference', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('Voice design unavailable')
    await expect(page.getByRole('button', { name: 'Save voice', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: 'Generate reference', exact: true }).click()
    await expect(page.getByText('Normalized reference')).toBeVisible()
    await expect(page.getByLabel('Exact transcript')).toHaveValue('Try this sample.')
  })

  test('cannot generate without an installed TTS model', async ({ page }) => {
    await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [] } }))
    await openDesign(page)
    await page.getByLabel('Voice instructions').fill('Warm narrator')
    await page.getByLabel('Sample text').fill('Hello there.')
    await expect(page.getByRole('button', { name: 'Generate reference', exact: true })).toBeDisabled()
  })

  test('rejects generated audio outside the reference duration limit', async ({ page }) => {
    await page.route('**/tts', route => route.fulfill({ status: 200, contentType: 'audio/wav', body: pcmWav(0.5) }))
    await openDesign(page)
    await page.getByLabel('Voice instructions').fill('Warm narrator')
    await page.getByLabel('Sample text').fill('Hi.')
    await page.getByRole('button', { name: 'Generate reference', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText('Reference audio must be between 1 second and 2 minutes.')
    await expect(page.getByText('Normalized reference')).toBeHidden()
    await expect(page.getByRole('button', { name: 'Save voice', exact: true })).toBeDisabled()
  })

})
